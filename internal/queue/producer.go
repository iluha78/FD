package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	FramesStreamName  = "FRAMES"
	FramesSubjectBase = "frames"
	EventsStreamName  = "EVENTS"
	EventsSubjectBase = "events"
)

type Producer struct {
	nc *nats.Conn
	js jetstream.JetStream
}

func NewProducer(natsURL string) (*Producer, error) {
	nc, err := nats.Connect(natsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to nats: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("create jetstream context: %w", err)
	}

	return &Producer{nc: nc, js: js}, nil
}

// EnsureStreams creates JetStream streams if they don't exist.
// Retries up to 30 times (1s apart) to handle NATS startup delay.
func (p *Producer) EnsureStreams(ctx context.Context) error {
	streams := []jetstream.StreamConfig{
		{
			Name:        FramesStreamName,
			Subjects:    []string{FramesSubjectBase + ".>"},
			Retention:   jetstream.WorkQueuePolicy,
			MaxAge:      5 * time.Minute,
			MaxMsgs:     100000,
			MaxBytes:    1 * 1024 * 1024 * 1024, // 1GB
			Storage:     jetstream.FileStorage,
			Discard:     jetstream.DiscardOld,
			Duplicates:  30 * time.Second,
			Description: "Frame tasks for vision workers",
		},
		{
			Name:        EventsStreamName,
			Subjects:    []string{EventsSubjectBase + ".>"},
			Retention:   jetstream.InterestPolicy,
			MaxAge:      24 * time.Hour,
			MaxMsgs:     1000000,
			Storage:     jetstream.FileStorage,
			Description: "Detection/recognition events",
		},
	}

	const maxAttempts = 30
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		allOK := true
		for _, cfg := range streams {
			opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := p.js.CreateOrUpdateStream(opCtx, cfg)
			cancel()
			if err != nil {
				allOK = false
				if attempt == maxAttempts {
					return fmt.Errorf("create stream %s: %w (after %d attempts)", cfg.Name, err, maxAttempts)
				}
				slog.Warn("ensure NATS stream (retrying...)", "name", cfg.Name, "attempt", attempt, "error", err)
				break
			}
			slog.Info("ensured NATS stream", "name", cfg.Name)
		}
		if allOK {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return nil
}

// PublishFrame publishes a frame task to NATS.
func (p *Producer) PublishFrame(ctx context.Context, streamID string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal frame task: %w", err)
	}

	subject := fmt.Sprintf("%s.%s", FramesSubjectBase, streamID)
	_, err = p.js.Publish(ctx, subject, payload)
	if err != nil {
		return fmt.Errorf("publish frame: %w", err)
	}
	return nil
}

// PublishEvent publishes a detection event to NATS.
func (p *Producer) PublishEvent(ctx context.Context, streamID string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	subject := fmt.Sprintf("%s.%s", EventsSubjectBase, streamID)
	_, err = p.js.Publish(ctx, subject, payload)
	if err != nil {
		return fmt.Errorf("publish event: %w", err)
	}
	return nil
}

// QueueDepth returns the number of pending messages in the FRAMES stream.
func (p *Producer) QueueDepth(ctx context.Context) (uint64, error) {
	stream, err := p.js.Stream(ctx, FramesStreamName)
	if err != nil {
		return 0, err
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return 0, err
	}
	return info.State.Msgs, nil
}

// PublishControl publishes a control command via raw NATS (not JetStream).
// Ingestor subscribes to "stream.control" subject for start/stop commands.
func (p *Producer) PublishControl(data []byte) error {
	return p.nc.Publish("stream.control", data)
}

func (p *Producer) Ping() error {
	if !p.nc.IsConnected() {
		return fmt.Errorf("nats not connected")
	}
	return nil
}

func (p *Producer) Close() {
	p.nc.Close()
}
