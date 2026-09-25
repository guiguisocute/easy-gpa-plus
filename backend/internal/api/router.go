package api

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/config"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/ratelimit"
)

type Server struct {
	cfg            *config.Config
	deps           Dependencies
	opsConfig      runtimeConfigStore
	activeRequests atomic.Int64
	passwordWork   atomic.Int64
	mcpTransfers   atomic.Int64
	mcpBusiness    *gin.Engine
	mcpHTTP        http.Handler
	mcpTools       []mcpToolSpec
}

type requestRateLimiter interface {
	Allow(context.Context, string, ratelimit.Rule) (ratelimit.Decision, error)
}

type runtimeConfigStore interface {
	Flags(context.Context) (opsconfig.Flags, error)
	Mail(context.Context) (opsconfig.Mail, error)
	AI(context.Context) (opsconfig.AI, error)
	Lifecycle(context.Context) (opsconfig.Lifecycle, error)
	BackupRemote(context.Context) (opsconfig.BackupRemote, error)
	Invalidate(string)
}

func NewRouter(cfg *config.Config, deps Dependencies) *gin.Engine {
	if cfg.AppEnv == "prod" {
		gin.SetMode(gin.ReleaseMode)
	}
	var runtimeConfig runtimeConfigStore
	if deps.Pools != nil && deps.Pools.Ops != nil {
		runtimeConfig = opsconfig.New(deps.Pools.Ops, 0)
	}
	s := &Server{cfg: cfg, deps: deps, opsConfig: runtimeConfig}
	if deps.GovernanceContext != nil {
		go s.governanceScheduler(deps.GovernanceContext)
	}
	r := gin.New()
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		panic("invalid trusted proxy configuration: " + err.Error())
	}
	r.Use(gin.Recovery(), requestIDMiddleware(), securityHeadersMiddleware(cfg.AppEnv == "prod"), requestBodyLimitMiddleware(), s.requestConcurrencyMiddleware(), s.ipRateLimitMiddleware())
	// environment is intentionally public: local E2E clients use it as a
	// fail-closed guard before sending credentials or mutating test data.
	health := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "environment": cfg.AppEnv})
	}
	r.GET("/healthz", health)
	// Vite proxies /api without rewriting it. This alias lets browser E2E prove
	// that the same-origin proxy is connected to a development API too.
	r.GET("/api/healthz", health)
	r.GET("/readyz", s.ready)

	v1 := r.Group("/api/v1")
	s.registerAuthRoutes(v1)
	v1.GET("/branding", s.publicBranding)
	v1.POST("/mail/opt-out", s.optOutMail)
	v1.POST("/mail/ses-events", s.mailSESEvent)

	secured := v1.Group("")
	secured.Use(s.authenticate(), s.tenantTransaction(), s.validateActorState(), s.maintenanceModeMiddleware())
	secured.GET("/me", s.me)
	secured.GET("/me/agent-connections", s.requireBusiness(), s.myAgentConnections)
	secured.POST("/me/agent-connections", s.requireBusiness(), s.passwordWorkMiddleware(), s.rateLimitByActor("mcp-create", ratelimit.Rule{Requests: 10, Period: time.Hour, Burst: 3}), s.createAgentConnection)
	secured.DELETE("/me/agent-connections/:id", s.requireBusiness(), s.revokeAgentConnection)
	secured.GET("/me/agent-operations", s.requireBusiness(), s.myAgentOperations)
	secured.POST("/me/agent-operations/:id/decision", s.requireBusiness(), s.decideAgentOperation)
	secured.PUT("/me/password", s.requireBusiness(), s.passwordWorkMiddleware(), s.changePassword)
	secured.GET("/me/emails", s.requireBusiness(), s.myEmails)
	secured.GET("/me/mail-preferences", s.requireBusiness(), s.myMailPreferences)
	secured.PUT("/me/mail-preferences", s.requireBusiness(), s.updateMyMailPreferences)
	secured.POST("/me/emails", s.requireBusiness(), s.addEmail)
	secured.POST("/me/emails/verify", s.requireBusiness(), s.verifyAddedEmail)
	secured.PUT("/me/emails/:id/primary", s.requireBusiness(), s.setPrimaryEmail)
	secured.DELETE("/me/emails/:id", s.requireBusiness(), s.deleteEmail)
	secured.POST("/me/view-as", s.requireBusiness(), s.viewAs)

	business := secured.Group("")
	business.Use(s.requireBusiness(), s.classLockdownMiddleware(), s.governanceGuard())
	business.GET("/governance", s.governanceState)
	business.PUT("/governance/config", s.configureGovernance)
	business.PUT("/governance/membership", s.joinGovernance)
	business.GET("/governance/proposals", s.governanceProposals)
	business.POST("/governance/proposals", s.createGovernanceProposal)
	business.POST("/governance/proposals/:id/vote", s.voteGovernance)
	business.POST("/governance/proposals/:id/close", s.closeGovernanceProposal)
	business.GET("/governance/proposals/:id/comments", s.governanceComments)
	business.POST("/governance/proposals/:id/comments", s.commentGovernance)
	business.GET("/governance/cases/:id", s.governanceCaseDetail)
	business.POST("/governance/cases/:id/opinion", s.decideGovernanceCase)
	business.POST("/governance/cases/:id/ballot", s.ballotGovernanceCase)
	business.POST("/governance/cases/:id/restart", s.restartGovernanceCase)
	business.GET("/governance/gate", s.adminGate)
	business.POST("/governance/settle", s.collectiveSettlement)
	collective := business.Group("/governance", s.requireGovernanceMember())
	collective.GET("/students", s.reviewStudents)
	collective.GET("/students/:uid/scorecard", s.reviewStudentScorecard)
	collective.GET("/exports/:job", s.governanceExport)
	collective.GET("/bonus-grants", s.bonusGrants)
	collective.POST("/bonus-grant-uploads", s.createBonusGrantUpload)
	collective.POST("/bonus-grant-uploads/:id/notes/presign", func(c *gin.Context) { s.presignNoteEvidence(c, noteOwnerBonusUpload) })
	collective.POST("/bonus-grant-uploads/:id/notes/:eid/complete", func(c *gin.Context) { s.completeNoteEvidence(c, noteOwnerBonusUpload) })
	collective.DELETE("/bonus-grant-uploads/:id/notes/:eid", func(c *gin.Context) { s.deleteNoteEvidence(c, noteOwnerBonusUpload) })
	// The collective desks are the ordinary ones with the same handlers behind
	// them; membership replaces the group role, and no extra rights come with it.
	collective.GET("/gpa", s.adminGPA)
	collective.GET("/score-history/:kind/:id", s.reviewerScoreHistory)
	collective.GET("/objections", s.reviewObjections)
	collective.POST("/objections", s.createObjection)
	collective.PUT("/objections/:id", s.updateObjection)
	collective.DELETE("/objections/:id", s.deleteObjection)
	collective.POST("/objections/submit", s.submitObjections)
	// No withdraw here on purpose: submitting already drew the seats and recorded
	// who must stand aside. Taking the objection back would leave that case open
	// with nothing to decide, and cancelling a drawn panel is not in this version.
	collective.POST("/objections/:id/notes/presign", s.presignObjectionNote)
	collective.POST("/objections/:id/notes/:eid/complete", s.completeObjectionNote)
	collective.DELETE("/objections/:id/notes/:eid", s.deleteObjectionNote)
	business.GET("/scheme/current", s.currentScheme)
	business.GET("/scheme/versions/:version", s.schemeVersion)
	business.GET("/window", s.window)
	business.GET("/class-resources", s.classResources)
	business.GET("/class-resources/:id/url", s.classResourceURL)
	business.GET("/submissions", s.submissions)
	business.POST("/submissions", s.createSubmission)
	business.GET("/submissions/:id", s.submission)
	business.PUT("/submissions/:id", s.updateSubmission)
	business.DELETE("/submissions/:id", s.deleteSubmission)
	business.POST("/submissions/:id/submit", s.collectiveAfter(s.submitSubmission))
	business.POST("/submissions/:id/withdraw", s.withdrawSubmission)
	business.POST("/submissions/:id/force-reject", s.selfForceRejectSubmission)
	/* 上传类限流必须比它保护的业务配额松，否则真正在起作用的是限流器而不是配额：
	   学生按方案允许的量传材料，会在中途被 429 打断，而配额一次都没用到。真正
	   兜底的是每日字节配额（evidence_daily_quota）与每批数量/体积上限，这里只
	   拦"一秒钟几百次"这种明显异常。
	   burst 10 意味着一条提交挂到第 11 份佐证就开始每 30 秒才放一份。 */
	uploadPresignRule := ratelimit.Rule{Requests: 300, Period: time.Hour, Burst: 40}
	business.POST("/submissions/:id/evidence/presign", s.rateLimitByActor("evidence-upload-presign", uploadPresignRule), s.presignSubmissionEvidence)
	business.POST("/submissions/:id/evidence/:eid/complete", s.completeSubmissionEvidence)
	business.DELETE("/submissions/:id/evidence/:eid", s.deleteSubmissionEvidence)
	business.POST("/submissions/:id/notes/presign", s.rateLimitByActor("evidence-upload-presign", uploadPresignRule), s.presignSubmissionNote)
	for _, owner := range []noteOwnerKind{noteOwnerReportReview} {
		base := "/" + string(owner) + "s/:id/notes"
		business.POST(base+"/presign", s.rateLimitByActor("evidence-upload-presign", uploadPresignRule), func(c *gin.Context) { s.presignNoteEvidence(c, owner) })
		business.POST(base+"/:eid/complete", func(c *gin.Context) { s.completeNoteEvidence(c, owner) })
		business.DELETE(base+"/:eid", func(c *gin.Context) { s.deleteNoteEvidence(c, owner) })
	}
	business.POST("/submissions/:id/notes/:eid/complete", s.completeSubmissionNote)

	business.GET("/ai/status", s.aiStatus)
	ai := business.Group("/ai")
	ai.Use(s.requireAI())
	ai.POST("/batches", s.rateLimitByActor("ai-batch-action", ratelimit.Rule{Requests: 10, Period: time.Hour, Burst: 3}), s.createAIBatch)
	/* 一批最多 MaterialMaxItems（默认 100）张、每天最多 MaterialDailyBatches
	   （默认 5）批，也就是一天 500 次 presign 都属于正常使用。原来 burst 20、
	   300/小时，等于第 21 张起每 12 秒才放一张：24 张的一批必然当场吃 429，
	   而这只是配额允许量的四分之一。burst 给到一整批装得下，速率覆盖一天的量。 */
	ai.POST("/batches/:id/assets/presign", s.rateLimitByActor("ai-upload-presign", ratelimit.Rule{Requests: 600, Period: time.Hour, Burst: 120}), s.presignAIAsset)
	ai.POST("/batches/:id/assets/:asset/complete", s.completeAIAsset)
	ai.DELETE("/batches/:id/assets/:asset", s.deleteAIAsset)
	ai.POST("/batches/:id/start", s.rateLimitByActor("ai-batch-action", ratelimit.Rule{Requests: 10, Period: time.Hour, Burst: 3}), s.startAIBatch)
	ai.POST("/batches/:id/recompose", s.rateLimitByActor("ai-batch-action", ratelimit.Rule{Requests: 10, Period: time.Hour, Burst: 3}), s.recomposeAIBatch)
	/* 停止归组不限流：它是等得不耐烦时的逃生口，卡在这里问"请稍后再试"没有道理。
	   它只置一个标记，重复点也不会有第二种后果。 */
	ai.POST("/batches/:id/stop-compose", s.stopComposeAIBatch)
	ai.GET("/batches/:id", s.aiBatch)
	ai.POST("/batches/:id/apply", s.applyAIBatch)
	ai.DELETE("/batches/:id", s.deleteAIBatch)

	business.GET("/agent/status", s.agentStatus)
	business.GET("/agent/context", s.agentPageContext)
	business.GET("/agent/conversations", s.agentConversations)
	business.POST("/agent/conversations", s.createAgentConversation)
	business.GET("/agent/conversations/:id", s.agentConversation)
	business.DELETE("/agent/conversations/:id", s.deleteAgentConversation)
	business.POST("/agent/conversations/:id/messages", s.rateLimitByActor("agent-message", ratelimit.Rule{Requests: 20, Period: time.Minute, Burst: 5}), s.createAgentMessage)
	business.POST("/agent/messages/:id/cancel", s.cancelAgentMessage)
	business.POST("/agent/attachments/presign", s.rateLimitByActor("agent-upload-presign", ratelimit.Rule{Requests: 120, Period: time.Hour, Burst: 10}), s.presignAgentAttachment)
	business.POST("/agent/attachments/:id/complete", s.completeAgentAttachment)
	business.GET("/agent/attachments/:id/url", s.agentAttachmentURL)
	business.DELETE("/agent/attachments/:id", s.deleteAgentAttachment)
	business.GET("/agent/sources/:documentId/:entryId", s.agentSource)
	business.GET("/agent/actions/:id", s.agentAction)
	business.POST("/agent/actions/:id/prepare", s.prepareAgentAction)
	business.POST("/agent/actions/:id/apply", s.applyAgentAction)
	business.POST("/agent/actions/:id/reject", s.rejectAgentAction)
	business.GET("/evidence/:eid/url", s.evidenceURL)
	business.GET("/me/base-items", s.myBaseItems)
	business.GET("/me/score", s.myScore)
	business.GET("/me/seal", s.mySeal)
	business.POST("/me/seal", s.sealMySubmissions)
	business.GET("/me/scorecard", s.myScorecard)
	business.POST("/me/scorecard/confirm", s.confirmMyResult)
	business.GET("/appeals", s.appeals)
	business.POST("/appeals/draft", s.createAppealDraft)
	business.POST("/appeals", s.collectiveAfter(s.createAppeal))
	business.GET("/appeals/:id", s.appeal)
	business.POST("/appeals/:id/submit", s.collectiveAfter(s.submitAppeal))
	business.POST("/appeals/:id/withdraw", s.withdrawAppeal)
	business.DELETE("/appeals/:id", s.deleteAppealDraft)
	business.POST("/appeals/:id/evidence/presign", s.rateLimitByActor("evidence-upload-presign", uploadPresignRule), s.presignAppealEvidence)
	business.POST("/appeals/:id/evidence/:eid/complete", s.completeAppealEvidence)
	business.DELETE("/appeals/:id/evidence/:eid", s.deleteAppealEvidence)
	business.POST("/appeals/:id/notes/presign", s.rateLimitByActor("evidence-upload-presign", uploadPresignRule), s.presignAppealNote)
	business.POST("/appeals/:id/notes/:eid/complete", s.completeAppealNote)
	business.DELETE("/appeals/:id/notes/:eid", s.deleteAppealNote)
	business.POST("/objections/:id/notes/presign", s.rateLimitByActor("evidence-upload-presign", uploadPresignRule), s.presignObjectionNote)
	business.POST("/objections/:id/notes/:eid/complete", s.completeObjectionNote)
	business.DELETE("/objections/:id/notes/:eid", s.deleteObjectionNote)
	business.DELETE("/submissions/:id/notes/:eid", s.deleteSubmissionNote)

	/* 学生匿名举报。放在 business 上而不是 reviewer 上——这正是这个功能的要点：
	   综测小组以外的普通学生也能用。开关在方案能力位 studentReport 上，
	   handler 里逐个 ensureCapability，关掉就只关入口，在途的照走。
	   举报限流比佐证上传严得多：一天十条是业务上限，这里拦的是脚本连点。 */
	reportRule := ratelimit.Rule{Requests: 40, Period: time.Hour, Burst: 6}
	business.GET("/class-penalties", s.classPenalties)
	business.GET("/class-penalties/:kind/:id/history", s.studentScoreHistory)
	business.POST("/reports", s.rateLimitByActor("student-report", reportRule), s.createReport)
	business.GET("/reports/mine", s.myReports)
	/* 举报的佐证。走的是和小组提案同一条 notes 通道，区别只有一条：
	   这一路不记上传人，审计也不记 actor——见 note_evidence_handlers.go。 */
	business.POST("/reports/:id/notes/presign", s.rateLimitByActor("evidence-upload-presign", uploadPresignRule), s.presignReportNote)
	business.POST("/reports/:id/notes/:eid/complete", s.completeReportNote)
	business.DELETE("/reports/:id/notes/:eid", s.deleteReportNote)

	admin := business.Group("/admin")
	admin.Use(requireMinimumRole("class_admin"))
	admin.GET("/scheme", s.adminSchemes)
	admin.POST("/scheme", s.createScheme)
	admin.GET("/templates", s.adminTemplates)
	admin.GET("/template-share-requests", s.adminTemplateShareRequests)
	admin.GET("/scheme/:id", s.adminScheme)
	admin.PUT("/scheme/:id", s.updateScheme)
	admin.DELETE("/scheme/:id", s.deleteScheme)
	admin.POST("/scheme/:id/publish", s.publishScheme)
	admin.POST("/scheme/:id/share", s.shareScheme)
	admin.GET("/class", s.adminClass)
	/* 时间线不再是方案的一部分，路由名跟着改。/window 保留一轮做别名，
	   让升级期间还缓存着旧前端的浏览器不至于当场报错；前端切完就删。 */
	admin.PUT("/timeline", s.updateTimeline)
	admin.PUT("/window", s.updateTimeline)
	admin.PUT("/window/capabilities", s.updateCapability)
	admin.GET("/seals", s.adminSeals)
	admin.POST("/seals/remind", s.remindUnsealed)
	admin.POST("/seals/:uid/unseal", s.unsealStudent)
	admin.GET("/whitelist", s.whitelist)
	admin.POST("/whitelist", s.addWhitelist)
	admin.POST("/whitelist/import", s.importWhitelist)
	admin.DELETE("/whitelist/:id", s.deleteWhitelist)
	admin.GET("/users", s.adminUsers)
	admin.GET("/bonus-grants", s.bonusGrants)
	admin.POST("/bonus-grants", s.createBonusGrant)
	admin.POST("/bonus-grant-uploads", s.createBonusGrantUpload)
	admin.POST("/bonus-grant-uploads/:id/notes/presign", func(c *gin.Context) { s.presignNoteEvidence(c, noteOwnerBonusUpload) })
	admin.POST("/bonus-grant-uploads/:id/notes/:eid/complete", func(c *gin.Context) { s.completeNoteEvidence(c, noteOwnerBonusUpload) })
	admin.DELETE("/bonus-grant-uploads/:id/notes/:eid", func(c *gin.Context) { s.deleteNoteEvidence(c, noteOwnerBonusUpload) })
	admin.GET("/deputy", s.classDeputy)
	admin.PUT("/deputy", s.updateClassDeputy)
	admin.POST("/users/reset-password", s.resetClassUserPasswords)
	admin.POST("/users/:id/reset-password", s.resetUserPassword)
	// Compatibility aliases for browsers that still have the previous frontend.
	admin.POST("/users/initialize", s.resetClassUserPasswords)
	admin.POST("/users/:id/initialize", s.resetUserPassword)
	admin.PUT("/users/:id/role", s.updateUserRole)
	admin.PUT("/users/:id/status", s.updateUserStatus)
	admin.GET("/dispatch", s.currentDispatch)
	admin.POST("/dispatch/preview", s.previewDispatch)
	admin.POST("/dispatch/run", s.runDispatch)
	admin.PUT("/dispatch/auto", s.updateDispatchAuto)
	admin.PUT("/dispatch/reviewers/:uid", s.updateDispatchReviewer)
	admin.GET("/dispatch/reviewers/:uid/queue", s.dispatchReviewerQueue)
	admin.POST("/dispatch/handover", s.handoverDispatch)
	admin.PUT("/dispatch/submissions/:id", s.reassignSubmission)
	admin.GET("/submissions", s.adminSubmissions)
	admin.GET("/adjudication-history", s.adminAdjudicationHistory)
	admin.GET("/adjudication-history/:id", s.adminAdjudicationHistoryDetail)
	admin.POST("/submissions/:id/arbitrate", s.arbitrateSubmission)
	admin.POST("/submissions/:id/force-reject", s.forceRejectSubmission)
	admin.POST("/submissions/:id/force-score", s.forceScoreSubmission)
	admin.POST("/submissions/:id/classification/suggest", s.adminSuggestClassification)
	admin.POST("/submissions/:id/classification/resolve", s.resolveClassification)
	admin.GET("/classification-suggestions", s.adminClassificationSuggestions)
	admin.POST("/submissions/:id/summarize", notImplemented("ai_disabled", "AI 冲突归纳在 M5 前保持关闭"))
	admin.GET("/appeals", s.adminAppeals)
	admin.POST("/appeals/:id/final", s.finalizeAppealRequest)
	admin.GET("/objections", s.adminObjections)
	admin.POST("/objections/:id/decide", s.decideAdminObjection)
	admin.POST("/objections/decide-batch", s.decideAdminObjectionsBatch)
	/* 举报只有两名复核人谈不拢时才到班管手上，所以这里没有"批量"——
	   升上来的每一条都恰好是有人不同意的那一条。 */
	admin.GET("/reports", s.adminReports)
	admin.POST("/reports/:id/final", s.finalizeReport)
	admin.POST("/gpa/import", s.importGPAFile)
	admin.POST("/gpa/paste", s.pasteGPA)
	admin.GET("/gpa", s.adminGPA)
	admin.GET("/gate", s.adminGate)
	admin.POST("/gate/force", s.forceGate)
	admin.POST("/settle", s.runSettlement)
	admin.GET("/stats", s.adminStats)
	admin.GET("/review-progress", s.adminReviewProgress)
	admin.POST("/review-progress/remind", s.remindOverdueReviews)
	admin.GET("/review-sla", s.reviewSLA)
	admin.PUT("/review-sla", s.updateReviewSLA)
	admin.POST("/export", s.requestExport)
	admin.POST("/export/parse", notImplemented("ai_disabled", "自然语言导出解析在 M5 前保持关闭"))
	admin.GET("/export/:job", s.exportJob)
	admin.GET("/audit-log", s.adminAuditLog)
	admin.GET("/knowledge", s.adminKnowledge)
	admin.PUT("/knowledge/policy", s.updateKnowledgePolicy)
	admin.POST("/knowledge/documents/presign", s.rateLimitByActor("knowledge-upload-presign", ratelimit.Rule{Requests: 120, Period: time.Hour, Burst: 10}), s.presignKnowledgeDocument)
	admin.POST("/knowledge/documents/:id/complete", s.completeKnowledgeDocument)
	admin.GET("/knowledge/documents/:id", s.knowledgeDocument)
	admin.PUT("/knowledge/documents/:id", s.updateKnowledgeDocument)
	admin.POST("/knowledge/documents/:id/reprocess", s.rateLimitByActor("knowledge-reprocess", ratelimit.Rule{Requests: 30, Period: time.Hour, Burst: 3}), s.reprocessKnowledgeDocument)
	admin.DELETE("/knowledge/documents/:id", s.deleteKnowledgeDocument)
	admin.GET("/knowledge/entries/:id", s.adminKnowledgeEntry)

	reviewer := business.Group("/review")
	reviewer.Use(requireMinimumRole("group"))
	// Reuse the adjudication handlers; their object-level checks restrict this
	// surface to administrators' own cases, without granting admin access.
	deputy := reviewer.Group("/deputy", requireDeputy())
	deputy.GET("/submissions", s.adminSubmissions)
	deputy.POST("/submissions/:id/arbitrate", s.arbitrateSubmission)
	deputy.POST("/submissions/:id/force-reject", s.forceRejectSubmission)
	deputy.POST("/submissions/:id/force-score", s.forceScoreSubmission)
	deputy.POST("/submissions/:id/classification/suggest", s.adminSuggestClassification)
	deputy.POST("/submissions/:id/classification/resolve", s.resolveClassification)
	deputy.GET("/classification-suggestions", s.adminClassificationSuggestions)
	deputy.GET("/appeals", s.adminAppeals)
	deputy.GET("/appeals/:id", s.appeal)
	deputy.POST("/appeals/:id/final", s.finalizeAppealRequest)
	deputy.GET("/objections", s.adminObjections)
	deputy.POST("/objections/:id/decide", s.decideAdminObjection)
	deputy.POST("/objections/decide-batch", s.decideAdminObjectionsBatch)
	deputy.GET("/reports", s.adminReports)
	deputy.POST("/reports/:id/final", s.finalizeReport)
	reviewer.GET("/tasks", s.reviewTasks)
	reviewer.GET("/tasks/:id", s.reviewTask)
	reviewer.POST("/tasks/:id/decision", s.decideReviewTask)
	reviewer.PUT("/tasks/:id/decision", s.reviseReviewTask)
	reviewer.POST("/tasks/:id/suggest", notImplemented("ai_disabled", "AI 预审在 M5 前保持关闭"))
	reviewer.GET("/category", s.reviewCategory)
	reviewer.GET("/history", s.reviewHistory)
	reviewer.GET("/appeals", s.reviewerAppeals)
	reviewer.GET("/appeals/:id", s.appeal)
	reviewer.POST("/appeals/:id/rereview", s.resolveAppeal)
	reviewer.GET("/students", s.reviewStudents)
	reviewer.GET("/students/:uid/scorecard", s.reviewStudentScorecard)
	reviewer.GET("/score-history/:kind/:id", s.reviewerScoreHistory)
	/* 举报复核。任务本身在 /review/tasks 里以 type='report' 出现，
	   详情和结论走这两条独立的路——id 与 submission 不共享命名空间。 */
	reviewer.GET("/reports/:id", s.reviewReport)
	reviewer.GET("/reports/:id/target", s.reportTargetDetail)
	reviewer.POST("/reports/:id/decision", s.decideReport)
	reviewer.GET("/objections", s.reviewObjections)
	reviewer.POST("/objections", s.createObjection)
	reviewer.PUT("/objections/:id", s.updateObjection)
	reviewer.DELETE("/objections/:id", s.deleteObjection)
	reviewer.POST("/objections/submit", s.submitObjections)
	reviewer.POST("/objections/:id/withdraw", s.withdrawObjection)

	s.registerOpsRoutes(secured)
	s.setupMCP(r)
	r.NoRoute(s.noRoute)

	return r
}

func (s *Server) ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if s.deps.Pools == nil || s.deps.Pools.App == nil || s.deps.Redis == nil {
		writeError(c, http.StatusServiceUnavailable, "not_ready", "依赖尚未初始化", nil)
		return
	}
	if err := s.deps.Pools.App.Ping(ctx); err != nil {
		writeError(c, http.StatusServiceUnavailable, "database_unavailable", "数据库不可用", nil)
		return
	}
	if err := s.deps.Redis.Ping(ctx).Err(); err != nil {
		writeError(c, http.StatusServiceUnavailable, "redis_unavailable", "Redis 不可用", nil)
		return
	}
	if s.deps.Objects == nil || s.deps.Objects.Ready(ctx) != nil {
		writeError(c, http.StatusServiceUnavailable, "object_store_unavailable", "对象存储不可用", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
