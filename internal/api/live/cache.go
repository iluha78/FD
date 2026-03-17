package live

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// FrameEntry holds metadata about the latest frame for a stream.
type FrameEntry struct {
	FrameKey  string
	Width     int
	Height    int
	UpdatedAt time.Time
}

// FrameCache stores the latest frame key per stream and notifies MJPEG subscribers.
// It is updated by the event consumer (when DetectionResults arrive with FrameKey).
type FrameCache struct {
	mu      sync.RWMutex
	entries map[uuid.UUID]*FrameEntry
	subs    map[uuid.UUID]map[chan string]struct{}
}

func NewFrameCache() *FrameCache {
	return &FrameCache{
		entries: make(map[uuid.UUID]*FrameEntry),
		subs:    make(map[uuid.UUID]map[chan string]struct{}),
	}
}

// Update sets the latest frame for a stream and notifies all MJPEG subscribers.
func (c *FrameCache) Update(streamID uuid.UUID, frameKey string, width, height int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[streamID] = &FrameEntry{
		FrameKey:  frameKey,
		Width:     width,
		Height:    height,
		UpdatedAt: time.Now(),
	}

	for ch := range c.subs[streamID] {
		select {
		case ch <- frameKey:
		default: // skip slow subscriber
		}
	}
}

// Subscribe registers a channel to receive frame keys for a stream.
// Returns the channel; the current frame key (if any) is sent immediately.
func (c *FrameCache) Subscribe(streamID uuid.UUID) chan string {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch := make(chan string, 4)
	if c.subs[streamID] == nil {
		c.subs[streamID] = make(map[chan string]struct{})
	}
	c.subs[streamID][ch] = struct{}{}

	// Send current frame key immediately so the first viewer frame loads fast.
	if entry, ok := c.entries[streamID]; ok {
		select {
		case ch <- entry.FrameKey:
		default:
		}
	}

	return ch
}

// Unsubscribe removes a subscriber channel for a stream.
func (c *FrameCache) Unsubscribe(streamID uuid.UUID, ch chan string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if subs, ok := c.subs[streamID]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(c.subs, streamID)
		}
	}
}

// Latest returns the current frame entry for a stream, or nil.
func (c *FrameCache) Latest(streamID uuid.UUID) *FrameEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.entries[streamID]
}
