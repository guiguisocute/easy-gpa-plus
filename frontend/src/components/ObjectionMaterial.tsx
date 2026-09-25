import type { Objection } from '@/api/types'
import { ScoreItemMaterial } from '@/components/ScoreItemMaterial'

export function ObjectionMaterial({ item }: { item: Objection }) {
  return <ScoreItemMaterial item={item} context={{
    title: '提案依据', note: '生效后学生能看到这一段', reason: item.basis,
    evidenceTitle: '提案与裁定附件', evidence: item.noteEvidence,
  }} />
}
