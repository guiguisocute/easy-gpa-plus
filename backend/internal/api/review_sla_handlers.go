package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (s *Server) reviewSLA(c *gin.Context) {
	tx := mustTx(c)
	actor := mustActor(c)
	var itemHours int
	err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO review_sla_config (class_id) VALUES ($1)
		 ON CONFLICT (class_id) DO UPDATE SET class_id=EXCLUDED.class_id
		 RETURNING item_hours
	`, actor.ClassID).Scan(&itemHours)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"itemHours": itemHours})
}

func (s *Server) updateReviewSLA(c *gin.Context) {
	var input struct {
		ItemHours int `json:"itemHours"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.ItemHours < 1 || input.ItemHours > 168 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_sla", "审核 SLA 必须在 1—168 小时之间", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO review_sla_config (class_id,item_hours) VALUES ($1,$2)
		 ON CONFLICT (class_id) DO UPDATE SET item_hours=EXCLUDED.item_hours,updated_at=now()
	`, actor.ClassID, input.ItemHours); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "review_sla.updated", "class", strconv.FormatInt(actor.ClassID, 10), nil, gin.H{"itemHours": input.ItemHours}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"itemHours": input.ItemHours})
}
