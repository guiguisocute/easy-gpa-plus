import { useEffect, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import { useForceRejectSubmission } from '@/api/queries'
import type { RuleSnapshot } from '@/api/types'
import { scoreBounds } from '@/lib/claim'
import { Btn, Field } from '@/components/ui'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'

export interface ForceRejectTarget {
  id: string
  student: string
  studentId: string
  title: string
  finalScore: number | null
  ruleSnapshot?: RuleSnapshot
}

export function ForceRejectSubmissionForm({ target, onClose, selfReject = false, forceScore = false }: { target: ForceRejectTarget; onClose: () => void; selfReject?: boolean; forceScore?: boolean }) {
  const adjusting = forceScore && !selfReject
  const [previousScore] = useState(target.finalScore)
  const [score, setScore] = useState(target.finalScore == null ? '' : String(target.finalScore))
  const [reason, setReason] = useState('')
  const [error, setError] = useState('')
  const reasonInput = useRef<HTMLTextAreaElement>(null)
  const submitting = useRef(false)
  const action = useForceRejectSubmission(selfReject, adjusting)
  const say = useApp((s) => s.say)
  const reasonLength = Array.from(reason.trim()).length
  const newScore = adjusting ? Number(score) : 0
  const bounds = target.ruleSnapshot ? scoreBounds(target.ruleSnapshot.item.scoreRule) : null
  const valid = reasonLength > 0 && reasonLength <= 2000 && (!adjusting || (previousScore !== null && score.trim() !== '' && Number.isFinite(newScore) && Math.abs(newScore) <= 99999 && (!bounds || newScore >= bounds.lo && newScore <= bounds.hi) && Math.round(newScore * 1000) !== Math.round(previousScore * 1000)))
  const label = adjusting ? '改分' : '驳回'

  useEffect(() => { reasonInput.current?.focus() }, [])

  async function submit() {
    if (!valid || submitting.current) return
    submitting.current = true
    setError('')
    try {
      await action.mutateAsync({ id: target.id, reason: reason.trim(), ...(adjusting ? { score: newScore, previousScore: previousScore! } : {}) })
      say(`已强制${label}，该项认定分为 ${f.score(newScore)} 分`)
      onClose()
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : `强制${label}失败，请重试`)
    } finally {
      submitting.current = false
    }
  }

  return (
    <form aria-label={`强制${label}确认`} onSubmit={(event) => { event.preventDefault(); void submit() }} style={{ border: '1px solid var(--red)', background: 'var(--redBg)', padding: '18px 20px', margin: '14px 0', maxWidth: 760, lineHeight: 1.7 }}>
      <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--red)' }}>{selfReject ? '确认主动驳回本人的提交' : `确认强制${label}这一条材料`}</div>
      <div style={{ fontSize: 13, color: 'var(--fg)', marginTop: 10, overflowWrap: 'anywhere' }}>
        <div>学生：<strong>{target.student}</strong> · {target.studentId}</div>
        <div>材料：<strong>{f.submissionTitle(target.title)}</strong></div>
        <div>认定分：<strong style={{ color: 'var(--red)' }}>{previousScore === null ? '未定分' : `${f.score(previousScore)} 分`} → {adjusting && score.trim() === '' ? '待填写' : `${f.score(newScore)} 分`}</strong></div>
      </div>
      <p style={{ fontSize: 12.5, color: 'var(--fg2)', margin: '10px 0 14px' }}>{selfReject
        ? '确认后记 0 分，正在走的审核和申诉一并结束。材料还在，不能反悔，也不能再申诉。'
        : `确认后马上${adjusting ? '按新分数' : '按 0 分'}认定，正在走的流程结束。材料还在，不能再申诉。`}</p>
      {adjusting && <Field label="新认定分" required hint={`最多三位小数${bounds ? `，允许范围 ${f.score(bounds.lo)} 至 ${Number.isFinite(bounds.hi) ? f.score(bounds.hi) : '不限上限'} 分` : '，须符合此项评分规则'}。`}>
        <input aria-label="新认定分" type="number" step="0.001" min={bounds?.lo} max={bounds && Number.isFinite(bounds.hi) ? bounds.hi : undefined} value={score} required disabled={action.isPending} onChange={(event) => setScore(event.target.value)} style={{ width: 160, padding: '10px 12px', background: 'var(--bg)', border: '1px solid var(--line)', color: 'var(--fg)', font: 'inherit' }} />
      </Field>}
      <Field label={`${label}理由`} required hint={`${reasonLength} / 2000 字 · ${selfReject ? '写清为什么放弃或交错了材料' : '学生看得到这段理由'}`}>
        <textarea aria-label={`${label}理由`} ref={reasonInput} value={reason} onChange={(event) => setReason(event.target.value)} required disabled={action.isPending} rows={3} placeholder={`请填写${label}理由`} style={{ resize: 'vertical', width: '100%', boxSizing: 'border-box', padding: '10px 12px', background: 'var(--bg)', border: '1px solid var(--line)', color: 'var(--fg)', font: 'inherit', fontSize: 13 }} />
      </Field>
      {error && <div role="alert" style={{ color: 'var(--red)', fontSize: 13, marginTop: 10 }}>{error}</div>}
      <div style={{ display: 'flex', gap: 10, marginTop: 16, flexWrap: 'wrap' }}>
        <Btn primary danger type="submit" disabled={!valid || action.isPending}>{action.isPending ? `正在${label}…` : adjusting ? '确认强制改分' : '确认驳回并记 0 分'}</Btn>
        <Btn onClick={onClose} disabled={action.isPending}>取消</Btn>
      </div>
    </form>
  )
}
