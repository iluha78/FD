package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/your-org/fd/internal/storage"
	"github.com/your-org/fd/pkg/dto"
)

type CollectionHandler struct {
	db *storage.PostgresStore
}

func NewCollectionHandler(db *storage.PostgresStore) *CollectionHandler {
	return &CollectionHandler{db: db}
}

func (h *CollectionHandler) Create(c *gin.Context) {
	var req dto.CreateCollectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	col, err := h.db.CreateCollection(c.Request.Context(), req.Name, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, dto.CollectionResponse{
		ID:          col.ID,
		Name:        col.Name,
		Description: col.Description,
		CreatedAt:   col.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (h *CollectionHandler) List(c *gin.Context) {
	cols, err := h.db.ListCollections(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]dto.CollectionResponse, 0, len(cols))
	for _, col := range cols {
		resp = append(resp, dto.CollectionResponse{
			ID:          col.ID,
			Name:        col.Name,
			Description: col.Description,
			CreatedAt:   col.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"collections": resp, "total": len(resp)})
}
