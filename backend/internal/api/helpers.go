package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

func pathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, http.StatusBadRequest, "invalid_id", "资源 ID 不正确", nil)
		return 0, false
	}
	return id, true
}

func notFound(c *gin.Context, err error, resource string) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusNotFound, "not_found", resource+"不存在", nil)
		return true
	}
	return false
}
