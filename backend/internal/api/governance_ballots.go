package api

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
)

// Hash only the decision, never the reviewer or their wording. Votes for equal
// outcomes therefore count together without revealing a reviewer identifier.
func governanceCandidate(in governanceCaseInput) string {
	in.Reason = ""
	raw, _ := json.Marshal(in)
	return mcpDigest(raw)
}

func (s *Server) ballotGovernanceCase(c *gin.Context) {
	if _, ok := c.Request.Context().Value(mcpCallKey{}).(mcpCall); ok {
		writeError(c, 403, "human_vote_required", "评议须由本人核对", nil)
		return
	}
	var input struct {
		Candidate string `json:"candidate"`
	}
	if c.ShouldBindJSON(&input) != nil || (len(input.Candidate) != 64 && input.Candidate != "abstain") {
		writeError(c, 422, "invalid_ballot", "请选择裁决方案或弃权", nil)
		return
	}
	if !governanceLock(c) {
		return
	}
	ctx, tx := c.Request.Context(), mustTx(c)
	p, err := scanGovernanceProposal(tx.QueryRow(ctx, governanceSelect+` WHERE id=$1 FOR UPDATE`, c.Param("id")))
	if err != nil {
		if !notFound(c, err, "评审案件") {
			writeServiceError(c, err)
		}
		return
	}
	if !governanceCase(p) || p.Status != "deliberating" || !time.Now().Before(p.Closes) {
		writeError(c, 409, "ballot_closed", "当前不在共同评议时段", nil)
		return
	}
	rows, err := tx.Query(ctx, `SELECT opinion FROM governance_voter WHERE proposal_id=$1 AND active AND opinion IS NOT NULL`, p.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	valid := input.Candidate == "abstain"
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var in governanceCaseInput
		if json.Unmarshal(raw, &in) == nil && governanceCandidate(in) == input.Candidate {
			valid = true
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !valid {
		writeError(c, 422, "candidate_missing", "候选裁决不属于本轮冻结意见", nil)
		return
	}
	result, err := tx.Exec(ctx, `UPDATE governance_voter SET candidate=$1 WHERE proposal_id=$2 AND user_id=$3 AND active AND opinion IS NOT NULL AND candidate IS NULL`, input.Candidate, p.ID, mustActor(c).UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if result.RowsAffected() != 1 {
		writeError(c, 403, "seat_required", "只能由本轮评审本人投一票", nil)
		return
	}
	if err = governanceEvent(c, &p.ID, "case.ballot_cast", gin.H{}); err != nil {
		writeServiceError(c, err)
		return
	}
	s.finishGovernanceCase(c, p)
}

func (s *Server) finishGovernanceBallot(c *gin.Context, p governanceProposal, inputs map[int64]governanceCaseInput) {
	ctx, tx := c.Request.Context(), mustTx(c)
	rows, err := tx.Query(ctx, `SELECT candidate,count(*) FROM governance_voter WHERE proposal_id=$1 AND active AND candidate IS NOT NULL GROUP BY candidate`, p.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	count := 0
	winner := ""
	for rows.Next() {
		var key string
		var n int
		if err = rows.Scan(&key, &n); err != nil {
			break
		}
		count += n
		if key != "abstain" && n >= p.Required {
			winner = key
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if count < p.Electorate && time.Now().Before(p.Closes) {
		c.JSON(200, gin.H{"status": "deliberating", "participated": count})
		return
	}
	if winner == "" {
		if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status='blocked',result='{"reason":"no_majority"}' WHERE id=$1`, p.ID); err != nil {
			writeServiceError(c, err)
			return
		}
		c.JSON(200, gin.H{"status": "blocked", "notice": "未形成同一裁决的多数，须补充事实或规则依据后再审；不会自动平均或交给班管"})
		return
	}
	for _, in := range inputs {
		if governanceCandidate(in) != winner {
			continue
		}
		if p.Action == "appeal" {
			in.Decision = "adjust"
		}
		payload, _ := json.Marshal(in)
		hash, e := s.governanceCaseState(c, p.Action, payload)
		if e != nil {
			writeServiceError(c, e)
			return
		}
		if hash != p.StateHash {
			writeError(c, 409, "case_changed", "案件依据已变化，请开启新版本复核", nil)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET payload=$1,status='passed',decided_at=now() WHERE id=$2`, payload, p.ID); err != nil {
			writeServiceError(c, err)
			return
		}
		p.Payload = payload
		p.Status = "passed"
		s.executeGovernanceProposal(c, p)
		return
	}
	writeError(c, 409, "candidate_missing", "裁决方案已失效", nil)
}

func (s *Server) restartGovernanceCase(c *gin.Context) {
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
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT $2=subject_id OR EXISTS(SELECT 1 FROM governance_voter WHERE proposal_id=$1 AND user_id=$2 AND active) FROM governance_proposal WHERE id=$1`, p.ID, mustActor(c).UserID).Scan(&allowed); err != nil {
		writeServiceError(c, err)
		return
	}
	if !allowed || !governanceCase(p) || p.Target == nil || !(p.Status == "blocked" || p.Status == "voting" || p.Status == "deliberating") {
		writeError(c, 403, "restart_forbidden", "只有本案当事人或评审可对未结案件请求新版本", nil)
		return
	}
	var round int
	if err = tx.QueryRow(ctx, "SELECT review_round FROM governance_proposal WHERE id=$1", p.ID).Scan(&round); err != nil {
		writeServiceError(c, err)
		return
	}
	if round >= 2 {
		writeError(c, 409, "round_limit", "本案已补充复核一次，保留未解决状态，不能反复重投", nil)
		return
	}
	hash, err := s.governanceCaseState(c, p.Action, p.Payload)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if hash == p.StateHash {
		writeError(c, 409, "no_new_facts", "案件没有新的事实依据，不能重复投票直到产生期望结果", nil)
		return
	}
	// Archive the entire round before clearing ballots; do not replace the panel.
	if _, err = tx.Exec(ctx, `INSERT INTO governance_event(class_id,proposal_id,actor_id,action,detail) SELECT $1,$2,$3,'case.round_archived',jsonb_build_object('stateHash',$4::text,'opinions',COALESCE(jsonb_agg(to_jsonb(v)),'[]')) FROM governance_voter v WHERE proposal_id=$2`, mustActor(c).ClassID, p.ID, mustActor(c).UserID, p.StateHash); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE governance_voter SET opinion=NULL,reason=NULL,cast_at=NULL,locked=false,candidate=NULL WHERE proposal_id=$1 AND active`, p.ID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE governance_proposal SET status='voting',state_hash=$1,result=NULL,review_round=review_round+1,closes_at=now()+interval '48 hours' WHERE id=$2`, hash, p.ID); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"status": "voting", "notice": "已保留原小组重新核对新版本，旧意见留档"})
}
