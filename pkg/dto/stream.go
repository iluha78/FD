package dto

import (
	"encoding/json"

	"github.com/google/uuid"
)

type CreateStreamRequest struct {
	URL          string          `json:"url" binding:"required"`
	StreamType   string          `json:"stream_type" binding:"required,oneof=rtsp youtube http"`
	Mode         string          `json:"mode" binding:"required,oneof=all identify"`
	FPS          int             `json:"fps"`
	CollectionID *uuid.UUID      `json:"collection_id,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
}

type StreamResponse struct {
	ID           uuid.UUID       `json:"id"`
	URL          string          `json:"url"`
	StreamType   string          `json:"stream_type"`
	Mode         string          `json:"mode"`
	FPS          int             `json:"fps"`
	Status       string          `json:"status"`
	CollectionID *uuid.UUID      `json:"collection_id,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

type StreamListResponse struct {
	Streams []StreamResponse `json:"streams"`
	Total   int              `json:"total"`
}
