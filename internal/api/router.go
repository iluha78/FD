package api

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/your-org/fd/internal/api/handlers"
	"github.com/your-org/fd/internal/api/ws"
	"github.com/your-org/fd/internal/auth"
	"github.com/your-org/fd/internal/queue"
	"github.com/your-org/fd/internal/storage"
)

type RouterConfig struct {
	APIKey   string
	DB       *storage.PostgresStore
	MinIO    *storage.MinIOStore
	Producer *queue.Producer
	Hub      *ws.Hub
	// EmbedFn extracts a face embedding from image bytes (from vision pipeline).
	EmbedFn func(imageData []byte) ([]float32, float32, error)
}

func NewRouter(cfg RouterConfig) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(LoggingMiddleware())
	r.Use(cors.Default())

	// System endpoints (no auth)
	systemH := handlers.NewSystemHandler(cfg.DB, cfg.MinIO, cfg.Producer)
	r.GET("/healthz", systemH.Healthz)
	r.GET("/readyz", systemH.Readyz)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// API v1 (with auth)
	v1 := r.Group("/v1")
	v1.Use(auth.APIKeyMiddleware(cfg.APIKey))

	// WebSocket
	v1.GET("/ws", cfg.Hub.HandleWS)

	// Collections
	colH := handlers.NewCollectionHandler(cfg.DB)
	v1.POST("/collections", colH.Create)
	v1.GET("/collections", colH.List)

	// Persons & Faces
	personH := handlers.NewPersonHandler(cfg.DB, cfg.MinIO)
	personH.EmbedFn = cfg.EmbedFn
	v1.POST("/persons", personH.Create)
	v1.GET("/persons", personH.List)
	v1.GET("/persons/:id", personH.Get)
	v1.POST("/persons/:id/faces", personH.AddFace)
	v1.GET("/persons/:id/faces", personH.ListFaces)
	v1.DELETE("/persons/:id/faces/:faceId", personH.DeleteFace)
	v1.POST("/search", personH.Search)

	// Streams
	streamH := handlers.NewStreamHandler(cfg.DB, cfg.Producer)
	v1.POST("/streams", streamH.Create)
	v1.GET("/streams", streamH.List)
	v1.GET("/streams/:id", streamH.Get)
	v1.POST("/streams/:id/start", streamH.Start)
	v1.POST("/streams/:id/stop", streamH.Stop)
	v1.DELETE("/streams/:id", streamH.Delete)

	// Events
	eventH := handlers.NewEventHandler(cfg.DB, cfg.MinIO)
	eventH.EmbedFn = cfg.EmbedFn
	v1.GET("/streams/:id/events", eventH.List)
	v1.GET("/events/:id/snapshot", eventH.Snapshot)
	v1.GET("/events/:id/frame", eventH.Frame)
	v1.GET("/events/similar", eventH.SimilarByTrack)
	v1.POST("/search/events", eventH.SearchEvents)

	return r
}
