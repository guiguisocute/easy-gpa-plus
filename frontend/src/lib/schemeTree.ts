import { acceptsSubmissions, type EnumOption, type SchemeBaseItem, type SchemeCategory, type SchemeConfig, type SchemeItem, type SubmissionStatus } from './types.ts'
import { COMMON_EVIDENCE_TYPES, DEFAULT_EVIDENCE_MAX_MB } from './evidence.ts'
import { claimableBaseNote, plainNoteToMarkdown } from './itemGuide.ts'

const OTHER_SELF_REPORT_PREFIX = '__other_self_report_'

export function otherSelfReportKey(categoryKey: string) {
  return `${OTHER_SELF_REPORT_PREFIX}${categoryKey}`
}

/* 导入类大项在树上仍然要占一格——它是总分的 60%，点不开反而像漏了。
   但它没有小项可选，所以给一个合成 key，选中后展示的是「这一项怎么算」，
   不是提交表单。前缀和兜底项一样带 __，不会和方案里的真 key 撞上。 */
const CATEGORY_BRIEF_PREFIX = '__category_brief_'

export function categoryBriefKey(categoryKey: string) {
  return `${CATEGORY_BRIEF_PREFIX}${categoryKey}`
}

export function findCategoryBrief(config: SchemeConfig, key: string | null): SchemeCategory | null {
  if (!key?.startsWith(CATEGORY_BRIEF_PREFIX)) return null
  const categoryKey = key.slice(CATEGORY_BRIEF_PREFIX.length)
  return config.categories.find((category) => category.key === categoryKey) ?? null
}

/**
 * 每个大项都提供一个系统兜底项。它不写回模板，因此旧的已发布方案也会立即拥有；
 * 后端用同一个稳定 key 生成并冻结规则快照，是真正可提交的小项，不是前端占位符。
 */
export function otherSelfReportItem(category: SchemeCategory): SchemeItem {
  return {
    key: otherSelfReportKey(category.key),
    name: '其他项目自报',
    scoreRule: { type: 'free', min: 0, max: category.maxTotal },
    evidence: { required: true, types: [...COMMON_EVIDENCE_TYPES], maxMb: DEFAULT_EVIDENCE_MAX_MB },
    note: '- 仅在现有小项均不适用时使用。\n- 请写明项目类别、级别、时间、主办方及自报依据，最终分值由审核人认定。',
  }
}

/** 不需要学生申报、没有人工扣减时就会自动计入的大项基础分。 */
export function automaticBaseScore(category: SchemeCategory): number {
  return category.baseItems.reduce((total, item) => total + (item.studentClaim ? 0 : item.full), 0)
}

export type ClaimableBaseState = 'unsubmitted' | 'draft' | 'reviewing' | 'appealing' | 'scored'

/**
 * 条件基础项走 submission，而不是 base_score。这里汇总它的真实进度，
 * 但始终按基础项满分封顶，避免把未申报分数混进自动基础分。
 */
export function claimableBaseProgress(
  submissions: { status: SubmissionStatus; finalScore: number | null }[],
  fullScore: number,
): { state: ClaimableBaseState; score: number; hasDecision: boolean } {
  const statuses = new Set(submissions.map((submission) => submission.status))
  const hasDecision = submissions.some((submission) => submission.finalScore !== null)
  const score = Math.min(
    fullScore,
    submissions.reduce((total, submission) => total + Math.max(0, submission.finalScore ?? 0), 0),
  )

  if (statuses.has('appealing') || statuses.has('arbitrating')) return { state: 'appealing', score, hasDecision }
  if (statuses.has('pending') || statuses.has('consensus')) return { state: 'reviewing', score, hasDecision }
  if (statuses.has('draft')) return { state: 'draft', score, hasDecision }
  if (hasDecision || statuses.has('scored') || statuses.has('locked')) return { state: 'scored', score, hasDecision }
  return { state: 'unsubmitted', score: 0, hasDecision: false }
}

export function studentClaimBaseItem(base: SchemeBaseItem): SchemeItem | null {
  if (!base.studentClaim) return null
  const generated = claimableBaseNote({ full: base.full, minimum: base.studentClaim.minimum, unit: base.studentClaim.unit })
  const extra = base.note?.trim() ? plainNoteToMarkdown(base.note.trim()) : ''
  return {
    key: base.key,
    name: base.name,
    scoreRule: { type: 'free', min: 0, max: base.full },
    evidence: base.studentClaim.evidence,
    note: extra ? `${generated}\n\n${extra}` : generated,
    claimableBase: { full: base.full, minimum: base.studentClaim.minimum, unit: base.studentClaim.unit },
  }
}

/** 规则快照里没有 claimableBase。对照当前方案把条件基础项重新标上，提交页和「我的提交」才是同一套外观。 */
export function attachClaimableBase(item: SchemeItem, config: SchemeConfig | undefined): SchemeItem {
  if (item.claimableBase || !config) return item
  for (const category of config.categories) {
    const base = category.baseItems.find((candidate) => candidate.key === item.key)
    if (base?.studentClaim) {
      return {
        ...item,
        claimableBase: { full: base.full, minimum: base.studentClaim.minimum, unit: base.studentClaim.unit },
      }
    }
  }
  return item
}

export function claimableItems(category: SchemeCategory): SchemeItem[] {
  /* 导入类大项（专业素质）一条都不可申报——连兜底的「其他项目自报」都不给，
     否则学生填完、审核完，分数在结算时照样被丢掉。后端 prepare 也会拒。 */
  if (!acceptsSubmissions(category)) return []
  const key = otherSelfReportKey(category.key)
  const configured = category.items.find((item) => item.key === key)
  const claimableBase = category.baseItems.map(studentClaimBaseItem).filter((item): item is SchemeItem => item !== null)
  return [configured ?? otherSelfReportItem(category), ...claimableBase, ...category.items.filter((item) => item.key !== key)]
}

/**
 * 「大项 / 小项」的人话。归类在接口里是 moral / moral_dorm 这样的稳定键，
 * 直接印到页面上只有写代码的人看得懂——申诉、仲裁、成绩核对三处都要过这一道。
 * 方案里找不到（比如旧快照里的小项已被删）就退回原始键，不假装认识它。
 */
export function schemePathName(config: SchemeConfig | undefined, category: string, itemKey: string): string {
  const found = config?.categories.find((row) => row.key === category)
  /* 申报项、基础项、扣分项都要能查到：申诉的对象是三类，只查申报项的话
     扣分项会把 moral_pen_absence 这种键原样印到学生眼前。 */
  const name =
    found &&
    (claimableItems(found).find((row) => row.key === itemKey)?.name ??
      found.baseItems.find((row) => row.key === itemKey)?.name ??
      found.penaltyItems.find((row) => row.key === itemKey)?.name)
  return `${found?.name ?? category} / ${name ?? itemKey}`
}

/** 旧模板用“国家级·一等奖”表达层级；新模板也可显式给 path。 */
export function enumOptionPath(option: EnumOption): string[] {
  const explicit = option.path?.map((part) => part.trim()).filter(Boolean)
  if (explicit?.length) return explicit
  const inferred = option.label.split('·').map((part) => part.trim()).filter(Boolean)
  return inferred.length ? inferred : [option.label]
}

export interface ReadOnlySchemeItem {
  key: string
  name: string
  kind: 'base' | 'penalty'
  category: SchemeCategory
}

export function findReadOnlySchemeItem(config: SchemeConfig, key: string | null): ReadOnlySchemeItem | null {
  if (!key) return null
  for (const category of config.categories) {
    const base = category.baseItems.find((item) => !item.studentClaim && item.key === key)
    if (base) return { key: base.key, name: base.name, kind: 'base', category }
    const penalty = category.penaltyItems.find((item) => item.key === key)
    if (penalty) return { key: penalty.key, name: penalty.name, kind: 'penalty', category }
  }
  return null
}
