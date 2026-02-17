package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/your-org/fd/internal/models"
	"github.com/your-org/fd/internal/queue"
	"github.com/your-org/fd/internal/storage"
	"github.com/your-org/fd/pkg/dto"
)

type StreamHandler struct {
	db       *storage.PostgresStore
	producer *queue.Producer
}

func NewStreamHandler(db *storage.PostgresStore, producer *queue.Producer) *StreamHandler {
	return &StreamHandler{db: db, producer: producer}
}

func (h *StreamHandler) Create(c *gin.Context) {
	var req dto.CreateStreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fps := req.FPS
	if fps <= 0 {
		fps = 5
	}

	st := &models.Stream{
		URL:          req.URL,
		StreamType:   models.StreamType(req.StreamType),
		Mode:         models.StreamMode(req.Mode),
		FPS:          fps,
		CollectionID: req.CollectionID,
		Config:       req.Config,
	}

	if err := h.db.CreateStream(c.Request.Context(), st); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, streamToResponse(st))
}

func (h *StreamHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stream id"})
		return
	}

	st, err := h.db.GetStream(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "stream not found"})
		return
	}

	c.JSON(http.StatusOK, streamToResponse(st))
}

func (h *StreamHandler) List(c *gin.Context) {
	streams, err := h.db.ListStreams(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]dto.StreamResponse, 0, len(streams))
	for _, st := range streams {
		resp = append(resp, streamToResponse(&st))
	}

	c.JSON(http.StatusOK, dto.StreamListResponse{Streams: resp, Total: len(resp)})
}

func (h *StreamHandler) Start(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stream id"})
		return
	}

	st, err := h.db.GetStream(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "stream not found"})
		return
	}

	if st.Status == models.StreamStatusRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "stream already running"})
		return
	}

	// Update status to starting
	if err := h.db.UpdateStreamStatus(c.Request.Context(), id, models.StreamStatusStarting, ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Publish start command to NATS for ingestor
	cmd := map[string]interface{}{
		"action":    "start",
		"stream_id": id.String(),
		"url":       st.URL,
		"type":      string(st.StreamType),
		"mode":      string(st.Mode),
		"fps":       st.FPS,
	}
	if st.CollectionID != nil {
		cmd["collection_id"] = st.CollectionID.String()
	}

	cmdData, _ := json.Marshal(cmd)
	if err := h.producer.PublishControl(cmdData); err != nil {
		_ = h.db.UpdateStreamStatus(c.Request.Context(), id, models.StreamStatusError, "failed to publish start command")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send start command"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "starting", "stream_id": id})
}

func (h *StreamHandler) Stop(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stream id"})
		return
	}

	st, err := h.db.GetStream(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "stream not found"})
		return
	}

	// Publish stop command
	cmd := map[string]interface{}{
		"action":    "stop",
		"stream_id": id.String(),
	}
	cmdData, _ := json.Marshal(cmd)
	_ = h.producer.PublishControl(cmdData)

	if err := h.db.UpdateStreamStatus(c.Request.Context(), id, models.StreamStatusStopped, ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "stopped", "stream_id": id})
}

func (h *StreamHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stream id"})
		return
	}

	// Stop stream first if running
	st, err := h.db.GetStream(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if st != nil && st.Status == models.StreamStatusRunning {
		cmd := map[string]interface{}{
			"action":    "stop",
			"stream_id": id.String(),
		}
		cmdData, _ := json.Marshal(cmd)
		_ = h.producer.PublishControl(cmdData)
	}

	if err := h.db.DeleteStream(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func streamToResponse(st *models.Stream) dto.StreamResponse {
	return dto.StreamResponse{
		ID:           st.ID,
		URL:          st.URL,
		StreamType:   string(st.StreamType),
		Mode:         string(st.Mode),
		FPS:          st.FPS,
		Status:       string(st.Status),
		CollectionID: st.CollectionID,
		Config:       st.Config,
		ErrorMessage: st.ErrorMessage,
		CreatedAt:    st.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    st.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
