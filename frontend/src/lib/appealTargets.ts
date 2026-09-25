import type { BaseItem, Submission } from '@/api/types'
import type { AppealTargetType } from '@/api/types'
import type { CategoryKey, SchemeConfig } from '@/lib/types'
import * as f from '@/lib/format'
import { schemePathName } from '@/lib/schemeTree'

/** 「发起申诉」页要列出的一笔：三类对象共用这个形状。 */
export interface AppealTarget {
  key: string
  type: AppealTargetType
  id: string
  title: string
  path: string
  score: number | null
  used: number
  category: CategoryKey
  itemKey: string
}

/** canAppeal 由后端算好，这里只汇总，不自己推。 */
export function collectAppealTargets(input: {
  canAppeal: boolean
  confirmed: boolean
  submissions: { items: Submission[] } | undefined
  baseItems: { items: BaseItem[] } | undefined
  scheme: SchemeConfig | undefined
}): AppealTarget[] {
  if (!input.canAppeal || input.confirmed) return []
  const out: AppealTarget[] = []
  for (const s of input.submissions?.items ?? []) {
    if (!s.canAppeal) continue
    out.push({
      key: `submission:${s.id}`,
      type: 'submission',
      id: s.id,
      title: f.submissionTitle(s.title),
      path: schemePathName(input.scheme, s.category, s.itemKey),
      score: s.finalScore,
      used: s.appealsUsed,
      category: s.category,
      itemKey: s.itemKey,
    })
  }
  for (const b of input.baseItems?.items ?? []) {
    if (!b.canAppeal || !b.id) continue
    const type: AppealTargetType = b.kind === 'penalty' ? 'penalty_score' : 'base_score'
    out.push({
      key: `${type}:${b.id}`,
      type,
      id: b.id,
      title: b.itemName,
      path: b.categoryName,
      score: b.score,
      used: b.appealsUsed,
      category: b.category,
      itemKey: b.itemKey,
    })
  }
  return out
}
