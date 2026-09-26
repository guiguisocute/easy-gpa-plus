/* 导航表。四类角色的全部页面（DESIGN §1）。
   图标是原型里手画的两段 path，不走 lucide——这些形状是为 17px 见方调过的，
   换成通用图标库会在侧栏里显得过重。通用图标（关闭/菜单/箭头）仍用 lucide。 */

import type { Role } from './types'

export type View =
  | 'classGovernance'
  | 'govHome' | 'govProposals' | 'govReviews' | 'govParticipation'
  | 'govOperations' | 'govBonus' | 'govObjections' | 'govGpa' | 'govExport' | 'govSettle' | 'govTimeline' | 'govNewProposal'
  | 'govSetupRoster' | 'govSetupScheme' | 'govSetupFeatures' | 'govSetupFiles'
  /* student */
  | 'stuHome'
  | 'stuSubmit'
  | 'stuList'
  | 'stuFileAppeal'
  | 'stuAppeals'
  | 'stuClassPenalties'
  | 'stuResult'
  | 'stuResources'
  | 'stuAccount'
  /* group */
  | 'revTasks'
  | 'revDesk'
  | 'revAppeals'
  | 'revObjections'
  | 'revReports'
  | 'revCat'
  | 'revHist'
  | 'revDeputy'
  /* class_admin */
  | 'admBoard'
  | 'admReviewProgress'
  | 'admScheme'
  | 'admTimeline'
  | 'admFeatures'
  | 'admRoster'
  | 'admDispatch'
  | 'admSubs'
  | 'admHist'
  | 'admGpa'
  | 'admBonus'
  | 'admKnowledge'
  | 'admExport'
  | 'admAudit'
  /* ops */
  | 'opsInstances'
  | 'opsTemplates'
  | 'opsAgent'
  | 'opsMail'
  | 'opsJobs'
  | 'opsBackup'
  | 'opsFlags'
  | 'opsHealth'
  | 'opsDeploy'
  | 'opsAudit'

export interface NavEntry {
  view: View
  label: string
  /** 顶栏左上角那行 mono 小字 */
  en: string
  /** 顶栏 mono 小字右边的一句话描述 */
  desc: string
  /** 图标的两段 path。d2 为空串表示只有一段 */
  d1: string
  d2: string
  deputyOnly?: boolean
  preparationOnly?: boolean
  /** 侧栏分组标题。不写的（运维）整份表排成一组，标题取 NAV_LABEL。 */
  group?: string
  /** Stable identity for explicitly grouped navigation; independent of labels. */
  groupId?: string
  /* 不进侧栏列表的三种去处：account 收进底部账号菜单（三个角色都有账号设置），
     primary 是那个红按钮、hidden 只能从别的页面跳进来（都只有学生用）。
     页面本身照常存在（?v= 直达、Agent 跳转、后端 agentcontext 都不受影响），
     只是不在侧栏各占一行——九行等宽的列表里，学生最常用的「提交材料」和一学期
     用一次的「举报台」看上去一样重。 */
  sidebar?: 'primary' | 'account' | 'hidden'
  /** sidebar: 'hidden' 的页面打开时，侧栏高亮哪一条。 */
  under?: View
}

/* 学生、综测小组、班级管理员共用的一页，三份表都放它，位置都在最后：它不进侧栏列表，
   而是收在左下角账号菜单里，点自己的名字就能改自己的邮箱和密码。

   运维没有这一条：/me/emails 和 /me/password 对 ops 一律 404（requireBusiness），
   平台身份的密码不走这条路，摆出来只会点进去报错。

   view 仍叫 stuAccount：后端 agentcontext 的页面注册表按这个名字对齐，改名要两边一起动，
   而它那边只用它判「这个角色能不能站在这一页」，学生页对班管本来就成立。 */
const ACCOUNT_ENTRY: NavEntry = { view: 'stuAccount', label: '账号设置', en: 'ACCOUNT', desc: '邮箱 · 提醒 · 密码', d1: 'M16 19v-2a4 4 0 0 0-8 0v2', d2: 'M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8', sidebar: 'account' }
const GOVERNANCE_ENTRY: NavEntry = { view: 'classGovernance', label: '班级共治', en: 'CLASS GOVERNANCE', desc: '参与方式 · 共同决策 · 随机评审', d1: 'M4 5h16v14H4z', d2: 'M8 12l3 3 5-6', group: '班级' }

export const NAV: Record<Role, NavEntry[]> = {
  /* 第一条是登录后的落地页（stores/app.ts 取 NAV[role][0].view），所以「提交材料」
     排在最前面：学生登录进来九成是来交材料的，让他们先落在总览上再自己找入口，
     等于每个人每次都多走两步。它同时是列表上方那个红按钮（sidebar:'primary'），
     不在等宽列表里占一行——那样它看上去会和一学期用一次的「举报台」一样重。

     其余的顺序是学生走过的顺序：看分 → 看进度 → 不服就申诉 → 最后确认成绩核对。 */
  student: [
    { view: 'stuSubmit', label: '提交材料', en: 'SUBMIT EVIDENCE', desc: '按小项填写 · 上传佐证', d1: 'M12 5v14', d2: 'M5 12h14', sidebar: 'primary' },
    { view: 'stuHome', label: '综测总览', en: 'MY SCORE', desc: '四项得分 · 总分 · 排名 · 阶段', d1: 'M4 19V10M10 19V5M16 19v-7', d2: 'M3 21h18', group: '我的综测' },
    { view: 'stuList', label: '我的提交', en: 'MY SUBMISSIONS', desc: '草稿 · 待审 · 已定分 · 申诉', d1: 'M4 6h16M4 12h16M4 18h10', d2: '', group: '我的综测' },
    { view: 'stuAppeals', label: '我的申诉', en: 'MY APPEALS', desc: '我发起的申诉 · 处理进度 · 结论', d1: 'M12 3.6L21.2 19.5H2.8z', d2: 'M12 9.6v4.2M12 16.6v.6', group: '我的综测' },
    { view: 'stuFileAppeal', label: '发起申诉', en: 'FILE APPEAL', desc: '选一笔 · 写理由 · 传佐证', d1: 'M5 4h9l4 4v12H5z', d2: 'M8 14h6M11 11v6', sidebar: 'hidden', under: 'stuAppeals' },
    { view: 'stuResult', label: '我的成绩表', en: 'LIVE SCORECARD', desc: '实时成绩 · 处理进度 · 一键核对', d1: 'M6 4h9l4 4v12H6z', d2: 'M9 13l2.2 2.2L16 10.6', group: '我的综测' },
    { view: 'stuResources', label: '评定细则', en: 'CLASS RULES', desc: '班级文件 · 原件下载', d1: 'M6 4h9l4 4v12H6z', d2: 'M9 13h6M9 17h6', group: '班级' },
    { view: 'stuClassPenalties', label: '举报台', en: 'REPORT A PEER', desc: '看全班扣分 · 匿名举报', d1: 'M12 3l8 4v6c0 4.4-3.4 7.4-8 8-4.6-.6-8-3.6-8-8V7z', d2: 'M12 8v4M12 15v.6', group: '班级' },
    ACCOUNT_ENTRY,
  ],
  /* 落地页仍是「待办任务」：审核人一进来要先看清手上有多少条、哪些在等别人交。
     九条按判的东西分三段：材料（审核）→ 学生不服和扣分（申诉与扣分）→ 自己的账（我的记录）。

     这里不放学生那种红按钮。小组不像学生那样绝大多数时间都在做同一件事——
     手上是初审、成绩核对、申诉还是举报，取决于这一轮分到了什么，挑一条描红只会误导。 */
  group: [
    { view: 'revTasks', label: '待办任务', en: 'REVIEW TASKS', desc: '分给我的审核和申诉', d1: 'M9 11l3 3 8-8', d2: 'M20 12v7H4V5h11', group: '审核' },
    { view: 'revDesk', label: '初审工作台', en: 'REVIEW DESK', desc: '单项审核 · 佐证与认定分', d1: 'M4 4h16v12H4z', d2: 'M8 20h8', group: '审核' },

    { view: 'revAppeals', label: '处理申诉', en: 'APPEAL DESK', desc: '复评派给我的申诉', d1: 'M12 3.6L21.2 19.5H2.8z', d2: 'M12 9.6v4.2M12 16.6v.6', group: '申诉与扣分' },
    { view: 'revObjections', label: '扣分与异议', en: 'DEDUCTIONS', desc: '基础分 · 扣分 · 定分异议', d1: 'M5 12h14', d2: 'M4 5h16v14H4z', group: '申诉与扣分' },
    { view: 'revReports', label: '举报复核', en: 'REPORT DESK', desc: '匿名举报 · 两人分头复核', d1: 'M12 3l8 4v6c0 4.4-3.4 7.4-8 8-4.6-.6-8-3.6-8-8V7z', d2: 'M12 8v4M12 15v.6', group: '申诉与扣分' },

    { view: 'revCat', label: '我的审核量', en: 'MY WORKLOAD', desc: '我审了多少 · 和人均比 · 规则速查', d1: 'M4 19V9M10 19V5M16 19v-9', d2: 'M3 21h18', group: '我的记录' },
    { view: 'revHist', label: '历史审核', en: 'HISTORY', desc: '我的结论 · 被改判记录', d1: 'M12 8v5l4 2', d2: 'M3 12a9 9 0 1 0 9-9', group: '我的记录' },

    /* 只有被任命的人才看得见，所以单独一段：混进上面任何一段，没被任命的人会觉得少了一行。 */
    { view: 'revDeputy', label: '副班管仲裁', en: 'DEPUTY ARBITRATION', desc: '班管本人的仲裁事项', d1: 'M12 3l8 4v6c0 4.4-3.4 7.4-8 8-4.6-.6-8-3.6-8-8V7z', d2: 'M9 12l2 2 4-4', deputyOnly: true, group: '副班管' },
    ACCOUNT_ENTRY,
  ],
  /* 落地页仍是「班级看板」。同样不放红按钮：班管一学期里做的事按阶段换——
     开学定方案和时间线，中间盯进度和仲裁，最后导表，没有哪一页是天天点的。

     四段按什么时候用来分：日常看的（班级）→ 开学定下来的（评定设置）→
     评定期间推进的（评分）→ 收尾和事后查的（导出与记录）。 */
  class_admin: [
    { view: 'admBoard', label: '班级看板', en: 'CLASS BOARD', desc: '审核进度 · 分数分布 · 待办统计', d1: 'M4 19V10M10 19V5M16 19v-7', d2: 'M3 21h18', group: '班级' },
    { view: 'admReviewProgress', label: '审核明细', en: 'REVIEW DETAILS', desc: '逐项查看材料 · 审核轨迹 · 处理', d1: 'M4 6h16M4 12h16M4 18h10', d2: '', group: '班级' },
    { view: 'admRoster', label: '班级成员', en: 'ROSTER', desc: '名单 · 身份 · 账号', d1: 'M16 19v-2a4 4 0 0 0-8 0v2', d2: 'M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8', group: '班级' },
    { view: 'admKnowledge', label: '班级资料', en: 'CLASS FILES', desc: '评定文件 · 全班发布', d1: 'M4 5h6a3 3 0 0 1 3 3v11H7a3 3 0 0 0-3 3z', d2: 'M20 5h-6a3 3 0 0 0-3 3v11h6a3 3 0 0 1 3 3z', group: '班级' },

    { view: 'admScheme', label: '方案编辑器', en: 'SCHEME EDITOR', desc: '方案树 · 计分规则 · 学生端预览', d1: 'M5 4h6v6H5zM13 14h6v6h-6z', d2: 'M8 10v4h5', group: '评定设置' },
    { view: 'admTimeline', label: '时间窗口', en: 'TIME WINDOWS', desc: '材料与公示窗口 · 审核时限 · 评优设置', d1: 'M12 8v5l4 2', d2: 'M3 12a9 9 0 1 0 9-9', group: '评定设置' },
    { view: 'admFeatures', label: '功能开关', en: 'FEATURE SWITCHES', desc: '业务功能 · 结算闸门', d1: 'M6 8h12M6 16h12', d2: 'M9 5v6M15 13v6', group: '评定设置' },

    { view: 'admDispatch', label: '分发', en: 'DISPATCH', desc: '材料初审 · 任务分配 · 改派', d1: 'M4 6h6v6H4z', d2: 'M14 12l6 6M14 18h6v-6', group: '评分' },
    { view: 'admBonus', label: '加分台', en: 'DIRECT BONUS', desc: '按班级方案 · 单人或批量直接加分', d1: 'M12 5v14', d2: 'M5 12h14', group: '评分' },
    { view: 'admSubs', label: '仲裁与申诉', en: 'ARBITRATION', desc: '冲突 · 申诉 · 小组提案', d1: 'M12 3.6L21.2 19.5H2.8z', d2: 'M12 9.6v4.2M12 16.6v.6', group: '评分' },
    { view: 'admHist', label: '处理历史', en: 'DECISION HISTORY', desc: '我处理过的仲裁、申诉与其他终裁', d1: 'M12 3a9 9 0 1 1-8 5M3 3v5h5', d2: 'M12 7v5l3 2', group: '评分' },
    { view: 'admGpa', label: '专业素质分', en: 'GPA IMPORT', desc: '成绩导入 · 总分合成 · 三好', d1: 'M4 17l6-6 4 4 6-8', d2: 'M3 21h18', group: '评分' },

    { view: 'admExport', label: '导出中心', en: 'EXPORT', desc: '汇总表 · 逐人明细 · 归档包', d1: 'M12 4v11M8 11l4 4 4-4', d2: 'M4 20h16', group: '导出与记录' },
    { view: 'admAudit', label: '审计日志', en: 'AUDIT LOG', desc: '谁看了什么 · 谁改了什么', d1: 'M4 4h16v16H4z', d2: 'M8 9h8M8 13h5', group: '导出与记录' },
    ACCOUNT_ENTRY,
  ],
  ops: [
    { view: 'opsInstances', label: '班级与班管', en: 'TENANTS', desc: '开启班级 · 任命班管 · 停用班级', d1: 'M4 6h7v12H4z', d2: 'M13 10h7v8h-7', group: '日常运营', groupId: 'ops-operations' },
    { view: 'opsTemplates', label: '模板库', en: 'TEMPLATES', desc: '模板上架与下架', d1: 'M5 4h14v5H5z', d2: 'M5 13h14v7H5z', group: '日常运营', groupId: 'ops-operations' },
    { view: 'opsMail', label: '邮件与通知', en: 'MAIL & NOTIFY', desc: '发信通道 · 通知策略 · 投递日志', d1: 'M3 6h18v12H3z', d2: 'M3 7l9 6 9-6', group: '平台能力', groupId: 'ops-capabilities' },
    { view: 'opsAgent', label: 'Agent 与知识库', en: 'AGENT CONFIG', desc: '模型路由 · 知识库 · 用量上限', d1: 'M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z', d2: 'M18.5 15.5l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7z', group: '平台能力', groupId: 'ops-capabilities' },
    { view: 'opsFlags', label: '开关与阈值', en: 'FLAGS & LIMITS', desc: '访问控制 · 容量 · 频率限制 · 品牌', d1: 'M6 8h12M6 16h12', d2: 'M9 5v6M15 13v6', group: '平台能力', groupId: 'ops-capabilities' },
    { view: 'opsHealth', label: '组件健康', en: 'HEALTH', desc: '服务状态 · 存储连接 · 运行环境', d1: 'M3 12h4l2 5 3-11 2 6h7', d2: '', group: '系统维护', groupId: 'ops-maintenance' },
    { view: 'opsJobs', label: '队列与任务', en: 'QUEUES & JOBS', desc: '积压 · 失败重投 · 心跳 · 定时任务', d1: 'M4 6h16M4 12h16M4 18h16', d2: '', group: '系统维护', groupId: 'ops-maintenance' },
    { view: 'opsBackup', label: '备份与恢复', en: 'BACKUP', desc: '备份计划 · 远程副本 · 恢复演练', d1: 'M4 7h16v13H4z', d2: 'M9 12h6M12 9v6', group: '系统维护', groupId: 'ops-maintenance' },
    { view: 'opsDeploy', label: '部署与版本', en: 'DEPLOY', desc: '版本 · 连接 · 会话 · 部署配置', d1: 'M12 3l8 4v10l-8 4-8-4V7z', d2: 'M12 12v9', group: '系统维护', groupId: 'ops-maintenance' },
    { view: 'opsAudit', label: '平台审计', en: 'PLATFORM AUDIT', desc: '资源操作 · 变更留痕', d1: 'M4 4h16v16H4z', d2: 'M8 9h8M8 13h5', group: '系统维护', groupId: 'ops-maintenance' },
  ],
}

export const NAV_LABEL: Record<Role, string> = {
  student: '学生面板',
  group: '综测小组',
  class_admin: '班级管理',
  ops: '运维控制台',
}

/** 共治是独立工作台，所有成员使用同一份导航；复用个人材料组件，不复用角色导航。 */
const COLLECTIVE_AFFAIRS: Pick<NavEntry, 'view' | 'label' | 'en' | 'desc'>[] = [
  { view: 'govBonus', label: '共同加分', en: 'SHARED BONUS', desc: '共同事项 · 受益人回避' },
  { view: 'govObjections', label: '扣分与成绩异议', en: 'SCORE OBJECTIONS', desc: '事实与依据 · 随机评审' },
  { view: 'govGpa', label: '专业分核验', en: 'GPA VERIFICATION', desc: '成绩原件 · 全班核验' },
  { view: 'govExport', label: '导出授权', en: 'EXPORT AUTHORIZATION', desc: '单次授权 · 按用途领取' },
  { view: 'govSettle', label: '结算检查', en: 'SETTLEMENT', desc: '业务条件检查 · 正式结果' },
  { view: 'govTimeline', label: '时间窗口提案', en: 'TIME WINDOWS', desc: '时间调整 · 重大事项表决' },
]
const COLLECTIVE_PREPARATION: Pick<NavEntry, 'view' | 'label'>[] = [
  { view: 'govSetupRoster', label: '准备班级名单' },
  { view: 'govSetupScheme', label: '准备评分方案' },
  { view: 'govSetupFeatures', label: '准备业务开关' },
  { view: 'govSetupFiles', label: '准备班级资料' },
]
export const COLLECTIVE_NAV: NavEntry[] = [
  { ...NAV.class_admin[0], view: 'govHome', label: '共治工作台', en: 'OUR WORKSPACE', desc: '我的待办 · 班级动态', group: '一起决定' },
  { ...GOVERNANCE_ENTRY, view: 'govProposals', label: '提案与表决', en: 'PROPOSALS', desc: '共同事项 · 讨论 · 投票', group: '一起决定' },
  { ...NAV.group[0], view: 'govReviews', label: '我的评审', en: 'MY REVIEWS', desc: '随机分配 · 独立认定 · 处理进度', group: '一起决定' },
  { ...NAV.class_admin[3], view: 'govOperations', label: '共同事务', en: 'CLASS AFFAIRS', desc: '共同加分 · 专业分 · 时间窗口 · 结算', group: '一起决定' },
  ...NAV.student.filter(n => ['stuSubmit', 'stuList', 'stuResult', 'stuAppeals', 'stuFileAppeal'].includes(n.view)).map(n => ({ ...n, group: '我的综测', ...(n.view === 'stuList' ? { label: '我的材料' } : {}), ...(n.view === 'stuResult' ? { label: '实时成绩' } : {}) })),
  { ...GOVERNANCE_ENTRY, view: 'govParticipation', label: '规则与参与', en: 'PARTICIPATION', desc: '自愿参与 · 评审报名 · 本周期章程', group: '班级' },
  { ...NAV.student.find(n => n.view === 'stuResources')!, label: '班级资料' },
  ...COLLECTIVE_AFFAIRS.map(entry => ({ ...GOVERNANCE_ENTRY, ...entry, sidebar: 'hidden' as const, under: 'govOperations' as const })),
  { ...GOVERNANCE_ENTRY, view: 'govNewProposal', label: '发起共同提案', sidebar: 'hidden', under: 'govProposals' },
  ...COLLECTIVE_PREPARATION.map(entry => ({ ...GOVERNANCE_ENTRY, ...entry, sidebar: 'hidden' as const, under: 'govParticipation' as const, preparationOnly: true })),
  { ...NAV.student.find(n => n.view === 'stuHome')!, sidebar: 'hidden', under: 'stuResult' },
  { ...NAV.student.find(n => n.view === 'stuClassPenalties')!, sidebar: 'hidden', under: 'govOperations' },
  ACCOUNT_ENTRY,
]

export const ALL_VIEWS = new Set<View>([...Object.values(NAV).flat(), ...COLLECTIVE_NAV].map(n => n.view).concat('classGovernance'))

export function workspaceNav(role: Role, collective: boolean): NavEntry[] {
  return collective && role !== 'ops' ? COLLECTIVE_NAV : NAV[role]
}

export function workspaceView(entries: NavEntry[], view: View): View {
  return entries.some(n => n.view === view) ? view : entries[0].view
}

/** 这个角色的导航里有这一页吗。用于校验 URL 里带来的 view 是否越权。
    问的不是「这一页属于谁」——账号设置三个角色都有，那个问题没有唯一答案。
    真正的越权拦截在后端，这里只防误操作。 */
export function viewInNav(role: Role, view: View): boolean {
  return NAV[role].some((n) => n.view === view)
}

export function navEntry(role: Role, view: View): NavEntry {
  return NAV[role].find((n) => n.view === view) ?? COLLECTIVE_NAV.find((n) => n.view === view) ?? NAV[role][0]
}

/** 侧栏列表：按 group 分段，保持表里的先后顺序。没写 group 的角色整份排成 fallback 一段。 */
export function navGroups(entries: NavEntry[], fallback: string): { id: string; title: string; entries: NavEntry[] }[] {
  const groups: { id: string; title: string; entries: NavEntry[] }[] = []
  for (const entry of entries) {
    if (entry.sidebar) continue
    const title = entry.group ?? fallback
    const last = groups.at(-1)
    if (last && last.title === title && (!entry.groupId || last.id === entry.groupId)) last.entries.push(entry)
    else groups.push({ id: entry.groupId ?? entry.view, title, entries: [entry] })
  }
  return groups
}

/** 侧栏该给哪一条描红。列表里没有的页面（发起申诉）落到它挂靠的那一条上，
    否则从总览点「发起申诉」跳过去，侧栏会一条都不亮，看着像是跳出了系统。 */
export function navHighlight(role: Role, view: View): View {
  return NAV[role].find((n) => n.view === view)?.under ?? view
}
