package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type StreamType string

const (
	StreamTypeRTSP    StreamType = "rtsp"
	StreamTypeYouTube StreamType = "youtube"
	StreamTypeHTTP    StreamType = "http"
)

type StreamMode string

const (
	StreamModeAll      StreamMode = "all"
	StreamModeIdentify StreamMode = "identify"
)

type StreamStatus string

const (
	StreamStatusStopped  StreamStatus = "stopped"
	StreamStatusStarting StreamStatus = "starting"
	StreamStatusRunning  StreamStatus = "running"
	StreamStatusError    StreamStatus = "error"
)

type Stream struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	URL          string          `json:"url" db:"url"`
	StreamType   StreamType      `json:"stream_type" db:"stream_type"`
	Mode         StreamMode      `json:"mode" db:"mode"`
	FPS          int             `json:"fps" db:"fps"`
	Status       StreamStatus    `json:"status" db:"status"`
	CollectionID *uuid.UUID      `json:"collection_id,omitempty" db:"collection_id"`
	Config       json.RawMessage `json:"config" db:"config"`
	ErrorMessage string          `json:"error_message,omitempty" db:"error_message"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}
