package models

import (
	"time"

	"github.com/google/uuid"
)

type Event struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	StreamID         uuid.UUID  `json:"stream_id" db:"stream_id"`
	TrackID          string     `json:"track_id" db:"track_id"`
	Timestamp        time.Time  `json:"timestamp" db:"timestamp"`
	Gender           string     `json:"gender" db:"gender"`
	GenderConfidence float32    `json:"gender_confidence" db:"gender_confidence"`
	Age              int        `json:"age" db:"age"`
	AgeRange         string     `json:"age_range" db:"age_range"`
	Confidence       float32    `json:"confidence" db:"confidence"`
	Embedding        []float32  `json:"-" db:"embedding"`
	BBox             [4]float32 `json:"bbox" db:"bbox"`                           // x1, y1, x2, y2 in pixels
	FrameWidth       int        `json:"frame_width" db:"frame_width"`
	FrameHeight      int        `json:"frame_height" db:"frame_height"`
	MatchedPersonID  *uuid.UUID `json:"matched_person_id,omitempty" db:"matched_person_id"`
	MatchScore       float32    `json:"match_score,omitempty" db:"match_score"`
	MatchedName      string     `json:"matched_name,omitempty" db:"matched_name"` // populated via JOIN
	SnapshotKey      string     `json:"snapshot_key" db:"snapshot_key"`
	FrameKey         string     `json:"frame_key" db:"frame_key"` // MinIO key of the full frame
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
}

// FrameTask is the message published to NATS for worker processing.
type FrameTask struct {
	StreamID     uuid.UUID  `json:"stream_id"`
	FrameID      uuid.UUID  `json:"frame_id"`
	Timestamp    time.Time  `json:"timestamp"`
	FrameRef     string     `json:"frame_ref"` // MinIO object key
	Width        int        `json:"width"`
	Height       int        `json:"height"`
	CollectionID *uuid.UUID `json:"collection_id,omitempty"` // stream's collection for scoped search
}

// DetectionResult is the output from a vision worker for one face.
type DetectionResult struct {
	StreamID         uuid.UUID  `json:"stream_id"`
	TrackID          string     `json:"track_id"`
	Timestamp        time.Time  `json:"timestamp"`
	BBox             [4]float32 `json:"bbox"` // x1, y1, x2, y2 in pixels
	FrameWidth       int        `json:"frame_width"`
	FrameHeight      int        `json:"frame_height"`
	Gender           string     `json:"gender"`
	GenderConfidence float32    `json:"gender_confidence"`
	Age              int        `json:"age"`
	AgeRange         string     `json:"age_range"`
	Confidence       float32    `json:"confidence"`
	Embedding        []float32  `json:"embedding"`
	MatchedPersonID  *uuid.UUID `json:"matched_person_id,omitempty"`
	MatchScore       float32    `json:"match_score,omitempty"`
	SnapshotKey      string     `json:"snapshot_key"`
	FrameKey         string     `json:"frame_key"` // MinIO key of the full frame
}
