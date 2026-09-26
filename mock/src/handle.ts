import { mockGovernance, governanceMode, governanceHelper, governanceOpenHelper, MOCK_GOVERNANCE_EXECUTION } from './governance'
import demoCrest from '../../examples/crest.svg?raw'
import { mockMCPSettings } from './mcp'
import { mockMailConfig, updateMockMail, resetMockMail } from './opsMail'
import { DEFAULT_OPS_FLAGS } from './opsFlags'
import type {
  Appeal,
  AppealDetail,
  AIProvider,
  ClassInfo,
  ClassificationSuggestion,
  Evidence,
  Gate,
  MyScore,
  MyScorecard,
  ModelRouteState,
  NamedReview,
  Objection,
  ReviewStudent,
  ReviewTaskDetail,
  StudentScorecard,
  Submission,
  SubmissionDetail,
  TrailEntry,
  WindowState,
} from '@/api/types'
import type { CategoryKey, Role, SchemeConfig } from '@/lib/types'
import type { MailPreferences } from '@/api/types'
import { MAIL_CATEGORIES, mailPreset } from '@/lib/mailPreferences'
import { claimableItems } from '@/lib/schemeTree'
import { scoreBounds } from '@/lib/claim'
import {
  CLASS_ID,
  CLASS_NAME,
  DEMO_CODE,
  DEMO_PASSWORD,
  WINDOW_CLOSE,
  WINDOW_OPEN,
  adminSeals,
  adminUsers,
  db,
  dispatchState,
  findItem,
  nid,
  nowIso,
  pageOf,
  persist,
  provisionTourUser,
  publicUser,
  resetStore,
  reviewTasksFor,
  snapshot,
  tokenFor,
  uploadUrl,
  userById,
  userBySid,
  userFromToken,
  wantScore,
  applyTourCast,
  isTourUser,
  landMockScore,
  TOUR_CASTS,
  TOUR_SID,
  type DemoUser,
  type MockReport,
  type TourCast,
} from './db'
import { fail } from './errors'
import { reportScoreHistory, reportTargetDetail } from './reportMaterial'
import { adjudicationHistory } from './adjudicationHistory'
import {
  adjudicationPermission,
  adjudicationRows,
  appendAudit,
  applySubmissionDecision,
  clearIneligibleDeputy,
  decisionReason,
  decisionScore,
  deputyPath,
  requireAdjudicationRead,
  requireAdjudicationTarget,
} from './adjudication'

type Body = Record<string, unknown>

function json(body: unknown): Body {
  if (!body || typeof body !== 'object' || body instanceof FormData) return {}
  return body as Body
}
function str(v: unknown) {
  return typeof v === 'string' ? v : ''
}

/* 桶链接解析。规则和后端 opsconfig.ParseBucketURL 一致，只有两条：
   路径里还有段就是路径式（第一段是桶名），路径为空就是虚拟主机式
   （第一个 DNS 标签是桶名）。地域猜不出就留空由人填。 */
function parseBucketUrl(raw: string) {
  const text = raw.trim()
  if (!text) fail(422, 'bucket_url_invalid', '桶链接未填写')
  let url: URL
  try {
    url = new URL(text.includes('://') ? text : `https://${text}`)
  } catch {
    return fail(422, 'bucket_url_invalid', '桶链接格式不正确')
  }
  if (url.username || url.password) fail(422, 'bucket_url_invalid', '桶链接里不要带账号密码，凭据请填在下面的两个框里')
  if (url.search || url.hash) fail(422, 'bucket_url_invalid', '桶链接不允许带查询参数或片段')
  if (url.protocol !== 'https:' && url.protocol !== 'http:') fail(422, 'bucket_url_invalid', '桶链接必须是 http 或 https')

  const segments = url.pathname.split('/').filter(Boolean)
  const useSsl = url.protocol === 'https:'
  if (segments.length > 0) {
    const prefix = segments.slice(1).join('/')
    return {
      endpoint: url.host, bucket: segments[0], region: guessBucketRegion(url.host),
      prefix: prefix ? `${prefix}/` : '', useSsl, pathStyle: true,
    }
  }
  const dot = url.host.indexOf('.')
  if (dot <= 0 || dot === url.host.length - 1) fail(422, 'bucket_url_invalid', '从链接里认不出桶名，请改用 主机名/桶名 的写法')
  return {
    endpoint: url.host.slice(dot + 1), bucket: url.host.slice(0, dot),
    region: guessBucketRegion(url.host.slice(dot + 1)), prefix: '', useSsl, pathStyle: false,
  }
}

function guessBucketRegion(endpoint: string) {
  const host = endpoint.split(':')[0].toLowerCase()
  const labels = host.split('.')
  if (host.endsWith('.r2.cloudflarestorage.com')) return 'auto'
  if (labels.length > 1 && labels[0] === 'cos') return labels[1]
  if (labels[0]?.startsWith('oss-')) return labels[0]
  if (labels.length > 1 && labels[0] === 's3' && labels[1] !== 'amazonaws') return labels[1]
  if (host === 's3.amazonaws.com') return 'us-east-1'
  return ''
}

/* 仲裁台顶上那三个筛选框对每个页签都生效。原来 mock 直接把整张表分页返回，
   于是「申诉」和「小组提案」上打字没有任何反应——看起来就像这两页没有搜索。 */
function filterRows<T>(rows: T[], query: URLSearchParams, text: (row: T) => (string | null | undefined)[], category?: (row: T) => string) {
  const student = (query.get('student') ?? '').trim().toLowerCase()
  const q = (query.get('q') ?? '').trim().toLowerCase()
  const cat = (query.get('category') ?? '').trim()
  return rows.filter((row) => {
    const fields = text(row).filter((value): value is string => !!value).map((value) => value.toLowerCase())
    if (student && !fields.some((value) => value.includes(student))) return false
    if (q && !fields.some((value) => value.includes(q))) return false
    if (cat && category && category(row) !== cat) return false
    return true
  })
}

function requireUser(user: DemoUser | null): DemoUser {
  if (!user) fail(401, 'unauthenticated', '请先登录')
  return user
}

function requireRole(user: DemoUser | null, roles: Role[]): DemoUser {
  const u = requireUser(user)
  if (!roles.includes(u.role)) fail(403, 'forbidden', '当前身份不能做这项操作')
  return u
}

function resetMemberPassword(user: DemoUser): number {
  if (!user.registered || user.status !== 'active' || (user.role !== 'student' && user.role !== 'group')) return 0
  const store = db()
  user.password = ''
  user.registered = false
  user.sessionVersion += 1
  const row = store.whitelist.find((item) => item.sid === user.sid)
  if (row) {
    row.registered = false
    row.registeredAt = null
    row.role = user.role
  }
  for (const [id, ticket] of Object.entries(store.registerTickets)) if (ticket.sid === user.sid) delete store.registerTickets[id]
  for (const [id, token] of Object.entries(store.resetTokens)) if (token.sid === user.sid) delete store.resetTokens[id]
  for (const [id, challenge] of Object.entries(store.emailChallenges)) if (challenge.userId === user.id) delete store.emailChallenges[id]
  return 1
}

function match(pattern: string, path: string): Record<string, string> | null {
  const a = pattern.split('/')
  const b = path.split('/')
  if (a.length !== b.length) return null
  const out: Record<string, string> = {}
  for (let i = 0; i < a.length; i++) {
    if (a[i].startsWith(':')) out[a[i].slice(1)] = decodeURIComponent(b[i] || '')
    else if (a[i] !== b[i]) return null
  }
  return out
}

function windowState(): WindowState {
  const s = db()
  return {
    window: s.timeline.window,
    capabilities: s.timeline.capabilities,
    honorRoll: s.timeline.honorRoll,
    collegeName: s.timeline.collegeName,
    enrollmentClass: s.timeline.enrollmentClass,
    academicYear: s.timeline.academicYear,
    serverNow: nowIso(),
    sealed: false,
    schemeVersion: s.scheme.version,
  }
}

function baseItemsFor(user: DemoUser) {
  const scheme = db().scheme
  const recorded = db().bases.filter((b) => b.userId === user.id)
  const out = []
  for (const cat of scheme.categories) {
    for (const b of cat.baseItems) {
      if (b.studentClaim) continue
      const rec = recorded.find((r) => r.itemKey === b.key)
      out.push({
        id: rec?.recorded ? `${user.id}:${b.key}` : null,
        category: cat.key,
        categoryName: cat.name,
        itemKey: b.key,
        itemName: b.name,
        kind: 'base' as const,
        fullScore: b.full,
        score: rec?.score ?? b.full,
        basis: rec?.basis ?? '方案默认满分，尚未人工调整。',
        canAppeal: Boolean(rec?.recorded && rec.canAppeal),
        appealsUsed: rec?.appealsUsed ?? 0,
        recorded: rec?.recorded ?? false,
        updatedAt: rec?.updatedAt,
      })
    }
    for (const p of cat.penaltyItems) {
      const rec = recorded.find((r) => r.itemKey === p.key)
      out.push({
        id: rec?.recorded ? `${user.id}:${p.key}` : null,
        category: cat.key,
        categoryName: cat.name,
        itemKey: p.key,
        itemName: p.name,
        kind: 'penalty' as const,
        fullScore: p.per,
        score: rec?.score ?? 0,
        basis: rec?.basis ?? '无扣分记录。',
        canAppeal: Boolean(rec?.recorded && rec.canAppeal),
        appealsUsed: rec?.appealsUsed ?? 0,
        recorded: rec?.recorded ?? false,
        updatedAt: rec?.updatedAt,
      })
    }
  }
  return out
}

/* 奖学金档位。真站里这是 settle.Compute 算定后冻进快照的字段，演示站没有
   结算器，所以在这里按每档独立四舍五入后的名额现算。 */
function awardTierFor(rank: number, size: number): string {
  let cumulative = 0
  for (const tier of db().timeline.honorRoll.awards) {
    cumulative += Math.round((size * tier.topPercent) / 100)
    const cutoff = cumulative
    if (cutoff > 0 && rank <= cutoff) return tier.name
  }
  return ''
}

function myScore(user: DemoUser): MyScore {
  const current = currentDemoScore(user)
  if (user.role === 'ops') return { settled: false, current }
  const rank = user.id === 'u-chen' ? 4 : user.id === 'u-zhao' ? 9 : 12
  return {
    settled: true,
    current,
    stale: Boolean(db().flags.forceRejectionSettlementStale),
    runId: 'run-1',
    schemeId: 'sch-pub',
    completedAt: '2026-08-24T08:00:00.000Z',
    categoryScores: { major: 88.6, moral: 82, practice: 70, health: 68 },
    categoryRanks: { major: 5, moral: 4, practice: 6, health: 8 },
    totalScore: 82.4,
    classRank: rank,
    majorRank: 5,
    honor: [5, 4, 6, 8].every((r) => r <= Math.round(35 * db().timeline.honorRoll.topPercent / 100)),
    awardTier: awardTierFor(rank, 35),
    classSize: 35,
    awardQuota: Math.min(35, db().timeline.honorRoll.awards.reduce((sum, t) => sum + Math.round(35 * t.topPercent / 100), 0)),
    awardPosition: rank,
    honorTopPercent: db().timeline.honorRoll.topPercent,
    distribution: { min: 61, median: 78, max: 91 },
    details: {
      categories: {
        major: { itemTotal: 0, baseTotal: 0, penaltyTotal: 0, beforeCap: 88.6, total: 88.6, maxTotal: 100 },
        moral: { itemTotal: 15, baseTotal: 70, penaltyTotal: -1, beforeCap: 84, total: 82, maxTotal: 100 },
        practice: { itemTotal: 30, baseTotal: 0, penaltyTotal: 0, beforeCap: 70, total: 70, maxTotal: 100 },
        health: { itemTotal: 5, baseTotal: 70, penaltyTotal: 0, beforeCap: 75, total: 68, maxTotal: 100 },
      },
      items: [],
      baseItems: [],
      gpa: { score: 88.6, imported: true },
    },
    configSnapshot: db().scheme,
  }
}

// The demo uses its synthetic members too, so a forced rejection visibly
// changes current scores without modifying the old settlement fixture.
function currentCategoryBreakdown(member: DemoUser, cat: SchemeConfig['categories'][number]) {
  const points = (value: number) => Math.round(value * 1000)
  const submissions = db().submissions.filter((s) => s.studentId === member.sid)
  const exclusive = new Map<string, { score: number; group?: { key: string; cap: number } }>()
  const selected: { score: number; group?: { key: string; cap: number } }[] = []
  for (const sub of submissions.filter((s) => s.category === cat.key && s.finalScore != null && !s.forceRejection && ['scored', 'locked', 'appealing', 'arbitrating'].includes(s.status))) {
    const rule = sub.ruleSnapshot.item
    const cap = 'cap' in rule.scoreRule ? rule.scoreRule.cap : undefined
    const entry = { score: Math.min(points(sub.finalScore!), cap == null ? Infinity : points(cap)), group: rule.capGroup }
    if (!rule.exclusiveGroup) selected.push(entry)
    else if (entry.score > (exclusive.get(rule.exclusiveGroup)?.score ?? -Infinity)) exclusive.set(rule.exclusiveGroup, entry)
  }
  selected.push(...exclusive.values())
  let itemTotal = 0
  const groups = new Map<string, { score: number; cap: number }>()
  for (const entry of selected) {
    if (!entry.group) itemTotal += entry.score
    else {
      const prior = groups.get(entry.group.key)
      groups.set(entry.group.key, { score: (prior?.score ?? 0) + entry.score, cap: points(entry.group.cap) })
    }
  }
  for (const entry of groups.values()) itemTotal += Math.min(entry.score, entry.cap)

  const entries = baseItemsFor(member).filter((b) => b.category === cat.key)
  const baseTotal = entries.filter((b) => b.kind === 'base').reduce((sum, b) => sum + points(b.score), 0)
  const penaltyTotal = entries.filter((b) => b.kind === 'penalty').reduce((sum, b) => sum + points(b.score), 0)
  const beforeCap = itemTotal + baseTotal + penaltyTotal
  return { itemTotal: itemTotal / 1000, baseTotal: baseTotal / 1000, penaltyTotal: penaltyTotal / 1000, beforeCap: beforeCap / 1000, total: Math.min(beforeCap, points(cat.maxTotal)) / 1000, maxTotal: cat.maxTotal }
}

function currentDemoScore(user: DemoUser): MyScore['current'] {
  const state = db()
  const points = (value: number) => Math.round(value * 1000)
  const members = state.users.filter((u) => u.classId === user.classId && u.status === 'active' && u.role !== 'ops')
  const rows = members.map((member) => {
    const scores: Record<string, number | null> = { major: state.gpa.find((g) => g.userId === member.id)?.score ?? null }
    const submissions = state.submissions.filter((s) => s.studentId === member.sid)
    for (const cat of state.scheme.categories.filter((c) => c.key !== 'major')) {
      scores[cat.key] = currentCategoryBreakdown(member, cat).total
    }
    const total = scores.major == null ? null : Object.entries(scores).reduce((sum, [key, value]) => sum + points((value ?? 0) * (state.scheme.weights[key as CategoryKey] ?? 0)), 0) / 1000
    return { id: member.id, scores, total, pending: submissions.filter((s) => !s.forceRejection && ['pending', 'consensus', 'appealing', 'arbitrating'].includes(s.status)).length }
  })
  const mine = rows.find((row) => row.id === user.id)
  const imported = rows.filter((row) => row.scores.major != null).length
  const ready = rows.length > 0 && imported === rows.length
  return {
    schemeId: 'sch-pub', calculatedAt: nowIso(), classSize: rows.length, gpaImported: imported, rankingReady: ready,
    categoryScores: mine?.scores ?? {}, totalScore: mine?.total ?? null, pendingItems: mine?.pending ?? 0,
    categoryRanks: mine ? Object.fromEntries(Object.entries(mine.scores).filter(([key]) => key !== 'major' || ready).map(([key, value]) => [key, 1 + rows.filter((row) => points(row.scores[key] ?? 0) > points(value ?? 0)).length])) : {},
    classRank: ready && mine ? 1 + rows.filter((row) => points(row.total!) > points(mine.total!)).length : null,
    majorRank: ready && mine ? 1 + rows.filter((row) => points(row.scores.major!) > points(mine.scores.major!)).length : null,
  }
}

function myScorecard(user: DemoUser): MyScorecard {
 const scheme = db().scheme
  const items = db()
    .submissions.filter((s) => s.studentId === user.sid && s.status !== 'draft' && s.finalScore != null)
    .map((s) => ({
      id: s.id,
      title: s.title,
      filedCategory: s.category,
      filedItemKey: s.itemKey,
      category: s.category,
      itemKey: s.itemKey,
      claim: s.claim,
      requestedScore: s.wantScore,
      score: s.finalScore ?? 0,
      forceRejection: s.forceRejection,
        forcedScore: s.forcedScore,
      note: s.note ?? '',
      source: s.source,
      submittedAt: s.submittedAt,
      ruleSnapshot: s.ruleSnapshot,
      filedRuleSnapshot: s.ruleSnapshot,
      evidence: db().evidence[s.id] ?? [],
      noteEvidence: db().evidence[`notes:submissions:${s.id}`] ?? [],
      reviews: (db().reviews[s.id] ?? []).map((r) => ({ decision: r.decision, score: r.score, reason: r.reason, at: r.at })),
    }))

 const baseItems = baseItemsFor(user).map((b) => ({ id:b.id, category:b.category, categoryName:b.categoryName, itemKey:b.itemKey, itemName:b.itemName, kind:b.kind, fullScore:b.fullScore, perScore:b.kind==='penalty'?b.fullScore:null, score:b.score, basis:b.basis, recorded:b.recorded, updatedAt:b.updatedAt }))
 const categories = scheme.categories.filter((c) => c.key !== 'major').map((c) => ({ key: c.key, name: c.name, ...currentCategoryBreakdown(user, c), weight: scheme.weights[c.key] }))
 const gpaScore=db().gpa.find((g)=>g.userId===user.id)?.score??null
 const issues: NonNullable<MyScorecard['scorecard']>['issues'] = [
  ...db().submissions.filter((s)=>s.studentId===user.sid && ['pending','consensus','arbitrating','appealing'].includes(s.status)).map((s)=>({kind:'submission' as const,id:s.id,title:s.title,status:s.status})),
  ...db().appeals.filter((a)=>a.studentId===user.sid && ['filed','reviewing','escalated'].includes(a.status)).map((a)=>({kind:'appeal' as const,id:a.id,title:'成绩申诉',status:a.status})),
  ...db().objections.filter((o)=>o.studentId===user.sid && o.status==='submitted').map((o)=>({kind:'objection' as const,id:o.id,title:'计分异议',status:o.status})),
 ]
 const scorecard={categories,items,baseItems,issues,gpa:{score:gpaScore,imported:gpaScore!==null},estimatedTotal:currentDemoScore(user)?.totalScore ?? null,estimateNote:'当前已认定成绩；待审核材料不计入，正式结果以班级结算为准。'}
 // Demo versioning is local and deterministic; the API uses SHA-256.
 const raw=JSON.stringify({scorecard,version:scheme.version})
 let hash=2166136261
 for(let i=0;i<raw.length;i++)hash=Math.imul(hash^raw.charCodeAt(i),16777619)
 const revision=(hash>>>0).toString(16).padStart(8,'0').repeat(8)
 const previous=db().confirmed[user.id]
 const confirmed=previous?.revision===revision
 return {state:confirmed?'confirmed':'confirmable',revision,updatedAt:nowIso(),confirmation:{confirmed,confirmedAt:confirmed?previous!.confirmedAt:null,recheck:!!previous&&!confirmed},scorecard}
}

function submissionTrail(sub: Submission): TrailEntry[] {
  /* 动作名与正式站 audit_log 一致，翻译在学生页 TRAIL_LABEL。不要写 created 这种
     没有对照的键，否则轨迹上会直接露出英文。 */
  const step = (action: string, at: string): TrailEntry => ({ action, after: null, metadata: {}, at })
  const steps: TrailEntry[] = []
  if (sub.submittedAt) steps.push(step('submission.submitted', sub.submittedAt))
  for (const file of db().evidence[sub.id] ?? []) {
    if (file.uploadedAt) steps.push(step('evidence.completed', file.uploadedAt))
  }
  const reviews = [...(db().reviews[sub.id] ?? [])].sort((a, b) => a.at.localeCompare(b.at))
  for (const row of reviews) steps.push(step('review.decided', row.at))
  const appeals = db().appeals
    .filter((row) => row.targetType === 'submission' && row.targetId === sub.id && row.reason.trim().length > 0 && !(row.status === 'filed' && row.handlers.length === 0))
    .sort((a, b) => a.round - b.round || a.createdAt.localeCompare(b.createdAt))
  for (const appeal of appeals) {
    steps.push(step('appeal.filed', appeal.createdAt))
    for (const handler of appeal.handlers) {
      if (handler.rereview) steps.push(step('appeal.rereviewed', handler.rereview.at))
    }
    if (appeal.status === 'escalated') steps.push(step('appeal.escalated', appeal.updatedAt))
    if (appeal.resolvedAt) steps.push(step(appeal.status === 'final' ? 'appeal.final' : 'appeal.resolved', appeal.resolvedAt))
  }
  const order = ['submission.submitted', 'evidence.completed', 'review.decided', 'appeal.filed', 'appeal.rereviewed', 'appeal.resolved', 'appeal.escalated', 'appeal.final']
  return steps.sort((a, b) => a.at.localeCompare(b.at) || order.indexOf(a.action) - order.indexOf(b.action))
}

function submissionDetail(id: string): SubmissionDetail {
  const sub = db().submissions.find((s) => s.id === id)
  if (!sub) fail(404, 'not_found', '提交不存在')
  const named = db().reviews[id] ?? []
  /* 这个接口的契约是 StudentReview[]（只有理由，没有人名）。原来给非学生角色
     直接回 NamedReview[]，字段名都不一样（reason / why），前端按 why 取就是空。
     人名要看谁审的，走仲裁台那一份带 reviewer 的接口。 */
  const reviews = named.map((r) => ({ decision: r.decision, score: r.score, why: r.reason, spentSeconds: r.spentSeconds, at: r.at }))
  return {
    submission: sub,
    reviews,
    evidence: db().evidence[id] ?? [],
    noteEvidence: db().evidence[`notes:submissions:${id}`] ?? [],
    trail: submissionTrail(sub),
  }
}

function filterSubs(items: Submission[], q: URLSearchParams) {
  const id = q.get('id')
  const category = q.get('category')
  const status = q.get('status')
  const student = q.get('student') ?? q.get('q')
  return items.filter((s) => {
    if (id && s.id !== id) return false
    if (category && s.category !== category) return false
    if (status && s.status !== status) return false
    if (q.get('force_rejected') === 'true' && !s.forceRejection) return false
    if (student && !(`${s.student}${s.studentId}${s.title}`.includes(student))) return false
    return true
  })
}

function gate(): Gate {
  const s = db()
  const students = s.users.filter((u) => u.role !== 'ops' && u.status === 'active')
  const sealedCount = students.filter((u) => s.sealed[u.id]?.sealed).length
  const pendingAppeals = s.appeals.filter((appeal) => ['filed', 'reviewing', 'escalated'].includes(appeal.status))
  const pendingConflicts = pendingAppeals.length + s.objections.filter((row) => row.status === 'submitted').length
    + s.submissions.filter((sub) => sub.status === 'arbitrating' && !pendingAppeals.some((appeal) => appeal.targetType === 'submission' && appeal.targetId === sub.id)).length
  const pendingClassifications = s.classificationSuggestions.filter((row) => row.status === 'pending').length
  const unfinalizedReviews = s.submissions.filter((sub) => sub.status !== 'draft' && (!['scored', 'locked'].includes(sub.status) || sub.finalScore === null)).length
  const gpaImportedCount = s.gpa.filter((row) => row.score !== null).length
  const conditions = [
    { key: 'sealed' as const, label: '全员封存或窗口截止', ok: sealedCount === students.length, detail: `${sealedCount} / ${students.length}` },
    { key: 'allReviewsFinal' as const, label: '单项全部定分', ok: unfinalizedReviews === 0, detail: unfinalizedReviews ? `${unfinalizedReviews} 条申报未定分` : '全部已定分' },
    { key: 'classificationResolved' as const, label: '分类建议全部处理', ok: pendingClassifications === 0, detail: pendingClassifications ? `${pendingClassifications} 条分类建议待处理` : '无待处理建议' },
    { key: 'noConflict' as const, label: '无待终裁冲突', ok: pendingConflicts === 0, detail: pendingConflicts ? `${pendingConflicts} 条事项待处理` : '当前无冲突' },
    { key: 'gpaImported' as const, label: '专业素质分已导入', ok: gpaImportedCount === students.length, detail: `${gpaImportedCount} / ${students.length}` },
  ]
  return {
    open: s.gateForced || conditions.every((c) => c.ok),
    forced: s.gateForced,
    conditions,
    studentCount: students.length,
    sealedCount,
    pendingConflicts,
    unfinalizedReviews,
    pendingClassifications,
    gpaImportedCount,
    schemeId: 'sch-pub',
    schemeVersion: s.scheme.version,
    forceReason: s.gateForced ? '演示强制打开闸门' : null,
    forcedAt: s.gateForced ? nowIso() : null,
  }
}

function knowledgeState() {
  const docs = db().knowledgeDocs
  return {
    policy: { externalProcessingApproved: true, approvedBy: '王管理', approvedAt: '2026-08-08T00:00:00.000Z', revokedAt: null },
    limits: { fileMb: 50, filesPerClass: 40, storageMbPerClass: 512, pdfPages: 80 },
    stats: {
      totalFiles: docs.length + 3,
      classFiles: docs.length,
      evidenceFiles: 3,
      readyFiles: docs.length,
      processingFiles: 0,
      storageBytes: docs.reduce((n, d) => n + d.sizeBytes, 0),
      updatedAt: nowIso(),
    },
    documents: docs.map(({ text: _t, ...d }) => d),
    evidence: [
      {
        id: 'e-cadre',
        filename: '班长任命文件.png',
        mediaType: 'image/png',
        sizeBytes: 120000,
        status: 'ready' as const,
        kind: 'claim' as const,
        source: 'submission' as const,
        ownerId: 's-cadre',
        title: '担任 演示班班长',
        subjectSid: '20240001',
        subjectName: '陈屿',
        uploaderSid: '20240001',
        uploaderName: '陈屿',
        createdAt: '2026-08-18T08:00:00.000Z',
      },
    ],
    platform: { knowledgeEnabled: true, knowledgeEgressEnabled: false, aiEnabled: false },
  }
}

function agentStatus() {
  return {
    enabled: true,
    reason: '' as const,
    actionsEnabled: true,
    externalProcessingApproved: true,
    quota: { used: 2, limit: 40 },
    attachmentQuota: { maxFileMb: 8, maxMessageMb: 16, dailyMb: 40, dailyUsedBytes: 0, maxCount: 4 },
    model: { configured: true, visionConfigured: true, inherited: false },
  }
}

function advanceAgent(id: string) {
  const cv = db().conversations.find((c) => c.id === id)
  if (!cv) return
  for (const m of cv.messages) {
    if (m.role !== 'assistant') continue
    if (m.status === 'queued') {
      m.status = 'running'
      m.toolTrace = [{ seq: 1, tool: 'current_scheme', status: 'running', thought: '正在对照当前方案。', summary: '', durationMs: 0 }]
      return
    }
    if (m.status === 'running') {
      m.status = 'complete'
      m.finishedAt = nowIso()
      m.toolTrace = [{ seq: 1, tool: 'current_scheme', status: 'complete', thought: '已读到当前方案。', summary: '读到四大项与学生干部任职档', durationMs: 380 }]
      m.finalThought = '按方案原文说明，不替学生提交。'
      m.content =
        m.content ||
        '这是演示回复：请在「提交材料」选择对应小项，按档或按次填写，并上传佐证。条件基础分未申报不计分。'
    }
  }
}


function aiLimits() {
  return {
    items: 12,
    dailyBatches: 3,
    activeBatches: 1,
    fileMb: 20,
    batchMb: 40,
    pdfPages: 30,
    allowedFormats: ['jpeg', 'png', 'webp', 'pdf'] as const,
  }
}

function opsHealth() {
  return {
    status: 'ok' as const,
    components: [
      { name: 'postgres', status: 'ok', detail: '演示数据，无真实数据库' },
      { name: 'redis', status: 'ok' },
      { name: 'garage', status: 'ok' },
      { name: 'api', status: 'ok' },
    ],
  }
}

function opsDeploy() {
  return {
    appEnv: 'mock',
    imageTag: 'mock-local',
    gitSha: 'mock000000000000000000000000000000000000',
    migrationVersion: 64,
    databaseRole: { name: 'easygpa', superuser: false, bypassRls: false },
    opsDatabaseRole: { name: 'easygpa_ops', superuser: false, bypassRls: true },
    aiEnabled: false,
    configuration: {
      publicUrl: 'https://gpa.example.org', trustedProxies: [],
      auth: { cookieSecure: true, accessTokenTtlSeconds: 900, refreshTokenTtlSeconds: 2592000 },
      mcp: { enabled: true, maxTtlHours: 24 },
      storage: { endpoint: 'garage:3900', publicEndpoint: 'files.example.org', bucket: 'easygpa', region: 'garage', useSsl: false, publicUseSsl: true },
      backup: { directory: '/backups/local', offsiteDirectory: '/backups/offsite', remoteRecipientReady: true },
      secrets: { mailReady: true, aiReady: true, backupRemoteReady: true },
    },
  }
}

function agentProviders(): AIProvider[] {
  return [{
    id: 'prov-1', name: '演示通道', baseUrl: 'https://api.example.invalid/v1', authType: 'bearer',
    timeoutSeconds: 30, maxRetries: 2, capabilities: { json: true, stream: true, vision: true, models: true },
    enabled: true, revision: 1, createdAt: '2026-07-01T00:00:00.000Z', updatedAt: nowIso(), apiKeySet: true,
  }]
}

function agentRoutes(): ModelRouteState {
  return {
    items: [{ purpose: 'agent.text', providerId: 'prov-1', providerName: '演示通道', model: 'demo-agent', parameters: {}, revision: 1, updatedAt: nowIso() }],
    purposes: ['material.vision', 'material.compose', 'knowledge.ocr', 'agent.text', 'agent.vision'],
  }
}

function agentConfig() {
  const providers = agentProviders()
  const routes = agentRoutes()
  const config = { baseUrl: 'https://api.example.invalid/v1', textModel: 'demo-text', visionModel: 'demo-vision', agentModel: 'demo-agent' }
  const routeStatus = Object.fromEntries(routes.purposes.map((purpose) => {
    const route = routes.items.find((item) => item.purpose === purpose)
    const provider = providers.find((item) => item.id === route?.providerId)
    // The real API resolves unbound purposes through the legacy connection.
    const fallbackModel = purpose === 'material.compose' ? config.textModel : purpose === 'agent.text' ? config.agentModel : config.visionModel
    return [purpose, route && provider
      ? { ready: provider.enabled && provider.apiKeySet, providerId: provider.id, provider: provider.name, model: route.model, legacy: false }
      : { ready: true, providerId: '', provider: 'Legacy default', model: fallbackModel, legacy: true }]
  }))
  return {
    config,
    sources: { baseUrl: 'database' as const, apiKey: 'database' as const, textModel: 'database' as const, visionModel: 'database' as const, agentModel: 'database' as const },
    enabled: true,
    ready: true,
    legacyReady: true,
    agentReady: true,
    providerCount: providers.length,
    routeCount: routes.items.length,
    routeStatus,
    statusReason: '',
    knowledgeEnabled: true,
    agentActionsEnabled: true,
    knowledgeEgressEnabled: false,
    knowledgeReady: false,
    knowledgeStatusReason: '尚未允许知识内容发送到模型端点',
    limits: {
      agentMaxSteps: 8,
      agentTimeoutSeconds: 60,
      agentMaxAnswerKb: 32,
      agentToolResultKb: 24,
      agentToolScanMb: 8,
      agentDailyMessages: 40,
      materialDailyBatches: 3,
      materialActiveBatches: 1,
      knowledgeMaxFilesPerClass: 40,
      knowledgeMaxStorageMbPerClass: 512,
      materialMaxItems: 12,
      materialMaxFileMb: 20,
      materialMaxBatchMb: 40,
      materialMaxPdfPages: 30,
      materialConcurrency: 2,
      materialRetentionDays: 14,
      materialAllowedFormats: ['jpeg', 'png', 'webp', 'pdf'],
      agentAttachmentMaxFileMb: 8,
      agentAttachmentMaxMessageMb: 16,
      agentAttachmentDailyMb: 40,
      agentAttachmentMaxCount: 4,
    },
    usage: { dailyMessages: 2, dailyAttachments: 0, dailyAttachmentBytes: 0 },
    apiKeySet: true,
    storedApiKeySet: true,
    secretKeyReady: true,
  }
}

function platformKnowledge() {
  return {
    limits: { fileMb: 20, files: 20, storageMb: 256, pdfPages: 40 },
    stats: { totalFiles: 1, readyFiles: 1, processingFiles: 0, storageBytes: 80000 },
    documents: [
      {
        id: 'pk-1',
        sourceKind: 'builtin' as const,
        builtinKey: 'product-help',
        filename: '产品说明.md',
        displayName: '综测平台使用说明',
        allowedRoles: ['student', 'group', 'class_admin'] as const,
        views: ['stuHome', 'stuSubmit'],
        keywords: ['提交', '审核'],
        sortOrder: 1,
        status: 'ready' as const,
        enabled: true,
        searchable: true,
        extractor: 'markdown',
        entries: 1,
        pageCount: 1,
        sheetCount: null,
        warning: '',
        error: '',
        contentVersion: '1',
        contentHash: 'abc',
        mediaType: 'text/markdown',
        sizeBytes: 80000,
        createdAt: '2026-07-01T00:00:00.000Z',
        updatedAt: '2026-08-01T00:00:00.000Z',
        publishedAt: '2026-08-01T00:00:00.000Z',
      },
    ],
  }
}

function classInfo(): ClassInfo {
  const s = db()
  return {
    id: String(CLASS_ID),
    name: CLASS_NAME,
    archived: false,
    createdAt: '2026-07-01T00:00:00.000Z',
    schemeVersion: s.scheme.version,
    window: s.timeline.window,
    capabilities: s.timeline.capabilities,
  }
}

function scorecardFor(uid: string): StudentScorecard {
  const u = userById(uid)
  if (!u) fail(404, 'not_found', '成员不存在')
  const student: ReviewStudent = {
    userId: u.id,
    sid: u.sid,
    name: u.name,
    self: false,
    sealed: Boolean(db().sealed[u.id]?.sealed),
    scoredCount: db().submissions.filter((s) => s.studentId === u.sid && s.finalScore != null).length,
    openObjections: db().objections.filter((o) => o.studentUserId === u.id && (o.status === 'draft' || o.status === 'submitted')).length,
  }
  return {
    student,
    baseItems: baseItemsFor(u).map((b) => ({
      id: b.id,
      category: b.category,
      categoryName: b.categoryName,
      itemKey: b.itemKey,
      itemName: b.itemName,
      kind: b.kind,
      fullScore: b.kind === 'base' ? b.fullScore : null,
      perScore: b.kind === 'penalty' ? b.fullScore : null,
      score: b.score,
      basis: b.basis,
      recorded: b.recorded,
      locked: false,
      updatedAt: b.updatedAt ?? null,
    })),
    submissions: db()
      .submissions.filter((s) => s.studentId === u.sid)
      .map((s) => ({
        id: s.id,
        category: s.category,
        categoryName: s.categoryName,
        itemKey: s.itemKey,
        itemName: s.itemName,
        title: s.title,
        finalScore: s.finalScore,
        source: s.source,
        status: s.status,
        ruleSnapshot: s.ruleSnapshot,
        forceRejection: s.forceRejection,
        forcedScore: s.forcedScore,
        reviewedByMe: false,
        locked: s.status === 'appealing' || s.status === 'arbitrating',
        scoredAt: s.status === 'scored' ? s.updatedAt : null,
        evidence: db().evidence[s.id] ?? [],
      })),
  }
}

/* 轨迹＝这条申诉走过的每一步，按发生顺序摆开：谁在什么时候做了什么。
   它的用处是"改判不抹旧结论"——复评人和班管都能看出这一分是怎么一步步变成现在这样的。
   原来 mock 返回空数组，页面上只剩一个标题，看不出它是干什么的。 */
function appealTrail(a: Appeal): TrailEntry[] {
  /* 动作名照抄后端写进 audit_log 的那一套，翻译交给前端 lib/trail.ts。
     故意混进 appeal.read：轨迹里的只读动作要被前端滤掉，这里得有东西给它滤。 */
  /* after / metadata 前端的 lib/trail.ts 会读，缺了它按 undefined 走，
     轨迹上那一行就只剩动作名。演示站补成空对象，形状和真实接口对齐。 */
  const step = (action: string, at: string): TrailEntry => ({ action, at, after: null, metadata: {} })
  const steps = [step('appeal.draft_created', a.createdAt), step('appeal.filed', a.createdAt)]
  if ((db().evidence[a.id] ?? []).length) steps.push(step('appeal.evidence_completed', a.createdAt))
  if (a.handlers.length) steps.push(step('appeal.assigned', a.createdAt))
  steps.push(step('appeal.read', a.updatedAt))
  for (const h of a.handlers) {
    if (h.rereview) steps.push(step('appeal.rereviewed', h.rereview.at))
  }
  if (a.status === 'escalated') steps.push(step(a.round === 2 ? 'appeal.escalated_round2' : 'appeal.escalated', a.updatedAt))
  if (a.resolvedAt) steps.push(step(a.status === 'final' ? 'appeal.final' : 'appeal.resolved', a.resolvedAt))
  return steps
}

function appealDetail(id: string, viewer: DemoUser): AppealDetail {
  const a = db().appeals.find((x) => x.id === id)
  if (!a) fail(404, 'not_found', '申诉不存在')
  const subject = userBySid(a.studentId)
  if (!subject || subject.classId !== viewer.classId) fail(404, 'not_found', '申诉不存在')
  const owner = viewer.sid === a.studentId
  const submitted = a.reason.trim() && !(a.status === 'filed' && a.handlers.length === 0)
  if (!owner && !submitted) fail(404, 'not_found', '申诉尚未提交')
  const assigned = viewer.role === 'group' && a.handlers.some((handler) => handler.id === viewer.id)
  const deputyTarget = viewer.role === 'group' && viewer.isDeputy && subject.role === 'class_admin'
  if (!owner && viewer.role !== 'class_admin' && !assigned && !deputyTarget) fail(403, 'forbidden', '无权读取这条申诉')
  /* 复评人人名只对班级管理员开放。终裁人（班管）不是单盲对象，final 时对学生下发 handler。 */
  const adjudicator = viewer.role === 'class_admin' || (viewer.role === 'group' && viewer.isDeputy && userBySid(a.studentId)?.role === 'class_admin')
  const named = (h: Appeal['handlers'][number]) => adjudicator || h.id === viewer.id
  const bothDecided = a.handlers.every((handler) => handler.decided)
  const handlers = a.handlers.map((h) => ({ ...h, mine: h.id === viewer.id, name: named(h) ? h.name : undefined, sid: named(h) ? h.sid : undefined, rereview: viewer.role === 'class_admin' || bothDecided || h.id === viewer.id ? h.rereview : null }))
  const original = db().submissions.find((s) => s.id === a.targetId)
  /* 基础项与扣分项的申诉没有 submission，录入依据和满分要回 bases 里取。 */
  const base = db().bases.find((row) => row.itemKey === a.itemKey && row.category === a.category && userById(row.userId)?.sid === a.studentId)
  /* 二次申诉要回放第一轮：第一轮在 mock 里是一条真实存在的申诉，直接读它。 */
  const previous = a.previousAppealId ? db().appeals.find((x) => x.id === a.previousAppealId) : undefined
  return {
    ...a,
    handler: a.status === 'final' ? a.handler ?? '王管理' : undefined,
    ...adjudicationPermission(viewer, a.studentId),
    handlers,
    basis: base?.basis,
    fullScore: base?.fullScore,
    previousRound: previous
      ? {
          id: previous.id,
          reason: previous.reason,
          resolutionScore: previous.resolutionScore,
          resolutionReason: previous.resolutionReason,
          resolvedAt: previous.resolvedAt,
          handlers: previous.handlers.map((h) => ({ ...h, mine: h.id === viewer.id, name: named(h) ? h.name : undefined, sid: named(h) ? h.sid : undefined })),
        }
      : undefined,
    evidence: db().evidence[a.id] ?? [],
    noteEvidence: db().evidence[`notes:appeals:${a.id}`] ?? [],
    trail: appealTrail(a),
    originalEvidence: original ? db().evidence[original.id] ?? [] : [],
    ruleSnapshot: original?.ruleSnapshot,
    originalReviews:
      viewer.role === 'student'
        ? (db().reviews[a.targetId] ?? []).map((r) => ({ decision: r.decision, score: r.score, why: r.reason, spentSeconds: r.spentSeconds, at: r.at }))
        : db().reviews[a.targetId] ?? [],
  }
}

function createSubmission(user: DemoUser, body: Body, status: Submission['status'] = 'draft'): Submission {
  const category = str(body.category) as CategoryKey
  const itemKey = str(body.itemKey)
  const found = findItem(db().scheme, category, itemKey)
  if (!found) fail(400, 'invalid_item', '找不到该加分项')
  const claim = (body.claim ?? {}) as { quantity?: number; option?: string; score?: number }
  const snap = snapshot(db().scheme, category, itemKey)
  snap.capturedAt = nowIso()
  const row: Submission = {
    id: nid('s'),
    student: user.name,
    studentId: user.sid,
    category,
    categoryName: found.category.name,
    itemKey,
    itemName: found.item.name,
    title: str(body.title) || found.item.name,
    claim,
    wantScore: wantScore(found.item, claim),
    finalScore: null,
    status,
    submittedAt: status === 'draft' ? null : nowIso(),
    ruleSnapshot: snap,
    evidenceCount: 0,
    note: str(body.note) || undefined,
    source: 'manual',
    appealsUsed: 0,
    canAppeal: false,
    updatedAt: nowIso(),
  }
  db().submissions.unshift(row)
  return row
}

export async function handle(method: string, pathWithQuery: string, body: unknown, token: string | null = null, authorization?: symbol): Promise<unknown> {
  const qIndex = pathWithQuery.indexOf('?')
  const requestedPath = qIndex >= 0 ? pathWithQuery.slice(0, qIndex) : pathWithQuery
  const query = new URLSearchParams(qIndex >= 0 ? pathWithQuery.slice(qIndex + 1) : '')
  let user = userFromToken(token)
  const b = json(body)
  const m = method.toUpperCase()
  const deputy = requestedPath.startsWith('/review/deputy/')
  let path = deputy ? deputyPath(m, requestedPath, user) : requestedPath
  const adjudicatorMetadata = deputy ? { adjudicatorRole: 'deputy' } : {}

  const trusted = authorization === MOCK_GOVERNANCE_EXECUTION
  const helper = requestedPath.startsWith('/governance/') && requestedPath !== '/governance/proposals'
    ? (governanceOpenHelper(m, requestedPath) ?? governanceHelper(m, requestedPath, requireUser(user)))
    : null
  if (helper) {path = helper; user = {...requireUser(user), role: 'class_admin'}}
  if (trusted) user = {...requireUser(user), role: 'class_admin'}
  const run = async () => {
    if (governanceMode() === 'collective' && path === '/me/demo-cast') fail(403, 'collective_workspace', '共治成员使用同一个工作台，不切换身份')
    if (!helper && path.startsWith('/governance')) return mockGovernance(m, path, b, requireUser(user), async (action, payload, target, author) => {
      if (action === 'submission') {
        const submission = db().submissions.find(s => s.id === target)
        if (!submission) fail(404, 'missing', '材料不存在')
        submission.finalScore = Number(payload.score); submission.status = 'scored'; submission.updatedAt = nowIso()
        return {score: submission.finalScore}
      }
      const routes: Record<string, string> = {bonus: '/admin/bonus-grants', timeline: '/admin/timeline', gpa: '/admin/gpa/paste', export: '/admin/export'}
      const route = routes[action]
      if (!route) fail(422, 'action_unknown', '该演示动作暂不可执行')
      const old = new Set(db().submissions.map(s => s.id))
      const result = await handle(action === 'timeline' ? 'PUT' : 'POST', route, payload, tokenFor(userById(author)!), MOCK_GOVERNANCE_EXECUTION)
      if (action === 'bonus') db().submissions.filter(s => !old.has(s.id)).forEach(s => {s.source = 'collective_grant'})
      return result
    })
    if (!trusted && !helper && governanceMode() === 'collective' && (path.startsWith('/admin/') || path.startsWith('/review/'))) fail(403, 'collective_required', '本班采用共同决策，请进入班级共治')
    if (path.startsWith('/me/agent-connections') || path.startsWith('/me/agent-operations')) return mockMCPSettings(m, path, b, requireUser(user))
    if (!deputy && path.startsWith('/admin/')) requireRole(user, ['class_admin'])
    if (path.startsWith('/ops/')) requireRole(user, ['ops'])
    if (path.startsWith('/review/')) requireRole(user, ['group', 'class_admin'])
    if (m === 'GET' && path === '/branding') return db().branding ?? { svg: demoCrest, revision: 'demo-crest' }
    if (m === 'PUT' && path === '/ops/branding') {
      const svg = str(b.svg).trim()
      if (new TextEncoder().encode(svg).length > 128 * 1024) fail(400, 'invalid_svg', 'SVG 不能超过 128 KB')
      if (svg) {
        const doc = new DOMParser().parseFromString(svg, 'image/svg+xml')
        const tags = new Set('svg g path rect circle ellipse line polyline polygon defs linearGradient radialGradient stop clipPath title desc image'.split(' '))
        const attrs = new Set('viewBox width height x y x1 y1 x2 y2 cx cy r rx ry d points fill fill-rule fill-opacity stroke stroke-width stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset stroke-opacity opacity transform id offset stop-color stop-opacity gradientUnits gradientTransform spreadMethod clip-path clip-rule preserveAspectRatio version xmlns'.split(' '))
        if (doc.doctype || doc.querySelector('parsererror') || doc.documentElement.localName !== 'svg') fail(400, 'invalid_svg', 'SVG 格式不正确')
        for (const element of doc.querySelectorAll('*')) {
          if (!tags.has(element.localName)) fail(400, 'invalid_svg', '请上传仅包含矢量形状的 SVG')
          for (const attribute of element.attributes) {
            if (attribute.name === 'xmlns' || attribute.prefix === 'xmlns') continue
            if (element.localName === 'image' && attribute.localName === 'href' && (!attribute.namespaceURI || attribute.namespaceURI === 'http://www.w3.org/1999/xlink')) {
              if (!/^data:image\/(png|jpeg);base64,[A-Za-z0-9+/=]+$/.test(attribute.value)) fail(400, 'invalid_svg', '图片仅支持内嵌 PNG/JPEG')
              const img = new Image()
              img.src = attribute.value
              try { await img.decode() } catch { fail(400, 'invalid_svg', '内嵌图片格式不正确') }
              if (!img.naturalWidth || !img.naturalHeight || img.naturalWidth > 4096 || img.naturalHeight > 4096 || img.naturalWidth * img.naturalHeight > 4_000_000) fail(400, 'invalid_svg', '内嵌图片尺寸超限')
              continue
            }
            if (!attrs.has(attribute.name) || (attribute.name !== 'xmlns' && /https?:|data:|javascript:|url\((?!#)/i.test(attribute.value))) fail(400, 'invalid_svg', '请移除样式、脚本和外部资源')
          }
        }
      }
      db().branding = { svg, revision: nowIso() }; persist(); return db().branding
    }
    /* ---- 鉴权 ---- */
    if (m === 'POST' && path === '/auth/login') {
      const account = str(b.account)
      const password = str(b.password)
      const u = userBySid(account)
      if (!u || !u.registered || u.password !== password) fail(401, 'invalid_credentials', '账号或密码不正确')
      if (u.status === 'disabled') fail(403, 'disabled', '账号已停用')
      return { access_token: tokenFor(u), expires_in: 3600, user: publicUser(u) }
    }
    if (m === 'POST' && path === '/auth/refresh') {
      const u = requireUser(user)
      return { access_token: tokenFor(u), expires_in: 3600, user: publicUser(u) }
    }
    if (m === 'POST' && path === '/auth/register/check') {
      const sid = str(b.sid).trim()
      const name = str(b.name).trim()
      const row = db().whitelist.find((w) => w.sid === sid && w.name === name)
      if (!row) fail(400, 'not_on_roster', '学号与姓名未命中班级名单')
      if (!row.active) fail(400, 'inactive', '该名单项未启用')
      if (row.registered) fail(400, 'already_registered', '该学号已经注册')
      const ticket = nid('tk')
      db().registerTickets[ticket] = { sid, expires: Date.now() + 10 * 60_000 }
      return { ticket, expires_in: 600, name: row.name, role: row.role }
    }
    if (m === 'POST' && path === '/auth/register/complete') {
      const ticket = db().registerTickets[str(b.ticket)]
      if (!ticket || ticket.expires < Date.now()) fail(400, 'invalid_ticket', '核对已过期，请重新开始')
      const u = userBySid(ticket.sid)
      if (!u) fail(400, 'not_on_roster', '名单项不存在')
      const password = str(b.password)
      if (password.length < 10) fail(400, 'weak_password', '密码至少 10 位')
      u.password = password
      u.registered = true
      const w = db().whitelist.find((x) => x.sid === u.sid)
      if (w) {
        w.registered = true
        w.registeredAt = nowIso()
      }
      delete db().registerTickets[str(b.ticket)]
      if (isTourUser(u)) {
        provisionTourUser(u)
        applyTourCast('student')
      }
      return { access_token: tokenFor(u), expires_in: 3600, user: publicUser(userFromToken(tokenFor(u)) ?? u) }
    }
    if (m === 'POST' && path === '/auth/forgot-password') {
      const u = userBySid(str(b.account))
      if (u) db().resetTokens['demo-reset'] = { sid: u.sid, expires: Date.now() + 30 * 60_000 }
      return undefined
    }
    if (m === 'POST' && path === '/auth/reset-password') {
      const tok = db().resetTokens[str(b.token)]
      if (!tok) fail(400, 'invalid_token', '重置链接无效或已过期')
      const u = userBySid(tok.sid)
      if (!u) fail(400, 'invalid_token', '重置链接无效或已过期')
      u.password = str(b.password)
      return undefined
    }
    if (m === 'POST' && path === '/auth/logout') {
      if (isTourUser(user)) resetStore()
      return undefined
    }
    if (m === 'POST' && path === '/auth/demo-tour') {
      const u = userBySid(TOUR_SID)
      if (!u) fail(404, 'not_found', '录像号不存在')
      if (!u.password) u.password = DEMO_PASSWORD
      provisionTourUser(u)
      applyTourCast('student')
      const token = tokenFor(u)
      return { access_token: token, expires_in: 3600, user: publicUser(userFromToken(token) ?? u) }
    }

    /* ---- 当前用户 ---- */
    if (m === 'GET' && path === '/me') return publicUser(requireUser(user))
    if (m === 'GET' && path === '/me/mail-preferences') {
      const u = requireUser(user)
      return { preferences: db().mailPreferences?.[u.id] ?? mailPreset('balanced'), notificationsPaused: true, deliveryState: (db().emails[u.id] ?? []).some((item) => item.primary && item.verified) ? 'ready' : 'no_primary_email' }
    }
    if (m === 'PUT' && path === '/me/mail-preferences') {
      const u = requireUser(user)
      const p = b as unknown as MailPreferences
      if (typeof p.enabled !== 'boolean' || !Number.isInteger(p.dailyLimit) || p.dailyLimit < 1 || p.dailyLimit > 50 || !MAIL_CATEGORIES.every(({ key }) => ['off', 'digest', 'immediate', 'frequent'].includes(p.categories?.[key])) || ![p.digestTime, p.quietStart, p.quietEnd].every((value) => /^(?:[01]\d|2[0-3]):[0-5]\d$/.test(value))) fail(422, 'mail_preferences_invalid', '邮件偏好参数不正确')
      db().mailPreferences ??= {}
      db().mailPreferences![u.id] = structuredClone(p)
      persist()
      return undefined
    }
    if (m === 'GET' && path === '/me/emails') return { items: db().emails[requireUser(user).id] ?? [] }
    if (m === 'POST' && path === '/me/emails') {
      const u = requireUser(user)
      const id = nid('ch')
      db().emailChallenges[id] = { userId: u.id, email: str(b.email), expires: Date.now() + 10 * 60_000 }
      return { challengeId: id, expiresIn: 600 }
    }
    if (m === 'POST' && path === '/me/emails/verify') {
      const u = requireUser(user)
      const ch = db().emailChallenges[str(b.challengeId)]
      if (!ch || ch.userId !== u.id) fail(400, 'invalid_challenge', '验证码无效')
      if (str(b.code) !== DEMO_CODE) fail(400, 'invalid_code', '验证码不正确')
      const row = { id: nid('em'), email: ch.email, primary: (db().emails[u.id] ?? []).length === 0, verified: true, verifiedAt: nowIso(), createdAt: nowIso() }
      db().emails[u.id] = [...(db().emails[u.id] ?? []), row]
      if (row.primary) u.primaryEmail = row.email
      return row
    }
    {
      const p = match('/me/emails/:id/primary', path)
      if (m === 'PUT' && p) {
        const u = requireUser(user)
        for (const e of db().emails[u.id] ?? []) e.primary = e.id === p.id
        const hit = (db().emails[u.id] ?? []).find((e) => e.id === p.id)
        if (hit) u.primaryEmail = hit.email
        return undefined
      }
    }
    {
      const p = match('/me/emails/:id', path)
      if (m === 'DELETE' && p) {
        const u = requireUser(user)
        db().emails[u.id] = (db().emails[u.id] ?? []).filter((e) => e.id !== p.id)
        return undefined
      }
    }
    if (m === 'PUT' && path === '/me/password') {
      const u = requireUser(user)
      if (str(b.currentPassword) !== u.password) fail(400, 'wrong_password', '当前密码不正确')
      if (str(b.newPassword).length < 10) fail(400, 'weak_password', '密码至少 10 位')
      u.password = str(b.newPassword)
      return undefined
    }
    if (m === 'POST' && path === '/me/view-as') return undefined
    if (m === 'POST' && path === '/me/demo-cast') {
      const u = requireUser(user)
      if (!isTourUser(u)) fail(403, 'forbidden', '只有录像号可以切换演示身份')
      const identity = str(b.identity) as TourCast
      if (!TOUR_CASTS.some((item) => item.id === identity)) fail(400, 'invalid_identity', '录像号没有这种身份')
      applyTourCast(identity)
      return publicUser(userFromToken(token) ?? u)
    }
    if (m === 'GET' && path === '/me/base-items') return { items: baseItemsFor(requireUser(user)) }
    if (m === 'GET' && path === '/me/score') return myScore(requireUser(user))
    if (/\/(blind-audits|final-scorecards|dispatch\/finals|review-progress\/issues|result-confirmation-schedule)(\/|$)/.test(path)) fail(404, 'not_found', '此功能已移除')
    if (m === 'GET' && path === '/me/scorecard') return myScorecard(requireUser(user))
    if (m === 'GET' && path === '/me/seal') {
      const u = requireUser(user)
      const rows = db().submissions.filter((s) => s.studentId === u.sid)
      const seal = db().sealed[u.id]
      return {
        sealed: Boolean(seal?.sealed),
        source: seal?.source ?? null,
        sealedAt: seal?.sealedAt ?? null,
        draftCount: rows.filter((s) => s.status === 'draft').length,
        submittedCount: rows.filter((s) => s.status !== 'draft').length,
        confirmationPhrase: '确认封存',
        windowClose: WINDOW_CLOSE,
      }
    }
    if (m === 'POST' && path === '/me/seal') {
      const u = requireUser(user)
      if (str(b.phrase) !== '确认封存' || str(b.sid) !== u.sid) fail(400, 'confirm_mismatch', '确认短语或学号不一致')
      const drafts = db().submissions.filter((s) => s.studentId === u.sid && s.status === 'draft').length
      db().sealed[u.id] = { sealed: true, sealedAt: nowIso(), source: 'manual' }
      delete db().confirmed[u.id]
      return { id: u.id, sealed: true, draftsExcluded: drafts }
    }
    if (m === 'POST' && path === '/me/scorecard/confirm') {
      const u = requireUser(user)
      const card=myScorecard(u)
      if(str(b.revision)!==card.revision) fail(409,'scorecard_changed','成绩或处理进度已更新，请刷新核对后再确认')
      const at=db().confirmed[u.id]?.revision===card.revision ? db().confirmed[u.id]!.confirmedAt! : nowIso()
      db().confirmed[u.id]={confirmed:true,confirmedAt:at,revision:card.revision}
      return {confirmed:true,confirmedAt:at,revision:card.revision}
    }

    /* ---- 方案 / 窗口 / 资料 ---- */
    if (m === 'GET' && path === '/scheme/current') return db().scheme
    if (m === 'GET' && path === '/window') return windowState()
    if (m === 'GET' && path === '/class-resources') return { items: db().resources }
    {
      const p = match('/class-resources/:id/url', path)
      if (m === 'GET' && p) {
        const doc = db().resources.find((r) => r.id === p.id) ?? db().knowledgeDocs.find((r) => r.id === p.id)
        if (!doc) fail(404, 'not_found', '文件不存在')
        return { url: '/favicon.svg', filename: doc.filename, expiresIn: 600 }
      }
    }
    if (m === 'GET' && path === '/ai/status') return { enabled: false, configured: false, limits: aiLimits() }

    /* ---- 提交 ---- */
    if (m === 'GET' && path === '/submissions') {
      const u = requireUser(user)
      const scoped = u.role === 'student' ? db().submissions.filter((s) => s.studentId === u.sid) : db().submissions
      return pageOf(filterSubs(scoped, query), query)
    }
    if (m === 'POST' && path === '/submissions') {
      const row = createSubmission(requireUser(user), b)
      return { id: row.id, status: row.status, requestedScore: row.wantScore }
    }
    {
      const p = match('/submissions/:id/evidence/presign', path)
      if (m === 'POST' && p) {
        requireUser(user)
        const id = nid('e')
        db().pendingUploads[id] = { id, kind: 'submission', ownerId: p.id, filename: str(b.filename), mediaType: str(b.mediaType), sizeBytes: Number(b.sizeBytes) || 0 }
        return { evidenceId: id, uploadUrl: '/mock-upload', uploadFields: { key: id }, expiresIn: 600, method: 'POST', completeUrl: `/submissions/${p.id}/evidence/${id}/complete` }
      }
    }
    {
      const p = match('/submissions/:id/evidence/:eid/complete', path)
      if (m === 'POST' && p) {
        const pending = db().pendingUploads[p.eid]
        const ev: Evidence = {
          id: p.eid,
          name: pending?.filename || '附件',
          mediaType: pending?.mediaType || 'application/octet-stream',
          sizeBytes: pending?.sizeBytes || 0,
          sha256: null,
          status: 'ready',
          uploadedAt: nowIso(),
        }
        db().evidence[p.id] = [...(db().evidence[p.id] ?? []), ev]
        const sub = db().submissions.find((s) => s.id === p.id)
        if (sub) sub.evidenceCount = db().evidence[p.id].length
        delete db().pendingUploads[p.eid]
        return { evidenceId: p.eid, status: 'ready', filename: ev.name, mediaType: ev.mediaType }
      }
    }
    {
      const p = match('/submissions/:id/evidence/:eid', path)
      if (m === 'DELETE' && p) {
        db().evidence[p.id] = (db().evidence[p.id] ?? []).filter((e) => e.id !== p.eid)
        delete db().pendingUploads[p.eid]
        const sub = db().submissions.find((s) => s.id === p.id)
        if (sub) sub.evidenceCount = (db().evidence[p.id] ?? []).length
        return undefined
      }
    }
    {
      const p = match('/submissions/:id/withdraw', path)
      if (m === 'POST' && p) {
        const sub = db().submissions.find((s) => s.id === p.id)
        if (!sub) fail(404, 'not_found', '提交不存在')
        sub.status = 'draft'
        sub.submittedAt = null
        sub.updatedAt = nowIso()
        return { id: sub.id, status: sub.status }
      }
    }
    {
      const p = match('/submissions/:id/submit', path)
      if (m === 'POST' && p) {
        const sub = db().submissions.find((s) => s.id === p.id)
        if (!sub) fail(404, 'not_found', '提交不存在')
        sub.status = 'pending'
        sub.submittedAt = nowIso()
        sub.updatedAt = nowIso()
        db().assignments.push(
          { id: nid('asg'), submissionId: sub.id, reviewerId: 'u-li', decided: false },
          { id: nid('asg'), submissionId: sub.id, reviewerId: 'u-zhou', decided: false },
        )
        return { id: sub.id, status: sub.status }
      }
    }
    {
      const p = match('/submissions/:id', path)
      if (p && !path.includes('/evidence')) {
        if (m === 'GET') { requireUser(user); return submissionDetail(p.id) }
        if (m === 'PUT') {
          const sub = db().submissions.find((s) => s.id === p.id)
          if (!sub) fail(404, 'not_found', '提交不存在')
          const found = findItem(db().scheme, str(b.category) as CategoryKey, str(b.itemKey)) ?? findItem(db().scheme, sub.category, sub.itemKey)
          if (b.title) sub.title = str(b.title)
          if (b.note !== undefined) sub.note = str(b.note)
          if (b.claim) {
            sub.claim = b.claim as Submission['claim']
            if (found) sub.wantScore = wantScore(found.item, sub.claim)
          }
          sub.updatedAt = nowIso()
          return { id: sub.id, status: sub.status, requestedScore: sub.wantScore }
        }
        if (m === 'DELETE') {
          db().submissions = db().submissions.filter((s) => s.id !== p.id)
          return undefined
        }
      }
    }

    {
      const p = match('/evidence/:id/url', path)
      if (m === 'GET' && p) {
        return { url: '/favicon.svg', expiresIn: 600, filename: 'preview.png', mediaType: 'image/png', inline: query.get('disposition') === 'inline' }
      }
    }

    /* ---- 申诉 ---- */
    if (m === 'GET' && path === '/appeals') {
      const u = requireUser(user)
      /* 草稿不进列表。POST /appeals/draft 会先建一条空壳（挂佐证要有 id），
         正文还没写就出现在「我的申诉」里，学生会以为自己已经交了。 */
      const mine = db().appeals.filter((a) => a.reason.trim().length > 0 && !(a.status === 'filed' && a.handlers.length === 0))
      const items = mine.filter((a) => a.studentId === u.sid)
      return { items }
    }
    if (m === 'POST' && path === '/appeals/draft') {
      const u = requireUser(user)
      const targetType = (str(b.targetType) as Appeal['targetType']) || 'submission'
      const targetId = str(b.targetId)
      const targetAppeals = db().appeals.filter((a) => a.studentId === u.sid && a.targetType === targetType && a.targetId === targetId)
      const existing = targetAppeals.find((a) => a.status === 'filed' && a.handlers.length === 0)
      if (existing) return { id: existing.id }
      if (targetAppeals.some((a) => a.status === 'reviewing' || a.status === 'escalated')) {
        fail(409, 'appeal_in_progress', '这一笔申诉还在处理中，请等待结果后再操作')
      }
      const previous = targetAppeals
        .filter((a) => a.status === 'resolved' || a.status === 'final')
        .sort((a, b) => b.round - a.round || b.createdAt.localeCompare(a.createdAt))[0]
      if (previous?.status === 'final') fail(409, 'appeal_finalized', '这一笔已经由班级管理员终裁，不能再次申诉')
      if (previous && previous.round >= 2) fail(409, 'appeal_exhausted', '这一笔的两次申诉机会已经用完')
      const sub = db().submissions.find((s) => s.id === targetId && s.studentId === u.sid)
      const base = db().bases.find((row) => row.userId === u.id && (row.itemKey === targetId || `${u.id}:${row.itemKey}` === targetId))
      const validSubmission = targetType === 'submission' && sub?.canAppeal
      const validBase = targetType === 'base_score' && base?.kind === 'base' && base.recorded && base.canAppeal
      const validPenalty = targetType === 'penalty_score' && base?.kind === 'penalty' && base.recorded && base.canAppeal
      if (!validSubmission && !validBase && !validPenalty) fail(409, 'not_appealable', '这一笔当前不能发起申诉')
      const id = nid('ap')
      db().appeals.unshift({
        id,
        targetType,
        targetId,
        target: sub?.title ?? base?.itemName ?? '基础分',
        category: sub?.category ?? base?.category ?? 'moral',
        itemKey: sub?.itemKey ?? base?.itemKey ?? targetId,
        student: u.name,
        studentId: u.sid,
        reason: '',
        round: previous ? 2 : 1,
        status: 'filed',
        baselineScore: sub?.finalScore ?? base?.score ?? null,
        currentScore: sub?.finalScore ?? base?.score ?? null,
        proposedScore: null,
        resolutionScore: null,
        resolutionReason: null,
        handlers: [],
        previousAppealId: previous?.id,
        createdAt: nowIso(),
        updatedAt: nowIso(),
        resolvedAt: null,
      })
      return { id }
    }
    if (m === 'POST' && path === '/appeals') {
      const u = requireUser(user)
      const id = nid('ap')
      db().appeals.unshift({
        id,
        targetType: str(b.targetType) as Appeal['targetType'],
        targetId: str(b.targetId),
        target: str(b.targetId),
        category: (str(b.category) as CategoryKey) || 'moral',
        itemKey: str(b.itemKey),
        student: u.name,
        studentId: u.sid,
        reason: str(b.reason),
        round: 1,
        status: 'reviewing',
        baselineScore: null,
        currentScore: null,
        proposedScore: typeof b.score === 'number' ? b.score : null,
        resolutionScore: null,
        resolutionReason: null,
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: false, rereview: null },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: false, rereview: null },
        ],
        createdAt: nowIso(),
        updatedAt: nowIso(),
        resolvedAt: null,
      })
      return { id, round: 1, status: 'reviewing', handlers: 2 }
    }
    {
      const p = match('/appeals/:id/submit', path)
      if (m === 'POST' && p) {
        const a = db().appeals.find((x) => x.id === p.id)
        if (!a) fail(404, 'not_found', '申诉不存在')
        if (a.status !== 'filed' || a.handlers.length > 0) fail(409, 'appeal_not_draft', '这份申诉已经提交，不能重复提交')
        a.reason = str(b.reason) || a.reason
        if (a.reason.trim().length < 8) fail(422, 'appeal_invalid', '申诉理由至少需要 8 个字')
        /* 分类与分数是随提交一起主张的，不落下来列表里就只剩一个破折号。 */
        if (typeof b.score === 'number') a.proposedScore = b.score
        if (str(b.category)) a.proposedCategory = str(b.category) as CategoryKey
        if (str(b.itemKey)) a.proposedItemKey = str(b.itemKey)
        a.originalCategory = a.originalCategory ?? a.category
        a.originalItemKey = a.originalItemKey ?? a.itemKey
        a.updatedAt = nowIso()
        a.status = a.round === 2 ? 'escalated' : 'reviewing'
        a.handlers = a.round === 2
          ? []
          : [
              { id: 'u-li', name: '李审核', sid: '20240002', decided: false, rereview: null },
              { id: 'u-zhou', name: '周复评', sid: '20240006', decided: false, rereview: null },
            ]
        const sub = db().submissions.find((row) => row.id === a.targetId && row.studentId === a.studentId)
        if (sub) {
          sub.status = 'appealing'
          sub.canAppeal = false
          sub.appealsUsed = Math.max(sub.appealsUsed, a.round)
          sub.updatedAt = nowIso()
        }
        const owner = db().users.find((row) => row.sid === a.studentId)
        const base = db().bases.find((row) => row.userId === owner?.id && `${row.userId}:${row.itemKey}` === a.targetId)
        if (base) {
          base.canAppeal = false
          base.appealsUsed = Math.max(base.appealsUsed, a.round)
          base.updatedAt = nowIso()
        }
        return { id: a.id, round: a.round, status: a.status, handlers: a.handlers.length }
      }
    }
    {
      const p = match('/appeals/:id/evidence/presign', path)
      if (m === 'POST' && p) {
        const id = nid('e')
        db().pendingUploads[id] = { id, kind: 'appeal', ownerId: p.id, filename: str(b.filename), mediaType: str(b.mediaType), sizeBytes: Number(b.sizeBytes) || 0 }
        return { evidenceId: id, uploadUrl: '/mock-upload', uploadFields: { key: id }, expiresIn: 600, method: 'POST', completeUrl: '' }
      }
    }
    {
      const p = match('/appeals/:id/evidence/:eid/complete', path)
      if (m === 'POST' && p) {
        const pending = db().pendingUploads[p.eid]
        db().evidence[p.id] = [
          ...(db().evidence[p.id] ?? []),
          { id: p.eid, name: pending?.filename || '附件', mediaType: pending?.mediaType || 'image/png', sizeBytes: pending?.sizeBytes || 0, sha256: null, status: 'ready', uploadedAt: nowIso() },
        ]
        return { evidenceId: p.eid, status: 'ready', filename: pending?.filename || '附件', mediaType: pending?.mediaType || 'image/png' }
      }
    }
    {
      const p = match('/appeals/:id/withdraw', path)
      if (m === 'POST' && p) {
        const a = db().appeals.find((x) => x.id === p.id)
        if (!a) fail(404, 'not_found', '申诉不存在')
        if (a.studentId !== requireUser(user).sid) fail(403, 'forbidden', '只能撤回自己的申诉')
        if (a.status === 'resolved' || a.status === 'final' || a.status === 'escalated') fail(409, 'not_withdrawable', '这一笔已经有结论，不能再改')
        if (a.handlers.some((h) => h.decided)) fail(409, 'not_withdrawable', '已有复评记录，不能退回修改')
        a.handlers = []
        a.status = 'filed'
        a.updatedAt = nowIso()
        const sub = db().submissions.find((row) => row.id === a.targetId && row.studentId === a.studentId)
        if (sub) {
          sub.status = 'scored'
          sub.canAppeal = true
          sub.appealsUsed = Math.max(0, a.round - 1)
          sub.updatedAt = nowIso()
        }
        const owner = db().users.find((row) => row.sid === a.studentId)
        const base = db().bases.find((row) => row.userId === owner?.id && `${row.userId}:${row.itemKey}` === a.targetId)
        if (base) {
          base.canAppeal = true
          base.appealsUsed = Math.max(0, a.round - 1)
          base.updatedAt = nowIso()
        }
        return { id: a.id, status: a.status }
      }
    }
    {
      const p = match('/appeals/:id', path)
      if (p) {
        if (m === 'GET') return appealDetail(p.id, requireUser(user))
        if (m === 'DELETE') {
          const a = db().appeals.find((x) => x.id === p.id)
          if (!a) fail(404, 'not_found', '申诉不存在')
          if (a.studentId !== requireUser(user).sid) fail(403, 'forbidden', '只能删除自己的申诉')
          if (a.status !== 'filed' || a.handlers.length > 0) fail(409, 'appeal_not_deletable', '只有未提交的申诉草稿可以删除')
          const sub = db().submissions.find((row) => row.id === a.targetId && row.studentId === a.studentId)
          if (sub) {
            sub.status = 'scored'
            sub.canAppeal = true
            sub.appealsUsed = Math.max(0, a.round - 1)
            sub.updatedAt = nowIso()
          }
          const owner = db().users.find((row) => row.sid === a.studentId)
          const base = db().bases.find((row) => row.userId === owner?.id && `${row.userId}:${row.itemKey}` === a.targetId)
          if (base) {
            base.canAppeal = true
            base.appealsUsed = Math.max(0, a.round - 1)
            base.updatedAt = nowIso()
          }
          db().appeals = db().appeals.filter((x) => x.id !== p.id)
          delete db().evidence[p.id]
          for (const [uploadId, upload] of Object.entries(db().pendingUploads)) {
            if (upload.ownerId === p.id) delete db().pendingUploads[uploadId]
          }
          return undefined
        }
      }
    }

    /* ---- 审核 ---- */
    /* ---- 学生匿名举报 ----
       演示站里同样不下发举报人：db 里连字段都没有，这里只能返回举报本身。 */
    if (m === 'GET' && path === '/class-penalties') {
      const me = requireUser(user)
      const scheme = db().scheme
      const penaltyItems = scheme.categories.flatMap((c) =>
        c.penaltyItems.map((i) => ({ category: c.key, categoryName: c.name, itemKey: i.key, itemName: i.name, perScore: i.per })),
      )
      const items = db().users
        .filter((u) => u.role !== 'ops' && u.status === 'active')
        .map((u) => {
          /* 基础项走 baseItemsFor——和小组「扣分与异议」的计分卡是同一个来源。
             只列已经落库的那几行是不行的：基础分默认满分，没人调过就没有行，
             那样这一页在一个还没被扣过分的班里会是空的，而小组那边是满的。
             扣分项只列真的记在册的，方案里的全量清单走 penaltyItems 那一份。
             三类都不带 basis——依据原文不对学生公开，和真实接口一致。 */
          const card = baseItemsFor(u)
          const subs = db().submissions.filter((sub) => sub.studentId === u.sid && (sub.status === 'scored' || sub.status === 'locked') && sub.finalScore !== null)
          const entries = [
            ...card
              .filter((row) => row.kind === 'base' || row.recorded)
              .map((row) => ({
                kind: row.kind,
                category: row.category,
                categoryName: row.categoryName,
                itemKey: row.itemKey,
                itemName: row.itemName,
                targetId: row.id,
                score: row.score,
                updatedAt: row.updatedAt ?? null,
                evidence: [],
              })),
            ...subs.map((sub) => ({
              kind: 'submission' as const,
              category: sub.category,
              categoryName: sub.categoryName,
              itemKey: sub.itemKey,
              itemName: sub.title,
              source: sub.source,
              targetId: sub.id,
              score: sub.finalScore ?? 0,
              updatedAt: sub.updatedAt,
              /* 学生看得到清单，取不到原件——前端用 readOnly 摆出来，
                 真实后端那一侧按角色拦下载。 */
              evidence: db().evidence[sub.id] ?? [],
            })),
          ]
          return {
            userId: u.id,
            sid: u.sid,
            name: u.name,
            self: u.id === me.id,
            penaltyTotal: Math.round(card.filter((row) => row.kind === 'penalty' && row.recorded).reduce((sum, row) => sum + row.score, 0) * 1000) / 1000,
            openReports: db().reports.filter((r) => r.studentUserId === u.id && (r.status === 'reviewing' || r.status === 'escalated')).length,
            entries,
          }
        })
      return { items, penaltyItems, dailyLimit: 10 }
    }
    if (m === 'GET' && path === '/reports/mine') {
      requireUser(user)
      return { items: db().reports.filter((r) => r.mine).map(myReportJSON) }
    }
    {
      const p = match('/class-penalties/:kind/:id/history', path)
      if (m === 'GET' && p) {
        requireUser(user)
        const history = reportScoreHistory(p.kind, p.id)
        return { events: history.events.map((event) => ({ ...event, reason: event.reason.replace(/!?\[[^\n]*?\]\([^\n]*?\)|https?:\/\/\S+|evidence:\S+|<[^>]+>/g, '（附件或链接已隐藏）') })), evidence: [] }
      }
    }
    if (m === 'POST' && path === '/reports') {
      const me = requireUser(user)
      const target = db().users.find((u) => u.id === str(b.studentUserId))
      if (!target) fail(404, 'student_not_found', '班级成员不存在')
      if (target.id === me.id) fail(422, 'self_report', '不能举报自己')
      const kind = str(b.kind) as MockReport['kind']
      if (kind !== 'base' && kind !== 'penalty' && kind !== 'submission') fail(422, 'report_invalid', '举报类型不正确')
      if ([...str(b.basis).trim()].length < 10) fail(422, 'report_invalid', '举报必须写 10—5000 字说明')

      const cat = db().scheme.categories.find((c) => c.key === b.category)
      if (!cat) fail(422, 'report_invalid', '大项不在当前方案中')
      let itemName = str(b.itemKey)
      let proposed = 0
      let quantity: number | null = null
      let currentScore: number | null = null
      let targetId: string | null = null

      if (kind === 'penalty') {
        const item = cat.penaltyItems.find((i) => i.key === str(b.itemKey))
        if (!item) fail(422, 'report_invalid', '扣分项不在当前方案中')
        quantity = Number(b.quantity) || 0
        if (quantity <= 0) fail(422, 'quantity_invalid', '次数必须是正数')
        itemName = item.name
        proposed = Math.round(quantity * item.per * 1000) / 1000
      } else if (kind === 'base') {
        const item = cat.baseItems.find((i) => i.key === str(b.itemKey))
        if (!item) fail(422, 'report_invalid', '基础项不在当前方案中')
        const recorded = db().bases.find((row) => row.userId === target.id && row.itemKey === item.key && row.kind === 'base')
        itemName = item.name
        currentScore = recorded ? recorded.score : item.full
        proposed = Number(b.proposedScore)
        if (Number.isNaN(proposed) || proposed < 0 || proposed > item.full) fail(422, 'score_invalid', '基础项建议分必须在 0 与满分之间')
        if (proposed >= (currentScore ?? 0)) fail(422, 'not_worse', '举报只能主张把分改低。认为分给少了，请本人走申诉')
      } else {
        const sub = db().submissions.find((row) => row.id === str(b.targetId))
        if (!sub || sub.studentId !== target.sid) fail(404, 'target_not_found', '已定分条目不存在')
        itemName = sub!.title
        targetId = sub!.id
        currentScore = sub!.finalScore
        proposed = Number(b.proposedScore)
        if (Number.isNaN(proposed) || proposed < 0) fail(422, 'score_invalid', '建议分不正确')
        if (proposed >= (currentScore ?? 0)) fail(422, 'not_worse', '举报只能主张把分改低。认为分给少了，请本人走申诉')
      }

      const duplicate = db().reports.some(
        (r) => r.mine && r.studentUserId === target.id && r.kind === kind && r.itemKey === str(b.itemKey) && (r.status === 'reviewing' || r.status === 'escalated'),
      )
      if (duplicate) fail(409, 'report_exists', '你已经就这一条提过举报，正在处理中')
      const reviewerIds = db().users.filter((u) => (u.role === 'group' || u.role === 'class_admin') && u.id !== target.id && u.id !== me.id).slice(0, 2).map((u) => u.id)
      if (reviewerIds.length < 2) fail(409, 'no_reviewers', '班里可用的审核人不足两名')
      const row: MockReport = {
        id: nid('rp'),
        kind,
        studentUserId: target.id,
        studentId: target.sid,
        student: target.name,
        category: cat!.key,
        categoryName: cat!.name,
        itemKey: str(b.itemKey),
        itemName,
        targetId,
        currentScore,
        quantity,
        proposedScore: proposed,
        basis: str(b.basis).trim(),
        status: 'reviewing',
        finalScore: null,
        decisionReason: null,
        createdAt: nowIso(),
        decidedAt: null,
        reviewerIds,
        reviews: reviewerIds.map((id) => ({ reviewerId: id, decision: null, score: null, reason: null, spentSeconds: null, at: null })),
        evidence: [],
        mine: true,
      }
      db().reports.unshift(row)
      return { id: row.id, status: row.status, reviewers: reviewerIds.length }
    }
    {
      const p = match('/review/reports/:id/decision', path)
      if (m === 'POST' && p) {
        const me = requireUser(user)
        const row = db().reports.find((r) => r.id === p.id)
        if (!row) fail(404, 'not_found', '举报不存在')
        const seat = row.reviews.find((r) => r.reviewerId === me.id)
        if (!seat) fail(404, 'not_found', '这条举报没有派给你')
        if (seat.at) fail(409, 'already_reviewed', '你已经复核过这条举报')
        const decision = str(b.decision) as 'uphold' | 'adjust' | 'reject'
        /* 维持现状的那个分：扣分项是"再扣 0"，另外两类是"保持当前分"。
           不区分的话，一条把基础分改成 0 的举报和一条被驳回的举报会写出同一个结果。 */
        const quo = row.kind === 'penalty' ? 0 : (row.currentScore ?? 0)
        seat.decision = decision
        seat.score = decision === 'uphold' ? row.proposedScore : decision === 'reject' ? quo : Number(b.score) || 0
        seat.reason = str(b.reason)
        seat.spentSeconds = Number(b.spentSeconds) || 0
        seat.at = nowIso()
        const decided = row.reviews.filter((r) => r.at)
        if (decided.length < row.reviews.length) {
          return { reportStatus: 'reviewing', bothDecided: false, conflict: false, finalScore: null }
        }
        const scores = decided.map((r) => r.score ?? 0)
        const agreed = scores.every((value) => value === scores[0])
        if (!agreed) {
          row.status = 'escalated'
          return { reportStatus: 'escalated', bothDecided: true, conflict: true, finalScore: null }
        }
        row.decidedAt = nowIso()
        if (scores[0] === quo) {
          row.status = 'dismissed'
          row.finalScore = quo
          row.decisionReason = '两名复核人一致认为举报不成立'
          return { reportStatus: 'dismissed', bothDecided: true, conflict: false, finalScore: quo }
        }
        row.status = 'applied'
        row.finalScore = scores[0]
        row.decisionReason = '两名复核人结论一致'
        applyMockOutcome(row, scores[0])
        return { reportStatus: 'applied', bothDecided: true, conflict: false, finalScore: scores[0] }
      }
    }
    {
      const p = match('/review/reports/:id/target', path)
      if (m === 'GET' && p) return reportTargetDetail(p.id, requireUser(user))
    }
    {
      const p = match('/review/score-history/:kind/:id', path)
      if (m === 'GET' && p) return reportScoreHistory(p.kind, p.id)
    }
    {
      const p = match('/review/reports/:id', path)
      if (m === 'GET' && p) {
        const me = requireUser(user)
        const row = db().reports.find((r) => r.id === p.id)
        if (!row) fail(404, 'not_found', '举报不存在')
        const seat = row.reviews.find((r) => r.reviewerId === me.id)
        const recorded = db().bases.find((base) => base.userId === row.studentUserId && base.itemKey === row.itemKey && base.kind === 'penalty')
        return {
          id: row.id,
          kind: row.kind,
          evidence: row.evidence,
          noteEvidence: db().evidence[`notes:report-reviews:${row.id}`] ?? [],
          student: row.student,
          studentId: row.studentId,
          category: row.category,
          categoryName: row.categoryName,
          itemKey: row.itemKey,
          itemName: row.itemName,
          quantity: row.quantity,
          proposedScore: row.proposedScore,
          basis: row.basis,
          status: row.status,
          createdAt: row.createdAt,
          /* 扣分项读"已经扣了多少"，另外两种读"现在给了多少"——和真实接口同一口径。 */
          currentScore: row.kind === 'penalty' ? (recorded ? recorded.score : null) : row.currentScore,
          expectedReviews: row.reviews.length,
          decidedReviews: row.reviews.filter((r) => r.at).length,
          myReview: seat?.at ? { decision: seat.decision, score: seat.score, reason: seat.reason, spentSeconds: seat.spentSeconds, at: seat.at } : null,
        }
      }
    }
    if (m === 'GET' && path === '/review/tasks') {
      const tab = query.get('tab') || 'mine'
      const u = requireUser(user)
      if (tab === 'appeal') {
        const items = db().appeals.filter((a) => a.status === 'reviewing').map((a) => ({ ...a, type: 'appeal' as const, handlers: a.handlers.map((h) => ({ ...h, mine: h.id === u.id })) }))
        return { items, tab }
      }
      return { items: reviewTasksFor(u, tab), tab }
    }
    {
      const p = match('/review/tasks/:id/decision', path)
      if (m === 'POST' && p) {
        const u = requireUser(user)
        const asg = db().assignments.find((a) => a.id === p.id)
        if (!asg) fail(404, 'not_found', '任务不存在')
        const sub = db().submissions.find((s) => s.id === asg.submissionId)
        if (!sub) fail(404, 'not_found', '提交不存在')
        const review: NamedReview = {
          id: nid('rv'),
          reviewer: u.name,
          reviewerSid: u.sid,
          reviewerId: u.id,
          decision: str(b.decision) as NamedReview['decision'],
          score: Number(b.score) || 0,
          reason: str(b.reason),
          spentSeconds: Number(b.spentSeconds) || 0,
          at: nowIso(),
        }
        db().reviews[sub.id] = [...(db().reviews[sub.id] ?? []), review]
        asg.decided = true
        const count = db().reviews[sub.id].length
        if (count >= 2) {
          const scores = db().reviews[sub.id].map((r) => r.score)
          if (scores[0] === scores[1]) {
            sub.status = 'scored'
            sub.finalScore = scores[0]
            sub.canAppeal = true
          } else {
            sub.status = 'arbitrating'
          }
        } else sub.status = 'consensus'
        sub.updatedAt = nowIso()
        return { reviewId: review.id, submissionStatus: sub.status, finalScore: sub.finalScore, conflict: sub.status === 'arbitrating' }
      }
    }
    {
      const p = match('/review/tasks/:id', path)
      if (m === 'GET' && p) {
        const u = requireUser(user)
        const asg = db().assignments.find((a) => a.id === p.id)
        if (!asg) fail(404, 'not_found', '任务不存在')
        const sub = db().submissions.find((s) => s.id === asg.submissionId)
        if (!sub) fail(404, 'not_found', '提交不存在')
        const mine = (db().reviews[sub.id] ?? []).find((r) => r.reviewerId === u.id)
        const detail: ReviewTaskDetail = {
          id: asg.id,
          title: sub.title,
          category: sub.category,
          itemKey: sub.itemKey,
          claim: sub.claim,
          requestedScore: sub.wantScore,
          ruleSnapshot: sub.ruleSnapshot,
          note: sub.note ?? '',
          submittedAt: sub.submittedAt,
          studentId: sub.studentId,
          student: sub.student,
          status: sub.status,
          expectedReviews: 2,
          evidence: db().evidence[sub.id] ?? [],
          noteEvidence: db().evidence[`notes:submissions:${p.id}`] ?? [],
          myReview: mine ? { id: mine.id, decision: mine.decision, score: mine.score, reason: mine.reason, spentSeconds: mine.spentSeconds, at: mine.at } : null,
        }
        return detail
      }
    }
    if (m === 'GET' && path === '/review/appeals') {
      const u = requireUser(user)
      const scope = query.get('scope') || 'pending'
      let items = db().appeals.filter((a) => a.handlers.some((h) => h.id === u.id) || u.role === 'class_admin')
      if (scope === 'pending') items = items.filter((a) => a.status === 'reviewing' || a.status === 'filed')
      if (scope === 'done') items = items.filter((a) => a.status === 'resolved' || a.status === 'final')
      return { items: items.map((a) => ({ ...a, type: 'appeal' as const, handlers: a.handlers.map((h) => ({ ...h, mine: h.id === u.id })) })) }
    }
    {
      const p = match('/review/appeals/:id/rereview', path)
      if (m === 'POST' && p) {
        const u = requireUser(user)
        const a = db().appeals.find((x) => x.id === p.id)
        if (!a) fail(404, 'not_found', '申诉不存在')
        const h = a.handlers.find((x) => x.id === u.id)
        if (h) {
          h.decided = true
          h.rereview = { decision: str(b.decision) as 'uphold' | 'adjust', score: Number(b.score) || 0, reason: str(b.reason), spentSeconds: Number(b.spentSeconds) || 0, at: nowIso() }
        }
        const both = a.handlers.every((x) => x.decided)
        const scores = a.handlers.map((x) => x.rereview?.score)
        const conflict = both && scores[0] !== scores[1]
        if (both && !conflict) {
          a.status = 'resolved'
          a.resolutionScore = scores[0] ?? null
          a.resolvedAt = nowIso()
        } else if (conflict) a.status = 'escalated'
        return { id: a.id, status: a.status, bothDecided: both, conflict, resolvedScore: a.resolutionScore }
      }
    }
    {
      const p = match('/review/appeals/:id', path)
      if (m === 'GET' && p) return appealDetail(p.id, requireUser(user))
    }
    if (m === 'GET' && path === '/review/students') {
      const u = requireUser(user)
      return {
        items: db()
          .users.filter((x) => x.role !== 'ops' && x.registered)
          .map((x) => ({
            userId: x.id,
            sid: x.sid,
            name: x.name,
            self: x.id === u.id,
            sealed: Boolean(db().sealed[x.id]?.sealed),
            scoredCount: db().submissions.filter((s) => s.studentId === x.sid && s.finalScore != null).length,
            openObjections: db().objections.filter((o) => o.studentUserId === x.id && o.status !== 'withdrawn' && o.status !== 'dismissed').length,
          })),
      }
    }
    {
      const p = match('/review/students/:uid/scorecard', path)
      if (m === 'GET' && p) return scorecardFor(p.uid)
    }
    if (m === 'GET' && path === '/review/objections') {
      const status = query.get('status')
      const items = db().objections.filter((o) => !status || o.status === status)
      return { items }
    }
    if (m === 'POST' && path === '/review/objections/submit') {
      const ids = Array.isArray(b.ids) ? (b.ids as string[]) : []
      const batchId = nid('obb')
      let submitted = 0
      for (const id of ids) {
        const o = db().objections.find((x) => x.id === id)
        if (o && o.status === 'draft') {
          o.status = 'submitted'
          o.batchId = batchId
          o.submittedAt = nowIso()
          submitted++
        }
      }
      return { batchId, submitted }
    }
    if (m === 'POST' && path === '/review/objections') {
      const u = requireUser(user)
      const stu = userById(str(b.studentUserId))
      const cat = db().scheme.categories.find((c) => c.key === b.category)
      const item = cat ? [...cat.baseItems, ...cat.penaltyItems, ...claimableItems(cat)].find((i) => i.key === b.itemKey) : undefined
      const row: Objection = {
        id: nid('ob'),
        kind: str(b.kind) as Objection['kind'],
        studentId: stu?.sid ?? '',
        studentUserId: str(b.studentUserId),
        student: stu?.name ?? '',
        category: str(b.category) as CategoryKey,
        categoryName: cat?.name ?? '',
        itemKey: str(b.itemKey),
        itemName: item && 'name' in item ? item.name : str(b.itemKey),
        targetId: (b.targetId as string | null) ?? null,
        currentScore: null,
        proposedScore: Number(b.proposedScore) || 0,
        quantity: b.quantity == null ? null : Number(b.quantity),
        basis: str(b.basis),
        status: 'draft',
        batchId: null,
        proposer: u.name,
        proposerId: u.id,
        decidedScore: null,
        decisionReason: null,
        createdAt: nowIso(),
        submittedAt: null,
        decidedAt: null,
      }
      db().objections.unshift(row)
      return { id: row.id, status: row.status }
    }
    {
      const p = match('/review/objections/:id/withdraw', path)
      if (m === 'POST' && p) {
        const o = db().objections.find((x) => x.id === p.id)
        if (o) o.status = 'withdrawn'
        return { id: p.id, status: 'withdrawn' }
      }
    }
    {
      const p = match('/review/objections/:id', path)
      if (p) {
        if (m === 'PUT') return { id: p.id }
        if (m === 'DELETE') {
          db().objections = db().objections.filter((o) => o.id !== p.id)
          return undefined
        }
      }
    }
    // 审核正文附件使用独立键，避免和学生原始材料混在一起。
    {
      const note = /^\/(submissions|appeals|report-reviews)\/([^/]+)\/notes\/(presign|[^/]+)(\/complete)?$/.exec(path)
      if (note && (m === 'POST' || m === 'DELETE')) {
        requireUser(user)
        const [, owner, ownerId, action, complete] = note
        const key = `notes:${owner}:${ownerId}`
        if (action === 'presign' && m === 'POST') {
          const id = nid('e')
          db().pendingUploads[id] = { id, kind: 'note', ownerId: key, filename: str(b.filename), mediaType: str(b.mediaType), sizeBytes: Number(b.sizeBytes) || 0 }
          return { evidenceId: id, uploadUrl: '/mock-upload', uploadFields: { key: id } }
        }
        const pending = db().pendingUploads[action]
        if (complete && m === 'POST') {
          if (!pending || pending.ownerId !== key) fail(404, 'not_found', '待上传附件不存在')
          const file: Evidence = { id: action, name: pending.filename, mediaType: pending.mediaType, sizeBytes: pending.sizeBytes, sha256: null, status: 'ready', uploadedAt: nowIso() }
          db().evidence[key] = [...(db().evidence[key] ?? []), file]
          delete db().pendingUploads[action]
          return { evidenceId: action, status: 'ready', filename: file.name, mediaType: file.mediaType }
        }
        if (m === 'DELETE') { delete db().pendingUploads[action]; return undefined }
      }
    }

    /* 提案草稿的佐证附件。真实后端挂在 /objections/:id/notes（note_evidence_handlers.go），
       演示站也照着走一遍，否则传完刷新一次文件就没了，看上去像上传失败。 */
    {
      const p = match('/objections/:id/notes/presign', path)
      if (m === 'POST' && p) {
        requireUser(user)
        const id = nid('e')
        db().pendingUploads[id] = { id, kind: 'note', ownerId: p.id, filename: str(b.filename), mediaType: str(b.mediaType), sizeBytes: Number(b.sizeBytes) || 0 }
        return { evidenceId: id, uploadUrl: '/mock-upload', uploadFields: { key: id }, expiresIn: 600, method: 'POST', completeUrl: `/objections/${p.id}/notes/${id}/complete` }
      }
    }
    {
      const p = match('/objections/:id/notes/:eid/complete', path)
      if (m === 'POST' && p) {
        const pending = db().pendingUploads[p.eid]
        const target = db().objections.find((o) => o.id === p.id)
        if (target) {
          target.noteEvidence = [
            ...(target.noteEvidence ?? []),
            {
              id: p.eid,
              name: pending?.filename || '附件',
              mediaType: pending?.mediaType || 'application/octet-stream',
              sizeBytes: pending?.sizeBytes || 0,
              sha256: null,
              status: 'ready',
              uploadedAt: nowIso(),
            },
          ]
        }
        delete db().pendingUploads[p.eid]
        return { evidenceId: p.eid, status: 'ready', filename: pending?.filename || '附件', mediaType: pending?.mediaType || 'application/octet-stream' }
      }
    }
    {
      const p = match('/objections/:id/notes/:eid', path)
      if (m === 'DELETE' && p) {
        const target = db().objections.find((o) => o.id === p.id)
        if (target) target.noteEvidence = (target.noteEvidence ?? []).filter((e) => e.id !== p.eid)
        delete db().pendingUploads[p.eid]
        return undefined
      }
    }

    /* 举报的佐证。和上面那三条同一条通道，区别在真实后端那一侧：那边不记上传人、
       审计也不记 actor。演示站本来就没有这两样东西可记，所以这里只是把文件挂上去。 */
    {
      const p = match('/reports/:id/notes/presign', path)
      if (m === 'POST' && p) {
        requireUser(user)
        const id = nid('e')
        db().pendingUploads[id] = { id, kind: 'note', ownerId: p.id, filename: str(b.filename), mediaType: str(b.mediaType), sizeBytes: Number(b.sizeBytes) || 0 }
        return { evidenceId: id, uploadUrl: '/mock-upload', uploadFields: { key: id }, expiresIn: 600, method: 'POST', completeUrl: `/reports/${p.id}/notes/${id}/complete` }
      }
    }
    {
      const p = match('/reports/:id/notes/:eid/complete', path)
      if (m === 'POST' && p) {
        const pending = db().pendingUploads[p.eid]
        const target = db().reports.find((r) => r.id === p.id)
        if (target) {
          target.evidence = [
            ...target.evidence,
            {
              id: p.eid,
              name: pending?.filename || '附件',
              mediaType: pending?.mediaType || 'application/octet-stream',
              sizeBytes: pending?.sizeBytes || 0,
              sha256: null,
              status: 'ready',
              uploadedAt: nowIso(),
            },
          ]
        }
        delete db().pendingUploads[p.eid]
        return { evidenceId: p.eid, status: 'ready', filename: pending?.filename || '附件', mediaType: pending?.mediaType || 'application/octet-stream' }
      }
    }
    {
      const p = match('/reports/:id/notes/:eid', path)
      if (m === 'DELETE' && p) {
        const target = db().reports.find((r) => r.id === p.id)
        if (target) target.evidence = target.evidence.filter((e) => e.id !== p.eid)
        delete db().pendingUploads[p.eid]
        return undefined
      }
    }
    if (m === 'GET' && path === '/review/category') {
      return {
        items: db().scheme.categories.map((c) => ({ category: c.key, total: 4, scored: 2, conflicts: 0, average: 6 })),
        /* 这组数要能互相对上，「我的审核量」页会把它们并排显示：
           assigned = pending + done，decisions 合计 = done，
           classAverage 与 spread 要能由 reviewerCount 个人的累计量还原（我 5、另一位 4）。 */
        load: {
          assigned: 5,
          pending: 2,
          done: 3,
          classAverage: 4.5,
          spread: 1,
          rank: 1,
          reviewerCount: 2,
          decisions: { accepted: 2, adjusted: 1, rejected: 0 },
          avgSpentSeconds: 90,
          groupDecisions: { accepted: 4, adjusted: 2, rejected: 0 },
          groupAvgSpentSeconds: 80,
        },
      }
    }
    if (m === 'GET' && path === '/review/history') {
      const u = requireUser(user)
      const items = Object.entries(db().reviews)
        .flatMap(([sid, rows]) => rows.filter((r) => r.reviewerId === u.id).map((r) => {
          const sub = db().submissions.find((s) => s.id === sid)
          return {
            id: r.id,
            submissionId: sid,
            title: sub?.title ?? '',
            studentId: sub?.studentId ?? '',
            student: sub?.student ?? '',
            category: sub?.category ?? 'moral',
            decision: r.decision,
            score: r.score,
            reason: r.reason,
            spentSeconds: r.spentSeconds,
            at: r.at,
            finalScore: sub?.finalScore ?? null,
            status: sub?.status ?? 'scored',
            changed: sub?.finalScore != null && sub.finalScore !== r.score,
          }
        }))
      return { items }
    }

    /* ---- 管理端 ---- */
    if (m === 'GET' && path === '/admin/scheme') {
      return {
        items: db().schemes.map((s) => ({
          id: s.id,
          name: s.name,
          version: s.version,
          status: s.status,
          lockVersion: s.lockVersion,
          publishedAt: s.status === 'published' ? '2026-08-08T00:00:00.000Z' : undefined,
          updatedAt: nowIso(),
        })),
      }
    }
    if (m === 'POST' && path === '/admin/scheme') {
      const id = nid('sch')
      const config = (b.config as SchemeConfig) ?? db().scheme
      db().schemes.push({ id, name: str(b.name) || '未命名草稿', version: null, status: 'draft', config, lockVersion: 1 })
      return { id, lockVersion: 1 }
    }
    {
      const p = match('/admin/scheme/:id/publish', path)
      if (m === 'POST' && p) {
        const draft = db().schemes.find((s) => s.id === p.id)
        if (!draft) fail(404, 'not_found', '方案不存在')
        const version = (db().scheme.version.split('.').map(Number)[1] ?? 0) + 1
        // 发布只换评分规则，时间线原样不动——真站里两者根本不在一张表上。
        db().scheme = { ...draft.config, version: `2024.${version}` }
        draft.status = 'published'
        draft.version = version
        return { id: draft.id, version, config: db().scheme }
      }
    }
    {
      const p = match('/admin/scheme/:id/share', path)
      if (m === 'POST' && p) return { requestId: nid('shr'), status: 'pending' }
    }
    {
      const p = match('/admin/scheme/:id', path)
      if (p) {
        if (m === 'GET') {
          const s = db().schemes.find((x) => x.id === p.id)
          if (!s) fail(404, 'not_found', '方案不存在')
          return s
        }
        if (m === 'PUT') {
          const s = db().schemes.find((x) => x.id === p.id)
          if (!s) fail(404, 'not_found', '方案不存在')
          s.name = str(b.name) || s.name
          if (b.config) s.config = b.config as SchemeConfig
          s.lockVersion += 1
          return { id: s.id, lockVersion: s.lockVersion }
        }
        if (m === 'DELETE') {
          db().schemes = db().schemes.filter((s) => s.id !== p.id)
          return undefined
        }
      }
    }
    if (m === 'GET' && path === '/admin/templates') {
      return { items: [{ id: 'tpl-1', name: db().scheme.schemeName, createdAt: '2026-07-01T00:00:00.000Z' }] }
    }
    if (m === 'GET' && path === '/admin/template-share-requests') return { items: [] }
    if (m === 'GET' && path === '/admin/class') return classInfo()
    /* 时间线写入。和真站一样，改这里不动 db().scheme——方案版本号不会变。
       /admin/window 是后端保留的旧别名，两条路走同一段逻辑。 */
    if (m === 'PUT' && (path === '/admin/timeline' || path === '/admin/window')) {
      const t = db().timeline
      if (typeof b.collegeName === 'string') t.collegeName = b.collegeName
      if (typeof b.enrollmentClass === 'string') t.enrollmentClass = b.enrollmentClass
      if (typeof b.academicYear === 'string') t.academicYear = b.academicYear
      t.window = {
        open: str(b.open) || WINDOW_OPEN,
        close: str(b.close) || WINDOW_CLOSE,
        lockdown: str(b.lockdown) || null,
      }
      if (typeof b.honorRollTopPercent === 'number') t.honorRoll.topPercent = b.honorRollTopPercent
      if (Array.isArray(b.awards)) {
        t.honorRoll.awards = (b.awards as { name?: unknown; topPercent?: unknown }[]).map((a) => ({
          name: str(a.name),
          topPercent: typeof a.topPercent === 'number' ? a.topPercent : 0,
        }))
      }
      return { window: t.window, honorRoll: t.honorRoll }
    }
    if (m === 'PUT' && path === '/admin/window/capabilities') {
      const key = str(b.key)
      if (key === 'gpa') fail(400, 'invalid_capability', '专业素质分导入默认开放，无需设置开关或截止时间')
      const on = Boolean(b.on)
      const caps = db().timeline.capabilities
      if (key === 'submit' || key === 'edit' || key === 'appeal' || key === 'review' || key === 'arbitrate') caps[key] = on
      if (key === 'export') caps.export = { on, gate: 'settlement' }
      return { capabilities: caps }
    }
    if (m === 'GET' && path === '/admin/whitelist') return { items: db().whitelist }
    if (m === 'POST' && path === '/admin/whitelist') {
      const id = nid('w')
      db().whitelist.push({
        id,
        sid: str(b.sid),
        name: str(b.name),
        role: 'student',
        active: true,
        registered: false,
        registeredAt: null,
        createdAt: nowIso(),
      })
      return { id }
    }
    if (m === 'POST' && path === '/admin/whitelist/import') {
      const lines = str(b.csv).split(/\r?\n/).filter(Boolean)
      return { imported: Math.max(0, lines.length - 1) }
    }
    {
      const p = match('/admin/whitelist/:id', path)
      if (m === 'DELETE' && p) {
        db().whitelist = db().whitelist.filter((w) => w.id !== p.id)
        return undefined
      }
    }
    if (m === 'GET' && path === '/admin/deputy') {
      const actor = requireRole(user, ['class_admin'])
      const appointed = db().users.find((member) => member.classId === actor.classId && member.isDeputy)
      return { deputy: appointed ? { id: appointed.id, sid: appointed.sid, name: appointed.name, registered: appointed.registered } : null }
    }
    if (m === 'PUT' && path === '/admin/deputy') {
      const actor = requireRole(user, ['class_admin'])
      if (!Object.hasOwn(b, 'userId')) fail(400, 'invalid_request', '请选择副班管，或明确撤销任命')
      let next: DemoUser | undefined
      if (b.userId !== null) {
        if ((typeof b.userId !== 'string' || !b.userId) && (typeof b.userId !== 'number' || !Number.isFinite(b.userId) || b.userId <= 0)) fail(400, 'invalid_id', '成员 ID 不正确')
        next = userById(String(b.userId))
        if (!next || next.classId !== actor.classId) fail(404, 'not_found', '班级成员不存在')
        if (next.role !== 'group' || next.status !== 'active' || !next.registered) fail(409, 'deputy_ineligible', '只能任命已注册且已启用的综测小组成员为副班管')
      }
      const current = db().users.find((member) => member.classId === actor.classId && member.isDeputy)
      if (current) current.isDeputy = false
      if (next) next.isDeputy = true
      appendAudit(actor, 'class.deputy_changed', 'class', String(actor.classId), { userId: current?.id ?? null }, { userId: next?.id ?? null })
      return undefined
    }
    if (m === 'GET' && path === '/admin/users') return { items: adminUsers() }
    if (m === 'GET' && path === '/admin/bonus-grants') return { items: db().bonusGrants.map((batch) => ({ ...batch,
      members: batch.members.map((member) => { const sub = db().submissions.find((s) => s.id === member.id); return { ...member, score: sub?.finalScore ?? member.score, status: sub?.status ?? member.status } }),
    })) }
    if (m === 'POST' && path === '/admin/bonus-grants') {
      const actor = requireRole(user, ['class_admin'])
      const locked = db().timeline.window.lockdown
      if (locked && Date.parse(locked) <= Date.now()) fail(409, 'class_locked', '本学期已全系统封锁')
      const ids = Array.isArray(b.studentIds) ? b.studentIds.map(str).sort() : []
      const title = str(b.title).trim(), note = str(b.note).trim(), requestId = str(b.requestId)
      const request = JSON.stringify({ ...b, title, note, studentIds: ids })
      const prior = db().bonusGrants.find((batch) => batch.id === requestId)
      if (prior) {
        if (prior.request !== request) fail(409, 'bonus_grant_changed', '本批次已经提交，不能修改内容')
        return { id: prior.id, count: prior.members.length, score: prior.score }
      }
      if (!requestId || !title || [...note].length < 4 || [...note].length > 2000 || !ids.length || ids.length > 1000 || new Set(ids).size !== ids.length) fail(422, 'bonus_grant_invalid', '请填写事项、依据并选择不重复的班级成员')
      if (b.schemeVersion !== db().scheme.version) fail(409, 'scheme_changed', '班级方案已更新，请刷新后重新确认')
      const members = ids.map((id) => userById(id))
      if (members.some((member) => !member || member.classId !== actor.classId || member.status !== 'active' || member.role === 'ops')) fail(422, 'bonus_member_invalid', '所选成员已停用或不属于本班，本批次未加分')
      const found = findItem(db().scheme, str(b.category) as CategoryKey, str(b.itemKey))
      if (!found) fail(422, 'bonus_grant_invalid', '请选择当前方案的加分小项')
      const claim = (b.claim ?? {}) as Submission['claim']
      const bounds = scoreBounds(found.item.scoreRule)
      const rule = found.item.scoreRule
      if ((rule.type === 'enum' && !rule.options.some((option) => option.label === claim.option)) ||
        (claim.score != null && (!Number.isFinite(claim.score) || claim.score < bounds.lo || claim.score > bounds.hi)) ||
        (claim.quantity != null && (!Number.isFinite(claim.quantity) || claim.quantity < 0))) fail(422, 'bonus_grant_invalid', '数量、档位或分值不在方案允许范围内')
      const rawScore = wantScore(found.item, claim)
      const score = rawScore == null ? null : Math.round(rawScore * 1000) / 1000
      if (score === null || !Number.isFinite(score) || score <= 0 || score > 99999 || score < bounds.lo || score > bounds.hi) fail(422, 'bonus_grant_invalid', '请填写方案允许的正分值')
      const created = members.map((member) => {
        const sub = createSubmission(member!, { ...b, title, note }, 'scored')
        sub.source = 'admin_grant'; sub.finalScore = score; sub.canAppeal = true
        appendAudit(actor, 'submission.admin_granted', 'submission', sub.id, null, { status: 'scored', finalScore: score }, { reason: note, batchId: requestId, selfGranted: member!.id === actor.id })
        delete db().confirmed[member!.id]
        return { id: sub.id, studentId: member!.id, sid: member!.sid, name: member!.name, score, status: sub.status, itemName: sub.itemName }
      })
      db().bonusGrants.unshift({ id: requestId, title, note, score, createdAt: nowIso(), members: created, request })
      return { id: requestId, count: created.length, score }
    }
    {
      const p = match('/admin/users/:id/role', path)
      if (m === 'PUT' && p) {
        const u = userById(p.id)
        if (u && u.role !== 'ops') {
          u.role = str(b.role) as Exclude<Role, 'ops'>
          clearIneligibleDeputy(u)
          const row = db().whitelist.find((item) => item.sid === u.sid)
          if (row) row.role = u.role
        }
        return undefined
      }
    }
    {
      const p = match('/admin/users/:id/status', path)
      if (m === 'PUT' && p) {
        const u = userById(p.id)
        if (u) {
          u.status = str(b.status) === 'disabled' ? 'disabled' : 'active'
          clearIneligibleDeputy(u)
        }
        return undefined
      }
    }
    {
      const p = match('/admin/users/:id/reset-password', path)
      if (m === 'POST' && p) {
        const u = userById(p.id)
        if (u?.status === 'disabled') fail(400, 'disabled', '请先启用账号，再重置密码')
        return { reset: u ? resetMemberPassword(u) : 0 }
      }
    }
    if (m === 'POST' && path === '/admin/users/reset-password') return { reset: db().users.reduce((count, u) => count + resetMemberPassword(u), 0) }
    if (m === 'GET' && path === '/admin/seals') return { items: adminSeals() }
    if (m === 'POST' && path === '/admin/seals/remind') return { recipientCount: 3 }
    {
      const p = match('/admin/seals/:uid/unseal', path)
      if (m === 'POST' && p) {
        db().sealed[p.uid] = { sealed: false, sealedAt: null, source: null }
        delete db().confirmed[p.uid]
        return undefined
      }
    }
    if (m === 'GET' && path === '/admin/review-progress') {
      const keyword = (query.get('q') ?? '').trim().toLowerCase()
      const student = (query.get('student') ?? '').trim().toLowerCase()
      const reviewer = (query.get('reviewer') ?? '').trim().toLowerCase()
      const kind = query.get('kind') ?? ''
      const status = query.get('status') ?? ''
      const items = db().submissions
        .filter((s) => s.status !== 'draft')
        .filter((s) => !keyword || [s.studentId, s.student, s.title, s.note, s.itemName].join(' ').toLowerCase().includes(keyword))
        .map((s) => ({
          id: `item:${s.id}`,
          submissionId: s.id,
          finalScore: s.finalScore,
          forceRejection: s.forceRejection,
        forcedScore: s.forcedScore,
          canForceScore: !s.forceRejection && s.finalScore != null && ['scored', 'locked', 'appealing', 'arbitrating'].includes(s.status) && adjudicationPermission(requireUser(user), s.studentId).canAdjudicate,
        forceScoreBlockedReason: s.finalScore == null ? '只能强制修改已定分的材料' : adjudicationPermission(requireUser(user), s.studentId).recusalReason,
        canForceReject: !s.forceRejection && adjudicationPermission(requireUser(user), s.studentId).canAdjudicate,
          forceRejectBlockedReason: s.forceRejection ? '该条目已被强制驳回' : adjudicationPermission(requireUser(user), s.studentId).recusalReason,
          kind: 'item' as const,
          status: (s.status === 'scored' ? 'complete' : s.status === 'pending' ? '0/2' : s.status === 'consensus' ? '1/2' : s.status === 'arbitrating' ? 'pending_admin' : 'complete') as 'complete' | '0/2' | '1/2' | 'pending_admin',
          studentId: s.studentId,
          student: s.student,
          title: s.title,
          reviewers: [
            { id: 'u-li', sid: '20240002', name: '李审核', submitted: Boolean(db().reviews[s.id]?.some((r) => r.reviewerId === 'u-li')) },
            { id: 'u-zhou', sid: '20240006', name: '周复评', submitted: Boolean(db().reviews[s.id]?.some((r) => r.reviewerId === 'u-zhou')) },
          ],
          expected: 2,
          submitted: db().reviews[s.id]?.length ?? 0,
          assignedAt: s.submittedAt,
          waitingSeconds: 3600,
          overdue: false,
          detailUrl: `?v=admSubs&id=${s.id}`,
          trail: [],
        }))
        .filter((row) => (!kind || kind === row.kind)
          && (!student || `${row.studentId} ${row.student}`.toLowerCase().includes(student))
          && (!reviewer || row.reviewers.some((person) => `${person.sid} ${person.name}`.toLowerCase().includes(reviewer)))
          && (!status || status === 'all' || status === row.status || (status === 'unfinished' && row.status !== 'complete') || (status === 'overdue' && row.overdue)))
      return pageOf(items, query)
    }
    if (m === 'POST' && path === '/admin/review-progress/remind') return { recipientCount: 2, overdueCount: 0 }
    if (m === 'GET' && path === '/admin/review-sla') return { itemHours: 72 }
    if (m === 'PUT' && path === '/admin/review-sla') return { itemHours: Number(b.itemHours) || 72 }
    if (m === 'GET' && path === '/admin/dispatch') return dispatchState()
    if ((m === 'POST' && path === '/admin/dispatch/preview') || (m === 'POST' && path === '/admin/dispatch/run')) {
      const rows = dispatchState().reviewers.map((r) => ({ reviewerId: r.userId, name: r.name, sid: r.sid, before: r.assigned, after: r.assigned, delta: 0 }))
      return { policy: 'balanced_per_submission', seed: db().dispatch.seed, avoidSelf: true, targets: 0, rows, spreadBefore: 0, spreadAfter: 0, blocked: [], assigned: 0 }
    }
    if (m === 'PUT' && path === '/admin/dispatch/auto') {
      db().dispatch.auto = Boolean(b.on)
      return { auto: db().dispatch.auto }
    }
    {
      const p = match('/admin/dispatch/reviewers/:uid/queue', path)
      if (m === 'GET' && p) {
        const items = db()
          .assignments.filter((a) => a.reviewerId === p.uid && !a.decided)
          .map((a) => {
            const s = db().submissions.find((x) => x.id === a.submissionId)!
            return { submissionId: s.id, title: s.title, student: s.student, studentId: s.studentId, category: s.category, submittedAt: s.submittedAt, peer: '周复评', peerDecided: false }
          })
        return { items }
      }
    }
    {
      const p = match('/admin/dispatch/reviewers/:uid', path)
      if (m === 'PUT' && p) {
        db().dispatch.paused[p.uid] = Boolean(b.paused)
        const r = dispatchState().reviewers.find((x) => x.userId === p.uid)
        return r
      }
    }
    if (m === 'GET' && path === '/admin/submissions') {
      const visible = adjudicationRows(db().submissions, requireUser(user), deputy, (sub) => sub.studentId)
        .filter((sub) => sub.status !== 'draft' && (!deputy || query.get('force_rejectable') === 'true' || ['arbitrating', 'scored', 'locked', 'appealing'].includes(sub.status)))
      const items = filterSubs(visible, query).map((s) => ({
        id: s.id,
        studentId: s.studentId,
        student: s.student,
        category: s.category,
        itemKey: s.itemKey,
        title: s.title,
        requestedScore: s.wantScore,
        finalScore: s.finalScore,
        status: s.status,
        submittedAt: s.submittedAt,
        forceRejection: s.forceRejection,
        forcedScore: s.forcedScore,
        canForceScore: !s.forceRejection && s.finalScore != null && ['scored', 'locked', 'appealing', 'arbitrating'].includes(s.status) && adjudicationPermission(requireUser(user), s.studentId).canAdjudicate,
        forceScoreBlockedReason: s.finalScore == null ? '只能强制修改已定分的材料' : adjudicationPermission(requireUser(user), s.studentId).recusalReason,
        canForceReject: !s.forceRejection && adjudicationPermission(requireUser(user), s.studentId).canAdjudicate,
        forceRejectBlockedReason: s.forceRejection ? '该条目已被强制驳回' : adjudicationPermission(requireUser(user), s.studentId).recusalReason,
        updatedAt: s.updatedAt,
        reviews: deputy && ['pending', 'consensus'].includes(s.status) ? [] : db().reviews[s.id] ?? [],
        evidence: db().evidence[s.id],
        noteEvidence: db().evidence[`notes:submissions:${s.id}`] ?? [],
        /* 仲裁台要对着学生原本交的东西判，这三样都在 submission 上，一并下发。 */
        claim: s.claim,
        note: s.note ?? '',
        ruleSnapshot: s.ruleSnapshot,
        filedCategory: s.filedCategory ?? s.filedRuleSnapshot?.categoryKey ?? s.ruleSnapshot.categoryKey,
        filedItemKey: s.filedItemKey ?? s.filedRuleSnapshot?.item.key ?? s.ruleSnapshot.item.key,
        ...adjudicationPermission(requireUser(user), s.studentId),
      }))
      return pageOf(items, query)
    }
    {
      const forceScore = path.endsWith('/force-score')
      const p = match('/admin/submissions/:id/force-reject', path) ?? match('/admin/submissions/:id/force-score', path)
      if (m === 'POST' && p) {
        const s = db().submissions.find((x) => x.id === p.id)
        if (!s) fail(404, 'not_found', '提交不存在')
        requireAdjudicationTarget(requireUser(user), s.studentId)
        const lockdown = db().timeline.window.lockdown
        if (lockdown && Date.parse(lockdown) <= Date.now()) fail(403, 'class_locked_down', '班级已封锁，请先调整时间线')
        const reason = str(b.reason).trim()
        if (!reason || [...reason].length > 2000) fail(422, 'reason_required', '请填写不超过 2000 字的驳回理由')
        if (s.status === 'draft') fail(409, 'not_submitted', '草稿尚未提交审核')
        if (s.forceRejection) fail(409, 'submission_force_rejected', '该条目已被强制驳回')
        const newScore = forceScore ? Math.round(Number(b.score) * 1000) / 1000 : 0
        if (forceScore) {
          if (s.finalScore == null || !['scored', 'locked', 'appealing', 'arbitrating'].includes(s.status)) fail(409, 'force_score_unavailable', '只能修改已定分材料')
          if (b.previousScore !== s.finalScore) fail(409, 'score_changed', '认定分已变化，请刷新材料后重新确认')
          const bounds = scoreBounds(s.ruleSnapshot.item.scoreRule)
          if (b.score == null || !Number.isFinite(newScore) || newScore < bounds.lo || newScore > bounds.hi || newScore === s.finalScore) fail(422, 'score_invalid', '新认定分须在规则范围内且与原分不同')
        }
        const before = { status: s.status, finalScore: s.finalScore }
        const correction = { reason, previousScore: s.finalScore, rejectedAt: nowIso(), actorName: requireUser(user).name, ...(forceScore ? { score: newScore } : {}) }
        if (forceScore) s.forcedScore = correction
        else s.forceRejection = correction
        s.finalScore = newScore
        s.status = 'scored'
        s.canAppeal = false
        s.updatedAt = correction.rejectedAt
        for (const a of db().appeals) {
          if (a.targetType === 'submission' && a.targetId === s.id && ['filed', 'reviewing', 'escalated'].includes(a.status)) {
            a.status = 'final'
            a.resolutionScore = newScore
            a.resolutionReason = reason
            a.updatedAt = s.updatedAt
          }
        }
        for (const o of db().objections) {
          if (o.kind === 'submission' && o.targetId === s.id && ['draft', 'submitted'].includes(o.status)) o.status = 'dismissed'
        }
        for (const c of db().classificationSuggestions) {
          if (c.submissionId === s.id && ['draft', 'pending'].includes(c.status)) c.status = 'rejected'
        }
        for (const r of db().reports) {
          if (r.kind === 'submission' && r.targetId === s.id && ['reviewing', 'escalated'].includes(r.status)) {
            r.status = 'final'
            r.finalScore = newScore
            r.decisionReason = reason
            r.decidedAt = s.updatedAt
          }
        }
        db().assignments = db().assignments.filter((a) => a.submissionId !== s.id || a.decided)
        const student = userBySid(s.studentId)
        if (student) {
          delete db().confirmed[student.id]
        }
        db().flags.forceRejectionSettlementStale = true
        appendAudit(user, forceScore ? 'submission.force_scored' : 'submission.force_rejected', 'submission', s.id, before, { status: s.status, finalScore: newScore, forceRejection: s.forceRejection, forcedScore: s.forcedScore }, { ...adjudicatorMetadata, reason })
        return { id: s.id, status: s.status, finalScore: newScore, forceRejection: s.forceRejection, forcedScore: s.forcedScore }
      }
    }
    {
      const p = match('/admin/submissions/:id/arbitrate', path)
      if (m === 'POST' && p) {
        const s = db().submissions.find((x) => x.id === p.id)
        if (!s) fail(404, 'not_found', '提交不存在')
        requireAdjudicationTarget(requireUser(user), s.studentId)
        const legacyCross = s.status === 'locked' && db().classificationSuggestions.some((suggestion) => suggestion.submissionId === s.id && suggestion.scope === 'cross_category' && suggestion.status === 'pending')
        if (s.status !== 'arbitrating' && !legacyCross) fail(409, 'not_arbitrating', '当前条目不需要仲裁')
        const reason = decisionReason(b.reason)
        const before = { status: s.status, finalScore: s.finalScore, category: s.category, itemKey: s.itemKey }
        applySubmissionDecision(s, b)
        appendAudit(user, 'submission.arbitrated', 'submission', s.id, before, { status: s.status, finalScore: s.finalScore, category: s.category, itemKey: s.itemKey }, { ...adjudicatorMetadata, reason })
        return { id: s.id, status: s.status, finalScore: s.finalScore, category: s.category, itemKey: s.itemKey }
      }
    }
    if (m === 'GET' && path === '/admin/appeals') {
      /* origin 把"压着一份终表的申诉"和"学生自己直接提的"分开。
         班管清终表时先清前者，后端用 origin_batch_id 记的就是这件事。 */
      const origin = (query.get('origin') ?? '').trim()
      const visible = adjudicationRows(db().appeals, requireUser(user), deputy, (appeal) => appeal.studentId)
        .filter((a) => a.reason.trim() && !(a.status === 'filed' && a.handlers.length === 0) && (!query.get('status') || a.status === query.get('status')))
      const scoped = origin === 'batch' ? visible.filter((a) => !!a.originBatchId)
        : origin === 'direct' ? visible.filter((a) => !a.originBatchId)
        : visible
      return pageOf(filterRows(scoped, query, (a) => [a.student, a.studentId, a.target, a.reason], (a) => a.category), query)
    }
    {
      const p = match('/admin/appeals/:id', path)
      if (m === 'GET' && p) {
        const appeal = db().appeals.find((item) => item.id === p.id)
        if (!appeal) fail(404, 'not_found', '申诉不存在')
        const viewer = requireUser(user)
        requireAdjudicationRead(viewer, appeal.studentId, deputy)
        if (!appeal.reason.trim() || (appeal.status === 'filed' && appeal.handlers.length === 0)) fail(404, 'not_found', '申诉尚未提交')
        return appealDetail(p.id, viewer)
      }
    }
    {
      const p = match('/admin/appeals/:id/final', path)
      if (m === 'POST' && p) {
        const a = db().appeals.find((x) => x.id === p.id)
        if (!a) fail(404, 'not_found', '申诉不存在')
        const actor = requireUser(user)
        requireAdjudicationTarget(actor, a.studentId)
        if (a.status !== 'escalated') fail(409, 'appeal_not_escalated', '只有升级申诉需要终裁')
        const reason = decisionReason(b.reason)
        const score = b.decision === 'reject' ? 0 : decisionScore(b.score)
        const original = db().submissions.find((sub) => sub.id === a.targetId)
        if (a.targetType === 'submission' && original) {
          if (original.studentId !== a.studentId) fail(403, 'target_mismatch', '申诉对象与班级成员不一致')
          applySubmissionDecision(original, { ...b, score }, false)
          a.resolutionCategory = original.category
          a.resolutionItemKey = original.itemKey
        } else {
          const subject = userBySid(a.studentId)!
          const base = db().bases.find((row) => row.userId === subject.id && row.category === a.category && row.itemKey === a.itemKey)
          if (!base) fail(404, 'not_found', '申诉的计分记录不存在')
          if ((base.kind === 'base' && (score < 0 || (base.fullScore !== null && score > base.fullScore))) || (base.kind === 'penalty' && score > 0)) fail(422, 'score_invalid', '认定分不在允许的范围内')
          base.score = score
          base.basis = reason
          base.canAppeal = false
          base.updatedAt = nowIso()
        }
        a.status = 'final'
        a.resolutionScore = score
        a.resolutionReason = reason
        a.handler = actor.name
        a.currentScore = score
        a.resolvedAt = nowIso()
        a.updatedAt = a.resolvedAt
        appendAudit(actor, 'appeal.final', 'appeal', a.id, { status: 'escalated' }, { status: a.status, score }, { ...adjudicatorMetadata, reason })
        return { id: a.id, status: a.status, score: a.resolutionScore, category: a.resolutionCategory ?? a.category, itemKey: a.resolutionItemKey ?? a.itemKey }
      }
    }
    if (m === 'GET' && path === '/admin/classification-suggestions') {
      const items = db().classificationSuggestions.filter((suggestion) => !query.get('status') || suggestion.status === query.get('status')).map((suggestion) => {
        const sub = db().submissions.find((row) => row.id === suggestion.submissionId)
        return { ...suggestion, studentId: sub ? userBySid(sub.studentId)?.id : undefined, studentSid: sub?.studentId, student: sub?.student, title: sub?.title, finalScore: sub?.finalScore }
      })
      return { items: adjudicationRows(items, requireUser(user), deputy, (suggestion) => suggestion.studentSid ?? '') }
    }
    {
      const p = match('/admin/submissions/:id/classification/suggest', path)
      if (m === 'POST' && p) {
        const sub = db().submissions.find((row) => row.id === p.id)
        if (!sub) fail(404, 'not_found', '提交不存在')
        requireAdjudicationTarget(requireUser(user), sub.studentId)
        if (!['scored', 'arbitrating', 'locked'].includes(sub.status)) fail(409, 'not_classifiable', '只有已定分或待终裁条目可以提出分类建议')
        const reason = decisionReason(b.reason)
        const category = str(b.category) as CategoryKey
        const itemKey = str(b.itemKey)
        if (!findItem(db().scheme, category, itemKey) || (sub.category === category && sub.itemKey === itemKey)) fail(422, 'classification_invalid', '请选择其他有效小项')
        const suggestion: ClassificationSuggestion = {
          id: nid('cs'), submissionId: sub.id, scope: sub.category === category ? 'within_category' : 'cross_category',
          status: 'pending', fromCategory: sub.category, fromItemKey: sub.itemKey, toCategory: category, toItemKey: itemKey,
          reason, source: 'admin', createdAt: nowIso(),
        }
        db().classificationSuggestions.push(suggestion)
        if (suggestion.scope === 'cross_category' && sub.status !== 'locked') {
          sub.status = 'arbitrating'
          sub.finalScore = null
          sub.updatedAt = nowIso()
        }
        appendAudit(user, 'classification.suggested', 'classification_suggestion', suggestion.id, null, suggestion, adjudicatorMetadata)
        return { id: suggestion.id, scope: suggestion.scope, status: suggestion.status }
      }
    }
    {
      const p = match('/admin/submissions/:id/classification/resolve', path)
      if (m === 'POST' && p) {
        const sub = db().submissions.find((row) => row.id === p.id)
        if (!sub) fail(404, 'not_found', '提交不存在')
        requireAdjudicationTarget(requireUser(user), sub.studentId)
        const suggestion = db().classificationSuggestions.find((row) => row.id === b.suggestionId)
        if (b.suggestionId && (!suggestion || suggestion.submissionId !== sub.id || suggestion.status !== 'pending')) fail(409, 'suggestion_unavailable', '分类建议不属于本申报或已经处理')
        if (suggestion?.scope === 'cross_category' || (b.category && b.category !== sub.category)) fail(409, 'classification_arbitration_required', '跨大项分类必须在仲裁台同时签发分类、分数与终裁理由')
        const reason = decisionReason(b.reason)
        const before = { category: sub.category, itemKey: sub.itemKey, score: sub.finalScore }
        applySubmissionDecision(sub, b)
        const id = nid('cr')
        const after = { submissionId: sub.id, category: sub.category, itemKey: sub.itemKey, score: sub.finalScore, status: sub.status }
        appendAudit(user, 'classification.resolved', 'classification_resolution', id, before, after, { ...adjudicatorMetadata, reason, submissionId: sub.id })
        return { id, ...after }
      }
    }
    if (m === 'GET' && path === '/admin/reports') {
      return {
        items: adjudicationRows(db().reports, requireUser(user), deputy, (row) => row.studentId).map((row) => ({
          canAdjudicate: row.canAdjudicate,
          recusalReason: row.recusalReason,
          id: row.id,
          kind: row.kind,
          studentUserId: row.studentUserId,
          studentId: row.studentId,
          student: row.student,
          category: row.category,
          categoryName: row.categoryName,
          itemKey: row.itemKey,
          itemName: row.itemName,
          quantity: row.quantity,
          currentScore: row.currentScore,
          proposedScore: row.proposedScore,
          basis: row.basis,
          status: row.status,
          finalScore: row.finalScore,
          decisionReason: row.decisionReason,
          createdAt: row.createdAt,
          decidedAt: row.decidedAt,
          evidence: row.evidence,
          noteEvidence: db().evidence[`notes:report-reviews:${row.id}`] ?? [],
          /* 复核人姓名对班管可见；举报人依然不可见。 */
          reviews: row.reviews.map((review) => {
            const who = db().users.find((u) => u.id === review.reviewerId)
            return {
              reviewer: who?.name ?? '审核人',
              reviewerSid: who?.sid ?? '',
              decision: review.decision,
              score: review.score,
              reason: review.reason,
              spentSeconds: review.spentSeconds,
              at: review.at,
              decided: Boolean(review.at),
            }
          }),
        })),
      }
    }
    {
      const p = match('/admin/reports/:id/final', path)
      if (m === 'POST' && p) {
        const row = db().reports.find((r) => r.id === p.id)
        if (!row) fail(404, 'not_found', '举报不存在')
        requireAdjudicationTarget(requireUser(user), row.studentId)
        if (row.status !== 'escalated') fail(409, 'report_not_escalated', '只有升上来的举报需要终裁')
        const score = decisionScore(b.score)
        const reason = decisionReason(b.reason)
        if (row.kind === 'submission' && !db().submissions.some((sub) => sub.id === row.targetId && sub.studentId === row.studentId)) fail(403, 'target_mismatch', '举报对象与班级成员不一致')
        const ceiling = row.kind === 'penalty' ? 0 : (row.currentScore ?? 0)
        if (score > ceiling || (row.kind !== 'penalty' && score < 0)) fail(422, 'score_invalid', '裁定分不能高于当前分或低于允许范围')
        row.status = 'final'
        row.finalScore = score
        row.decisionReason = reason
        row.decidedAt = nowIso()
        if (score !== ceiling) applyMockOutcome(row, score)
        appendAudit(user, 'report.finalized', 'report', row.id, { status: 'escalated' }, { status: row.status, score }, { ...adjudicatorMetadata, reason })
        return { id: row.id, status: 'final', score }
      }
    }
    if (m === 'GET' && path === '/admin/objections') {
      const visible = adjudicationRows(db().objections, requireUser(user), deputy, (objection) => objection.studentId)
        .filter((row) => row.status !== 'draft' && row.status !== 'withdrawn' && (!query.get('status') || row.status === query.get('status')))
      return pageOf(filterRows(visible, query, (o) => [o.student, o.studentId, o.itemName, o.basis], (o) => o.category), query)
    }
    {
      const p = match('/admin/objections/:id/decide', path)
      if (m === 'POST' && p) {
        const o = db().objections.find((x) => x.id === p.id)
        if (!o) fail(404, 'not_found', '提案不存在')
        requireAdjudicationTarget(requireUser(user), o.studentId)
        if (o.status !== 'submitted') fail(409, 'objection_not_submitted', '只有已提交的提案可以终裁')
        const action = str(b.action)
        if (!['apply', 'adjust', 'dismiss'].includes(action)) fail(422, 'decision_invalid', '请选择处理结论')
        const reason = decisionReason(b.reason)
        const score = action === 'dismiss' ? null : decisionScore(action === 'adjust' ? b.score : o.proposedScore)
        if (score !== null) validateObjectionScore(o, score)
        applyObjectionDecision(o, action, score, reason)
        appendAudit(user, 'objection.decided', 'objection', o.id, { status: 'submitted' }, { status: o.status, score }, { ...adjudicatorMetadata, reason })
        return { id: o.id, status: o.status, score: o.decidedScore }
      }
    }
    if (m === 'POST' && path === '/admin/objections/decide-batch') {
      const ids = Array.isArray(b.ids) ? [...new Set(b.ids.map(String))] : []
      if (!ids.length) fail(422, 'ids_required', '请至少选择一条提案')
      const action = str(b.action)
      if (action !== 'apply' && action !== 'dismiss') fail(422, 'decision_invalid', '批量操作只支持通过或驳回')
      const reason = decisionReason(b.reason)
      // 先校验全部对象，混入本人或范围外提案时整批不写入。
      const rows = ids.map((id) => {
        const o = db().objections.find((x) => x.id === id)
        if (!o) fail(404, 'not_found', '提案不存在')
        requireAdjudicationTarget(requireUser(user), o.studentId)
        if (o.status !== 'submitted') fail(409, 'objection_not_submitted', '只有已提交的提案可以终裁')
        if (action === 'apply') validateObjectionScore(o, decisionScore(o.proposedScore))
        return o
      })
      for (const o of rows) {
        const score = action === 'dismiss' ? null : o.proposedScore
        applyObjectionDecision(o, action, score, reason)
        appendAudit(user, 'objection.decided', 'objection', o.id, { status: 'submitted' }, { status: o.status, score }, { ...adjudicatorMetadata, reason })
      }
      return { decided: rows.length }
    }
    if (m === 'GET' && path === '/admin/gpa') {
      const items = db().gpa
      const scores = items.map((g) => g.score).filter((n): n is number => n != null)
      return {
        items,
        imported: scores.length,
        total: items.length,
        complete: scores.length === items.length,
        average: scores.length ? scores.reduce((a, b) => a + b, 0) / scores.length : null,
        latestAt: items[0]?.updatedAt ?? null,
        weights: db().scheme.weights,
        honorTopPercent: db().timeline.honorRoll.topPercent,
      }
    }
    if ((m === 'POST' && path === '/admin/gpa/paste') || (m === 'POST' && path === '/admin/gpa/import')) {
      return { batch: nid('gpa'), imported: db().gpa.length, totalStudents: db().gpa.length, missing: [], complete: true }
    }
    if (m === 'GET' && path === '/admin/gate') return gate()
    if (m === 'POST' && path === '/admin/gate/force') {
      db().gateForced = true
      return gate()
    }
    if (m === 'POST' && path === '/admin/settle') {
      return {
        runId: nid('run'),
        completedAt: nowIso(),
        reused: false,
        items: db()
          .users.filter((u) => u.role !== 'ops' && u.registered)
          .map((u, i, arr) => ({
            userId: u.id,
            sid: u.sid,
            name: u.name,
            categoryScores: { major: 80, moral: 70, practice: 60, health: 60 },
            totalScore: 75 - i,
            classRank: i + 1,
            majorRank: i + 8,
            honor: i < 3,
            awardTier: awardTierFor(i + 1, arr.length),
            details: {},
          })),
      }
    }
    if (m === 'GET' && path === '/admin/stats') {
      const g = gate()
      const subs = db().submissions
      const current = currentDemoScore(requireUser(user))!
      const members = db().users.filter((u) => u.role !== 'ops' && u.status === 'active' && u.classId === requireUser(user).classId)
      return {
        users: members.length,
        sealed: Object.values(db().sealed).filter((x) => x.sealed).length,
        gpaImported: db().gpa.filter((x) => x.score != null).length,
        submissions: {
          draft: subs.filter((s) => s.status === 'draft').length,
          pending: subs.filter((s) => s.status === 'pending').length,
          consensus: subs.filter((s) => s.status === 'consensus').length,
          scored: subs.filter((s) => s.status === 'scored').length,
          appealing: subs.filter((s) => s.status === 'appealing').length,
          arbitrating: subs.filter((s) => s.status === 'arbitrating').length,
          locked: subs.filter((s) => s.status === 'locked').length,
        },
        reviews: Object.values(db().reviews).reduce((n, r) => n + r.length, 0),
        activeAppeals: db().appeals.filter((a) => a.reason.trim() && a.handlers.length > 0 && a.status !== 'resolved' && a.status !== 'final').length,
        pendingConflicts: 0,
        gate: g,
        settlement: { runId: 'run-1', completedAt: '2026-08-24T08:00:00.000Z', stale: Boolean(db().flags.forceRejectionSettlementStale) },
        distribution: { min: 61, average: 78, median: 78, max: 91 },
        reviewProgress: { item: { '0/2': 2, '1/2': 0, complete: 3 }, scorecard: {}, overdue: { item: 0, scorecard: 0 } },
        ranking: {
          calculatedAt: current.calculatedAt, rankingReady: current.rankingReady,
          classSize: current.classSize, gpaImported: current.gpaImported,
          scoredItems: subs.filter((s) => ['scored', 'locked'].includes(s.status)).length,
          totalItems: subs.length,
          pendingFinal: 2,
          items: members.map((u) => {
            const score = currentDemoScore(u)!
            const own = subs.filter((s) => s.studentId === u.sid)
            return { sid: u.sid, name: u.name, total: score.totalScore, categoryScores: score.categoryScores, categoryRanks: score.categoryRanks, classRank: score.classRank,
              scored: own.filter((s) => ['scored', 'locked'].includes(s.status)).length, pending: score.pendingItems, conflicts: own.filter((s) => s.status === 'arbitrating').length }
          }),
        },
      }
    }
    if (m === 'POST' && path === '/admin/export') {
      const job = { jobId: nid('ex'), kind: str(b.kind) as 'summary' | 'detail' | 'archive', status: 'complete' as const, createdAt: nowIso(), downloadUrl: '/favicon.svg', downloadExpiresIn: 600 }
      db().exportJobs.push(job)
      return job
    }
    {
      const p = match('/admin/export/:id', path)
      if (m === 'GET' && p) return db().exportJobs.find((j) => j.jobId === p.id) ?? { jobId: p.id, kind: 'summary', status: 'complete', downloadUrl: '/favicon.svg' }
    }
    if (m === 'GET' && path === '/admin/adjudication-history') return adjudicationHistory(requireRole(user, ['class_admin']), query)
    {
      const p = match('/admin/adjudication-history/:id', path)
      if (m === 'GET' && p) return adjudicationHistory(requireRole(user, ['class_admin']), query, p.id)
    }
    if (m === 'GET' && path === '/admin/audit-log') return pageOf(db().audit, query)
    if (m === 'GET' && path === '/admin/knowledge') return knowledgeState()
    if (m === 'PUT' && path === '/admin/knowledge/policy') return undefined
    if (m === 'POST' && path === '/admin/knowledge/documents/presign') {
      const id = nid('kd')
      db().pendingUploads[id] = { id, kind: 'knowledge', ownerId: id, filename: str(b.filename), mediaType: str(b.mediaType), sizeBytes: Number(b.sizeBytes) || 0 }
      return { documentId: id, uploadUrl: uploadUrl(), uploadFields: { key: id }, expiresIn: 600, limits: knowledgeState().limits }
    }
    {
      const p = match('/admin/knowledge/documents/:id/complete', path)
      if (m === 'POST' && p) {
        const pending = db().pendingUploads[p.id]
        db().knowledgeDocs.push({
          id: p.id,
          filename: pending?.filename || '文档',
          logicalPath: pending?.filename || 'doc',
          displayName: pending?.filename || '文档',
          mediaType: pending?.mediaType || 'application/pdf',
          sizeBytes: pending?.sizeBytes || 0,
          status: 'ready',
          searchable: true,
          extractor: 'pdf',
          entries: 1,
          pageCount: 1,
          sheetCount: null,
          warning: null,
          error: null,
          createdAt: nowIso(),
          updatedAt: nowIso(),
          publishedAt: nowIso(),
          text: '演示上传的班级资料。',
        })
        return undefined
      }
    }
    {
      const p = match('/admin/knowledge/documents/:id', path)
      if (p) {
        if (m === 'GET') {
          const d = db().knowledgeDocs.find((x) => x.id === p.id)
          if (!d) fail(404, 'not_found', '文档不存在')
          return {
            ...d,
            entriesPreview: [{ id: 'ke-1', documentId: d.id, filename: d.filename, logicalPath: d.logicalPath, kind: 'page', locator: { page: 1 }, startLine: 1, endLine: 20, text: d.text, lineCount: 20, charCount: d.text.length }],
            downloadUrl: '/favicon.svg',
            downloadExpiresIn: 600,
          }
        }
        if (m === 'PUT') {
          const d = db().knowledgeDocs.find((x) => x.id === p.id)
          if (d && b.displayName) d.displayName = str(b.displayName)
          return undefined
        }
        if (m === 'DELETE') {
          db().knowledgeDocs = db().knowledgeDocs.filter((d) => d.id !== p.id)
          return undefined
        }
        if (m === 'POST' && path.endsWith('/reprocess')) return undefined
      }
    }
    {
      const p = match('/admin/knowledge/entries/:id', path)
      if (m === 'GET' && p) {
        const d = db().knowledgeDocs[0]
        return { id: p.id, documentId: d?.id, filename: d?.filename, logicalPath: d?.logicalPath, locator: { page: 1 }, startLine: 1, endLine: 20, text: d?.text ?? '' }
      }
    }

    /* ---- Agent ---- */
    if (m === 'GET' && path === '/agent/status') return agentStatus()
    if (m === 'GET' && path === '/agent/conversations') {
      return { items: db().conversations.map((c) => ({ id: c.id, title: c.title, preview: c.messages.at(-1)?.content ?? '', createdAt: c.createdAt, updatedAt: c.updatedAt })) }
    }
    if (m === 'POST' && path === '/agent/conversations') {
      const row = { id: nid('cv'), title: str(b.title) || '新对话', messages: [], createdAt: nowIso(), updatedAt: nowIso() }
      db().conversations.unshift(row)
      return row
    }
    {
      const p = match('/agent/conversations/:id/messages', path)
      if (m === 'POST' && p) {
        const cv = db().conversations.find((c) => c.id === p.id)
        if (!cv) fail(404, 'not_found', '会话不存在')
        const now = nowIso()
        cv.messages.push({
          id: nid('m'),
          role: 'user',
          status: 'complete',
          content: str(b.content),
          attachments: [],
          citations: [],
          toolTrace: [],
          finalThought: '',
          actions: [],
          sourceRevoked: false,
          error: null,
          createdAt: now,
          finishedAt: now,
        })
        const mid = nid('m')
        cv.messages.push({
          id: mid,
          role: 'assistant',
          status: 'queued',
          content: '',
          attachments: [],
          citations: [],
          toolTrace: [],
          finalThought: '',
          actions: [],
          sourceRevoked: false,
          error: null,
          createdAt: now,
          finishedAt: null,
        })
        cv.updatedAt = now
        if (!cv.title || cv.title === '新对话') cv.title = str(b.content).slice(0, 18) || cv.title
        return { messageId: mid, status: 'queued' }
      }
    }
    {
      const p = match('/agent/conversations/:id', path)
      if (p) {
        if (m === 'GET') {
          advanceAgent(p.id)
          const cv = db().conversations.find((c) => c.id === p.id)
          if (!cv) fail(404, 'not_found', '会话不存在')
          return cv
        }
        if (m === 'DELETE') {
          db().conversations = db().conversations.filter((c) => c.id !== p.id)
          return undefined
        }
      }
    }
    if (m === 'POST' && path === '/agent/attachments/presign') {
      const id = nid('att')
      db().pendingUploads[id] = { id, kind: 'agent', ownerId: id, filename: str(b.filename), mediaType: str(b.mediaType), sizeBytes: Number(b.sizeBytes) || 1 }
      return { attachmentId: id, uploadUrl: uploadUrl(), uploadFields: { key: id }, expiresIn: 600 }
    }
    {
      const p = match('/agent/attachments/:id/complete', path)
      if (m === 'POST' && p) {
        const pending = db().pendingUploads[p.id]
        return { id: p.id, filename: pending?.filename || 'image.png', mediaType: 'image/png', sizeBytes: Math.max(1, pending?.sizeBytes || 1) }
      }
    }
    {
      const p = match('/agent/attachments/:id/url', path)
      if (m === 'GET' && p) return { attachmentId: p.id, filename: 'image.png', mediaType: 'image/png', sizeBytes: 12, downloadUrl: uploadUrl(), expiresIn: 600 }
    }
    {
      const p = match('/agent/attachments/:id', path)
      if (m === 'DELETE' && p) return undefined
    }
    {
      const p = match('/agent/sources/:doc/:entry', path)
      if (m === 'GET' && p) {
        const d = db().knowledgeDocs.find((x) => x.id === p.doc) ?? db().knowledgeDocs[0]
        return { documentId: p.doc, entryId: p.entry, filename: d?.filename ?? 'doc', logicalPath: d?.logicalPath ?? '', locator: { page: 1 }, downloadUrl: '/favicon.svg', expiresIn: 600, content: d?.text ?? '', mediaType: 'text/plain' }
      }
    }
    {
      const p = match('/agent/actions/:id/prepare', path)
      if (m === 'POST' && p) {
        return {
          id: p.id,
          kind: 'submission_draft',
          status: 'prepared',
          title: '生成一条草稿',
          summary: '演示动作，确认后会落到提交页。',
          diff: [{ field: 'title', after: '演示草稿' }],
          citations: [],
          expiresAt: new Date(Date.now() + 600000).toISOString(),
          targetView: 'stuSubmit',
          createdAt: nowIso(),
          confirmToken: 'demo-confirm',
          confirmExpiresAt: new Date(Date.now() + 600000).toISOString(),
          risk: '只会生成草稿，不会直接提交。',
        }
      }
    }
    {
      const p = match('/agent/actions/:id/apply', path)
      if (m === 'POST' && p) {
        const u = requireUser(user)
        const row = createSubmission(u, { category: 'moral', itemKey: 'moral_cadre', title: 'Agent 演示草稿', claim: { option: '班长、团支书、辅导员助理、新生助导员', score: 8 } })
        return { id: p.id, kind: 'submission_draft', status: 'applied', navigateTo: 'stuSubmit', resourceId: row.id, draft: { id: row.id, status: 'draft', source: 'ai' } }
      }
    }
    {
      const p = match('/agent/actions/:id/reject', path)
      if (m === 'POST' && p) return undefined
    }
    {
      const p = match('/agent/messages/:id/cancel', path)
      if (m === 'POST' && p) return undefined
    }

    /* ---- 运维 ---- */
    if (m === 'GET' && path === '/ops/tenants') return { items: db().tenants }
    if (m === 'POST' && path === '/ops/tenants') {
      const id = nid('tn')
      const row = {
        id,
        name: str(b.name),
        slug: str(b.slug) || `class-${id}`,
        state: 'running' as const,
        archived: false,
        storageBytes: 0,
        storageCalibratedAt: nowIso(),
        admins: [{ sid: str(b.adminSid), name: str(b.adminName), registered: false }],
        createdAt: nowIso(),
        updatedAt: nowIso(),
      }
      db().tenants.push(row)
      return { id, name: row.name, slug: row.slug, admin: row.admins[0] }
    }
    {
      const p = match('/ops/tenants/:id/members', path)
      if (m === 'GET' && p) {
        return {
          items: db()
            .whitelist.map((w) => {
              const u = userBySid(w.sid)
              return {
                whitelistId: w.id,
                userId: u?.registered ? u.id : null,
                sid: w.sid,
                name: w.name,
                role: w.role,
                rosterActive: w.active,
                registered: w.registered,
                accountStatus: u?.status ?? null,
                registeredAt: w.registeredAt,
                lastLoginAt: u?.registered ? '2026-08-25T08:00:00.000Z' : null,
              }
            }),
        }
      }
    }
    {
      const p = match('/ops/tenants/:id/admin', path)
      if (m === 'PUT' && p) return { sid: str(b.sid), name: str(b.name), registered: true }
    }
    {
      const p = match('/ops/tenants/:id/storage/reconcile', path)
      if (m === 'POST' && p) return { id: p.id, storageBytes: 12_000_000, storageCalibratedAt: nowIso() }
    }
    {
      const p = match('/ops/tenants/:id', path)
      if (m === 'PUT' && p) {
        const t = db().tenants.find((x) => x.id === p.id)
        if (!t) fail(404, 'not_found', '班级不存在')
        if (b.archived === true) {
          t.archived = true
          t.state = 'archived'
        }
        if (b.archived === false) {
          t.archived = false
          t.state = 'running'
        }
        if (b.name) t.name = str(b.name)
        t.updatedAt = nowIso()
        return t
      }
    }
    if (m === 'GET' && path === '/ops/templates') {
      return { items: [{ id: 'tpl-1', name: db().scheme.schemeName, active: true, createdAt: '2026-07-01T00:00:00.000Z', retiredAt: null }] }
    }
    if (m === 'POST' && path === '/ops/templates') return { id: nid('tpl'), name: str(b.name), active: true, created: true, restored: false, updated: false }
    if (m === 'GET' && path === '/ops/template-share-requests') return { items: [] }
    if (m === 'GET' && path === '/ops/mail') return mockMailConfig()
    if (m === 'PUT' && path === '/ops/mail') return updateMockMail(b)
    if (m === 'POST' && path === '/ops/mail/test') {
      if (!mockMailConfig().configured) fail(422, 'mail_config_invalid', '请先完整配置并保存邮件通道')
      return { sent: true, messageId: nid('mail'), demo: true }
    }
    if (m === 'POST' && path === '/ops/mail/rotate-key') return resetMockMail()
    if (m === 'GET' && path === '/ops/mail/log') {
      return pageOf(
        [{ id: 'ml-1', tenantId: 'tn-demo', eventId: 'ev-1', recipientHash: 'abc123', provider: mockMailConfig().activeProvider, status: 'sent', attempt: 1, error: null, createdAt: nowIso() }],
        query,
      )
    }
    if (m === 'GET' && path === '/ops/agent') return agentConfig()
    if (m === 'PUT' && path === '/ops/agent') return undefined
    if (m === 'DELETE' && path === '/ops/agent/key') return undefined
    if (m === 'DELETE' && path === '/ops/agent') return undefined
    if (m === 'POST' && path === '/ops/agent/test') return { ok: true, slot: str(b.slot) || 'text', model: 'demo-text', durationMs: 120 }
    if (m === 'GET' && path === '/ops/agent/knowledge') return platformKnowledge()
    if (m === 'POST' && path === '/ops/agent/knowledge/documents/presign') {
      const id = nid('pk')
      return { documentId: id, uploadUrl: uploadUrl(), uploadFields: { key: id }, expiresIn: 600, limits: platformKnowledge().limits }
    }
    {
      const p = match('/ops/agent/knowledge/documents/:id', path)
      if (p) {
        if (m === 'GET') {
          const d = platformKnowledge().documents[0]
          return { ...d, id: p.id, entriesPreview: [{ id: 'pke-1', locator: { page: 1 }, startLine: 1, endLine: 10, text: '平台帮助文档演示正文。' }], downloadUrl: '/favicon.svg', downloadExpiresIn: 600, content: '平台帮助文档演示正文。' }
        }
        if (m === 'PUT' || m === 'DELETE' || m === 'POST') return undefined
      }
    }
    if (m === 'GET' && path === '/ops/agent/providers') {
      return { items: agentProviders() }
    }
    if (m === 'POST' && path === '/ops/agent/providers') return { id: nid('prov') }
    if (m === 'GET' && path === '/ops/agent/routes') {
      return agentRoutes()
    }
    if (m === 'GET' && path === '/ops/queues') {
      return {
        items: [
          { stream: 'notify', group: 'g1', length: 0, pending: 0, lag: 0, consumers: 1, lastDeliveredId: '0-0' },
          { stream: 'export', group: 'g1', length: 0, pending: 0, lag: 0, consumers: 1, lastDeliveredId: '0-0' },
        ],
        deadLetters: 0,
        outboxPending: 0,
      }
    }
    if (m === 'GET' && path === '/ops/queues/dead') return { items: [], nextCursor: null }
    if (m === 'POST' && path === '/ops/queues/dead/redeliver') return { redelivered: Array.isArray(b.ids) ? b.ids.length : 0 }
    if (m === 'GET' && path === '/ops/workers') {
      return {
        items: ['dispatch', 'notify', 'export', 'maintenance', 'backup', 'ai', 'agent'].map((name) => ({
          name,
          lastHeartbeat: nowIso(),
          heartbeatAlive: true,
          heartbeatStatus: 'alive',
        })),
      }
    }
    // Worker 只提供状态查询，未知操作不能落入通用写接口的成功兜底。
    if (path === '/ops/workers' || path.startsWith('/ops/workers/')) {
      fail(404, 'not_found', 'Worker 接口不存在')
    }
    if (m === 'GET' && path === '/ops/cron') {
      return {
        items: [
          { name: 'seal-remind', schedule: '0 9 * * *', handler: 'seal_remind', last: nowIso(), next: nowIso(), result: 'ok' },
          { name: 'backup', schedule: '0 2 * * *', handler: 'backup', last: nowIso(), result: 'ok' },
        ],
      }
    }
    if (m === 'GET' && path === '/ops/backups') {
      const remote = db().backupRemote
      const remoteReady = remote.enabled && remote.accessKeySet && remote.secretKeySet
      return {
        items: [
          {
            id: 'bk-remote-1', kind: 'remote_push', status: 'complete', offsite: true,
            detail: { sourceBackupId: 'bk-1', bucket: remote.bucket, archiveBytes: 9_400_000, verified: true },
            createdAt: '2026-08-25T02:05:00.000Z', finishedAt: '2026-08-25T02:11:00.000Z',
          },
          { id: 'bk-1', kind: 'backup', status: 'complete', offsite: false, detail: { objectCount: 42, objectBytes: 12_000_000 }, createdAt: '2026-08-25T02:00:00.000Z', finishedAt: '2026-08-25T02:03:00.000Z' },
        ],
        page: 1,
        pageSize: 20,
        total: 2,
        policy: {
          enabled: true, automatic: true, schedule: 'daily 02:00 Asia/Shanghai', retentionDays: 14, offsite: false,
          remote: {
            enabled: remoteReady, bucket: remote.bucket, retentionDays: remote.retentionDays,
            lastPushAt: remoteReady ? '2026-08-25T02:11:00.000Z' : null, lastPushOk: true,
          },
        },
      }
    }
    if (m === 'POST' && path === '/ops/backups') return { id: nid('bk'), status: 'queued' }
    if (m === 'POST' && path === '/ops/backups/restore-drill') return { id: nid('drill'), status: 'queued' }
    if (m === 'GET' && path === '/ops/backups/remote') {
      const { accessKeySet, secretKeySet, ...config } = db().backupRemote
      return { config, accessKeySet, secretKeySet, secretKeyReady: true, recipientReady: true }
    }
    if (m === 'PUT' && path === '/ops/backups/remote') {
      const remote = { ...db().backupRemote }
      for (const key of ['enabled', 'useSsl', 'pathStyle'] as const) {
        if (typeof b[key] === 'boolean') remote[key] = b[key]
      }
      for (const key of ['endpoint', 'bucket', 'region', 'prefix'] as const) {
        if (typeof b[key] === 'string') remote[key] = b[key].trim()
      }
      if (typeof b.retentionDays === 'number') {
        if (b.retentionDays < 1 || b.retentionDays > 3650) fail(422, 'backup_remote_invalid', '远程保留天数必须在 1—3650 之间')
        remote.retentionDays = b.retentionDays
      }
      /* 凭据只记"设没设过"，演示站从不保存值。空串表示清空。 */
      if (typeof b.accessKey === 'string') remote.accessKeySet = b.accessKey.trim() !== ''
      if (typeof b.secretKey === 'string') remote.secretKeySet = b.secretKey.trim() !== ''
      if (remote.enabled && !(remote.endpoint && remote.bucket && remote.accessKeySet && remote.secretKeySet)) {
        fail(422, 'backup_remote_invalid', '开启前要先把桶地址、桶名和两个凭据都填好')
      }
      db().backupRemote = remote
      persist()
      return undefined
    }
    if (m === 'DELETE' && path === '/ops/backups/remote') {
      db().backupRemote = {
        enabled: false, endpoint: '', bucket: '', region: '', prefix: '',
        useSsl: true, pathStyle: false, retentionDays: 90, accessKeySet: false, secretKeySet: false,
      }
      persist()
      return undefined
    }
    if (m === 'POST' && path === '/ops/backups/remote/test') {
      const remote = db().backupRemote
      if (!(remote.endpoint && remote.bucket && remote.accessKeySet && remote.secretKeySet)) {
        fail(409, 'backup_remote_incomplete', '远程备份桶还没配齐：地址、桶名和两个凭据都要填')
      }
      return { ok: true, wrote: true, read: true, removed: true, recipientReady: true }
    }
    if (m === 'POST' && path === '/ops/backups/remote/parse') return parseBucketUrl(str(b.url))
    if (m === 'GET' && path === '/ops/lifecycle') {
      return {
        policy: {
          backupEnabled: true,
          backupSchedule: '02:00',
          backupRetentionDays: 14,
          exportRetentionDays: 30,
          knowledgeDeleteGraceHours: 24,
          agentAttachmentGraceHours: 24,
          storageReconcileMinutes: 60,
          knowledgeMaxPdfPages: 80,
          knowledgeMaxArchiveMembers: 40,
          knowledgeMaxArchiveMb: 80,
          knowledgeMaxArchiveRatio: 12,
          knowledgeMaxArchiveDepth: 3,
          knowledgeMaxExtractedTextMb: 8,
          knowledgeConverterTimeoutSeconds: 60,
        },
      }
    }
    if (m === 'PUT' && path === '/ops/lifecycle') return { policy: b }
    if (m === 'GET' && path === '/ops/flags') {
      return { flags: { ...DEFAULT_OPS_FLAGS, ...db().flags }, locked: [], deploymentLimits: { apiRateLimitPerMinute: 180, apiRateLimitBurst: 60, passwordHashConcurrency: 4 } }
    }
    {
      const p = match('/ops/flags/:key', path)
      if (m === 'PUT' && p) {
        db().flags[p.key] = b.value as boolean | number | string[]
        return undefined
      }
    }
    if (m === 'GET' && path === '/ops/health') return opsHealth()
    if (m === 'GET' && path === '/ops/deploy') return opsDeploy()
    if (m === 'GET' && path === '/ops/audit') {
      return pageOf(
        db().audit.map((a) => ({ id: a.id, actor: a.actor ?? 'system', action: a.action, resourceType: a.resourceType, resourceId: a.resourceId, metadata: a.metadata, ip: a.ip, createdAt: a.createdAt })),
        query,
      )
    }

    /* 笔记图片、AI 批次等次要写接口：给一个能过的回执，避免页面红掉。 */
    if (path.includes('/presign') && m === 'POST') {
      const id = nid('up')
      return { evidenceId: id, assetId: id, documentId: id, attachmentId: id, uploadUrl: uploadUrl(), uploadFields: { key: id }, expiresIn: 600, method: 'POST', completeUrl: '', limits: knowledgeState().limits }
    }
    if (path.includes('/complete') && m === 'POST') return { evidenceId: nid('e'), assetId: nid('a'), status: 'ready', filename: 'file' }
    if (m === 'POST' && path === '/ai/batches') return { id: nid('ai'), status: 'uploading', expiresAt: nowIso(), limits: aiLimits() }

    if (m === 'GET') {
      if (path.endsWith('s') || path.includes('?')) return { items: [], page: 1, pageSize: 20, total: 0 }
      fail(404, 'not_found', `演示接口未覆盖：${m} ${path}`)
    }
    if (m === 'DELETE' || m === 'PUT' || m === 'POST') return { ok: true, id: nid('x') }
    fail(404, 'not_found', `演示接口未覆盖：${m} ${path}`)
  }

  try {
    const result = await run()
    if (m !== 'GET') persist()
    return result
  } catch (error) {
    persist()
    throw error
  }
}

function validateObjectionScore(row: Objection, score: number) {
  if (row.kind === 'penalty') {
    if (score > 0) fail(422, 'score_invalid', '扣分认定值不能大于零')
    return
  }
  if (row.kind === 'submission') {
    const sub = db().submissions.find((item) => item.id === row.targetId)
    if (!sub) fail(404, 'not_found', '提案的提交条目不存在')
    if (sub.studentId !== row.studentId) fail(403, 'target_mismatch', '提案对象与班级成员不一致')
    const bounds = scoreBounds(sub.ruleSnapshot.item.scoreRule)
    if (score < bounds.lo || score > bounds.hi) fail(422, 'score_invalid', '认定分不在小项允许的范围内')
    return
  }
  const item = db().scheme.categories.find((cat) => cat.key === row.category)?.baseItems.find((base) => base.key === row.itemKey)
  if (!item || score < 0 || score > item.full) fail(422, 'score_invalid', '基础分认定值不在允许范围内')
}

function applyObjectionDecision(row: Objection, action: string, score: number | null, reason: string) {
  row.status = action === 'dismiss' ? 'dismissed' : action === 'adjust' ? 'adjusted' : 'applied'
  row.decidedScore = score
  row.decisionReason = reason
  row.decidedAt = nowIso()
  if (score !== null) applyMockOutcome(row, score, '综测小组提案，经终裁认定')
}

/* 举报和提案落定时把结果写回计分卡。三种对象写法不同：
   扣分项累加（一学期可能扣好几次），基础项与已定分条目覆盖。 */
function applyMockOutcome(row: Pick<MockReport, 'kind' | 'targetId' | 'studentUserId' | 'category' | 'categoryName' | 'itemKey' | 'itemName'>, score: number, basis = '学生举报，经复核与终裁认定') {
  landMockScore(row, score, basis)
}

function myReportJSON(row: MockReport) {
  return {
    id: row.id,
    kind: row.kind,
    category: row.category,
    categoryName: row.categoryName,
    itemKey: row.itemKey,
    itemName: row.itemName,
    quantity: row.quantity,
    currentScore: row.currentScore,
    proposedScore: row.proposedScore,
    basis: row.basis,
    status: row.status,
    finalScore: row.finalScore,
    decisionReason: row.decisionReason,
    createdAt: row.createdAt,
    decidedAt: row.decidedAt,
    evidence: row.evidence,
    expectedReviews: row.reviews.length,
    decidedReviews: row.reviews.filter((r) => r.at).length,
  }
}
