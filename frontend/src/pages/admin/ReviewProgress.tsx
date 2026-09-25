import { useEffect, useState } from 'react'
import { useAdminReviewProgress, useAdminSubmissions, useReviewProgressActions } from '@/api/queries'
import type { AdminReviewProgressRow } from '@/api/types'
import { ApiError } from '@/api/client'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { ForceRejectSubmissionForm } from '@/components/ForceRejectSubmissionForm'
import { ForcedRejectionNotice } from '@/components/ForcedRejectionNotice'
import { ReviewSubject } from '@/components/ReviewSubject'
import { RuleCard } from '@/components/RuleCard'
import { ScoreHistory } from '@/components/ScoreHistory'
import { StudentNote } from '@/components/StudentNote'
import { BackLink, Btn, Empty, Note, PageHead, Pill, Sub, TextBtn, Timeline } from '@/components/ui'
import * as f from '@/lib/format'
import { PROGRESS_LIFECYCLE, PROGRESS_STATUSES, progressStatusLabel, type ProgressKind } from '@/lib/reviewProgress'
import { fieldStyle, mono } from '@/lib/style'
import { useApp } from '@/stores/app'
import '@/styles/review-progress.css'

function initialInt(key: string, fallback: number, allowed?: number[]) {
  const value = Number(new URLSearchParams(window.location.search).get(key))
  return Number.isInteger(value) && value > 0 && (!allowed || allowed.includes(value)) ? value : fallback
}

function ProgressTrail({ row }: { row: AdminReviewProgressRow }) {
  return <section style={{ paddingBlock: 20 }} aria-label="任务进度轨迹">
    <Sub title="任务进度轨迹" />
    {row.trail.length ? <Timeline items={row.trail.map((event) => ({ title: event.event, at: f.dateTime(event.at), desc: event.detail }))} /> : <Note>暂无任务进度记录。</Note>}
  </section>
}

function SubmissionDetail({ row }: { row: AdminReviewProgressRow }) {
  const query = useAdminSubmissions({ id: row.submissionId, page_size: 1 }, !!row.submissionId)
  const { refetch } = query
  const [correction, setCorrection] = useState<'reject' | 'score' | null>(null)
  const go = useApp((state) => state.go)
  useEffect(() => { if (row.submissionId) void refetch() }, [refetch, row.submissionId, row.status, row.submitted, row.finalScore])
  if (query.isLoading) return <div className="load-bar"><span /></div>
  if (query.isError) return <Note tone="bad">材料加载失败。<TextBtn onClick={() => void refetch()}>重试</TextBtn></Note>
  const item = query.data?.items.find((entry) => entry.id === row.submissionId)
  if (!item) return <Empty title="这条材料已不可读取" desc="请刷新任务列表后重新选择。" />
  const files = item.evidence ?? []
  const activeAppealId = item.status === 'appealing' ? item.appealId : null
  return <>
    <section aria-label="申报附件" style={{ paddingBlock: 20 }}>
      <Sub title="申报附件" note={`${files.length} 份 · 点击预览或下载`} />
      {files.length ? <EvidenceFiles items={files} /> : <Note>这条材料没有附件。</Note>}
    </section>
    <StudentNote value={item.note} evidence={files} />
    {item.forcedScore && !item.forceRejection && <div style={{ marginTop: 16 }}><ForcedRejectionNotice rejection={item.forcedScore} /></div>}
    {item.forceRejection && <div style={{ marginTop: 16 }}><ForcedRejectionNotice rejection={item.forceRejection} /></div>}
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px 24px', paddingBlock: 20, fontSize: 13 }}>
      <span>学生期望分：{f.score(item.requestedScore)}</span>
      <span>当前认定分：<strong>{item.finalScore == null ? '未定分' : f.score(item.finalScore)}</strong></span>
      <span style={{ color: 'var(--fg3)' }}>提交于 {f.dateTime(item.submittedAt)}</span>
    </div>
    {item.ruleSnapshot && <RuleCard item={item.ruleSnapshot.item} categoryName={item.ruleSnapshot.categoryName} capturedAt={item.ruleSnapshot.capturedAt} claim={item.claim} requestedScore={item.requestedScore} />}
    <ProgressTrail row={row} />
    {item.finalScore != null
      ? <ScoreHistory kind="submission" targetId={item.id} audience="reviewer" shownEvidenceIds={files.map((file) => file.id)} />
      : <Note>尚未形成认定分，当前分配与提交情况见任务进度轨迹。定分后会展示审核理由，以及后续申诉、异议的处理记录。</Note>}
    {!item.forceRejection && <section aria-label="材料处理" style={{ borderTop: '1px solid var(--line)', marginTop: 20, paddingTop: 20 }}>
      <Sub title="材料处理" />
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
        {(item.status === 'arbitrating' || activeAppealId) && <Btn onClick={() => {
          useApp.getState().setAgentFocus({ view: 'admSubs', resourceKind: activeAppealId ? 'appeal' : 'submission', resourceId: activeAppealId ?? item.id })
          go('admSubs')
        }}>处理{activeAppealId ? '这条申诉' : '这条仲裁'}</Btn>}
        <Btn danger disabled={!item.canForceReject} onClick={() => setCorrection('reject')}>强制驳回并记 0 分</Btn>
        <Btn disabled={!item.canForceScore} title={item.forceScoreBlockedReason} onClick={() => setCorrection('score')}>强制改分</Btn>
      </div>
      {!item.canForceReject && <p style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{item.forceRejectBlockedReason}</p>}
      {!item.canForceScore && item.canForceReject && <p style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{item.forceScoreBlockedReason}</p>}
      {correction && (correction === 'score' ? item.canForceScore : item.canForceReject) && <ForceRejectSubmissionForm key={correction} target={item} forceScore={correction === 'score'} onClose={() => setCorrection(null)} />}
    </section>}
  </>
}

export default function ReviewProgress() {
  const go = useApp((state) => state.go)
  const say = useApp((state) => state.say)
  const [kind, setKind] = useState<ProgressKind>(() => {
    const value = new URLSearchParams(window.location.search).get('kind')
    return value === 'item' || value === 'scorecard' ? value : 'all'
  })
  const [status, setStatus] = useState(() => {
    const value = new URLSearchParams(window.location.search).get('status') ?? 'all'
    return PROGRESS_STATUSES.has(value) ? value : 'all'
  })
  const [keyword, setKeyword] = useState(() => {
    const search = new URLSearchParams(window.location.search)
    return search.get('q') ?? search.get('student') ?? ''
  })
  const [reviewer, setReviewer] = useState(() => new URLSearchParams(window.location.search).get('reviewer') ?? '')
  const [page, setPage] = useState(() => initialInt('page', 1))
  const [pageSize, setPageSize] = useState(() => initialInt('page_size', 25, [10, 25, 50]))
  const [selectedID, setSelectedID] = useState<string | null>(() => new URLSearchParams(window.location.search).get('task'))
  const params = new URLSearchParams({ status, page: String(page), page_size: String(pageSize) })
  if (kind !== 'all') params.set('kind', kind)
  if (keyword.trim()) params.set('q', keyword.trim())
  if (reviewer.trim()) params.set('reviewer', reviewer.trim())
  const queryString = params.toString()
  const progress = useAdminReviewProgress(`?${queryString}`)
  const actions = useReviewProgressActions()
  useEffect(() => {
    const search = new URLSearchParams(queryString)
    search.set('v', 'admReviewProgress')
    if (selectedID) search.set('task', selectedID)
    window.history.replaceState(null, '', `?${search}`)
  }, [queryString, selectedID])
  const items = progress.data?.items ?? []
  const total = progress.data?.total ?? 0
  const selected = items.find((row) => row.id === selectedID)
  const at = items.findIndex((row) => row.id === selectedID)
  const filtered = kind !== 'all' || status !== 'all' || Boolean(keyword || reviewer)
  const resetSelection = () => { setPage(1); setSelectedID(null) }
  const reset = () => { setKind('all'); setStatus('all'); setKeyword(''); setReviewer(''); resetSelection() }
  return <div>
    <BackLink label="返回班级看板" onClick={() => go('admBoard')} />
    <PageHead en="REVIEW DETAILS" title="审核明细" desc="逐条看材料、轨迹和处理" side={<Btn disabled={actions.remind.isPending} onClick={() => actions.remind.mutate({ kind: kind === 'all' ? '' : kind, q: keyword.trim(), reviewer: reviewer.trim() }, {
      onSuccess: (result) => say(`已提醒 ${result.recipientCount} 名负责人 · 涉及 ${result.overdueCount} 个逾期任务`),
      onError: (error) => say(error instanceof ApiError ? error.message : '提醒失败'),
    })}>提醒逾期任务</Btn>} />
    <div className="review-progress-filters">
      <select aria-label="任务类型" value={kind} onChange={(event) => { setKind(event.target.value as ProgressKind); resetSelection() }} style={fieldStyle}>
        <option value="all">全部任务</option><option value="item">普通单项</option>
      </select>
      <select aria-label="任务状态" value={status} onChange={(event) => { setStatus(event.target.value); resetSelection() }} style={fieldStyle}>
        <option value="all">全部状态</option><option value="unfinished">未完成</option><option value="overdue">仅逾期</option>
        {PROGRESS_LIFECYCLE.map((part) => <option key={part.key} value={part.key}>{part.label}</option>)}
      </select>
      <input className="review-progress-search" aria-label="搜索学生、学号、标题、自述或小项" placeholder="学生 / 学号 / 标题 / 自述 / 小项" value={keyword} onChange={(event) => { setKeyword(event.target.value); resetSelection() }} style={fieldStyle} />
      <input aria-label="搜索负责人" placeholder="搜索负责人" value={reviewer} onChange={(event) => { setReviewer(event.target.value); resetSelection() }} style={fieldStyle} />
      {filtered && <TextBtn onClick={reset}>清空筛选</TextBtn>}
    </div>
    <div className="review-progress-pagination">
      <span aria-live="polite">共 <strong>{total}</strong> 条 · 第 {page} / {Math.max(1, Math.ceil(total / pageSize))} 页</span>
      <label>每页 <select aria-label="每页条数" value={pageSize} onChange={(event) => { setPageSize(Number(event.target.value)); resetSelection() }} style={{ ...fieldStyle, width: 'auto', padding: '6px 8px' }}>{[10, 25, 50].map((size) => <option key={size} value={size}>{size}</option>)}</select> 条</label>
      <div style={{ display: 'flex', gap: 8, marginLeft: 'auto' }}>
        <Btn disabled={page <= 1 || progress.isLoading} onClick={() => { setPage(page - 1); setSelectedID(null) }}>上一页</Btn>
        <Btn disabled={page * pageSize >= total || progress.isLoading} onClick={() => { setPage(page + 1); setSelectedID(null) }}>下一页</Btn>
      </div>
    </div>
    {progress.isError ? <Note tone="bad">任务加载失败。<TextBtn onClick={() => void progress.refetch()}>重试</TextBtn></Note>
      : progress.isLoading ? <div className="load-bar"><span /></div>
        : !items.length ? <Empty title="没有匹配的审核任务" desc="调整筛选条件，或等待审核任务生成。" />
          : <div className="review-progress-workspace" data-selected={Boolean(selected)}>
            <section className="review-progress-list" aria-label="审核任务列表">
              {items.map((row) => <button type="button" key={row.id} className="review-progress-task" aria-pressed={row.id === selectedID} onClick={() => setSelectedID(row.id)}>
                <span className="review-progress-task-copy">
                  <span style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}><strong>{row.student}</strong><span style={mono('11px', '0')}>{row.studentId}</span><Pill tone={row.forceRejection ? 'bad' : row.status === 'complete' ? 'ok' : 'idle'}>{progressStatusLabel(row)}</Pill></span>
                  <span className="review-progress-task-title">{row.title}</span>
                  <span style={{ fontSize: 12, color: 'var(--fg3)' }}>材料审核 · {row.expected > 0 ? `${row.submitted}/${row.expected} 已提交` : `${row.submitted} 份已提交结论`}{row.overdue ? ' · 逾期' : ''}</span>
                  <span style={{ fontSize: 12, color: 'var(--fg3)', overflowWrap: 'anywhere' }}>{row.reviewers.map((person) => person.name).join('、') || (row.status === 'complete' ? '本轮处理已结束' : '尚未分配负责人')}</span>
                </span>
                {row.kind === 'item' && row.finalScore != null && <span className="review-progress-task-score" aria-label={`实际分值 ${f.score(row.finalScore)} 分`}>
                  <span>实际分值</span>
                  <strong>{f.score(row.finalScore)}<small>分</small></strong>
                </span>}
              </button>)}
            </section>
            <section className="review-progress-detail" aria-label="审核任务详情">
              {selected ? <>
                <div className="review-progress-detail-nav"><TextBtn onClick={() => setSelectedID(null)}>返回任务列表</TextBtn><span style={{ marginLeft: 'auto', fontSize: 12, color: 'var(--fg3)' }}>本页 {at + 1} / {items.length}</span><Btn disabled={at <= 0} onClick={() => setSelectedID(items[at - 1].id)}>上一条</Btn><Btn disabled={at >= items.length - 1} onClick={() => setSelectedID(items[at + 1].id)}>下一条</Btn></div>
                <ReviewSubject label="当前材料" name={selected.student} sid={selected.studentId} meta={progressStatusLabel(selected)} />
                <h2 style={{ fontSize: 20, lineHeight: 1.5, margin: '20px 0 0', overflowWrap: 'anywhere' }}>{selected.title}</h2>
                <SubmissionDetail key={selected.id} row={selected} />
              </> : <Empty title="选择一条审核任务" desc="附件、轨迹和处理会显示在这里。" />}
            </section>
          </div>}
  </div>
}
