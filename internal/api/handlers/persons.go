package handlers

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/your-org/fd/internal/storage"
	"github.com/your-org/fd/pkg/dto"
)

type PersonHandler struct {
	db    *storage.PostgresStore
	minio *storage.MinIOStore
	// embedFn extracts a face embedding from image bytes.
	// Set this after vision pipeline is initialized.
	EmbedFn func(imageData []byte) ([]float32, float32, error)
}

func NewPersonHandler(db *storage.PostgresStore, minio *storage.MinIOStore) *PersonHandler {
	return &PersonHandler{db: db, minio: minio}
}

func (h *PersonHandler) Create(c *gin.Context) {
	var req dto.CreatePersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify collection exists
	col, err := h.db.GetCollection(c.Request.Context(), req.CollectionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	person, err := h.db.CreatePerson(c.Request.Context(), req.CollectionID, req.Name, req.Metadata)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, dto.PersonResponse{
		ID:           person.ID,
		CollectionID: person.CollectionID,
		Name:         person.Name,
		Metadata:     person.Metadata,
		FaceCount:    0,
		CreatedAt:    person.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (h *PersonHandler) List(c *gin.Context) {
	var collectionID *uuid.UUID
	if colStr := c.Query("collection_id"); colStr != "" {
		id, err := uuid.Parse(colStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid collection_id"})
			return
		}
		collectionID = &id
	}

	persons, err := h.db.ListPersons(c.Request.Context(), collectionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]dto.PersonResponse, 0, len(persons))
	for _, p := range persons {
		faceCount, _ := h.db.CountFaces(c.Request.Context(), p.ID)
		resp = append(resp, dto.PersonResponse{
			ID:           p.ID,
			CollectionID: p.CollectionID,
			Name:         p.Name,
			Metadata:     p.Metadata,
			FaceCount:    faceCount,
			CreatedAt:    p.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"persons": resp, "total": len(resp)})
}

func (h *PersonHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid person id"})
		return
	}

	person, err := h.db.GetPerson(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if person == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "person not found"})
		return
	}

	faceCount, _ := h.db.CountFaces(c.Request.Context(), id)

	c.JSON(http.StatusOK, dto.PersonResponse{
		ID:           person.ID,
		CollectionID: person.CollectionID,
		Name:         person.Name,
		Metadata:     person.Metadata,
		FaceCount:    faceCount,
		CreatedAt:    person.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// AddFace accepts a multipart image upload, extracts embedding, and stores it.
func (h *PersonHandler) AddFace(c *gin.Context) {
	personID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid person id"})
		return
	}

	// Verify person exists
	person, err := h.db.GetPerson(c.Request.Context(), personID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if person == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "person not found"})
		return
	}

	file, header, err := c.Request.FormFile("image")
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

	if h.EmbedFn == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vision pipeline not initialized"})
		return
	}

	embedding, quality, err := h.EmbedFn(imageData)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "failed to extract face: " + err.Error()})
		return
	}

	// Store source image in MinIO
	sourceKey := "faces/" + personID.String() + "/" + uuid.New().String() + "_" + header.Filename
	if err := h.minio.PutObject(c.Request.Context(), sourceKey, imageData, header.Header.Get("Content-Type")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "store image failed"})
		return
	}

	fe, err := h.db.AddFaceEmbedding(c.Request.Context(), personID, embedding, quality, sourceKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, dto.FaceEmbeddingResponse{
		ID:        fe.ID,
		PersonID:  fe.PersonID,
		Quality:   fe.Quality,
		SourceKey: fe.SourceKey,
		CreatedAt: fe.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (h *PersonHandler) DeleteFace(c *gin.Context) {
	personID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid person id"})
		return
	}
	faceID, err := uuid.Parse(c.Param("faceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid face id"})
		return
	}

	if err := h.db.DeleteFaceEmbedding(c.Request.Context(), personID, faceID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *PersonHandler) ListFaces(c *gin.Context) {
	personID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid person id"})
		return
	}

	faces, err := h.db.ListFaceEmbeddings(c.Request.Context(), personID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]dto.FaceEmbeddingResponse, 0, len(faces))
	for _, f := range faces {
		resp = append(resp, dto.FaceEmbeddingResponse{
			ID:        f.ID,
			PersonID:  f.PersonID,
			Quality:   f.Quality,
			SourceKey: f.SourceKey,
			CreatedAt: f.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"faces": resp, "total": len(resp)})
}

// Search performs a face similarity search by uploading an image.
func (h *PersonHandler) Search(c *gin.Context) {
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

	if h.EmbedFn == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vision pipeline not initialized"})
		return
	}

	embedding, _, err := h.EmbedFn(imageData)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "failed to extract face: " + err.Error()})
		return
	}

	var collectionID *uuid.UUID
	if colStr := c.PostForm("collection_id"); colStr != "" {
		if id, err := uuid.Parse(colStr); err == nil {
			collectionID = &id
		}
	}

	threshold := 0.4
	limit := 5

	matches, err := h.db.SearchFaces(c.Request.Context(), embedding, collectionID, threshold, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	results := make([]dto.SearchResult, 0, len(matches))
	for _, m := range matches {
		results = append(results, dto.SearchResult{
			PersonID: m.PersonID,
			Name:     m.Name,
			Score:    m.Score,
		})
	}

	c.JSON(http.StatusOK, gin.H{"results": results, "total": len(results)})
}
