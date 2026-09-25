/* 申诉表单。提交条目与基础分/扣分项共用一份实现。

   口径：申诉的终点是「班级管理员下结论」，不是「用满两次」。复评一致就结案、学生仍可再申诉；
   一旦升到班管并由他签字（final），这一笔就到此为止——哪怕学生只申诉过一次。

   三类对象一律走草稿流。POST /appeals/draft 后端对 base_score / penalty_score 同样受理
   （validAppealTargetType）；以前前端自己限制成只有 submission 能建草稿，
   基础分与扣分在写理由时没有 appeal id，佐证区挂不上去。

   挂载时建草稿（ensureAppealDraft 幂等），取消时删掉。正文可粘贴图片，图片挂在这份草稿上。
   大块投放区复用 FileDropzone（经 AppealEvidence）。

   round 由调用方按 appealsUsed 算好传进来；能不能发起由后端 canAppeal 决定。 */

import { useEffect, useState, type CSSProperties } from 'react'
import { AppealEvidence } from '@/components/AppealEvidence'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { Btn } from '@/components/ui'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useAppeal, useAppealActions, useScheme } from '@/api/queries'
import { claimableItems } from '@/lib/schemeTree'
import { fieldStyle } from '@/lib/style'
import { appealRoundLabel } from '@/api/types'
import type { AppealTargetType, Evidence } from '@/api/types'
import type { CategoryKey } from '@/lib/types'

const label: CSSProperties = { fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }

export function AppealComposer({
  target,
  round,
  claim,
  quotable = [],
  onSubmitted,
  onCancel,
}: {
  target: { type: AppealTargetType; id: string }
  /** 下一次申诉是第几轮 */
  round: 1 | 2
  /** 提交条目的归类/分数主张；基础分与扣分项不传 */
  claim?: { category: CategoryKey; itemKey: string; score: number | null }
  quotable?: Evidence[]
  onSubmitted: () => void
  onCancel: () => void
}) {
  const say = useApp((s) => s.say)
  const scheme = useScheme()
  const { createDraft, submitDraft, deleteDraft } = useAppealActions()

  const [text, setText] = useState('')
  const [draftId, setDraftId] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [category, setCategory] = useState<CategoryKey>(claim?.category ?? ('' as CategoryKey))
  const [itemKey, setItemKey] = useState(claim?.itemKey ?? '')
  const [score, setScore] = useState(claim?.score == null ? '' : String(claim.score))
  const [prefilled, setPrefilled] = useState(false)
  const [draftError, setDraftError] = useState<string | null>(null)

  const wantsClaim = Boolean(claim)
  const categoryConfig = scheme.data?.categories.find((row) => row.key === category)
  const items = categoryConfig ? claimableItems(categoryConfig) : []
  const draft = useAppeal(draftId)

  const prepareDraft = () => {
    setDraftError(null)
    createDraft.mutate(
      { targetType: target.type, targetId: target.id },
      {
        onSuccess: ({ id }) => setDraftId(id),
        onError: (e) => {
          const message = e instanceof ApiError ? e.message : '申诉草稿创建失败'
          setDraftError(message)
          say(message)
        },
      },
    )
  }

  /* 仅挂载时建一次草稿。React 严格模式的双挂载安全：后端对同一目标复用同一份草稿。 */
  useEffect(() => {
    prepareDraft()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /* 撤回回来的草稿要把原文和主张分读回表单，否则学生看到的是一张空纸。 */
  useEffect(() => {
    if (prefilled || !draft.data) return
    if (draft.data.reason.trim()) setText(draft.data.reason)
    if (draft.data.proposedScore != null) setScore(String(draft.data.proposedScore))
    if (draft.data.proposedCategory) setCategory(draft.data.proposedCategory)
    if (draft.data.proposedItemKey) setItemKey(draft.data.proposedItemKey)
    setPrefilled(true)
  }, [draft.data, prefilled])

  const done = (r: { round: number; handlers: number; status?: string }) => {
    onSubmitted()
    if (r.status === 'resolved' || r.status === 'final') {
      say('申诉已处理完毕')
      return
    }
    say(r.round === 2 ? `${appealRoundLabel(2)}已提交 · 等待班级管理员终裁` : r.handlers > 0 ? `${appealRoundLabel(1)}已提交 · 已交原审核人复评` : '申诉已提交 · 已经升到班级管理员那里')
  }
  const cancel = () => {
    const id = draftId
    if (!id) {
      onCancel()
      return
    }
    deleteDraft.mutate(id, {
      onSuccess: onCancel,
      onError: (e) => say(e instanceof ApiError ? e.message : '草稿暂未删除，请稍后重试'),
    })
  }
  const submit = () => {
    if (!draftId) {
      say('申诉草稿尚未准备好，请稍后重试')
      return
    }
    setBusy(true)
    submitDraft.mutate(
      {
        id: draftId,
        reason: text.trim(),
        ...(wantsClaim ? { category, itemKey, score: Number(score) } : {}),
      },
      {
        onSuccess: done,
        onError: (e) => say(e instanceof ApiError ? e.message : '提交失败'),
        onSettled: () => setBusy(false),
      },
    )
  }

  const claimBad = wantsClaim && (!itemKey || score.trim() === '' || Number.isNaN(Number(score)))
  const invalid = text.trim().length < 8 || !draftId || claimBad
  const pending = uploading || busy || createDraft.isPending || deleteDraft.isPending
  const headline =
    round === 1
      ? wantsClaim
        ? '由原审核人复评；结论不一致则由班级管理员终裁。'
        : '由原记录人复核。'
      : '第二次申诉由班级管理员终裁，裁定后本条不再接受申诉。'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <span style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7 }}>{headline}</span>

      {wantsClaim && (
        <section style={{ display: 'flex', flexDirection: 'column', gap: 16, border: '1px solid var(--line)', borderLeft: '3px solid var(--red)', background: 'var(--sub)', padding: '17px 19px' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            <strong style={{ fontSize: 17, fontWeight: 650, letterSpacing: '-.025em', color: 'var(--fg)' }}>你主张的归类与分数</strong>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.65 }}>
              这段话直接交给复评人。顺手确认一下归类和分数没带错。
            </span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(220px,1fr))', gap: 18 }}>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 0 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>大项</span>
              <select
                value={category}
                onChange={(event) => {
                  const next = event.target.value as CategoryKey
                  setCategory(next)
                  const first = scheme.data?.categories.find((row) => row.key === next)
                  setItemKey(first ? claimableItems(first)[0]?.key ?? '' : '')
                }}
                style={fieldStyle}
              >
                {scheme.data?.categories.map((row) => (
                  <option key={row.key} value={row.key}>{row.name}</option>
                ))}
              </select>
            </label>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 0 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>小项</span>
              <select value={itemKey} onChange={(event) => setItemKey(event.target.value)} style={fieldStyle}>
                {items.map((item) => (
                  <option key={item.key} value={item.key}>{item.name}</option>
                ))}
              </select>
            </label>
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 16, paddingTop: 14, borderTop: '1px solid var(--line)', flexWrap: 'wrap' }}>
            <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 150 }}>
              <strong style={{ fontSize: 14, color: 'var(--fg)' }}>你认为应认定为</strong>
              <span style={{ fontSize: 12, color: 'var(--fg3)' }}>请填写复评后应生效的分数</span>
            </span>
            <span style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
              <input
                aria-label="申诉主张分数"
                value={score}
                onChange={(event) => setScore(event.target.value)}
                inputMode="decimal"
                placeholder="0.00"
                style={{ ...fieldStyle, width: 180, fontSize: 28, fontWeight: 650, letterSpacing: '-.04em', padding: '5px 0' }}
              />
              <strong style={{ fontSize: 14, color: 'var(--fg2)' }}>分</strong>
            </span>
          </label>
        </section>
      )}

      <label style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        <span style={{ ...label, fontSize: 13 }}>申诉理由</span>
        <MarkdownEditor
          onUploadingChange={setUploading}
          value={text}
          onChange={setText}
          minHeight={130}
          invalid={text.trim().length > 0 && text.trim().length < 8}
          evidence={quotable}
          uploadTarget={draftId ? { kind: 'appeal-claim', id: draftId } : undefined}
          placeholder="写清判偏在哪，依据是什么。可以直接粘截图。"
        />
      </label>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
        <span style={label}>佐证</span>
        {draftId ? (
          <AppealEvidence appealId={draftId} open title="把佐证拖到这里" emptyHint="还没传新材料。原来的佐证会一起给复评人。" />
        ) : draftError ? (
          <div style={{ border: '1px solid var(--red)', background: 'var(--redBg)', padding: '14px 16px', display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 12.5, color: 'var(--red)', lineHeight: 1.6 }}>{draftError}</span>
            <span style={{ marginLeft: 'auto' }}><Btn onClick={prepareDraft}>重试准备草稿</Btn></span>
          </div>
        ) : (
          <div style={{ border: '1px dashed var(--line)', padding: '18px 16px', fontSize: 12.5, color: 'var(--fg3)' }}>
            草稿建好之后就能在这里传文件了。
          </div>
        )}
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        <Btn primary disabled={invalid || pending} onClick={submit}>
          {busy ? '提交中…' : '提交申诉'}
        </Btn>
        <Btn disabled={pending} onClick={cancel}>取消并删除草稿</Btn>
        <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>理由至少 8 字</span>
      </div>
    </div>
  )
}
