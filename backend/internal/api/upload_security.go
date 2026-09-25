package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) discardInvalidUpload(c *gin.Context, objectKey string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	if err := s.deps.Objects.Remove(ctx, objectKey); err != nil {
		slog.Warn("remove invalid upload", "error", err, "request_id", c.Writer.Header().Get("X-Request-ID"))
	}
}
