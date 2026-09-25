/* 审核台当前对象。

   初审和申诉复评都必须先回答「我现在审的是谁」。学生姓名不能再塞进
   PageHead 的 12.5px 描述里，否则标题和操作都很显眼，真正的审核对象反而最弱。

   sticky 是审核台用的形态：佐证、档位表、材料清单都能滚好几屏，
   审核人滚到哪都得知道自己在审谁。吸顶那一条必须换成单行——立着的大卡片有一百多像素高，
   钉在顶栏下面等于把正文再切掉一块；高度固定 52px 写在 global.css 里，
   因为同页其他吸顶元素要按 57+52 落位。 */

import type { ReactNode } from 'react'
import { mono } from '@/lib/style'
import '@/styles/mobile-pages.css'

export function ReviewSubject({
  label,
  name,
  sid,
  meta,
  side,
  sticky,
}: {
  label: string
  name: string
  sid?: string | null
  meta?: ReactNode
  side?: ReactNode
  sticky?: boolean
}) {
  if (sticky) {
    return (
      <section
        data-r="reviewsubject"
        aria-label={`${label}：${name}`}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 12,
          boxSizing: 'border-box',
          padding: '0 16px',
          border: '1px solid var(--line)',
          borderLeft: '3px solid var(--red)',
          background: 'var(--sub)',
          whiteSpace: 'nowrap',
          overflow: 'hidden',
        }}
      >
        <span className="review-subject-label" style={{ fontSize: 11.5, fontWeight: 600, letterSpacing: '.06em', color: 'var(--fg3)', flex: 'none' }}>{label}</span>
        <div className="review-subject-identity" style={{ display: 'flex', alignItems: 'center', gap: 12, minWidth: 0 }}>
          <strong title={name} style={{ fontSize: 19, lineHeight: 1.15, fontWeight: 650, letterSpacing: '-.03em', color: 'var(--fg)', flex: 'none' }}>{name}</strong>
          {sid && (
            <span title={sid} style={{ ...mono('11.5px', '.02em'), border: '1px solid var(--line)', padding: '3px 7px', background: 'var(--bg)', flex: 'none' }}>
              {sid}
            </span>
          )}
        </div>
        {/* 当前条目、归类、提交时间这类随条目变的说明留在条上，但只留一行：
            放不下就省略号，不能让它把这一条撑成两行、顶穿下面的吸顶元素。 */}
        {meta && (
          <span data-r="hidesm" style={{ fontSize: 12.5, color: 'var(--fg3)', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {meta}
          </span>
        )}
        {side && <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 10, flex: 'none', paddingLeft: 8 }}>{side}</div>}
      </section>
    )
  }
  return (
    <section
      aria-label={`${label}：${name}`}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 20,
        padding: '16px 18px',
        border: '1px solid var(--line)',
        borderLeft: '3px solid var(--red)',
        background: 'var(--sub)',
        flexWrap: 'wrap',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0 }}>
        <span style={{ fontSize: 11.5, fontWeight: 600, letterSpacing: '.06em', color: 'var(--fg3)' }}>{label}</span>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, minWidth: 0, flexWrap: 'wrap' }}>
          <strong style={{ fontSize: 24, lineHeight: 1.15, fontWeight: 650, letterSpacing: '-.035em', color: 'var(--fg)' }}>{name}</strong>
          {sid && (
            <span style={{ ...mono('12px', '.02em'), border: '1px solid var(--line)', padding: '3px 8px', background: 'var(--bg)' }}>
              {sid}
            </span>
          )}
        </div>
        {meta && <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.65, textWrap: 'pretty' }}>{meta}</div>}
      </div>
      {side && <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>{side}</div>}
    </section>
  )
}
