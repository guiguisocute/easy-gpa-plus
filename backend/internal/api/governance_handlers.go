package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"easygpa/backend/internal/governance"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type governanceConfig struct {
	Mode    string     `json:"mode"`
	Version int64      `json:"version"`
	Open    *time.Time `json:"enrollmentOpenAt"`
	Close   *time.Time `json:"enrollmentCloseAt"`
	Profile *string    `json:"profile"`
}

func loadGovernance(ctx context.Context, tx pgx.Tx, classID int64) (governanceConfig, error) {
	var g governanceConfig
	err := tx.QueryRow(ctx, `SELECT mode,version,enrollment_open_at,enrollment_close_at,profile FROM class_governance WHERE class_id=$1`, classID).Scan(&g.Mode, &g.Version, &g.Open, &g.Close, &g.Profile)
	if errors.Is(err, pgx.ErrNoRows) {
		return governanceConfig{Mode: "centralized"}, nil
	}
	return g, err
}
func governanceLock(c *gin.Context) bool {
	if _, err := mustTx(c).Exec(c.Request.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('easygpa:governance:' || $1::bigint::text,0))`, mustActor(c).ClassID); err != nil {
		writeServiceError(c, err)
		return false
	}
	return true
}
func governanceMembers(ctx context.Context, tx pgx.Tx) ([]governance.Member, error) {
	rows, err := tx.Query(ctx, `SELECT u.id,u.password_hash IS NOT NULL,u.status='active',m.joined_at,m.left_at,COALESCE(m.reviewer,false),
 EXISTS(SELECT 1 FROM submission s WHERE s.student_id=u.id AND s.status<>'draft' AND s.source IN ('manual','ai')),
 (SELECT count(*) FROM governance_voter v JOIN governance_proposal p ON p.id=v.proposal_id WHERE v.user_id=u.id AND v.active AND p.kind IN ('review','appeal'))
 FROM app_user u LEFT JOIN governance_member m ON m.user_id=u.id AND m.class_id=u.class_id ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]governance.Member, 0)
	for rows.Next() {
		var m governance.Member
		if err = rows.Scan(&m.ID, &m.Registered, &m.Active, &m.JoinedAt, &m.LeftAt, &m.Reviewer, &m.Submitted, &m.Load); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
func governanceRoster(ctx context.Context, tx pgx.Tx) (int, string, error) {
	var n int
	var hash string
	err := tx.QueryRow(ctx, `SELECT count(*),md5(COALESCE(string_agg(id::text,',' ORDER BY id),'')) FROM app_user WHERE status='active'`).Scan(&n, &hash)
	return n, hash, err
}
func governanceEvent(c *gin.Context, id *int64, action string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	var actor any = mustActor(c).UserID
	if automatic, _ := c.Get("governance.automatic"); automatic == true {
		actor = nil
	}
	_, err = mustTx(c).Exec(c.Request.Context(), `INSERT INTO governance_event(class_id,proposal_id,actor_id,action,detail) VALUES($1,$2,$3,$4,$5)`, mustActor(c).ClassID, id, actor, action, raw)
	return err
}
func (s *Server) governanceState(c *gin.Context) {
	ctx, tx, actor := c.Request.Context(), mustTx(c), mustActor(c)
	config, err := loadGovernance(ctx, tx, actor.ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	members, err := governanceMembers(ctx, tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	roster, registered, submitted, enrolled, reviewers := 0, 0, 0, 0, 0
	var mine governance.Member
	for _, m := range members {
		if m.ID == actor.UserID {
			mine = m
		}
		if !m.Active {
			continue
		}
		roster++
		if m.Registered {
			registered++
		}
		if m.Submitted {
			submitted++
		}
		if m.JoinedAt != nil && m.LeftAt == nil {
			enrolled++
		}
		if governance.Eligible(m, time.Now()) && m.Reviewer {
			reviewers++
		}
	}
	electorate := len(governance.Electorate(members, time.Now(), nil))
	ordinary, _ := governance.Threshold("ordinary", max(1, electorate), max(1, roster))
	protected, _ := governance.Threshold("protected", max(1, electorate), max(1, roster))
	var started bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM submission WHERE status<>'draft') OR EXISTS(SELECT 1 FROM settlement_run) OR EXISTS(SELECT 1 FROM appeal WHERE status<>'draft') OR EXISTS(SELECT 1 FROM report) OR EXISTS(SELECT 1 FROM objection WHERE status<>'draft')`).Scan(&started); err != nil {
		writeServiceError(c, err)
		return
	}
	profile, profileErr := governance.ReviewProfile(reviewers)
	problem := ""
	if profileErr != nil {
		problem = profileErr.Error()
	}
	if config.Profile != nil && *config.Profile == "standard" && reviewers < 11 {
		problem = fmt.Sprintf("本周期已固定标准章程，当前缺少 %d 名评审成员；不会自动改为精简章程", 11-reviewers)
	}
	c.JSON(200, gin.H{"config": config, "counts": gin.H{"roster": roster, "registered": registered, "submitted": submitted, "enrolled": enrolled, "electorate": electorate, "reviewers": reviewers}, "mine": mine, "canConfigure": actor.Role == "class_admin" && !started && (config.Version == 0 || config.Mode == "enrolling"), "ordinaryRequired": ordinary, "protectedRequired": protected, "suggestedProfile": profile, "capacityReason": problem})
}
func (s *Server) configureGovernance(c *gin.Context) {
	if mustActor(c).Role != "class_admin" {
		writeError(c, 403, "forbidden", "只有初始化管理员可以选择初始模式", nil)
		return
	}
	var input struct {
		Mode string `json:"mode"`
	}
	if c.ShouldBindJSON(&input) != nil || (input.Mode != "centralized" && input.Mode != "collective") {
		writeError(c, 422, "invalid_mode", "请选择中心化或班级共治", nil)
		return
	}
	if !governanceLock(c) {
		return
	}
	ctx, tx, actor := c.Request.Context(), mustTx(c), mustActor(c)
	g, err := loadGovernance(ctx, tx, actor.ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var started bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM submission WHERE status<>'draft') OR EXISTS(SELECT 1 FROM settlement_run) OR EXISTS(SELECT 1 FROM appeal WHERE status<>'draft') OR EXISTS(SELECT 1 FROM report) OR EXISTS(SELECT 1 FROM objection WHERE status<>'draft')`).Scan(&started); err != nil {
		writeServiceError(c, err)
		return
	}
	cancelEnrollment := false
	if input.Mode == "centralized" && g.Mode == "enrolling" && g.Close != nil && !time.Now().Before(*g.Close) {
		var reviewers int
		var pending bool
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM governance_member WHERE reviewer AND left_at IS NULL AND joined_at<=now()-interval '24 hours'`).Scan(&reviewers); err != nil {
			writeServiceError(c, err)
			return
		}
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governance_proposal WHERE action='activate' AND status IN ('discussion','voting','passed'))`).Scan(&pending); err != nil {
			writeServiceError(c, err)
			return
		}
		cancelEnrollment = reviewers < 7 && !pending
	}
	if started || (g.Version > 0 && !cancelEnrollment) {
		writeError(c, 409, "governance_started", "本周期已开始或正在共治招募，不能单人切换模式", nil)
		return
	}
	mode := input.Mode
	var open, close *time.Time
	if mode == "collective" {
		mode = "enrolling"
		now := time.Now()
		end := now.Add(governance.EnrollmentNotice)
		open = &now
		close = &end
	}
	_, err = tx.Exec(ctx, `INSERT INTO class_governance(class_id,mode,enrollment_open_at,enrollment_close_at,created_by) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(class_id) DO UPDATE SET mode=EXCLUDED.mode,version=class_governance.version+1,enrollment_open_at=EXCLUDED.enrollment_open_at,enrollment_close_at=EXCLUDED.enrollment_close_at`, actor.ClassID, mode, open, close, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err = governanceEvent(c, nil, "governance.configured", gin.H{"mode": mode}); err != nil {
		writeServiceError(c, err)
		return
	}
	s.governanceState(c)
}
func (s *Server) joinGovernance(c *gin.Context) {
	var input struct {
		Join     bool `json:"join"`
		Reviewer bool `json:"reviewer"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, 422, "invalid_request", "请选择参与方式", nil)
		return
	}
	if !governanceLock(c) {
		return
	}
	ctx, tx, actor := c.Request.Context(), mustTx(c), mustActor(c)
	g, err := loadGovernance(ctx, tx, actor.ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if g.Mode == "centralized" {
		writeError(c, 409, "not_collective", "班级尚未开启共治招募", nil)
		return
	}
	if input.Join {
		_, err = tx.Exec(ctx, `INSERT INTO governance_member(class_id,user_id,reviewer) VALUES($1,$2,$3)
 ON CONFLICT(class_id,user_id) DO UPDATE SET joined_at=CASE WHEN governance_member.left_at IS NULL THEN governance_member.joined_at ELSE now() END,left_at=NULL,reviewer=EXCLUDED.reviewer`, actor.ClassID, actor.UserID, input.Reviewer)
	} else {
		_, err = tx.Exec(ctx, `UPDATE governance_member SET left_at=COALESCE(left_at,now()),reviewer=false WHERE class_id=$1 AND user_id=$2`, actor.ClassID, actor.UserID)
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err = governanceEvent(c, nil, "governance.membership", input); err != nil {
		writeServiceError(c, err)
		return
	}
	s.governanceState(c)
}

type governanceProposal struct {
	ID          int64           `json:"id,string"`
	Author      int64           `json:"-"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Action      string          `json:"action"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Target      *int64          `json:"targetId,omitempty,string"`
	Subject     *int64          `json:"subjectId,omitempty,string"`
	Parent      *int64          `json:"parentId,omitempty,string"`
	Version     int64           `json:"version"`
	StateHash   string          `json:"-"`
	RosterHash  string          `json:"-"`
	RosterCount int             `json:"rosterCount"`
	Electorate  int             `json:"electorateCount"`
	Required    int             `json:"requiredYes"`
	Status      string          `json:"status"`
	Opens       time.Time       `json:"opensAt"`
	Closes      time.Time       `json:"closesAt"`
	Result      json.RawMessage `json:"result,omitempty"`
}

const governanceSelect = `SELECT id,author_id,kind,title,body,action,payload,target_id,subject_id,parent_id,governance_version,state_hash,roster_hash,roster_count,electorate_count,required_yes,status,opens_at,closes_at,result FROM governance_proposal`

func scanGovernanceProposal(row pgx.Row) (governanceProposal, error) {
	var p governanceProposal
	err := row.Scan(&p.ID, &p.Author, &p.Kind, &p.Title, &p.Body, &p.Action, &p.Payload, &p.Target, &p.Subject, &p.Parent, &p.Version, &p.StateHash, &p.RosterHash, &p.RosterCount, &p.Electorate, &p.Required, &p.Status, &p.Opens, &p.Closes, &p.Result)
	return p, err
}
func governanceCase(p governanceProposal) bool { return p.Kind == "review" || p.Kind == "appeal" }
func (s *Server) governanceProposals(c *gin.Context) {
	rows, err := mustTx(c).Query(c.Request.Context(), `SELECT id FROM governance_proposal p WHERE kind NOT IN ('review','appeal') OR subject_id=$1 OR EXISTS(SELECT 1 FROM governance_voter v WHERE v.proposal_id=p.id AND v.user_id=$1 AND v.active) ORDER BY id DESC LIMIT 100`, mustActor(c).UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	items := []gin.H{}
	for _, id := range ids {
		p, e := scanGovernanceProposal(mustTx(c).QueryRow(c.Request.Context(), governanceSelect+` WHERE id=$1`, id))
		if e != nil {
			writeServiceError(c, e)
			return
		}
		item, e := governanceProposalView(c, p)
		if e != nil {
			writeServiceError(c, e)
			return
		}
		items = append(items, item)
	}
	c.JSON(200, gin.H{"items": items})
}
func governanceProposalView(c *gin.Context, p governanceProposal) (gin.H, error) {
	var cast, yes, no, abstain int
	var eligible bool
	var submitted bool
	var choice *string
	err := mustTx(c).QueryRow(c.Request.Context(), `SELECT count(*) FILTER(WHERE cast_at IS NOT NULL),count(*) FILTER(WHERE choice='yes'),count(*) FILTER(WHERE choice='no'),count(*) FILTER(WHERE choice='abstain') FROM governance_voter WHERE proposal_id=$1 AND active`, p.ID).Scan(&cast, &yes, &no, &abstain)
	if err != nil {
		return nil, err
	}
	err = mustTx(c).QueryRow(c.Request.Context(), `SELECT true,choice,CASE WHEN $3='deliberating' THEN candidate IS NOT NULL ELSE opinion IS NOT NULL OR choice IS NOT NULL END FROM governance_voter WHERE proposal_id=$1 AND user_id=$2 AND active`, p.ID, mustActor(c).UserID, p.Status).Scan(&eligible, &choice, &submitted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	result := gin.H{"proposal": p, "participated": cast, "eligible": eligible, "myChoice": choice, "mySubmitted": submitted}
	if !governanceCase(p) {
		presentation, e := governancePresentation(c, p, eligible)
		if e != nil {
			return nil, e
		}
		for key, value := range presentation {
			result[key] = value
		}
	}
	if p.Status == "passed" || p.Status == "applied" || p.Status == "rejected" {
		result["tally"] = gin.H{"yes": yes, "no": no, "abstain": abstain}
	}
	if p.Action == "gpa" && !eligible && p.Author != mustActor(c).UserID {
		p.Payload = nil
		result["proposal"] = p
	}
	return result, nil
}

type governanceProposalInput struct {
	RequestID string          `json:"requestId"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Action    string          `json:"action"`
	Payload   json.RawMessage `json:"payload"`
}

func (s *Server) createGovernanceProposal(c *gin.Context) {
	var in governanceProposalInput
	if c.ShouldBindJSON(&in) != nil {
		writeError(c, 422, "invalid_request", "请填写提案内容", nil)
		return
	}
	request, err := uuid.Parse(in.RequestID)
	if err != nil || request == uuid.Nil || utf8.RuneCountInString(strings.TrimSpace(in.Title)) < 1 || utf8.RuneCountInString(in.Title) > 120 || utf8.RuneCountInString(strings.TrimSpace(in.Body)) < 4 || utf8.RuneCountInString(in.Body) > 10000 {
		writeError(c, 422, "invalid_proposal", "提案需要有效标识、标题和 4—10000 字依据", nil)
		return
	}
	if !governanceLock(c) {
		return
	}
	ctx, tx, actor := c.Request.Context(), mustTx(c), mustActor(c)
	var oldID int64
	var same bool
	raw, _ := json.Marshal(in)
	err = tx.QueryRow(ctx, `SELECT p.id,e.detail=$3::jsonb FROM governance_proposal p JOIN governance_event e ON e.proposal_id=p.id AND e.action='proposal.created' WHERE p.author_id=$1 AND p.request_id=$2`, actor.UserID, request.String(), raw).Scan(&oldID, &same)
	if err == nil {
		if !same {
			writeError(c, 409, "request_changed", "同一提案标识不能用于不同内容", nil)
			return
		}
		c.JSON(200, gin.H{"id": strconv.FormatInt(oldID, 10)})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	g, err := loadGovernance(ctx, tx, actor.ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	now := time.Now()
	if g.Mode == "centralized" {
		writeError(c, 409, "not_collective", "本班使用中心化配置", nil)
		return
	}
	members, err := governanceMembers(ctx, tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	joined := false
	for _, m := range members {
		if m.ID == actor.UserID && governance.Eligible(m, now) {
			joined = true
		}
	}
	if !joined {
		writeError(c, 403, "membership_required", "请主动加入共治并等待 24 小时后发起提案", nil)
		return
	}
	if in.Kind == "activate" {
		if g.Mode != "enrolling" || g.Close == nil || now.Before(*g.Close) {
			writeError(c, 409, "enrollment_open", "至少公示招募 7 天后才能表决启动", nil)
			return
		}
		reviewers := 0
		for _, m := range members {
			if governance.Eligible(m, now) && m.Reviewer {
				reviewers++
			}
		}
		profile, e := governance.ReviewProfile(reviewers)
		if e != nil {
			writeError(c, 409, "review_capacity", e.Error(), nil)
			return
		}
		in.Action = "activate"
		in.Payload, _ = json.Marshal(profile)
	} else if g.Mode != "collective" {
		writeError(c, 409, "not_active", "共治仍在招募，请先完成启动表决", nil)
		return
	}
	excluded, err := s.validateGovernanceAction(c, &in)
	if err != nil {
		writeError(c, 422, "proposal_invalid", err.Error(), nil)
		return
	}
	ids := governance.Electorate(members, now, excluded)
	roster, rosterHash, err := governanceRoster(ctx, tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	required, err := governance.Threshold(in.Kind, len(ids), roster)
	if err != nil {
		writeError(c, 422, "electorate_empty", err.Error(), nil)
		return
	}
	if required > len(ids) {
		writeError(c, 409, "quorum_unreachable", "当前参与人数不足以通过这类事项；请邀请更多成员自愿加入，不能降低全班保护门槛", gin.H{"eligible": len(ids), "required": required})
		return
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM governance_proposal WHERE author_id=$1 AND status IN ('discussion','voting','passed','blocked')`, actor.UserID).Scan(&pending); err != nil {
		writeServiceError(c, err)
		return
	}
	if pending >= 3 {
		writeError(c, 409, "proposal_limit", "请先处理已有提案，每人最多同时发起 3 项", nil)
		return
	}
	state, err := s.governanceActionState(c, in.Action, in.Payload)
	if err != nil {
		writeError(c, 409, "proposal_changed", "无法核验提案对象，请刷新后重试", nil)
		return
	}
	discussion, duration := 24*time.Hour, 48*time.Hour
	if in.Kind == "protected" || in.Kind == "activate" {
		discussion, duration = 48*time.Hour, 72*time.Hour
	}
	opens := now.Add(discussion)
	closes := opens.Add(duration)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO governance_proposal(class_id,author_id,request_id,kind,title,body,action,payload,governance_version,state_hash,roster_hash,roster_count,electorate_count,required_yes,opens_at,closes_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id`, actor.ClassID, actor.UserID, request.String(), in.Kind, in.Title, in.Body, in.Action, in.Payload, g.Version, state, rosterHash, roster, len(ids), required, opens, closes).Scan(&id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for _, uid := range ids {
		if _, err = tx.Exec(ctx, `INSERT INTO governance_voter(class_id,proposal_id,user_id) VALUES($1,$2,$3)`, actor.ClassID, id, uid); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err = governanceEvent(c, &id, "proposal.created", json.RawMessage(raw)); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10)})
}
func (s *Server) voteGovernance(c *gin.Context) {
	if _, mcp := c.Request.Context().Value(mcpCallKey{}).(mcpCall); mcp {
		writeError(c, 403, "human_vote_required", "表决须由本人在网页核对并提交", nil)
		return
	}
	var in struct {
		Choice string `json:"choice"`
		Lock   bool   `json:"lock"`
	}
	if c.ShouldBindJSON(&in) != nil || (in.Choice != "yes" && in.Choice != "no" && in.Choice != "abstain") {
		writeError(c, 422, "invalid_vote", "请选择赞成、反对或弃权", nil)
		return
	}
	if !governanceLock(c) {
		return
	}
	ctx, tx := c.Request.Context(), mustTx(c)
	p, err := scanGovernanceProposal(tx.QueryRow(ctx, governanceSelect+` WHERE id=$1 FOR UPDATE`, c.Param("id")))
	if err != nil {
		if !notFound(c, err, "提案") {
			writeServiceError(c, err)
		}
		return
	}
	if governanceCase(p) {
		writeError(c, 422, "case_review_required", "个人事项请通过评审席位提交认定", nil)
		return
	}
	if (p.Status != "discussion" && p.Status != "voting") || time.Now().Before(p.Opens) || !time.Now().Before(p.Closes) {
		writeError(c, 409, "vote_closed", "当前不在本提案投票时段", nil)
		return
	}
	_, roster, err := governanceRoster(ctx, tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if roster != p.RosterHash {
		writeError(c, 409, "roster_changed", "正式成员名单已经变化，需要重新发起提案；不会临时改变分母", nil)
		return
	}
	result, err := tx.Exec(ctx, `UPDATE governance_voter SET choice=$1,cast_at=now(),locked=$2 WHERE proposal_id=$3 AND user_id=$4 AND active AND NOT locked`, in.Choice, in.Lock, p.ID, mustActor(c).UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if result.RowsAffected() != 1 {
		writeError(c, 403, "not_eligible", "你不在本次冻结名单内，或已锁定本人的票；新加入仅适用于后续提案", nil)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status='voting' WHERE id=$1`, p.ID); err != nil {
		writeServiceError(c, err)
		return
	}
	// Choice is stored in the protected ballot table, not in public event data.
	if err = governanceEvent(c, &p.ID, "ballot.updated", gin.H{"locked": in.Lock}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"saved": true, "locked": in.Lock})
}
func (s *Server) closeGovernanceProposal(c *gin.Context) {
	if !governanceLock(c) {
		return
	}
	ctx, tx := c.Request.Context(), mustTx(c)
	p, err := scanGovernanceProposal(tx.QueryRow(ctx, governanceSelect+` WHERE id=$1 FOR UPDATE`, c.Param("id")))
	if err != nil {
		if !notFound(c, err, "提案") {
			writeServiceError(c, err)
		}
		return
	}
	if governanceCase(p) {
		if automatic, _ := c.Get("governance.automatic"); automatic != true {
			allowed, e := governanceCanRead(c, p)
			if e != nil {
				writeServiceError(c, e)
				return
			}
			if !allowed {
				writeError(c, 403, "forbidden", "无权处理本案", nil)
				return
			}
		}
		s.finishGovernanceCase(c, p)
		return
	}
	if p.Status == "applied" || p.Status == "rejected" || p.Status == "stale" {
		c.JSON(200, gin.H{"status": p.Status})
		return
	}
	var yes, locked int
	err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE choice='yes'),count(*) FILTER(WHERE locked AND cast_at IS NOT NULL) FROM governance_voter WHERE proposal_id=$1 AND active`, p.ID).Scan(&yes, &locked)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if time.Now().Before(p.Closes) && locked != p.Electorate {
		writeError(c, 409, "vote_pending", "投票截止或所有成员明确锁票后才能结票", nil)
		return
	}
	if time.Now().Before(p.Opens) {
		writeError(c, 409, "discussion_pending", "讨论期尚未结束", nil)
		return
	}
	_, roster, err := governanceRoster(ctx, tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	g, err := loadGovernance(ctx, tx, mustActor(c).ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	status := "passed"
	if yes < p.Required {
		status = "rejected"
	}
	if roster != p.RosterHash || g.Version != p.Version {
		status = "stale"
	}
	if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status=$1,decided_at=COALESCE(decided_at,now()) WHERE id=$2`, status, p.ID); err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "passed" {
		if err = governanceEvent(c, &p.ID, "proposal.closed", gin.H{"status": status, "yes": yes, "required": p.Required}); err != nil {
			writeServiceError(c, err)
			return
		}
		c.JSON(200, gin.H{"status": status})
		return
	}
	p.Status = "passed"
	s.executeGovernanceProposal(c, p)
}
