package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestLogger provides structured slog logging for all HTTP interactions.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method

		if raw != "" {
			path = path + "?" + raw
		}

		lvl := slog.LevelInfo
		if statusCode >= 400 && statusCode < 500 {
			lvl = slog.LevelWarn
		} else if statusCode >= 500 {
			lvl = slog.LevelError
		}

		slog.Log(c.Request.Context(), lvl, "HTTP Request",
			"status", statusCode,
			"method", method,
			"path", path,
			"ip", clientIP,
			"latency_ms", latency.Milliseconds(),
			"user_agent", c.Request.UserAgent(),
		)
	}
}

