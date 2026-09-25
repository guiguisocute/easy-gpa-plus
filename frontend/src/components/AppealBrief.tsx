/* 申诉台与仲裁台共用的两块：学生的诉求，和逐条翻页。

   学生自己写的那段话是这两个页面的主角——复评人和班级管理员打开页面，
   要回答的就是"他说的这一点成立吗"。原来它和旁边的元数据一样是 12.5px 灰字，
   还把 moral / moral_dorm 这种键直接印出来，主次完全看不出来。

   翻页条复用审核台样式：申诉常常是一批一批来的（同一场比赛十几个人一起申诉），
   处理完一条要退回列表再点下一条，来回几十趟。 */

import type { CSSProperties, ReactNode } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { RichText } from '@/components/Markdown'
import { num } from '@/lib/style'
import * as f from '@/lib/format'
import type { Evidence } from '@/api/types'

/** 「原认定 → 学生主张」的一行。归类一律传已经翻译好的中文名。 */
function ClaimLine({ label, path, score, tone }: { label: string; path: string; score: number | null | undefined; tone?: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, padding: '9px 0', borderTop: '1px solid var(--line2)', flexWrap: 'wrap' }}>
      <span style={{ fontSize: 12, color: 'var(--fg3)', flex: 'none', width: 60 }}>{label}</span>
      <span style={{ fontSize: 13.5, color: 'var(--fg)', minWidth: 0, textWrap: 'pretty' }}>{path}</span>
      <span style={{ marginLeft: 'auto', flex: 'none', fontSize: 17, fontWeight: 600, color: tone ?? 'var(--fg)', ...num }}>{f.score(score)}</span>
      <span style={{ flex: 'none', fontSize: 12, color: 'var(--fg3)' }}>分</span>
    </div>
  )
}

export function AppealBrief({
  reason,
  evidence,
  originalPath,
  originalScore,
  proposedPath,
  proposedScore,
  note,
  heading = '学生的申诉理由',
  proposedLabel = '他主张',
}: {
  reason: string
  evidence?: Evidence[]
  /** 非提交条目（基础项、扣分项）没有归类，传空就不渲染这两行。 */
  originalPath?: string
  originalScore?: number | null
  proposedPath?: string
  proposedScore?: number | null
  note?: string
  heading?: string
  proposedLabel?: string
}) {
  return (
    <div style={{ border: '1px solid var(--red)', borderLeft: '3px solid var(--red)', padding: '18px 20px', display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--red)' }}>{heading}</span>
        {note && <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{note}</span>}
      </div>
      <RichText value={reason} evidence={evidence} size="lg" />
      {originalPath && (
        <div style={{ paddingTop: 2 }}>
          <ClaimLine label="原认定" path={originalPath} score={originalScore} />
          <ClaimLine label={proposedLabel} path={proposedPath ?? originalPath} score={proposedScore} tone="var(--red)" />
        </div>
      )}
    </div>
  )
}

/** 逐条翻页。两个台面的列表顺序就是这里的顺序，翻到头就禁用。 */
export function StepBar({
  at,
  total,
  prevLabel,
  nextLabel,
  onPrev,
  onNext,
  middle,
}: {
  at: number
  total: number
  prevLabel: string
  nextLabel: string
  onPrev: () => void
  onNext: () => void
  middle?: ReactNode
}) {
  const noPrev = at <= 0
  const noNext = at < 0 || at >= total - 1
  const side = (disabled: boolean, right: boolean): CSSProperties => ({
    display: 'flex', alignItems: 'center', gap: 10, background: 'none',
    border: '1px solid var(--line)', margin: 0,
    padding: right ? '7px 10px 7px 15px' : '7px 15px 7px 10px',
    borderRadius: 999, font: 'inherit', cursor: disabled ? 'not-allowed' : 'pointer',
    color: disabled ? 'var(--fg3)' : 'var(--fg2)',
    textAlign: right ? 'right' : 'left', minWidth: 0, maxWidth: 250,
  })
  const stack: CSSProperties = { display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }
  const title: CSSProperties = { fontSize: 12.5, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }

  return (
    <div data-r="auditnav" style={{ display: 'flex', alignItems: 'center', gap: 14, background: 'var(--bg)', borderTop: '1px solid var(--line)', borderBottom: '1px solid var(--line)', padding: '11px 0', flexWrap: 'wrap' }}>
      <button type="button" className="hv-line-fg" onClick={onPrev} disabled={noPrev} style={side(noPrev, false)}>
        <ChevronLeft size={15} strokeWidth={1.7} style={{ flex: 'none' }} />
        <span style={stack}>
          <span style={{ fontSize: 11, color: 'var(--fg3)' }}>上一条</span>
          <span style={title}>{noPrev ? '已是第一条' : prevLabel}</span>
        </span>
      </button>
      <span style={{ margin: '0 auto', fontSize: 12.5, color: 'var(--fg3)', textAlign: 'center' }}>
        {middle ?? <>第 <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)', ...num }}>{at + 1}</span> / {total} 条</>}
      </span>
      <button type="button" className="hv-line-fg" onClick={onNext} disabled={noNext} style={side(noNext, true)}>
        <span style={stack}>
          <span style={{ fontSize: 11, color: 'var(--fg3)' }}>下一条</span>
          <span style={title}>{noNext ? '已是最后一条' : nextLabel}</span>
        </span>
        <ChevronRight size={15} strokeWidth={1.7} style={{ flex: 'none' }} />
      </button>
    </div>
  )
}
