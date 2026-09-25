import { enumOptionPath } from '@/lib/schemeTree'
import type { ScoreRule } from '@/lib/types'

type EnumRule = Extract<ScoreRule, { type: 'enum' }>

/**
 * 将扁平 options 渲染成逐级选择器。新模板可给 option.path；旧模板继续使用
 * label 中的“·”分隔层级，因此不需要重做既有 JSON。
 */
export function EnumOptionPicker({
  rule,
  value,
  onChange,
  compact = false,
}: {
  rule: EnumRule
  value: number
  onChange: (index: number) => void
  compact?: boolean
}) {
  const entries = rule.options.map((option, index) => ({ option, index, path: enumOptionPath(option) }))
  const selected = entries[value] ?? entries[0]
  if (!selected) return null

  const maxDepth = Math.max(...entries.map((entry) => entry.path.length))
  const rows = Array.from({ length: maxDepth }, (_, depth) => {
    const prefix = selected.path.slice(0, depth)
    const candidates = entries.filter((entry) => prefix.every((part, index) => entry.path[index] === part))
    const choices = [...new Set(candidates.map((entry) => entry.path[depth]).filter((part): part is string => !!part))]
    return { depth, candidates, choices }
  }).filter(({ choices }) => choices.length > 1 || maxDepth === 1)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: compact ? 9 : 12 }}>
      {rows.map(({ depth, candidates, choices }) => (
        <div key={depth} style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
          <span style={{ fontSize: compact ? 11.5 : 12.5, fontWeight: 600, color: 'var(--fg2)' }}>
            {rule.levels?.[depth] || (maxDepth === 1 ? '等级 / 档次' : `第 ${depth + 1} 级`)}
          </span>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: compact ? 7 : 9 }}>
            {choices.map((choice) => {
              const matching = candidates.filter((entry) => entry.path[depth] === choice)
              const target = matching[0]
              const on = selected.path[depth] === choice
              const leaf = matching.every((entry) => entry.path.length === depth + 1)
              return (
                <button
                  key={choice}
                  type="button"
                  /* 选中的档次是实心块（底色就是 --fg），不能再用把字染成 --fg 的 hover，
                     否则鼠标停在刚点下的那一档上，字和底同色，整个胶囊变成一坨黑。 */
                  className={on ? 'hv-op82' : 'hv-line-fg'}
                  onClick={() => onChange(target.index)}
                  style={{
                    margin: 0,
                    padding: compact ? '6px 12px' : '9px 17px',
                    borderRadius: 999,
                    font: 'inherit',
                    fontSize: compact ? 12 : 13,
                    fontWeight: on ? 600 : 400,
                    cursor: 'pointer',
                    background: on ? 'var(--fg)' : 'none',
                    color: on ? 'var(--bg)' : 'var(--fg2)',
                    border: `1px solid ${on ? 'var(--fg)' : 'var(--line)'}`,
                  }}
                >
                  {choice}{leaf ? ` · 建议 ${target.option.score} 分` : ''}
                </button>
              )
            })}
          </div>
        </div>
      ))}

      {maxDepth > 1 && (
        <div style={{ fontSize: compact ? 11.5 : 12.5, color: 'var(--fg3)', lineHeight: 1.6 }}>
          已选：{selected.path.join(' / ')} · 建议 {selected.option.score} 分
        </div>
      )}
    </div>
  )
}
