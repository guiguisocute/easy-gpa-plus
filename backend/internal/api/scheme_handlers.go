package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/classtimeline"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/scheme"
)

type storedScheme struct {
	ID      int64
	Version int
	Name    string
	Config  scheme.Config
	Raw     []byte
	// PublishedAt is what students are shown instead of the version number.
	// 「v7」tells them nothing; 「最后更新 2026-08-20」tells them whether the
	// rules moved since they last read them. The version still exists — every
	// submission snapshots it and settlement reports it — it just is not the
	// thing to put in front of a student.
	PublishedAt *time.Time
	// CollegeName and EnrollmentClass ride beside Config rather than inside it.
	// scheme.Config carries scoring rules only; these two are report identity
	// for the 学院 forms, and folding them in would undo exactly the separation
	// 000043 established.
	CollegeName     string
	EnrollmentClass string
	AcademicYear    string
}

// currentSchemeResponse embeds the stored config so the payload keeps its
// existing shape, and adds the publish timestamp alongside it. PublishedAt is
// deliberately not a field on scheme.Config: that struct is what gets written
// back to JSONB, and a timestamp injected on read has no business being
// persisted there.
type currentSchemeResponse struct {
	scheme.Config
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

func loadCurrentScheme(ctx context.Context, tx pgx.Tx) (storedScheme, error) {
	row, err := readCurrentScheme(ctx, tx)
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	if err := ensureDefaultSelfReportScheme(ctx, tx); err != nil {
		return storedScheme{}, err
	}
	return readCurrentScheme(ctx, tx)
}

func readCurrentScheme(ctx context.Context, tx pgx.Tx) (storedScheme, error) {
	var row storedScheme
	var classID int64
	err := tx.QueryRow(ctx, `
		SELECT id,class_id,version,name,config,COALESCE(published_at,updated_at)
		  FROM scheme
		 WHERE status='published'
		 ORDER BY version DESC
		 LIMIT 1
	`).Scan(&row.ID, &classID, &row.Version, &row.Name, &row.Raw, &row.PublishedAt)
	if err != nil {
		return storedScheme{}, err
	}
	if err := json.Unmarshal(row.Raw, &row.Config); err != nil {
		return storedScheme{}, fmt.Errorf("decode published scheme: %w", err)
	}
	// The scheme JSONB carries scoring rules only. Window, capability switches
	// and honour-roll policy belong to the class, not to a scheme version, so
	// they are layered on here — every caller downstream keeps reading
	// Config.Window and friends exactly as it did before.
	timeline, err := classtimeline.Load(ctx, tx, classID)
	if err != nil {
		return storedScheme{}, err
	}
	timeline.Apply(&row.Config)
	row.CollegeName = timeline.CollegeName
	row.EnrollmentClass = timeline.EnrollmentClass
	row.AcademicYear = timeline.AcademicYear
	return row, nil
}

func ensureDefaultSelfReportScheme(ctx context.Context, tx pgx.Tx) error {
	var creatorID, classID int64
	if err := tx.QueryRow(ctx, `
		SELECT id,class_id FROM app_user
		 WHERE status='active'
		 ORDER BY CASE role WHEN 'class_admin' THEN 0 WHEN 'group' THEN 1 ELSE 2 END,id
		 LIMIT 1
	`).Scan(&creatorID, &classID); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM scheme`).Scan(&version); err != nil {
		return err
	}
	cfg := scheme.DefaultSelfReportConfig("v" + strconv.Itoa(version))
	if err := scheme.Validate(cfg); err != nil {
		return fmt.Errorf("validate default self-report scheme: %w", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO scheme (class_id,name,version,status,config,created_by,published_at)
		VALUES ($1,$2,$3,'published',$4,$5,now())
		ON CONFLICT DO NOTHING
	`, classID, cfg.SchemeName, version, raw, creatorID)
	return err
}

func (s *Server) currentScheme(c *gin.Context) {
	tx := mustTx(c)
	row, err := loadCurrentScheme(c.Request.Context(), tx)
	if notFound(c, err, "当前方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "scheme.read", "scheme", strconv.FormatInt(row.ID, 10), nil, nil, map[string]any{"version": row.Version}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, currentSchemeResponse{Config: row.Config, PublishedAt: row.PublishedAt})
}

func (s *Server) schemeVersion(c *gin.Context) {
	versionText := strings.TrimPrefix(strings.TrimSpace(c.Param("version")), "v")
	version, err := strconv.Atoi(versionText)
	if err != nil || version <= 0 {
		writeError(c, http.StatusBadRequest, "invalid_version", "方案版本不正确", nil)
		return
	}
	tx := mustTx(c)
	var id int64
	var raw []byte
	err = tx.QueryRow(c.Request.Context(), `SELECT id,config FROM scheme WHERE status='published' AND version=$1`, version).Scan(&id, &raw)
	if notFound(c, err, "方案版本") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var cfg scheme.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		writeServiceError(c, err)
		return
	}
	// A past scheme version is a scoring snapshot, but the timeline it is read
	// against is always the class's current one — there is only ever one live
	// window, no matter which version's rules you are looking at.
	timeline, err := classtimeline.Load(c.Request.Context(), tx, mustActor(c).ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	timeline.Apply(&cfg)
	if err := appendAudit(c, tx, "scheme.version_read", "scheme", strconv.FormatInt(id, 10), nil, nil, map[string]any{"version": version}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

func (s *Server) window(c *gin.Context) {
	tx := mustTx(c)
	row, err := loadCurrentScheme(c.Request.Context(), tx)
	if notFound(c, err, "当前方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	actor := mustActor(c)
	var sealed bool
	err = tx.QueryRow(c.Request.Context(), `SELECT EXISTS (SELECT 1 FROM seal WHERE student_id=$1 AND unsealed_at IS NULL)`, actor.UserID).Scan(&sealed)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// Live submission-state refreshes are reads, not business audit events.
	c.JSON(http.StatusOK, gin.H{
		"window": row.Config.Window, "capabilities": row.Config.Capabilities,
		"honorRoll":   row.Config.HonorRoll,
		"collegeName": row.CollegeName, "enrollmentClass": row.EnrollmentClass,
		"academicYear": row.AcademicYear,
		"serverNow":    time.Now().UTC(), "sealed": sealed, "schemeVersion": row.Config.Version,
	})
}

type schemeMeta struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Version     *int       `json:"version"`
	Status      string     `json:"status"`
	LockVersion int64      `json:"lockVersion"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

func (s *Server) adminTemplates(c *gin.Context) {
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT id,name,created_at
		  FROM platform_template
		 WHERE active
		 ORDER BY created_at DESC,id DESC
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var name string
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &createdAt); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "name": name, "createdAt": createdAt})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "template.available_list", "platform_template", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) adminSchemes(c *gin.Context) {
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT id,name,version,status,lock_version,published_at,updated_at
		  FROM scheme ORDER BY COALESCE(version,2147483647) DESC,updated_at DESC
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]schemeMeta, 0)
	for rows.Next() {
		var id int64
		var item schemeMeta
		if err := rows.Scan(&id, &item.Name, &item.Version, &item.Status, &item.LockVersion, &item.PublishedAt, &item.UpdatedAt); err != nil {
			writeServiceError(c, err)
			return
		}
		item.ID = strconv.FormatInt(id, 10)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "scheme.list", "scheme", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) createScheme(c *gin.Context) {
	var input struct {
		Name           string          `json:"name"`
		Config         json.RawMessage `json:"config"`
		TemplateID     *int64          `json:"templateId"`
		SourceSchemeID *int64          `json:"sourceSchemeId"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "方案参数不正确", nil)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		writeError(c, http.StatusBadRequest, "invalid_name", "方案名称不能为空", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if input.TemplateID != nil && input.SourceSchemeID != nil {
		writeError(c, http.StatusBadRequest, "invalid_source", "平台模板与已发布版本不能同时作为来源", nil)
		return
	}
	raw := input.Config
	if input.TemplateID != nil {
		var templateRaw []byte
		if err := tx.QueryRow(c.Request.Context(), `SELECT config FROM platform_template WHERE id=$1 AND active`, *input.TemplateID).Scan(&templateRaw); err != nil {
			if notFound(c, err, "平台模板") {
				return
			}
			writeServiceError(c, err)
			return
		}
		template, err := scheme.DecodeTemplate(templateRaw)
		if err != nil {
			writeError(c, http.StatusUnprocessableEntity, "template_invalid", "平台模板无法解析为评分方案", err.Error())
			return
		}
		if err := scheme.ValidateTemplate(template); err != nil {
			writeError(c, http.StatusUnprocessableEntity, "template_invalid", "平台模板无法通过评分规则校验", err.Error())
			return
		}
		// A draft holds scoring rules only, so there is no class runtime to
		// carry over any more — the timeline lives in class_timeline and is
		// untouched by publishing new scoring rules.
		raw, err = json.Marshal(scheme.ApplyTemplate(scheme.DefaultSelfReportConfig("draft"), template))
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if input.SourceSchemeID != nil {
		if err := tx.QueryRow(c.Request.Context(), `SELECT config FROM scheme WHERE id=$1 AND class_id=$2 AND status='published'`, *input.SourceSchemeID, actor.ClassID).Scan(&raw); err != nil {
			if notFound(c, err, "已发布方案") {
				return
			}
			writeServiceError(c, err)
			return
		}
	}
	if len(raw) == 0 {
		cfg := scheme.DefaultSelfReportConfig("draft")
		cfg.SchemeName = input.Name
		cfg.Version = "draft"
		var err error
		raw, err = json.Marshal(cfg)
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if !json.Valid(raw) || len(raw) > 2<<20 {
		writeError(c, http.StatusBadRequest, "invalid_config", "方案配置不是合法 JSON 或体积过大", nil)
		return
	}
	baseName := input.Name
	for suffix := 1; ; suffix++ {
		candidate := baseName
		if suffix > 1 {
			candidate = fmt.Sprintf("%s（%d）", baseName, suffix)
		}
		var exists bool
		if err := tx.QueryRow(c.Request.Context(), `SELECT EXISTS (SELECT 1 FROM scheme WHERE class_id=$1 AND status='draft' AND lower(name)=lower($2))`, actor.ClassID, candidate).Scan(&exists); err != nil {
			writeServiceError(c, err)
			return
		}
		if !exists {
			input.Name = candidate
			break
		}
	}
	var id int64
	var lockVersion int64
	err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO scheme (class_id,name,status,config,created_by)
		VALUES ($1,$2,'draft',$3,$4)
		RETURNING id,lock_version
	`, actor.ClassID, input.Name, []byte(raw), actor.UserID).Scan(&id, &lockVersion)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	metadata := map[string]any{}
	if input.TemplateID != nil {
		metadata["templateId"] = *input.TemplateID
	}
	if input.SourceSchemeID != nil {
		metadata["sourceSchemeId"] = *input.SourceSchemeID
	}
	if err := appendAudit(c, tx, "scheme.created", "scheme", strconv.FormatInt(id, 10), nil, map[string]any{"name": input.Name}, metadata); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10), "lockVersion": lockVersion})
}

func (s *Server) deleteScheme(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var name string
	var raw []byte
	if err := tx.QueryRow(c.Request.Context(), `SELECT name,config FROM scheme WHERE id=$1 AND status='draft' FOR UPDATE`, id).Scan(&name, &raw); err != nil {
		if notFound(c, err, "方案草稿") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "scheme.deleted", "scheme", strconv.FormatInt(id, 10), map[string]any{"name": name, "config": json.RawMessage(raw)}, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	command, err := tx.Exec(c.Request.Context(), `DELETE FROM scheme WHERE id=$1 AND status='draft'`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if command.RowsAffected() != 1 {
		writeError(c, http.StatusConflict, "scheme_not_deleted", "草稿未能删除，请刷新后重试", nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) adminScheme(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var name, status string
	var version *int
	var lockVersion int64
	var raw []byte
	err := tx.QueryRow(c.Request.Context(), `SELECT name,version,status,config,lock_version FROM scheme WHERE id=$1`, id).
		Scan(&name, &version, &status, &raw, &lockVersion)
	if notFound(c, err, "方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "scheme.admin_read", "scheme", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	var configValue any
	_ = json.Unmarshal(raw, &configValue)
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "name": name, "version": version, "status": status, "config": configValue, "lockVersion": lockVersion})
}

func (s *Server) updateScheme(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		Name        string          `json:"name"`
		Config      json.RawMessage `json:"config"`
		LockVersion int64           `json:"lockVersion"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.LockVersion <= 0 || !json.Valid(input.Config) || len(input.Config) > 2<<20 {
		writeError(c, http.StatusBadRequest, "invalid_request", "方案草稿参数不正确", nil)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		writeError(c, http.StatusBadRequest, "invalid_name", "方案名称不能为空", nil)
		return
	}
	tx := mustTx(c)
	var before []byte
	if err := tx.QueryRow(c.Request.Context(), `SELECT config FROM scheme WHERE id=$1 AND status='draft'`, id).Scan(&before); err != nil {
		if notFound(c, err, "方案草稿") {
			return
		}
		writeServiceError(c, err)
		return
	}
	var nextLock int64
	err := tx.QueryRow(c.Request.Context(), `
		UPDATE scheme SET name=$1,config=$2,lock_version=lock_version+1,updated_at=now()
		 WHERE id=$3 AND status='draft' AND lock_version=$4
		 RETURNING lock_version
	`, input.Name, []byte(input.Config), id, input.LockVersion).Scan(&nextLock)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusConflict, "stale_version", "方案草稿已被其他操作更新，请刷新后重试", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "scheme.updated", "scheme", strconv.FormatInt(id, 10), json.RawMessage(before), input.Config, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "lockVersion": nextLock})
}

func (s *Server) publishScheme(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var previousSchemeID *int64
	previousErr := tx.QueryRow(c.Request.Context(), `SELECT id FROM scheme WHERE status='published' ORDER BY version DESC,id DESC LIMIT 1`).Scan(&previousSchemeID)
	if previousErr != nil && !errors.Is(previousErr, pgx.ErrNoRows) {
		writeServiceError(c, previousErr)
		return
	}
	var name string
	var raw []byte
	err := tx.QueryRow(c.Request.Context(), `SELECT name,config FROM scheme WHERE id=$1 AND status='draft' FOR UPDATE`, id).Scan(&name, &raw)
	if notFound(c, err, "方案草稿") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var cfg scheme.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_config", "方案配置无法解析", err.Error())
		return
	}
	var version int
	if err := tx.QueryRow(c.Request.Context(), `SELECT COALESCE(MAX(version),0)+1 FROM scheme WHERE status='published'`).Scan(&version); err != nil {
		writeServiceError(c, err)
		return
	}
	cfg.SchemeName = name
	cfg.Version = "v" + strconv.Itoa(version)
	// Validate covers the timeline too, so give it the class's live envelope
	// rather than whatever the draft's zero values are. Publishing scoring
	// rules must not disturb the timeline, and cannot: the fields are not
	// serialized back into the scheme row.
	timeline, err := classtimeline.Load(c.Request.Context(), tx, mustActor(c).ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	timeline.Apply(&cfg)
	if err := scheme.Validate(cfg); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "scheme_invalid", "方案整体校验未通过", err.Error())
		return
	}
	flags := s.runtimeFlags(c.Request.Context())
	if err := validateSchemeEvidencePolicy(cfg, int64(flags.UploadMaxMB), flags.EvidenceAllowedFormats); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "scheme_invalid", "方案佐证上限超过平台限制", err.Error())
		return
	}
	normalized, _ := json.Marshal(cfg)
	command, err := tx.Exec(c.Request.Context(), `
		UPDATE scheme SET version=$1,status='published',config=$2,published_at=now(),updated_at=now(),lock_version=lock_version+1
		 WHERE id=$3 AND status='draft'
	`, version, normalized, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if command.RowsAffected() != 1 {
		writeError(c, http.StatusConflict, "scheme_changed", "方案草稿状态已变化，请刷新后重试", nil)
		return
	}
	if previousSchemeID != nil && *previousSchemeID != id {
		if err := invalidateBlindAuditsForScheme(c.Request.Context(), tx, *previousSchemeID, "new scheme published"); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "scheme.published", "scheme", strconv.FormatInt(id, 10), json.RawMessage(raw), cfg, map[string]any{"version": version}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "version": version, "config": cfg})
}

func validateSchemeEvidenceLimit(cfg scheme.Config, maxMB int64) error {
	return validateSchemeEvidencePolicy(cfg, maxMB, opsconfig.DefaultEvidenceAllowedFormats())
}

func validateSchemeEvidencePolicy(cfg scheme.Config, maxMB int64, platformFormats []string) error {
	if maxMB <= 0 {
		maxMB = scheme.DefaultEvidenceMaxMB
	}
	allowed := make(map[string]bool, len(platformFormats))
	for _, value := range platformFormats {
		allowed[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")] = true
	}
	checkRule := func(label string, rule *scheme.EvidenceRule) error {
		if rule == nil {
			return nil
		}
		if rule.MaxMB > maxMB {
			return fmt.Errorf("%s 的 maxMb=%d，平台上限为 %d MB", label, rule.MaxMB, maxMB)
		}
		for _, value := range scheme.EffectiveEvidenceTypes(rule) {
			format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
			if !allowed[format] && !(format == "jpeg" && allowed["jpg"]) && !(format == "jpg" && allowed["jpeg"]) {
				return fmt.Errorf("%s 使用了平台已禁用的格式 .%s", label, format)
			}
		}
		return nil
	}
	for _, category := range cfg.Categories {
		for _, item := range category.BaseItems {
			if item.StudentClaim != nil {
				if err := checkRule(fmt.Sprintf("可申报基础项 %q", item.Key), item.StudentClaim.Evidence); err != nil {
					return err
				}
			}
		}
		for _, item := range category.Items {
			if err := checkRule(fmt.Sprintf("小项 %q", item.Key), item.Evidence); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) shareScheme(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var name string
	var raw []byte
	if err := tx.QueryRow(c.Request.Context(), `SELECT name,config FROM scheme WHERE id=$1 AND status='published'`, id).Scan(&name, &raw); err != nil {
		if notFound(c, err, "已发布方案") {
			return
		}
		writeServiceError(c, err)
		return
	}
	actor := mustActor(c)
	var cfg scheme.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		writeServiceError(c, err)
		return
	}
	template := scheme.TemplateFromConfig(cfg)
	if err := scheme.ValidateTemplate(template); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "scheme_share_invalid", "当前评分方案无法生成平台模板", err.Error())
		return
	}
	flags := s.runtimeFlags(c.Request.Context())
	if err := validateSchemeEvidencePolicy(cfg, int64(flags.UploadMaxMB), flags.EvidenceAllowedFormats); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "scheme_share_invalid", "当前方案不再符合平台佐证策略", err.Error())
		return
	}
	templateRaw, err := json.Marshal(template)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var requestID, status string
	err = tx.QueryRow(c.Request.Context(), `
		SELECT id::text,status FROM template_share_request
		 WHERE scheme_id=$1 AND status IN ('pending','approved')
		 ORDER BY CASE WHEN status='pending' THEN 0 ELSE 1 END,created_at DESC LIMIT 1
	`, id).Scan(&requestID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(c.Request.Context(), `
			INSERT INTO template_share_request (class_id,scheme_id,requested_by,name,config)
			VALUES ($1,$2,$3,$4,$5) RETURNING id::text,status
		`, actor.ClassID, id, actor.UserID, name, templateRaw).Scan(&requestID, &status)
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "scheme.share_requested", "scheme", strconv.FormatInt(id, 10), nil, nil, map[string]any{"requestId": requestID}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"requestId": requestID, "status": status})
}

func (s *Server) adminTemplateShareRequests(c *gin.Context) {
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT id::text,scheme_id::text,name,status,review_reason,template_id::text,created_at,reviewed_at
		  FROM template_share_request ORDER BY created_at DESC,id DESC LIMIT 100
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var requestID, schemeID, name, status string
		var reason, templateID *string
		var createdAt time.Time
		var reviewedAt *time.Time
		if err := rows.Scan(&requestID, &schemeID, &name, &status, &reason, &templateID, &createdAt, &reviewedAt); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": requestID, "schemeId": schemeID, "name": name, "status": status, "reviewReason": reason, "templateId": templateID, "createdAt": createdAt, "reviewedAt": reviewedAt})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) adminClass(c *gin.Context) {
	tx := mustTx(c)
	actor := mustActor(c)
	var name string
	var archived bool
	var createdAt time.Time
	if err := tx.QueryRow(c.Request.Context(), `SELECT name,archived,created_at FROM class WHERE id=$1`, actor.ClassID).Scan(&name, &archived, &createdAt); err != nil {
		writeServiceError(c, err)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	response := gin.H{"id": strconv.FormatInt(actor.ClassID, 10), "name": name, "archived": archived, "createdAt": createdAt}
	if err == nil {
		response["schemeVersion"] = current.Config.Version
		response["window"] = current.Config.Window
		response["capabilities"] = current.Config.Capabilities
	}
	if err := appendAudit(c, tx, "class.read", "class", strconv.FormatInt(actor.ClassID, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

/* 时间线与业务开关的写入口。

   这两个 handler 以前都走 cloneCurrentScheme：改一次截止时间就克隆出一个新的
   已发布方案版本，还要把 base_score 和 student_gpa 整表复制到新的 scheme_id
   下面。"把窗口延长三天"和"发布了一版新的评优方案"根本不是一回事，那条因果
   链是错的。现在两者各归各位——时间线写 class_timeline，方案版本只在真正改了
   评分规则时才涨。 */

func (s *Server) updateTimeline(c *gin.Context) {
	var input struct {
		Open                time.Time       `json:"open"`
		Close               time.Time       `json:"close"`
		Lockdown            *time.Time      `json:"lockdown"`
		Publicity           json.RawMessage `json:"publicity,omitempty"`
		HonorRollTopPercent *int            `json:"honorRollTopPercent,omitempty"`
		Awards              []scheme.Award  `json:"awards"`
		CollegeName         *string         `json:"collegeName,omitempty"`
		EnrollmentClass     *string         `json:"enrollmentClass,omitempty"`
		AcademicYear        *string         `json:"academicYear,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_window", "时间线参数不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	timeline, err := classtimeline.Load(c.Request.Context(), tx, actor.ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	before := timeline

	if len(input.Publicity) > 0 {
		var publicity *scheme.PublicityWindow
		if err := json.Unmarshal(input.Publicity, &publicity); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_window", "公示窗口时间格式不正确", nil)
			return
		}
		timeline.Window.Publicity = publicity
	}
	timeline.Window.Open = input.Open.UTC()
	timeline.Window.Close = input.Close.UTC()
	timeline.Window.Lockdown = nil
	if input.Lockdown != nil {
		locked := input.Lockdown.UTC()
		timeline.Window.Lockdown = &locked
	}
	if input.HonorRollTopPercent != nil {
		timeline.HonorRoll.TopPercent = *input.HonorRollTopPercent
	}
	if input.Awards != nil {
		for i := range input.Awards {
			input.Awards[i].Name = strings.TrimSpace(input.Awards[i].Name)
		}
		timeline.HonorRoll.Awards = input.Awards
	}
	// 学院名与教务班级名只喂给学院报表，不参与任何评分或闸门判断，所以只做
	// 长度上的兜底，不校验内容——各学院怎么写自己的全称不该由这里规定。
	if input.CollegeName != nil {
		timeline.CollegeName = strings.TrimSpace(*input.CollegeName)
	}
	if input.EnrollmentClass != nil {
		timeline.EnrollmentClass = strings.TrimSpace(*input.EnrollmentClass)
	}
	if input.AcademicYear != nil {
		timeline.AcademicYear = strings.TrimSpace(*input.AcademicYear)
		if !validAcademicYear(timeline.AcademicYear) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_window", "评定学年须为连续两年，如 2025-2026", nil)
			return
		}
	}
	if len(timeline.CollegeName) > 100 || len(timeline.EnrollmentClass) > 100 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_window", "学院名称与教务班级名各不能超过 100 字节", nil)
		return
	}
	if err := scheme.ValidateTimeline(timeline.Window, timeline.Capabilities, timeline.HonorRoll); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "invalid_window", "时间线设置未通过校验", err.Error())
		return
	}
	if err := classtimeline.Save(c.Request.Context(), tx, actor.ClassID, timeline); err != nil {
		writeServiceError(c, err)
		return
	}
	if !reflect.DeepEqual(before.HonorRoll, timeline.HonorRoll) {
		if err := invalidateLatestSettlement(c.Request.Context(), tx, "honor and scholarship policy changed"); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "timeline.updated", "class", strconv.FormatInt(actor.ClassID, 10), timelineAudit(before), timelineAudit(timeline), nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"window": timeline.Window, "honorRoll": timeline.HonorRoll,
		"collegeName": timeline.CollegeName, "enrollmentClass": timeline.EnrollmentClass,
		"academicYear": timeline.AcademicYear,
	})
}

func (s *Server) updateCapability(c *gin.Context) {
	var input struct {
		Key string `json:"key"`
		On  bool   `json:"on"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "能力开关参数不正确", nil)
		return
	}
	if input.Key == "export" {
		writeError(c, http.StatusConflict, "export_gate_managed", "导出能力只由结算门禁控制，班级管理员不能直接开关", nil)
		return
	}
	if input.Key == "gpa" {
		writeError(c, http.StatusBadRequest, "invalid_capability", "专业素质分导入默认开放，无需设置开关或截止时间", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	timeline, err := classtimeline.Load(c.Request.Context(), tx, actor.ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	before := timeline
	switch input.Key {
	case "submit":
		timeline.Capabilities.Submit = input.On
	case "edit":
		timeline.Capabilities.Edit = input.On
	case "appeal":
		timeline.Capabilities.Appeal = input.On
	case "review":
		timeline.Capabilities.Review = input.On
	case "arbitrate":
		timeline.Capabilities.Arbitrate = input.On
	case "studentReport":
		// 关掉只关入口：已经在复核中的举报仍然会走完，
		// 已经落到 base_score 的扣分也不回退（那要走申诉或实名提案）。
		timeline.Capabilities.StudentReport = input.On
	default:
		writeError(c, http.StatusBadRequest, "invalid_capability", "未知能力开关", nil)
		return
	}
	if err := scheme.ValidateTimeline(timeline.Window, timeline.Capabilities, timeline.HonorRoll); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "invalid_window", "能力开关设置未通过校验", err.Error())
		return
	}
	if err := classtimeline.Save(c.Request.Context(), tx, actor.ClassID, timeline); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "timeline.capability_updated", "class", strconv.FormatInt(actor.ClassID, 10), timelineAudit(before), timelineAudit(timeline), map[string]any{"key": input.Key}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"capabilities": timeline.Capabilities})
}

// timelineAudit flattens the envelope for the audit log's before/after columns.
// classtimeline.Timeline has no JSON tags of its own — it is a carrier, not a
// wire type — so spelling the shape out here keeps the audit rows readable.
func timelineAudit(timeline classtimeline.Timeline) map[string]any {
	return map[string]any{
		"window":          timeline.Window,
		"capabilities":    timeline.Capabilities,
		"honorRoll":       timeline.HonorRoll,
		"collegeName":     timeline.CollegeName,
		"enrollmentClass": timeline.EnrollmentClass,
		"academicYear":    timeline.AcademicYear,
	}
}
