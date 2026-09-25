import type { ComponentPropsWithoutRef, ReactNode } from 'react'

interface FileDropzoneProps extends Omit<ComponentPropsWithoutRef<'div'>, 'style' | 'title'> {
  active?: boolean
  disabled?: boolean
  icon?: ReactNode
  title: ReactNode
  hint?: ReactNode
  actions?: ReactNode
}

/** 所有业务上传入口共用的投放区；页面只提供文案、按钮和拖放行为。 */
export function FileDropzone({ active = false, disabled = false, icon, title, hint, actions, ...props }: FileDropzoneProps) {
  return (
    <div
      {...props}
      aria-disabled={disabled || undefined}
      style={{
        minHeight: 150,
        border: `1px dashed ${active ? 'var(--red)' : 'var(--line)'}`,
        background: active ? 'var(--redBg)' : 'var(--sub)',
        color: 'var(--fg2)',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 9,
        padding: 18,
        opacity: disabled ? 0.55 : 1,
        transition: 'border-color .16s ease, background .16s ease, opacity .16s ease',
      }}
    >
      {icon}
      <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', textAlign: 'center' }}>{title}</span>
      {hint && <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6, textAlign: 'center' }}>{hint}</span>}
      {actions && <div style={{ display: 'flex', gap: 10, marginTop: 4, flexWrap: 'wrap', justifyContent: 'center' }}>{actions}</div>}
    </div>
  )
}
