package handlers

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/your-org/fd/internal/storage"
	"github.com/your-org/fd/pkg/dto"
)

type EventHandler struct {
	db      *storage.PostgresStore
	minio   *storage.MinIOStore
	EmbedFn func(imageData []byte) ([]float32, float32, error)
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
		if ev.FrameKey != "" {
			r.FrameURL = "/v1/events/" + ev.ID.String() + "/frame"
		}
		resp = append(resp, r)
	}

	c.JSON(http.StatusOK, dto.EventListResponse{Events: resp, Total: total})
}

// SearchEvents finds past detection events visually similar to a uploaded face photo.
// Optional query params: stream_id, threshold (default 0.4), limit (default 10).
func (h *EventHandler) SearchEvents(c *gin.Context) {
	if h.EmbedFn == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vision pipeline not initialized"})
		return
	}

	file, _, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image file required"})
		return
	}
	defer file.Close()

	imageData, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read image failed"})
		return
	}

	embedding, _, err := h.EmbedFn(imageData)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "failed to extract face: " + err.Error()})
		return
	}

	var streamID *uuid.UUID
	if sidStr := c.Query("stream_id"); sidStr != "" {
		if id, err := uuid.Parse(sidStr); err == nil {
			streamID = &id
		}
	}

	threshold := 0.4
	if tStr := c.Query("threshold"); tStr != "" {
		if t, err := strconv.ParseFloat(tStr, 64); err == nil && t > 0 {
			threshold = t
		}
	}

	limit := 10
	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	matches, err := h.db.SearchEvents(c.Request.Context(), embedding, streamID, threshold, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	results := make([]dto.EventSearchResult, 0, len(matches))
	for _, m := range matches {
		r := dto.EventSearchResult{
			EventID:         m.EventID,
			StreamID:        m.StreamID,
			Timestamp:       m.Timestamp.Format(time.RFC3339),
			Score:           m.Score,
			Gender:          m.Gender,
			Age:             m.Age,
			AgeRange:        m.AgeRange,
			MatchedPersonID: m.MatchedPersonID,
		}
		if m.SnapshotKey != "" {
			r.SnapshotURL = "/v1/events/" + m.EventID.String() + "/snapshot"
		}
		results = append(results, r)
	}

	c.JSON(http.StatusOK, gin.H{"results": results, "total": len(results)})
}

// SimilarByTrack finds events with faces similar to a given track_id.
// Required query params: stream_id, track_id.
// Optional: threshold (default 0.4), limit (default 10).
func (h *EventHandler) SimilarByTrack(c *gin.Context) {
	streamID, err := uuid.Parse(c.Query("stream_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stream_id required"})
		return
	}

	trackID := c.Query("track_id")
	if trackID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "track_id required"})
		return
	}

	threshold := 0.4
	if tStr := c.Query("threshold"); tStr != "" {
		if t, err := strconv.ParseFloat(tStr, 64); err == nil && t > 0 {
			threshold = t
		}
	}

	limit := 10
	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Get embedding from the best event of this track
	embedding, err := h.db.GetEmbeddingByTrackID(c.Request.Context(), streamID, trackID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if embedding == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no events found for this track_id"})
		return
	}

	// Search similar events across all streams (or pass nil for no stream filter)
	matches, err := h.db.SearchEvents(c.Request.Context(), embedding, nil, threshold, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	results := make([]dto.EventSearchResult, 0, len(matches))
	for _, m := range matches {
		r := dto.EventSearchResult{
			EventID:         m.EventID,
			StreamID:        m.StreamID,
			Timestamp:       m.Timestamp.Format(time.RFC3339),
			Score:           m.Score,
			Gender:          m.Gender,
			Age:             m.Age,
			AgeRange:        m.AgeRange,
			MatchedPersonID: m.MatchedPersonID,
		}
		if m.SnapshotKey != "" {
			r.SnapshotURL = "/v1/events/" + m.EventID.String() + "/snapshot"
		}
		results = append(results, r)
	}

	c.JSON(http.StatusOK, gin.H{"results": results, "total": len(results)})
}

// Frame proxies the full source frame image from MinIO.
func (h *EventHandler) Frame(c *gin.Context) {
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

	if ev.FrameKey == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no frame for this event"})
		return
	}

	data, err := h.minio.GetObject(c.Request.Context(), ev.FrameKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "frame not found"})
		return
	}

	c.Data(http.StatusOK, "image/jpeg", data)
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
