package queue

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type MessageHandler func(ctx context.Context, msg jetstream.Msg) error

type Consumer struct {
	nc *nats.Conn
	js jetstream.JetStream
}

func NewConsumer(natsURL string) (*Consumer, error) {
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

	return &Consumer{nc: nc, js: js}, nil
}

// ConsumeFrames starts consuming frame tasks from the FRAMES stream.
// workerCount determines how many goroutines process messages concurrently.
func (c *Consumer) ConsumeFrames(ctx context.Context, consumerName string, handler MessageHandler, workerCount int) error {
	stream, err := c.js.Stream(ctx, FramesStreamName)
	if err != nil {
		return fmt.Errorf("get stream %s: %w", FramesStreamName, err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    3,
		FilterSubject: FramesSubjectBase + ".>",
	})
	if err != nil {
		return fmt.Errorf("create consumer %s: %w", consumerName, err)
	}

	msgCh := make(chan jetstream.Msg, workerCount*2)

	// Start consumer fetch loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(msgCh)
				return
			default:
			}

			batch, err := cons.Fetch(workerCount, jetstream.FetchMaxWait(5*time.Second))
			if err != nil {
				if ctx.Err() != nil {
					close(msgCh)
					return
				}
				slog.Warn("fetch frames error", "error", err)
				time.Sleep(time.Second)
				continue
			}

			for msg := range batch.Messages() {
				select {
				case msgCh <- msg:
				case <-ctx.Done():
					close(msgCh)
					return
				}
			}
		}
	}()

	// Start workers
	for i := 0; i < workerCount; i++ {
		go func(workerID int) {
			for msg := range msgCh {
				if err := handler(ctx, msg); err != nil {
					slog.Error("process frame error", "worker", workerID, "error", err, "subject", msg.Subject())
					_ = msg.Nak()
				} else {
					_ = msg.Ack()
				}
			}
		}(i)
	}

	slog.Info("frame consumer started", "consumer", consumerName, "workers", workerCount)
	return nil
}

// ConsumeEvents starts consuming detection events (for API to broadcast via WebSocket).
func (c *Consumer) ConsumeEvents(ctx context.Context, consumerName string, handler MessageHandler) error {
	stream, err := c.js.Stream(ctx, EventsStreamName)
	if err != nil {
		return fmt.Errorf("get stream %s: %w", EventsStreamName, err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       10 * time.Second,
		MaxDeliver:    3,
		FilterSubject: EventsSubjectBase + ".>",
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return fmt.Errorf("create consumer %s: %w", consumerName, err)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			batch, err := cons.Fetch(10, jetstream.FetchMaxWait(5*time.Second))
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(time.Second)
				continue
			}

			for msg := range batch.Messages() {
				if err := handler(ctx, msg); err != nil {
					slog.Error("process event error", "error", err)
					_ = msg.Nak()
				} else {
					_ = msg.Ack()
				}
			}
		}
	}()

	slog.Info("event consumer started", "consumer", consumerName)
	return nil
}

func (c *Consumer) Close() {
	c.nc.Close()
}
