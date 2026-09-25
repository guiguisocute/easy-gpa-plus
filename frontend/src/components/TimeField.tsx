import type { ReactNode } from 'react'
import { fieldStyle } from '@/lib/style'

/** 时间窗口的共同输入；保存或发起提案由调用页面决定。 */
export function TimeField({
  label,
  value,
  onChange,
  hint,
  min,
  extra,
  disabled,
  required,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  hint: string
  min?: string
  disabled?: boolean
  required?: boolean
  extra?: ReactNode
}) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
      <span style={{ display: 'flex', alignItems: 'baseline', gap: 10, minHeight: 17 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>{label}</span>
        {extra}
      </span>
      <input
        className="hv-bfg"
        type="datetime-local"
        value={value}
        min={min}
        disabled={disabled}
        required={required}
        onChange={(e) => onChange(e.target.value)}
        style={{ ...fieldStyle, font: "400 14px/1.4 'JetBrains Mono',monospace" }}
      />
      <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.7, textWrap: 'pretty' }}>
        {hint}
      </span>
    </label>
  )
}
