/* 申报值 → 期望分。学生提交页与方案编辑器的「学生端预览」共用这一份。

   两边共用是有意的：预览的全部意义就是"学生实际会看到什么、算出多少"，
   各写一份迟早对不上，那预览就成了骗自己。

   口径对齐后端 internal/scheme/score.go：
   先按规则算原始分，再按小项 cap 封顶。互斥组与共享封顶组是跨条目的，
   单条算不出来，只在界面上提示，不参与这里的计算。 */

import type { ScoreRule, SchemeItem } from './types'
import type { Claim } from '@/api/types'
import { evidenceTypesLabel } from './evidence.ts'

export interface Expected {
  /** 未填/非法时为 null，表示"待审核人认定"。 */
  raw: number | null
  final: number | null
  capped: boolean
}

export function expected(rule: ScoreRule, qty: string, level: number, free: string): Expected {
  if (rule.type === 'per_unit') {
    const n = Number(qty)
    if (!qty.trim() || Number.isNaN(n) || n < 0) return { raw: null, final: null, capped: false }
    const raw = n * rule.per
    const final = rule.cap != null ? Math.min(raw, rule.cap) : raw
    return { raw, final, capped: rule.cap != null && raw > rule.cap }
  }
  if (rule.type === 'enum') {
    const o = rule.options[level]
    if (!o) return { raw: null, final: null, capped: false }
    const n = free.trim() === '' ? o.score : Number(free)
    if (Number.isNaN(n)) return { raw: null, final: null, capped: false }
    const { lo, hi } = scoreBounds(rule)
    const final = Math.min(Math.max(n, lo), hi)
    return { raw: n, final, capped: final !== n }
  }
  if (rule.type === 'threshold') {
    const n = Number(qty)
    if (!qty.trim() || Number.isNaN(n) || n < 0) return { raw: null, final: null, capped: false }
    const score = n >= rule.minimum ? rule.award : 0
    return { raw: score, final: score, capped: false }
  }
  const n = Number(free)
  if (!free.trim() || Number.isNaN(n)) return { raw: null, final: null, capped: false }
  const final = Math.min(Math.max(n, rule.min), rule.max)
  return { raw: n, final, capped: final !== n }
}

/** 认定分允许的区间，对齐后端 scoreWithinRule。
    审核工作台、申诉复评、扣分与异议三处都要拦这一道，口径只能有一份。 */
export function scoreBounds(rule: ScoreRule): { lo: number; hi: number } {
  if (rule.type === 'free') return { lo: rule.min, hi: rule.max }
  if (rule.type === 'per_unit') return { lo: 0, hi: rule.cap ?? Infinity }
  if (rule.type === 'threshold') return { lo: 0, hi: rule.award }
  return { lo: 0, hi: Math.max(0, ...rule.options.map((o) => o.score)) }
}

export function buildClaim(rule: ScoreRule, qty: string, level: number, free: string): Claim {
  if (rule.type === 'per_unit') {
    /* 留空是合法的：per_unit 允许学生不填数量，交给审核人依据佐证认定。
       后端 prepareSubmission 对这种情况特意放行，前端别自作主张填 0。 */
    return qty.trim() === '' ? {} : { quantity: Number(qty) }
  }
  if (rule.type === 'enum') {
    const option = rule.options[level]
    return { option: option?.label ?? '', score: free.trim() === '' ? option?.score : Number(free) }
  }
  if (rule.type === 'threshold') return qty.trim() === '' ? {} : { quantity: Number(qty) }
  return free.trim() === '' ? {} : { score: Number(free) }
}

/** buildClaim 的逆向：把存下来的 claim 读回表单的三件套，用于继续编辑草稿。
    enum 按 label 找回档次；旧草稿没有 score 时，用该档位建议分补齐可编辑的期望分。
    方案改过档次名就找不回来，
    这时落到第一档并由界面提示学生自己确认，好过静默填一个错的分。 */
export function readClaim(rule: ScoreRule, claim: Claim): { qty: string; level: number; free: string } {
  const blank = { qty: '', level: 0, free: '' }
  if (rule.type === 'per_unit' || rule.type === 'threshold') {
    return claim.quantity == null ? blank : { ...blank, qty: String(claim.quantity) }
  }
  if (rule.type === 'enum') {
    const found = rule.options.findIndex((option) => option.label === claim.option)
    const level = found < 0 ? 0 : found
    const score = claim.score ?? rule.options[level]?.score
    return { ...blank, level, free: score == null ? '' : String(score) }
  }
  return claim.score == null ? blank : { ...blank, free: String(claim.score) }
}

/** 档次名对不上（方案改过档次）时为 true，界面要提醒学生复核这一档。 */
export function claimOptionLost(rule: ScoreRule, claim: Claim) {
  return rule.type === 'enum' && !!claim.option && !rule.options.some((option) => option.label === claim.option)
}

export const evidenceHint = (item: SchemeItem) => {
  const e = item.evidence
  if (!e) return '未配置佐证要求'
  return `${e.required ? '必传' : '选传'} · ${evidenceTypesLabel(e.types)} · 单份 ≤ ${e.maxMb} MB`
}
