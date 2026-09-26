import { useEffect, useState, type ReactNode } from 'react'
import { Btn, Note } from './ui'
import '@/styles/mobile-ops.css'

export function OpsTabs({ id, value, onChange, items }: {
  id: string; value: string; onChange: (value: string) => void
  items: readonly { id: string; label: string }[]
}) {
  return <div className="ops-tabs" role="tablist" aria-label="设置分类">
    {items.map((item, index) => <button key={item.id} type="button" role="tab" id={`${id}-tab-${item.id}`}
      aria-selected={value === item.id} aria-controls={`${id}-panel-${item.id}`} tabIndex={value === item.id ? 0 : -1}
      onClick={() => onChange(item.id)} onKeyDown={(event) => {
        const next = event.key === 'ArrowRight' ? (index + 1) % items.length
          : event.key === 'ArrowLeft' ? (index + items.length - 1) % items.length
            : event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : null
        if (next === null) return
        event.preventDefault(); onChange(items[next].id)
        document.getElementById(`${id}-tab-${items[next].id}`)?.focus()
      }}>{item.label}</button>)}
  </div>
}

/** Visit on demand; keep already visited forms mounted so changing a tab does
 * not throw away unsaved settings. Hidden panels do not participate in layout. */
export function OpsTabPanel({ id, name, active, children }: { id: string; name: string; active: string; children: ReactNode }) {
  const selected = active === name
  const [visited, setVisited] = useState(selected)
  useEffect(() => { if (selected) setVisited(true) }, [selected])
  if (!selected && !visited) return null
  return <section role="tabpanel" id={`${id}-panel-${name}`} aria-labelledby={`${id}-tab-${name}`} hidden={!selected} tabIndex={0}>{children}</section>
}

export function OpsSection({ title, desc, children, aside }: { title: string; desc?: string; children: ReactNode; aside?: ReactNode }) {
  return <section className="ops-section">
    <div className="ops-section-heading"><h2>{title}</h2>{desc && <p>{desc}</p>}{aside}</div>
    <div className="ops-section-content">{children}</div>
  </section>
}

export function OpsField({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return <label className="ops-field"><span className="ops-field-label">{label}</span>{children}{hint && <span className="ops-field-hint">{hint}</span>}</label>
}

export function OpsActions({ children, note }: { children: ReactNode; note?: string }) {
  return <div className="ops-actions"><div>{children}</div>{note && <span>{note}</span>}</div>
}

export function OpsLoadError({ title = '设置暂时无法读取', onRetry }: { title?: string; onRetry: () => void }) {
  return <div className="ops-load-error" role="alert"><Note tone="warn">{title}，请重新读取后再操作。</Note><Btn onClick={onRetry}>重新读取</Btn></div>
}
