import { useEffect, useMemo, useState } from 'react'
import { categoryBriefKey, claimableItems } from '@/lib/schemeTree'
import { fieldStyle } from '@/lib/style'
import { acceptsSubmissions, type SchemeConfig } from '@/lib/types'

const matches = (value: string, query: string) => value.toLocaleLowerCase().includes(query)

/* 折到两行再省略。方案里的小项名普遍很长（「组织各类活动、比赛，或任体育活动教练员
   （院级组织分）」），单行截断之后一串项目开头一模一样，根本挑不出要点哪个。 */
const clampTwoLines = {
  minWidth: 0,
  display: '-webkit-box',
  WebkitBoxOrient: 'vertical' as const,
  WebkitLineClamp: 2,
  overflow: 'hidden',
  lineHeight: 1.45,
  overflowWrap: 'anywhere' as const,
}

/** 学生提交页与方案编辑器预览共用同一棵树，搜索、只读折叠和兜底项不会两边漂移。 */
export function SchemeTree({
  config,
  selectedKey,
  onSelect,
  /** 有未提交内容（本地暂存或数据库草稿）的小项，树上标一个点。方案编辑器的预览不传。 */
  draftKeys,
}: {
  config: SchemeConfig
  selectedKey: string | null
  onSelect: (key: string) => void
  draftKeys?: ReadonlySet<string>
}) {
  const [query, setQuery] = useState('')
  const [openReadOnly, setOpenReadOnly] = useState<Set<string>>(() => new Set())
  const normalizedQuery = query.trim().toLocaleLowerCase()

  useEffect(() => {
    if (!selectedKey) return
    const category = config.categories.find((candidate) =>
      [...candidate.baseItems.filter((item) => !item.studentClaim), ...candidate.penaltyItems].some((item) => item.key === selectedKey),
    )
    if (category) {
      setOpenReadOnly((current) => {
        if (current.has(category.key)) return current
        const next = new Set(current)
        next.add(category.key)
        return next
      })
    }
  }, [config, selectedKey])

  const visible = useMemo(() => config.categories.map((category) => {
    const categoryMatches = normalizedQuery !== '' && matches(category.name, normalizedQuery)
    const claimable = claimableItems(category).filter((item) =>
      !normalizedQuery || categoryMatches || matches(`${item.name} ${item.key}`, normalizedQuery),
    )
    const readOnly = [
      ...category.baseItems.filter((item) => !item.studentClaim).map((item) => ({ ...item, kind: '基础' as const })),
      ...category.penaltyItems.map((item) => ({ ...item, kind: '扣分' as const })),
    ].filter((item) => !normalizedQuery || categoryMatches || matches(`${item.name} ${item.key}`, normalizedQuery))
    /* 导入类大项没有任何可选小项，但它是总分里最重的一项。给它一格入口，
       指向「这一项怎么算」，否则学生在树上看到一个空标题，只会以为坏了。 */
    const brief = !acceptsSubmissions(category) &&
      (!normalizedQuery || categoryMatches || matches('计分公式 计分说明 教务系统 教务导入', normalizedQuery))
    return { category, claimable, readOnly, brief }
  }).filter(({ claimable, readOnly, brief }) => !normalizedQuery || claimable.length > 0 || readOnly.length > 0 || brief), [config, normalizedQuery])

  return (
    <nav data-r="scheme-tree" aria-label="方案大项与小项" style={{ minWidth: 0, padding: '22px 18px 22px 0', borderRight: '1px solid var(--line)' }}>
      <div data-r="scheme-tree-head" style={{ paddingBottom: 12 }}>
        <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 9 }}>
          选择小项
        </div>
        <div style={{ position: 'relative' }}>
          <span aria-hidden="true" style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', color: 'var(--fg3)', fontSize: 13 }}>⌕</span>
          <input
            type="search"
            aria-label="搜索方案小项"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索项目名称"
            style={{ ...fieldStyle, width: '100%', border: '1px solid var(--line)', background: 'var(--bg)', padding: '8px 28px 8px 29px', fontSize: 12.5 }}
          />
          {query && (
            <button type="button" aria-label="清空搜索" onClick={() => setQuery('')} style={{ position: 'absolute', right: 8, top: '50%', transform: 'translateY(-50%)', border: 0, background: 'none', padding: 2, color: 'var(--fg3)', cursor: 'pointer' }}>×</button>
          )}
        </div>
      </div>

      {visible.map(({ category, claimable, readOnly, brief }) => {
        const readOnlyExpanded = !!normalizedQuery || openReadOnly.has(category.key)
        const briefKey = categoryBriefKey(category.key)
        return (
          <div key={category.key} style={{ display: 'flex', flexDirection: 'column', paddingBottom: 16 }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, padding: '8px 0' }}>
              <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em' }}>{category.name}</span>
              <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>占 {config.weights[category.key] ?? 0}</span>
            </div>

            {brief && (
              <button
                type="button"
                className="hv-fg"
                aria-current={briefKey === selectedKey ? 'true' : undefined}
                title={`${category.name}的计分公式与认定说明`}
                onClick={() => onSelect(briefKey)}
                style={{
                  display: 'flex', alignItems: 'flex-start', gap: 8, width: '100%',
                  background: briefKey === selectedKey ? 'var(--sub)' : 'transparent', border: 0, margin: 0,
                  padding: '9px 8px 9px 0', font: 'inherit', textAlign: 'left',
                  cursor: 'pointer', color: 'var(--fg)',
                }}
              >
                <span style={{ width: 3, height: 17, flex: 'none', marginTop: 1, background: briefKey === selectedKey ? 'var(--red)' : 'transparent' }} />
                <span style={{ ...clampTwoLines, fontSize: 13, fontWeight: 600 }}>计分说明</span>
                <span style={{ marginLeft: 'auto', fontSize: 11.5, color: 'var(--fg3)', flex: 'none' }}>教务系统</span>
              </button>
            )}

            {claimable.map((item, index) => {
              const active = item.key === selectedKey
              const fallback = index === 0 && item.key.startsWith('__other_self_report_')
              const kind = item.claimableBase
                ? { text: '基础分', color: 'var(--red)' }
                : fallback
                  ? { text: '自报', color: 'var(--fg3)' }
                  : item.scoreRule.type === 'per_unit'
                    ? { text: '按量', color: 'var(--fg3)' }
                    : item.scoreRule.type === 'enum'
                      ? { text: '按档', color: 'var(--fg3)' }
                      : item.scoreRule.type === 'threshold'
                        ? { text: '条件申报', color: 'var(--fg3)' }
                        : { text: '自报', color: 'var(--fg3)' }
              return (
                <button
                  key={item.key}
                  type="button"
                  className="hv-fg"
                  aria-current={active ? 'true' : undefined}
                  title={item.claimableBase ? `${item.name}（条件基础分，须申报）` : item.name}
                  onClick={() => onSelect(item.key)}
                  style={{
                    display: 'flex', alignItems: 'flex-start', gap: 8, width: '100%',
                    background: active ? 'var(--sub)' : 'transparent', border: 0, margin: 0,
                    padding: '9px 8px 9px 0', font: 'inherit', textAlign: 'left',
                    cursor: 'pointer', color: active || item.claimableBase ? 'var(--fg)' : 'var(--fg2)',
                  }}
                >
                  <span style={{ width: 3, height: 17, flex: 'none', marginTop: 1, background: active || item.claimableBase ? 'var(--red)' : 'transparent' }} />
                  {/* 小项名允许折到两行再截断。方案里「组织各类活动、比赛，或任体育活动教练员（院级组织分）」
                      这种名字很常见，一行截断后剩下的全是「组织各类活动、比赛，或任…」，彼此分不出来。 */}
                  <span style={{ ...clampTwoLines, fontSize: 13, fontWeight: active || fallback || item.claimableBase ? 600 : 400 }}>{item.name}</span>
                  {draftKeys?.has(item.key) && (
                    <span title="有未提交的内容" style={{ flex: 'none', fontSize: 9, lineHeight: 1, color: 'var(--warn)' }}>●</span>
                  )}
                  <span style={{ marginLeft: 'auto', fontSize: 11.5, color: kind.color, flex: 'none' }}>
                    {kind.text}
                  </span>
                </button>
              )
            })}

            {readOnly.length > 0 && (
              <>
                <button
                  type="button"
                  aria-expanded={readOnlyExpanded}
                  onClick={() => setOpenReadOnly((current) => {
                    const next = new Set(current)
                    if (next.has(category.key)) next.delete(category.key)
                    else next.add(category.key)
                    return next
                  })}
                  style={{ display: 'flex', alignItems: 'center', gap: 7, border: 0, background: 'none', padding: '9px 8px 6px 3px', color: 'var(--fg3)', font: 'inherit', fontSize: 11.5, cursor: 'pointer', textAlign: 'left' }}
                >
                  <span style={{ width: 12, textAlign: 'center' }}>{readOnlyExpanded ? '▾' : '▸'}</span>
                  只读项目 · {readOnly.length}
                </button>
                {readOnlyExpanded && readOnly.map((item) => {
                  const active = item.key === selectedKey
                  return (
                    <button
                      key={item.key}
                      type="button"
                      className="hv-fg"
                      aria-current={active ? 'true' : undefined}
                      title={item.name}
                      onClick={() => onSelect(item.key)}
                      style={{
                        display: 'flex', alignItems: 'center', gap: 8, width: '100%',
                        background: active ? 'var(--sub)' : 'transparent', border: 0, margin: 0,
                        padding: '8px 8px 8px 12px', font: 'inherit', textAlign: 'left', cursor: 'pointer', color: 'var(--fg3)',
                      }}
                    >
                      <span style={{ width: 3, height: 12, flex: 'none', background: active ? 'var(--red)' : 'transparent' }} />
                      <span style={{ ...clampTwoLines, fontSize: 12.5 }}>{item.name}</span>
                      <span style={{ marginLeft: 'auto', fontSize: 10.5, flex: 'none' }}>{item.kind}</span>
                    </button>
                  )
                })}
              </>
            )}
          </div>
        )
      })}

      {visible.length === 0 && (
        <div style={{ padding: '18px 6px', fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7 }}>没有找到“{query.trim()}”相关的小项。</div>
      )}
    </nav>
  )
}
