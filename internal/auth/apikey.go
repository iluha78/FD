package auth

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

const headerName = "X-API-Key"

// APIKeyMiddleware validates the API key from X-API-Key header or ?api_key= query param.
// Query param support is required for browser WebSocket and MJPEG <img> tag usage.
// If apiKey is empty, authentication is disabled.
func APIKeyMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey == "" {
			c.Next()
			return
		}

		provided := c.GetHeader(headerName)
		if provided == "" {
			provided = c.Query("api_key")
		}
		if provided == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing API key",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(provided), []byte(apiKey)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "invalid API key",
			})
			return
		}

		c.Next()
	}
}
