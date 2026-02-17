package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/your-org/fd/internal/models"
	"github.com/your-org/fd/internal/observability"
	"github.com/your-org/fd/internal/queue"
	"github.com/your-org/fd/internal/storage"
)

// StreamCommand represents a start/stop command from the API.
type StreamCommand struct {
	Action       string `json:"action"` // start, stop
	StreamID     string `json:"stream_id"`
	URL          string `json:"url"`
	Type         string `json:"type"`
	Mode         string `json:"mode"`
	FPS          int    `json:"fps"`
	CollectionID string `json:"collection_id,omitempty"`
}

type activeStream struct {
	cancel    context.CancelFunc
	extractor *FFmpegExtractor
}

// Manager manages video stream ingestion lifecycle.
type Manager struct {
	producer *queue.Producer
	minio    *storage.MinIOStore
	db       *storage.PostgresStore
	width    int

	mu      sync.RWMutex
	streams map[string]*activeStream
}

func NewManager(producer *queue.Producer, minio *storage.MinIOStore, db *storage.PostgresStore, frameWidth int) *Manager {
	return &Manager{
		producer: producer,
		minio:    minio,
		db:       db,
		width:    frameWidth,
		streams:  make(map[string]*activeStream),
	}
}

// HandleCommand processes a stream control command.
func (m *Manager) HandleCommand(ctx context.Context, cmd StreamCommand) error {
	switch cmd.Action {
	case "start":
		return m.startStream(ctx, cmd)
	case "stop":
		return m.stopStream(cmd.StreamID)
	default:
		return fmt.Errorf("unknown action: %s", cmd.Action)
	}
}

func (m *Manager) startStream(ctx context.Context, cmd StreamCommand) error {
	m.mu.Lock()
	if _, exists := m.streams[cmd.StreamID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("stream %s already running", cmd.StreamID)
	}
	m.mu.Unlock()

	streamURL := cmd.URL

	// Resolve YouTube URLs
	if cmd.Type == "youtube" {
		resolved, err := ResolveYouTubeURL(ctx, cmd.URL)
		if err != nil {
			m.updateStatus(cmd.StreamID, models.StreamStatusError, err.Error())
			return fmt.Errorf("resolve youtube url: %w", err)
		}
		streamURL = resolved
		slog.Info("resolved youtube url", "stream_id", cmd.StreamID)
	}

	fps := cmd.FPS
	if fps <= 0 {
		fps = 5
	}

	streamCtx, cancel := context.WithCancel(ctx)
	extractor := &FFmpegExtractor{}

	as := &activeStream{
		cancel:    cancel,
		extractor: extractor,
	}

	m.mu.Lock()
	m.streams[cmd.StreamID] = as
	m.mu.Unlock()

	observability.ActiveStreams.Inc()
	m.updateStatus(cmd.StreamID, models.StreamStatusRunning, "")

	slog.Info("starting stream ingestion", "stream_id", cmd.StreamID, "url", cmd.URL, "fps", fps)

	// Run extraction in a goroutine with retry logic
	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.streams, cmd.StreamID)
			m.mu.Unlock()
			observability.ActiveStreams.Dec()
			slog.Info("stream ingestion stopped", "stream_id", cmd.StreamID)
		}()

		const maxRetries = 3
		currentURL := streamURL

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				delay := time.Duration(1<<uint(attempt)) * time.Second // 2s, 4s, 8s
				slog.Warn("retrying stream extraction",
					"stream_id", cmd.StreamID,
					"attempt", attempt,
					"delay", delay,
				)
				select {
				case <-streamCtx.Done():
					m.updateStatus(cmd.StreamID, models.StreamStatusStopped, "")
					return
				case <-time.After(delay):
				}

				// Re-resolve YouTube URLs (they expire)
				if cmd.Type == "youtube" {
					resolved, err := ResolveYouTubeURL(streamCtx, cmd.URL)
					if err != nil {
						slog.Warn("youtube re-resolve failed", "stream_id", cmd.StreamID, "error", err)
						continue
					}
					currentURL = resolved
				}

				// Need a fresh extractor for retry
				extractor = &FFmpegExtractor{}
			}

			err := extractor.StartExtraction(streamCtx, currentURL, fps, m.width, func(frameData []byte) error {
				frameID := uuid.New()
				streamUUID, _ := uuid.Parse(cmd.StreamID)

				// Upload frame to MinIO
				key := fmt.Sprintf("frames/%s/%s.jpg", cmd.StreamID, frameID.String())
				if err := m.minio.PutObject(streamCtx, key, frameData, "image/jpeg"); err != nil {
					return fmt.Errorf("upload frame: %w", err)
				}

				// Publish frame task to NATS
				task := models.FrameTask{
					StreamID:  streamUUID,
					FrameID:   frameID,
					Timestamp: time.Now(),
					FrameRef:  key,
					Width:     m.width,
					Height:    0, // Will be determined by worker
				}

				if err := m.producer.PublishFrame(streamCtx, cmd.StreamID, task); err != nil {
					return fmt.Errorf("publish frame task: %w", err)
				}

				observability.FramesProcessed.WithLabelValues(cmd.StreamID).Inc()
				return nil
			})

			if err == nil || streamCtx.Err() != nil {
				// Clean exit or context cancelled (user stopped stream)
				m.updateStatus(cmd.StreamID, models.StreamStatusStopped, "")
				return
			}

			slog.Error("stream extraction failed",
				"stream_id", cmd.StreamID,
				"attempt", attempt,
				"error", err,
			)
		}

		// All retries exhausted
		m.updateStatus(cmd.StreamID, models.StreamStatusError, "stream failed after retries")
	}()

	return nil
}

func (m *Manager) stopStream(streamID string) error {
	m.mu.RLock()
	as, exists := m.streams[streamID]
	m.mu.RUnlock()

	if !exists {
		return nil // Already stopped
	}

	as.extractor.Stop()
	as.cancel()

	slog.Info("stop command sent", "stream_id", streamID)
	return nil
}

func (m *Manager) updateStatus(streamID string, status models.StreamStatus, errMsg string) {
	id, err := uuid.Parse(streamID)
	if err != nil {
		return
	}
	if err := m.db.UpdateStreamStatus(context.Background(), id, status, errMsg); err != nil {
		slog.Error("update stream status", "stream_id", streamID, "error", err)
	}
}

// ActiveCount returns the number of currently running streams.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.streams)
}

// StopAll stops all running streams.
func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.streams))
	for id := range m.streams {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		_ = m.stopStream(id)
	}
}

// ParseCommand parses a NATS message into a StreamCommand.
func ParseCommand(data []byte) (StreamCommand, error) {
	var cmd StreamCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return cmd, fmt.Errorf("parse command: %w", err)
	}
	return cmd, nil
}
