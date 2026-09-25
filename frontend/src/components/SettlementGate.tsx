/* 结算闸门。导出中心和共治的结算检查读的是同一份 /gate，条件、顺序、措辞都一样，
   所以摆法也只留一份：条件逐条列清楚"这条过没过、还差什么"，而不是把不满足的
   几条挤成一行灰字——那行字回答不了"我现在该去做哪件事"。 */

import type { ReactNode } from 'react'
import { Note, Sub } from '@/components/ui'
import type { Gate } from '@/api/types'

const STEPS = [
  { i: '①', name: '取数', desc: '只算已经定分的条目。草稿、还在申诉的都不算' },
  { i: '②', name: '算分', desc: '每条材料按它对应的那条规则算分' },
  { i: '③', name: '封顶', desc: '依次卡小项、共享组、大项三层上限' },
  { i: '④', name: '合成', desc: '四项按权重加成总分。同分先看专业素质分，再看学号；四项都进线才算三好' },
]

export function SettlementSteps({ note }: { note?: ReactNode }) {
  return (
    <div style={{ padding: '26px 0 0' }}>
      <Sub title="结算流程" note="取数、算分、封顶、合成" />
      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 16 }}>
        {STEPS.map((s) => (
          <div key={s.i} style={{ border: '1px solid var(--line)', padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 8, minWidth: 0 }}>
            <span style={{ fontSize: 18, fontWeight: 600, color: 'var(--red)' }}>{s.i}</span>
            <span style={{ fontSize: 14, fontWeight: 600, letterSpacing: '-.02em' }}>{s.name}</span>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, textWrap: 'pretty' }}>{s.desc}</span>
          </div>
        ))}
      </div>
      <div style={{ marginTop: 16 }}>
        <Note>{note ?? '同一批数据，导出多少次结果都一样。'}</Note>
      </div>
    </div>
  )
}

/** 逐条开闸条件。绿勾是过了，红叉是还差；差在哪一条，后端已经写在 detail 里。 */
export function SettlementConditions({ gate, title = '开闸条件', note }: { gate: Gate; title?: string; note?: string }) {
  return (
    <section aria-label={title} style={{ padding: '26px 0 0' }}>
      <Sub title={title} note={note ?? `${gate.conditions.filter((c) => c.ok).length} / ${gate.conditions.length} 已满足`} />
      {gate.conditions.map((condition) => (
        <div
          key={condition.key}
          style={{ display: 'flex', alignItems: 'baseline', gap: 14, borderTop: '1px solid var(--line2)', padding: '13px 0', flexWrap: 'wrap' }}
        >
          <span aria-hidden style={{ width: 16, flex: 'none', color: condition.ok ? 'var(--ok)' : 'var(--red)', fontSize: 13, fontWeight: 600 }}>
            {condition.ok ? '✓' : '×'}
          </span>
          <span style={{ fontSize: 13, fontWeight: 500, color: 'var(--fg)', flex: 'none' }}>{condition.label}</span>
          <span style={{ fontSize: 12.5, color: condition.ok ? 'var(--fg3)' : 'var(--fg2)', minWidth: 0, textWrap: 'pretty' }}>
            {condition.detail}
          </span>
          <span style={{ marginLeft: 'auto', fontSize: 12.5, color: condition.ok ? 'var(--ok)' : 'var(--red)', flex: 'none' }}>
            {condition.ok ? '已满足' : '未满足'}
          </span>
        </div>
      ))}
    </section>
  )
}
