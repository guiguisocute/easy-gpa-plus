import type { ReactNode } from 'react'
import type { ExportKind } from '@/api/types'
import { EXPORT_PRODUCTS } from '@/lib/exportProducts'
import { mono } from '@/lib/style'

/** 两种工作方式共用文件清单；行尾操作由页面注入，组件不发起导出或授权。 */
export function ExportProducts({
  selected,
  badge,
  action,
}: {
  selected?: ExportKind | null
  badge?: ReactNode
  action: (kind: ExportKind) => ReactNode
}) {
  return (
    <div>
      {EXPORT_PRODUCTS.map((p) => (
        <div
          key={p.key}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 14,
            borderTop: `1px solid ${selected === p.key ? 'var(--fg)' : 'var(--line2)'}`,
            background: selected === p.key ? 'var(--sub)' : 'transparent',
            padding: '15px 10px',
            flexWrap: 'wrap',
          }}
        >
          <span style={{ ...mono('10.5px', '.06em'), width: 40, flex: 'none' }}>{p.fmt}</span>
          <span
            style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 200, flex: 1 }}
          >
            <span
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                fontSize: 13,
                fontWeight: 600,
                color: 'var(--fg)',
              }}
            >
              {p.name}
              {selected === p.key && badge}
            </span>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>
              {p.desc}
            </span>
          </span>
          {action(p.key)}
        </div>
      ))}
    </div>
  )
}
