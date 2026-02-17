package api

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/your-org/fd/internal/observability"
)

// LoggingMiddleware logs each request with slog.
func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()

		slog.Info("request",
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"duration", duration.String(),
			"ip", c.ClientIP(),
		)

		observability.HTTPRequestDuration.WithLabelValues(
			c.Request.Method,
			path,
			fmt.Sprintf("%d", status),
		).Observe(duration.Seconds())
	}
}
