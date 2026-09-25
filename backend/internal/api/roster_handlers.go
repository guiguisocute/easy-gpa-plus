package api

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"easygpa/backend/internal/events"
)

type whitelistInput struct {
	SID  string `json:"sid"`
	Name string `json:"name"`
	Role string `json:"role"`
	// Gender is optional and only ever reaches the 学院 report. An empty value
	// means the roster did not carry the column, which is different from "this
	// student has no gender on file" only in that we decline to guess either.
	Gender string `json:"gender"`
}

func validBusinessRole(role string) bool {
	return role == "student" || role == "group" || role == "class_admin"
}

func normalizeWhitelistInput(input whitelistInput) (whitelistInput, error) {
	input.SID = strings.TrimSpace(input.SID)
	input.Name = strings.TrimSpace(input.Name)
	input.Role = strings.TrimSpace(input.Role)
	input.Gender = strings.TrimSpace(input.Gender)
	if input.Role == "" {
		input.Role = "student"
	}
	if input.SID == "" || input.Name == "" || len(input.SID) > 64 || len(input.Name) > 100 || !validBusinessRole(input.Role) {
		return whitelistInput{}, errors.New("学号、姓名或角色不正确")
	}
	if input.Gender != "" && input.Gender != "男" && input.Gender != "女" {
		return whitelistInput{}, errors.New("性别只能是男或女")
	}
	return input, nil
}

func (s *Server) whitelist(c *gin.Context) {
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT w.id,w.sid,w.name,w.role,w.active,w.registered_at,w.created_at,COALESCE(w.gender,'')
		  FROM whitelist w ORDER BY w.sid
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var sid, name, role, gender string
		var active bool
		var registeredAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &sid, &name, &role, &active, &registeredAt, &createdAt, &gender); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "sid": sid, "name": name, "role": role, "active": active, "registered": registeredAt != nil, "registeredAt": registeredAt, "createdAt": createdAt, "gender": gender})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "whitelist.list", "whitelist", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) addWhitelist(c *gin.Context) {
	var raw struct {
		SID  string `json:"sid"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&raw); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "白名单参数不正确", nil)
		return
	}
	// 单人新建固定为学生。需要任命综测小组或班管时，必须在账号注册后走独立的
	// 角色变更接口，让升权校验、会话撤销和审计保持在同一条路径上。
	input := whitelistInput{SID: raw.SID, Name: raw.Name, Role: "student"}
	input, err := normalizeWhitelistInput(input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "whitelist_invalid", err.Error(), nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var id int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO whitelist (class_id,sid,name,role) VALUES ($1,$2,$3,$4) RETURNING id
	`, actor.ClassID, input.SID, input.Name, input.Role).Scan(&id)
	if uniqueViolation(err) {
		writeError(c, http.StatusConflict, "sid_exists", "该学号已存在于系统白名单", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "whitelist.added", "whitelist", strconv.FormatInt(id, 10), nil, input, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10)})
}

func (s *Server) importWhitelist(c *gin.Context) {
	var content string
	if strings.HasPrefix(c.GetHeader("Content-Type"), "text/csv") {
		reader := io.LimitReader(c.Request.Body, 1<<20)
		raw, err := io.ReadAll(reader)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_csv", "CSV 无法读取", nil)
			return
		}
		content = string(raw)
	} else {
		var input struct {
			CSV string `json:"csv"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "请提供 CSV 内容", nil)
			return
		}
		content = input.CSV
	}
	if len(content) > 1<<20 {
		writeError(c, http.StatusRequestEntityTooLarge, "csv_too_large", "CSV 不能超过 1 MB", nil)
		return
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(content, "\ufeff"))).ReadAll()
	if err != nil || len(records) < 2 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_csv", "CSV 至少需要表头和一行数据", nil)
		return
	}
	headers := make(map[string]int)
	for i, header := range records[0] {
		headers[strings.ToLower(strings.TrimSpace(header))] = i
	}
	sidIndex, sidOK := firstHeader(headers, "sid", "学号")
	nameIndex, nameOK := firstHeader(headers, "name", "姓名")
	roleIndex, _ := firstHeader(headers, "role", "角色")
	genderIndex, _ := firstHeader(headers, "gender", "性别")
	if !sidOK || !nameOK {
		writeError(c, http.StatusUnprocessableEntity, "invalid_csv_header", "CSV 表头必须包含 sid/学号 与 name/姓名", nil)
		return
	}
	inputs := make([]whitelistInput, 0, len(records)-1)
	seen := make(map[string]bool)
	for line, record := range records[1:] {
		if sidIndex >= len(record) || nameIndex >= len(record) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_csv_row", "CSV 行字段不足", gin.H{"line": line + 2})
			return
		}
		role := "student"
		if roleIndex < len(record) && roleIndex >= 0 && strings.TrimSpace(record[roleIndex]) != "" {
			role = strings.TrimSpace(record[roleIndex])
		}
		gender := ""
		if genderIndex >= 0 && genderIndex < len(record) {
			gender = strings.TrimSpace(record[genderIndex])
		}
		input, err := normalizeWhitelistInput(whitelistInput{SID: record[sidIndex], Name: record[nameIndex], Role: role, Gender: gender})
		if err != nil || seen[input.SID] {
			writeError(c, http.StatusUnprocessableEntity, "invalid_csv_row", "CSV 中存在无效或重复学号", gin.H{"line": line + 2})
			return
		}
		seen[input.SID] = true
		inputs = append(inputs, input)
	}
	tx := mustTx(c)
	actor := mustActor(c)
	genders := 0
	for _, input := range inputs {
		_, err := tx.Exec(c.Request.Context(), `
			INSERT INTO whitelist (class_id,sid,name,role,gender) VALUES ($1,$2,$3,$4,NULLIF($5,''))
			ON CONFLICT (class_id,sid) DO UPDATE
			   SET name=EXCLUDED.name,role=EXCLUDED.role,active=true,updated_at=now()
			 WHERE whitelist.registered_at IS NULL
		`, actor.ClassID, input.SID, input.Name, input.Role, input.Gender)
		if uniqueViolation(err) {
			writeError(c, http.StatusConflict, "sid_exists", "某个学号已属于另一班级", gin.H{"sid": input.SID})
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		// 性别不参与"谁能注册"的判定，所以它的写入不受注册状态限制。上面那条
		// 语句的 WHERE 是在保护学号、姓名和角色不被从已注册账号底下改掉；补一
		// 列报表用的性别不属于要防的事，而等到要出学院报表时全班早就注册完了，
		// 卡在同一个 WHERE 上就等于永远导不进去。空值不覆盖已有值。
		if input.Gender != "" {
			if _, err := tx.Exec(c.Request.Context(), `
				UPDATE whitelist SET gender=$3,updated_at=now() WHERE class_id=$1 AND sid=$2
			`, actor.ClassID, input.SID, input.Gender); err != nil {
				writeServiceError(c, err)
				return
			}
			genders++
		}
	}
	if err := appendAudit(c, tx, "whitelist.imported", "whitelist", "", nil, nil, map[string]any{"count": len(inputs), "genders": genders}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"imported": len(inputs), "genders": genders})
}

func firstHeader(headers map[string]int, names ...string) (int, bool) {
	for _, name := range names {
		if index, ok := headers[name]; ok {
			return index, true
		}
	}
	return -1, false
}

func (s *Server) deleteWhitelist(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var sid, name, role string
	err := tx.QueryRow(c.Request.Context(), `DELETE FROM whitelist WHERE id=$1 AND registered_at IS NULL RETURNING sid,name,role`, id).Scan(&sid, &name, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusConflict, "not_removable", "白名单不存在或账号已经注册", nil)
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		writeError(c, http.StatusConflict, "member_has_records", "该成员已有综测记录，不能移出名单", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "whitelist.deleted", "whitelist", strconv.FormatInt(id, 10), map[string]any{"sid": sid, "name": name, "role": role}, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) adminUsers(c *gin.Context) {
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT u.id,u.sid,u.name,u.role,u.status,u.last_login_at,u.created_at,u.is_deputy,
		       e.email,EXISTS (SELECT 1 FROM seal s WHERE s.student_id=u.id AND s.unsealed_at IS NULL),
		       CASE WHEN EXISTS (SELECT 1 FROM seal s WHERE s.student_id=u.id AND s.unsealed_at IS NULL)
		            THEN COALESCE((SELECT CASE WHEN b.status='complete' THEN 'complete' WHEN b.status='blocked' THEN 'blocked' ELSE 'sealed_unreviewed' END
		                             FROM scorecard_audit_batch b WHERE b.student_id=u.id AND b.scheme_id=(SELECT id FROM scheme WHERE status='published' ORDER BY version DESC,id DESC LIMIT 1) ORDER BY b.created_at DESC LIMIT 1),'sealed_unreviewed')
		            ELSE 'unsealed' END,
		       CASE WHEN NOT EXISTS (SELECT 1 FROM seal s WHERE s.student_id=u.id AND s.unsealed_at IS NULL) THEN ''
		            ELSE COALESCE((SELECT CASE
		              WHEN b.status='blocked' THEN COALESCE(NULLIF(b.detail->>'blockedReason',''),NULLIF(b.error_message,''),'可用审核人不足')
		              WHEN b.status='open' THEN CONCAT((SELECT count(*) FROM scorecard_audit_assignment a JOIN scorecard_audit_subject subject ON subject.id=a.subject_id WHERE subject.batch_id=b.id AND a.status='submitted'),'/2 已提交')
		              WHEN b.status='resolving' THEN '等班级管理员把问题处理完'
		              WHEN b.status='complete' THEN ''
		              ELSE '等待前置事项完成或自动补建' END
		              FROM scorecard_audit_batch b WHERE b.student_id=u.id AND b.scheme_id=(SELECT id FROM scheme WHERE status='published' ORDER BY version DESC,id DESC LIMIT 1) ORDER BY b.created_at DESC LIMIT 1),'等待前置事项完成或自动补建') END
		  FROM app_user u
		  LEFT JOIN user_email e ON e.user_id=u.id AND e.is_primary AND e.verified_at IS NOT NULL
		 WHERE u.password_hash IS NOT NULL
		 ORDER BY u.sid
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var sid, name, role, status string
		var lastLogin *time.Time
		var created time.Time
		var email *string
		var sealed, isDeputy bool
		var auditStatus, auditBlocker string
		if err := rows.Scan(&id, &sid, &name, &role, &status, &lastLogin, &created, &isDeputy, &email, &sealed, &auditStatus, &auditBlocker); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "sid": sid, "name": name, "role": role, "isDeputy": isDeputy, "status": status, "primaryEmail": email, "sealed": sealed, "auditStatus": auditStatus, "auditBlocker": auditBlocker, "lastLoginAt": lastLogin, "createdAt": created})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "user.list", "user", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type passwordResetTarget struct {
	UserID      int64
	WhitelistID int64
	SID         string
	Name        string
	Role        string
	Status      string
	Registered  bool
	EmailCount  int64
}

func (s *Server) resetUserPassword(c *gin.Context) {
	userID, ok := pathID(c, "id")
	if !ok {
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	var target passwordResetTarget
	err := tx.QueryRow(c.Request.Context(), `
		SELECT u.id,w.id,u.sid,u.name,u.role,u.status,
		       u.password_hash IS NOT NULL,
		       (SELECT count(*) FROM user_email e WHERE e.user_id=u.id)
		  FROM app_user u
		  JOIN whitelist w ON w.id=u.whitelist_id AND w.class_id=u.class_id
		 WHERE u.id=$1 AND u.class_id=$2
		 FOR UPDATE OF u,w
	`, userID, actor.ClassID).Scan(
		&target.UserID, &target.WhitelistID, &target.SID, &target.Name, &target.Role,
		&target.Status, &target.Registered, &target.EmailCount,
	)
	if notFound(c, err, "用户") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if target.Role != "student" && target.Role != "group" {
		writeError(c, http.StatusConflict, "member_account_required", "只能重置学生或综测小组成员的密码", nil)
		return
	}
	if !target.Registered {
		writeError(c, http.StatusConflict, "account_unregistered", "该成员尚未设置密码，无需重置", nil)
		return
	}
	if target.Status != "active" {
		writeError(c, http.StatusConflict, "account_disabled", "请先启用该账号，再重置密码", nil)
		return
	}
	if err := resetUserPasswords(c.Request.Context(), tx, actor.ClassID, []passwordResetTarget{target}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAdminPasswordResetAudit(c, tx, target, false); err != nil {
		writeServiceError(c, err)
		return
	}
	afterCommit(c, func(ctx context.Context) error {
		return s.deps.Auth.RevokeOtherSessions(ctx, target.UserID, "")
	})
	writePasswordResetResult(c, 1)
}

func (s *Server) resetClassUserPasswords(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	// The cached frontend's bulk confirmation names students only; retain that
	// scope until it reloads and uses the new password-reset endpoint.
	legacyStudentScope := strings.HasSuffix(c.FullPath(), "/initialize")
	rows, err := tx.Query(c.Request.Context(), `
		SELECT u.id,w.id,u.sid,u.name,u.role,u.status,
		       u.password_hash IS NOT NULL,
		       (SELECT count(*) FROM user_email e WHERE e.user_id=u.id)
		  FROM app_user u
		  JOIN whitelist w ON w.id=u.whitelist_id AND w.class_id=u.class_id
		 WHERE u.class_id=$1 AND (u.role='student' OR (u.role='group' AND NOT $2))
		   AND u.status='active' AND u.password_hash IS NOT NULL
		 ORDER BY u.id
		 FOR UPDATE OF u,w
	`, actor.ClassID, legacyStudentScope)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	targets := make([]passwordResetTarget, 0)
	for rows.Next() {
		var target passwordResetTarget
		if err := rows.Scan(
			&target.UserID, &target.WhitelistID, &target.SID, &target.Name, &target.Role,
			&target.Status, &target.Registered, &target.EmailCount,
		); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		targets = append(targets, target)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if len(targets) == 0 {
		writePasswordResetResult(c, 0)
		return
	}
	if err := resetUserPasswords(c.Request.Context(), tx, actor.ClassID, targets); err != nil {
		writeServiceError(c, err)
		return
	}
	userIDs := make([]int64, 0, len(targets))
	for _, target := range targets {
		if err := appendAdminPasswordResetAudit(c, tx, target, true); err != nil {
			writeServiceError(c, err)
			return
		}
		userIDs = append(userIDs, target.UserID)
	}
	afterCommit(c, func(ctx context.Context) error {
		var result error
		for _, id := range userIDs {
			result = errors.Join(result, s.deps.Auth.RevokeOtherSessions(ctx, id, ""))
		}
		return result
	})
	writePasswordResetResult(c, len(targets))
}

func writePasswordResetResult(c *gin.Context, count int) {
	// Keep the previous response shape for a cached frontend during deployment.
	if strings.HasSuffix(c.FullPath(), "/initialize") {
		c.JSON(http.StatusOK, gin.H{"initialized": count})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reset": count})
}

func resetUserPasswords(ctx context.Context, tx pgx.Tx, classID int64, targets []passwordResetTarget) error {
	if len(targets) == 0 {
		return nil
	}
	userIDs := make([]int64, 0, len(targets))
	whitelistIDs := make([]int64, 0, len(targets))
	for _, target := range targets {
		userIDs = append(userIDs, target.UserID)
		whitelistIDs = append(whitelistIDs, target.WhitelistID)
	}
	command, err := tx.Exec(ctx, `
		UPDATE app_user u
		   SET password_hash=NULL,
		       token_version=u.token_version+1,
		       updated_at=now()
		  FROM whitelist w
		 WHERE u.id=ANY($1::bigint[])
		   AND u.class_id=$2
		   AND u.role IN ('student','group')
		   AND u.status='active' AND u.password_hash IS NOT NULL
		   AND w.id=u.whitelist_id
		   AND w.class_id=u.class_id
	`, userIDs, classID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != int64(len(targets)) {
		return fmt.Errorf("reset user passwords: reset %d of %d users", command.RowsAffected(), len(targets))
	}
	command, err = tx.Exec(ctx, `
		UPDATE whitelist
		   SET registered_at=NULL,updated_at=now()
		 WHERE id=ANY($1::bigint[]) AND class_id=$2
	`, whitelistIDs, classID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != int64(len(targets)) {
		return fmt.Errorf("reset user passwords: reset %d of %d roster entries", command.RowsAffected(), len(targets))
	}
	return nil
}

func appendAdminPasswordResetAudit(c *gin.Context, tx pgx.Tx, target passwordResetTarget, bulk bool) error {
	return appendAudit(
		c,
		tx,
		"user.password_reset_by_admin",
		"user",
		strconv.FormatInt(target.UserID, 10),
		map[string]any{"registered": true, "role": target.Role, "status": target.Status, "emailCount": target.EmailCount},
		map[string]any{"registered": false, "role": target.Role, "status": target.Status, "emailCount": target.EmailCount},
		map[string]any{"sid": target.SID, "name": target.Name, "bulk": bulk},
	)
}

func (s *Server) updateUserRole(c *gin.Context) {
	userID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || !validBusinessRole(input.Role) {
		writeError(c, http.StatusBadRequest, "invalid_role", "角色不正确", nil)
		return
	}
	tx := mustTx(c)
	var beforeRole string
	var whitelistID int64
	if err := tx.QueryRow(c.Request.Context(), `SELECT role,whitelist_id FROM app_user WHERE id=$1 FOR UPDATE`, userID).Scan(&beforeRole, &whitelistID); err != nil {
		if notFound(c, err, "用户") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if beforeRole == "class_admin" && input.Role != "class_admin" {
		var admins int
		if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM app_user WHERE role='class_admin' AND status='active'`).Scan(&admins); err != nil {
			writeServiceError(c, err)
			return
		}
		if admins <= 1 {
			writeError(c, http.StatusConflict, "last_admin", "不能降级最后一名有效管理员", nil)
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE app_user SET role=$1,token_version=token_version+1,updated_at=now() WHERE id=$2`, input.Role, userID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE whitelist SET role=$1,updated_at=now() WHERE id=$2`, input.Role, whitelistID); err != nil {
		writeServiceError(c, err)
		return
	}
	if beforeRole == "group" || beforeRole == "class_admin" {
		if input.Role == "student" {
			if _, err := reassignUnavailableSubmissionReviewer(c.Request.Context(), tx, actorClassID(c), userID); err != nil {
				writeServiceError(c, err)
				return
			}
		}
	}
	if err := appendAudit(c, tx, "user.role_changed", "user", strconv.FormatInt(userID, 10), map[string]any{"role": beforeRole}, map[string]any{"role": input.Role}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	afterCommit(c, func(ctx context.Context) error { return s.deps.Auth.RevokeOtherSessions(ctx, userID, "") })
	c.Status(http.StatusNoContent)
}

func (s *Server) updateUserStatus(c *gin.Context) {
	userID, ok := pathID(c, "id")
	if !ok {
		return
	}
	actor := mustActor(c)
	if userID == actor.UserID {
		writeError(c, http.StatusConflict, "self_disable", "不能停用当前登录账号", nil)
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || (input.Status != "active" && input.Status != "disabled") {
		writeError(c, http.StatusBadRequest, "invalid_status", "账号状态不正确", nil)
		return
	}
	tx := mustTx(c)
	var beforeStatus, role string
	if err := tx.QueryRow(c.Request.Context(), `SELECT status,role FROM app_user WHERE id=$1 FOR UPDATE`, userID).Scan(&beforeStatus, &role); err != nil {
		if notFound(c, err, "用户") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if role == "class_admin" && input.Status == "disabled" {
		var admins int
		if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM app_user WHERE role='class_admin' AND status='active'`).Scan(&admins); err != nil {
			writeServiceError(c, err)
			return
		}
		if admins <= 1 {
			writeError(c, http.StatusConflict, "last_admin", "不能停用最后一名有效管理员", nil)
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE app_user SET status=$1,token_version=token_version+1,updated_at=now() WHERE id=$2`, input.Status, userID); err != nil {
		writeServiceError(c, err)
		return
	}
	if input.Status == "disabled" {
		if _, err := reassignUnavailableSubmissionReviewer(c.Request.Context(), tx, actor.ClassID, userID); err != nil {
			writeServiceError(c, err)
			return
		}
		if _, err := tx.Exec(c.Request.Context(), `
			WITH stale AS (
				UPDATE scorecard_audit_batch
				   SET status='stale',stale_at=now(),stale_reason='班级成员停用',
				       invalidated_at=now(),invalidated_reason='班级成员停用'
				 WHERE student_id=$1 AND status IN ('generating','blocked','open','resolving')
				 RETURNING class_id,id
			)
			INSERT INTO outbox_event (class_id,type,payload)
			SELECT class_id,$2,jsonb_build_object('batchId',id::text,'studentId',$1,'reason','班级成员停用') FROM stale
		`, userID, events.ScorecardAuditStale); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "user.status_changed", "user", strconv.FormatInt(userID, 10), map[string]any{"status": beforeStatus}, map[string]any{"status": input.Status}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	afterCommit(c, func(ctx context.Context) error { return s.deps.Auth.RevokeOtherSessions(ctx, userID, "") })
	c.Status(http.StatusNoContent)
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
