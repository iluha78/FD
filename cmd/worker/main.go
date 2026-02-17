package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/your-org/fd/internal/config"
	"github.com/your-org/fd/internal/models"
	"github.com/your-org/fd/internal/observability"
	"github.com/your-org/fd/internal/queue"
	"github.com/your-org/fd/internal/storage"
	"github.com/your-org/fd/internal/vision"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	observability.SetupLogger(cfg.Logging.Level, cfg.Logging.Format)

	slog.Info("starting FD Vision Worker",
		"workers", cfg.Vision.WorkerCount,
		"cpu_cores", runtime.NumCPU(),
	)

	// Initialize ONNX Runtime
	ort.SetSharedLibraryPath(getONNXLibPath())
	if err := ort.InitializeEnvironment(); err != nil {
		slog.Error("init onnx runtime", "error", err)
		os.Exit(1)
	}
	defer ort.DestroyEnvironment()

	// Connect to Postgres
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

	// Connect to NATS
	producer, err := queue.NewProducer(cfg.NATS.URL)
	if err != nil {
		slog.Error("connect to nats producer", "error", err)
		os.Exit(1)
	}
	defer producer.Close()

	if err := producer.EnsureStreams(context.Background()); err != nil {
		slog.Warn("ensure nats streams", "error", err)
	}

	// Initialize vision pipeline
	pipeline, err := vision.NewPipeline(cfg.Vision, cfg.Tracking, db, minioStore, producer)
	if err != nil {
		slog.Error("init vision pipeline", "error", err)
		os.Exit(1)
	}
	defer pipeline.Close()

	slog.Info("vision pipeline initialized")

	// Create NATS consumer
	consumer, err := queue.NewConsumer(cfg.NATS.URL)
	if err != nil {
		slog.Error("create consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start consuming frame tasks
	err = consumer.ConsumeFrames(ctx, "vision-workers", func(ctx context.Context, msg jetstream.Msg) error {
		var task models.FrameTask
		if err := json.Unmarshal(msg.Data(), &task); err != nil {
			slog.Error("unmarshal frame task", "error", err)
			return nil // Don't retry on unmarshal errors
		}

		if err := pipeline.ProcessFrame(ctx, task); err != nil {
			return fmt.Errorf("process frame %s: %w", task.FrameID, err)
		}

		return nil
	}, cfg.Vision.WorkerCount)
	if err != nil {
		slog.Error("start frame consumer", "error", err)
		os.Exit(1)
	}

	// Metrics endpoint
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})
		slog.Info("worker metrics listening", "addr", ":8082")
		if err := http.ListenAndServe(":8082", mux); err != nil {
			slog.Error("metrics server error", "error", err)
		}
	}()

	// Periodically report queue depth
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				depth, err := producer.QueueDepth(ctx)
				if err == nil {
					observability.QueueDepth.Set(float64(depth))
				}
			}
		}
	}()

	// Wait for shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down worker...")
	cancel()
	time.Sleep(2 * time.Second)
	slog.Info("worker stopped")
}

// getONNXLibPath returns the ONNX Runtime shared library path
// based on the operating system.
func getONNXLibPath() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "linux":
		return "libonnxruntime.so"
	case "darwin":
		return "libonnxruntime.dylib"
	default:
		return "onnxruntime.dll"
	}
}
