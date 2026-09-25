package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type mcpToolSpec struct {
	Name, Title, Description, Scope, Role, Method, Path string
	Handler                                             gin.HandlerFunc
	Body                                                map[string]any
	Approval, Deputy, RequireVersion                    bool
	Resource                                            string
}

func (t mcpToolSpec) Params() []string {
	out := []string{}
	for _, p := range strings.Split(t.Path, "/") {
		if strings.HasPrefix(p, ":") {
			out = append(out, p[1:])
		}
	}
	return out
}
func (t mcpToolSpec) Schema() map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, p := range t.Params() {
		pattern := "^[1-9][0-9]*$"
		if p == "job" || p == "upload" {
			pattern = "^[a-zA-Z0-9_-]{1,100}$"
		}
		properties[p] = map[string]any{"type": "string", "pattern": pattern, "description": "从读取工具返回的资源 ID"}
		required = append(required, p)
	}
	if t.Method == http.MethodGet {
		properties["query"] = map[string]any{"type": "object", "description": "筛选与分页参数，值均为字符串；每页 limit 不超过 100。", "additionalProperties": map[string]any{"type": "string", "maxLength": 500}}
	} else {
		properties["idempotencyKey"] = map[string]any{"type": "string", "pattern": "^[a-zA-Z0-9_.:-]{8,100}$", "description": "每个新操作生成唯一 UUID，网络重试时复用；禁止用同一键提交不同内容。"}
		required = append(required, "idempotencyKey")
		if t.Body != nil {
			properties["input"] = t.Body
			required = append(required, "input")
		}
		if t.RequireVersion {
			properties["expectedVersion"] = map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$", "description": "刚读取详情返回的 resourceVersion"}
			required = append(required, "expectedVersion")
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func mcpBody(description, fields string, required ...string) map[string]any {
	properties := map[string]any{}
	for _, field := range strings.Fields(fields) {
		name, kind, _ := strings.Cut(field, ":")
		property := map[string]any{"type": kind}
		if kind == "array" {
			property["items"] = map[string]any{}
		}
		properties[name] = property
	}
	return map[string]any{"type": "object", "description": description, "properties": properties, "required": required, "additionalProperties": true}
}
func (s *Server) mcpToolRegistry() []mcpToolSpec {
	out := []mcpToolSpec{}
	read := func(name, title, role, path string, handler gin.HandlerFunc) {
		out = append(out, mcpToolSpec{Name: name, Title: title, Description: title + "；仅返回账号当前可见的数据。", Scope: "read", Role: role, Method: "GET", Path: "/api/v1" + path, Handler: handler})
	}
	write := func(name, title, scope, role, method, path string, body map[string]any, approval bool, handler gin.HandlerFunc) {
		out = append(out, mcpToolSpec{Name: name, Title: title, Description: title + "；沿用网页业务权限、窗口与状态限制。", Scope: scope, Role: role, Method: method, Path: "/api/v1" + path, Body: body, Approval: approval, Handler: handler})
	}
	submission := mcpBody("category 与 itemKey 来自当前方案；claim 使用该小项的数量、档位或自报分结构。", "category:string itemKey:string title:string claim:object note:string", "category", "itemKey", "claim")
	reason := mcpBody("处理理由，保留引用与依据。", "reason:string", "reason")
	decision := mcpBody("decision 为 accept/adjust/reject，按任务返回的规则填写认定分与具体理由。", "decision:string score:number reason:string spentSeconds:integer classificationSuggestion:object", "decision", "reason")
	arbitration := mcpBody("当前小项与裁定分数及理由，不能处理本人事项。", "category:string itemKey:string score:number reason:string", "score", "reason")
	read("account.me", "当前账号", "student", "/me", s.me)
	read("scheme.current", "当前评分规则", "student", "/scheme/current", s.currentScheme)
	read("class.window", "班级窗口与业务状态", "student", "/window", s.window)
	read("class.resources", "班级公开资料", "student", "/class-resources", s.classResources)
	read("scores.mine", "我的实时成绩与排名", "student", "/me/score", s.myScore)
	read("scores.scorecard", "我的成绩明细与待处理事项", "student", "/me/scorecard", s.myScorecard)
	read("submissions.list", "我的提交", "student", "/submissions", s.submissions)
	read("submissions.get", "我的提交详情", "student", "/submissions/:id", s.submission)
	out[len(out)-1].Resource = "submission"
	read("evidence.read", "获取佐证下载地址", "student", "/evidence/:eid/url", s.mcpEvidenceLink)
	read("resources.read", "获取班级资料下载地址", "student", "/class-resources/:id/url", s.mcpResourceLink)
	read("appeals.list", "我的申诉", "student", "/appeals", s.appeals)
	read("appeals.get", "可见申诉详情", "student", "/appeals/:id", s.appeal)
	write("submissions.draft", "创建材料草稿", "draft", "student", "POST", "/submissions", submission, false, s.createSubmission)
	write("submissions.update", "修改材料草稿", "draft", "student", "PUT", "/submissions/:id", submission, false, s.updateSubmission)
	out[len(out)-1].Resource = "submission"
	out[len(out)-1].RequireVersion = true
	write("submissions.delete", "删除本人草稿", "draft", "student", "DELETE", "/submissions/:id", nil, false, s.deleteSubmission)
	out[len(out)-1].Resource = "submission"
	out[len(out)-1].RequireVersion = true
	write("evidence.prepare_upload", "准备上传佐证", "draft", "student", "POST", "/submissions/:id/evidence/presign", mcpBody("计算本地文件大小与 SHA-256。最大 64 MB；平台与小项规则可以设置更小上限。返回 PUT 地址，使用相同 MCP Bearer 发送原始字节。", "filename:string mediaType:string sizeBytes:integer sha256:string", "filename", "mediaType", "sizeBytes", "sha256"), false, s.mcpPrepareUpload)
	write("evidence.complete_upload", "完成佐证上传", "draft", "student", "POST", "/mcp-upload/:upload/complete", nil, false, s.mcpCompleteUpload)
	write("submissions.submit", "正式提交材料", "submit", "student", "POST", "/submissions/:id/submit", nil, false, s.collectiveAfter(s.submitSubmission))
	write("submissions.withdraw", "撤回本人材料", "submit", "student", "POST", "/submissions/:id/withdraw", nil, false, s.withdrawSubmission)
	write("scores.acknowledge", "核对当前成绩版本", "submit", "student", "POST", "/me/scorecard/confirm", mcpBody("revision 使用 scores.scorecard 返回的实时版本；核对不阻塞结算，不取消申诉权。", "revision:string", "revision"), false, s.confirmMyResult)
	write("appeals.draft", "创建申诉草稿", "draft", "student", "POST", "/appeals/draft", mcpBody("targetType 为 submission/base_score/penalty_score，targetId 使用成绩或轨迹返回的目标 ID。", "targetType:string targetId:string", "targetType", "targetId"), false, s.createAppealDraft)
	write("appeals.submit", "提交本人申诉", "submit", "student", "POST", "/appeals/:id/submit", mcpBody("填写申诉理由与建议小项和分数。", "reason:string category:string itemKey:string score:number", "reason"), false, s.collectiveAfter(s.submitAppeal))
	read("governance.state", "共治参与与门槛", "student", "/governance", s.governanceState)
	read("governance.proposals", "可见共治事项", "student", "/governance/proposals", s.governanceProposals)
	read("governance.case", "当次获授权的评审案件", "student", "/governance/cases/:id", s.governanceCaseDetail)
	write("appeals.withdraw", "撤回本人申诉", "submit", "student", "POST", "/appeals/:id/withdraw", nil, false, s.withdrawAppeal)
	read("reviews.mine", "我的审核任务", "group", "/review/tasks", s.reviewTasks)
	read("reviews.context", "我的单项审核上下文", "group", "/review/tasks/:id", s.reviewTask)
	read("reviews.appeals", "我的申诉复评任务", "group", "/review/appeals", s.reviewerAppeals)
	read("reviews.history", "本人审核历史", "group", "/review/history", s.reviewHistory)
	read("reviews.students", "小组可见班级成员", "group", "/review/students", s.reviewStudents)
	read("reviews.scorecard", "小组可见成员计分表", "group", "/review/students/:uid/scorecard", s.reviewStudentScorecard)
	read("reviews.reports", "举报复核上下文（队列 report: 前缀后的数字 ID）", "group", "/review/reports/:id", s.reviewReport)
	read("reviews.objections", "扣分与异议提案", "group", "/review/objections", s.reviewObjections)
	write("reviews.decide", "提交独立审核结论", "review", "group", "POST", "/review/tasks/:id/decision", decision, false, s.decideReviewTask)
	write("reviews.appeal_decide", "提交本人申诉复评", "review", "group", "POST", "/review/appeals/:id/rereview", mcpBody("当前 reviewing 第一轮任务的独立复评结论，decision 为 uphold/adjust。", "decision:string category:string itemKey:string score:number reason:string spentSeconds:integer", "decision", "score", "reason"), false, s.resolveAppeal)
	write("reviews.report_decide", "提交举报复核结论", "review", "group", "POST", "/review/reports/:id/decision", mcpBody("decision 为 uphold/adjust/reject。", "decision:string score:number reason:string spentSeconds:integer", "decision", "reason"), false, s.decideReport)
	write("reviews.objection_draft", "创建扣分或异议提案", "review", "group", "POST", "/review/objections", mcpBody("kind 为 penalty/base/submission，使用成员 ID、目标 ID 和当前小项规则，填写拟定分数与依据。", "kind:string studentUserId:string category:string itemKey:string targetId:string proposedScore:number quantity:number basis:string", "kind", "studentUserId", "category", "itemKey", "proposedScore", "basis"), false, s.createObjection)
	write("reviews.objections_submit", "提交本人异议草稿", "review", "group", "POST", "/review/objections/submit", mcpBody("要正式提交的本人草稿 ID。", "ids:array", "ids"), false, s.submitObjections)
	read("class.settings", "班级设置", "class_admin", "/admin/class", s.adminClass)
	read("class.members", "班级成员", "class_admin", "/admin/users", s.adminUsers)
	read("class.roster", "班级注册名单", "class_admin", "/admin/whitelist", s.whitelist)
	read("class.report", "班级统计与实时排名", "class_admin", "/admin/stats", s.adminStats)
	read("class.progress", "审核进度与明细", "class_admin", "/admin/review-progress", s.adminReviewProgress)
	read("class.submissions", "班级材料与认定分", "class_admin", "/admin/submissions", s.adminSubmissions)
	read("class.appeals", "待处理申诉", "class_admin", "/admin/appeals", s.adminAppeals)
	read("class.objections", "待裁定异议", "class_admin", "/admin/objections", s.adminObjections)
	read("class.reports", "待裁定举报", "class_admin", "/admin/reports", s.adminReports)
	read("class.gpa", "专业素质分", "class_admin", "/admin/gpa", s.adminGPA)
	read("class.gate", "结算条件", "class_admin", "/admin/gate", s.adminGate)
	read("class.dispatch", "审核分发情况", "class_admin", "/admin/dispatch", s.currentDispatch)
	read("class.schemes", "班级评分方案列表", "class_admin", "/admin/scheme", s.adminSchemes)
	read("class.scheme_get", "评分方案详情", "class_admin", "/admin/scheme/:id", s.adminScheme)
	read("class.audit", "班级审计记录", "class_admin", "/admin/audit-log", s.adminAuditLog)
	write("class.arbitrate", "裁定材料分歧", "manage", "class_admin", "POST", "/admin/submissions/:id/arbitrate", arbitration, true, s.arbitrateSubmission)
	write("class.force_score", "强制修改认定分", "manage", "class_admin", "POST", "/admin/submissions/:id/force-score", mcpBody("必须填写原认定分、新分数及依据。", "previousScore:number score:number reason:string", "previousScore", "score", "reason"), true, s.forceScoreSubmission)
	write("class.force_reject", "强制驳回材料", "manage", "class_admin", "POST", "/admin/submissions/:id/force-reject", reason, true, s.forceRejectSubmission)
	write("class.bonus", "直接加分", "manage", "class_admin", "POST", "/admin/bonus-grants", mcpBody("requestId 使用 UUID，schemeVersion 来自当前方案；studentIds 为成员 ID，claim 符合小项规则。", "requestId:string schemeVersion:string studentIds:array category:string itemKey:string title:string claim:object note:string", "requestId", "schemeVersion", "studentIds", "category", "itemKey", "title", "claim", "note"), true, s.createBonusGrant)
	write("class.timeline", "修改时间窗口", "manage", "class_admin", "PUT", "/admin/timeline", mcpBody("从 class.settings.window 读取当前值；必填 open/close RFC3339 时间，lockdown 可为 null，省略会清空封锁；保留未修改字段。", "open:string close:string honorRollTopPercent:integer awards:array collegeName:string enrollmentClass:string academicYear:string", "open", "close"), true, s.updateTimeline)
	write("class.gpa_import", "导入专业素质分", "manage", "class_admin", "POST", "/admin/gpa/paste", mcpBody("使用专业素质分导入的 rows 结构，先核对名单和分数范围。", "rows:array", "rows"), true, s.pasteGPA)
	write("class.roster_add", "新增名单成员", "manage", "class_admin", "POST", "/admin/whitelist", mcpBody("学号与真实姓名，成员初始为学生。", "sid:string name:string", "sid", "name"), true, s.addWhitelist)
	write("class.scheme_create", "创建评分方案草稿", "manage", "class_admin", "POST", "/admin/scheme", mcpBody("从 templateId、sourceSchemeId 或完整 config 创建草稿。", "name:string config:object templateId:integer sourceSchemeId:integer", "name"), true, s.createScheme)
	write("class.scheme_update", "修改评分方案草稿", "manage", "class_admin", "PUT", "/admin/scheme/:id", mcpBody("先读取方案详情并保留 lockVersion；config 为评分规则。", "name:string config:object lockVersion:integer", "name", "config", "lockVersion"), true, s.updateScheme)
	write("class.scheme_publish", "发布评分方案", "manage", "class_admin", "POST", "/admin/scheme/:id/publish", nil, true, s.publishScheme)
	write("class.dispatch_run", "分发单项审核", "manage", "class_admin", "POST", "/admin/dispatch/run", mcpBody("默认只分发未分配项；includeAssigned 可重新分配，seed 为数字字符串。始终回避本人。", "seed:string includeAssigned:boolean"), true, s.runDispatch)
	write("class.settle", "生成班级结算", "manage", "class_admin", "POST", "/admin/settle", nil, true, s.runSettlement)
	write("class.unseal", "解封学生补交", "manage", "class_admin", "POST", "/admin/seals/:uid/unseal", reason, true, s.unsealStudent)
	write("class.appeal_final", "终裁申诉", "manage", "class_admin", "POST", "/admin/appeals/:id/final", arbitration, true, s.finalizeAppealRequest)
	write("class.objection_decide", "裁定异议", "manage", "class_admin", "POST", "/admin/objections/:id/decide", mcpBody("action 为 apply/adjust/dismiss；adjust 需要 score。", "action:string score:number reason:string", "action", "reason"), true, s.decideAdminObjection)
	write("class.report_final", "终裁举报", "manage", "class_admin", "POST", "/admin/reports/:id/final", mcpBody("根据举报详情填写裁定分数与理由。", "score:number reason:string", "score", "reason"), true, s.finalizeReport)
	write("exports.create", "创建报表导出", "export", "class_admin", "POST", "/admin/export", mcpBody("kind 使用 summary/detail/archive/college，其他选项遵循报表配置。", "kind:string", "kind"), false, s.requestExport)
	read("exports.status", "导出状态与下载", "class_admin", "/admin/export/:job", s.mcpExportStatus)
	out[len(out)-1].Scope = "export"
	// Deputy tools preserve the original deputy route so recusal and audit tags
	// match the web UI; a reviewer connection never acquires admin settings.
	read("deputy.submissions", "副班管可处理材料", "group", "/review/deputy/submissions", s.adminSubmissions)
	out[len(out)-1].Deputy = true
	write("deputy.arbitrate", "副班管裁定班管材料", "review", "group", "POST", "/review/deputy/submissions/:id/arbitrate", arbitration, true, s.arbitrateSubmission)
	out[len(out)-1].Deputy = true
	write("deputy.force_score", "副班管纠正班管认定分", "review", "group", "POST", "/review/deputy/submissions/:id/force-score", mcpBody("填写原分、新分与理由，只处理班管本人材料。", "previousScore:number score:number reason:string", "previousScore", "score", "reason"), true, s.forceScoreSubmission)
	out[len(out)-1].Deputy = true
	return out
}
