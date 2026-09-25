package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/classtimeline"
	"easygpa/backend/internal/store"
)

func validAcademicYear(value string) bool {
	if len(value) != 9 || value[4] != '-' {
		return false
	}
	start, err := strconv.Atoi(value[:4])
	end, endErr := strconv.Atoi(value[5:])
	return err == nil && endErr == nil && start >= 2000 && start < 9999 && end == start+1
}

// importReportIdentity supplements an existing class from an authoritative
// roster. It cannot create members or change names, roles, activation or login
// credentials. Every supplied student must match both SID and name.
func (s *Server) importReportIdentity(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		CollegeName     string           `json:"collegeName"`
		EnrollmentClass string           `json:"enrollmentClass"`
		AcademicYear    string           `json:"academicYear"`
		Reason          string           `json:"reason"`
		Members         []whitelistInput `json:"members"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "报表资料格式不正确", nil)
		return
	}
	input.CollegeName = strings.TrimSpace(input.CollegeName)
	input.EnrollmentClass = strings.TrimSpace(input.EnrollmentClass)
	input.AcademicYear = strings.TrimSpace(input.AcademicYear)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.CollegeName == "" || len(input.CollegeName) > 100 || input.EnrollmentClass == "" || len(input.EnrollmentClass) > 100 || !validAcademicYear(input.AcademicYear) || input.Reason == "" || len(input.Reason) > 1000 || len(input.Members) > 1000 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_request", "请提供学院、教务班级名、连续两年的评定学年与导入原因，单次最多 1000 人", nil)
		return
	}
	seen := make(map[string]bool, len(input.Members))
	for i, member := range input.Members {
		normalized, err := normalizeWhitelistInput(member)
		if err != nil || normalized.Gender == "" || seen[normalized.SID] {
			writeError(c, http.StatusUnprocessableEntity, "invalid_member", "学生资料存在无效或重复项", gin.H{"line": i + 1})
			return
		}
		seen[normalized.SID] = true
		input.Members[i] = normalized
	}
	ctx := c.Request.Context()
	tx, err := store.BeginTenant(ctx, s.deps.Pools.App, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(ctx)
	timeline, err := classtimeline.Load(ctx, tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	timeline.CollegeName, timeline.EnrollmentClass, timeline.AcademicYear = input.CollegeName, input.EnrollmentClass, input.AcademicYear
	for i, member := range input.Members {
		tag, err := tx.Exec(ctx, `UPDATE whitelist SET gender=$4,updated_at=now() WHERE class_id=$1 AND sid=$2 AND name=$3`, id, member.SID, member.Name, member.Gender)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if tag.RowsAffected() != 1 {
			writeError(c, http.StatusConflict, "member_mismatch", "学号与姓名未精确匹配本班名单，整批未写入", gin.H{"line": i + 1})
			return
		}
	}
	if err := classtimeline.Save(ctx, tx, id, timeline); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(ctx, c, "tenant.report_identity_imported", "tenant", strconv.FormatInt(id, 10), gin.H{
		"count": len(input.Members), "collegeName": input.CollegeName, "enrollmentClass": input.EnrollmentClass,
		"academicYear": input.AcademicYear, "reason": input.Reason,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"imported": len(input.Members)})
}
