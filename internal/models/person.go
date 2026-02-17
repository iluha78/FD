package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Person struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	CollectionID uuid.UUID       `json:"collection_id" db:"collection_id"`
	Name         string          `json:"name" db:"name"`
	Metadata     json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

type FaceEmbedding struct {
	ID        uuid.UUID `json:"id" db:"id"`
	PersonID  uuid.UUID `json:"person_id" db:"person_id"`
	Embedding []float32 `json:"embedding" db:"embedding"`
	Quality   float32   `json:"quality" db:"quality"`
	SourceKey string    `json:"source_key" db:"source_key"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
