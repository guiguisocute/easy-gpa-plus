/* 审核台共用件。综测小组的「初审工作台」和共治的「我的评审」是同一件事：
   核对佐证与规则，填一个认定分，写一段学生看得到的理由，交完等同伴。
   两边曾经各画一套——共治那边只剩一个下拉、一个没有边界提示的数字框和一块
   纯文本 textarea，同样的判断在两种模式下看起来像两件不同的事。

   这里只放形状，不放业务：组件收数据和回调，不发请求、不判权限。
   谁能审、审完往哪送，仍由各自页面和后端决定。 */

import type { ReactNode } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { mono, num } from '@/lib/style'
import { ChoiceChip } from '@/components/ui'
import type { Evidence, ReviewDecision } from '@/api/types'
import type { DeskChoice } from '@/lib/reviewDesk'
import '@/styles/mobile-pages.css'

export function DecisionChips({ choices, value, onChange }: { choices: DeskChoice[]; value: ReviewDecision; onChange: (key: ReviewDecision) => void }) {
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 9 }}>
      {choices.map((c) => (
        <ChoiceChip key={c.key} on={c.key === value} tone={c.tone} onClick={() => onChange(c.key)}>
          <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', flex: 'none' }}>
            <path d={c.icon} />
          </svg>
          {c.label}
        </ChoiceChip>
      ))}
    </div>
  )
}

/** 认定分输入。数字比周围大一号，越界当场变红——后端还会再验一次。 */
export function DeskScoreField({ label, value, onChange, invalid, hint, placeholder, disabled }: {
  label: ReactNode
  value: string
  onChange: (value: string) => void
  invalid?: boolean
  hint?: ReactNode
  placeholder?: string
  disabled?: boolean
}) {
  const bad = !!invalid && value.trim() !== ''
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 20 }}>
      <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>{label}</span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        inputMode="decimal"
        disabled={disabled}
        placeholder={placeholder}
        style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${bad ? 'var(--red)' : 'var(--warn)'}`, padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
      />
      {bad && hint && <span style={{ fontSize: 12, color: 'var(--red)' }}>{hint}</span>}
    </label>
  )
}

/** 右栏顶上那条：这一项现在几分、规则允许到几分。 */
export function ExpectedScoreBar({ label, value, unit, tone, side }: {
  label: string
  value: string
  unit?: string
  tone?: string
  side?: ReactNode
}) {
  return (
    <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '13px 18px', display: 'flex', alignItems: 'baseline', gap: 9, flexWrap: 'wrap' }}>
      <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>{label}</span>
      <span style={{ fontSize: 30, fontWeight: 600, letterSpacing: '-.045em', color: tone ?? 'var(--fg)', ...num }}>{value}</span>
      {unit && <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{unit}</span>}
      {side && <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>{side}</span>}
    </div>
  )
}

/** 右栏里一块带底色的卡片：要动手的地方都长这样。 */
export function DeskPanel({ title, tone, children }: { title: ReactNode; tone?: string; children: ReactNode }) {
  return (
    <div style={{ border: `1px solid ${tone ?? 'var(--line)'}`, padding: 20, background: 'var(--sub)' }}>
      <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', paddingBottom: 12 }}>{title}</div>
      {children}
    </div>
  )
}

/** 左栏的佐证区。没有附件也要明说一句，"他没传"和"我没看见"是两回事。 */
export function EvidencePane({ items, title = '佐证材料', note, empty = '这条材料没有佐证附件。' }: {
  items: Evidence[]
  title?: string
  note?: string
  empty?: string
}) {
  return (
    <section aria-label={title}>
      <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 14 }}>
        {title}{note ? ` · ${note}` : ''}
      </div>
      {items.length === 0 ? (
        <div style={{ fontSize: 12.5, color: 'var(--fg3)', border: '1px solid var(--line)', padding: '14px 16px' }}>{empty}</div>
      ) : (
        <EvidenceFiles items={items} />
      )}
    </section>
  )
}

/** 同案其他人交了没有——只显示交没交，永远不显示交了什么。 */
export function ReviewerProgress({ title = '评审进度', rows, note }: {
  title?: string
  rows: { who: string; state: string; tone: string; bold?: boolean }[]
  note?: ReactNode
}) {
  return (
    <div>
      <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 12 }}>{title}</div>
      {rows.map((row) => (
        <div key={row.who} style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0' }}>
          <span style={{ width: 5, height: 5, flex: 'none', background: row.tone }} />
          <span style={{ fontSize: 12.5, fontWeight: row.bold ? 600 : 400, color: 'var(--fg)' }}>{row.who}</span>
          <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>{row.state}</span>
        </div>
      ))}
      {note && (
        <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 12, textWrap: 'pretty' }}>{note}</div>
      )}
    </div>
  )
}

/* 右栏吸顶。左边的佐证和档位表能滚好几屏，而要下的判断始终在这一栏。
   offset 跟着这一页有没有那条 52px 的当前对象条走：有它就落到 57+52=109，
   可用高度相应少 52。横向 padding 与滚动条让道写在 global.css，五个台面一起改。 */
export function DeskSide({ children, offset = 109 }: { children: ReactNode; offset?: number }) {
  return (
    <div
      data-r="deskside"
      style={{ top: offset, maxHeight: `calc(100dvh - ${offset + 16}px)`, paddingBlock: 24, display: 'flex', flexDirection: 'column', gap: 24 }}
    >
      {children}
    </div>
  )
}

/* 队列条只铺首尾与当前附近几条，中间收成省略号：99 条全铺出来要占掉三行，
   反而看不出自己在哪一条。返回下标，'…' 表示一处断口。 */
function queueWindow(total: number, cur: number, span = 2): (number | '…')[] {
  if (total <= 9) return Array.from({ length: total }, (_, i) => i)
  const keep = new Set([0, total - 1])
  for (let i = cur - span; i <= cur + span; i++) if (i >= 0 && i < total) keep.add(i)
  const out: (number | '…')[] = []
  let prev = -1
  for (const i of [...keep].sort((a, b) => a - b)) {
    if (prev >= 0 && i - prev > 1) out.push('…')
    out.push(i)
    prev = i
  }
  return out
}

/** 已审的变绿留在原位；当前这一条实心。绿且实心＝正看着一条自己审过的。 */
function chipTone(on: boolean, finished: boolean) {
  return {
    border: `1px solid ${finished ? 'var(--ok)' : on ? 'var(--fg)' : 'var(--line)'}`,
    background: on ? (finished ? 'var(--ok)' : 'var(--fg)') : finished ? 'var(--okBg)' : 'transparent',
    color: on ? (finished ? 'var(--onAccent)' : 'var(--bg)') : finished ? 'var(--ok)' : 'var(--fg3)',
  }
}

export interface QueueItem {
  id: string
  title: string
  /** 悬停提示。不给就用标题——上一条／下一条那两个胶囊只放得下标题。 */
  tip?: string
  done?: boolean
}

/* 队列条。放在页尾而不是页头：进到这一页第一眼该看的是佐证与规则，
   不是还剩多少条。上一条／下一条各占一头，中间是可跳转的队列。 */
export function ReviewQueueBar({ items, index, onGo, hint }: {
  items: QueueItem[]
  index: number
  onGo: (index: number) => void
  hint?: ReactNode
}) {
  if (items.length === 0) return null
  const last = items.length - 1
  return (
    <div className="review-queue" style={{ display: 'flex', alignItems: 'center', gap: 16, borderTop: '1px solid var(--line)', padding: '14px 0 4px', flexWrap: 'wrap' }}>
      <button
        type="button"
        className="hv-line-fg"
        onClick={() => onGo(index - 1)}
        disabled={index === 0}
        style={{ display: 'flex', alignItems: 'center', gap: 11, background: 'none', border: '1px solid var(--line)', margin: 0, padding: '8px 16px 8px 11px', borderRadius: 999, font: 'inherit', cursor: index === 0 ? 'not-allowed' : 'pointer', color: index === 0 ? 'var(--fg3)' : 'var(--fg2)', textAlign: 'left', minWidth: 0, maxWidth: 240 }}
      >
        <ChevronLeft size={15} strokeWidth={1.7} style={{ flex: 'none' }} />
        <span style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
          <span style={{ fontSize: 11, color: 'var(--fg3)' }}>上一条</span>
          <span style={{ fontSize: 12.5, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {index > 0 ? items[index - 1].title : '已是第一条'}
          </span>
        </span>
      </button>

      <div className="review-queue-pages" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 9, margin: '0 auto', minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          {queueWindow(items.length, index).map((slot, at) =>
            slot === '…' ? (
              <span key={`gap${at}`} style={{ ...mono('11.5px', '0'), width: 18, textAlign: 'center' }}>…</span>
            ) : (
              <button
                key={items[slot].id}
                type="button"
                /* 当前这一条是实心块，hover 不能再把字染成 --fg——同 EnumOptionPicker。 */
                className={slot === index ? 'hv-op82' : 'hv-line-fg'}
                onClick={() => onGo(slot)}
                title={`${items[slot].tip ?? items[slot].title}${items[slot].done ? ' · 已提交结论' : ''}`}
                style={{
                  width: 30, height: 27, margin: 0, padding: 0,
                  font: "500 11.5px/1 'JetBrains Mono',monospace", cursor: 'pointer',
                  ...chipTone(slot === index, !!items[slot].done),
                }}
              >
                {slot + 1}
              </button>
            ),
          )}
        </div>
        {hint && (
          <span style={{ fontSize: 12.5, color: 'var(--fg3)', textAlign: 'center', textWrap: 'pretty' }}>{hint}</span>
        )}
      </div>

      <button
        type="button"
        className="hv-line-fg"
        onClick={() => onGo(index + 1)}
        disabled={index === last}
        style={{ display: 'flex', alignItems: 'center', gap: 11, background: 'none', border: '1px solid var(--line)', margin: 0, padding: '8px 11px 8px 16px', borderRadius: 999, font: 'inherit', cursor: index === last ? 'not-allowed' : 'pointer', color: index === last ? 'var(--fg3)' : 'var(--fg2)', textAlign: 'right', minWidth: 0, maxWidth: 240 }}
      >
        {/* 右对齐只能靠 textAlign，不能用 alignItems:flex-end——那会把子元素按内容宽度定尺寸，
            于是下面那行标题的 overflow:hidden 永远够不着边界，长标题会从胶囊里溢出来。 */}
        <span style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
          <span style={{ fontSize: 11, color: 'var(--fg3)' }}>下一条</span>
          <span style={{ fontSize: 12.5, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {index < last ? items[index + 1].title : '已是最后一条'}
          </span>
        </span>
        <ChevronRight size={15} strokeWidth={1.7} style={{ flex: 'none' }} />
      </button>
    </div>
  )
}
