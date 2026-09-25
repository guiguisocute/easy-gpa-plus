package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"easygpa/backend/internal/classtimeline"
	"easygpa/backend/internal/exportjob"
	"easygpa/backend/internal/governance"
	"github.com/gin-gonic/gin"
)

type governanceExecutionKey struct{}
type governanceExecution struct {
	ID, Author int64
	Action     string
	Subject    *int64
}

func governanceExecutionFor(c *gin.Context, action string) bool {
	grant, ok := c.Request.Context().Value(governanceExecutionKey{}).(governanceExecution)
	return ok && grant.ID > 0 && grant.Action == action
}

func (s *Server) validateGovernanceAction(c *gin.Context, in *governanceProposalInput) (map[int64]bool, error) {
	excluded := map[int64]bool{}
	if len(in.Payload) == 0 {
		in.Payload = json.RawMessage(`{}`)
	}
	if len(in.Payload) > 256*1024 || !json.Valid(in.Payload) {
		return nil, errors.New("提案执行内容过大或格式错误")
	}
	switch in.Action {
	case "gpa":
		if in.Kind != "ordinary" {
			return nil, errors.New("专业分属于来源核验事项")
		}
		var input struct {
			Text     string `json:"text"`
			UploadID jsonID `json:"uploadId"`
		}
		if json.Unmarshal(in.Payload, &input) != nil || input.UploadID <= 0 {
			return nil, errors.New("请上传正式成绩原件并提供待核验的表格")
		}
		table, err := parseCSVRows(strings.NewReader(input.Text))
		if err != nil {
			return nil, errors.New("成绩表格式错误")
		}
		rows, err := parseGPATable(table)
		if err != nil {
			return nil, err
		}
		var roster int
		if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT count(*) FROM app_user WHERE status='active'`).Scan(&roster); err != nil {
			return nil, err
		}
		if roster != len(rows) {
			return nil, errors.New("专业分原件必须覆盖全班在册成员，包括未注册和没有材料的成员")
		}
		for _, row := range rows {
			var found bool
			if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM app_user WHERE sid=$1 AND status='active' AND ($2='' OR name=$2))`, row.SID, row.Name).Scan(&found); err != nil {
				return nil, err
			}
			if !found {
				return nil, errors.New("成绩原件与班级名单不匹配")
			}
		}
		var ready bool
		if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM bonus_grant_upload u WHERE u.id=$1 AND u.created_by=$2 AND u.used_by IS NULL AND EXISTS(SELECT 1 FROM evidence WHERE bonus_upload_id=u.id AND status='ready') AND NOT EXISTS(SELECT 1 FROM evidence WHERE bonus_upload_id=u.id AND status<>'ready'))`, int64(input.UploadID), mustActor(c).UserID).Scan(&ready); err != nil {
			return nil, err
		}
		if !ready {
			return nil, errors.New("请先完成正式成绩原件上传")
		}
		in.Payload, _ = json.Marshal(gin.H{"rows": rows, "uploadId": input.UploadID})
		excluded[mustActor(c).UserID] = true
	case "export":
		if in.Kind != "ordinary" {
			return nil, errors.New("导出授权使用日常事项表决")
		}
		var input struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(in.Payload, &input) != nil || !exportjob.ValidKind(input.Kind) {
			return nil, errors.New("请选择汇总、明细、归档或学院报表")
		}
		in.Payload, _ = json.Marshal(gin.H{"kind": input.Kind, "recipientId": strconv.FormatInt(mustActor(c).UserID, 10)})
		excluded[mustActor(c).UserID] = true
	case "activate":
		if in.Kind != "activate" {
			return nil, errors.New("启动须使用启动表决")
		}
	case "motion":
		if in.Kind != "ordinary" && in.Kind != "protected" {
			return nil, errors.New("共同事项类型无效")
		}
		in.Payload = json.RawMessage(`{}`)
	case "timeline":
		if in.Kind != "protected" {
			return nil, errors.New("时间窗口变更属于重大事项")
		}
		var input map[string]json.RawMessage
		if json.Unmarshal(in.Payload, &input) != nil {
			return nil, errors.New("时间窗口格式错误")
		}
		for key := range input {
			switch key {
			case "open", "close", "lockdown", "publicity":
			default:
				return nil, errors.New("当前周期只能提出时间窗口变更，不能借此修改评优标准")
			}
		}
		var window struct {
			Open  time.Time `json:"open"`
			Close time.Time `json:"close"`
		}
		if json.Unmarshal(in.Payload, &window) != nil || window.Open.IsZero() || !window.Close.After(window.Open) {
			return nil, errors.New("请提供完整且有效的开放和截止时间")
		}
		if _, ok := input["lockdown"]; !ok {
			timeline, err := classtimeline.Load(c.Request.Context(), mustTx(c), mustActor(c).ClassID)
			if err != nil {
				return nil, err
			}
			input["lockdown"], _ = json.Marshal(timeline.Window.Lockdown)
			in.Payload, _ = json.Marshal(input)
		}
	case "bonus":
		if in.Kind != "bonus" {
			return nil, errors.New("定向共同加分须按受益回避表决")
		}
		var input bonusGrantInput
		if json.Unmarshal(in.Payload, &input) != nil {
			return nil, errors.New("共同加分参数无效")
		}
		if err := normalizeBonusGrant(&input); err != nil {
			return nil, err
		}
		current, err := loadCurrentScheme(c.Request.Context(), mustTx(c))
		if err != nil {
			return nil, err
		}
		if current.Config.Version != input.SchemeVersion {
			return nil, errors.New("评分方案已经变化")
		}
		prepared, err := prepareBonusGrant(current, input, time.Now())
		if err != nil {
			return nil, err
		}
		if prepared.Item.Evidence != nil && prepared.Item.Evidence.Required && input.UploadID == nil {
			return nil, errors.New("该小项要求共同佐证，请先上传原件")
		}
		for _, id := range input.StudentIDs {
			var active bool
			if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT status='active' FROM app_user WHERE id=$1`, int64(id)).Scan(&active); err != nil || !active {
				return nil, errors.New("受益名单包含不属于本班的有效成员")
			}
			excluded[int64(id)] = true
		}
		var roster int
		if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT count(*) FROM app_user WHERE status='active'`).Scan(&roster); err != nil {
			return nil, err
		}
		// An equal grant to every roster member is a class-wide rule matter,
		// not a personal award with no disinterested voters. Apply the protected
		// whole-class threshold and notice period, including unregistered people.
		if len(input.StudentIDs) == roster {
			in.Kind = "protected"
			excluded = map[int64]bool{}
		}
		if input.UploadID != nil {
			var own bool
			if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT created_by=$2 AND used_by IS NULL AND EXISTS(SELECT 1 FROM evidence WHERE bonus_upload_id=u.id AND status='ready') AND NOT EXISTS(SELECT 1 FROM evidence WHERE bonus_upload_id=u.id AND status<>'ready') FROM bonus_grant_upload u WHERE id=$1`, int64(*input.UploadID), mustActor(c).UserID).Scan(&own); err != nil || !own {
				return nil, errors.New("共同佐证必须由发起人上传且尚未使用")
			}
		}
		in.Payload, _ = json.Marshal(input)
	default:
		return nil, errors.New("不支持的共治执行事项")
	}
	return excluded, nil
}

// Fingerprints contain only the affected facts, not unrelated class activity.
// Voting itself and account registration must not invalidate a proposal.
func (s *Server) governanceActionState(c *gin.Context, action string, payload []byte) (string, error) {
	ctx, tx := c.Request.Context(), mustTx(c)
	// PostgreSQL jsonb canonicalizes whitespace and key order on storage. Hash
	// that representation both before inserting and after reading a proposal.
	if err := tx.QueryRow(ctx, `SELECT $1::jsonb`, payload).Scan(&payload); err != nil {
		return "", err
	}
	var raw []byte
	var err error
	switch action {
	case "motion":
		return mcpDigest(payload), nil
	case "activate", "timeline":
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object('scheme',(SELECT jsonb_agg(jsonb_build_object('id',id,'version',version,'config',config)) FROM scheme WHERE status='published'),'timeline',(SELECT to_jsonb(t) FROM class_timeline t WHERE class_id=$1))`, mustActor(c).ClassID).Scan(&raw)
	case "bonus", "gpa":
		var in struct {
			UploadID *jsonID `json:"uploadId"`
		}
		if err = json.Unmarshal(payload, &in); err != nil {
			return "", err
		}
		var upload *int64
		if in.UploadID != nil {
			value := int64(*in.UploadID)
			upload = &value
		}
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object('scheme',(SELECT jsonb_agg(jsonb_build_object('id',id,'version',version,'config',config)) FROM scheme WHERE status='published'),'files',(SELECT jsonb_agg(jsonb_build_object('id',id,'key',object_key,'sha',sha256,'status',status) ORDER BY id) FROM evidence WHERE bonus_upload_id=$1))`, upload).Scan(&raw)
		if err == nil && action == "gpa" {
			var gpa []byte
			err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(g) ORDER BY student_id),'[]') FROM student_gpa g`).Scan(&gpa)
			raw = append(raw, gpa...)
		}
	case "export":
		err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY id),'[]') FROM settlement_run r WHERE status='complete' AND NOT EXISTS(SELECT 1 FROM settlement_invalidation i WHERE i.run_id=r.id)`).Scan(&raw)
	default:
		return s.governanceCaseState(c, action, payload)
	}
	if err != nil {
		return "", err
	}
	return mcpDigest(append(raw, payload...)), nil
}

func (s *Server) executeGovernanceProposal(c *gin.Context, p governanceProposal) {
	ctx, tx, actor := c.Request.Context(), mustTx(c), mustActor(c)
	if p.Kind == "protected" || p.Kind == "activate" {
		var decided time.Time
		if err := tx.QueryRow(ctx, `SELECT decided_at FROM governance_proposal WHERE id=$1`, p.ID).Scan(&decided); err != nil {
			writeServiceError(c, err)
			return
		}
		if time.Now().Before(decided.Add(24 * time.Hour)) {
			c.JSON(200, gin.H{"status": "passed", "notice": "公示通过后满 24 小时生效"})
			return
		}
	}
	state, err := s.governanceActionState(c, p.Action, p.Payload)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if state != p.StateHash {
		if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status='stale' WHERE id=$1`, p.ID); err != nil {
			writeServiceError(c, err)
			return
		}
		c.JSON(200, gin.H{"status": "stale", "notice": "依据或对象已变化，请重新核对并发起新版本"})
		return
	}
	var result []byte
	switch p.Action {
	case "motion":
		result = []byte(`{"recorded":true}`)
	case "activate":
		members, e := governanceMembers(ctx, tx)
		if e != nil {
			writeServiceError(c, e)
			return
		}
		var wanted governance.Profile
		if json.Unmarshal(p.Payload, &wanted) != nil {
			writeError(c, 409, "profile_invalid", "评审配置无效", nil)
			return
		}
		count := 0
		for _, m := range members {
			if governance.Eligible(m, time.Now()) && m.Reviewer {
				count++
			}
		}
		if count < wanted.Minimum {
			writeError(c, 409, "capacity_changed", "当前评审人数不足以履行已通过的章程，请补齐成员；不会缩小评审规模", nil)
			return
		}
		var started bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM submission WHERE status<>'draft')`).Scan(&started); err != nil {
			writeServiceError(c, err)
			return
		}
		if started {
			writeError(c, 409, "assessment_started", "已经产生正式材料，不能切换处理模式", nil)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE class_governance SET mode='collective',profile=$1,activated_at=now(),version=version+1 WHERE class_id=$2 AND mode='enrolling'`, wanted.Name, actor.ClassID); err != nil {
			writeServiceError(c, err)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE agent_connection SET revoked_at=COALESCE(revoked_at,now()) WHERE class_id=$1`, actor.ClassID); err != nil {
			writeServiceError(c, err)
			return
		}
		result = []byte(`{"mode":"collective"}`)
	default:
		handler := s.governanceActionHandler(p.Action)
		if handler == nil {
			writeError(c, 422, "action_unknown", "此决议缺少执行方式", nil)
			return
		}
		request, params := c.Request, c.Params
		c.Request = c.Request.Clone(context.WithValue(ctx, governanceExecutionKey{}, governanceExecution{ID: p.ID, Author: p.Author, Action: p.Action, Subject: p.Subject}))
		c.Request.Body = io.NopCloser(bytes.NewReader(p.Payload))
		c.Request.ContentLength = int64(len(p.Payload))
		if p.Target != nil {
			c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(*p.Target, 10)}}
		}
		if p.Action == "report" {
			if _, err = tx.Exec(ctx, `UPDATE report SET status='escalated' WHERE id=$1 AND status='reviewing'`, p.Target); err != nil {
				writeServiceError(c, err)
				return
			}
		}
		code, body := capturedMCPHandler(c, handler)
		c.Request, c.Params = request, params
		if code >= 400 {
			writeMCPJSON(c, code, body)
			return
		}
		result = body
		if len(result) == 0 {
			result = []byte(`{}`)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status='applied',applied_at=now(),result=$2 WHERE id=$1`, p.ID, result); err != nil {
		writeServiceError(c, err)
		return
	}
	if err = governanceEvent(c, &p.ID, "proposal.applied", gin.H{"action": p.Action}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err = appendAudit(c, tx, "governance.applied", "governance_proposal", strconv.FormatInt(p.ID, 10), nil, nil, gin.H{"action": p.Action, "required": p.Required, "electorate": p.Electorate}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"status": "applied", "result": json.RawMessage(result)})
}
func (s *Server) governanceActionHandler(action string) gin.HandlerFunc {
	switch action {
	case "gpa":
		return s.pasteGPA
	case "export":
		return s.requestExport
	case "timeline":
		return s.updateTimeline
	case "bonus":
		return s.createBonusGrant
	case "submission":
		return s.arbitrateSubmission
	case "appeal":
		return s.finalizeAppealRequest
	case "report":
		return s.finalizeReport
	case "objection":
		return s.decideAdminObjection
	}
	return nil
}
