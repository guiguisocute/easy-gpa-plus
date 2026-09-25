package api

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"strings"
	"unicode/utf8"
)

func governanceCanRead(c *gin.Context, p governanceProposal) (bool, error) {
	if !governanceCase(p) {
		return true, nil
	}
	var allowed bool
	err := mustTx(c).QueryRow(c.Request.Context(), `SELECT $2=subject_id OR EXISTS(SELECT 1 FROM governance_voter WHERE proposal_id=$1 AND user_id=$2 AND active) FROM governance_proposal WHERE id=$1`, p.ID, mustActor(c).UserID).Scan(&allowed)
	return allowed, err
}
func (s *Server) governanceComments(c *gin.Context) {
	p, err := scanGovernanceProposal(mustTx(c).QueryRow(c.Request.Context(), governanceSelect+` WHERE id=$1`, c.Param("id")))
	if err != nil {
		if !notFound(c, err, "提案") {
			writeServiceError(c, err)
		}
		return
	}
	allowed, err := governanceCanRead(c, p)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !allowed {
		writeError(c, 403, "forbidden", "无权查看本案讨论", nil)
		return
	}
	var raw []byte
	err = mustTx(c).QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'body',body,'kind',kind,'createdAt',created_at,'mine',actor_id=$2) ORDER BY id),'[]') FROM (SELECT * FROM governance_comment WHERE proposal_id=$1 ORDER BY id DESC LIMIT 100) recent`, p.ID, mustActor(c).UserID).Scan(&raw)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"items": json.RawMessage(raw)})
}
func (s *Server) commentGovernance(c *gin.Context) {
	var in struct {
		Body string `json:"body"`
		Kind string `json:"kind"`
	}
	if c.ShouldBindJSON(&in) != nil || utf8.RuneCountInString(strings.TrimSpace(in.Body)) < 4 || utf8.RuneCountInString(in.Body) > 5000 {
		writeError(c, 422, "comment_invalid", "请填写 4—5000 字说明", nil)
		return
	}
	if !governanceLock(c) {
		return
	}
	ctx, tx := c.Request.Context(), mustTx(c)
	p, err := scanGovernanceProposal(tx.QueryRow(ctx, governanceSelect+` WHERE id=$1`, c.Param("id")))
	if err != nil {
		if !notFound(c, err, "提案") {
			writeServiceError(c, err)
		}
		return
	}
	allowed, err := governanceCanRead(c, p)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !allowed {
		writeError(c, 403, "forbidden", "无权参与本案讨论", nil)
		return
	}
	if p.Status == "applied" || p.Status == "rejected" || p.Status == "stale" {
		writeError(c, 409, "closed", "本事项已归档", nil)
		return
	}
	if governanceCase(p) && in.Kind != "evidence" {
		writeError(c, 409, "independent_review", "评审先独立提交意见，不能提前交流结论", nil)
		return
	}
	if in.Kind == "evidence" {
		if p.Subject == nil || *p.Subject != mustActor(c).UserID || !governanceCase(p) || utf8.RuneCountInString(in.Body) < 20 {
			writeError(c, 403, "subject_only", "仅当事人可提交至少 20 字的新事实和规则依据", nil)
			return
		}
		var round int
		if err = tx.QueryRow(ctx, `SELECT review_round FROM governance_proposal WHERE id=$1`, p.ID).Scan(&round); err != nil {
			writeServiceError(c, err)
			return
		}
		if round >= 2 {
			writeError(c, 409, "round_limit", "本案已完成一次补充复核，不能反复重投；保留待解决状态和申诉路径", nil)
			return
		}
	} else {
		in.Kind = "discussion"
	}
	var recent int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM governance_comment WHERE actor_id=$1 AND created_at>now()-interval '1 hour'`, mustActor(c).UserID).Scan(&recent); err != nil {
		writeServiceError(c, err)
		return
	}
	if recent >= 20 {
		writeError(c, 429, "comment_limit", "请整理意见后再发，每小时最多 20 条", nil)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO governance_comment(class_id,proposal_id,actor_id,body,kind) VALUES($1,$2,$3,$4,$5)`, mustActor(c).ClassID, p.ID, mustActor(c).UserID, strings.TrimSpace(in.Body), in.Kind); err != nil {
		writeServiceError(c, err)
		return
	}
	if in.Kind == "evidence" {
		s.restartGovernanceCase(c)
		return
	}
	c.JSON(201, gin.H{"saved": true})
}
