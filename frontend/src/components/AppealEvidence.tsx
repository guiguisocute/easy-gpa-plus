/* 申诉的补传佐证。

   后端早就有这条路（POST /appeals/:id/evidence/presign + complete，router.go:78-79），
   queries.ts 里的 uploadAppealEvidence 也一直写着，但界面上从来没有入口——
   学生端的申诉表单甚至写着"补传在申诉详情里做"，而那个详情不存在。这里把它接上。

   为什么补传是"递交之后"才做：POST /appeals 一次就把申诉建出来了，
   写理由的那一刻还没有 appeal id，没有东西可以往上传。要让学生在写的时候就能贴图，
   得先有申诉草稿——那是后端的活，写在 docs/RICHTEXT-PASTE-BACKEND-CONTRACT.md。 */

import { EvidenceUploader } from '@/components/EvidenceUploader'
import { useApp } from '@/stores/app'
import { useAppeal, useInvalidateAppeal, uploadAppealEvidence } from '@/api/queries'

export function AppealEvidence({
  appealId,
  open,
  title = '补传佐证',
  emptyHint = '还没有补传任何材料。',
}: {
  appealId: string
  open: boolean
  title?: string
  emptyHint?: string
}) {
  const say = useApp((s) => s.say)
  const detail = useAppeal(open ? appealId : null)
  const refresh = useInvalidateAppeal()

  if (!open) return null

  return (
    <EvidenceUploader
      items={detail.data?.evidence ?? []}
      loading={detail.isLoading}
      title={title}
      emptyHint={emptyHint}
      upload={(file, onProgress) => uploadAppealEvidence(appealId, file, onProgress)}
      onUploaded={(count) => {
        refresh(appealId)
        say(count > 1 ? `${count} 份佐证已补传，复评人看得到` : '佐证已补传，复评人看得到')
      }}
    />
  )
}
