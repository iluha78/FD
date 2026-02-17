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
	ort "github.com/yalue/onnxruntime_go"

	"github.com/your-org/fd/internal/api"
	"github.com/your-org/fd/internal/api/ws"
	"github.com/your-org/fd/internal/config"
	"github.com/your-org/fd/internal/models"
	"github.com/your-org/fd/internal/observability"
	"github.com/your-org/fd/internal/queue"
	"github.com/your-org/fd/internal/storage"
	"github.com/your-org/fd/internal/vision"
	"github.com/your-org/fd/pkg/dto"
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

	slog.Info("starting FD API service", "port", cfg.Server.Port)

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

	// WebSocket hub
	hub := ws.NewHub()
	go hub.Run()

	// Start event consumer to broadcast events via WebSocket
	consumer, err := queue.NewConsumer(cfg.NATS.URL)
	if err != nil {
		slog.Error("create event consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = consumer.ConsumeEvents(ctx, "api-events", func(ctx context.Context, msg jetstream.Msg) error {
		var result models.DetectionResult
		if err := json.Unmarshal(msg.Data(), &result); err != nil {
			return err
		}

		// Store event in DB
		event := &models.Event{
			StreamID:         result.StreamID,
			TrackID:          result.TrackID,
			Timestamp:        result.Timestamp,
			Gender:           result.Gender,
			GenderConfidence: result.GenderConfidence,
			Age:              result.Age,
			AgeRange:         result.AgeRange,
			Confidence:       result.Confidence,
			Embedding:        result.Embedding,
			MatchedPersonID:  result.MatchedPersonID,
			MatchScore:       result.MatchScore,
			SnapshotKey:      result.SnapshotKey,
		}
		if err := db.CreateEvent(ctx, event); err != nil {
			slog.Error("store event", "error", err)
		}

		// Broadcast via WebSocket
		evtType := "face_detected"
		if result.MatchedPersonID != nil {
			evtType = "face_recognized"
		}

		hub.BroadcastEvent(&dto.WSEvent{
			Type:     evtType,
			StreamID: result.StreamID,
			Data: dto.EventResponse{
				ID:               event.ID,
				StreamID:         event.StreamID,
				TrackID:          event.TrackID,
				Timestamp:        event.Timestamp.Format(time.RFC3339),
				Gender:           event.Gender,
				GenderConfidence: event.GenderConfidence,
				Age:              event.Age,
				AgeRange:         event.AgeRange,
				Confidence:       event.Confidence,
				MatchedPersonID:  event.MatchedPersonID,
				MatchScore:       event.MatchScore,
				SnapshotURL:      "/v1/events/" + event.ID.String() + "/snapshot",
				CreatedAt:        event.CreatedAt.Format(time.RFC3339),
			},
		})

		return nil
	})
	if err != nil {
		slog.Warn("start event consumer", "error", err)
	}

	// Initialize ONNX Runtime for face embedding (AddFace / Search endpoints)
	var embedFn func([]byte) ([]float32, float32, error)

	ort.SetSharedLibraryPath(getONNXLibPath())
	if err := ort.InitializeEnvironment(); err != nil {
		slog.Warn("onnx runtime init failed — AddFace/Search will be unavailable", "error", err)
	} else {
		pipeline, err := vision.NewPipeline(cfg.Vision, cfg.Tracking, db, minioStore, producer)
		if err != nil {
			slog.Warn("vision pipeline init failed — AddFace/Search will be unavailable", "error", err)
		} else {
			embedFn = pipeline.EmbedImage
			defer pipeline.Close()
			defer ort.DestroyEnvironment()
			slog.Info("vision pipeline ready for API (AddFace/Search)")
		}
	}

	// Setup router
	router := api.NewRouter(api.RouterConfig{
		APIKey:   cfg.Server.APIKey,
		DB:       db,
		MinIO:    minioStore,
		Producer: producer,
		Hub:      hub,
		EmbedFn:  embedFn,
	})

	// Start HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("API server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down API server...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	slog.Info("API server stopped")
}

// getONNXLibPath returns the ONNX Runtime shared library path.
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
