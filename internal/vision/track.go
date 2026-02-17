package vision

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Track represents a tracked face across frames.
type Track struct {
	ID              string
	BBox            [4]float32
	Confidence      float32
	Age             int           // frames since creation
	Hits            int           // number of consecutive detections
	TimeSinceUpdate int           // frames since last detection match
	Embedding       []float32     // last known embedding
	LastRecognized  time.Time     // last time recognition was run
	PersonID        string        // matched person ID, if any
	MatchScore      float32       // match score
	Gender          string
	GenderConf      float32
	FaceAge         int
	AgeRange        string
}

// Tracker implements a simple SORT-like face tracker.
type Tracker struct {
	mu       sync.Mutex
	tracks   map[string]*Track
	nextID   int
	maxAge   int // max frames without detection before track is removed
	minHits  int // min hits before track is confirmed
	streamID string
}

// NewTracker creates a new face tracker for a given stream.
func NewTracker(streamID string, maxAge, minHits int) *Tracker {
	return &Tracker{
		tracks:   make(map[string]*Track),
		maxAge:   maxAge,
		minHits:  minHits,
		streamID: streamID,
	}
}

// Update matches detections to existing tracks and creates new tracks.
// Returns a list of (track, isNew) pairs for detections that should be processed further.
func (t *Tracker) Update(detections []Detection) []TrackUpdate {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Increment age for all tracks
	for _, track := range t.tracks {
		track.TimeSinceUpdate++
	}

	updates := make([]TrackUpdate, 0, len(detections))

	// Simple IoU-based matching
	trackList := make([]*Track, 0, len(t.tracks))
	for _, tr := range t.tracks {
		trackList = append(trackList, tr)
	}

	matched := make(map[string]bool)
	detMatched := make(map[int]bool)

	// Match detections to tracks by IoU
	for di, det := range detections {
		bestIoU := float32(0.3) // min IoU threshold
		bestTrack := ""

		for _, tr := range trackList {
			if matched[tr.ID] {
				continue
			}
			iouVal := iou(det.BBox, tr.BBox)
			if iouVal > bestIoU {
				bestIoU = iouVal
				bestTrack = tr.ID
			}
		}

		if bestTrack != "" {
			// Update existing track
			tr := t.tracks[bestTrack]
			tr.BBox = det.BBox
			tr.Confidence = det.Confidence
			tr.Hits++
			tr.TimeSinceUpdate = 0
			matched[bestTrack] = true
			detMatched[di] = true

			updates = append(updates, TrackUpdate{
				Track: tr,
				IsNew: false,
			})
		}
	}

	// Create new tracks for unmatched detections
	for di, det := range detections {
		if detMatched[di] {
			continue
		}

		t.nextID++
		trackID := fmt.Sprintf("%s_%d", t.streamID, t.nextID)
		tr := &Track{
			ID:              trackID,
			BBox:            det.BBox,
			Confidence:      det.Confidence,
			Hits:            1,
			TimeSinceUpdate: 0,
		}
		t.tracks[trackID] = tr

		updates = append(updates, TrackUpdate{
			Track: tr,
			IsNew: true,
		})
	}

	// Remove stale tracks
	for id, tr := range t.tracks {
		if tr.TimeSinceUpdate > t.maxAge {
			delete(t.tracks, id)
		}
	}

	return updates
}

// ShouldRecognize returns true if recognition should be run for this track.
func (t *Tracker) ShouldRecognize(track *Track, interval time.Duration) bool {
	if track.Hits < t.minHits {
		return false
	}
	if track.Embedding == nil {
		return true // Never recognized
	}
	return time.Since(track.LastRecognized) >= interval
}

// TrackCount returns the number of active tracks.
func (t *Tracker) TrackCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.tracks)
}

type TrackUpdate struct {
	Track *Track
	IsNew bool
}

// CosineSimilarity computes cosine similarity between two normalized vectors.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return float32(math.Min(1.0, math.Max(-1.0, dot)))
}
