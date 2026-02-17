package dto

import (
	"encoding/json"

	"github.com/google/uuid"
)

type CreateCollectionRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type CollectionResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   string    `json:"created_at"`
}

type CreatePersonRequest struct {
	CollectionID uuid.UUID       `json:"collection_id" binding:"required"`
	Name         string          `json:"name" binding:"required"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

type PersonResponse struct {
	ID           uuid.UUID       `json:"id"`
	CollectionID uuid.UUID       `json:"collection_id"`
	Name         string          `json:"name"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	FaceCount    int             `json:"face_count"`
	CreatedAt    string          `json:"created_at"`
}

type FaceEmbeddingResponse struct {
	ID        uuid.UUID `json:"id"`
	PersonID  uuid.UUID `json:"person_id"`
	Quality   float32   `json:"quality"`
	SourceKey string    `json:"source_key"`
	CreatedAt string    `json:"created_at"`
}

type SearchRequest struct {
	CollectionID *uuid.UUID `json:"collection_id,omitempty"`
	Threshold    float64    `json:"threshold"`
	Limit        int        `json:"limit"`
}

type SearchResult struct {
	PersonID uuid.UUID `json:"person_id"`
	Name     string    `json:"name"`
	Score    float32   `json:"score"`
}
