import { Field } from '@/components/ui'

/** 普通导入与共治核验共用输入格式，提交权限与整批校验仍由各自接口处理。 */
export function GpaTextInput({
  value,
  onChange,
  rows = 12,
  required = false,
  disabled = false,
}: {
  value: string
  onChange: (value: string) => void
  rows?: number
  required?: boolean
  disabled?: boolean
}) {
  const lines = value.split('\n').filter((line) => line.trim()).length
  return (
    <Field
      label="全班成绩"
      required={required}
      hint={`每行：学号,姓名,均分（也可省略姓名） · 已填写 ${lines} 行`}
    >
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={rows}
        required={required}
        disabled={disabled}
        placeholder={'学号,姓名,均分'}
        style={{
          width: '100%',
          background: 'var(--bg)',
          border: '1px solid var(--line)',
          padding: '12px 14px',
          color: 'var(--fg)',
          font: "400 12.5px/1.9 'JetBrains Mono',monospace",
          resize: 'vertical',
        }}
      />
    </Field>
  )
}
