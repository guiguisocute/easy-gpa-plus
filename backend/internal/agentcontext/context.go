package agentcontext

import (
	"errors"
	"strings"
)

type Page struct {
	View  string
	Role  string
	Label string
}

type Context struct {
	ActualRole    string `json:"actualRole"`
	EffectiveRole string `json:"effectiveRole"`
	View          string `json:"view"`
	ViewLabel     string `json:"viewLabel"`
	ResourceID    string `json:"resourceId,omitempty"`
	ResourceKind  string `json:"resourceKind,omitempty"`
	ResourceLabel string `json:"resourceLabel,omitempty"`
	Revision      string `json:"revision,omitempty"`
	EvidenceID    string `json:"evidenceId,omitempty"`
	Draft         *Draft `json:"draft,omitempty"`
}

// pages 必须和前端 lib/nav.ts 的 NAV 逐条对齐（ops 的页面除外——ops 不在 rank
// 里，本来就用不了 Agent）。少一条，学生在那一页问一句就是 422
// 「页面上下文不正确」；label 对不上则更隐蔽：系统提示词里会写着「当前页面是
// X」，模型据此判断该用哪些工具、答什么，页面改了名这里没跟，它就一直在按旧
// 页面回答。frontend/scripts/check-ui-copy.mjs 里有一条对照检查挡这个漂移。
var pages = map[string]Page{
	"govHome": {"govHome", "student", "共治工作台"}, "govProposals": {"govProposals", "student", "提案与表决"},
	"govReviews": {"govReviews", "student", "我的评审"}, "govOperations": {"govOperations", "student", "共同事务"},
	"govParticipation": {"govParticipation", "student", "规则与参与"}, "govNewProposal": {"govNewProposal", "student", "发起共同提案"},
	"govBonus": {"govBonus", "student", "共同加分"}, "govObjections": {"govObjections", "student", "扣分与成绩异议"},
	"govGpa": {"govGpa", "student", "专业分核验"}, "govExport": {"govExport", "student", "导出授权"},
	"govSettle": {"govSettle", "student", "结算检查"}, "govTimeline": {"govTimeline", "student", "时间窗口提案"},
	"govSetupRoster": {"govSetupRoster", "class_admin", "准备班级名单"}, "govSetupScheme": {"govSetupScheme", "class_admin", "准备评分方案"},
	"govSetupFeatures": {"govSetupFeatures", "class_admin", "准备业务开关"}, "govSetupFiles": {"govSetupFiles", "class_admin", "准备班级资料"},
	"stuHome": {"stuHome", "student", "综测总览"}, "stuSubmit": {"stuSubmit", "student", "提交材料"},
	"stuList": {"stuList", "student", "我的提交"}, "stuFileAppeal": {"stuFileAppeal", "student", "发起申诉"}, "stuAppeals": {"stuAppeals", "student", "我的申诉"},
	"stuClassPenalties": {"stuClassPenalties", "student", "举报台"},
	"stuResult":         {"stuResult", "student", "我的成绩表"},
	"stuResources":      {"stuResources", "student", "评定细则"},
	"stuAccount":        {"stuAccount", "student", "账号设置"},
	"classGovernance":   {"classGovernance", "student", "班级共治"},
	"revTasks":          {"revTasks", "group", "待办任务"}, "revDesk": {"revDesk", "group", "初审工作台"},
	"revAppeals": {"revAppeals", "group", "处理申诉"}, "revObjections": {"revObjections", "group", "扣分与异议"},
	"revReports": {"revReports", "group", "举报复核"},
	"revDeputy":  {"revDeputy", "group", "副班管仲裁"},
	"revCat":     {"revCat", "group", "我的审核量"}, "revHist": {"revHist", "group", "历史审核"},
	"admBoard": {"admBoard", "class_admin", "班级看板"}, "admScheme": {"admScheme", "class_admin", "方案编辑器"},
	"admReviewProgress": {"admReviewProgress", "class_admin", "审核明细"},
	"admBonus":          {"admBonus", "class_admin", "加分台"},
	"admTimeline":       {"admTimeline", "class_admin", "时间窗口"}, "admRoster": {"admRoster", "class_admin", "班级成员"},
	"admFeatures": {"admFeatures", "class_admin", "功能开关"},
	"admDispatch": {"admDispatch", "class_admin", "分发"},
	"admSubs":     {"admSubs", "class_admin", "仲裁与申诉"},
	"admHist":     {"admHist", "class_admin", "处理历史"},
	"admGpa":      {"admGpa", "class_admin", "专业素质分"}, "admKnowledge": {"admKnowledge", "class_admin", "班级资料"},
	"admExport": {"admExport", "class_admin", "导出中心"}, "admAudit": {"admAudit", "class_admin", "审计日志"},
}

var rank = map[string]int{"student": 0, "group": 1, "class_admin": 2}

func Resolve(actualRole, view, resourceID string) (Context, error) {
	actualRole = strings.TrimSpace(actualRole)
	if _, ok := rank[actualRole]; !ok {
		return Context{}, errors.New("invalid business role")
	}
	if view == "" {
		return Context{ActualRole: actualRole, EffectiveRole: actualRole, ViewLabel: "未指定页面", ResourceID: resourceID}, nil
	}
	page, ok := pages[view]
	if !ok || !CanAssume(actualRole, page.Role) {
		return Context{}, errors.New("view is not available to actor")
	}
	return Context{ActualRole: actualRole, EffectiveRole: page.Role, View: page.View, ViewLabel: page.Label, ResourceID: resourceID}, nil
}

func CanAssume(actualRole, effectiveRole string) bool {
	actual, okActual := rank[actualRole]
	effective, okEffective := rank[effectiveRole]
	return okActual && okEffective && effective <= actual
}

func PageForView(view string) (Page, bool) {
	page, ok := pages[view]
	return page, ok
}
