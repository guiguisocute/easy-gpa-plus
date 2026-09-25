/* 学生自己写的补充说明。初审台、复评台、仲裁台共用。

   它和申诉理由是同一类东西：这一页上唯一由学生本人写的话。原来三个台面各画各的——
   初审台是一段 12.5px 的灰字加一条细线，连 Markdown 都不解析（学生在提交页用的是
   MarkdownEditor，写的列表到了审核人眼前变成一行 `- xxx - yyy`）；复评台和仲裁台是
   13px 的引用体，和旁边的元数据一个分量。审核人扫一眼就滑过去了。

   这里统一成一块有底色的卡片，正文走 RichText 的 lg 档，佐证引用照常解析。
   学生什么都没写时也明说一句——"他没写"和"我没看见"是两回事。 */

import { RichText } from '@/components/Markdown'
import type { Evidence } from '@/api/types'

export function StudentNote({
  value,
  evidence,
  title = '学生的补充说明',
  note = '按提交时的原文显示',
}: {
  value: string | null | undefined
  evidence?: Evidence[]
  title?: string
  note?: string
}) {
  const text = value?.trim() ?? ''
  return (
    <div style={{ border: '1px solid var(--line)', borderLeft: '3px solid var(--fg3)', background: 'var(--sub)', padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>{title}</span>
        <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{note}</span>
      </div>
      {text ? (
        <RichText value={text} evidence={evidence} size="lg" />
      ) : (
        <span style={{ fontSize: 13, color: 'var(--fg3)' }}>学生没有写补充说明。</span>
      )}
    </div>
  )
}
