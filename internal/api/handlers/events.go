package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/your-org/fd/internal/storage"
	"github.com/your-org/fd/pkg/dto"
)

type EventHandler struct {
	db    *storage.PostgresStore
	minio *storage.MinIOStore
}

func NewEventHandler(db *storage.PostgresStore, minio *storage.MinIOStore) *EventHandler {
	return &EventHandler{db: db, minio: minio}
}

func (h *EventHandler) List(c *gin.Context) {
	streamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stream id"})
		return
	}

	var from, to *time.Time
	if fromStr := c.Query("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			from = &t
		}
	}
	if toStr := c.Query("to"); toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			to = &t
		}
	}

	var personID *uuid.UUID
	if pidStr := c.Query("person_id"); pidStr != "" {
		if id, err := uuid.Parse(pidStr); err == nil {
			personID = &id
		}
	}

	var unknown *bool
	if unknownStr := c.Query("unknown"); unknownStr != "" {
		b := unknownStr == "true" || unknownStr == "1"
		unknown = &b
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	events, total, err := h.db.QueryEvents(c.Request.Context(), streamID, from, to, personID, unknown, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]dto.EventResponse, 0, len(events))
	for _, ev := range events {
		r := dto.EventResponse{
			ID:               ev.ID,
			StreamID:         ev.StreamID,
			TrackID:          ev.TrackID,
			Timestamp:        ev.Timestamp.Format(time.RFC3339),
			Gender:           ev.Gender,
			GenderConfidence: ev.GenderConfidence,
			Age:              ev.Age,
			AgeRange:         ev.AgeRange,
			Confidence:       ev.Confidence,
			MatchedPersonID:  ev.MatchedPersonID,
			MatchScore:       ev.MatchScore,
			CreatedAt:        ev.CreatedAt.Format(time.RFC3339),
		}
		if ev.SnapshotKey != "" {
			r.SnapshotURL = "/v1/events/" + ev.ID.String() + "/snapshot"
		}
		resp = append(resp, r)
	}

	c.JSON(http.StatusOK, dto.EventListResponse{Events: resp, Total: total})
}

// Snapshot proxies the face snapshot image from MinIO.
func (h *EventHandler) Snapshot(c *gin.Context) {
	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	ev, err := h.db.GetEvent(c.Request.Context(), eventID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}

	if ev.SnapshotKey == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no snapshot for this event"})
		return
	}

	data, err := h.minio.GetObject(c.Request.Context(), ev.SnapshotKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
		return
	}

	c.Data(http.StatusOK, "image/jpeg", data)
}
