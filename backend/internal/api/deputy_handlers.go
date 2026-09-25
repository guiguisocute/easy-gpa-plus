package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// requireDeputy checks the appointment inside the request transaction. Holding
// the member row prevents a concurrent revocation from racing a final decision.
func requireDeputy() gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := mustActor(c)
		if actor.Role != "group" || !actor.IsDeputy {
			writeError(c, http.StatusForbidden, "deputy_required", "此页面仅限班管任命的副班管使用", nil)
			return
		}
		var appointed bool
		err := mustTx(c).QueryRow(c.Request.Context(), `
			SELECT is_deputy AND role='group' AND status='active'
			  FROM app_user WHERE id=$1 AND class_id=$2 FOR SHARE
		`, actor.UserID, actor.ClassID).Scan(&appointed)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !appointed) {
			writeError(c, http.StatusForbidden, "deputy_required", "副班管任命已撤销，请刷新页面", nil)
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		c.Next()
	}
}

func (s *Server) classDeputy(c *gin.Context) {
	var id int64
	var sid, name string
	var registered bool
	err := mustTx(c).QueryRow(c.Request.Context(), `
		SELECT id,sid,name,password_hash IS NOT NULL FROM app_user
		 WHERE class_id=$1 AND is_deputy
	`, mustActor(c).ClassID).Scan(&id, &sid, &name, &registered)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, gin.H{"deputy": nil})
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deputy": gin.H{"id": strconv.FormatInt(id, 10), "sid": sid, "name": name, "registered": registered}})
}

func (s *Server) updateClassDeputy(c *gin.Context) {
	var input struct {
		UserID json.RawMessage `json:"userId"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.UserID) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "请选择副班管，或明确撤销任命", nil)
		return
	}
	var userID *jsonID
	if err := json.Unmarshal(input.UserID, &userID); err != nil || (userID != nil && *userID <= 0) {
		writeError(c, http.StatusBadRequest, "invalid_id", "成员 ID 不正确", nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	// All appointments in a class serialize before replacing its current deputy.
	var classID int64
	if err := tx.QueryRow(c.Request.Context(), `SELECT id FROM class WHERE id=$1 FOR NO KEY UPDATE`, actor.ClassID).Scan(&classID); err != nil {
		writeServiceError(c, err)
		return
	}
	var beforeID *int64
	var currentID int64
	err := tx.QueryRow(c.Request.Context(), `SELECT id FROM app_user WHERE class_id=$1 AND is_deputy FOR UPDATE`, actor.ClassID).Scan(&currentID)
	if err == nil {
		beforeID = &currentID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	if userID != nil {
		var eligible bool
		err := tx.QueryRow(c.Request.Context(), `
			SELECT role='group' AND status='active' AND password_hash IS NOT NULL
			  FROM app_user WHERE id=$1 AND class_id=$2 FOR UPDATE
		`, int64(*userID), actor.ClassID).Scan(&eligible)
		if notFound(c, err, "班级成员") {
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if !eligible {
			writeError(c, http.StatusConflict, "deputy_ineligible", "只能任命已注册且已启用的综测小组成员为副班管", nil)
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE app_user SET is_deputy=false,updated_at=now() WHERE class_id=$1 AND is_deputy`, actor.ClassID); err != nil {
		writeServiceError(c, err)
		return
	}
	var afterID *int64
	if userID != nil {
		id := int64(*userID)
		afterID = &id
		if _, err := tx.Exec(c.Request.Context(), `UPDATE app_user SET is_deputy=true,updated_at=now() WHERE id=$1 AND class_id=$2`, id, actor.ClassID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "class.deputy_changed", "class", strconv.FormatInt(actor.ClassID, 10),
		map[string]any{"userId": beforeID}, map[string]any{"userId": afterID}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
