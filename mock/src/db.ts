import type { MockGovernance } from './governance'
import schemeDoc from '../../examples/scoring-scheme.json'
import type { AgentConnection, AgentOperation } from '@/api/mcp'
import type {
  AdminSeal,
  AdminUser,
  AgentConversation,
  Appeal,
  AuditEntry,
  ClassResource,
  ClassificationSuggestion,
  DispatchState,
  Evidence,
  ExportJob,
  GpaRow,
  NamedReview,
  Objection,
  ReportTask,
  ReviewTask,
  SchemeDraft,
  Submission,
  BonusGrantBatch,
  Tenant,
  UserEmail,
  MailPreferences,
  WhitelistRow,
} from '@/api/types'
import type { HonorRoll, TimelineWindow } from '@/api/types'
import type { Capabilities, CategoryKey, Role, SchemeConfig, SchemeItem, User } from '@/lib/types'

/** 班级运行设置。真站里这是 class_timeline 一行，和方案版本互不相干。 */
export interface Timeline {
  window: TimelineWindow
  capabilities: Capabilities
  honorRoll: HonorRoll
  collegeName: string
  enrollmentClass: string
  academicYear: string
}
import { claimableItems } from '@/lib/schemeTree'
import { EXTRA_STUDENTS, buildReviewLoad, isOverdueSubmission } from './classLoad'
import { seedDeputyCases } from './deputySeed'

export const DEMO_PASSWORD = 'demodemo12'
export const DEMO_CODE = '123456'
export const CLASS_ID = 24
export const CLASS_NAME = '综合测评演示班'
export const WINDOW_OPEN = '2026-08-01T00:00:00+08:00'
export const WINDOW_CLOSE = '2026-10-15T23:59:59+08:00'

const STORE_KEY = 'easygpa.mock.store'
/* 演示数据版本升级时重建本地合成数据。 */
const STORE_VER = 30

export const TOUR_ID = 'u-wait'
export const TOUR_SID = '20240099'
export const TOUR_NAME = '周未注册'
export type TourCast = 'student' | 'group' | 'deputy' | 'class_admin'
export const TOUR_CASTS: { id: TourCast; label: string }[] = [
  { id: 'student', label: '学生' },
  { id: 'group', label: '综测小组' },
  { id: 'deputy', label: '副班管' },
  { id: 'class_admin', label: '班级管理员' },
]

export interface DemoUser extends User {
  id: string
  password: string
  status: 'active' | 'disabled'
  registered: boolean
  primaryEmail: string | null
  sessionVersion: number
}

export interface Assignment {
  id: string
  submissionId: string
  reviewerId: string
  decided: boolean
}

export interface PendingUpload {
  id: string
  kind: 'submission' | 'appeal' | 'note' | 'knowledge' | 'agent' | 'ai'
  ownerId: string
  filename: string
  mediaType: string
  sizeBytes: number
}

/* 演示站的举报。故意不带 reporter 字段：真实后端把举报人放在另一张只写不读的表里，
   mock 连字段都不给，任何人照着这个形状实现都不会顺手把举报人露出去。
   myReports 靠 mineIds 记住"本次演示里我提过哪几条"，刷新即失效，够演示用。 */
export interface MockReport {
  id: string
  kind: 'base' | 'penalty' | 'submission'
  studentUserId: string
  studentId: string
  student: string
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  targetId: string | null
  currentScore: number | null
  quantity: number | null
  proposedScore: number
  basis: string
  status: 'reviewing' | 'applied' | 'dismissed' | 'escalated' | 'final'
  finalScore: number | null
  decisionReason: string | null
  createdAt: string
  decidedAt: string | null
  reviewerIds: string[]
  reviews: { reviewerId: string; decision: 'uphold' | 'adjust' | 'reject' | null; score: number | null; reason: string | null; spentSeconds: number | null; at: string | null }[]
  /** 举报人附上的材料。真实库里这些行不带上传人，所以这里也没有"谁传的"这一栏。 */
  evidence: Evidence[]
  /** 只有"我"提的才进这个集合；演示用，刷新页面就没了。 */
  mine: boolean
}

export interface RecordedBase {
  userId: string
  category: CategoryKey
  categoryName: string
  itemKey: string
  itemName: string
  kind: 'base' | 'penalty'
  fullScore: number | null
  score: number
  basis: string
  recorded: boolean
  appealsUsed: number
  canAppeal: boolean
  updatedAt?: string
}

export interface Store {
  demoMode?: 'centralized' | 'collective'
  governance?: MockGovernance
  agentConnections?: Record<string, AgentConnection[]>
  agentOperations?: Record<string, AgentOperation[]>
  branding?: { svg: string; revision: string }
  v: number
  seq: number
  users: DemoUser[]
  emails: Record<string, UserEmail[]>
  mailPreferences?: Record<string, MailPreferences>
  whitelist: WhitelistRow[]
  scheme: SchemeConfig
  schemes: SchemeDraft[]
  timeline: Timeline
  submissions: Submission[]
  bonusGrants: (BonusGrantBatch & { request: string })[]
  evidence: Record<string, Evidence[]>
  reviews: Record<string, NamedReview[]>
  assignments: Assignment[]
  appeals: Appeal[]
  objections: Objection[]
  /* 学生匿名举报。演示站里同样不存举报人：种子数据和运行时都不带 reporter 字段，
     免得有人照着 mock 的形状去实现真接口。 */
  reports: MockReport[]
  classificationSuggestions: ClassificationSuggestion[]
  bases: RecordedBase[]
  resources: ClassResource[]
  knowledgeDocs: KnowledgeDoc[]
  gpa: GpaRow[]
  tenants: Tenant[]
  conversations: AgentConversation[]
  pendingUploads: Record<string, PendingUpload>
  audit: AuditEntry[]
  sealed: Record<string, { sealed: boolean; sealedAt: string | null; source: 'manual' | 'auto' | null }>
    confirmed: Record<string, { confirmed: boolean; confirmedAt: string | null; revision?: string; source?: 'manual' | 'scheduled' }>
  dispatch: { auto: boolean; seed: string; paused: Record<string, boolean> }
  gateForced: boolean
  exportJobs: ExportJob[]
  flags: Record<string, boolean | number | string[]>
  registerTickets: Record<string, { sid: string; expires: number }>
  resetTokens: Record<string, { sid: string; expires: number }>
  emailChallenges: Record<string, { userId: string; email: string; expires: number }>
  /* 录像号当前镜头。只改 token 上读到的身份，不改名单里那一行，
     所以班级成员页始终把周未注册显示为学生，班管任命也不会被切身份带跑。 */
  tourCast: TourCast
  /* 远程备份桶。演示站不连真桶，两个凭据只记"设没设过"，永远不存值。 */
  backupRemote: BackupRemoteConfig & { accessKeySet: boolean; secretKeySet: boolean }
}

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

export interface KnowledgeDoc {
  id: string
  filename: string
  logicalPath: string
  displayName: string
  mediaType: string
  sizeBytes: number
  status: 'ready'
  searchable: boolean
  extractor: string
  entries: number
  pageCount: number
  sheetCount: null
  warning: null
  error: null
  createdAt: string
  updatedAt: string
  publishedAt: string
  text: string
}

const capabilities = (): Capabilities => ({
  submit: true,
  edit: true,
  appeal: true,
  review: true,
  arbitrate: true,
  /* 演示站默认把学生举报打开，否则「全班扣分」进去就是一张空的关闭提示页，
     看不出这个功能长什么样。真实班级里它默认是关的。 */
  studentReport: true,
  export: { on: true, gate: 'settlement' },
})

/* 方案只有评分规则；窗口、开关、评优档位是班级的运行设置，见 timeline()。 */
export function currentScheme(): SchemeConfig {
  return {
    ...(schemeDoc as Omit<SchemeConfig, 'version'>),
    version: '2024.1',
    /* 学生端显示的是这个时间，不是版本号——真站里它来自 scheme.published_at。 */
    publishedAt: '2026-08-20T09:00:00.000Z',
  }
}

/* 班级时间线。演示站不设全系统封锁——默认就把这个班冻住的话，
   点进去每一个业务页面都是 409，看不出系统平时长什么样。
   档位默认三档，和后端 scheme.DefaultAwards() 一致。 */
export function timeline(): Timeline {
  return {
    collegeName: '示例学院',
    enrollmentClass: '24级计算机科学与技术演示班',
    academicYear: '2025-2026',
    window: { open: WINDOW_OPEN, close: WINDOW_CLOSE, lockdown: null },
    capabilities: capabilities(),
    honorRoll: {
      topPercent: 30,
      awards: [
        { name: '一等综合素质奖学金', topPercent: 5 },
        { name: '二等综合素质奖学金', topPercent: 10 },
        { name: '三等综合素质奖学金', topPercent: 15 },
      ],
    },
  }
}

export function nowIso() {
  return new Date().toISOString()
}

export function publicUser(u: DemoUser): User {
  return {
    sid: u.sid,
    name: u.name,
    role: u.role,
    initial: u.initial,
    sub: u.sub,
    classId: u.classId,
    className: u.className,
    isDeputy: Boolean(u.isDeputy),
  }
}

export function findItem(scheme: SchemeConfig, category: CategoryKey, itemKey: string): { category: SchemeConfig['categories'][number]; item: SchemeItem } | null {
  const cat = scheme.categories.find((c) => c.key === category)
  if (!cat) return null
  const item = claimableItems(cat).find((i) => i.key === itemKey)
  return item ? { category: cat, item } : null
}

export function wantScore(item: SchemeItem, claim: { quantity?: number; option?: string; score?: number }): number | null {
  const r = item.scoreRule
  if (r.type === 'per_unit') {
    if (claim.quantity == null || Number.isNaN(claim.quantity)) return null
    const raw = claim.quantity * r.per
    return r.cap != null ? Math.min(raw, r.cap) : raw
  }
  if (r.type === 'enum') {
    if (typeof claim.score === 'number' && !Number.isNaN(claim.score)) return claim.score
    const o = r.options.find((x) => x.label === claim.option)
    return o ? o.score : null
  }
  if (r.type === 'threshold') {
    if (claim.quantity == null) return null
    return claim.quantity >= r.minimum ? r.award : 0
  }
  if (typeof claim.score === 'number' && !Number.isNaN(claim.score)) return Math.min(Math.max(claim.score, r.min), r.max)
  return null
}

export function snapshot(scheme: SchemeConfig, category: CategoryKey, itemKey: string) {
  const found = findItem(scheme, category, itemKey)
  if (!found) throw new Error(`unknown item ${category}/${itemKey}`)
  return {
    schemeId: 'sch-pub',
    version: scheme.version,
    categoryKey: category,
    categoryName: found.category.name,
    item: found.item,
    capturedAt: '2026-08-20T09:00:00.000Z',
  }
}

function person(id: string, sid: string, name: string, role: Role, sub: string, email: string | null, extra?: Partial<DemoUser>): DemoUser {
  return {
    id,
    sid,
    name,
    role,
    initial: name.slice(0, 1),
    sub,
    classId: CLASS_ID,
    className: CLASS_NAME,
    password: DEMO_PASSWORD,
    status: 'active',
    registered: true,
    primaryEmail: email,
    sessionVersion: 0,
    isDeputy: false,
    ...extra,
  }
}

function evidenceMediaType(name: string): string {
  const ext = name.slice(name.lastIndexOf('.') + 1).toLowerCase()
  const byExt: Record<string, string> = {
    png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', webp: 'image/webp', gif: 'image/gif',
    pdf: 'application/pdf',
    doc: 'application/msword',
    docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    xls: 'application/vnd.ms-excel',
    xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    ppt: 'application/vnd.ms-powerpoint',
    pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
    zip: 'application/zip', rar: 'application/vnd.rar', '7z': 'application/x-7z-compressed',
    txt: 'text/plain', csv: 'text/csv',
  }
  return byExt[ext] ?? 'application/octet-stream'
}

function seed(): Store {
  const scheme = currentScheme()
  const t = '2026-08-20T09:00:00.000Z'
  const users: DemoUser[] = [
    person('u-chen', '20240001', '陈屿', 'student', '学生 · 班长', 'chenyu@demo.local'),
    person('u-li', '20240002', '李审核', 'group', '综测小组', 'lishenhe@demo.local', { isDeputy: true }),
    person('u-wang', '20240003', '王管理', 'class_admin', '班级管理员', 'wangguanli@demo.local'),
    person('u-ops', 'ops@demo.local', '运维', 'ops', '运维超管', 'ops@demo.local', { className: '平台运维', classId: 0, sid: 'ops@demo.local' }),
    person('u-zhao', '20240004', '赵同学', 'student', '学生', null),
    person('u-sun', '20240005', '孙同学', 'student', '学生', null),
    person('u-zhou', '20240006', '周复评', 'group', '综测小组', null),
    person(TOUR_ID, TOUR_SID, TOUR_NAME, 'student', '学生', null, { registered: false, password: '' }),
    ...EXTRA_STUDENTS.map((s) => person(`u-x-${s.sid}`, s.sid, s.name, 'student', '学生', null)),
  ]

  const cadreSnap = snapshot(scheme, 'moral', 'moral_cadre')
  const dormSnap = snapshot(scheme, 'moral', 'moral_dorm')
  const bloodSnap = snapshot(scheme, 'moral', 'moral_blood')
  const practiceSnap = snapshot(scheme, 'practice', 'practice_base')
  const contestSnap = snapshot(scheme, 'health', 'health_contest')
  const cetSnap = snapshot(scheme, 'practice', 'practice_cet')
  /* 赵同学这一条是「提交后方案改过」的演示：快照里冻结的是修订前的档位
     （校级·二等 8 分、校级·三等 6 分），现行方案已经调成 6 分和 4 分。
     审核台会并排显示两边，提醒审核人按冻结的那份定分。
     没有这样一条的话，规则漂移的提示在演示站里永远不出现。 */
  const zhaoContest = (() => {
    const snap = snapshot(scheme, 'practice', 'practice_contest')
    const rule = snap.item.scoreRule
    if (rule.type !== 'enum') return snap
    const older = rule.options.map((option) =>
      option.label === '校级·二等' ? { ...option, score: 8 }
      : option.label === '校级·三等' ? { ...option, score: 6 }
      : option,
    )
    return { ...snap, item: { ...snap.item, scoreRule: { ...rule, options: older } } }
  })()

  const submissions: Submission[] = [
    {
      id: 's-cadre',
      student: '陈屿',
      studentId: '20240001',
      category: 'moral',
      categoryName: '思想道德素质',
      itemKey: 'moral_cadre',
      itemName: cadreSnap.item.name,
      title: '担任 演示班班长',
      claim: { option: '班长、团支书、辅导员助理、新生助导员', score: 8 },
      wantScore: 8,
      finalScore: null,
      status: 'pending',
      submittedAt: '2026-08-18T08:00:00.000Z',
      ruleSnapshot: cadreSnap,
      evidenceCount: 1,
      note: '本学年担任班长，组织过两次班会与一次志愿活动。',
      source: 'manual',
      appealsUsed: 0,
      canAppeal: false,
      updatedAt: t,
    },
    {
      id: 's-dorm',
      student: '陈屿',
      studentId: '20240001',
      category: 'moral',
      categoryName: '思想道德素质',
      itemKey: 'moral_dorm',
      itemName: dormSnap.item.name,
      title: '院级文明寝室',
      claim: { option: '院级文明寝室', score: 2 },
      wantScore: 2,
      finalScore: 2,
      status: 'scored',
      submittedAt: '2026-08-12T08:00:00.000Z',
      ruleSnapshot: dormSnap,
      evidenceCount: 1,
      source: 'manual',
      appealsUsed: 1,
      /* 这一条上还有一次没结案的申诉，结案前不能再发起——后端也是这个口径。 */
      canAppeal: false,
      updatedAt: t,
    },
    {
      id: 's-blood',
      student: '陈屿',
      studentId: '20240001',
      category: 'moral',
      categoryName: '思想道德素质',
      itemKey: 'moral_blood',
      itemName: bloodSnap.item.name,
      title: '无偿献血一次',
      claim: { quantity: 1 },
      wantScore: 2,
      finalScore: null,
      status: 'draft',
      submittedAt: null,
      ruleSnapshot: bloodSnap,
      evidenceCount: 0,
      source: 'manual',
      appealsUsed: 0,
      canAppeal: false,
      updatedAt: t,
    },
    {
      id: 's-practice',
      student: '陈屿',
      studentId: '20240001',
      category: 'practice',
      categoryName: '实践创新素质',
      itemKey: 'practice_base',
      itemName: practiceSnap.item.name,
      title: '蓝桥杯省赛 + 学院科创立项',
      claim: { score: 32 },
      wantScore: 32,
      finalScore: 30,
      status: 'arbitrating',
      submittedAt: '2026-08-10T08:00:00.000Z',
      ruleSnapshot: practiceSnap,
      evidenceCount: 1,
      note: '两项均有证书。',
      source: 'manual',
      appealsUsed: 0,
      canAppeal: false,
      updatedAt: t,
    },
    {
      id: 's-art',
      student: '陈屿',
      studentId: '20240001',
      category: 'health',
      categoryName: '身体心理素质',
      itemKey: 'health_contest',
      itemName: contestSnap.item.name,
      title: '校运会 4×100 第四名',
      claim: { option: '校级·第四名', score: 5 },
      wantScore: 5,
      finalScore: 5,
      status: 'appealing',
      submittedAt: '2026-08-11T08:00:00.000Z',
      ruleSnapshot: contestSnap,
      evidenceCount: 1,
      source: 'manual',
      /* 一轮结案 + 二轮待终裁：两次机会都用掉了，不该再出现在「还能申诉」里。 */
      appealsUsed: 2,
      canAppeal: false,
      updatedAt: t,
    },
    {
      /* 一轮已结案、还剩一次机会的那一笔：详情页「对这一笔再申诉一次」靠它演示。 */
      id: 's-chen-vol',
      student: '陈屿',
      studentId: '20240001',
      category: 'moral',
      categoryName: '思想道德素质',
      itemKey: 'moral_volunteer',
      itemName: snapshot(scheme, 'moral', 'moral_volunteer').item.name,
      title: '校运会志愿者两天',
      claim: { quantity: 8 },
      wantScore: 2,
      finalScore: 1,
      status: 'scored',
      submittedAt: '2026-08-13T08:00:00.000Z',
      ruleSnapshot: snapshot(scheme, 'moral', 'moral_volunteer'),
      evidenceCount: 1,
      note: '检录处两天共 8 小时，签到表两页都在附件里。',
      source: 'manual',
      appealsUsed: 1,
      canAppeal: true,
      updatedAt: t,
    },
    {
      /* 录像号学生镜头：定分、尚未申诉，用来录一次申诉。 */
      id: 's-wait-cet',
      student: TOUR_NAME,
      studentId: TOUR_SID,
      category: 'practice',
      categoryName: '实践创新素质',
      itemKey: 'practice_cet',
      itemName: cetSnap.item.name,
      title: '大学英语四级',
      claim: { option: '四级', score: 5 },
      wantScore: 5,
      finalScore: 0,
      status: 'scored',
      submittedAt: '2026-08-14T08:00:00.000Z',
      ruleSnapshot: cetSnap,
      evidenceCount: 1,
      note: '成绩单已上传。',
      source: 'manual',
      appealsUsed: 0,
      canAppeal: true,
      updatedAt: t,
    },
    {
      /* 录像号学生镜头：一轮已结案、还剩一次机会，用来录二次申诉。 */
      id: 's-wait-vol',
      student: TOUR_NAME,
      studentId: TOUR_SID,
      category: 'moral',
      categoryName: '思想道德素质',
      itemKey: 'moral_volunteer',
      itemName: snapshot(scheme, 'moral', 'moral_volunteer').item.name,
      title: '校运会志愿者两天',
      claim: { quantity: 8 },
      wantScore: 2,
      finalScore: 1,
      status: 'scored',
      submittedAt: '2026-08-13T08:00:00.000Z',
      ruleSnapshot: snapshot(scheme, 'moral', 'moral_volunteer'),
      evidenceCount: 1,
      note: '检录处两天共 8 小时，签到表两页都在附件里。',
      source: 'manual',
      appealsUsed: 1,
      canAppeal: true,
      updatedAt: t,
    },
    {
      id: 's-zhao',
      student: '赵同学',
      studentId: '20240004',
      category: 'practice',
      categoryName: '实践创新素质',
      itemKey: 'practice_contest',
      itemName: zhaoContest.item.name,
      title: '数学建模校赛二等奖',
      /* 档位写死成校级二等，别取 options[0]：那一档现在是「国家级 A1 类·特等」，
         和标题、期望分都对不上，审核台会显示成一条自相矛盾的申报。 */
      claim: { option: '校级·二等', score: 6 },
      wantScore: 6,
      finalScore: null,
      status: 'pending',
      submittedAt: '2026-08-19T08:00:00.000Z',
      ruleSnapshot: zhaoContest,
      evidenceCount: 1,
      source: 'manual',
      appealsUsed: 0,
      canAppeal: false,
      updatedAt: t,
    },
  ]

  const load = buildReviewLoad({
    scheme,
    snapshot,
    wantScore,
    students: EXTRA_STUDENTS.map((s) => ({ id: `u-x-${s.sid}`, sid: s.sid, name: s.name })),
  })
  /* 让初审队列里第一条学科竞赛带上「提交后方案改过」：把它的快照冻结成修订前的
     档位（校级二等 8 分、三等 6 分），现行方案已是 6 分和 4 分。审核台会并排显示
     两边，提醒审核人按冻结的那份定分。不做这一步的话，队列里每条快照都取自当前
     方案，规则漂移的提示在演示站里永远不出现。 */
  const AGED = new Map([['国家级 A1 类·二等', 25], ['国家级 A1 类·三等', 18], ['校级·二等', 8]])
  for (const row of load.submissions) {
    // 按标题挑，别按 itemKey 挑第一条：队列顺序和 submissions 数组顺序不是一回事，
    // 按 key 取到的那条会排到队列很后面，演示时翻十几页才看得到。
    if (!row.title.includes('ICPC')) continue
    const rule = row.ruleSnapshot.item.scoreRule
    if (rule.type !== 'enum') continue
    row.ruleSnapshot = {
      ...row.ruleSnapshot,
      item: {
        ...row.ruleSnapshot.item,
        scoreRule: {
          ...rule,
          options: rule.options.map((option) =>
            AGED.has(option.label) ? { ...option, score: AGED.get(option.label)! } : option,
          ),
        },
      },
    }
  }
  submissions.push(...load.submissions)

  /* 类型按扩展名认：EvidenceFiles 靠 mediaType 决定出缩略图还是出图标，
     一律写成 image/png 的话，PDF 和表格会被当成图片去渲染，列表里就是一排裂图。 */
  const ev = (id: string, name: string): Evidence => ({
    id,
    name,
    mediaType: evidenceMediaType(name),
    sizeBytes: 120_000,
    sha256: null,
    status: 'ready',
    uploadedAt: t,
  })

  const whitelist: WhitelistRow[] = users
    .filter((u) => u.role !== 'ops')
    .map((u) => ({
      id: `w-${u.id}`,
      sid: u.sid,
      name: u.name,
      role: u.role as Exclude<Role, 'ops'>,
      active: true,
      registered: u.registered,
      registeredAt: u.registered ? '2026-08-01T00:00:00.000Z' : null,
      createdAt: '2026-07-01T00:00:00.000Z',
    }))

  const bases: RecordedBase[] = []
  for (const u of users.filter((x) => x.role !== 'ops' && x.registered)) {
    for (const cat of scheme.categories) {
      for (const b of cat.baseItems) {
        if (b.studentClaim) continue
        bases.push({
          userId: u.id,
          category: cat.key,
          categoryName: cat.name,
          itemKey: b.key,
          itemName: b.name,
          kind: 'base',
          fullScore: b.full,
          score: b.full,
          basis: '方案默认满分，尚未人工调整。',
          recorded: false,
          appealsUsed: 0,
          canAppeal: true,
        })
      }
    }
  }

  const chenAbsence: RecordedBase = {
    userId: 'u-chen',
    category: 'moral',
    categoryName: '思想道德素质',
    itemKey: 'moral_pen_absence',
    itemName: '考勤缺勤（每次；迟到早退累计 2 次记 1 次）',
    kind: 'penalty',
    fullScore: -1,
    score: -1,
    basis: '第 3 周周一上午迟到两次，记缺勤 1 次。',
    recorded: true,
    appealsUsed: 0,
    canAppeal: false,
    updatedAt: t,
  }
  bases.push(chenAbsence)

  const store: Store = {
    v: STORE_VER,
    seq: 200,
    users,
    emails: {
      'u-chen': [{ id: 'em-1', email: 'chenyu@demo.local', primary: true, verified: true, verifiedAt: t, createdAt: t }],
      'u-li': [{ id: 'em-2', email: 'lishenhe@demo.local', primary: true, verified: true, verifiedAt: t, createdAt: t }],
      'u-wang': [{ id: 'em-3', email: 'wangguanli@demo.local', primary: true, verified: true, verifiedAt: t, createdAt: t }],
      'u-ops': [{ id: 'em-4', email: 'ops@demo.local', primary: true, verified: true, verifiedAt: t, createdAt: t }],
    },
    whitelist,
    scheme,
    schemes: [
      {
        id: 'sch-pub',
        name: scheme.schemeName,
        version: 1,
        status: 'published',
        config: scheme,
        lockVersion: 4,
      },
      {
        id: 'sch-draft',
        name: `${scheme.schemeName}（下一稿）`,
        version: null,
        status: 'draft',
        config: scheme,
        lockVersion: 1,
      },
    ],
    timeline: timeline(),
    submissions,
    bonusGrants: [],
    evidence: {
      's-cadre': [ev('e-cadre', '班长任命文件.png')],
      's-dorm': [ev('e-dorm', '文明寝室公示.png')],
      's-practice': [ev('e-practice', '蓝桥杯证书.png')],
      's-art': [ev('e-art', '校运会秩序册.png')],
      's-zhao': [ev('e-zhao', '数模获奖名单.png')],
      /* 陈屿那几条申诉补传的原件：申诉详情里的「补传佐证」读的就是这里。 */
      'ap-1': [ev('e-ap-1', '校级文明寝室公示原件.png')],
      'ap-chen-2': [ev('e-ap-chen-2', '大创立项批文.png')],
      'ap-chen-3': [ev('e-ap-chen-3', '体育部盖章参赛名单.png')],
      'ap-chen-4': [ev('e-ap-chen-4', '上学期文明寝室公示.png')],
      'ap-chen-5': [ev('e-ap-chen-5', '两天的签到表.png')],
      's-chen-vol': [ev('e-chen-vol', '校运会检录签到表.png')],
      's-wait-cet': [ev('e-wait-cet', '大学英语四级成绩单.png')],
      's-wait-vol': [ev('e-wait-vol', '校运会检录签到表.png')],
      'ap-wait-vol': [ev('e-ap-wait-vol', '两天的签到表.png')],
      'ap-pen-1': [ev('e-ap-pen-1', '辅导员签字的请假条.png')],
      ...load.evidence,
    },
    reviews: {
      's-dorm': [
        { id: 'rv-d1', reviewer: '李审核', reviewerSid: '20240002', reviewerId: 'u-li', decision: 'accepted', score: 2, reason: '公示名单属实。', spentSeconds: 80, at: t },
        { id: 'rv-d2', reviewer: '周复评', reviewerSid: '20240006', reviewerId: 'u-zhou', decision: 'accepted', score: 2, reason: '同意。', spentSeconds: 40, at: t },
      ],
      's-practice': [
        { id: 'rv-p1', reviewer: '李审核', reviewerSid: '20240002', reviewerId: 'u-li', decision: 'adjusted', score: 30, reason: '两项材料齐全，按学院口径略作调整。', spentSeconds: 200, at: t },
        { id: 'rv-p2', reviewer: '周复评', reviewerSid: '20240006', reviewerId: 'u-zhou', decision: 'adjusted', score: 30, reason: '同意 30 分。', spentSeconds: 120, at: t },
      ],
      's-art': [
        { id: 'rv-a1', reviewer: '李审核', reviewerSid: '20240002', reviewerId: 'u-li', decision: 'accepted', score: 5, reason: '秩序册名次清楚。', spentSeconds: 70, at: t },
        { id: 'rv-a2', reviewer: '周复评', reviewerSid: '20240006', reviewerId: 'u-zhou', decision: 'accepted', score: 5, reason: '同意。', spentSeconds: 50, at: t },
      ],
      's-chen-vol': [
        { id: 'rv-vol1', reviewer: '李审核', reviewerSid: '20240002', reviewerId: 'u-li', decision: 'adjusted', score: 1, reason: '两页签到覆盖同一场活动的上下午，按 4 小时认定。', spentSeconds: 110, at: t },
        { id: 'rv-vol2', reviewer: '周复评', reviewerSid: '20240006', reviewerId: 'u-zhou', decision: 'adjusted', score: 1, reason: '同意。', spentSeconds: 60, at: t },
      ],
      's-wait-cet': [
        { id: 'rv-wait-cet1', reviewer: '李审核', reviewerSid: '20240002', reviewerId: 'u-li', decision: 'rejected', score: 0, reason: '成绩单落款为上一学年，本学年不予认定。', spentSeconds: 90, at: t },
        { id: 'rv-wait-cet2', reviewer: '周复评', reviewerSid: '20240006', reviewerId: 'u-zhou', decision: 'rejected', score: 0, reason: '按首次通过学年认定，同意。', spentSeconds: 50, at: t },
      ],
      's-wait-vol': [
        { id: 'rv-wait-vol1', reviewer: '李审核', reviewerSid: '20240002', reviewerId: 'u-li', decision: 'adjusted', score: 1, reason: '两页签到覆盖同一场活动的上下午，按 4 小时认定。', spentSeconds: 110, at: t },
        { id: 'rv-wait-vol2', reviewer: '周复评', reviewerSid: '20240006', reviewerId: 'u-zhou', decision: 'adjusted', score: 1, reason: '同意。', spentSeconds: 60, at: t },
      ],
      ...load.reviews,
    },
    assignments: [
      { id: '4001', submissionId: 's-cadre', reviewerId: 'u-li', decided: false },
      { id: '4002', submissionId: 's-cadre', reviewerId: 'u-zhou', decided: false },
      { id: '4003', submissionId: 's-zhao', reviewerId: 'u-li', decided: false },
      { id: '4004', submissionId: 's-zhao', reviewerId: 'u-zhou', decided: false },
      ...load.assignments,
    ],
    appeals: [
      /* 一条仍待处理的重复材料申诉。 */
      {
        id: 'oap-20240021',
        targetType: 'submission',
        targetId: 'ba-s-20240021-03',
        target: '大学英语四级（下学期）',
        category: 'practice',
        itemKey: 'practice_cet',
        student: '蒋默',
        studentId: '20240021',
        reason: '审核意见说我这条和上面那条是同一场活动，可我报的是承办和参赛两件事，请重新看一下秩序册第 4 页。',
        round: 1,
        status: 'escalated',
        baselineScore: 4,
        currentScore: 4,
        proposedScore: 6,
        originalCategory: 'practice',
        originalItemKey: 'practice_cet',
        proposedCategory: 'practice',
        proposedItemKey: 'practice_cet',
        resolutionScore: null,
        resolutionReason: null,
        originBatchId: 'bab-2026-08',
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: true, mine: false, rereview: { decision: 'uphold', score: 4, category: 'practice', itemKey: 'practice_cet', reason: '秩序册第 4 页只列了承办单位，看不出本人另外参赛，维持。', spentSeconds: 180, at: '2026-08-27T02:00:00.000Z' } },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: true, mine: false, rereview: { decision: 'adjust', score: 6, category: 'practice', itemKey: 'practice_cet', reason: '参赛名单里确实有本人，和承办不是一件事，建议分开算。', spentSeconds: 220, at: '2026-08-27T06:00:00.000Z' } },
        ],
        createdAt: '2026-08-26T02:00:00.000Z',
        updatedAt: '2026-08-27T06:00:00.000Z',
        resolvedAt: null,
      },
      {
        id: 'ap-1',
        targetType: 'submission',
        targetId: 's-dorm',
        target: '院级文明寝室',
        category: 'moral',
        itemKey: 'moral_dorm',
        student: '陈屿',
        studentId: '20240001',
        reason: '公示为校级文明寝室，申请按校级档计 3 分。\n\n补传的是学工处那份公示原件，上面写的是校级。',
        round: 1,
        status: 'reviewing',
        baselineScore: 2,
        currentScore: 2,
        proposedScore: 3,
        originalCategory: 'moral',
        originalItemKey: 'moral_dorm',
        proposedCategory: 'moral',
        proposedItemKey: 'moral_dorm',
        resolutionScore: null,
        resolutionReason: null,
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: false, mine: false, rereview: null },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: false, mine: false, rereview: null },
        ],
        createdAt: '2026-08-22T08:00:00.000Z',
        updatedAt: t,
        resolvedAt: null,
      },
      /* 「我的申诉」是学生演示账号（陈屿）的主场：六条覆盖全部形态——
         复评中 / 已升终裁 / 第 2 次申诉带第一轮回放 / 复评结案 / 已终裁 / 扣分项。
         少了任何一种，那一页的对应分支就没东西可看。 */
      {
        id: 'ap-chen-2',
        targetType: 'submission',
        targetId: 's-practice',
        target: '蓝桥杯省赛 + 学院科创立项',
        category: 'practice',
        itemKey: 'practice_base',
        student: '陈屿',
        studentId: '20240001',
        reason: '两项材料都交齐了，按方案「至少 2 项」应当拿满 40 分，现在只给了 30。\n\n补传的是大创立项批文，上面有项目编号。',
        round: 1,
        status: 'escalated',
        baselineScore: 30,
        currentScore: 30,
        proposedScore: 40,
        originalCategory: 'practice',
        originalItemKey: 'practice_base',
        proposedCategory: 'practice',
        proposedItemKey: 'practice_base',
        resolutionScore: null,
        resolutionReason: null,
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: true, mine: false, rereview: { decision: 'uphold', score: 30, category: 'practice', itemKey: 'practice_base', reason: '大创立项还在立项阶段，没有结项材料，按未完全达标认定。', spentSeconds: 210, at: '2026-08-24T06:00:00.000Z' } },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: true, mine: false, rereview: { decision: 'adjust', score: 40, category: 'practice', itemKey: 'practice_base', reason: '方案写的是「参加」，立项批文已经能证明参加，建议给满。', spentSeconds: 260, at: '2026-08-24T08:00:00.000Z' } },
        ],
        createdAt: '2026-08-23T01:00:00.000Z',
        updatedAt: '2026-08-24T08:00:00.000Z',
        resolvedAt: null,
      },
      {
        id: 'ap-chen-3-r1',
        targetType: 'submission',
        targetId: 's-art',
        target: '校运会 4×100 第四名',
        category: 'health',
        itemKey: 'health_contest',
        student: '陈屿',
        studentId: '20240001',
        reason: '第一次申诉：集体项目我是主力棒，不应该按减半算。',
        round: 1,
        status: 'resolved',
        baselineScore: 5,
        currentScore: 5,
        proposedScore: 8,
        originalCategory: 'health',
        originalItemKey: 'health_contest',
        proposedCategory: 'health',
        proposedItemKey: 'health_contest',
        resolutionScore: 5,
        resolutionReason: '秩序册上没有标注主力与替补，两位复评人一致维持原判。',
        resolutionCategory: 'health',
        resolutionItemKey: 'health_contest',
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: true, mine: false, rereview: { decision: 'uphold', score: 5, category: 'health', itemKey: 'health_contest', reason: '秩序册看不出主力身份，维持。', spentSeconds: 140, at: '2026-08-18T07:00:00.000Z' } },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: true, mine: false, rereview: { decision: 'uphold', score: 5, category: 'health', itemKey: 'health_contest', reason: '同意维持。', spentSeconds: 95, at: '2026-08-18T09:00:00.000Z' } },
        ],
        createdAt: '2026-08-17T02:00:00.000Z',
        updatedAt: '2026-08-18T09:00:00.000Z',
        resolvedAt: '2026-08-18T09:00:00.000Z',
      },
      {
        id: 'ap-chen-3',
        targetType: 'submission',
        targetId: 's-art',
        target: '校运会 4×100 第四名',
        category: 'health',
        itemKey: 'health_contest',
        student: '陈屿',
        studentId: '20240001',
        reason: '补交了体育部盖章的参赛名单，上面写明我跑第四棒，是主力。这是最后一次机会，请班级管理员核。',
        round: 2,
        status: 'escalated',
        baselineScore: 5,
        currentScore: 5,
        proposedScore: 8,
        originalCategory: 'health',
        originalItemKey: 'health_contest',
        proposedCategory: 'health',
        proposedItemKey: 'health_contest',
        resolutionScore: null,
        resolutionReason: null,
        previousAppealId: 'ap-chen-3-r1',
        handlers: [],
        createdAt: '2026-08-25T02:00:00.000Z',
        updatedAt: '2026-08-25T02:00:00.000Z',
        resolvedAt: null,
      },
      {
        id: 'ap-chen-4',
        targetType: 'submission',
        targetId: 's-dorm',
        target: '院级文明寝室（上学期）',
        category: 'moral',
        itemKey: 'moral_dorm',
        student: '陈屿',
        studentId: '20240001',
        reason: '上学期那次评的是校级，希望按校级档补差。',
        round: 1,
        status: 'final',
        baselineScore: 2,
        currentScore: 3,
        proposedScore: 3,
        originalCategory: 'moral',
        originalItemKey: 'moral_dorm',
        proposedCategory: 'moral',
        proposedItemKey: 'moral_dorm',
        resolutionScore: 3,
        resolutionReason: '学工处公示原件确为校级，按校级档改判为 3 分。两次机会到此用完。',
        resolutionCategory: 'moral',
        resolutionItemKey: 'moral_dorm',
        handler: '王管理',
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: true, mine: false, rereview: { decision: 'adjust', score: 3, category: 'moral', itemKey: 'moral_dorm', reason: '原件能对上，同意改判。', spentSeconds: 120, at: '2026-08-16T07:00:00.000Z' } },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: true, mine: false, rereview: { decision: 'uphold', score: 2, category: 'moral', itemKey: 'moral_dorm', reason: '公示名单上查不到这个寝室号。', spentSeconds: 160, at: '2026-08-16T09:00:00.000Z' } },
        ],
        createdAt: '2026-08-15T02:00:00.000Z',
        updatedAt: '2026-08-17T03:00:00.000Z',
        resolvedAt: '2026-08-17T03:00:00.000Z',
      },
      {
        id: 'ap-chen-5',
        targetType: 'submission',
        targetId: 's-chen-vol',
        target: '校运会志愿者两天',
        category: 'moral',
        itemKey: 'moral_volunteer',
        student: '陈屿',
        studentId: '20240001',
        reason: '签到表是两天两页，两天各 4 小时应该按 8 小时算，现在只认了 4 小时。',
        round: 1,
        status: 'resolved',
        baselineScore: 1,
        currentScore: 1,
        proposedScore: 2,
        originalCategory: 'moral',
        originalItemKey: 'moral_volunteer',
        proposedCategory: 'moral',
        proposedItemKey: 'moral_volunteer',
        resolutionScore: 1,
        resolutionReason: '两页签到表覆盖的是同一场活动的上下午，扣掉重叠时段后实际服务时长按 4 小时认定。',
        resolutionCategory: 'moral',
        resolutionItemKey: 'moral_volunteer',
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: true, mine: false, rereview: { decision: 'uphold', score: 1, category: 'moral', itemKey: 'moral_volunteer', reason: '上下午时段重叠，维持 4 小时。', spentSeconds: 130, at: '2026-08-19T07:00:00.000Z' } },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: true, mine: false, rereview: { decision: 'uphold', score: 1, category: 'moral', itemKey: 'moral_volunteer', reason: '同意。', spentSeconds: 80, at: '2026-08-19T09:00:00.000Z' } },
        ],
        createdAt: '2026-08-18T02:00:00.000Z',
        updatedAt: '2026-08-19T09:00:00.000Z',
        resolvedAt: '2026-08-19T09:00:00.000Z',
      },
      {
        id: 'ap-wait-vol',
        targetType: 'submission',
        targetId: 's-wait-vol',
        target: '校运会志愿者两天',
        category: 'moral',
        itemKey: 'moral_volunteer',
        student: TOUR_NAME,
        studentId: TOUR_SID,
        reason: '签到表是两天两页，两天各 4 小时应该按 8 小时算，现在只认了 4 小时。',
        round: 1,
        status: 'resolved',
        baselineScore: 1,
        currentScore: 1,
        proposedScore: 2,
        originalCategory: 'moral',
        originalItemKey: 'moral_volunteer',
        proposedCategory: 'moral',
        proposedItemKey: 'moral_volunteer',
        resolutionScore: 1,
        resolutionReason: '两页签到表覆盖的是同一场活动的上下午，扣掉重叠时段后实际服务时长按 4 小时认定。',
        resolutionCategory: 'moral',
        resolutionItemKey: 'moral_volunteer',
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: true, mine: false, rereview: { decision: 'uphold', score: 1, category: 'moral', itemKey: 'moral_volunteer', reason: '上下午时段重叠，维持 4 小时。', spentSeconds: 130, at: '2026-08-19T07:00:00.000Z' } },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: true, mine: false, rereview: { decision: 'uphold', score: 1, category: 'moral', itemKey: 'moral_volunteer', reason: '同意。', spentSeconds: 80, at: '2026-08-19T09:00:00.000Z' } },
        ],
        createdAt: '2026-08-18T02:00:00.000Z',
        updatedAt: '2026-08-19T09:00:00.000Z',
        resolvedAt: '2026-08-19T09:00:00.000Z',
      },
      /* 扣分项也能被申诉。申诉台与仲裁台对这一类走的是另一条分支
         （只有录入依据、当前分值和满分，没有规则档位），得有数据才看得见。 */
      {
        id: 'ap-pen-1',
        targetType: 'penalty_score',
        targetId: 'u-chen:moral_pen_absence',
        target: '考勤缺勤（每次；迟到早退累计 2 次记 1 次；已受处分不重复扣）',
        category: 'moral',
        itemKey: 'moral_pen_absence',
        student: '陈屿',
        studentId: '20240001',
        reason: '第 3 周周一上午我请过假，辅导员那边有条子，不该记缺勤。',
        round: 1,
        status: 'escalated',
        baselineScore: -1,
        currentScore: -1,
        proposedScore: 0,
        resolutionScore: null,
        resolutionReason: null,
        handlers: [
          { id: 'u-li', name: '李审核', sid: '20240002', decided: true, mine: false, rereview: { decision: 'uphold', score: -1, reason: '班委考勤表上这一格是空的，没有看到请假记录。', spentSeconds: 150, at: '2026-08-24T07:00:00.000Z' } },
          { id: 'u-zhou', name: '周复评', sid: '20240006', decided: true, mine: false, rereview: { decision: 'adjust', score: 0, reason: '学生补交了辅导员签字的请假条，这一次应当销掉。', spentSeconds: 190, at: '2026-08-24T09:00:00.000Z' } },
        ],
        createdAt: '2026-08-23T02:00:00.000Z',
        updatedAt: '2026-08-24T09:00:00.000Z',
        resolvedAt: null,
      },
      ...load.appeals,
    ],
    objections: [
      {
        id: 'ob-1',
        kind: 'penalty',
        studentId: '20240004',
        studentUserId: 'u-zhao',
        student: '赵同学',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_absence',
        itemName: '考勤缺勤（每次；迟到早退累计 2 次记 1 次）',
        targetId: null,
        currentScore: null,
        proposedScore: -2,
        quantity: 2,
        basis: '第 4 周实验课缺勤两次，附签到表。',
        status: 'submitted',
        batchId: 'ob-batch-1',
        proposer: '李审核',
        proposerId: 'u-li',
        decidedScore: null,
        decisionReason: null,
        createdAt: t,
        submittedAt: t,
        decidedAt: null,
      },
      ...load.objections,
    ],
    reports: [
      {
        id: 'rp-1',
        kind: 'penalty',
        studentUserId: 'u-sun',
        studentId: '20240005',
        student: '孙同学',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_absence',
        itemName: '考勤缺勤（每次；迟到早退累计 2 次记 1 次）',
        targetId: null,
        currentScore: null,
        quantity: 2,
        proposedScore: -2,
        basis: '第 5 周和第 6 周的班会都没到，两次点名都不在，班群里也没请假。',
        status: 'reviewing',
        finalScore: null,
        decisionReason: null,
        createdAt: t,
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: null, score: null, reason: null, spentSeconds: null, at: null },
          { reviewerId: 'u-zhou', decision: null, score: null, reason: null, spentSeconds: null, at: null },
        ],
        evidence: [ev('e-rp1-a', '班会点名表-第5周.png'), ev('e-rp1-b', '班会点名表-第6周.png')],
        mine: false,
      },
      {
        id: 'rp-2',
        kind: 'penalty',
        studentUserId: 'u-x-20240012',
        studentId: '20240012',
        student: '黄可',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_notice_college',
        itemName: '院级通报批评',
        targetId: null,
        currentScore: null,
        quantity: 1,
        proposedScore: -5,
        basis: '上个月的宿舍卫生检查通报里有他的名字，通报贴在楼下公告栏，但综测表上一直没记。',
        status: 'escalated',
        finalScore: null,
        decisionReason: null,
        createdAt: t,
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: -5, reason: '通报原件我看过，名单上确实有他。', spentSeconds: 120, at: t },
          { reviewerId: 'u-zhou', decision: 'reject', score: 0, reason: '那份通报是针对整间宿舍下的，不该按个人记到他头上。', spentSeconds: 200, at: t },
        ],
        evidence: [ev('e-rp2', '院级通报原件.pdf')],
        mine: false,
      },
      {
        id: 'rp-3',
        kind: 'penalty',
        studentUserId: 'u-x-20240015',
        studentId: '20240015',
        student: '罗宁',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_appliance',
        itemName: '违规使用大功率用电器（未受处分）',
        targetId: null,
        currentScore: null,
        quantity: 1,
        proposedScore: -3,
        basis: '宿舍查寝那天在他桌下看到电煮锅，宿管当场登记了，楼栋群里也发了整改通知。',
        status: 'applied',
        finalScore: -3,
        decisionReason: '两名复核人结论一致',
        createdAt: '2026-08-20T02:10:00.000Z',
        decidedAt: '2026-08-21T09:30:00.000Z',
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: -3, reason: '宿管登记表能对上，日期也对。', spentSeconds: 160, at: '2026-08-21T07:10:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'uphold', score: -3, reason: '同意，整改通知上写了房间号和姓名。', spentSeconds: 95, at: '2026-08-21T09:30:00.000Z' },
        ],
        evidence: [],
        mine: false,
      },
      {
        id: 'rp-4',
        kind: 'penalty',
        studentUserId: 'u-x-20240028',
        studentId: '20240028',
        student: '叶航',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_absence',
        itemName: '考勤缺勤（每次；迟到早退累计 2 次记 1 次）',
        targetId: null,
        currentScore: null,
        quantity: 3,
        proposedScore: -3,
        basis: '连着三周的形势与政策课都没见到人，我坐他后排。',
        status: 'dismissed',
        finalScore: 0,
        decisionReason: '两名复核人一致认为举报不成立',
        createdAt: '2026-08-18T01:00:00.000Z',
        decidedAt: '2026-08-19T06:20:00.000Z',
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'reject', score: 0, reason: '教务系统里这三周他有辅导员批的病假，考勤不该记缺勤。', spentSeconds: 210, at: '2026-08-19T03:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'reject', score: 0, reason: '看到同一份请假条，同意不成立。', spentSeconds: 130, at: '2026-08-19T06:20:00.000Z' },
        ],
        evidence: [],
        mine: false,
      },
      {
        id: 'rp-5',
        kind: 'penalty',
        studentUserId: 'u-x-20240019',
        studentId: '20240019',
        student: '韩越',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_candle',
        itemName: '点蜡烛',
        targetId: null,
        currentScore: null,
        quantity: 1,
        proposedScore: -2,
        basis: '上周五晚上在宿舍点蜡烛过生日，走廊都闻得到，宿管上来说过一次。',
        status: 'final',
        finalScore: -2,
        decisionReason: '两名复核人一个认定一个驳回。宿管的口头提醒有记录，按最轻的一档认定，扣 2 分。',
        createdAt: '2026-08-15T12:00:00.000Z',
        decidedAt: '2026-08-17T02:40:00.000Z',
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: -2, reason: '宿管值班记录上写了这件事。', spentSeconds: 140, at: '2026-08-16T05:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'reject', score: 0, reason: '只是口头提醒，没有正式处理单，我倾向不记。', spentSeconds: 175, at: '2026-08-16T08:30:00.000Z' },
        ],
        evidence: [],
        mine: false,
      },
      {
        id: 'rp-6',
        kind: 'penalty',
        studentUserId: 'u-x-20240023',
        studentId: '20240023',
        student: '彭舟',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_wire',
        itemName: '违章用电私拉电线',
        targetId: null,
        currentScore: null,
        quantity: 1,
        proposedScore: -5,
        basis: '从走廊插座拉了一条线进宿舍给电动车电池充电，已经拉了半个多月，楼里好几个人看到过。',
        status: 'reviewing',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-25T23:40:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: -5, reason: '我去楼里看过，线还在，拍了照。', spentSeconds: 240, at: '2026-08-26T01:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: null, score: null, reason: null, spentSeconds: null, at: null },
        ],
        evidence: [],
        mine: false,
      },
      {
        id: 'rp-7',
        kind: 'penalty',
        studentUserId: 'u-x-20240031',
        studentId: '20240031',
        student: '任秋',
        category: 'health',
        categoryName: '身体心理素质',
        itemKey: 'health_pen_fitness_fail',
        itemName: '体育测试不达标或体育课成绩不合格（需补考）',
        targetId: null,
        currentScore: null,
        quantity: 1,
        proposedScore: -30,
        basis: '体测成绩单上他是不及格，要补考，但综测表里没有扣这一项。',
        status: 'escalated',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-24T03:20:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: -30, reason: '体育部给的名单上确有他。', spentSeconds: 100, at: '2026-08-24T07:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'adjust', score: -15, reason: '他已经补考通过了，按细则应当减半而不是全扣。', spentSeconds: 300, at: '2026-08-24T10:00:00.000Z' },
        ],
        evidence: [],
        mine: false,
      },
      {
        id: 'rp-8',
        kind: 'base',
        studentUserId: 'u-x-20240022',
        studentId: '20240022',
        student: '蔡青',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_base_collective',
        itemName: '热爱集体、团结同学、尊敬师长、助人为乐，参加集体与公益活动',
        targetId: null,
        currentScore: 15,
        quantity: null,
        proposedScore: 9,
        basis: '这一学期班级大扫除、迎新、运动会检录一次都没参加过，班群里点名也不回。基础分按满分记不合适。',
        status: 'reviewing',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-26T02:00:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: null, score: null, reason: null, spentSeconds: null, at: null },
          { reviewerId: 'u-zhou', decision: null, score: null, reason: null, spentSeconds: null, at: null },
        ],
        evidence: [ev('e-rp8', '班级活动签到汇总.xlsx')],
        mine: false,
      },
      {
        id: 'rp-9',
        kind: 'submission',
        studentUserId: 'u-x-20240017',
        studentId: '20240017',
        student: '邓夏',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_dorm',
        itemName: '文明寝室成员',
        targetId: 's-r63',
        currentScore: 2,
        quantity: null,
        proposedScore: 0,
        basis: '公示的文明寝室名单我对过，是 4 号楼 312，他住的是 314。这一条不该给分。',
        status: 'escalated',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-23T09:00:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: 0, reason: '公示名单上确实是 312，房间号对不上。', spentSeconds: 260, at: '2026-08-23T12:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'reject', score: 2, reason: '他补交了调宿证明，这学期人就在 312。', spentSeconds: 180, at: '2026-08-23T14:00:00.000Z' },
        ],
        evidence: [],
        mine: false,
      },
      {
        id: 'rp-10',
        kind: 'base',
        studentUserId: 'u-x-20240030',
        studentId: '20240030',
        student: '丁一',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_base_campus',
        itemName: '遵守《高等学校学生行为准则》与校纪校规，爱校荣校',
        targetId: null,
        currentScore: 15,
        quantity: null,
        proposedScore: 10,
        basis: '这学期两次晚归被宿管登记，虽然没到通报的程度，但这一项按满分记说不过去。',
        status: 'reviewing',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-26T13:10:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: null, score: null, reason: null, spentSeconds: null, at: null },
          { reviewerId: 'u-zhou', decision: null, score: null, reason: null, spentSeconds: null, at: null },
        ],
        evidence: [],
        mine: false,
      },
      {
        id: 'rp-11',
        kind: 'submission',
        studentUserId: 'u-x-20240035',
        studentId: '20240035',
        student: '邹衡',
        category: 'practice',
        categoryName: '实践创新素质',
        itemKey: 'practice_contest',
        itemName: '与专业学习相关的学科竞赛获奖',
        targetId: 's-r53',
        currentScore: 6,
        quantity: null,
        proposedScore: 3,
        basis: '那个比赛的获奖名单上他是第三作者，按细则第三作者只能拿一半，不该按第一作者给。',
        status: 'reviewing',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-25T07:30:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: null, score: null, reason: null, spentSeconds: null, at: null },
          { reviewerId: 'u-zhou', decision: 'adjust', score: 3, reason: '证书上排第三，按细则确实减半。', spentSeconds: 220, at: '2026-08-25T11:00:00.000Z' },
        ],
        evidence: [],
        mine: false,
      },
      {
        id: 'rp-12',
        kind: 'penalty',
        studentUserId: 'u-x-20240033',
        studentId: '20240033',
        student: '傅南',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_facility',
        itemName: '故意污损公共设施、攀折花木',
        targetId: null,
        currentScore: null,
        quantity: 1,
        proposedScore: -4,
        basis: '把教学楼三楼的消防栓玻璃踢碎了，后勤来换的时候好几个人都在场。',
        status: 'applied',
        finalScore: -4,
        decisionReason: '两名复核人结论一致',
        createdAt: '2026-08-14T06:00:00.000Z',
        decidedAt: '2026-08-15T08:00:00.000Z',
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: -4, reason: '后勤的报修单上写了原因和班级。', spentSeconds: 150, at: '2026-08-15T06:30:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'uphold', score: -4, reason: '同意，他自己也认了。', spentSeconds: 90, at: '2026-08-15T08:00:00.000Z' },
        ],
        evidence: [],
        mine: false,
      },

      /* 以下是"我"（陈屿）提的。演示站靠 mine 这个布尔位模拟真实后端那张只写不读的
         report_reporter 表——真实环境里谁也查不出这个关系，这里就只是本地一个标记。
         五种状态、三种对象各来一遍，「我提交的举报」那一栏才有得看。 */
      {
        id: 'rp-me-1',
        kind: 'penalty',
        studentUserId: 'u-x-20240011',
        studentId: '20240011',
        student: '林晓',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_absence',
        itemName: '考勤缺勤（每次；迟到早退累计 2 次记 1 次）',
        targetId: null,
        currentScore: null,
        quantity: 2,
        proposedScore: -2,
        basis: '第 7 周和第 8 周的班会他都没来，点名的时候我就坐在他那一排，位置一直是空的。',
        status: 'reviewing',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-26T14:20:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: null, score: null, reason: null, spentSeconds: null, at: null },
          { reviewerId: 'u-zhou', decision: null, score: null, reason: null, spentSeconds: null, at: null },
        ],
        evidence: [ev('e-rpme1', '形势政策课考勤截图.png')],
        mine: true,
      },
      {
        id: 'rp-me-2',
        kind: 'base',
        studentUserId: 'u-x-20240016',
        studentId: '20240016',
        student: '谢知',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_base_edu',
        itemName: '参加形势政策、安全法规、校纪校规、心理健康、廉洁等教育活动',
        targetId: null,
        currentScore: 10,
        quantity: null,
        proposedScore: 6,
        basis: '这学期四次形势政策讲座，他只签到过一次，剩下三次的签到表上都没有名字。这一项按满分记不合适。',
        status: 'reviewing',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-25T02:00:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'adjust', score: 8, reason: '签到表少了两次不是三次，按缺两次扣。', spentSeconds: 280, at: '2026-08-25T09:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: null, score: null, reason: null, spentSeconds: null, at: null },
        ],
        evidence: [],
        mine: true,
      },
      {
        id: 'rp-me-3',
        kind: 'submission',
        studentUserId: 'u-x-20240018',
        studentId: '20240018',
        student: '曹启',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_volunteer',
        itemName: '青年志愿者服务、社区服务活动（按服务时长）',
        targetId: 's-r64',
        currentScore: 6,
        quantity: null,
        proposedScore: 3,
        basis: '他报的志愿服务工时里有一半是同一场活动上下午重复填报的，去掉重叠时段后实际服务时长只有一半。',
        status: 'applied',
        finalScore: 3,
        decisionReason: '两名复核人结论一致',
        createdAt: '2026-08-19T02:30:00.000Z',
        decidedAt: '2026-08-20T10:00:00.000Z',
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: 3, reason: '工时表上日期确实重了三天。', spentSeconds: 320, at: '2026-08-20T06:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'uphold', score: 3, reason: '同意，同一天上下午按一次。', spentSeconds: 175, at: '2026-08-20T10:00:00.000Z' },
        ],
        evidence: [],
        mine: true,
      },
      {
        id: 'rp-me-4',
        kind: 'penalty',
        studentUserId: 'u-x-20240020',
        studentId: '20240020',
        student: '冯川',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_stove',
        itemName: '使用煤油炉（酒精炉）',
        targetId: null,
        currentScore: null,
        quantity: 1,
        proposedScore: -3,
        basis: '上周在他们宿舍看到一个酒精炉，摆在窗台上，看着像是用过的。',
        status: 'dismissed',
        finalScore: 0,
        decisionReason: '两名复核人一致认为举报不成立',
        createdAt: '2026-08-16T11:00:00.000Z',
        decidedAt: '2026-08-18T03:20:00.000Z',
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'reject', score: 0, reason: '去看过，是化学实验课要交的模型，不是能点的炉子。', spentSeconds: 240, at: '2026-08-17T08:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'reject', score: 0, reason: '同意，宿管也确认过没有明火痕迹。', spentSeconds: 130, at: '2026-08-18T03:20:00.000Z' },
        ],
        evidence: [],
        mine: true,
      },
      {
        id: 'rp-me-5',
        kind: 'base',
        studentUserId: 'u-x-20240024',
        studentId: '20240024',
        student: '董衡',
        category: 'health',
        categoryName: '身体心理素质',
        itemKey: 'health_base_fitness',
        itemName: '积极参加体育锻炼，身体素质良好，卫生习惯良好',
        targetId: null,
        currentScore: 10,
        quantity: null,
        proposedScore: 4,
        basis: '晨跑打卡一学期只有五次，体育课也请了很多假，这一项按满分记和实际差得比较远。',
        status: 'escalated',
        finalScore: null,
        decisionReason: null,
        createdAt: '2026-08-21T23:10:00.000Z',
        decidedAt: null,
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'adjust', score: 7, reason: '打卡次数确实少，但请假都是有条的，扣一半太重。', spentSeconds: 300, at: '2026-08-22T07:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'reject', score: 10, reason: '这一项细则里没写打卡次数的要求，没有依据往下改。', spentSeconds: 260, at: '2026-08-22T12:30:00.000Z' },
        ],
        evidence: [],
        mine: true,
      },
      {
        id: 'rp-me-6',
        kind: 'submission',
        studentUserId: 'u-x-20240021',
        studentId: '20240021',
        student: '蒋默',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_dorm',
        itemName: '文明寝室成员',
        targetId: 's-r67',
        currentScore: 3,
        quantity: null,
        proposedScore: 0,
        basis: '他按校级文明寝室报的 3 分，但学院公示的校级名单我截图对过，里面没有他们寝室。',
        status: 'final',
        finalScore: 0,
        decisionReason: '两名复核人一个认定一个驳回。调取学院公示的校级文明寝室名单核实，确无此寝室，按举报认定改为 0 分。',
        createdAt: '2026-08-12T05:00:00.000Z',
        decidedAt: '2026-08-14T09:40:00.000Z',
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: 0, reason: '他附的是院级卫生检查截图，不是校级文明寝室名单。', spentSeconds: 210, at: '2026-08-13T06:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'reject', score: 3, reason: '名单我一时查不到，倾向维持原判。', spentSeconds: 150, at: '2026-08-13T09:00:00.000Z' },
        ],
        evidence: [ev('e-rpme6-a', '学院公示的校级文明寝室名单.png'), ev('e-rpme6-b', '他交的院级卫生检查截图.png')],
        mine: true,
      },
      {
        id: 'rp-me-7',
        kind: 'penalty',
        studentUserId: 'u-x-20240032',
        studentId: '20240032',
        student: '姚望',
        category: 'moral',
        categoryName: '思想道德素质',
        itemKey: 'moral_pen_notice_school',
        itemName: '校级通报批评',
        targetId: null,
        currentScore: null,
        quantity: 1,
        proposedScore: -10,
        basis: '学校 6 月的通报批评名单里有他，通报编号我记下来了，教务处网站上还能查到。',
        status: 'applied',
        finalScore: -10,
        decisionReason: '两名复核人结论一致',
        createdAt: '2026-08-10T01:00:00.000Z',
        decidedAt: '2026-08-11T07:15:00.000Z',
        reviewerIds: ['u-li', 'u-zhou'],
        reviews: [
          { reviewerId: 'u-li', decision: 'uphold', score: -10, reason: '通报原文我查到了，姓名学号都对得上。', spentSeconds: 190, at: '2026-08-11T02:00:00.000Z' },
          { reviewerId: 'u-zhou', decision: 'uphold', score: -10, reason: '同意，是校级不是院级。', spentSeconds: 110, at: '2026-08-11T07:15:00.000Z' },
        ],
        evidence: [],
        mine: true,
      },
    ],
    classificationSuggestions: [],
    bases,
    resources: [
      {
        id: 'res-1',
        filename: '班级操行分评定细则.pdf',
        logicalPath: 'class/操行细则.pdf',
        displayName: '班级操行分评定细则',
        mediaType: 'application/pdf',
        sizeBytes: 240_000,
        updatedAt: t,
        publishedAt: t,
      },
    ],
    knowledgeDocs: [
      {
        id: 'kd-1',
        filename: '班级操行分评定细则.pdf',
        logicalPath: 'class/操行细则.pdf',
        displayName: '班级操行分评定细则',
        mediaType: 'application/pdf',
        sizeBytes: 240_000,
        status: 'ready',
        searchable: true,
        extractor: 'pdf',
        entries: 1,
        pageCount: 4,
        sheetCount: null,
        warning: null,
        error: null,
        createdAt: t,
        updatedAt: t,
        publishedAt: t,
        text: '班级操行分按考勤、宿舍、活动参与综合评定。细则与学院文件一致，班委记录后由综测小组提案、班级管理员终裁。',
      },
      /* 活动汇总表：细则定规则，这张表定「今年办了哪些活动、每条按什么加」。
         它属于班级文件，不属于方案——真站里是班管上传 PDF 后由 knowledge 转换成
         可检索条目，学生问 Agent「书香晨光算什么分」时引用它。塞进小项说明是错的：
         七十条活动会把每个小项的说明重新撑爆。 */
      {
        id: 'kd-2',
        filename: '2025-2026学年示例学院团委学生会活动汇总.pdf',
        logicalPath: 'class/活动汇总表.pdf',
        displayName: '2025-2026 团委学生会活动汇总表',
        mediaType: 'application/pdf',
        sizeBytes: 746_842,
        status: 'ready',
        searchable: true,
        extractor: 'pdf',
        entries: 70,
        pageCount: 5,
        sheetCount: null,
        warning: null,
        error: null,
        createdAt: t,
        updatedAt: t,
        publishedAt: t,
        text: [
          '2025-2026学年示例学院团委学生会活动汇总。列出本学年已办活动的时间、级别、承办组织与加分口径。',
          '分值写作「A/B」时，A 为排名第一或主要承担者，B 为其他成员减半后的分值。',
          '',
          '【院团委 · 校级与省级】',
          '2026年暑期三下乡活动｜2026.7｜校级｜队长6分，其他成员5分',
          '2026年团委学生会换届大会｜2026.5｜院级｜组织者2分',
          '2025年秋季运动会｜2025.11｜校级｜组织者2分',
          '2026年春季运动会｜2026.5｜校级｜组织者2分',
          '2025年红色云游评选｜2025.9｜省级｜江西省团委｜省级一等奖(10分/5分)',
          '2025年红色走读｜2025.9｜省级｜江西省团委｜参与奖(2分/1分)',
          '第六届江西省高校红色文化实践案例布展｜2026.7｜省级｜省委教育工委宣传部｜省级一等奖(10分/5分)',
          '国防素养大赛｜2025.11｜省级｜江西省教育厅｜优秀奖(4分/2分)',
          '社会工作与志愿服务大赛｜2025.11｜省级｜省委社会工作部、省总工会｜入选精品展示交流项目(8分/4分)',
          '',
          '【团委组织部】',
          '青马工程｜2025.10.17｜院级｜参与者+0.25(思想道德)；院级优秀学员+6；校级结课学员+5；院级结课成员+4',
          '微团课大赛｜2025.10.20｜院级｜参与者、观众+0.25(思想道德)；院级一等+2、三等+1、鼓励+0.5；校级一等+4、二等+3、三等+2、鼓励+0.5（院级二等一栏原表被遮挡，按细则院级二等为 1 分）',
          '团支书技能培训会｜2025.12.12｜院级｜参与者+0.25(思想道德)',
          '2026年第一期入团积极分子选拔｜2025.12.17｜院级｜参与者+0.25(思想道德)',
          '团员和青年主题教育专题组织生活会｜2025.12.31｜院级｜参与者+0.25(思想道德)',
          '“青年实干家—青苗荟江西”项目申报｜2026.3.17｜院级｜参与者+0.25(思想道德)',
          '“两红两优”申报｜2026.3.19｜院级｜参与者+0.25(思想道德)',
          '2026年第二期入团积极分子选拔｜2026.6.4｜院级｜参与者+0.25(思想道德)',
          '',
          '【生活权益部】',
          '2026“赠一卷旧书，留满墙新语”主题活动｜2026.4.1｜院级｜参与者+0.25(思想道德)',
          '315消费者权益日活动｜2026.3.15｜院级｜参与者+0.25(思想道德)',
          '五个一项目(24级第二、三篇，25级第一篇)｜2025.12.6—2026.6.26｜院级｜参与者+0.25(思想道德)',
          '学代会｜2025.11.15｜校级｜组织者、参与者+0.25(思想道德)',
          '校园讲解大赛｜2026.6.1—6.23｜校级｜参与者+0.25(思想道德)',
          '',
          '【综合服务部】',
          '公文培训大会｜2025.10.18｜院级｜参与者+0.25(思想道德)',
          '红色标语搜集与故事挖掘实践活动｜2026.1.18｜校级｜组织者+0.25(思想道德)；一等奖2分，二、三等奖1分，参与奖0.5分',
          '“四六级能量贴”活动｜2025.12.29｜院级｜参与者+0.25(思想道德)',
          '“缅怀先烈，致敬清明”活动｜2026.4.5｜院级｜参与者+0.25(思想道德)',
          '朗诵比赛观赛｜2025.12｜校级｜参与者+0.25(思想道德)',
          '',
          '【新媒体运营中心】以下均为参与者+0.25(思想道德)',
          '“AI知心•洞见我心”主题活动｜2025.12.20｜院级',
          '“触手生‘智’，妙想成‘媒’”｜2025.12.28｜院级',
          '“接纳不完美，成就独一份”主题活动｜2025.12.20｜院级',
          '“元旦赏诗品韵中华”主题活动｜2025.12.31｜院级',
          '“指尖生花•创意折纸”趣味折纸主题活动｜2025.12.21｜院级',
          '“智弈五子，对决AI”主题活动｜2025.12.14｜院级',
          '视频剪辑技能培训会｜2025.10｜院级',
          '微信推送培训会｜2025.10｜院级',
          '摄影技能培训｜2025.12｜院级',
          '新闻稿撰写技能培训｜2025.12｜院级',
          '海报设计大赛｜2026.5.15｜院级｜获奖可按校级获奖加分',
          '',
          '【学生会学习部 ·「智志+」辅导员工作室】以下均为参与者+0.25(思想道德)',
          '学习部学生干部第一次技能培训｜2025.10.17｜院级｜学习部',
          '第三次红色讲堂活动｜2025.11.21｜校级｜智志+',
          '第四次红色讲堂活动｜2025.12.24｜校级｜智志+',
          '“智破难点，学享同行”学习沙龙活动｜2025.10.26—29｜院级｜学习部｜获奖可按院级获奖加分',
          '“布上生花，心手同行”心理主题活动｜2026.3.22｜校级｜智志+',
          '“心灵绿洲•科普有光”心理科普作品征集｜2026.4.15—4.20｜校级｜智志+｜获奖可按校级获奖加分',
          '“谷雨育芽•劳动育心”劳动主题活动｜2026.4.19｜校级｜智志+',
          '“智启未来”AI工具分享会｜2026.4.26｜校级｜智志+',
          '“智启新程，志创未来”竞赛指导讲座｜2026.5.16｜校级｜智志+',
          '“铭记五卅历史，传承爱国精神”五卅纪念日主题活动｜2026.5.30｜校级｜智志+',
          '“期末冲刺•优学伴学”学习沙龙活动｜2026.6.29｜院级｜学习部｜获奖可按院级获奖加分',
          '',
          '【院学生会校园文化部】队伍类工作与运动会训练按具体加分条认定',
          '啦啦队工作｜2025-2026｜校级｜参考具体加分条',
          '礼仪队工作｜2025-2026｜院级｜参考具体加分条',
          '篮球队工作｜2025-2026｜校级｜参考具体加分条',
          '舞蹈队工作｜2025-2026｜院级｜参考具体加分条',
          '主持人队工作｜2025-2026｜院级｜参考具体加分条',
          '乒乓球工作｜2025-2026｜校级｜参考具体加分条',
          '气排球工作｜2025-2026｜校级｜参考具体加分条',
          '足球队工作｜2025-2026｜校级｜参考具体加分条',
          '2025年秋季运动会运动员训练｜2025.9.26—10.25｜校级｜参考具体加分条（按训练时长折算为身体心理素质）',
          '2026年春季运动会运动员训练｜2026.4.22—5.22｜校级｜参考具体加分条（按训练时长折算为身体心理素质）',
          '院十佳歌手活动｜2025.10.14—21｜院级｜参考具体加分条',
          '春季运动会优秀运动员｜2026.5｜校级｜3分(身体心理素质)',
          '',
          '【青年志愿者协会】志愿服务一律按实际服务时长折算，1 小时 0.25 分',
          '迎新场地布置｜2025.9.16｜院级｜按实际时长计分',
          '开学典礼｜2025.9.26｜院级｜按实际时长计分',
          '珐琅画｜2025.9.20—10.7｜院级｜按实际时长计分',
          '红色云游志愿者｜2025.10｜院级｜按实际时长计分',
          '迎新志愿者｜2025.9.5｜校级｜1分',
          '运动会海报制作｜2025.10｜院级｜按实际时长计分',
          '书香晨光｜2025.11.7、2025.12.1、2026.3.26、2026.4.7、2026.4.24、2026.5.20｜校级｜1小时/0.25分',
          '国防科技大赛｜2025.11.7｜院级｜按实际时长计分',
          '国际志愿者日｜2025.12.5｜院级｜按实际时长计分',
          '冬日暖心捐｜2025.12.20｜院级｜组织者可按时长计分，参与者0.5分',
          '冬季招聘会｜2025.12.23｜院级｜按实际时长计分',
          '数据录入｜2025.12.23｜院级｜按实际时长计分',
          '师大青年说｜2025.12.29｜校级｜按实际时长计分',
          '雷锋护校河｜2026.3.21 与 6.6｜院级｜按实际时长计分',
          '无偿献血｜2026.6.10｜校级｜按实际时长计分（献血本身另按「无偿献血」每次 2 分申报）',
          '',
          '【第二课堂运营中心】',
          '“笔墨连千里•同心向未来”｜2026.5.17｜院级｜组织者1分，参与者0.25分',
          '培训会｜2025.10.28｜院级｜组织者1分，参与者0.25分',
        ].join('\n'),
      },
    ],
    gpa: users
      .filter((u) => u.role !== 'ops' && u.registered)
      .map((u, i) => ({
        userId: u.id,
        sid: u.sid,
        name: u.name,
        score: u.id === 'u-chen' ? 88.6 : 80 + i,
        batch: 'gpa-202608',
        updatedAt: t,
      })),
    tenants: [
      {
        id: 'tn-demo',
        name: CLASS_NAME,
        slug: '24-cs-demo',
        state: 'running',
        archived: false,
        storageBytes: 12_000_000,
        storageCalibratedAt: t,
        admins: [{ sid: '20240003', name: '王管理', registered: true }],
        createdAt: '2026-07-01T00:00:00.000Z',
        updatedAt: t,
      },
      {
        id: 'tn-arch',
        name: '23软件演示班',
        slug: '23-se-demo',
        state: 'archived',
        archived: true,
        storageBytes: 1_000_000,
        storageCalibratedAt: t,
        admins: [{ sid: '20230001', name: '钱老师', registered: true }],
        createdAt: '2025-09-01T00:00:00.000Z',
        updatedAt: t,
      },
    ],
    conversations: [
      {
        id: 'cv-1',
        title: '班长任职怎么报',
        messages: [
          {
            id: 'm-1',
            role: 'user',
            status: 'complete',
            content: '学生干部任职选哪一档？',
            attachments: [],
            citations: [],
            toolTrace: [],
            finalThought: '',
            actions: [],
            sourceRevoked: false,
            error: null,
            createdAt: t,
            finishedAt: t,
          },
          {
            id: 'm-2',
            role: 'assistant',
            status: 'complete',
            content: '班长、团支书与辅导员助理、新生助导员是同一档，建议分 8 分。同一互斥组只取最高一条，只任职一学期减半。请在「提交材料」里选择「学生干部任职」，并上传任命文件。',
            attachments: [],
            citations: [
              {
                documentId: 'kd-1',
                entryId: 'ke-1',
                filename: '班级操行分评定细则.pdf',
                logicalPath: 'class/操行细则.pdf',
                locator: { page: 1 },
                excerpt: '学生干部任职按岗位分档，不累加。',
                scope: 'class',
                sourceKind: 'class',
                downloadable: true,
                audienceRole: 'student',
              },
            ],
            toolTrace: [
              { seq: 1, tool: 'current_scheme', status: 'complete', thought: '先看当前方案里的干部任职档。', summary: '读到学生干部任职 6 档', durationMs: 420 },
              { seq: 2, tool: 'read_text', status: 'complete', thought: '对照班级细则。', summary: '操行细则第 1 页', durationMs: 260 },
            ],
            finalThought: '按方案原文回答，不替学生填报。',
            actions: [],
            sourceRevoked: false,
            error: null,
            createdAt: t,
            finishedAt: t,
          },
        ],
        createdAt: t,
        updatedAt: t,
      },
    ],
    pendingUploads: {},
    audit: [
      {
        id: 'au-1',
        actorRole: 'class_admin',
        actorSid: '20240003',
        actor: '王管理',
        action: 'scheme.publish',
        resourceType: 'scheme',
        resourceId: 'sch-pub',
        metadata: { version: 1 },
        ip: '192.0.2.2',
        userAgent: 'Mozilla/5.0',
        createdAt: t,
      },
      ...load.audit,
    ],
    sealed: {},
    confirmed: {},
    dispatch: { auto: true, seed: '20260820001', paused: {} },
    gateForced: false,
    exportJobs: [],
    flags: { registration: true, maintenance: false, uploadMaxMb: 50, apiRateLimitPerMinute: 120 },
    registerTickets: {},
    resetTokens: {},
    emailChallenges: {},
    tourCast: 'student',
    /* 演示站默认已经配好并开着，这样「备份与恢复」一进去就能看到异地那一层
       是什么样子；点保存、测试、清空都能走通，只是不连真桶。 */
    backupRemote: {
      enabled: true,
      endpoint: 'cos.ap-guangzhou.myqcloud.com',
      bucket: 'easygpa-demo-1250000000',
      region: 'ap-guangzhou',
      prefix: 'easygpa/',
      useSsl: true,
      pathStyle: false,
      retentionDays: 90,
      accessKeySet: true,
      secretKeySet: true,
    },
  }
  landSettledReports(store)
  seedDeputyCases(store, snapshot)
  return store
}

/* 种子里已经落定的举报，要把分真的写到计分卡上。
   不写的话演示站会自相矛盾：「我提交的举报」显示这条已认定 -3，翻到那个人的计分卡上
   却一分没扣。真实后端是复核通过的那一刻就写 base_score 的，这里补上同一步。 */
function landSettledReports(store: Store) {
  for (const row of store.reports) {
    if (row.finalScore === null) continue
    if (row.status !== 'applied' && row.status !== 'final') continue
    if (row.kind === 'submission') {
      const sub = store.submissions.find((x) => x.id === row.targetId)
      if (sub) sub.finalScore = row.finalScore
      continue
    }
    const basis = '学生举报，经复核认定'
    const hit = store.bases.find((b) => b.userId === row.studentUserId && b.itemKey === row.itemKey && b.kind === row.kind)
    if (hit) {
      hit.score = row.kind === 'penalty' ? Math.round((hit.score + row.finalScore) * 1000) / 1000 : row.finalScore
      hit.basis = basis
      hit.recorded = true
      hit.updatedAt = row.decidedAt ?? hit.updatedAt
      continue
    }
    const cat = store.scheme.categories.find((c) => c.key === row.category)
    store.bases.push({
      userId: row.studentUserId,
      category: row.category,
      categoryName: row.categoryName,
      itemKey: row.itemKey,
      itemName: row.itemName,
      kind: row.kind,
      fullScore: row.kind === 'penalty' ? null : (cat?.baseItems.find((i) => i.key === row.itemKey)?.full ?? null),
      score: row.finalScore,
      basis,
      recorded: true,
      appealsUsed: 0,
      canAppeal: row.kind !== 'penalty',
      updatedAt: row.decidedAt ?? nowIso(),
    })
  }
}

let mem: Store | null = null

export function db(): Store {
  if (mem) {
    if (mem.v !== STORE_VER) {
      mem = seed()
      persist()
    }
    return mem
  }
  try {
    const raw = sessionStorage.getItem(STORE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Store
      if (parsed.v === STORE_VER) {
        mem = parsed
        if (scrubInstantTourFeedback(mem)) persist()
        return mem
      }
    }
  } catch {
    /* 坏数据就重种 */
  }
  mem = seed()
  persist()
  return mem
}

/** 旧版录像号会在提交或切身份时写两条假审核。那份文案已从源码删掉，
    但本标签页 sessionStorage 里可能还留着，看起来就像功能又回来了。 */
function scrubInstantTourFeedback(store: Store) {
  const fake = /演示站即时假反馈|对向身份已按申报/
  let changed = false
  for (const [id, reviews] of Object.entries(store.reviews)) {
    if (!reviews.some((row) => fake.test(row.reason))) continue
    delete store.reviews[id]
    changed = true
    const sub = store.submissions.find((row) => row.id === id)
    if (sub && (sub.status === 'scored' || sub.status === 'consensus' || sub.status === 'arbitrating')) {
      sub.status = 'pending'
      sub.finalScore = null
      sub.canAppeal = false
      sub.updatedAt = nowIso()
    }
    for (const seat of store.assignments) {
      if (seat.submissionId === id) seat.decided = false
    }
  }
  return changed
}

export function persist() {
  if (!mem) return
  try {
    sessionStorage.setItem(STORE_KEY, JSON.stringify(mem))
  } catch {
    /* 配额满时演示仍可继续，只是刷新会丢 */
  }
}

export function resetStore() {
  mem = seed()
  persist()
}

export function nid(prefix: string) {
  const s = db()
  s.seq += 1
  return `${prefix}-${s.seq}`
}

export function userBySid(account: string): DemoUser | undefined {
  const q = account.trim()
  return db().users.find((u) => u.sid === q || u.primaryEmail === q)
}

export function userById(id: string): DemoUser | undefined {
  return db().users.find((u) => u.id === id)
}

export function userFromToken(token: string | null): DemoUser | null {
  const match = /^mock\.(.+)\.(\d+)$/.exec(token ?? '')
  if (!match) return null
  const user = userById(match[1])
  if (!user?.registered || user.status !== 'active' || token !== tokenFor(user)) return null
  return isTourUser(user) ? tourActor(user) : user
}

export function isTourUser(user: { id?: string; sid?: string } | null | undefined): boolean {
  return Boolean(user && (user.id === TOUR_ID || user.sid === TOUR_SID))
}

function tourOverlay(cast: TourCast): Pick<DemoUser, 'role' | 'isDeputy' | 'sub'> {
  if (cast === 'class_admin') return { role: 'class_admin', isDeputy: false, sub: '班级管理员 · 录像号' }
  if (cast === 'deputy') return { role: 'group', isDeputy: true, sub: '综测小组 · 副班管' }
  if (cast === 'group') return { role: 'group', isDeputy: false, sub: '综测小组 · 录像号' }
  return { role: 'student', isDeputy: false, sub: '学生 · 录像号' }
}

/* 读身份走镜头，写账号资料仍落到名单那一行。切到班管时成员列表里的周未注册还是学生。 */
function tourActor(user: DemoUser): DemoUser {
  return new Proxy(user, {
    get(target, prop, receiver) {
      if (prop === 'role' || prop === 'isDeputy' || prop === 'sub') {
        return tourOverlay(db().tourCast ?? 'student')[prop]
      }
      return Reflect.get(target, prop, receiver)
    },
  })
}

export function applyTourCast(identity: TourCast) {
  const store = db()
  store.tourCast = identity
  if (identity === 'group' || identity === 'deputy') ensureTourReviewLoad()
}

export function provisionTourUser(user: DemoUser) {
  const store = db()
  const scheme = store.scheme
  const at = nowIso()
  user.password = user.password || DEMO_PASSWORD
  user.registered = true
  user.status = 'active'
  user.sessionVersion = user.sessionVersion || 0
  const row = store.whitelist.find((item) => item.sid === user.sid)
  if (row) {
    row.registered = true
    row.registeredAt = row.registeredAt ?? at
  }
  if (!store.gpa.some((item) => item.userId === user.id)) {
    store.gpa.push({ userId: user.id, sid: user.sid, name: user.name, score: 86.4, batch: 'gpa-202608', updatedAt: at })
  }
  for (const cat of scheme.categories) {
    for (const item of cat.baseItems) {
      if (item.studentClaim) continue
      if (store.bases.some((base) => base.userId === user.id && base.itemKey === item.key && base.kind === 'base')) continue
      store.bases.push({
        userId: user.id,
        category: cat.key,
        categoryName: cat.name,
        itemKey: item.key,
        itemName: item.name,
        kind: 'base',
        fullScore: item.full,
        score: item.full,
        basis: '方案默认满分，尚未人工调整。',
        recorded: false,
        appealsUsed: 0,
        canAppeal: true,
      })
    }
  }
  if (!store.tourCast) store.tourCast = 'student'
}

/* 录像号切到小组时，把李审核手头未完成的初审、举报和申诉座位抄一份过来。
   不改李审核本人的任命和队列，这样其他演示号在同一份种子里仍能对得上。 */
function ensureTourReviewLoad() {
  const store = db()
  const tour = store.users.find((item) => item.id === TOUR_ID)
  if (!tour) return
  for (const seat of store.assignments.filter((item) => item.reviewerId === 'u-li' && !item.decided)) {
    if (store.assignments.some((item) => item.reviewerId === TOUR_ID && item.submissionId === seat.submissionId)) continue
    store.assignments.push({ id: nid('asg'), submissionId: seat.submissionId, reviewerId: TOUR_ID, decided: false })
  }
  for (const row of store.reports.filter((item) => item.status === 'reviewing')) {
    if (row.reviewerIds.includes(TOUR_ID)) continue
    const index = row.reviewerIds.findIndex((id) => id === 'u-zhou')
    const from = index >= 0 ? 'u-zhou' : row.reviewerIds.find((id) => id !== 'u-li')
    if (!from) continue
    row.reviewerIds = row.reviewerIds.map((id) => (id === from ? TOUR_ID : id))
    const seat = row.reviews.find((item) => item.reviewerId === from && !item.at)
    if (seat) seat.reviewerId = TOUR_ID
  }
  for (const appeal of store.appeals.filter((item) => item.status === 'reviewing')) {
    if (appeal.handlers.some((handler) => handler.id === TOUR_ID)) continue
    const seat = appeal.handlers.find((handler) => handler.id === 'u-zhou' && !handler.decided)
      ?? appeal.handlers.find((handler) => !handler.decided && handler.id !== 'u-li')
    if (!seat) continue
    seat.id = TOUR_ID
    seat.name = tour.name
    seat.sid = tour.sid
  }
}

export function landMockScore(
  row: Pick<MockReport, 'kind' | 'targetId' | 'studentUserId' | 'category' | 'categoryName' | 'itemKey' | 'itemName'>,
  score: number,
  basis = '学生举报，经复核与终裁认定',
) {
  const store = db()
  if (row.kind === 'submission') {
    const sub = store.submissions.find((item) => item.id === row.targetId)
    if (sub) {
      sub.finalScore = score
      sub.updatedAt = nowIso()
    }
    return
  }
  const existing = store.bases.find((base) => base.userId === row.studentUserId && base.itemKey === row.itemKey && base.kind === row.kind)
  if (existing) {
    existing.score = row.kind === 'penalty' ? Math.round((existing.score + score) * 1000) / 1000 : score
    existing.basis = basis
    existing.recorded = true
    existing.updatedAt = nowIso()
    return
  }
  const cat = store.scheme.categories.find((item) => item.key === row.category)
  store.bases.push({
    userId: row.studentUserId,
    category: row.category,
    categoryName: row.categoryName,
    itemKey: row.itemKey,
    itemName: row.itemName,
    kind: row.kind,
    fullScore: row.kind === 'penalty' ? null : (cat?.baseItems.find((item) => item.key === row.itemKey)?.full ?? null),
    score,
    basis,
    recorded: true,
    appealsUsed: 0,
    canAppeal: true,
    updatedAt: nowIso(),
  })
}

export function tokenFor(user: DemoUser) {
  return `mock.${user.id}.${user.sessionVersion}`
}

export function adminUsers(): AdminUser[] {
  return db()
    .users.filter((u) => u.role !== 'ops' && u.registered)
    .map((u) => ({
      id: u.id,
      sid: u.sid,
      name: u.name,
      role: u.role as Exclude<Role, 'ops'>,
      isDeputy: Boolean(u.isDeputy),
      status: u.status,
      primaryEmail: u.primaryEmail,
      sealed: Boolean(db().sealed[u.id]?.sealed),
      lastLoginAt: u.registered ? '2026-08-25T08:00:00.000Z' : null,
      createdAt: '2026-07-01T00:00:00.000Z',
    }))
}

export function adminSeals(): AdminSeal[] {
  return db()
    // 与真实班管接口一致：名单成员即使尚未注册，也要作为“尚未封存”出现在全班状态里。
    .users.filter((u) => u.role !== 'ops')
    .map((u) => {
      const rows = db().submissions.filter((s) => s.studentId === u.sid)
      const seal = db().sealed[u.id]
      return {
        userId: u.id,
        sid: u.sid,
        name: u.name,
        role: u.role,
        drafts: rows.filter((s) => s.status === 'draft').length,
        submitted: rows.filter((s) => s.status !== 'draft').length,
        scored: rows.filter((s) => s.status === 'scored' || s.status === 'locked').length,
        sealState: seal?.sealed ? 'sealed' : rows.length ? 'active' : 'none',
        sealSource: seal?.source ?? null,
        sealedAt: seal?.sealedAt ?? null,
        lastActivity: '2026-08-25T08:00:00.000Z',
      } satisfies AdminSeal
    })
}

export function dispatchState(): DispatchState {
  const s = db()
  /* 审核池不只是综测小组：后端分发按 role IN ('group','class_admin') 取人，
     班级管理员默认也背审核量。只筛 group 的话分发页上根本看不到班管。 */
  const reviewers = s.users.filter((u) => u.role === 'group' || u.role === 'class_admin')
  const assigned = s.assignments
  const rows = reviewers.map((u) => {
    const mine = assigned.filter((a) => a.reviewerId === u.id)
    return {
      userId: u.id,
      sid: u.sid,
      name: u.name,
      role: u.role as Exclude<Role, 'ops'>,
      paused: Boolean(s.dispatch.paused[u.id]),
      assigned: mine.length,
      pending: mine.filter((a) => !a.decided).length,
      done: mine.filter((a) => a.decided).length,
    }
  })
  const counts = rows.map((r) => r.assigned)
  return {
    policy: 'balanced_per_submission',
    seed: s.dispatch.seed,
    avoidSelf: true,
    auto: s.dispatch.auto,
    reviewers: rows,
    unassigned: 0,
    assignedTotal: assigned.length,
    pendingTotal: assigned.filter((a) => !a.decided).length,
    spread: counts.length ? Math.max(...counts) - Math.min(...counts) : 0,
    lastRunAt: '2026-08-20T09:00:00.000Z',
    lastRunBy: '王管理',
  }
}

export function reviewTasksFor(user: DemoUser, tab: string): (ReviewTask | ReportTask)[] {
  const s = db()
  const mine = s.assignments.filter((a) => a.reviewerId === user.id)
  const tasks: (ReviewTask | ReportTask)[] = []
  for (const a of mine) {
    const sub = s.submissions.find((x) => x.id === a.submissionId)
    if (!sub) continue
    const decided = Boolean(s.reviews[sub.id]?.some((r) => r.reviewerId === user.id) || a.decided)
    const reviewCount = s.reviews[sub.id]?.length ?? 0
    const overdue = isOverdueSubmission(sub.id)
    const row: ReviewTask = {
      id: a.id,
      type: 'submission',
      title: sub.title,
      category: sub.category,
      itemKey: sub.itemKey,
      requestedScore: sub.wantScore,
      status: sub.status,
      submittedAt: sub.submittedAt,
      assignedAt: sub.submittedAt ?? nowIso(),
      slaHours: 72,
      dueAt: overdue ? '2026-08-22T08:00:00.000Z' : '2026-08-29T18:00:00.000Z',
      overdue,
      studentId: sub.studentId,
      student: sub.student,
      assignmentId: a.id,
      reviewedByMe: decided,
      reviewCount,
      expectedReviews: 2,
    }
    if (tab === 'mine' && !decided && (sub.status === 'pending' || sub.status === 'consensus')) tasks.push(row)
    else if (tab === 'peer' && decided && (sub.status === 'pending' || sub.status === 'consensus')) tasks.push(row)
    else if (tab === 'conflict' && sub.status === 'arbitrating') tasks.push(row)
  }

  /* 学生匿名举报也派给同一批审核人。id 带 report: 前缀，和 submission id 分开命名空间——
     真实后端也是这么做的。举报人不在这里，也不在任何一层里。 */
  if (tab === 'mine' || tab === 'peer') {
    for (const row of s.reports.filter((r) => r.status === 'reviewing' && r.reviewerIds.includes(user.id))) {
      const seat = row.reviews.find((r) => r.reviewerId === user.id)
      const decided = Boolean(seat?.at)
      if (tab === 'mine' ? decided : !decided) continue
      tasks.push({
        id: `report:${row.id}`,
        type: 'report',
        title: '匿名举报复核',
        kind: row.kind,
        category: row.category,
        categoryName: row.categoryName,
        itemKey: row.itemKey,
        itemName: row.itemName,
        requestedScore: row.proposedScore,
        status: row.status,
        submittedAt: row.createdAt,
        assignedAt: row.createdAt,
        slaHours: 48,
        dueAt: '2026-08-28T08:00:00.000Z',
        overdue: false,
        studentId: row.studentId,
        student: row.student,
        assignmentId: `${row.id}-${user.id}`,
        reviewedByMe: decided,
        reviewCount: row.reviews.filter((r) => r.at).length,
        expectedReviews: row.reviews.length,
      })
    }
  }

  return tasks.sort((a, b) => Number(b.overdue) - Number(a.overdue) || a.dueAt.localeCompare(b.dueAt) || a.id.localeCompare(b.id, undefined, { numeric: true }))
}

export function uploadUrl() {
  return new URL('/mock-upload', location.origin).href
}

export function pageOf<T>(items: T[], query: URLSearchParams) {
  const page = Math.max(1, Number(query.get('page') ?? 1) || 1)
  const pageSize = Math.min(100, Math.max(1, Number(query.get('page_size') ?? 20) || 20))
  const start = (page - 1) * pageSize
  return { items: items.slice(start, start + pageSize), page, pageSize, total: items.length }
}
