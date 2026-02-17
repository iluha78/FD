package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/your-org/fd/internal/queue"
	"github.com/your-org/fd/internal/storage"
)

type SystemHandler struct {
	db       *storage.PostgresStore
	minio    *storage.MinIOStore
	producer *queue.Producer
}

func NewSystemHandler(db *storage.PostgresStore, minio *storage.MinIOStore, producer *queue.Producer) *SystemHandler {
	return &SystemHandler{db: db, minio: minio, producer: producer}
}

func (h *SystemHandler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *SystemHandler) Readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]string{}
	healthy := true

	// Check Postgres
	if err := h.db.Ping(ctx); err != nil {
		checks["postgres"] = err.Error()
		healthy = false
	} else {
		checks["postgres"] = "ok"
	}

	// Check MinIO
	if err := h.minio.Ping(ctx); err != nil {
		checks["minio"] = err.Error()
		healthy = false
	} else {
		checks["minio"] = "ok"
	}

	// Check NATS
	if err := h.producer.Ping(); err != nil {
		checks["nats"] = err.Error()
		healthy = false
	} else {
		checks["nats"] = "ok"
	}

	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}

	c.JSON(status, gin.H{
		"status": map[bool]string{true: "ready", false: "not ready"}[healthy],
		"checks": checks,
	})
}
