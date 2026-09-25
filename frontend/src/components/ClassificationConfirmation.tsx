import { useState } from 'react'
import { ApiError } from '@/api/client'
import { useClassificationActions, useScheme } from '@/api/queries'
import type { ClassificationSuggestion } from '@/api/types'
import { DEPUTY_RECUSAL_NOTE, isAdjudicationRecused } from '@/lib/adjudication'
import { schemePathName } from '@/lib/schemeTree'
import { fieldStyle } from '@/lib/style'
import { useApp } from '@/stores/app'
import { Btn, Empty, Note, TextBtn } from './ui'

/** 导出前确认与副班管仲裁共用同一份分类表单。跨大项仍由冲突详情同时裁定归类和分数。 */
export function ClassificationConfirmation({ suggestions, onOpenArbitration, showEmpty = false }: {
  suggestions: ClassificationSuggestion[]
  onOpenArbitration: (submissionId: string) => void
  showEmpty?: boolean
}) {
  const me = useApp((state) => state.user)
  const say = useApp((state) => state.say)
  const scheme = useScheme()
  const actions = useClassificationActions()
  const [drafts, setDrafts] = useState<Record<string, { choice: 'keep' | 'accept'; score: string; reason: string }>>({})
  const within = suggestions.filter((item) => item.scope === 'within_category')
  const cross = suggestions.filter((item) => item.scope === 'cross_category')

  if (suggestions.length === 0) return showEmpty ? <Empty title="没有待确认的分类建议" /> : null

  return <div style={{ paddingTop: 18 }}>
    {cross.map((suggestion) => {
      const recused = isAdjudicationRecused(suggestion, suggestion.studentSid, me?.sid)
      return <div key={suggestion.id} style={{ borderTop: '1px solid var(--line2)', padding: '14px 0' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
          <div style={{ flex: 1, minWidth: 220 }}>
            <strong style={{ fontSize: 12.5 }}>{suggestion.student} · {suggestion.studentSid}</strong>
            <span style={{ display: 'block', marginTop: 4, fontSize: 12, color: 'var(--fg3)' }}>{suggestion.title} · 跨大项</span>
            <div style={{ fontSize: 12.5, lineHeight: 1.7, marginTop: 6 }}>{schemePathName(scheme.data, suggestion.fromCategory, suggestion.fromItemKey)} → {schemePathName(scheme.data, suggestion.toCategory, suggestion.toItemKey)}</div>
            <div style={{ fontSize: 12.5, lineHeight: 1.7, marginTop: 6, color: 'var(--fg2)', whiteSpace: 'pre-wrap' }}>{suggestion.reason}</div>
          </div>
          <TextBtn onClick={() => onOpenArbitration(suggestion.submissionId)}>{recused ? '查看' : '处理'}</TextBtn>
        </div>
        {recused && <div style={{ marginTop: 12 }}><Note tone="warn">{suggestion.recusalReason || DEPUTY_RECUSAL_NOTE}</Note></div>}
      </div>
    })}
    {within.map((suggestion) => {
      const draft = drafts[suggestion.id] ?? { choice: 'keep' as const, score: suggestion.finalScore == null ? '' : String(suggestion.finalScore), reason: '' }
      const category = draft.choice === 'keep' ? suggestion.fromCategory : suggestion.toCategory
      const itemKey = draft.choice === 'keep' ? suggestion.fromItemKey : suggestion.toItemKey
      const score = Number(draft.score)
      const recused = isAdjudicationRecused(suggestion, suggestion.studentSid, me?.sid)
      const patch = (value: Partial<typeof draft>) => setDrafts((all) => ({ ...all, [suggestion.id]: { ...draft, ...value } }))
      return <div key={suggestion.id} style={{ borderTop: '1px solid var(--line2)', padding: '14px 0' }}>
        <div data-r="split" style={{ display: 'grid', gridTemplateColumns: 'minmax(180px,1fr) minmax(210px,1.2fr) auto', gap: 14, alignItems: 'center' }}>
          <div>
            <strong style={{ fontSize: 12.5 }}>{suggestion.student} · {suggestion.studentSid}</strong>
            <span style={{ display: 'block', marginTop: 4, fontSize: 12, color: 'var(--fg3)' }}>{suggestion.title} · 同大项</span>
            <div style={{ fontSize: 12.5, lineHeight: 1.7, marginTop: 6, color: 'var(--fg2)', whiteSpace: 'pre-wrap' }}>{suggestion.reason}</div>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 88px', gap: 8 }}>
            <select aria-label={`${suggestion.student} 的分类处理`} value={draft.choice} onChange={(event) => patch({ choice: event.target.value as 'keep' | 'accept' })} disabled={recused} style={fieldStyle}>
              <option value="keep">保留 {schemePathName(scheme.data, suggestion.fromCategory, suggestion.fromItemKey)}</option>
              <option value="accept">采纳 {schemePathName(scheme.data, suggestion.toCategory, suggestion.toItemKey)}</option>
            </select>
            <input aria-label={`${suggestion.student} 的分类认定分`} value={draft.score} onChange={(event) => patch({ score: event.target.value })} inputMode="decimal" placeholder="认定分" disabled={recused} style={fieldStyle} />
            <input aria-label={`${suggestion.student} 的分类处理理由`} value={draft.reason} onChange={(event) => patch({ reason: event.target.value })} placeholder="理由" disabled={recused} style={{ ...fieldStyle, gridColumn: '1 / -1' }} />
          </div>
          <Btn primary disabled={recused || draft.reason.trim().length < 4 || draft.score.trim() === '' || !Number.isFinite(score) || actions.resolve.isPending} onClick={() => actions.resolve.mutate({ submissionId: suggestion.submissionId, suggestionId: suggestion.id, category, itemKey, score, reason: draft.reason.trim() }, {
            onSuccess: () => say(`分类已确认：${schemePathName(scheme.data, category, itemKey)}`),
            onError: (error) => say(error instanceof ApiError ? error.message : '分类确认失败'),
          })}>确认</Btn>
        </div>
        {recused && <div style={{ marginTop: 12 }}><Note tone="warn">{suggestion.recusalReason || DEPUTY_RECUSAL_NOTE}</Note></div>}
      </div>
    })}
  </div>
}
