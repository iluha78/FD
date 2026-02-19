package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/your-org/fd/internal/config"
	"github.com/your-org/fd/internal/ingest"
	"github.com/your-org/fd/internal/observability"
	"github.com/your-org/fd/internal/queue"
	"github.com/your-org/fd/internal/storage"
)

func cleanupFrames(ctx context.Context, db *storage.PostgresStore, minio *storage.MinIOStore, retention int) {
	streams, err := db.ListStreams(ctx)
	if err != nil {
		slog.Warn("cleanup: list streams", "error", err)
		return
	}
	for _, s := range streams {
		prefix := fmt.Sprintf("frames/%s/", s.ID.String())
		keys, err := minio.ListObjects(ctx, prefix)
		if err != nil {
			slog.Warn("cleanup: list objects", "prefix", prefix, "error", err)
			continue
		}
		if len(keys) <= retention {
			continue
		}
		toDelete := keys[:len(keys)-retention]
		if err := minio.DeleteObjects(ctx, toDelete); err != nil {
			slog.Warn("cleanup: delete objects", "prefix", prefix, "error", err)
			continue
		}
		slog.Info("cleanup: deleted old frames", "stream_id", s.ID, "deleted", len(toDelete), "remaining", retention)
	}
}

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	observability.SetupLogger(cfg.Logging.Level, cfg.Logging.Format)
	slog.Info("starting FD Ingestor service")

	// Connect to Postgres (for status updates)
	db, err := storage.NewPostgresStore(cfg.Database)
	if err != nil {
		slog.Error("connect to postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Connect to MinIO
	minioStore, err := storage.NewMinIOStore(cfg.MinIO)
	if err != nil {
		slog.Error("connect to minio", "error", err)
		os.Exit(1)
	}
	if err := minioStore.EnsureBucket(context.Background()); err != nil {
		slog.Warn("ensure minio bucket", "error", err)
	}

	// Connect to NATS
	producer, err := queue.NewProducer(cfg.NATS.URL)
	if err != nil {
		slog.Error("connect to nats", "error", err)
		os.Exit(1)
	}
	defer producer.Close()

	if err := producer.EnsureStreams(context.Background()); err != nil {
		slog.Warn("ensure nats streams", "error", err)
	}

	// Create stream manager
	manager := ingest.NewManager(producer, minioStore, db, cfg.Vision.FrameWidth)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe to control commands via NATS (raw subject, not JetStream)
	nc, err := nats.Connect(cfg.NATS.URL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		slog.Error("connect to nats for control", "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	// Subscribe to stream control commands
	_, err = nc.Subscribe("stream.control", func(msg *nats.Msg) {
		cmd, err := ingest.ParseCommand(msg.Data)
		if err != nil {
			slog.Error("parse command", "error", err)
			return
		}

		slog.Info("received command", "action", cmd.Action, "stream_id", cmd.StreamID)
		if err := manager.HandleCommand(ctx, cmd); err != nil {
			slog.Error("handle command", "error", err, "action", cmd.Action, "stream_id", cmd.StreamID)
		}
	})
	if err != nil {
		slog.Error("subscribe to control", "error", err)
		os.Exit(1)
	}

	// Also listen for control commands via FRAMES JetStream stream
	consumer, err := queue.NewConsumer(cfg.NATS.URL)
	if err != nil {
		slog.Error("create consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	// Frame cleanup goroutine
	if cfg.Storage.FrameRetention > 0 {
		slog.Info("frame cleanup enabled", "retention", cfg.Storage.FrameRetention)
		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cleanupFrames(ctx, db, minioStore, cfg.Storage.FrameRetention)
				}
			}
		}()
	}

	// Metrics endpoint
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})
		slog.Info("ingestor metrics listening", "addr", ":8081")
		if err := http.ListenAndServe(":8081", mux); err != nil {
			slog.Error("metrics server error", "error", err)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down ingestor...")
	cancel()
	manager.StopAll()

	// Give streams time to stop
	time.Sleep(2 * time.Second)
	slog.Info("ingestor stopped")
}
