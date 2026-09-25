import type { AICandidate, Claim } from '../api/types.ts'
import type { SchemeItem, ScoreRule } from './types.ts'

export interface AIMaterialItem {
  categoryKey: string
  categoryName: string
  item: SchemeItem
}

export function candidateItem(candidate: AICandidate, items: AIMaterialItem[]) {
  return items.find((entry) => entry.categoryKey === candidate.categoryKey && entry.item.key === candidate.itemKey)
}

/** 档位与建议分一起替换，不能把 AI 原先的分数或数量带到手动选择的新档位。 */
export function chooseEnumClaim(rule: Extract<ScoreRule, { type: 'enum' }>, label: string): Claim {
  const option = rule.options.find((entry) => entry.label === label)
  return option ? { option: option.label, score: option.score } : {}
}

export function resetClaim(rule: ScoreRule, previous: Claim): Claim {
  if (rule.type === 'per_unit' || rule.type === 'threshold') {
    return previous.quantity == null ? {} : { quantity: previous.quantity }
  }
  if (rule.type === 'enum') {
    const option = rule.options.find((entry) => entry.label === previous.option)
    return option ? { option: option.label, score: previous.score ?? option.score } : {}
  }
  return previous.score == null ? {} : { score: previous.score }
}

/** 只拦非法填写，不拦未填和 AI 的待核对提示；草稿允许不完整，正式提交仍由服务端把关。 */
export function applyProblem(candidates: AICandidate[], items: AIMaterialItem[]) {
  if (!candidates.length) return '至少保留一条候选申报'
  for (const candidate of candidates) {
    const entry = candidateItem(candidate, items)
    if (!entry) return `候选「${candidate.title || candidate.id}」还没有选择有效小项`
    if (!candidate.title.trim()) return '请补全每条候选的事项名称'
    if (!candidate.assets.length) return `候选「${candidate.title}」没有佐证材料`
    const rule = entry.item.scoreRule
    if (rule.type === 'enum' && candidate.claim.option && !rule.options.some((option) => option.label === candidate.claim.option)) {
      return `「${candidate.title}」的档次不在当前方案里，请重新选择或留空`
    }
    if (rule.type === 'enum' && candidate.claim.score != null) {
      const max = Math.max(0, ...rule.options.map((option) => option.score))
      if (!Number.isFinite(candidate.claim.score) || candidate.claim.score < 0 || candidate.claim.score > max) {
        return `「${candidate.title}」的期望加分必须在 0—${max} 之间`
      }
    }
    if (candidate.claim.quantity != null && (!Number.isFinite(candidate.claim.quantity) || candidate.claim.quantity < 0)) {
      return `「${candidate.title}」的数量必须是非负数字`
    }
    if (rule.type === 'free' && candidate.claim.score != null && (!Number.isFinite(candidate.claim.score) || candidate.claim.score < rule.min || candidate.claim.score > rule.max)) {
      return `「${candidate.title}」的自报分必须在 ${rule.min}—${rule.max} 之间`
    }
  }
  return null
}
