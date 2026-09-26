/* 两级档位的矩阵：级别竖排、等次横排，学生选中的那一格高亮。

   原来这个组件私藏在 RuleCard.tsx 里，于是只有审核台看得到表格，学生在提交页
   只有一串级联胶囊——想知道「二等和一等差多少分」得一档一档点过去。同一份档位
   本来就该长成同一个样子，所以提出来给两边共用。

   档名与分值原样取自已发布方案，不做换算、合并或改写：任何"我们理解的规则"
   都会和文件漂移。 */

import type { CSSProperties } from 'react'
import { enumOptionPath } from '@/lib/schemeTree'
import { num } from '@/lib/style'
import * as f from '@/lib/format'
import type { EnumRule } from '@/lib/tierMatrix'

const uniq = (values: string[]) => [...new Set(values)]

export function TierMatrix({ rule, picked }: { rule: EnumRule; picked?: string }) {
  const entries = rule.options.map((option) => ({ option, path: enumOptionPath(option) }))
  const rows = uniq(entries.map((entry) => entry.path[0]))
  const cols = uniq(entries.map((entry) => entry.path[1]))
  const cell = (row: string, col: string) => entries.find((entry) => entry.path[0] === row && entry.path[1] === col)
  const hit = entries.find((entry) => entry.option.label === picked)

  const head: CSSProperties = { fontSize: 12, color: 'var(--fg3)', padding: '0 8px 9px', borderBottom: '1px solid var(--line)' }

  return (
    <div data-r="scroll">
      <div style={{ minWidth: 60 + cols.length * 62, display: 'grid', gridTemplateColumns: `minmax(72px,auto) repeat(${cols.length},minmax(0,1fr))` }}>
        <div style={{ ...head, fontWeight: 600, color: 'var(--fg2)' }}>{rule.levels?.[0] ?? ''}</div>
        {cols.map((col) => (
          <div key={col} style={{ ...head, textAlign: 'center', color: hit?.path[1] === col ? 'var(--fg)' : 'var(--fg3)', fontWeight: hit?.path[1] === col ? 600 : 400 }}>
            {col}
          </div>
        ))}
        {rows.map((row) => (
          <div key={row} style={{ display: 'contents' }}>
            <div style={{ fontSize: 13, padding: '11px 8px', borderBottom: '1px solid var(--line2)', color: hit?.path[0] === row ? 'var(--fg)' : 'var(--fg2)', fontWeight: hit?.path[0] === row ? 600 : 400 }}>
              {row}
            </div>
            {cols.map((col) => {
              const found = cell(row, col)
              const on = !!found && found.option.label === picked
              return (
                <div
                  key={col}
                  title={found ? `${row} · ${col}` : undefined}
                  style={{
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    fontSize: 14.5, fontWeight: on ? 700 : 500,
                    color: on ? 'var(--red)' : found ? 'var(--fg)' : 'var(--fg3)',
                    background: on ? 'var(--redBg)' : 'transparent',
                    boxShadow: on ? 'inset 0 0 0 1px var(--red)' : undefined,
                    padding: '11px 6px', borderBottom: '1px solid var(--line2)', ...num,
                  }}
                >
                  {found ? f.score(found.option.score) : '—'}
                </div>
              )
            })}
          </div>
        ))}
      </div>
    </div>
  )
}
