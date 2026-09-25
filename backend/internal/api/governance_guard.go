package api

import (
	"net/http"
	"strings"
	"time"

	"easygpa/backend/internal/governance"
	"github.com/gin-gonic/gin"
)

func (s *Server) governanceGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.FullPath()
		// Serialize class mutations with proposal snapshots, including uploads,
		// self-rejection and initialization. RLS remains the tenant boundary.
		if c.Request.Method != http.MethodGet && !governanceLock(c) {
			return
		}
		g, err := loadGovernance(c.Request.Context(), mustTx(c), mustActor(c).ClassID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if g.Mode == "centralized" {
			c.Next()
			return
		}
		if g.Mode == "enrolling" {
			if c.Request.Method != http.MethodGet && (path == "/api/v1/submissions/:id/submit" || path == "/api/v1/reports" || strings.HasPrefix(path, "/api/v1/appeals") || strings.HasPrefix(path, "/api/v1/review/") || strings.Contains(path, "bonus-grants") || strings.Contains(path, "/arbitrate") || strings.Contains(path, "force-score") || strings.Contains(path, "force-reject") || path == "/api/v1/admin/settle") {
				writeError(c, 409, "governance_enrolling", "共治招募与启动表决尚未完成，暂不开始正式评定", nil)
				return
			}
			c.Next()
			return
		}
		if strings.HasPrefix(path, "/api/v1/agent/actions/") && strings.HasSuffix(path, "/apply") {
			writeError(c, 403, "collective_decision_required", "共治模式的管理变更须形成提案，Agent 不能代替成员决策", nil)
			return
		}
		if strings.HasPrefix(path, "/api/v1/admin/") || strings.HasPrefix(path, "/api/v1/review/") {
			writeError(c, 403, "collective_decision_required", "本班使用共治模式，请从班级共治查看任务并形成集体决议", nil)
			return
		}
		if path == "/mcp/files/:kind/:id" && c.Param("kind") == "export" {
			writeError(c, 403, "collective_decision_required", "共治导出文件需按决议在网页领取", nil)
			return
		}
		// Legacy class/group roles confer no ambient administrative access in
		// collective mode. Case seats and passed proposals grant narrow rights.
		actor := mustActor(c)
		actor.Role = "student"
		actor.IsDeputy = false
		c.Set(actorContextKey, actor)
		c.Next()
	}
}
func (s *Server) collectiveSettlement(c *gin.Context) {
	g, err := loadGovernance(c.Request.Context(), mustTx(c), mustActor(c).ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if g.Mode != "collective" {
		writeError(c, 403, "not_collective", "本班结算由班管发起", nil)
		return
	}
	members, err := governanceMembers(c.Request.Context(), mustTx(c))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	allowed := false
	for _, m := range members {
		if m.ID == mustActor(c).UserID && governance.Eligible(m, time.Now()) {
			allowed = true
		}
	}
	if !allowed {
		writeError(c, 403, "membership_required", "请加入共治后发起结算检查", nil)
		return
	}
	s.runSettlement(c)
}

func (s *Server) requireGovernanceMember() gin.HandlerFunc {
	return func(c *gin.Context) {
		g, err := loadGovernance(c.Request.Context(), mustTx(c), mustActor(c).ClassID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if g.Mode != "collective" {
			writeError(c, 403, "not_collective", "本班尚未启用共治", nil)
			return
		}
		var allowed bool
		if err = mustTx(c).QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM governance_member WHERE user_id=$1 AND left_at IS NULL AND joined_at<=now()-interval '24 hours')`, mustActor(c).UserID).Scan(&allowed); err != nil {
			writeServiceError(c, err)
			return
		}
		if !allowed {
			writeError(c, 403, "membership_required", "请自愿加入共治并等待 24 小时", nil)
			return
		}
		c.Next()
	}
}
