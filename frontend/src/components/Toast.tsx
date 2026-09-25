import { useApp } from '@/stores/app'

/* 底部居中的短提示。role="status" 让读屏软件在不抢焦点的前提下播报。 */
export default function Toast() {
  const toast = useApp((s) => s.toast)
  if (!toast) return null
  return (
    <div
      role="status"
      aria-live="polite"
      className="app-toast"
      style={{
        position: 'fixed',
        left: '50%',
        bottom: 30,
        zIndex: 120,
        transform: 'translateX(-50%)',
        animation: 'toastin .2s ease both',
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        padding: '11px 18px',
        border: '1px solid var(--line)',
        background: 'var(--bg)',
        boxShadow: '0 10px 34px rgba(0,0,0,.16)',
        fontSize: 12.5,
        color: 'var(--fg)',
        maxWidth: 'calc(100vw - 32px)',
      }}
    >
      <span style={{ width: 5, height: 5, background: 'var(--red)', flex: 'none' }} />
      <span style={{ textWrap: 'pretty', minWidth: 0, overflowWrap: 'anywhere' }}>{toast.msg}</span>
    </div>
  )
}
