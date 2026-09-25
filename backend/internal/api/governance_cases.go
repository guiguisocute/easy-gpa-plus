package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"easygpa/backend/internal/governance"
	"easygpa/backend/internal/scheme"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type governanceCaseInput struct {
	Version  string   `json:"expectedVersion,omitempty"`
	TargetID jsonID   `json:"targetId"`
	Score    *float64 `json:"score,omitempty"`
	Category string   `json:"category,omitempty"`
	ItemKey  string   `json:"itemKey,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Decision string   `json:"decision,omitempty"`
	Action   string   `json:"action,omitempty"`
}
type governanceCaseData struct {
	Subject                     int64
	Title, Body, Category, Item string
	Score                       *float64
	Rule                        json.RawMessage
	Submission                  *int64
}

func governanceCaseTable(action string) (string, error) {
	switch action {
	case "submission", "appeal", "report", "objection":
		return action, nil
	}
	return "", errors.New("案件类型无效")
}
func (s *Server) governanceCaseState(c *gin.Context, action string, payload []byte) (string, error) {
	table, err := governanceCaseTable(action)
	if err != nil {
		return "", err
	}
	var input governanceCaseInput
	if json.Unmarshal(payload, &input) != nil || input.TargetID <= 0 {
		return "", errors.New("案件对象无效")
	}
	var raw []byte
	err = mustTx(c).QueryRow(c.Request.Context(), `SELECT jsonb_build_object('target',to_jsonb(t),'files',(SELECT jsonb_agg(jsonb_build_object('id',e.id,'status',e.status,'sha',e.sha256) ORDER BY e.id) FROM evidence e WHERE e.`+table+`_id=t.id),'scheme',(SELECT jsonb_agg(jsonb_build_object('id',id,'config',config)) FROM scheme WHERE status='published')) FROM `+table+` t WHERE t.id=$1`, int64(input.TargetID)).Scan(&raw)
	if err != nil {
		return "", err
	}
	d, err := loadGovernanceCaseData(c, action, int64(input.TargetID))
	if err != nil {
		return "", err
	}
	var underlying []byte
	if d.Submission != nil {
		err = mustTx(c).QueryRow(c.Request.Context(), `SELECT to_jsonb(s) FROM submission s WHERE id=$1`, *d.Submission).Scan(&underlying)
	} else {
		err = mustTx(c).QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY id),'[]') FROM base_score b WHERE student_id=$1 AND category_key=$2 AND item_key=$3`, d.Subject, d.Category, d.Item).Scan(&underlying)
	}
	if err != nil {
		return "", err
	}
	var supplement []byte
	if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',m.id,'body',m.body) ORDER BY m.id),'[]') FROM governance_comment m JOIN governance_proposal p ON p.id=m.proposal_id WHERE p.action=$1 AND p.target_id=$2 AND m.kind='evidence'`, action, int64(input.TargetID)).Scan(&supplement); err != nil {
		return "", err
	}
	return mcpDigest(append(append(raw, underlying...), supplement...)), nil
}
func loadGovernanceCaseData(c *gin.Context, action string, id int64) (governanceCaseData, error) {
	ctx, tx := c.Request.Context(), mustTx(c)
	var d governanceCaseData
	var err error
	switch action {
	case "submission":
		err = tx.QueryRow(ctx, `SELECT student_id,title,markdown_note,category_key,item_key,final_score::float8,rule_snapshot,id FROM submission WHERE id=$1`, id).Scan(&d.Subject, &d.Title, &d.Body, &d.Category, &d.Item, &d.Score, &d.Rule, &d.Submission)
	case "appeal":
		var kind string
		var target int64
		err = tx.QueryRow(ctx, `SELECT student_id,reason,target_type,target_id FROM appeal WHERE id=$1`, id).Scan(&d.Subject, &d.Body, &kind, &target)
		if err != nil {
			return d, err
		}
		original, e := loadAppealTarget(ctx, tx, kind, target, d.Subject, false)
		if e != nil {
			return d, e
		}
		d.Title = "成绩申诉"
		d.Category, d.Item, d.Score, d.Rule = original.Category, original.ItemKey, original.CurrentScore, original.RuleSnapshot
		if kind == "submission" {
			d.Submission = &target
		}
	case "report", "objection":
		table, _ := governanceCaseTable(action)
		var target *int64
		var kind string
		var schemeID int64
		textColumn := "basis"
		err = tx.QueryRow(ctx, `SELECT student_id,`+textColumn+`,kind,target_id,scheme_id,category_key,item_key,current_score::float8 FROM `+table+` WHERE id=$1`, id).Scan(&d.Subject, &d.Body, &kind, &target, &schemeID, &d.Category, &d.Item, &d.Score)
		if err != nil {
			return d, err
		}
		d.Title = "成绩复核"
		current, e := loadSchemeByID(ctx, tx, schemeID)
		if e != nil {
			return d, e
		}
		item, categoryName, found := governanceRuleItem(current.Config, d.Category, d.Item)
		if !found {
			return d, errors.New("当前规则不存在")
		}
		// No CapturedAt: this rule is the currently published one, not a snapshot
		// frozen at submission time, and the desk labels those two differently.
		d.Rule, _ = json.Marshal(ruleSnapshot{SchemeID: strconv.FormatInt(schemeID, 10), CategoryKey: d.Category, CategoryName: categoryName, Item: item})
		if kind == "submission" {
			d.Submission = target
		}
	default:
		err = errors.New("案件类型无效")
	}
	return d, err
}

func (s *Server) ensureGovernanceCase(c *gin.Context, action string, id int64, excludeActor bool) error {
	ctx, tx, actor := c.Request.Context(), mustTx(c), mustActor(c)
	g, err := loadGovernance(ctx, tx, actor.ClassID)
	if err != nil || g.Mode != "collective" {
		return err
	}
	if !governanceLock(c) {
		return errors.New("无法取得共治锁")
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governance_proposal WHERE action=$1 AND target_id=$2 AND status<>'stale')`, action, id).Scan(&exists); err != nil || exists {
		return err
	}
	d, err := loadGovernanceCaseData(c, action, id)
	if err != nil {
		return err
	}
	if action == "submission" {
		if _, err = tx.Exec(ctx, `UPDATE submission SET status='arbitrating',updated_at=now() WHERE id=$1 AND status='pending'`, id); err != nil {
			return err
		}
	}
	members, err := governanceMembers(ctx, tx)
	if err != nil {
		return err
	}
	excluded := map[int64]bool{d.Subject: true}
	if excludeActor {
		excluded[actor.UserID] = true
	}
	seats := 2
	kind := "review"
	if action == "appeal" {
		kind = "appeal"
		seats = 3
		if g.Profile != nil && *g.Profile == "standard" {
			seats = 5
		}
		{
			rows, e := tx.Query(ctx, `SELECT DISTINCT v.user_id FROM governance_voter v JOIN governance_proposal p ON p.id=v.proposal_id WHERE p.subject_id=$2 AND ((p.action='submission' AND p.target_id=$1) OR (p.action='report' AND p.target_id IN (SELECT id FROM report WHERE student_id=$2 AND category_key=$3 AND item_key=$4)) OR (p.action='objection' AND p.target_id IN (SELECT id FROM objection WHERE student_id=$2 AND category_key=$3 AND item_key=$4)))`, d.Submission, d.Subject, d.Category, d.Item)
			if e != nil {
				return e
			}
			for rows.Next() {
				var uid int64
				if e = rows.Scan(&uid); e != nil {
					rows.Close()
					return e
				}
				excluded[uid] = true
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
		}
	}
	seedBytes := make([]byte, 32)
	if _, err = rand.Read(seedBytes); err != nil {
		return err
	}
	seed := hex.EncodeToString(seedBytes)
	picked, drawErr := governance.Draw(members, time.Now(), excluded, seats, seed)
	if kind == "review" {
		reserve := 6
		if g.Profile != nil && *g.Profile == "standard" {
			reserve = 10
		}
		available := 0
		for _, m := range members {
			if governance.Eligible(m, time.Now()) && m.Reviewer && !excluded[m.ID] {
				available++
			}
		}
		if available < reserve {
			drawErr = errors.New("回避后评审池不足以同时保留本轮和独立申诉席位")
			picked = nil
		}
	}
	// A capacity shortage stays a visible case, with no fake or self-review seat.
	payload, _ := json.Marshal(governanceCaseInput{TargetID: jsonID(id)})
	hash, err := s.governanceCaseState(c, action, payload)
	if err != nil {
		return err
	}
	roster, rosterHash, err := governanceRoster(ctx, tx)
	if err != nil {
		return err
	}
	status := "voting"
	if drawErr != nil {
		status = "blocked"
	}
	var proposalID int64
	err = tx.QueryRow(ctx, `INSERT INTO governance_proposal(class_id,author_id,request_id,kind,title,body,action,payload,target_id,subject_id,governance_version,state_hash,roster_hash,roster_count,electorate_count,required_yes,status,opens_at,closes_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$2,$10,$11,$12,$13,$14,$15,$16,now(),now()+interval '48 hours') RETURNING id`, actor.ClassID, d.Subject, uuid.NewString(), kind, d.Title, "按现有规则独立核对材料并提交认定。", action, payload, id, g.Version, hash, rosterHash, roster, seats, seats/2+1, status).Scan(&proposalID)
	if err != nil {
		return err
	}
	for _, uid := range picked {
		if _, err = tx.Exec(ctx, `INSERT INTO governance_voter(class_id,proposal_id,user_id) VALUES($1,$2,$3)`, actor.ClassID, proposalID, uid); err != nil {
			return err
		}
	}
	detail, _ := json.Marshal(gin.H{"seed": seed, "seedCommitment": mcpDigest(seedBytes), "excluded": excluded, "seats": seats, "capacityBlocked": drawErr != nil, "poolSnapshot": members})
	// No reporter identity in public/audit events. Exclusions remain internal to
	// this case's assignment record, which has no member-facing read endpoint.
	_, err = tx.Exec(ctx, `INSERT INTO governance_event(class_id,proposal_id,action,detail) VALUES($1,$2,'case.assigned',$3)`, actor.ClassID, proposalID, detail)
	return err
}

func (s *Server) governanceCaseDetail(c *gin.Context) {
	p, err := scanGovernanceProposal(mustTx(c).QueryRow(c.Request.Context(), governanceSelect+` WHERE id=$1`, c.Param("id")))
	if err != nil {
		if !notFound(c, err, "评审案件") {
			writeServiceError(c, err)
		}
		return
	}
	var seat bool
	err = mustTx(c).QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM governance_voter WHERE proposal_id=$1 AND user_id=$2 AND active)`, p.ID, mustActor(c).UserID).Scan(&seat)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !governanceCase(p) || p.Target == nil || (!seat && (p.Subject == nil || *p.Subject != mustActor(c).UserID)) {
		writeError(c, 403, "forbidden", "只有当事人和当次评审可以读取本案", nil)
		return
	}
	d, err := loadGovernanceCaseData(c, p.Action, *p.Target)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	table, _ := governanceCaseTable(p.Action)
	var files []byte
	err = mustTx(c).QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'name',filename,'mediaType',media_type,'sizeBytes',size_bytes,'sha256',sha256,'status',status,'uploadedAt',created_at) ORDER BY id),'[]') FROM evidence WHERE status='ready' AND (`+table+`_id=$1 OR (submission_id=$2 AND kind='claim')) AND blind_assignment_id IS NULL AND review_report_id IS NULL AND (kind='claim' OR report_id IS NOT NULL OR created_by=$3 OR objection_id=$4)`, *p.Target, d.Submission, d.Subject, func() any {
		if p.Action == "objection" {
			return p.Target
		}
		return nil
	}()).Scan(&files)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var ownOpinion []byte
	_ = mustTx(c).QueryRow(c.Request.Context(), `SELECT opinion FROM governance_voter WHERE proposal_id=$1 AND user_id=$2`, p.ID, mustActor(c).UserID).Scan(&ownOpinion)
	currentHash, e := s.governanceCaseState(c, p.Action, p.Payload)
	if e != nil {
		writeServiceError(c, e)
		return
	}
	/* 当事人是谁，本案的评审和当事人本来就看得到——回避名单是按人算的。
	   隐藏的是评审和举报人的身份，那两样这里一个都不下发。 */
	var student, studentSID string
	if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT name,sid FROM app_user WHERE id=$1`, d.Subject).Scan(&student, &studentSID); err != nil {
		writeServiceError(c, err)
		return
	}
	/* 只报"交了几份"，不报谁交的、交了什么：独立意见交齐前互相不可见。 */
	var submitted int
	if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT count(*) FROM governance_voter WHERE proposal_id=$1 AND active AND opinion IS NOT NULL`, p.ID).Scan(&submitted); err != nil {
		writeServiceError(c, err)
		return
	}
	response := gin.H{"proposal": p, "title": d.Title, "body": d.Body, "category": d.Category, "itemKey": d.Item, "currentScore": d.Score, "ruleSnapshot": jsonRaw(d.Rule), "evidence": json.RawMessage(files), "canReview": seat, "myOpinion": jsonRaw(ownOpinion), "version": currentHash, "changed": currentHash != p.StateHash, "student": student, "studentId": studentSID, "submitted": submitted}
	/* 学生申报了什么、期望几分、什么时候交的——审核人不看这三样就没法判分。
	   中心化初审台一直有，共治这边原来一项都没有。 */
	if d.Submission != nil {
		var claim []byte
		var requested *float64
		var submittedAt *time.Time
		if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT claim,requested_score::float8,submitted_at FROM submission WHERE id=$1`, *d.Submission).Scan(&claim, &requested, &submittedAt); err != nil {
			writeServiceError(c, err)
			return
		}
		response["claim"] = jsonRaw(claim)
		response["requestedScore"] = requested
		response["submittedAt"] = submittedAt
	}
	if p.Status == "deliberating" || p.Status == "blocked" {
		var opinions []byte
		err = mustTx(c).QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('opinion',opinion,'reason',reason,'hash',user_id::text) ORDER BY md5(user_id::text || proposal_id::text)),'[]') FROM governance_voter WHERE proposal_id=$1 AND active AND opinion IS NOT NULL`, p.ID).Scan(&opinions)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if seat {
			// Candidate tokens are hashes, never reviewer identifiers.
			var candidates []struct {
				Opinion governanceCaseInput `json:"opinion"`
				Reason  string              `json:"reason"`
				Hash    string              `json:"hash"`
			}
			if json.Unmarshal(opinions, &candidates) != nil {
				writeError(c, 500, "invalid_opinions", "意见无法读取", nil)
				return
			}
			for i := range candidates {
				candidates[i].Hash = governanceCandidate(candidates[i].Opinion)
			}
			response["opinions"] = candidates
		}
	}
	c.JSON(200, response)
}
func (s *Server) decideGovernanceCase(c *gin.Context) {
	if _, mcp := c.Request.Context().Value(mcpCallKey{}).(mcpCall); mcp {
		writeError(c, 403, "human_decision_required", "认定须由本人在网页核对", nil)
		return
	}
	var in governanceCaseInput
	if c.ShouldBindJSON(&in) != nil || in.Score == nil || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 6 || utf8.RuneCountInString(in.Reason) > 5000 {
		writeError(c, 422, "opinion_invalid", "请填写认定分和 6—5000 字的依据", nil)
		return
	}
	if !governanceLock(c) {
		return
	}
	ctx, tx := c.Request.Context(), mustTx(c)
	p, err := scanGovernanceProposal(tx.QueryRow(ctx, governanceSelect+` WHERE id=$1 FOR UPDATE`, c.Param("id")))
	if err != nil {
		if !notFound(c, err, "案件") {
			writeServiceError(c, err)
		}
		return
	}
	if !governanceCase(p) || p.Target == nil || p.Status != "voting" || !time.Now().Before(p.Closes) {
		writeError(c, 409, "case_not_reviewable", "本轮已结束或需要共同评议", nil)
		return
	}
	d, err := loadGovernanceCaseData(c, p.Action, *p.Target)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if d.Subject == mustActor(c).UserID {
		writeError(c, 403, "avoid_self", "不能评审本人事项", nil)
		return
	}
	in.TargetID = jsonID(*p.Target)
	if in.Category == "" {
		in.Category = d.Category
	}
	if in.ItemKey == "" {
		in.ItemKey = d.Item
	}
	if p.Action != "submission" && (in.Category != d.Category || in.ItemKey != d.Item) {
		writeError(c, 422, "classification_fixed", "本次复核按受理时的小项裁定，不能借意见变更案件范围", nil)
		return
	}
	if in.Decision == "reject" && (p.Action == "submission" || p.Action == "objection") {
		in.Decision = "rejected"
	} else {
		in.Decision = "accepted"
	}
	in.Action = "adjust"
	if p.Action == "objection" && in.Decision == "rejected" {
		in.Action = "dismiss"
	}
	var rule ruleSnapshot
	if json.Unmarshal(d.Rule, &rule) != nil {
		writeError(c, 409, "rule_unavailable", "评分规则不可用", nil)
		return
	}
	if d.Submission != nil {
		target, e := loadClassificationTarget(ctx, tx, *d.Submission, in.Category, in.ItemKey, time.Now())
		if e != nil {
			writeError(c, 422, "classification_invalid", e.Error(), nil)
			return
		}
		rule.Item = target.Item
	} else if in.Category != d.Category || in.ItemKey != d.Item {
		writeError(c, 422, "classification_invalid", "本案件不能变更归类", nil)
		return
	}
	if in.Decision == "rejected" {
		zero := 0.0
		in.Score = &zero
	}
	if err = scoreWithinRule(rule.Item.ScoreRule, *in.Score); err != nil && in.Decision != "rejected" {
		writeError(c, 422, "score_invalid", err.Error(), nil)
		return
	}
	if p.Action == "report" {
		var row reportRecord
		if err = tx.QueryRow(ctx, "SELECT kind,current_score::float8 FROM report WHERE id=$1", *p.Target).Scan(&row.Kind, &row.CurrentScore); err != nil {
			writeServiceError(c, err)
			return
		}
		if err = validateReportFinalScore(row, *in.Score); err != nil {
			writeError(c, 422, "score_invalid", err.Error(), nil)
			return
		}
	}
	*in.Score = scheme.NewPoints(*in.Score).Float64()
	hash, err := s.governanceCaseState(c, p.Action, p.Payload)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if in.Version != hash {
		writeError(c, 409, "case_changed", "材料版本已变化，请刷新并重新核对后提交", nil)
		return
	}
	in.Version = ""
	var votes int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM governance_voter WHERE proposal_id=$1 AND opinion IS NOT NULL`, p.ID).Scan(&votes); err != nil {
		writeServiceError(c, err)
		return
	}
	if hash != p.StateHash && votes > 0 {
		writeError(c, 409, "evidence_changed", "证据已经变化，需重新开始本轮复核", nil)
		return
	}
	if hash != p.StateHash {
		if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET state_hash=$1 WHERE id=$2`, hash, p.ID); err != nil {
			writeServiceError(c, err)
			return
		}
		p.StateHash = hash
	}
	raw, _ := json.Marshal(in)
	result, err := tx.Exec(ctx, `UPDATE governance_voter SET opinion=$1,reason=$2,cast_at=now(),locked=true WHERE proposal_id=$3 AND user_id=$4 AND active AND opinion IS NULL`, raw, in.Reason, p.ID, mustActor(c).UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if result.RowsAffected() != 1 {
		writeError(c, 403, "seat_required", "本案没有你的有效评审席位，或你已提交", nil)
		return
	}
	if err = governanceEvent(c, &p.ID, "case.opinion_submitted", gin.H{"roundSeats": p.Electorate}); err != nil {
		writeServiceError(c, err)
		return
	}
	s.finishGovernanceCase(c, p)
}
func (s *Server) finishGovernanceCase(c *gin.Context, p governanceProposal) {
	if !governanceCase(p) || p.Target == nil {
		writeError(c, 422, "not_case", "不是评审案件", nil)
		return
	}
	if p.Status == "applied" {
		c.JSON(200, gin.H{"status": "applied"})
		return
	}
	if p.Status != "voting" && p.Status != "deliberating" && p.Status != "blocked" {
		writeError(c, 409, "case_closed", "本案已结束", nil)
		return
	}
	ctx, tx := c.Request.Context(), mustTx(c)
	if p.Status == "blocked" && len(p.Result) > 0 {
		c.JSON(200, gin.H{"status": "blocked", "notice": "本轮未形成多数，需要新的事实依据后重新复核"})
		return
	}
	rows, err := tx.Query(ctx, `SELECT user_id,opinion FROM governance_voter WHERE proposal_id=$1 AND active AND opinion IS NOT NULL ORDER BY user_id`, p.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	opinions := []governance.Opinion{}
	inputs := map[int64]governanceCaseInput{}
	for rows.Next() {
		var uid int64
		var raw []byte
		if err = rows.Scan(&uid, &raw); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		var in governanceCaseInput
		if json.Unmarshal(raw, &in) != nil || in.Score == nil {
			rows.Close()
			writeError(c, 500, "invalid_opinion", "保存的评审意见无效", nil)
			return
		}
		inputs[uid] = in
		opinions = append(opinions, governance.Opinion{Reviewer: uid, Decision: in.Decision, Score: int64(scheme.NewPoints(*in.Score)), Category: in.Category, Item: in.ItemKey})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	g, err := loadGovernance(ctx, tx, mustActor(c).ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	maximum := 3
	if g.Profile != nil && *g.Profile == "standard" {
		maximum = 5
	}
	if p.Kind == "appeal" {
		maximum = p.Electorate
	}
	if p.Status == "deliberating" {
		s.finishGovernanceBallot(c, p, inputs)
		return
	}
	outcome, err := governance.Reconcile(opinions, p.Electorate, maximum)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	switch outcome.State {
	case "decided":
		winner := inputs[outcome.Opinion.Reviewer]
		if winner.Decision != "rejected" {
			winner.Decision = "adjust"
		}
		if p.Action == "submission" || p.Action == "report" {
			if winner.Decision != "rejected" {
				winner.Decision = ""
			}
		}
		p.Payload, _ = json.Marshal(winner)
		state, e := s.governanceCaseState(c, p.Action, p.Payload)
		if e != nil {
			writeServiceError(c, e)
			return
		}
		if state != p.StateHash {
			writeError(c, 409, "case_changed", "裁决所依据的材料已变化，请重新复核", nil)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET payload=$1,status='passed',decided_at=now() WHERE id=$2`, p.Payload, p.ID); err != nil {
			writeServiceError(c, err)
			return
		}
		p.Status = "passed"
		s.executeGovernanceProposal(c, p)
	case "expand":
		s.expandGovernanceCase(c, p, outcome.Seats)
	case "deliberating":
		if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status='deliberating',closes_at=now()+interval '48 hours' WHERE id=$1`, p.ID); err != nil {
			writeServiceError(c, err)
			return
		}
		c.JSON(200, gin.H{"status": "deliberating", "notice": "没有同一裁决达到多数，请阅读匿名意见后选择有依据的方案"})
	default:
		if !time.Now().Before(p.Closes) || p.Status == "blocked" {
			s.expandGovernanceCase(c, p, p.Electorate)
			return
		}
		c.JSON(200, gin.H{"status": "voting", "submitted": len(opinions), "seats": p.Electorate})
	}
}
func (s *Server) expandGovernanceCase(c *gin.Context, p governanceProposal, seats int) {
	ctx, tx := c.Request.Context(), mustTx(c)
	members, err := governanceMembers(ctx, tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var assignment []byte
	if err = tx.QueryRow(ctx, `SELECT detail FROM governance_event WHERE proposal_id=$1 AND action='case.assigned' ORDER BY id LIMIT 1`, p.ID).Scan(&assignment); err != nil {
		writeServiceError(c, err)
		return
	}
	var info struct {
		Excluded map[int64]bool `json:"excluded"`
		Seed     string         `json:"seed"`
	}
	if json.Unmarshal(assignment, &info) != nil {
		writeError(c, 500, "assignment_missing", "抽签记录不可用", nil)
		return
	}
	if info.Excluded == nil {
		info.Excluded = map[int64]bool{}
	}
	rows, err := tx.Query(ctx, `SELECT user_id,opinion IS NOT NULL,active,replacement_count FROM governance_voter WHERE proposal_id=$1`, p.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	active := 0
	var expired []int64
	maxReplacement := 0
	for rows.Next() {
		var id int64
		var done, on bool
		var replacements int
		if err = rows.Scan(&id, &done, &on, &replacements); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		info.Excluded[id] = true
		maxReplacement = max(maxReplacement, replacements)
		if on {
			if !done && !time.Now().Before(p.Closes) {
				expired = append(expired, id)
			} else {
				active++
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if automatic, _ := c.Get("governance.automatic"); automatic == true && len(expired) > 0 && maxReplacement >= 2 {
		if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status='blocked' WHERE id=$1`, p.ID); err != nil {
			writeServiceError(c, err)
			return
		}
		c.JSON(200, gin.H{"status": "blocked", "notice": "本案已自动补位两次，需成员明确请求再次随机补位；保留已交意见"})
		return
	}
	if active >= seats {
		c.JSON(200, gin.H{"status": p.Status})
		return
	}
	picked, err := governance.Draw(members, time.Now(), info.Excluded, seats-active, info.Seed+fmt.Sprint(seats, active, maxReplacement))
	if p.Kind == "review" {
		g, e := loadGovernance(ctx, tx, mustActor(c).ClassID)
		if e != nil {
			writeServiceError(c, e)
			return
		}
		reserve := 3
		if g.Profile != nil && *g.Profile == "standard" {
			reserve = 5
		}
		available := 0
		for _, m := range members {
			if governance.Eligible(m, time.Now()) && m.Reviewer && !info.Excluded[m.ID] {
				available++
			}
		}
		if available < reserve-active+reserve {
			err = errors.New("补位后将没有足够的独立申诉成员，请补充自愿评审人员")
		}
	}
	if err != nil {
		if _, e := tx.Exec(ctx, `UPDATE governance_proposal SET status='blocked' WHERE id=$1`, p.ID); e != nil {
			writeServiceError(c, e)
			return
		}
		c.JSON(200, gin.H{"status": "blocked", "notice": err.Error()})
		return
	}
	for _, id := range expired {
		if _, err = tx.Exec(ctx, `UPDATE governance_voter SET active=false WHERE proposal_id=$1 AND user_id=$2`, p.ID, id); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if len(expired) > 0 {
		maxReplacement++
	}
	for _, id := range picked {
		if _, err = tx.Exec(ctx, `INSERT INTO governance_voter(class_id,proposal_id,user_id,replacement_count) VALUES($1,$2,$3,$4)`, mustActor(c).ClassID, p.ID, id, maxReplacement); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status='voting',electorate_count=$1,required_yes=$2,closes_at=now()+interval '48 hours' WHERE id=$3`, seats, seats/2+1, p.ID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err = governanceEvent(c, &p.ID, "case.expanded", gin.H{"seats": seats, "replaced": len(expired)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"status": "voting", "seats": seats})
}

func (s *Server) collectiveAfter(handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		code, body := capturedMCPHandler(c, handler)
		if code < 400 && !c.IsAborted() {
			var result struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(body, &result) == nil && result.ID != "" {
				id, err := strconv.ParseInt(result.ID, 10, 64)
				if err == nil {
					action := "submission"
					if strings.HasPrefix(c.FullPath(), "/api/v1/appeals") {
						action = "appeal"
					}
					if err = s.ensureGovernanceCase(c, action, id, false); err != nil {
						writeServiceError(c, err)
						return
					}
				}
			}
		}
		writeMCPJSON(c, code, body)
	}
}

// The category name travels with the item: the review desk labels the rule card
// with it, and an empty label reads as "no rule captured" rather than "unnamed".
func governanceRuleItem(config scheme.Config, categoryKey, itemKey string) (scheme.Item, string, bool) {
	for _, category := range config.Categories {
		if category.Key != categoryKey {
			continue
		}
		for _, item := range category.Items {
			if item.Key == itemKey {
				return item, category.Name, true
			}
		}
		for _, base := range category.BaseItems {
			if base.Key == itemKey {
				zero := 0.0
				full := base.Full
				return scheme.Item{Key: base.Key, Name: base.Name, ScoreRule: scheme.ScoreRule{Type: "free", Min: &zero, Max: &full}}, category.Name, true
			}
		}
		for _, penalty := range category.PenaltyItems {
			if penalty.Key == itemKey {
				low, high := -99999.0, 0.0
				return scheme.Item{Key: penalty.Key, Name: penalty.Name, ScoreRule: scheme.ScoreRule{Type: "free", Min: &low, Max: &high}}, category.Name, true
			}
		}
	}
	return scheme.Item{}, "", false
}
