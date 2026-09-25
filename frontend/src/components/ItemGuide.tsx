/* 提交页、方案预览和「我的提交」展开区共用的小项标题区。

   标题单独一行；档位清单不再堆进副标题。条件基础项用一条红框说明
   「必须申报才计分」。细则按 Markdown 列在标题下面，不进期望加分预览。 */

import type { ReactNode } from 'react'
import { RichText } from '@/components/Markdown'
import { itemGuideMarkdown } from '@/lib/itemGuide'
import type { SchemeItem } from '@/lib/types'

export function ItemGuide({
  name,
  item,
  readOnlyHint,
  extra,
  embedded = false,
}: {
  name: string
  item?: SchemeItem | null
  readOnlyHint?: string
  extra?: ReactNode
  /** 嵌在「我的提交」展开区里：不再套一层标题卡片底。 */
  embedded?: boolean
}) {
  const guide = item ? itemGuideMarkdown(item) : ''
  const claim = item?.claimableBase

  return (
    <div style={{
      background: embedded ? 'transparent' : 'var(--sub)',
      borderBottom: embedded ? 0 : '1px solid var(--line)',
      padding: embedded ? 0 : '18px 22px',
      display: 'flex',
      flexDirection: 'column',
      gap: 14,
    }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 18, flexWrap: 'wrap' }}>
        <div style={{ fontSize: embedded ? 15 : 19, fontWeight: 600, letterSpacing: '-.03em', minWidth: 0, flex: 1 }}>{name}</div>
        {extra}
      </div>

      {claim && (
        <div style={{ border: '1px solid var(--red)', background: 'var(--redBg)', padding: '11px 13px', display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--red)', letterSpacing: '.01em' }}>条件基础分</span>
          <span style={{ fontSize: 12.5, color: 'var(--fg)', lineHeight: 1.7 }}>
            须申报并上传佐证，方可计入本大项基础分。未申报不计分。
          </span>
        </div>
      )}

      {readOnlyHint && (
        <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.7, textWrap: 'pretty' }}>{readOnlyHint}</div>
      )}

      {guide && (
        <div style={{ borderTop: `1px solid ${embedded ? 'var(--line2)' : 'var(--line)'}`, paddingTop: 14, display: 'flex', flexDirection: 'column', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)', letterSpacing: '.01em' }}>小项说明</span>
          <RichText value={guide} />
        </div>
      )}
    </div>
  )
}
