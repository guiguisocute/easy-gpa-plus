/* 综测小组 · 初审工作台。只审单项条目，一条一条裁定，队列可跳着审；
   本页处理分配给当前审核人的单项材料。

   三个不能弄错的点：
   1. 背靠背——只显示另一名审核人"交没交"，绝不显示交了什么（§5.1）；
   2. 认定分不得超过规则快照里的上限，前端先拦一道，后端 scoreWithinRule 仍会再验一次；
   3. 调分与驳回必须写理由，理由会原样发给学生（学生看得到理由，看不到人名）。

   队列就是 tab=mine 里 type=submission 的结果，不额外拉一个"我的队列"接口——
   两个来源迟早会不一致，而不一致的那一刻没人会发现。 */

import { useEffect, useMemo, useState } from 'react'
import { ShieldAlert } from 'lucide-react'
import { Btn, Empty, Note, PageHead, Row, Split, SplitCol } from '@/components/ui'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { RichText } from '@/components/Markdown'
import { RuleCard } from '@/components/RuleCard'
import { StudentNote } from '@/components/StudentNote'
import { ReviewSubject } from '@/components/ReviewSubject'
import {
  DecisionChips,
  DeskPanel,
  DeskScoreField,
  DeskSide,
  EvidencePane,
  ExpectedScoreBar,
  ReviewQueueBar,
  ReviewerProgress,
} from '@/components/ReviewDesk'
import { DESK_CHOICES, isDeskDecision } from '@/lib/reviewDesk'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { AI_ENABLED } from '@/api/mode'
import { scoreBounds } from '@/lib/claim'
import { claimableItems } from '@/lib/schemeTree'
import { findCurrentItem } from '@/lib/ruleDiff'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useReviewActions, useReviewTask, useReviewTasks, useScheme } from '@/api/queries'
import type { ReviewDecision, ReviewTask } from '@/api/types'
import type { CategoryKey } from '@/lib/types'
import '@/styles/mobile-pages.css'

const CHOICES = DESK_CHOICES

/* 与联合任务接口保持同一排序键：逾期优先，再按 SLA 截止时间。 */
const byQueueOrder = (a: ReviewTask, b: ReviewTask) =>
	Number(b.overdue) - Number(a.overdue) || a.dueAt.localeCompare(b.dueAt) || Number(a.id) - Number(b.id)

/* 举报提示条。这一页判的是学生自己交的材料，举报判的是别人替他报上来的分——
   两件事绝不能在同一个台面上混着做，
   但派到的是同一批人，所以这里得有个明显的去处，否则举报会在队列里被静静饿死。 */
function ReportHint({ count, onGo }: { count: number; onGo: () => void }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap', border: '1px solid var(--line)', borderLeft: '3px solid var(--red)', background: 'var(--sub)', padding: '13px 17px', marginBottom: 18 }}>
      <ShieldAlert size={16} strokeWidth={1.7} style={{ flex: 'none', color: 'var(--red)' }} />
      <span style={{ fontSize: 13, color: 'var(--fg)' }}>
        另有 <strong style={{ ...num, fontWeight: 600 }}>{count}</strong> 条学生匿名举报等你复核
      </span>
      <span style={{ fontSize: 12.5, color: 'var(--fg3)', minWidth: 0 }}>
        举报在另一页处理
      </span>
      <span style={{ marginLeft: 'auto' }}>
        <Btn onClick={onGo}>去举报复核</Btn>
      </span>
    </div>
  )
}

export default function ReviewDesk({ historySubmissionId, onBack }: { historySubmissionId?: string; onBack?: () => void } = {}) {
  const say = useApp((s) => s.say)
  const go = useApp((s) => s.go)
  const agentHandoff = useApp((s) => s.agentHandoff)
  const setAgentHandoff = useApp((s) => s.setAgentHandoff)

	const queue = useReviewTasks('mine', !historySubmissionId)
	const currentScheme = useScheme()

  /* tab=mine 只给"我还没交结论的"，交完一条它当场从接口里消失。但审核人要看得见
     自己刚交的那几条——交错了得能点回去看自己写了什么，所以把交过的留在本地，
     按同一个排序键并回队列：位置不动，只是变绿。 */
  const [done, setDone] = useState<ReviewTask[]>([])
	const items = useMemo(() => {
		const live = (queue.data?.items ?? []).filter((t): t is ReviewTask => t.type === 'submission')
    const liveIds = new Set(live.map((t) => t.id))
    return [...live, ...done.filter((t) => !liveIds.has(t.id))].sort(byQueueOrder)
  }, [queue.data, done])
  /* 学生匿名举报也派给同一批审核人，但它判的是别人替学生报上来的分、依据只有举报人
     写的一段话，和这一页的心智模型正相反，所以不混进队列，只在这里指过去。 */
  const reportCount = (queue.data?.items ?? []).filter((t) => t.type === 'report').length
  const doneIds = new Set(done.map((t) => t.id))
  const pending = items.filter((t) => !doneIds.has(t.id)).length

  const [idx, setIdx] = useState(0)
  const [decision, setDecision] = useState<ReviewDecision>('accepted')
  const [score, setScore] = useState('')
  const [why, setWhy] = useState('')
  const [uploading, setUploading] = useState(false)
	const [suggestClassification, setSuggestClassification] = useState(false)
	const [suggestCategory, setSuggestCategory] = useState<CategoryKey>('moral')
	const [suggestItem, setSuggestItem] = useState('')
	const [suggestReason, setSuggestReason] = useState('')
  const [startedAt, setStartedAt] = useState(() => Date.now())
  const [editingReviewId, setEditingReviewId] = useState<string | null>(null)

  useEffect(() => {
    if (historySubmissionId || agentHandoff?.kind !== 'review_draft') return
    const targetIndex = items.findIndex((item) => item.id === agentHandoff.resourceId)
    if (targetIndex < 0) return
    setIdx(targetIndex)
    const prefill = agentHandoff.prefill
    if (isDeskDecision(prefill?.decision)) setDecision(prefill.decision)
    if (typeof prefill?.score === 'number') setScore(String(prefill.score))
    if (typeof prefill?.reason === 'string') setWhy(prefill.reason)
    setStartedAt(Date.now())
    setAgentHandoff(null)
    say('Agent 审核草稿已经填好了，核对一遍再在工作台上手动提交')
  }, [agentHandoff, items, say, setAgentHandoff, historySubmissionId])

  const queuedCurrent = items[Math.min(idx, Math.max(0, items.length - 1))]
  const detail = useReviewTask(historySubmissionId ?? queuedCurrent?.id ?? null, !!historySubmissionId)
  const current = historySubmissionId ? detail.data : queuedCurrent
  const { decide } = useReviewActions()

  // Initialize once per editor. Refetches must neither erase unsaved input nor
  // replace the revision id used to reject a stale save from another tab.
  useEffect(() => {
    const d = detail.data
    if (!historySubmissionId || editingReviewId || detail.isFetching || !d?.myReview || !d.canEdit) return
    setEditingReviewId(d.myReview.id)
    setDecision(d.myReview.decision)
    setScore(String(d.myReview.score))
    setWhy(d.myReview.reason)
    const suggestion = d.myClassificationSuggestion
    setSuggestClassification(suggestion?.status === 'pending')
    if (suggestion?.status === 'pending') {
      setSuggestCategory(suggestion.category)
      setSuggestItem(suggestion.itemKey)
      setSuggestReason(suggestion.reason)
    }
    setStartedAt(Date.now())
  }, [historySubmissionId, editingReviewId, detail.data, detail.isFetching])

  /* 队列变短（比如条目被改派走）时把游标拉回范围内，否则会停在一个不存在的下标上。
     只在手上有队列数据时判断：拉取途中 items 会短暂只剩本地已审的那几条，
     照着那个长度收游标会把人莫名其妙弹回第一条。 */
  useEffect(() => {
    if (queue.data && idx > 0 && idx >= items.length) setIdx(Math.max(0, items.length - 1))
  }, [queue.data, items.length, idx])
	const suggestCategoryConfig = currentScheme.data?.categories.find((category) => category.key === suggestCategory)
	const suggestItems = useMemo(() => suggestCategoryConfig ? claimableItems(suggestCategoryConfig) : [], [suggestCategoryConfig])
	useEffect(() => {
		if (historySubmissionId && !editingReviewId) return
		if (suggestItems.length && !suggestItems.some((item) => item.key === suggestItem)) setSuggestItem(suggestItems[0].key)
	}, [suggestItems, suggestItem, historySubmissionId, editingReviewId])

	if (historySubmissionId ? detail.isLoading || (!editingReviewId && detail.isFetching) : queue.isLoading) return <div className="load-bar"><span /></div>
  if (detail.isError || (historySubmissionId && !current)) return <div>
    <PageHead en="REVIEW DESK" title={historySubmissionId ? '修改历史审核' : '初审工作台'} />
    <Empty title="无法读取审核任务" desc={detail.error instanceof ApiError ? detail.error.message : '请重试或返回历史审核查看最新状态。'} />
    <div style={{ display: 'flex', gap: 10, marginTop: 18 }}><Btn onClick={() => void detail.refetch()}>重试</Btn>{onBack && <Btn onClick={onBack}>返回历史审核</Btn>}</div>
  </div>
	if (!current) {
    return (
      <div style={{ animation: 'rise .28s ease both' }}>
        <PageHead en="REVIEW DESK" title="初审工作台" desc="核对材料，提交结论" />
        {/* 手上只剩举报时最容易出事：这一页说"没有待审条目"，人就走了，
            举报在另一个台面上没人管。所以空状态也要把它顶出来。 */}
        {reportCount > 0 && <ReportHint count={reportCount} onGo={() => go('revReports')} />}
        <Empty
          title="没有待审条目"
          desc="分给你的条目都交了结论。有冲突的已经自动升给班级管理员处理。"
        />
        <div style={{ marginTop: 18, display: 'flex', gap: 10 }}>
          <Btn onClick={() => go('revTasks')}>回到待办列表</Btn>
          <Btn onClick={() => go('revTasks')}>查看审核任务</Btn>
          {reportCount > 0 && <Btn onClick={() => go('revReports')}>去举报复核</Btn>}
        </div>
      </div>
    )
  }

  const d = detail.data
  const editing = !!historySubmissionId && !!editingReviewId
  const editStale = editing && (!d?.canEdit || d.myReview?.id !== editingReviewId)
  const peerSubmitted = d?.peerSubmitted ?? ((queuedCurrent?.reviewCount ?? 0) - (d?.myReview ? 1 : 0) > 0)
  /* 正文里的 evidence: 引用能解析到「学生的佐证」加「写理由的人贴的图」；
     下面那个附件网格只列前者——note 不是学生交的材料，混进去会看成申报内容。 */
  const quotable = [...(d?.evidence ?? []), ...(d?.noteEvidence ?? [])]
  const rule = d?.ruleSnapshot.item.scoreRule
  const { lo, hi } = rule ? scoreBounds(rule) : { lo: 0, hi: Infinity }
  const choice = CHOICES.find((c) => c.key === decision)!
  const scoreNum = Number(score)
  const scoreBad = choice.needScore && (score.trim() === '' || Number.isNaN(scoreNum) || scoreNum < lo || scoreNum > hi)
  const whyBad = choice.needWhy && why.trim().length < 4
  /* 学生留空数量或自报分时，requestedScore 为空，通过也必须给一个认定分 */
  const needScoreForAccept = decision === 'accepted' && d?.requestedScore == null
  const acceptScoreBad = needScoreForAccept && (score.trim() === '' || Number.isNaN(scoreNum) || scoreNum < lo || scoreNum > hi)
  const canSubmit = !scoreBad && !whyBad && !acceptScoreBad && !!d && !decide.isPending && !editStale
    && (!historySubmissionId || editing) && (d.status === 'pending' || d.status === 'consensus')
  const classificationError = !suggestClassification ? ''
    : !suggestItems.some((item) => item.key === suggestItem) ? '请选择建议大项和该大项下的小项。'
    : suggestCategory === d?.category && suggestItem === d?.itemKey ? '建议归类与当前归类相同，请更换大项或小项。'
    : suggestReason.trim().length < 4 ? '分类调整理由至少填写 4 个字，审核意见不能代替分类调整理由。'
    : ''
  const classificationBad = !!classificationError
  const submitHint = scoreBad || acceptScoreBad
    ? score.trim() === '' ? '请填写认定分值。' : `认定分值须在 ${lo} — ${hi === Infinity ? '不封顶' : hi} 之间。`
    : whyBad ? '审核意见至少填写 4 个字。'
    : classificationError

  const goTo = (i: number) => {
    if (i < 0 || i >= items.length) return
    setIdx(i)
    setDecision('accepted')
    setScore('')
    setWhy('')
	setSuggestClassification(false)
	setSuggestReason('')
    setStartedAt(Date.now())
  }

  /* 跳过与提交后都落到下一条还没审的，不会停在自己刚审过的绿块上；
     绿块只在手动点击时才回得去。 */
  const goNextPending = (from: number) => {
    const next = items.findIndex((t, i) => i > from && !doneIds.has(t.id))
    goTo(next >= 0 ? next : from + 1)
  }

  const submit = () => {
    if (!current || !canSubmit || classificationBad) return
    const spent = Math.max(1, Math.round((Date.now() - startedAt) / 1000))
    decide.mutate(
      {
        id: current.id,
        reviewId: editing ? editingReviewId : undefined,
        decision,
        score: choice.needScore || needScoreForAccept ? scoreNum : undefined,
        reason: why.trim(),
        spentSeconds: spent,
		classificationSuggestion: suggestClassification ? { category: suggestCategory, itemKey: suggestItem, reason: suggestReason.trim() } : undefined,
      },
      {
        onSuccess: (r) => {
          if (editing) {
            say('审核结论已修改，等待另一名审核人提交')
            onBack?.()
            return
          }
          say(
            r.conflict
              ? '结论已提交 · 和另一名审核人不一致，已经升给班级管理员仲裁'
              : r.submissionStatus === 'scored'
                ? `结论已提交 · 双评一致，认定 ${f.score(r.finalScore)} 分`
                : '结论已提交 · 等另一名审核人交（他看不到你写了什么）',
          )
          setDone((prev) => (prev.some((t) => t.id === queuedCurrent.id) ? prev : [...prev, queuedCurrent]))
          goNextPending(idx)
        },
        onError: (e) => {
          say(e instanceof ApiError ? e.message : '提交失败')
          if (e instanceof ApiError && (e.status === 409 || e.status === 404)) void detail.refetch()
        },
      },
    )
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en={`REVIEW DESK · #${current.id}`}
        title={historySubmissionId ? '修改历史审核' : '初审工作台'}
        desc={historySubmissionId ? '上次的结论已带入。对方没交之前还能改。' : undefined}
        side={onBack && <Btn onClick={onBack}>返回历史审核</Btn>}
      />
      {!historySubmissionId && reportCount > 0 && <ReportHint count={reportCount} onGo={() => go('revReports')} />}
      <ReviewSubject
        sticky
        label="当前初审学生"
        name={d?.student ?? current.student}
        sid={d?.studentId ?? current.studentId}
        meta={`当前条目：${d?.title ?? current.title} · ${d?.ruleSnapshot.categoryName ?? ''} / ${d?.ruleSnapshot.item.name ?? current.itemKey} · 提交于 ${f.dateTime(current.submittedAt)}`}
        side={
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={mono('11.5px', '0')}>#{current.id}</span>
            {!historySubmissionId && <><span style={{ width: 1, height: 11, background: 'var(--line)' }} />
            <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg2)', ...num }}>{idx + 1} / {items.length}</span></>}
          </div>
        }
      />

      <Split cols="1.25fr 1fr">
        <SplitCol first>
          <div style={{ padding: '24px 0' }}>
            {/* 与学生提交页共用 EvidenceFiles：图片出缩略图，其余按类型出图标，
                点开是页内灯箱或浏览器阅读器。审核台不给移除，所以不传 onRemove。 */}
            <EvidencePane items={d?.evidence ?? []} note="查看链接 10 分钟内有效，每看一次都会记进审计" />

            <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', padding: '26px 0 12px' }}>学生填报</div>
            <Row label="事项名称" value={d?.title ?? '—'} />
            <Row
              label="学生自报"
              value={f.claimText(d?.claim, rule?.type === 'per_unit' ? rule.unit : undefined)}
            />
            <Row label="学生期望分" value={d?.requestedScore == null ? '未填，待你认定' : f.score(d.requestedScore)} />
            {/* 学生写的东西按 Markdown 渲染。他在提交页用的就是 MarkdownEditor，
                这里当纯文本摊开的话，列表会变成一行挤在一起的 `- xxx - yyy`。 */}
            <div style={{ marginTop: 16 }}>
              <StudentNote value={d?.note} evidence={quotable} />
            </div>

            {/* 规则铺开成档位表，学生选的那一档高亮：审核人不必再从一行小字里数分数。
                快照里的 item 就是提交当时冻结的那一份，不去读当前方案。 */}
            <div style={{ padding: '26px 0 0' }}>
              {d && (
                <RuleCard
                  item={d.ruleSnapshot.item}
                  categoryName={d.ruleSnapshot.categoryName}
                  capturedAt={d.ruleSnapshot.capturedAt}
                  currentItem={findCurrentItem(currentScheme.data, d.ruleSnapshot.item.key)}
                  claim={d.claim}
                  requestedScore={d.requestedScore}
                />
              )}
            </div>
          </div>
        </SplitCol>

        <SplitCol>
          {/* 右栏吸顶：当前对象条横跨两栏钉在 57，右栏只能落在它下面（109 = 57 + 52）。
              窄屏收成一栏后 global.css 会把整个右栏改回 static，这个值随之作废。 */}
          <DeskSide>
            <ExpectedScoreBar
              label="学生期望分"
              value={d?.requestedScore == null ? '未填' : f.score(d.requestedScore)}
              unit={d?.requestedScore == null ? undefined : '分'}
              tone={d?.requestedScore == null ? 'var(--warn)' : undefined}
              side={`规则允许 ${lo} — ${hi === Infinity ? '不封顶' : hi}`}
            />

            {/* 结论卡紧跟着期望分：这一栏会跟着页面吸顶，栏内容超过一屏时挤出去的
                只能是双评状态这类读一眼就够的信息，不能是要动手的地方。 */}
            {d?.myReview && !editing ? (
              <DeskPanel title="你已就这条给出结论">
                <Row label="结论" value={CHOICES.find((c) => c.key === d.myReview!.decision)?.label ?? d.myReview.decision} />
                <Row label="认定分" value={f.score(d.myReview.score)} />
                <Row label="提交于" value={f.dateTime(d.myReview.at)} />
                <div style={{ marginTop: 12 }}>
                  <RichText value={d.myReview.reason} evidence={quotable} />
                </div>
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', marginTop: 12 }}>
                  {d.canEdit ? '另一名审核人尚未提交，可在历史审核中修改自己的结论。' : '这条结论当前不能修改。另一名审核人已提交、任务状态变化或审核关闭后，历史结论只供查看。'}
                </div>
                {d.canEdit && !historySubmissionId && <div style={{ marginTop: 14 }}><Btn onClick={() => go('revHist')}>去历史审核修改</Btn></div>}
              </DeskPanel>
            ) : (
              <DeskPanel title={editing ? '修改我的结论' : '我的结论'}>
                {editStale && <div role="alert" style={{ fontSize: 12.5, lineHeight: 1.8, color: 'var(--red)', marginBottom: 14 }}>审核状态或你的结论已变化，当前修改无法保存。请返回历史审核查看最新结果。</div>}
                <DecisionChips choices={CHOICES} value={decision} onChange={setDecision} />

                {(choice.needScore || needScoreForAccept) && (
                  <DeskScoreField
                    label={<>认定分值 · 规则允许 {lo} — {hi === Infinity ? '不封顶' : hi}{needScoreForAccept && '（学生未填数量，通过也要给分）'}</>}
                    value={score}
                    onChange={setScore}
                    invalid={scoreBad || acceptScoreBad}
                    hint={<>请填 {lo} — {hi === Infinity ? '任意' : hi} 之间的数字</>}
                    placeholder={d?.requestedScore == null ? '按佐证认定' : String(d.requestedScore)}
                  />
                )}

                {/* 编辑器里有工具条按钮，不能再套 <label>——一个 label 关联多个控件，
                    点标题会跳到哪个控件是没定论的。 */}
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 20 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: whyBad && why.trim() ? 'var(--red)' : 'var(--fg2)' }}>
                    审核意见
                  </span>
                  <MarkdownEditor
                    onUploadingChange={setUploading}
                    value={why}
                    onChange={setWhy}
                    invalid={whyBad && why.trim() !== ''}
                    evidence={quotable}
                    uploadTarget={d ? { kind: 'note', owner: 'submission', id: d.id } : undefined}
                    placeholder={choice.needWhy ? '写清依据的是哪条细则、佐证里的哪一部分。这段话学生能看到' : '一致通过时可留空'}
                  />
                </div>

                <label style={{ display: 'flex', alignItems: 'center', gap: 9, marginTop: 18, fontSize: 12.5, color: 'var(--fg2)' }}><input type="checkbox" checked={suggestClassification} onChange={(event) => setSuggestClassification(event.target.checked)} />同时提出分类调整建议</label>
                {suggestClassification && (
                  <div style={{ border: '1px solid var(--line)', padding: 14, marginTop: 10 }}>
                    <div data-r="split" style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)', gap: 9 }}>
                      <label style={{ display: 'flex', flexDirection: 'column', gap: 8, fontSize: 12.5, color: 'var(--fg2)' }}>
                        建议大项
                        <select value={suggestCategory} onChange={(event) => setSuggestCategory(event.target.value as CategoryKey)} style={fieldStyle}>
                          {currentScheme.data?.categories.filter((category) => claimableItems(category).length).map((category) => <option key={category.key} value={category.key}>{category.name}</option>)}
                        </select>
                      </label>
                      <label style={{ display: 'flex', flexDirection: 'column', gap: 8, fontSize: 12.5, color: 'var(--fg2)' }}>
                        建议小项
                        <select value={suggestItem} onChange={(event) => setSuggestItem(event.target.value)} style={fieldStyle}>
                          {suggestItems.map((item) => <option key={item.key} value={item.key}>{item.name}</option>)}
                        </select>
                      </label>
                    </div>
                    <label style={{ display: 'flex', flexDirection: 'column', gap: 8, fontSize: 12.5, color: 'var(--fg2)', marginTop: 12 }}>
                      分类调整理由 · 必填，至少 4 个字
                      <textarea value={suggestReason} onChange={(event) => setSuggestReason(event.target.value)} placeholder="说明为什么应归入所选大项和小项，至少 4 个字" style={{ ...fieldStyle, minHeight: 70, resize: 'vertical' }} />
                    </label>
                    {suggestCategory !== d?.category && <div style={{ color: 'var(--fg3)', fontSize: 12, lineHeight: 1.7, marginTop: 8 }}>跨大项建议会交班级管理员终裁；本次初审仍按当前归类的规则给出认定分。</div>}
                  </div>
                )}

                <div style={{ display: 'flex', gap: 10, marginTop: 20, flexWrap: 'wrap' }}>
                  <Btn primary tone={choice.tone} disabled={uploading || !canSubmit || classificationBad} onClick={submit}>
                    {decide.isPending ? (editing ? '保存中…' : '提交中…') : editing ? '保存修改' : '提交结论'}
                  </Btn>
                  {historySubmissionId ? <Btn onClick={onBack}>取消修改</Btn> : <Btn onClick={() => goNextPending(idx)}>先跳过</Btn>}
                </div>
                {submitHint && <div role="status" style={{ color: 'var(--red)', fontSize: 12.5, lineHeight: 1.7, marginTop: 10 }}>{submitHint}</div>}
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 14, textWrap: 'pretty' }}>
                  {historySubmissionId ? '保存后返回历史审核，旧结论与修改记录会保留。' : '提交后自动跳到下一条'}
                </div>
              </DeskPanel>
            )}

            <ReviewerProgress
              title="双评状态"
              rows={[
                { who: '你', state: d?.myReview ? '已提交结论' : '待提交', tone: d?.myReview ? 'var(--ok)' : 'var(--warn)', bold: true },
                {
                  who: '另一名审核人',
                  /* 只由"总结论数 − 我交没交"推出对方交没交，永远拿不到对方的分与理由 */
                  state: peerSubmitted ? '已提交结论' : '尚未提交',
                  tone: peerSubmitted ? 'var(--ok)' : 'var(--fg3)',
                },
              ]}
              note="两人结论一致就定分，不一致交班级管理员。"
            />

            {/* AI 预审建议属于 M5。AI_ENABLED=false 时这一整块不渲染（§7.0）。 */}
            {AI_ENABLED && (
              <div style={{ border: '1px solid var(--line)', padding: '18px 20px' }}>
                <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>AI 预审建议</div>
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.75, marginTop: 8 }}>
                  暂无可用建议。
                </div>
              </div>
            )}

            <Note>驳回记 0 分，学生可以申诉。</Note>
          </DeskSide>
        </SplitCol>
      </Split>

      {!historySubmissionId && (
        <ReviewQueueBar
          items={items.map((task) => ({ id: task.id, title: task.title, tip: `#${task.id} · ${task.title}`, done: doneIds.has(task.id) }))}
          index={idx}
          onGo={goTo}
          hint={`${pending > 0 ? `还剩 ${pending} 条 · 可以跳着审` : '这一批审完了 · 绿色的点开可以回看'}${done.length > 0 ? ` · 已审 ${done.length} 条` : ''}`}
        />
      )}
    </div>
  )
}
