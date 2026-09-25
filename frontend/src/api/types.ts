/* 后端 JSON 的镜像类型。

   这里的每个字段名都取自 backend/internal/api/*.go 的实际输出，不是"设计上应该长这样"。
   领域概念的类型（Role / CategoryKey / SubmissionStatus / SchemeConfig）复用 lib/types.ts，
   两边同名同义；只有传输层特有的形状（分页信封、快照、审计条目）写在这里。

   一个约定：后端所有 ID 都以字符串下发（int64 超出 JS 安全整数范围），
   前端也一律当字符串用，只有传给后端明确要求数字的字段（studentId、reviewerIds）才转回 number。 */

import type { CategoryKey, Capabilities, Role, SchemeConfig, SchemeItem, SubmissionStatus, User } from '@/lib/types'
import type { AdjudicationPermission } from '@/lib/adjudication'

/* ---- 信封 ---- */

export interface Page<T> {
  items: T[]
  page: number
  pageSize: number
  total: number
}

export interface List<T> {
  items: T[]
}

/* ---- 鉴权 ---- */

export interface SessionTokens {
  access_token: string
  expires_in: number
  /** refresh 接口只轮换 cookie，不重复下发 user */
  user?: User
}

/** 注册第一步的产物：身份已核对通过，凭它去设密码。本身建不出账号。 */
export interface RegisterTicket {
  ticket: string
  expires_in: number
  /** 白名单里登记的姓名与角色，回显给用户确认没进错班 */
  name: string
  role: Role
}

export interface UserEmail {
  id: string
  email: string
  primary: boolean
  verified: boolean
  verifiedAt: string | null
  createdAt: string
}

export interface AddEmailChallenge {
  challengeId: string
  expiresIn: number
}

/* ---- 方案与窗口 ---- */

export interface WindowState {
  window: TimelineWindow
  capabilities: Capabilities
  honorRoll: HonorRoll
  /** 学院全称，只用于学院报表；没填过是空串 */
  collegeName: string
  /** 教务在线上的班级名，如「24级计算机科学与技术2班」。
      和班级在系统里的简称（如「示例班级」）是两个东西：附件明写班级名称
      必须与教务在线一致，报表里的年级与专业也由它解析。 */
  enrollmentClass: string
  academicYear: string
  serverNow: string
  sealed: boolean
  schemeVersion: string
}

/** 两级截止：close 是封存截止（不再收材料、全班自动封存，审核申诉照常），
    lockdown 是全系统封锁（之后只剩查看与导出）。lockdown 可以不设。 */
export interface TimelineWindow {
  open: string
  close: string
  lockdown: string | null
  publicity?: PublicityWindow | null
}

export interface PublicityWindow {
  open: string
  close: string
}

/** topPercent 是三好线，awards 是奖学金档位。每档的 topPercent 是本档自己的
    名额占比，不是累计值；档位由后端结算器算定，前端不再自己推。 */
export interface HonorRoll {
  topPercent: number
  awards: AwardTier[]
}

export interface AwardTier {
  name: string
  topPercent: number
}

export interface SchemeMeta {
  id: string
  name: string
  version: number | null
  status: 'draft' | 'published'
  lockVersion: number
  publishedAt?: string
  updatedAt: string
}

export interface SchemeDraft {
  id: string
  name: string
  version: number | null
  status: 'draft' | 'published'
  config: SchemeConfig
  lockVersion: number
}

export interface ClassInfo {
  id: string
  name: string
  archived: boolean
  createdAt: string
  schemeVersion?: string
  window?: TimelineWindow
  capabilities?: Capabilities
}

/* ---- 提交 ---- */

/** 学生申报值。按档规则同时保存 option 与可调整的期望 score。 */
export interface Claim {
  quantity?: number
  option?: string
  score?: number
}

/** 提交时刻冻结的规则。算分永远按它，不按当前方案（§4.3）。 */
export interface RuleSnapshot {
  schemeId: string
  version: string
  categoryKey: CategoryKey
  categoryName: string
  item: SchemeItem
  capturedAt: string
}

export interface ForceRejection {
  reason: string
  previousScore: number | null
  rejectedAt: string
  actorName?: string
  score?: number
  selfRejected?: boolean
}

export interface ForceRejectPermission {
  canForceScore?: boolean
  forceScoreBlockedReason?: string
  canForceReject?: boolean
  forceRejectBlockedReason?: string
  forceRejection?: ForceRejection | null
  forcedScore?: ForceRejection | null
}

export interface Submission {
  id: string
  student: string
  studentId: string
  category: CategoryKey
	filedCategory?: CategoryKey
	filedItemKey?: string
	filedRuleSnapshot?: RuleSnapshot
  categoryName: string
  itemKey: string
  itemName: string
  title: string
  claim: Claim
  wantScore: number | null
  finalScore: number | null
  status: SubmissionStatus
  submittedAt: string | null
  ruleSnapshot: RuleSnapshot
  evidenceCount: number
  note?: string
  source: 'manual' | 'ai' | 'admin_grant' | 'collective_grant'
  /** 已经提过几次申诉。它只是次数，不是配额——能不能再提看 canAppeal */
  appealsUsed: number
  /** 还能不能再提一次。后端规则：班级管理员一旦下过结论（final）就到此为止，
      复评一致结案（resolved）则仍可再提。前端不自己推——推错就是给学生一个点不动的按钮 */
  canAppeal: boolean
  forceRejection?: ForceRejection | null
  forcedScore?: ForceRejection | null
  updatedAt: string
  canSelfForceReject?: boolean
}

export interface Evidence {
  id: string
  key?: string
  name: string
  mediaType: string
  sizeBytes: number
  sha256: string | null
  status: 'pending' | 'ready' | 'rejected'
  uploadedAt: string
}

/** 学生视角的审核结论：有理由，没有人名（§5.1 单盲）。 */
export interface StudentReview {
  decision: ReviewDecision
  score: number
  why: string
  spentSeconds: number
  at: string
}

/** 管理员/仲裁视角：附带真实审核人。 */
export interface NamedReview {
  id: string
  reviewer: string
  reviewerSid?: string
  reviewerId?: string
  decision: ReviewDecision
  score: number
  reason: string
  spentSeconds: number
  at: string
}

export interface TrailEntry {
  action: string
  after: unknown
  metadata: unknown
  at: string
}

export interface SubmissionDetail {
  submission: Submission
  reviews: StudentReview[]
  evidence: Evidence[]
  /** 见 ReviewTaskDetail.noteEvidence */
  noteEvidence?: Evidence[]
  trail: TrailEntry[]
  classificationHistory?: {
    id: string
    beforeCategory: CategoryKey
    beforeItemKey: string
    afterCategory: CategoryKey
    afterItemKey: string
    beforeScore: number | null
    afterScore: number | null
    reason: string
    at: string
  }[]
}

/* ---- AI 材料整理（候选只会应用为普通草稿） ---- */

export type AIBatchStatus =
  | 'uploading'
  | 'queued'
  | 'processing'
  | 'review'
  | 'complete'
  | 'failed'
  | 'canceled'
  | 'expired'

export interface AIAssetRef {
  assetId: string
  page?: number
}

export interface AIFields {
  title: string
  date: string
  issuer: string
  level: string
  quantity?: number | null
  unit: string
}

export interface AIObservation {
  assetId: string
  page?: number
  materialType: string
  rawText: string
  fields: AIFields
  warnings: string[]
}

export interface AICandidate {
  id: string
  assets: AIAssetRef[]
  categoryKey: string
  itemKey: string
  title: string
  claim: Claim
  note: string
  alternatives: string[]
  confidence: number
  needsReview: boolean
  reviewReasons: string[]
  expectedScore: number | null
}

export interface AIAsset {
  id: string
  filename: string
  mediaType: string
  sizeBytes: number
  status: 'pending' | 'ready' | 'processing' | 'complete' | 'failed' | 'applied' | 'rejected'
  pageCount: number | null
  error: string | null
  previewUrl?: string
}

export interface AIItem {
  id: string
  assetId: string
  page: number
  status: 'queued' | 'processing' | 'complete' | 'failed'
  perception?: AIObservation
  error: string | null
  durationMs: number
}

export interface AIBatch {
  id: string
  status: AIBatchStatus
  total: number
  processed: number
  /* 归组过程中已经成型的候选（标题与申报说明用换行分隔）。 */
  composePreview: string[]
  /* 模型的推理过程。它比正文早得多（实测 11 秒对 64 秒），等待期的头一分钟全靠它。 */
  composeThinking: string
  /* 归组按块跑、每块落库，所以这一步有真进度可报。 */
  composeChunks: { total: number; complete: number; failed: number; candidates: number }
  result: { candidates: AICandidate[]; warnings: string[] }
  usage: { inputTokens?: number; outputTokens?: number; totalTokens?: number }
  visionModel: string
  textModel: string
  promptVersion: string
  error: string | null
  startedAt: string | null
  completedAt: string | null
  expiresAt: string
  createdAt: string
  assets: AIAsset[]
  items: AIItem[]
  limits: AIBatchLimits
}

export interface AIBatchLimits {
  items: number
  dailyBatches: number
  activeBatches: number
  fileMb: number
  batchMb: number
  pdfPages: number
  allowedFormats: AIMaterialFormat[]
}

export type AIMaterialFormat = 'jpeg' | 'png' | 'webp' | 'pdf'

export interface AICreateBatch {
  id: string
  status: 'uploading'
  expiresAt: string
  limits: AIBatchLimits
}

export interface AIAppliedDraft {
  id: string
  candidateId: string
  requestedScore: number | null
}

export interface AIApplyResult {
  id: string
  status: 'complete'
  drafts: AIAppliedDraft[]
}

/** 佐证的临时读取链接。inline 为 true 时浏览器会就地渲染，否则触发下载。 */
export interface EvidenceLink {
  url: string
  expiresIn: number
  filename: string
  mediaType: string
  inline: boolean
}

export interface PresignResult {
  evidenceId: string
  uploadUrl: string
  uploadFields: Record<string, string>
  expiresIn: number
  method: 'POST'
  completeUrl: string
}

/* ---- 基础项 / 扣分项 ---- */

export interface BaseItem {
  id: string | null
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  kind: 'base' | 'penalty'
  fullScore: number | null
  score: number
  basis: string
  canAppeal: boolean
  /** 已经用掉几次申诉（0/1/2） */
  appealsUsed: number
  /** false = 方案默认值，还没有人录入过 */
  recorded: boolean
  updatedAt?: string
}

/* ---- 封存 ---- */

export interface SealState {
  sealed: boolean
  source: 'manual' | 'auto' | null
  sealedAt: string | null
  draftCount: number
  submittedCount: number
  /** 两步确认要逐字键入的短语，由后端给出，前端不硬编码 */
  confirmationPhrase: string
  windowClose?: string
}

export interface AdminSeal {
  userId: string
  sid: string
  name: string
  role: Role
  drafts: number
  submitted: number
  scored: number
  sealState: 'sealed' | 'active' | 'none'
  sealSource: 'manual' | 'auto' | null
  sealedAt: string | null
	lastActivity: string | null

}

export interface AnonReview {
  decision: ReviewDecision
  score: number | null
  reason: string
  at: string
}

export interface MyScorecardItem {
  id: string
  title: string
  filedCategory: string
  filedItemKey: string
  category: CategoryKey
  itemKey: string
  claim: unknown
  requestedScore: number | null
  score: number
  forceRejection?: ForceRejection | null
  forcedScore?: ForceRejection | null
  note: string
  source: string
  submittedAt: string | null
  ruleSnapshot: RuleSnapshot
  filedRuleSnapshot: RuleSnapshot | null
  evidence: Evidence[]
  noteEvidence: Evidence[]
  reviews: AnonReview[]
}

export interface MyScorecardBaseItem {
  id: string | null
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  kind: 'base' | 'penalty'
  fullScore: number | null
  perScore: number | null
  score: number
  basis: string
  recorded: boolean
  updatedAt?: string
}

export interface MyScorecardCategory {
  key: CategoryKey
  name: string
  itemTotal: number
  baseTotal: number
  penaltyTotal: number
  beforeCap: number
  total: number
  maxTotal: number
  weight: number
}

export interface MyScorecard {
 state: 'confirmable' | 'confirmed'
 revision: string
 updatedAt: string
 confirmation: { confirmed: boolean; confirmedAt: string | null; recheck: boolean }
  scorecard: {
    issues: { kind: 'submission' | 'appeal' | 'objection'; id: string; title: string; status: string }[]
    categories: MyScorecardCategory[]
    items: MyScorecardItem[]
    baseItems: MyScorecardBaseItem[]
    gpa: { score: number | null; imported: boolean }
    estimatedTotal: number | null
    estimateNote: string
  } | null
}

/* ---- 申诉（两轮制，DESIGN §5.2） ----

   一轮 = 学生的一次申诉。
   round 1：原封不动交回**原班人马**（提交条目是原两名审核人，基础/扣分项是原录入人）复评，
            复评一致即结案（resolved），学生不服还能再提一次；复评不一致升给管理员（escalated），
            而管理员签字（final）之后这一笔就封死了——哪怕学生只提过一次。
   round 2：不再经审核人，直接进班级管理员的终裁队列（escalated → final）。
   终裁（final）之后不再受理任何申诉。 */

export type AppealTargetType = 'submission' | 'base_score' | 'penalty_score'

export type AppealRound = 1 | 2

export type AppealStatus = 'filed' | 'reviewing' | 'escalated' | 'resolved' | 'final'

export const APPEAL_STATUS_LABEL: Record<AppealStatus, string> = {
  filed: '待派发',
  reviewing: '复评中',
  escalated: '待终裁',
  /* 原来叫「一审结案」。它和「第 1 次申诉」讲的不是一回事，摆在一起看却像是，
     而且那个「一」是这一页上唯一的汉字序号。这里只说发生了什么：原班人马复评谈拢了。 */
  resolved: '复评结案',
  final: '已终裁',
}

/** 轮次只有一种写法。同一件事以前有「第 1 次」「第二次申诉」「一次申诉」三种印法。 */
export const appealRoundLabel = (round: AppealRound | number) => `第 ${round} 次申诉`

/** 复评结论。只有两种：照原样维持，或给一个新分。 */
export type RereviewDecision = 'uphold' | 'adjust'

export const REREVIEW_LABEL: Record<RereviewDecision, string> = {
  uphold: '维持原判',
  adjust: '改判',
}

export interface Rereview {
  decision: RereviewDecision
  score: number
	category?: CategoryKey
	itemKey?: string
  reason: string
  spentSeconds: number
  at: string
}

/** 本轮复评人。round=2 时为空数组（那一轮没有复评人，直接归管理员）。 */
export interface AppealHandler {
  id: string
  /** 学生视角不下发姓名（§5.1 单盲）；小组与管理员视角下发 */
  name?: string
  sid?: string
  decided: boolean
  /** 小组视角标出哪一条是自己 */
  mine?: boolean
  /** 背靠背：同行的复评内容要等两人都交完才下发，在那之前恒为 null */
  rereview: Rereview | null
}

export interface Appeal extends AdjudicationPermission {
  id: string
  collective?: boolean
  targetType: AppealTargetType
  targetId: string
  target: string
  category: CategoryKey
  itemKey: string
  student: string
  studentId: string
  reason: string
  round: AppealRound
  status: AppealStatus
  /** 发起这一轮时该对象的分值 —— 学生申诉的就是它 */
  baselineScore: number | null
  currentScore: number | null
  proposedScore: number | null
  resolutionScore: number | null
  resolutionReason: string | null
	originalCategory?: CategoryKey | null
	originalItemKey?: string | null
	proposedCategory?: CategoryKey | null
	proposedItemKey?: string | null
	resolutionCategory?: CategoryKey | null
	resolutionItemKey?: string | null
	originBatchId?: string | null
  /** 上一轮申诉的 id（round=2 才有），用来把两轮串成一条链 */
  previousAppealId?: string
  handlers: AppealHandler[]
  createdAt: string
  updatedAt: string
  resolvedAt: string | null
  /** 终裁人姓名。status=final 时对学生也下发：班管不是单盲对象。 */
  handler?: string | null
}

export interface AppealDetail extends Appeal {
  evidence: Evidence[]
  /** 见 ReviewTaskDetail.noteEvidence */
  noteEvidence?: Evidence[]
  trail: TrailEntry[]
  originalEvidence?: Evidence[]
  ruleSnapshot?: RuleSnapshot
  /** 学生与小组视角是匿名的 StudentReview[]，管理员视角是 NamedReview[] */
  originalReviews?: (StudentReview | NamedReview)[]
  /** 上一轮的结果，round=2 的详情里回放给管理员看 */
  previousRound?: {
    id: string
    reason: string
    resolutionScore: number | null
    resolutionReason: string | null
    resolvedAt: string | null
    handlers: AppealHandler[]
  }
  basis?: string
  fullScore?: number | null
}

/** 复评提交的回执。前端据此决定提示哪一句话。 */
export interface RereviewResult {
  id: string
  status: AppealStatus
  /** 两名复评人是否都已提交 */
  bothDecided: boolean
  /** 复评不一致，已升给管理员 */
  conflict: boolean
  /** 复评一致时的落定分，否则为 null */
  resolvedScore: number | null
	resolvedCategory?: CategoryKey | null
	resolvedItemKey?: string | null
}

/* ---- 扣分与异议（综测小组提案 → 班级管理员终裁） ----

   三种提案共用一张台面：改基础分、提交扣分、对已定分条目提出异议。
   一律先落成草稿，批量提交后才进管理员的终裁队列 —— 小组自己不改分，
   这是把"谁都能扣分"和"扣分要有人签字"这两件事分开的唯一办法。 */

export type ObjectionKind = 'base' | 'penalty' | 'submission'

export const OBJECTION_KIND_LABEL: Record<ObjectionKind, string> = {
  base: '基础分调整',
  penalty: '扣分提交',
  submission: '定分异议',
}

export type ObjectionStatus = 'draft' | 'submitted' | 'applied' | 'adjusted' | 'dismissed' | 'withdrawn'

export const OBJECTION_STATUS_LABEL: Record<ObjectionStatus, string> = {
  draft: '草稿',
  submitted: '待终裁',
  applied: '已生效',
  adjusted: '改分生效',
  dismissed: '已驳回',
  withdrawn: '已撤回',
}

export interface Objection extends AdjudicationPermission {
  id: string
  kind: ObjectionKind
  /** 学号，展示用 */
  studentId: string
  /** 用户 id，写接口用（int64 字符串） */
  studentUserId: string
  student: string
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  /** kind=submission 时是提交条目 id；base/penalty 指向 base_score 行，从未录入过则为 null */
  targetId: string | null
  /** 提案时该对象的分值，null = 尚无记录 */
  currentScore: number | null
  proposedScore: number
  /** 扣分项的次数。分值 = 次数 × 单价，带上它是为了让终裁看得懂这 -3 分是怎么来的 */
  quantity: number | null
  basis: string
  noteEvidence?: Evidence[]
  status: ObjectionStatus
  /** 同一次提交的一批共用一个 batchId，管理员可以整批处理 */
  batchId: string | null
  /** 管理员视角才下发提出人 */
  proposer?: string
  proposerId?: string
  decidedScore: number | null
  decisionReason: string | null
  createdAt: string
  submittedAt: string | null
  decidedAt: string | null
}

export interface ObjectionDecisionResult {
  id: string
  status: ObjectionStatus
  score: number | null
}

/** 小组可操作的班级成员。名册接口只给这四个字段，不给邮箱与登录记录。 */
export interface ReviewStudent {
  userId: string
  sid: string
  name: string
  self: boolean
  sealed: boolean
  /** 已定分条目数，用来提示"这个人有东西可议" */
  scoredCount: number
  /** 我已经对这个人挂了几条未结提案 */
  openObjections: number
}

/** 计分卡：一个学生身上所有可提案的对象。编辑台的全部数据来源。 */
export interface StudentScorecard {
  student: ReviewStudent
  baseItems: ScorecardBaseRow[]
  submissions: ScorecardSubRow[]
}

export interface ScorecardBaseRow {
  /** base_score 行 id，从未录入过为 null */
  id: string | null
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  kind: 'base' | 'penalty'
  /** base 是满分，penalty 是单价（负数） */
  fullScore: number | null
  perScore: number | null
  score: number
  basis: string
  recorded: boolean
  /** 学生已就这一笔发起申诉且未结案时锁住，避免两条线同时改一个数 */
  locked: boolean
  updatedAt: string | null
}

export interface ScorecardSubRow {
  source?: 'manual' | 'ai' | 'admin_grant' | 'collective_grant'
  id: string
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  title: string
  finalScore: number | null
  status: SubmissionStatus
  ruleSnapshot: RuleSnapshot
  /** 我审过这条吗。审过的自己再提异议不合适，前端给个提示，不禁止 */
  reviewedByMe: boolean
  locked: boolean
  scoredAt: string | null
  /** 学生当初交的材料。定分之后对整个小组开放，判"这条给得对不对"要看它 */
  evidence: Evidence[]
}

/* ---- 审核 ---- */

export type ReviewDecision = 'accepted' | 'adjusted' | 'rejected'
export type ReviewTab = 'mine' | 'peer' | 'conflict' | 'appeal'

export const DECISION_LABEL: Record<ReviewDecision, string> = {
  accepted: '通过',
  adjusted: '调分',
  rejected: '驳回',
}

export interface ReviewTask {
  id: string
  type: 'submission'
  title: string
  category: CategoryKey
  itemKey: string
  requestedScore: number | null
  status: SubmissionStatus
  submittedAt: string | null
	assignedAt?: string
	slaHours: number
	dueAt: string
	overdue: boolean
  studentId: string
  student: string
  /** 分配记录 id（submission_reviewer 那一行），改派后会变 */
  assignmentId: string
  reviewedByMe: boolean
  /** 已提交的结论数。背靠背下只给数量，不给内容。 */
  reviewCount: number
  expectedReviews: number
}

/* ---- 学生匿名举报 ----

   综测小组的「扣分与异议」是实名提案、走班管终裁；举报是小组以外的学生提的，
   匿名，而且先由两名审核人背靠背复核，两人一致即落定，不一致才升给班管。

   前端在任何地方都拿不到举报人：后端根本不下发，这不是靠前端自觉不显示。 */

export type ReportStatus = 'reviewing' | 'applied' | 'dismissed' | 'escalated' | 'final'

export const REPORT_STATUS_LABEL: Record<ReportStatus, string> = {
  reviewing: '复核中',
  applied: '已扣分',
  dismissed: '不成立',
  escalated: '待班管终裁',
  final: '已终裁',
}

export type ReportDecision = 'uphold' | 'adjust' | 'reject'

export const REPORT_DECISION_LABEL: Record<ReportDecision, string> = {
  uphold: '属实 · 按举报认定',
  adjust: '属实 · 改分',
  reject: '不成立 · 维持原样',
}

/** 举报对象和小组提案一样有三种。 */
export type ReportKind = 'base' | 'penalty' | 'submission'

export const REPORT_KIND_LABEL: Record<ReportKind, string> = {
  base: '基础分',
  penalty: '扣分项',
  submission: '已定分条目',
}

/** 举报复核任务。和提交材料共用 /review/tasks，靠 type 区分——它判的是负分。 */
export interface ReportTask {
  id: string
  type: 'report'
  title: string
  kind: ReportKind
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  /** 举报主张的扣分，负值 */
  requestedScore: number
  status: ReportStatus
  submittedAt: string
  assignedAt: string
  slaHours: number
  dueAt: string
  overdue: boolean
  studentId: string
  student: string
  assignmentId: string
  reviewedByMe: boolean
  reviewCount: number
  expectedReviews: number
}

/** 举报台按需读取的原始材料；不包含举报人或未公开的审核备注。 */
export interface ReportTargetDetail {
  kind: ReportKind
  targetId: string | null
  currentScore: number | null
  recordedBasis: string
  fullScore: number | null
  perScore: number | null
  evidence: Evidence[]
  submission: {
    id: string
    title: string
    note: string
    claim: Claim
    requestedScore: number | null
    submittedAt: string | null
    ruleSnapshot: RuleSnapshot
    filedRuleSnapshot: RuleSnapshot | null
  } | null
}

export interface ReportTaskDetail {
  noteEvidence?: Evidence[]
  id: string
  kind: ReportKind
  student: string
  studentId: string
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  /** 只有扣分项有次数 */
  quantity: number | null
  proposedScore: number
  basis: string
  status: ReportStatus
  createdAt: string
  /** 举报当时这一条记着的分。扣分项是"已经扣了多少"，另外两种是"现在给了多少"。 */
  currentScore: number | null
  /** 举报人附上的材料。文件本身不带上传人，下载也只发给复核人和班管 */
  evidence: Evidence[]
  expectedReviews: number
  decidedReviews: number
  myReview: { decision: ReportDecision; score: number; reason: string; spentSeconds: number; at: string } | null
}

/** 我提交过的举报。只有本人看得到自己这一份。 */
export interface MyReport {
  id: string
  kind: ReportKind
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  quantity: number | null
  currentScore: number | null
  proposedScore: number
  basis: string
  status: ReportStatus
  finalScore: number | null
  decisionReason: string | null
  createdAt: string
  decidedAt: string | null
  /** 我传上去的材料。这里只回文件名，学生这一侧不发下载地址 */
  evidence: Evidence[]
  expectedReviews: number
  decidedReviews: number
}

/** 全班计分总览。只有分数和项目，没有依据原文和佐证。 */
export interface ClassPenaltyEntry {
  source?: 'manual' | 'ai' | 'admin_grant' | 'collective_grant'
  kind: ReportKind
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  /** base_score.id 或 submission.id。基础项还没有人调过时为 null */
  targetId: string | null
  score: number
  updatedAt: string | null
  /** 学生举报台不下发附件清单，固定为空 */
  evidence: Evidence[]
}

export interface ScoreHistoryEvent {
  kind: string
  at: string
  status: string
  score: number | null
  beforeScore: number | null
  reason: string
  round: number
  superseded: boolean
}

export interface ScoreHistory {
  publicity?: PublicityWindow | null
  serverNow?: string
  events: ScoreHistoryEvent[]
  evidence: Evidence[]
}

export interface BonusGrantInput {
  uploadId?: string
  requestId: string
  schemeVersion: string
  studentIds: string[]
  category: CategoryKey
  itemKey: string
  title: string
  note: string
  claim: Claim
}

export interface BonusGrantBatch {
  evidence?: Evidence[]
  id: string
  title: string
  note: string
  score: number
  createdAt: string
  members: { id: string; studentId: string; sid: string; name: string; score: number; status: SubmissionStatus; itemName: string }[]
}

export interface ClassPenaltyMember {
  userId: string
  sid: string
  name: string
  self: boolean
  penaltyTotal: number
  openReports: number
  entries: ClassPenaltyEntry[]
}

export interface ReportablePenaltyItem {
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  perScore: number
}

export interface ClassPenalties {
  items: ClassPenaltyMember[]
  penaltyItems: ReportablePenaltyItem[]
  dailyLimit: number
}

/** 班管视角的举报。复核人姓名可见，举报人依然不可见。 */
export interface AdminReport extends AdjudicationPermission {
  noteEvidence?: Evidence[]
  id: string
  studentUserId: string
  studentId: string
  student: string
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  kind: ReportKind
  quantity: number | null
  currentScore: number | null
  proposedScore: number
  basis: string
  status: ReportStatus
  finalScore: number | null
  decisionReason: string | null
  createdAt: string
  decidedAt: string | null
  evidence: Evidence[]
  reviews: {
    reviewer: string
    reviewerSid: string
    decision: ReportDecision | null
    score: number | null
    reason: string | null
    spentSeconds: number | null
    at: string | null
    decided: boolean
  }[]
}

/** 申诉任务复用同一个列表接口（tab=appeal），用 type 区分。
    两轮制下 tab=appeal 的口径是「我是本轮复评人且我还没交复评」，
    与「处理申诉」页 status=pending 完全同一批数据，只是那边给全量视图。 */
export interface AppealTask extends Appeal {
  type: 'appeal'
}

export interface ReviewTaskDetail {
  id: string
  title: string
  category: CategoryKey
  itemKey: string
  claim: Claim
  requestedScore: number | null
  ruleSnapshot: RuleSnapshot
  note: string
  submittedAt: string | null
  studentId: string
  student: string
  status: SubmissionStatus
  expectedReviews: number
  /** 仅本人当前结论可改；由后端同时检查同行提交状态与审核窗口。 */
  canEdit?: boolean
  peerSubmitted?: boolean
  evidence: Evidence[]
  /** 写理由的人随正文附上的图。只用来解析正文里的 evidence: 引用，
      不进"学生的佐证"那一格——两者的归属与可见性不是一回事。 */
  noteEvidence?: Evidence[]
  /** 我自己已提交的结论；看不到同行的 */
  myReview: {
    id: string
    decision: ReviewDecision
    score: number
    reason: string
    spentSeconds: number
    at: string
  } | null
	myClassificationSuggestion?: { id: string; category: CategoryKey; itemKey: string; reason: string; scope: ClassificationScope; status: ClassificationSuggestionStatus } | null
}

export interface ReviewDecisionResult {
  reviewId: string
  submissionStatus: SubmissionStatus
  finalScore: number | null
  conflict: boolean
	classificationArbitration?: boolean
	classificationSuggestionId?: string
}

export type ClassificationScope = 'within_category' | 'cross_category'
export type ClassificationSuggestionStatus = 'draft' | 'pending' | 'accepted' | 'rejected' | 'superseded' | 'withdrawn'

export interface ClassificationSuggestion extends AdjudicationPermission {
  noteEvidence?: Evidence[]
	id: string
	submissionId: string
	scope: ClassificationScope
	status: ClassificationSuggestionStatus
	fromCategory: CategoryKey
	fromItemKey: string
	toCategory: CategoryKey
	toItemKey: string
	reason: string
	source: 'item_review' | 'blind_audit' | 'admin'
	createdAt?: string
	studentId?: string
	studentSid?: string
	student?: string
	title?: string
	finalScore?: number | null
}

export interface ClassificationResolution {
	id: string
	submissionId: string
	category: CategoryKey
	itemKey: string
	score: number
	status: SubmissionStatus
}

export interface CategorySummary {
  category: CategoryKey
  total: number
  scored: number
  conflicts: number
  average: number | null
}

/** 我的审核量。按条均衡之后，"我负责哪个大项"不再成立，取而代之的是"我背了多少条"。 */
export interface ReviewLoad {
  assigned: number
  pending: number
  done: number
  /** 全班审核人的人均累计量，用来判断自己是不是被压了 */
  classAverage: number
  /** 全班累计量的极差 */
  spread: number
  /** 我在所有审核人里的排位（1 = 分得最多） */
  rank: number
  reviewerCount: number
  /** 我的裁定口径：通过 / 调分 / 驳回 各多少条 */
  decisions: Record<ReviewDecision, number>
  avgSpentSeconds: number
  /** 全组合计的同一组数字，用来对照自己是偏松还是偏严。不逐人拆分。 */
  groupDecisions: Record<ReviewDecision, number>
  groupAvgSpentSeconds: number
}

export interface ReviewHistoryRow {
  id: string
  submissionId: string
  title: string
  studentId: string
  student: string
  category: CategoryKey
  decision: ReviewDecision
  score: number
  reason: string
  spentSeconds: number
  at: string
  finalScore: number | null
  status: SubmissionStatus
  /** 终值与我的结论不同 —— 被仲裁或申诉改过 */
  changed: boolean
  canEdit?: boolean
}

/* ---- 名册 ---- */

export interface WhitelistRow {
  id: string
  sid: string
  name: string
  role: Exclude<Role, 'ops'>
  active: boolean
  registered: boolean
  registeredAt: string | null
  createdAt: string
  /** 「男」「女」或空串。只用于学院报表的性别列，空串表示名单里没带这一列 */
  gender?: string
}

export interface AdminUser {
  id: string
  sid: string
  name: string
  role: Exclude<Role, 'ops'>
  isDeputy?: boolean
  status: 'active' | 'disabled'
  primaryEmail: string | null
  sealed: boolean
	auditStatus?: 'unsealed' | 'sealed_unreviewed' | 'blocked' | 'complete' | string
	auditBlocker?: string
  lastLoginAt: string | null
  createdAt: string
}

/* ---- 分发（按条均衡，DESIGN §5.3） ----

   分配单位是**一条提交**，不是一个大项：谁审哪一条由均衡算法逐条算，
   目标是所有审核人的累计工作量尽量相等。大项限制取消了 ——
   审核人在哪个大项上都可能拿到条目，规则快照随条目走，本来也不需要"在这个大项里待久了"。 */

export interface DispatchReviewer {
  userId: string
  sid: string
  name: string
  role: Exclude<Role, 'ops'>
  /** 暂停参与分发：请假、退班委时用。不删人，只是不再分新的给他 */
  paused: boolean
  /** 累计分到的条目数（本方案版本内），均衡算法要拉平的就是这个数 */
  assigned: number
  /** 分到了但还没交结论 */
  pending: number
  done: number
}

export interface DispatchState {
  policy: 'balanced_per_submission'
  /** 字符串而不是数字：int64 的种子超出 JS 安全整数范围，当数字用会被静默取整 */
  seed: string
  avoidSelf: boolean
  /** 自动分发：学生一提交就分。关掉之后只能手动补分发 */
  auto: boolean
  reviewers: DispatchReviewer[]
  /** 已提交但还没有审核人的条目数 */
  unassigned: number
  assignedTotal: number
  pendingTotal: number
  /** 累计分配量的极差（max−min）。0 = 完全均衡，这是这一页唯一要盯的数 */
  spread: number
  lastRunAt: string | null
  lastRunBy: string | null
}

export interface DispatchPlanRow {
  reviewerId: string
  name: string
  sid: string
  before: number
  after: number
  delta: number
}

/** 试算结果。不写库，给管理员看"执行之后每个人会背多少"。 */
export interface DispatchPlan {
  policy: string
  seed: string
  avoidSelf: boolean
  /** 这一次会被分配的条目数 */
  targets: number
  rows: DispatchPlanRow[]
  spreadBefore: number
  spreadAfter: number
  /** 分不出去的条目与原因。人手不足或候选全是本人时出现，必须显式列出来而不是静默跳过 */
  blocked: { submissionId: string; title: string; student: string; reason: string }[]
}

export interface DispatchRunResult extends DispatchPlan {
  assigned: number
}

/** 某个审核人手上的待审队列。改派与整体转出都从这里进。 */
export interface DispatchQueueRow {
  submissionId: string
  title: string
  student: string
  studentId: string
  category: CategoryKey
  submittedAt: string | null
  /** 同一条的另一名审核人 */
  peer: string | null
  peerDecided: boolean
}

/* ---- 管理端提交台 ---- */

export interface AdminSubmission extends AdjudicationPermission, ForceRejectPermission {
  id: string
  studentId: string
  student: string
  category: CategoryKey
	filedCategory?: CategoryKey
	filedItemKey?: string
  itemKey: string
  title: string
  requestedScore: number | null
  finalScore: number | null
  status: SubmissionStatus
  submittedAt: string | null
  updatedAt: string
  reviews: NamedReview[]
  evidence?: Evidence[]
  noteEvidence?: Evidence[]
  /** 学生当初填的申报值与补充说明，以及提交时冻结的规则。仲裁台要对着它们判。 */
  claim?: Claim
  note?: string
  ruleSnapshot?: RuleSnapshot
  /** 最近一条申诉，没有则整组缺省 */
  appealId?: string
  appealRound?: AppealRound
  appealStatus?: AppealStatus
}

/* ---- 专业素质分 ---- */

export interface GpaRow {
  userId: string
  sid: string
  name: string
  score: number | null
  batch: string | null
  updatedAt: string | null
}

export interface GpaState extends List<GpaRow> {
  imported: number
  total: number
  complete: boolean
  average: number | null
  latestAt: string | null
  weights: Record<CategoryKey, number>
  honorTopPercent: number
}

export interface GpaImportResult {
  batch: string
  imported: number
  totalStudents: number
  missing: { sid: string; name: string }[]
  complete: boolean
}

/* ---- 闸门与结算 ---- */

export interface GateCondition {
  key: 'sealed' | 'allReviewsFinal' | 'classificationResolved' | 'noConflict' | 'gpaImported'
  label: string
  ok: boolean
  detail: string
}

export interface Gate {
  open: boolean
  forced: boolean
  conditions: GateCondition[]
  studentCount: number
  sealedCount: number
  pendingConflicts: number
	unfinalizedReviews: number
	pendingClassifications: number
  gpaImportedCount: number
  schemeId: string
  schemeVersion: string
  forceReason: string | null
  forcedAt: string | null
}

export interface SettlementRow {
  userId: string
  sid: string
  name: string
  categoryScores: Record<string, number>
  totalScore: number
  classRank: number
  majorRank: number
  honor: boolean
  /** 命中的奖学金档位名；空串表示未进档，或这次结算早于档位功能 */
  awardTier: string
  details: unknown
}

export interface SettlementResult {
  runId: string
  completedAt: string
  /** 复用了上一次未失效的结算，没有重新算 */
  reused: boolean
  items: SettlementRow[]
}

/** 当前已认定成绩。其他大项独立排名，专业和总排名等待专业成绩导齐。 */
export interface CurrentScore {
  schemeId: string
  calculatedAt: string
  classSize: number
  gpaImported: number
  /** 全班专业成绩已导齐，专业排名和总排名可用。 */
  rankingReady: boolean
  categoryScores: Record<string, number | null>
  /** 专业成绩未导齐时只省略 major，其余大项按当前认定分排名。 */
  categoryRanks: Partial<Record<string, number>>
  totalScore: number | null
  classRank: number | null
  majorRank: number | null
  pendingItems: number
}

/** 当前成绩与独立的正式结算快照；过期快照不可作为有效排名。 */
export type MyScore = { current: CurrentScore } & (
  | { settled: false }
  | {
      settled: true
      /** 结算后又有分数变化，这份快照已过期 */
      stale: boolean
      runId: string
      schemeId: string
      completedAt: string
      categoryScores: Record<string, number>
      categoryRanks: Record<string, number>
      totalScore: number
      classRank: number
      majorRank: number
      honor: boolean
      /** 命中的奖学金档位名；空串表示未进档 */
      awardTier: string
      classSize: number
      /** 这次结算实际发出去的获奖人数。名额是每档四舍五入后相加的，
          不等于"班级人数 × 三好线"，所以由后端数快照给出，前端不再推算。 */
      awardQuota: number
      awardPosition: number
      honorTopPercent: number
      distribution: { min: number; median: number; max: number }
      details: MyScoreDetails
      configSnapshot: SchemeConfig
    }
)

export interface MyScoreDetails {
  categories: Record<
    string,
    { itemTotal: number; baseTotal: number; penaltyTotal: number; beforeCap: number; total: number; maxTotal: number }
  >
  items: {
    id: string
    category: CategoryKey
    itemKey: string
    title: string
    score: number
    status: SubmissionStatus
    ruleSnapshot: RuleSnapshot
    reviews: StudentReview[]
    appeals: unknown[]
  }[]
  baseItems: {
    id?: string
    category: CategoryKey
    itemKey: string
    name: string
    kind: 'base' | 'penalty'
    fullScore: number | null
    score: number
    basis: string
    recorded: boolean
  }[]
  gpa?: unknown
  gpaMissing?: boolean
}

/* ---- 看板 ---- */

export interface AdminStats {
  users: number
  sealed: number
  gpaImported: number
  submissions: Partial<Record<SubmissionStatus, number>>
  reviews: number
  activeAppeals: number
  pendingConflicts: number
  gate: Gate
  settlement: { runId: string | null; completedAt: string | null; stale: boolean }
  distribution: { min: number; average: number; median: number; max: number } | null
	reviewProgress?: { item: Record<string, number>; scorecard: Record<string, number>; overdue: { item: number; scorecard: number } }
	ranking?: {
		scoredItems: number
		totalItems: number
		pendingFinal: number
        calculatedAt?: string
        rankingReady?: boolean
        classSize?: number
        gpaImported?: number
        items: { sid: string; name: string; total: number | null; scored: number; pending: number; conflicts: number; classRank?: number | null; categoryScores?: Record<string, number | null>; categoryRanks?: Record<string, number> }[]
	}
}

export interface AdminReviewProgressRow extends ForceRejectPermission {
  id: string
  submissionId?: string
  finalScore?: number | null
  kind: 'item' | 'scorecard'
  status: 'unassigned' | '0/2' | '1/2' | 'pending_admin' | 'complete' | 'blocked'
  studentId: string
  student: string
  title: string
  reviewers: { id: string; sid: string; name: string; submitted?: boolean; position?: number; status?: string }[]
  expected: number
  submitted: number
  assignedAt: string | null
  waitingSeconds: number
  overdue: boolean
  detailUrl: string
  batchId?: string
	completionMode?: 'clean' | 'resolved' | ''
	trail: { at: string; event: string; detail: string }[]
}

/* ---- 导出 ---- */

export type ExportKind = 'summary' | 'detail' | 'archive' | 'college'

export interface ExportJob {
  jobId: string
  kind: ExportKind
  status: 'queued' | 'running' | 'complete' | 'failed'
  runId?: number
  createdAt?: string
  startedAt?: string | null
  finishedAt?: string | null
  expiresAt?: string | null
  error?: string | null
  downloadUrl?: string
  downloadExpiresIn?: number
  filename?: string
  sizeBytes?: number
  etag?: string
}

/* ---- 审计 ---- */

export interface AuditEntry {
  id: string
  actorId?: string
  actorRole: Role | 'system'
  actorSid: string | null
  actor: string | null
  action: string
  resourceType: string
  resourceId: string | null
  before?: unknown
  after?: unknown
  metadata: unknown
  ip: string | null
  userAgent: string | null
  createdAt: string
}

/* ---- 运维台 ---- */

export interface Tenant {
  id: string
  name: string
  slug: string | null
  state: 'running' | 'archived'
  archived: boolean
  storageBytes: number
	storageCalibratedAt: string | null
  admins: TenantAdmin[]
  createdAt: string
  updatedAt: string
}

export interface TenantAdmin {
  sid: string
  name: string
  registered: boolean
}

export interface TenantMember {
  whitelistId: string
  userId: string | null
  sid: string
  name: string
  role: 'student' | 'group' | 'class_admin'
  rosterActive: boolean
  registered: boolean
  accountStatus: 'active' | 'disabled' | null
  registeredAt: string | null
  lastLoginAt: string | null
}

export interface TenantCreated {
  id: string
  name: string
  slug: string
  admin: TenantAdmin
}

export interface PlatformTemplate {
  id: string
  name: string
  active: boolean
  createdAt: string
  retiredAt: string | null
  sourceTenantId?: string
}

export interface TemplateShareRequest {
	id: string
	tenantId: string
	tenantName: string
	schemeId: string
	name: string
	status: 'pending' | 'approved' | 'rejected' | 'canceled'
	reviewReason: string | null
	reviewedBy: string | null
	templateId: string | null
	createdAt: string
	reviewedAt: string | null
}

export interface AdminTemplateShareRequest {
	id: string
	schemeId: string
	name: string
	status: 'pending' | 'approved' | 'rejected' | 'canceled'
	reviewReason: string | null
	templateId: string | null
	createdAt: string
	reviewedAt: string | null
}

export interface AvailableTemplate {
  id: string
  name: string
  createdAt: string
}

export interface MailConfig {
  notificationStats?: { suppressedRecipients: number; pendingBatches: number; failedBatches: number; lastFeedbackAt: string | null }
  config: {
    notificationsEnabled?: boolean
    notificationsSince?: string
    sesNotificationDomain?: string
    sesNotificationFrom?: string
    sesNotificationFromName?: string
    provider?: string
    perMinute?: number
    perDay?: number
    quietStart?: string
    quietEnd?: string
    /** 腾讯云 SES API 通道。全部留空表示沿用部署环境变量，凭据永远不会下发。 */
    sesRegion?: string
    sesFrom?: string
    sesFromName?: string
    sesReplyTo?: string
    sesTemplateIds?: Record<string, number>
  }
  activeProvider: string
  /** 'database' 表示通道来自本页配置，'environment' 表示来自部署 Secret。 */
  secretSource: string
  /** 只说明凭据是否保存过，不回显内容。 */
  sesSecretIdSet?: boolean
  sesSecretKeySet?: boolean
  /** 服务端配了 MAIL_SECRET_KEY 才能保存 API 凭据。 */
  secretKeyReady?: boolean
}

export type MailCategory = 'progress' | 'results' | 'tasks' | 'decisions' | 'deadlines' | 'receipts' | 'class_activity'
export type MailMode = 'off' | 'digest' | 'immediate' | 'frequent'
export interface MailPreferences {
  enabled: boolean
  categories: Record<MailCategory, MailMode>
  digestTime: string
  quietStart: string
  quietEnd: string
  dailyLimit: number
}
export interface MailPreferenceState {
  preferences: MailPreferences
  notificationsPaused: boolean
  deliveryState: string
}

export interface AIConfigState {
  config: {
    baseUrl: string
    textModel: string
    visionModel: string
    agentModel: string
  }
  sources: {
    baseUrl: 'database' | 'environment' | 'none'
    apiKey: 'database' | 'environment' | 'none'
    textModel: 'database' | 'environment' | 'none'
    visionModel: 'database' | 'environment' | 'none'
    agentModel: 'database' | 'environment' | 'textModel' | 'none'
  }
  enabled: boolean
  ready: boolean
  statusReason: string
  legacyReady?: boolean
  agentReady?: boolean
  providerCount?: number
  routeCount?: number
  routeStatus?: Partial<Record<ModelPurpose, {
    ready: boolean
    providerId?: string
    provider?: string
    model?: string
    legacy?: boolean
    reason?: string
  }>>
  knowledgeEnabled: boolean
  agentActionsEnabled: boolean
  knowledgeEgressEnabled: boolean
  knowledgeReady: boolean
  knowledgeStatusReason: string
  limits: {
    agentMaxSteps: number
    agentTimeoutSeconds: number
    agentMaxAnswerKb: number
    agentToolResultKb: number
    agentToolScanMb: number
    agentDailyMessages: number
    materialDailyBatches: number
    materialActiveBatches: number
    knowledgeMaxFilesPerClass: number
    knowledgeMaxStorageMbPerClass: number
    materialMaxItems: number
    materialMaxFileMb: number
    materialMaxBatchMb: number
    materialMaxPdfPages: number
    materialConcurrency: number
    materialRetentionDays: number
    materialAllowedFormats: AIMaterialFormat[]
		agentAttachmentMaxFileMb: number
		agentAttachmentMaxMessageMb: number
		agentAttachmentDailyMb: number
		agentAttachmentMaxCount: number
  }
	usage?: { dailyMessages: number; dailyAttachments: number; dailyAttachmentBytes: number }
  apiKeySet: boolean
  storedApiKeySet: boolean
  secretKeyReady: boolean
}

export interface AIStatus {
  /** 只有开关已开且后端能解析出完整安全配置时才为 true。 */
  enabled: boolean
  configured: boolean
  limits: AIBatchLimits
}

export interface AITestResult {
  ok: boolean
  slot: 'text' | 'vision' | 'agent'
  model: string
  durationMs: number
}

export type ModelPurpose = 'material.vision' | 'material.compose' | 'knowledge.ocr' | 'agent.text' | 'agent.vision'

export interface AIProviderCapabilities {
  json: boolean
  stream: boolean
  vision: boolean
  models: boolean
}

export interface AIProvider {
  id: string
  name: string
  baseUrl: string
  authType: 'bearer'
  timeoutSeconds: number
  maxRetries: number
  capabilities: AIProviderCapabilities
  enabled: boolean
  revision: number
  createdAt: string
  updatedAt: string
  apiKeySet: boolean
}

export interface ModelRoute {
  purpose: ModelPurpose
  providerId: string
  providerName: string
  model: string
  parameters: Record<string, unknown>
  revision: number
  updatedAt: string
}

export interface ModelRouteState {
  items: ModelRoute[]
  purposes: ModelPurpose[]
}

export interface ModelRouteTestResult {
  ok: boolean
  purpose: ModelPurpose
  providerId: string
  provider: string
  model: string
  durationMs: number
}

export type KnowledgeDocumentStatus =
  | 'uploading'
  | 'queued'
  | 'processing'
  | 'ready'
  | 'partial'
  | 'unsupported'
  | 'failed'
  | 'superseded'
  | 'deleted'

export interface KnowledgeDocument {
  id: string
  filename: string
  logicalPath: string
  displayName: string
  mediaType: string
  sizeBytes: number
  status: KnowledgeDocumentStatus
  searchable: boolean
  extractor: string | null
  entries: number
  pageCount: number | null
  sheetCount: number | null
  warning: string | null
  error: string | null
  createdAt: string
  updatedAt: string
  publishedAt: string | null
}

export interface KnowledgePolicy {
  externalProcessingApproved: boolean
  approvedBy: string | null
  approvedAt: string | null
  revokedAt: string | null
}

export interface KnowledgeLimits {
  fileMb: number
  filesPerClass: number
  storageMbPerClass: number
  pdfPages: number
}

export interface KnowledgeState {
  policy: KnowledgePolicy
  limits: KnowledgeLimits
  stats: {
    totalFiles: number
    classFiles: number
    evidenceFiles: number
    readyFiles: number
    processingFiles: number
    storageBytes: number
    updatedAt: string | null
  }
  documents: KnowledgeDocument[]
  evidence: KnowledgeEvidence[]
  platform: {
    knowledgeEnabled: boolean
    knowledgeEgressEnabled: boolean
    aiEnabled: boolean
  }
}

export interface KnowledgeEvidence {
  id: string
  filename: string
  mediaType: string
  sizeBytes: number
  status: 'pending' | 'ready'
  kind: 'claim' | 'note'
  source: 'submission' | 'appeal' | 'objection'
  ownerId: string
  title: string
  subjectSid: string
  subjectName: string
  uploaderSid: string
  uploaderName: string
  createdAt: string
}

export interface KnowledgeEntry {
  id: string
  documentId?: string
  filename?: string
  logicalPath?: string
  kind?: string
  locator: Record<string, unknown>
  metadata?: Record<string, unknown>
  lineCount?: number
  charCount?: number
  startLine: number
  endLine: number
  text: string
}

export interface KnowledgeDocumentDetail extends KnowledgeDocument {
  entriesPreview: KnowledgeEntry[]
  downloadUrl: string
  downloadExpiresIn: number
}

export interface ClassResource {
  id: string
  filename: string
  logicalPath: string
  displayName: string
  mediaType: string
  sizeBytes: number
  updatedAt: string
  publishedAt: string
}

export interface ClassResourceDownload {
  url: string
  filename: string
  expiresIn: number
}

export interface KnowledgePresignResult {
  documentId: string
  uploadUrl: string
  uploadFields: Record<string, string>
  expiresIn: number
  limits: KnowledgeLimits
}

export type PlatformKnowledgeRole = 'student' | 'group' | 'class_admin'
export type PlatformKnowledgeSourceKind = 'builtin' | 'custom'
export type PlatformKnowledgeStatus = KnowledgeDocumentStatus | 'retired'

export interface PlatformKnowledgeDocument {
  id: string
  sourceKind: PlatformKnowledgeSourceKind
  builtinKey: string
  filename: string
  displayName: string
  allowedRoles: PlatformKnowledgeRole[]
  views: string[]
  keywords: string[]
  sortOrder: number
  status: PlatformKnowledgeStatus
  enabled: boolean
  searchable: boolean
  extractor: string
  entries: number
  pageCount: number | null
  sheetCount: number | null
  warning: string
  error: string
  contentVersion: string
  contentHash: string
  mediaType: string
  sizeBytes: number
  createdAt: string
  updatedAt: string
  publishedAt: string | null
}

export interface PlatformKnowledgeState {
  limits: { fileMb: number; files: number; storageMb: number; pdfPages: number }
  stats: { totalFiles: number; readyFiles: number; processingFiles: number; storageBytes: number }
  documents: PlatformKnowledgeDocument[]
}

export interface PlatformKnowledgeDocumentDetail extends PlatformKnowledgeDocument {
  entriesPreview: KnowledgeEntry[]
  downloadUrl: string
  downloadExpiresIn: number
  content?: string
}

export interface PlatformKnowledgePresignResult {
  documentId: string
  uploadUrl: string
  uploadFields: Record<string, string>
  expiresIn: number
  limits: PlatformKnowledgeState['limits']
}

export type AgentMessageStatus = 'queued' | 'running' | 'complete' | 'failed' | 'canceled'
export type AgentActionKind = 'submission_draft' | 'review_draft' | 'scheme_draft' | 'export_draft' | 'arbitration_reason_draft'
export type AgentActionStatus = 'proposed' | 'prepared' | 'applied' | 'rejected' | 'expired' | 'stale'

export interface AgentAttachment {
  id: string
  filename: string
  mediaType: 'image/jpeg' | 'image/png' | 'image/webp'
  sizeBytes: number
}

export interface AgentAttachmentLink {
  attachmentId: string
  filename: string
  mediaType: AgentAttachment['mediaType']
  sizeBytes: number
  downloadUrl: string
  expiresIn: number
}

export interface AgentCitation {
  documentId: string
  entryId: string
  filename: string
  logicalPath: string
  locator: Record<string, unknown>
  excerpt: string
  scope: 'class' | 'platform' | 'scheme' | 'business'
  sourceKind: string
  downloadable: boolean
  audienceRole: PlatformKnowledgeRole | ''
}

export type AgentToolName = 'find_files' | 'grep' | 'read_text' | 'inspect_table' | 'file_stats' | 'current_scheme' | 'search_product_help' | 'read_current_context' | 'read_evidence'

export interface AgentPageContext {
  view: string
  resourceKind?: 'submission' | 'appeal'
  resourceId?: string
  resourceLabel?: string
  revision?: string
  evidenceId?: string
  draft?: { reason: string; score: string; category: string; itemKey: string }
}

export interface AgentToolTrace {
  seq: number
  tool: AgentToolName
  /** running 表示这一步已经开始但工具还没跑完，是实时回显的中间态。 */
  status: 'running' | 'complete' | 'failed' | 'canceled'
  /** 面向用户的一句进度说明，不是模型的完整推理。 */
  thought: string
  summary: string
  durationMs: number
}

export interface AgentActionDiff {
  field: string
  before?: unknown
  after?: unknown
}

export interface AgentAction {
  context?: AgentPageContext | null
  id: string
  kind: AgentActionKind
  status: AgentActionStatus
  title: string
  summary: string
  diff: AgentActionDiff[]
  citations: AgentCitation[]
  expiresAt: string
  targetView: string
  createdAt: string
}

export interface PreparedAgentAction extends AgentAction {
  confirmToken: string
  confirmExpiresAt: string
  risk: string
}

export interface AppliedAgentAction {
  id: string
  kind: AgentActionKind
  status: 'applied'
  navigateTo: string
  resourceId: string
  draft?: { id: string; status: 'draft'; source?: 'ai'; name?: string }
  prefill?: Record<string, unknown>
}

export interface AgentMessage {
  context?: AgentPageContext
  id: string
  role: 'user' | 'assistant'
  status: AgentMessageStatus
  content: string
  attachments: AgentAttachment[]
  citations: AgentCitation[]
  toolTrace: AgentToolTrace[]
  /** 给出最终回答之前的那一句说明，没有对应的工具行。 */
  finalThought: string
  actions: AgentAction[]
  sourceRevoked: boolean
  error: string | null
  createdAt: string
  finishedAt: string | null
}

export interface AgentConversationSummary {
  id: string
  title: string
  preview: string
  createdAt: string
  updatedAt: string
}

export interface AgentConversation {
  id: string
  title: string
  messages: AgentMessage[]
  createdAt: string
  updatedAt: string
}

export interface AgentStatus {
  enabled: boolean
  reason: '' | 'agent_disabled' | 'knowledge_disabled' | 'knowledge_consent_required' | 'quota_exceeded'
  actionsEnabled: boolean
  externalProcessingApproved: boolean
  quota: { used: number; limit: number }
	attachmentQuota: { maxFileMb: number; maxMessageMb: number; dailyMb: number; dailyUsedBytes: number; maxCount: number }
  model: { configured: boolean; visionConfigured?: boolean; inherited: boolean }
}

export interface AgentSource {
  documentId: string
  entryId: string
  filename: string
  logicalPath: string
  locator: Record<string, unknown>
  /** 当前已发布方案没有原件可下，这里是空串。 */
  downloadUrl: string
  expiresIn: number
  content: string
  mediaType: string
}

export interface MailLogRow {
  feedbackStatus?: string
  id: string
  tenantId: string | null
  eventId: string | null
  /** 运维接口只返回哈希，不返回明文地址。 */
  recipientHash: string
  provider: string
  status: string
  attempt: number
  error: string | null
  createdAt: string
  template?: string
  errorCode?: string | null
  messageId?: string | null
}

export interface QueueGroup {
  stream: string
  group: string
  length: number
  pending: number
  lag: number
  consumers: number
  lastDeliveredId: string
}

export interface QueueState extends List<QueueGroup> {
  deadLetters: number
  outboxPending: number
  deliveryAlertCount?: number
  deliveryAlerts?: { group: string; eventId: string; eventType: string; reason: string; since: string }[]
}

export interface DeadLetter {
	id: string
	values: Record<string, string>
}

export interface DeadLetterState extends List<DeadLetter> { nextCursor: string | null }

export interface WorkerState {
  name: string
  lastHeartbeat: string | null
  heartbeatAlive: boolean
  heartbeatStatus: 'alive' | 'stale' | 'missing' | 'error'
  heartbeatError?: string
}

export interface CronRow {
  name: string
  schedule: string
  handler: string
  last: string | null
	next?: string | null
	config?: Record<string, unknown>
	durationMs?: number | null
	result?: string | null
	error?: string | null
}

export interface BackupRow {
  id: string
  kind: string
  status: string
  offsite: boolean
  detail: unknown
  createdAt: string
  finishedAt: string | null
}

export interface BackupState extends List<BackupRow> {
	page: number
	pageSize: number
	total: number
  policy: {
    enabled: boolean
    automatic: boolean
    schedule: string
    retentionDays: number
    offsite: boolean
    remote: { enabled: boolean; bucket: string; retentionDays: number; lastPushAt: string | null; lastPushOk: boolean }
  }
}

/** 远程备份桶。两个凭据只写不读，接口永远不下发内容，只说设没设过。 */
export interface BackupRemoteConfig {
  enabled: boolean
  endpoint: string
  bucket: string
  region: string
  prefix: string
  useSsl: boolean
  pathStyle: boolean
  retentionDays: number
}

export interface BackupRemoteState {
  config: BackupRemoteConfig
  accessKeySet: boolean
  secretKeySet: boolean
  /** 服务端配了 BACKUP_REMOTE_SECRET_KEY，能加密保存桶凭据。 */
  secretKeyReady: boolean
  /** 服务端配了 BACKUP_REMOTE_RECIPIENT，能加密归档。缺了就传不了。 */
  recipientReady: boolean
}

/** 保存时可以只带改动的字段；两个凭据留空表示不改动，传空串表示清空。 */
export type BackupRemoteUpdate = Partial<BackupRemoteConfig & { accessKey: string; secretKey: string }>

export interface BucketUrlParsed {
  endpoint: string
  bucket: string
  region: string
  prefix: string
  useSsl: boolean
  pathStyle: boolean
}

export interface BackupRemoteTestResult {
  ok: boolean
  wrote: boolean
  read: boolean
  removed: boolean
  recipientReady: boolean
}

export interface LifecyclePolicy {
	backupEnabled: boolean
	backupSchedule: string
	backupRetentionDays: number
	exportRetentionDays: number
	knowledgeDeleteGraceHours: number
	agentAttachmentGraceHours: number
	storageReconcileMinutes: number
	knowledgeMaxPdfPages: number
	knowledgeMaxArchiveMembers: number
	knowledgeMaxArchiveMb: number
	knowledgeMaxArchiveRatio: number
	knowledgeMaxArchiveDepth: number
	knowledgeMaxExtractedTextMb: number
	knowledgeConverterTimeoutSeconds: number
}

export interface LifecycleState { policy: LifecyclePolicy }

export interface FlagsState {
  flags: Record<string, boolean | number | string[]>
  /** 服务端强制锁定的开关；当前 AI 已由 Agent 配置页接管，不再锁定。 */
  locked: string[]
  deploymentLimits: {
    apiRateLimitPerMinute: number
    apiRateLimitBurst: number
    passwordHashConcurrency: number
  }
}

export interface HealthState {
  status: 'ok' | 'degraded'
  components: { name: string; status: string; detail?: string }[]
}

export interface DeployState {
  appEnv: string
  imageTag: string
  gitSha: string
  migrationVersion: number
  databaseRole: { name: string; superuser: boolean; bypassRls: boolean }
  opsDatabaseRole?: { name: string; superuser: boolean; bypassRls: boolean }
  aiEnabled: boolean
}

export interface OpsAuditEntry {
  id: string
  actor: string
  action: string
  resourceType: string
  resourceId: string | null
  metadata: unknown
  ip: string | null
  createdAt: string
}
