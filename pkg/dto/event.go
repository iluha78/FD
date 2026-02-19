package dto

import "github.com/google/uuid"

type EventResponse struct {
	ID               uuid.UUID  `json:"id"`
	StreamID         uuid.UUID  `json:"stream_id"`
	TrackID          string     `json:"track_id"`
	Timestamp        string     `json:"timestamp"`
	Gender           string     `json:"gender"`
	GenderConfidence float32    `json:"gender_confidence"`
	Age              int        `json:"age"`
	AgeRange         string     `json:"age_range"`
	Confidence       float32    `json:"confidence"`
	MatchedPersonID  *uuid.UUID `json:"matched_person_id,omitempty"`
	MatchedName      string     `json:"matched_name,omitempty"`
	MatchScore       float32    `json:"match_score,omitempty"`
	SnapshotURL      string     `json:"snapshot_url,omitempty"`
	FrameURL         string     `json:"frame_url,omitempty"`
	CreatedAt        string     `json:"created_at"`
}

type EventListResponse struct {
	Events []EventResponse `json:"events"`
	Total  int             `json:"total"`
}

type EventQuery struct {
	StreamID string `form:"stream_id"`
	PersonID string `form:"person_id"`
	From     string `form:"from"`
	To       string `form:"to"`
	Unknown  *bool  `form:"unknown"`
	Limit    int    `form:"limit"`
	Offset   int    `form:"offset"`
}

// EventSearchResult is one result from POST /v1/search/events.
type EventSearchResult struct {
	EventID         uuid.UUID  `json:"event_id"`
	StreamID        uuid.UUID  `json:"stream_id"`
	Timestamp       string     `json:"timestamp"`
	Score           float32    `json:"score"`
	Gender          string     `json:"gender"`
	Age             int        `json:"age"`
	AgeRange        string     `json:"age_range"`
	MatchedPersonID *uuid.UUID `json:"matched_person_id,omitempty"`
	SnapshotURL     string     `json:"snapshot_url,omitempty"`
}

// WSEvent is a WebSocket message for real-time event delivery.
type WSEvent struct {
	Type     string        `json:"type"` // face_detected, face_recognized, stream_status
	StreamID uuid.UUID     `json:"stream_id"`
	Data     EventResponse `json:"data,omitempty"`
	Status   string        `json:"status,omitempty"`
}
