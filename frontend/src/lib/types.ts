/* 领域类型。与 docs/DESIGN.md §3 表清单、§4 方案配置树、§5 状态机一一对应。
   前端不自造概念：这里出现的每个字段都能在设计文档里找到出处。 */

export type Role = 'student' | 'group' | 'class_admin' | 'ops'

/* 视角切换只降不升（§14）。ops 不在这套体系内，故不给序号。 */
export const ROLE_RANK: Record<Exclude<Role, 'ops'>, number> = { student: 0, group: 1, class_admin: 2 }
export const ROLE_LABEL: Record<Role, string> = {
  student: '学生',
  group: '综测小组',
  class_admin: '班级管理员',
  ops: '运维超管',
}

export interface User {
  sid: string
  name: string
  role: Role
  /** 从综测小组任命的副班管，仍使用 group 身份。 */
  isDeputy?: boolean
  /** 侧栏头像里的单字/双字缩写 */
  initial: string
  /** 头像下方那行灰字：身份或职责 */
  sub: string
  classId: number
  className: string
}

/* ---- §4 方案配置树 ---- */

export interface EnumOption {
  /** 写入 claim 并由后端精确匹配的稳定名称。 */
  label: string
  /** 选择该档位时自动带入的建议分，不是学生期望分或审核认定分的锁定值。 */
  score: number
  /** 可选的级联路径；旧模板未配置时，会从 label 中的“·”自动推断。 */
  path?: string[]
}

export type ScoreRule =
  | { type: 'per_unit'; unit: string; per: number; cap?: number }
  | { type: 'enum'; options: EnumOption[]; levels?: string[] }
  | { type: 'free'; min: number; max: number }
  | { type: 'threshold'; unit: string; minimum: number; award: number }

export interface EvidenceRule {
  required: boolean
  types: string[]
  maxMb: number
}

export interface SchemeItem {
  key: string
  name: string
  scoreRule: ScoreRule
  /** 同组只取最高一条（学生干部各职务、同一竞赛多奖） */
  exclusiveGroup?: string
  /** 多小项共享一个封顶（新闻拍照 + 写稿共享 5 分） */
  capGroup?: { key: string; cap: number }
  evidence?: EvidenceRule
  /** 学生在提交页标题下看到的说明，按 Markdown 渲染。 */
  note?: string
  /** 本学年归到这个小项名下的活动。不随模板复用（导出模板时会被剥掉）。 */
  activities?: SchemeActivity[]
  /** 由条件基础项转换而来。学生端用来把它和普通加分项区分开。 */
  claimableBase?: { full: number; minimum: number; unit: string }
}

export interface SchemeBaseItem {
  key: string
  name: string
  full: number
  /** 开启后学生可凭佐证在 0..full 内自报；minimum 描述拿满 full 的条件。 */
  studentClaim?: {
    minimum: number
    unit: string
    evidence?: EvidenceRule
  }
  /** 附加在自动生成的达标条件之后，学生在提交页可见。 */
  note?: string
}
export interface SchemePenaltyItem {
  key: string
  name: string
  per: number
}

export interface SchemeCategory {
  key: CategoryKey
  name: string
  maxTotal: number
  /** 该大项分数怎么算出来的。专业素质不是申报累加，而是加权平均。 */
  formula?: SchemeFormula
  /** 大项级说明（Markdown）：哪些课算、哪些不算、等次怎么折百分制。 */
  note?: string
  baseItems: SchemeBaseItem[]
  penaltyItems: SchemePenaltyItem[]
  items: SchemeItem[]
}

/** 活动汇总表的一行：本学年实际办过、且归到某个小项名下的活动。
 *  score 是自由文本——原表每行的措辞不一样（「1h/0.25 分」「组织者 2 分」
 *  「省级一等奖(10分/5分)」），归一化就等于替学院发明规则。 */
export interface SchemeActivity {
  name: string
  date?: string
  level?: string
  org?: string
  score?: string
}

/** 一条分数线，不是数学语言。分子分母同时留空时退化成一行等式。 */
export interface SchemeFormula {
  lhs: string
  numerator?: string
  denominator?: string
}

export type CategoryKey = 'major' | 'moral' | 'practice' | 'health'

/** 教务导入、学生不申报的大项。后端 scheme.ImportedCategoryKey 是同一个约定。 */
export const IMPORTED_CATEGORY_KEY = 'major'

export function acceptsSubmissions(category: Pick<SchemeCategory, 'key'>): boolean {
  return category.key !== IMPORTED_CATEGORY_KEY
}

/** 窗口内并行开放的能力开关。没有"阶段"这个概念（§2.1）。 */
export interface Capabilities {
  submit: boolean
  edit: boolean
  appeal: boolean
  review: boolean
  arbitrate: boolean
  /** 学生匿名举报。默认关闭，由班级管理员在「功能开关」里打开；
      关掉只关入口，已经在复核中的举报仍然会走完。 */
  studentReport: boolean
  export: { on: boolean; gate: 'settlement' }
}

/* 方案只装评分规则。窗口、业务开关、评优比例属于班级的运行设置，存在
   class_timeline 上，由 /window 与 /admin/timeline 提供——改窗口不该让方案
   版本号加一，所以它们根本不在这个结构里。 */
export interface SchemeConfig {
  schemeName: string
  /** 内部版本号。提交快照、结算与审计都靠它，但不要印给学生看——见 publishedAt。 */
  version: string
  /** 这一版发布的时间。学生端一律显示它：「v7」说明不了任何事，
   *  「最后更新 8-20」才回答得了「我上次看的规则变了没有」。 */
  publishedAt?: string
  weights: Record<CategoryKey, number>
  categories: SchemeCategory[]
}

/* ---- §5 状态机 ---- */

/** 驳回不是独立状态，它等于「已定分（0 分 + 理由）」。 */
export type SubmissionStatus =
  | 'draft'
  | 'pending'
  | 'consensus'
  | 'scored'
  | 'appealing'
  | 'arbitrating'
  | 'locked'

export const STATUS_LABEL: Record<SubmissionStatus, string> = {
  draft: '草稿',
  pending: '待审核',
  consensus: '合议',
  scored: '已定分',
  appealing: '申诉中',
  arbitrating: '仲裁中',
  locked: '终态锁定',
}

/* 提交条目、审核记录、申诉、结算闸门这些"有传输形状"的类型都在 api/types.ts。
   那边的字段名直接对着后端 JSON，不在这里再定义一份人话版——
   两份形状一旦并存，页面就会开始在它们之间来回翻译。 */
