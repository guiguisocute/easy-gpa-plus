/* 班级管理员 · 仲裁与申诉。

   这是全系统唯一能改分的地方，所以做了三件事：
   1. 双评对照并排显示，两人姓名在这里可见（学生端仍单盲）；
   2. 终裁必须写理由，理由原样回写学生端，并同步给原两名审核人；
   3. 全过程轨迹留档——改判不是抹掉旧结论，是在轨迹上再加一条。

   三种对象共用一个台面但走不同接口：
   · 双评冲突   → POST /admin/submissions/:id/arbitrate
   · 申诉       → POST /admin/appeals/:id/final     （原班人马复评不一致，或学生的第 2 次申诉）
   · 小组异议   → POST /admin/objections/:id/decide （小组的扣分与异议提案，签了才生效）

   申诉在这里只有两种入口状态：escalated（该你出手）与 reviewing（复评人还在处理，你只读）。
   reviewing 也允许接管——复评人退学、失联这类事真的会发生，但接管要显式点一下并写进审计，
   不能让"顺手替他们判了"变成默认路径。 */

import { useEffect, useMemo, useState } from 'react'
import { Paperclip } from 'lucide-react'
import { BackLink, Btn, ChoiceChip, Empty, LineTabs, Note, PageHead, Pill, Row, Split, SplitCol, Sub, Table, TextBtn, THead, TRow, Timeline } from '@/components/ui'
import { AppealBrief, StepBar } from '@/components/AppealBrief'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { ReportMaterial } from '@/components/ReportMaterial'
import { ObjectionMaterial } from '@/components/ObjectionMaterial'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { RichText } from '@/components/Markdown'
import { ReviewSubject } from '@/components/ReviewSubject'
import { RuleCard } from '@/components/RuleCard'
import { StudentNote } from '@/components/StudentNote'
import { ClassificationConfirmation } from '@/components/ClassificationConfirmation'
import { ForceRejectSubmissionForm } from '@/components/ForceRejectSubmissionForm'
import { ForcedRejectionNotice } from '@/components/ForcedRejectionNotice'
import { useAdjudicationScope } from '@/api/adjudicationScope'
import { DEPUTY_RECUSAL_NOTE, isAdjudicationRecused } from '@/lib/adjudication'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { claimableItems, schemePathName } from '@/lib/schemeTree'
import { findCurrentItem } from '@/lib/ruleDiff'
import { readableTrail } from '@/lib/trail'
import { useApp } from '@/stores/app'
import { useAgentFormField, useRegisterAgentPage } from '@/stores/agentPage'
import { ApiError } from '@/api/client'
import {
  useAdminAppeals,
  useAdminObjections,
  useAdminReports,
  useAdminSubmissions,
  useAppeal,
  useAppealFinalActions,
  useArbitrationActions,
  useClassificationActions,
  useClassificationSuggestions,
  useObjectionDecisionActions,
  useReportFinalActions,
  useScheme,
} from '@/api/queries'
import {
  APPEAL_STATUS_LABEL,
  appealRoundLabel,
  DECISION_LABEL,
  OBJECTION_KIND_LABEL,
  OBJECTION_STATUS_LABEL,
  REPORT_DECISION_LABEL,
  REPORT_KIND_LABEL,
  REPORT_STATUS_LABEL,
  REREVIEW_LABEL,
  type AdminReport,
  type AdminSubmission,
  type Appeal,
  type AppealHandler,
  type AppealStatus,
  type ClassificationSuggestion,
  type Evidence,
  type NamedReview,
  type Objection,
  type ObjectionStatus,
} from '@/api/types'
import { STATUS_LABEL, type CategoryKey, type SubmissionStatus } from '@/lib/types'

const TABS = [
  { key: 'arbitrating', label: '冲突待裁' },
  { key: 'appeal', label: '申诉' },
  { key: 'objection', label: '小组提案' },
  { key: 'report', label: '学生举报' },
  { key: 'classification', label: '分类确认' },
] as const

type Tab = (typeof TABS)[number]['key'] | 'forceReject'

const STATUS_TONE: Record<SubmissionStatus, 'ok' | 'warn' | 'bad' | 'idle'> = {
  draft: 'idle', pending: 'idle', consensus: 'warn', scored: 'ok',
  appealing: 'warn', arbitrating: 'bad', locked: 'ok',
}

const APPEAL_TONE: Record<AppealStatus, 'ok' | 'warn' | 'bad' | 'idle'> = {
  filed: 'idle', reviewing: 'warn', escalated: 'bad', resolved: 'ok', final: 'ok',
}

const OBJECTION_TONE: Record<ObjectionStatus, 'ok' | 'warn' | 'bad' | 'idle'> = {
  draft: 'idle', submitted: 'warn', applied: 'ok', adjusted: 'ok', dismissed: 'bad', withdrawn: 'idle',
}




const APPEAL_TARGET_LABEL = { submission: '提交条目', base_score: '基础项', penalty_score: '扣分项' } as const

/* 四种台面各自有一张详情页，学生姓名一律走这张卡，和初审工作台、小组复评台同一个形状。
   原先姓名塞在 PageHead 那行 12.5px 的灰字里，标题是条目名、按钮是终裁，
   唯独"我现在判的是谁"最不显眼。

   吸顶和初审台一致：仲裁详情里佐证、双评对照、轨迹加起来能滚好几屏，滚下去之后
   谁都看不见判的是谁。间距用同级的空 div 撑，不能把 ReviewSubject 包进带 padding
   的容器——sticky 只在父元素的盒子里生效，包一层就等于把它锁死在原地。 */
function ArbitrationSubject({ label, name, sid, meta }: { label: string; name: string; sid: string; meta: string }) {
  return (
    <>
      <ReviewSubject sticky label={label} name={name} sid={sid} meta={meta} />
      <div style={{ height: 22 }} />
    </>
  )
}

function ReviewCard({ r, evidence }: { r: NamedReview; evidence?: Evidence[] }) {
  return (
    <div style={{ border: '1px solid var(--line)', padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 10, minWidth: 0 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 12.5, color: 'var(--fg)', fontWeight: 600 }}>{r.reviewer}</span>
        {r.reviewerSid && <span style={mono('11px', '.02em')}>{r.reviewerSid}</span>}
        <span style={{ marginLeft: 'auto' }}>
          <Pill tone={r.decision === 'accepted' ? 'ok' : r.decision === 'rejected' ? 'bad' : 'warn'}>{DECISION_LABEL[r.decision]}</Pill>
        </span>
      </div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
        <span style={{ fontSize: 28, fontWeight: 600, letterSpacing: '-.045em', ...num }}>{f.score(r.score)}</span>
        <span style={{ fontSize: 12, color: 'var(--fg3)' }}>分</span>
        <span style={{ marginLeft: 'auto', ...mono('11px', '0') }}>{f.dateTime(r.at)} · 用时 {f.duration(r.spentSeconds)}</span>
      </div>
      {r.reason ? (
        <RichText value={r.reason} evidence={evidence} />
      ) : (
        <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>（未填写理由）</span>
      )}
    </div>
  )
}

/** 复评卡。管理员这一侧姓名全见，两条并排就看得出分歧在哪。 */
function RereviewCard({ h, evidence, path }: { h: AppealHandler; evidence?: Evidence[]; path?: string }) {
  return (
    <div style={{ border: `1px solid ${h.decided ? 'var(--line)' : 'var(--line2)'}`, padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 9, minWidth: 0 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 12.5, color: 'var(--fg)', fontWeight: 600 }}>{h.name ?? `复评人 #${h.id}`}</span>
        {h.sid && <span style={mono('11px', '.02em')}>{h.sid}</span>}
        <span style={{ marginLeft: 'auto' }}>
          {h.rereview ? (
            <Pill tone={h.rereview.decision === 'uphold' ? 'idle' : 'warn'}>{REREVIEW_LABEL[h.rereview.decision]}</Pill>
          ) : (
            <Pill tone="warn">尚未复评</Pill>
          )}
        </span>
      </div>
      {h.rereview && (
        <>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
            <span style={{ fontSize: 24, fontWeight: 600, letterSpacing: '-.04em', ...num }}>{f.score(h.rereview.score)}</span>
            <span style={{ fontSize: 12, color: 'var(--fg3)' }}>分</span>
            <span style={{ marginLeft: 'auto', ...mono('11px', '0') }}>{f.dateTime(h.rereview.at)} · 用时 {f.duration(h.rereview.spentSeconds)}</span>
          </div>
          {path && <div style={{ fontSize: 12, color: 'var(--fg3)' }}>归类：{path}</div>}
          <RichText value={h.rereview.reason} evidence={evidence} />
        </>
      )}
    </div>
  )
}

/* ---- 冲突条目终裁 ---- */

function ConflictDetail({ item, queue, onOpen, onBack, classificationSuggestions }: { item: AdminSubmission; queue: AdminSubmission[]; onOpen: (row: AdminSubmission) => void; onBack: () => void; classificationSuggestions?: ClassificationSuggestion[] }) {
  const [uploading, setUploading] = useState(false)
  const say = useApp((s) => s.say)
  const me = useApp((s) => s.user)
  const scheme = useScheme()
  const at = queue.findIndex((row) => row.id === item.id)
  const arbitrate = useArbitrationActions()
  const classification = useClassificationActions()
  const [score, setScore] = useAgentFormField<string>('submission', item.id, item.updatedAt, 'score', '')
  const [reason, setReason] = useAgentFormField<string>('submission', item.id, item.updatedAt, 'reason', '')
	const [suggestReason, setSuggestReason] = useState('')
	const [category, setCategory] = useAgentFormField('submission', item.id, item.updatedAt, 'category', item.category)
	const [itemKey, setItemKey] = useAgentFormField('submission', item.id, item.updatedAt, 'itemKey', item.itemKey)
	const categoryConfig = scheme.data?.categories.find((row) => row.key === category)
	const categoryClaimable = useMemo(() => categoryConfig ? claimableItems(categoryConfig) : [], [categoryConfig])
	useEffect(() => {
		if (categoryClaimable.length && !categoryClaimable.some((row) => row.key === itemKey)) setItemKey(categoryClaimable[0].key)
	}, [categoryClaimable, itemKey, setItemKey])

  const scores = item.reviews.map((r) => r.score)
  const gap = scores.length === 2 ? Math.abs(scores[0] - scores[1]) : null
  const num2 = Number(score)
  const selfRecused = isAdjudicationRecused(item, item.studentId, me?.sid)
  const filedRule = item.ruleSnapshot?.item.scoreRule
  const ruleUnit = filedRule && 'unit' in filedRule ? filedRule.unit : undefined
  const itemQuotable = [...(item.evidence ?? []), ...(item.noteEvidence ?? [])]
  const ok = !uploading && !selfRecused && score.trim() !== '' && !Number.isNaN(num2) && num2 >= 0 && !!itemKey && reason.trim().length >= 6

  useRegisterAgentPage({ kind: 'submission', id: item.id, label: `${item.student} · #${item.id} · ${item.title}`,
    evidenceCount: itemQuotable.length, version: item.updatedAt, draft: { score, reason, category, itemKey }, onReason: setReason,
    editable: !selfRecused && item.status === 'arbitrating' })

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <BackLink label="回到仲裁台" onClick={onBack} />

      <PageHead
        en={`ARBITRATION · #${item.id}`}
        title={item.title}
        desc={`${classificationSuggestions?.length ? '跨大项分类待裁定' : '双评不一致的条目'} · 提交于 ${f.dateTime(item.submittedAt)}`}
        side={
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 6 }}>
            <Pill tone={STATUS_TONE[item.status]}>{STATUS_LABEL[item.status]}</Pill>
            {gap !== null && <span style={{ fontSize: 12.5, color: 'var(--red)' }}>两方分差 {f.score(gap)} 分</span>}
          </div>
        }
      />

      <ArbitrationSubject
        label="当前仲裁学生"
        name={item.student}
        sid={item.studentId}
        meta={`冲突条目：${item.title} · ${scheme.data?.categories.find((c) => c.key === item.category)?.name ?? item.category}`}
      />

      {classificationSuggestions?.map((suggestion) => <div key={suggestion.id} style={{ paddingBottom: 22 }}>
        <Sub title="待裁分类建议" note={`${schemePathName(scheme.data, suggestion.fromCategory, suggestion.fromItemKey)} → ${schemePathName(scheme.data, suggestion.toCategory, suggestion.toItemKey)}`} />
        <RichText value={suggestion.reason} evidence={suggestion.noteEvidence} />
      </div>)}

      {queue.length > 1 && (
        <div style={{ paddingBottom: 20 }}>
          <StepBar
            at={at}
            total={queue.length}
            prevLabel={at > 0 ? `${queue[at - 1].student} · ${queue[at - 1].title}` : ''}
            nextLabel={at >= 0 && at < queue.length - 1 ? `${queue[at + 1].student} · ${queue[at + 1].title}` : ''}
            onPrev={() => at > 0 && onOpen(queue[at - 1])}
            onNext={() => at >= 0 && at < queue.length - 1 && onOpen(queue[at + 1])}
          />
        </div>
      )}

      <Sub title="双评对照" note="学生那边只看得到结论和理由" />
      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, paddingBottom: 26 }}>
        {item.reviews.map((r) => (
          <ReviewCard key={r.id} r={r} evidence={[...(item.evidence ?? []), ...(item.noteEvidence ?? [])]} />
        ))}
        {item.reviews.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>还没有任何审核结论。</div>}
      </div>

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '10px 0 30px' }}>
            {/* 原来这一栏只有六行元数据。两名审核人为什么各说各话，得看学生当初交了什么，
                所以申报值、补充说明、佐证和规则快照都摆在这里。 */}
            <Sub title="学生交的是什么" note="提交当时的原文" />
            <Row label="学生" value={`${item.student} · ${item.studentId}`} />
            <Row label="学生自报" value={f.claimText(item.claim, ruleUnit)} />
            <Row label="学生期望分" value={f.score(item.requestedScore)} />
            <Row label="当前认定分" value={f.score(item.finalScore)} />
			<Row label="原始归类" value={schemePathName(scheme.data, item.filedCategory ?? item.category, item.filedItemKey ?? item.itemKey)} />
			<Row label="当前有效归类" value={schemePathName(scheme.data, item.category, item.itemKey)} />
            <Row label="最近更新" value={f.dateTime(item.updatedAt)} />

            <div style={{ paddingTop: 20 }}>
              <StudentNote value={item.note} evidence={itemQuotable} />
            </div>

            <div style={{ paddingTop: 20 }}>
              <Sub title="学生佐证" note={`${item.evidence?.length ?? 0} 份`} />
              {item.evidence?.length ? <EvidenceFiles items={item.evidence} /> : <Note>这一条没有佐证附件。</Note>}
            </div>

            {!!item.noteEvidence?.length && (
              <div style={{ paddingTop: 20 }}>
                <Sub title="说明与审核意见里的附件" note={`${item.noteEvidence.length} 份`} />
                <EvidenceFiles items={item.noteEvidence} />
              </div>
            )}

            {item.ruleSnapshot && (
              <div style={{ paddingTop: 22 }}>
                <RuleCard
                  item={item.ruleSnapshot.item}
                  categoryName={item.ruleSnapshot.categoryName}
                  capturedAt={item.ruleSnapshot.capturedAt}
                  currentItem={findCurrentItem(scheme.data, item.ruleSnapshot.item.key)}
                  claim={item.claim}
                  requestedScore={item.requestedScore}
                />
              </div>
            )}
          </div>
        </SplitCol>
        <SplitCol>
          <div style={{ padding: '10px 0 30px' }}>
            <Sub title="终裁" note={selfRecused ? '这是你自己的条目，你要回避' : undefined} />
            {selfRecused && (
              <div style={{ marginBottom: 16 }}>
                <Note tone="warn">
                  {item.recusalReason || DEPUTY_RECUSAL_NOTE}
                </Note>
              </div>
            )}
			<div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, paddingBottom: 16 }}><select value={category} onChange={(event) => setCategory(event.target.value as typeof category)} style={fieldStyle}>{scheme.data?.categories.filter((row) => claimableItems(row).length).map((row) => <option key={row.key} value={row.key}>{row.name}</option>)}</select><select value={itemKey} onChange={(event) => setItemKey(event.target.value)} style={fieldStyle}>{categoryClaimable.map((row) => <option key={row.key} value={row.key}>{row.name}</option>)}</select></div>
			{(item.status === 'scored' || item.status === 'arbitrating' || item.status === 'locked') && (category !== item.category || itemKey !== item.itemKey) && <div style={{ border: '1px solid var(--line)', padding: 12, marginBottom: 16 }}>
				<div style={{ display: 'flex', gap: 9 }}><input value={suggestReason} onChange={(event) => setSuggestReason(event.target.value)} placeholder="理由" style={{ ...fieldStyle, flex: 1 }} /><Btn disabled={selfRecused || suggestReason.trim().length < 4 || classification.suggest.isPending} onClick={() => classification.suggest.mutate({ submissionId: item.id, category, itemKey, reason: suggestReason.trim() }, { onSuccess: () => { say('分类建议已登记'); onBack() }, onError: (error) => say(error instanceof ApiError ? error.message : '分类建议提交失败') })}>登记建议</Btn></div>
			</div>}
            <label style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingBottom: 16 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>最终认定分</span>
              <input
                value={score}
                onChange={(e) => setScore(e.target.value)}
                inputMode="decimal"
                placeholder={scores.length ? `两方给的是 ${scores.map((s) => f.score(s)).join(' 与 ')}` : '按规则快照认定'}
                style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--warn)', padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
              />
            </label>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>终裁理由 · 至少 6 字</span>
              <MarkdownEditor
                onUploadingChange={setUploading}
                value={reason}
                onChange={setReason}
                minHeight={92}
                evidence={[...(item.evidence ?? []), ...(item.noteEvidence ?? [])]}
                uploadTarget={{ kind: 'note', owner: 'submission', id: item.id }}
                placeholder="写清依据哪条细则，以及为什么是这个分"
              />
            </div>
            <div style={{ display: 'flex', gap: 10, marginTop: 16, flexWrap: 'wrap' }}>
              <Btn
                primary
                disabled={!ok || arbitrate.isPending}
                onClick={() =>
                  arbitrate.mutate(
					{ id: item.id, category, itemKey, score: num2, reason: reason.trim() },
                    {
                      onSuccess: (r) => {
                        say(`已终裁 · 认定 ${f.score(r.finalScore)} 分，已同步给学生与两名审核人`)
                        onBack()
                      },
                      onError: (e) => say(e instanceof ApiError ? e.message : '终裁失败'),
                    },
                  )
                }
              >
                {arbitrate.isPending ? '提交中…' : '定分并发出'}
              </Btn>
            </div>
            <div style={{ marginTop: 18 }}>
              <Note>
                裁完之后，学生和原审核人都会看到结果。
              </Note>
            </div>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}

/* ---- 申诉终裁 ---- */

function AppealDetailView({ id, queue, onOpen, onBack }: { id: string; queue: Appeal[]; onOpen: (id: string) => void; onBack: () => void }) {
  const [uploading, setUploading] = useState(false)
  const say = useApp((s) => s.say)
  const me = useApp((s) => s.user)
  const detail = useAppeal(id)
  const d = detail.data
  const at = queue.findIndex((row) => row.id === id)
  const finalize = useAppealFinalActions()
  const scheme = useScheme()
  const [score, setScore] = useAgentFormField('appeal', id, d?.updatedAt, 'score', d?.proposedScore == null ? '' : String(d.proposedScore))
  const [reason, setReason] = useAgentFormField<string>('appeal', id, d?.updatedAt, 'reason', '')
  const [takeover, setTakeover] = useState(false)
  const [category, setCategory] = useAgentFormField<CategoryKey>('appeal', id, d?.updatedAt, 'category', d?.proposedCategory ?? d?.originalCategory ?? d?.category ?? 'moral')
  const [itemKey, setItemKey] = useAgentFormField('appeal', id, d?.updatedAt, 'itemKey', d?.proposedItemKey ?? d?.originalItemKey ?? d?.itemKey ?? '')

  useRegisterAgentPage(d ? { kind: 'appeal', id: d.id, label: `${d.student} · #${d.id} · ${d.target}`,
    evidenceCount: (d.evidence?.length ?? 0) + (d.originalEvidence?.length ?? 0) + (d.noteEvidence?.length ?? 0), version: d.updatedAt,
    draft: { score, reason, category, itemKey }, onReason: setReason,
    editable: !isAdjudicationRecused(d, d.studentId, me?.sid) && (d.status === 'escalated' || (d.status === 'reviewing' && takeover)) } : null)

  if (detail.isLoading) return <div className="load-bar"><span /></div>
  if (!d) return <Empty title="申诉不存在" desc="它可能已经被处理或撤回。" />

  /* 正文里能内嵌的佐证＝申诉补传的 + 原提交的。渲染器只解析出现在这里的 id。 */
  const appealQuotable = [...(d.evidence ?? []), ...(d.originalEvidence ?? []), ...(d.noteEvidence ?? [])]

  const num2 = Number(score)
  const categoryConfig = scheme.data?.categories.find((row) => row.key === category)
  const categoryItems = categoryConfig ? claimableItems(categoryConfig) : []
  const ok = !uploading && score.trim() !== '' && !Number.isNaN(num2) && reason.trim().length >= 6 && (d.targetType !== 'submission' || !!itemKey)
  const selfRecused = isAdjudicationRecused(d, d.studentId, me?.sid)
  const closed = d.status === 'final'
  /* 一审复评一致就自己结案了（resolved），那一轮不需要你签字；
     真正等你的是 escalated。reviewing 要接管得先显式点一下。 */
  const mine = !selfRecused && (d.status === 'escalated' || (d.status === 'reviewing' && takeover))
  const named = (d.originalReviews ?? []).filter((r): r is NamedReview => 'reviewer' in r)
  const decidedCount = d.handlers.filter((h) => h.decided).length

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <BackLink label="回到仲裁台" onClick={onBack} />

      <PageHead
        en={`APPEAL · #${d.id} · ROUND ${d.round}`}
        title={d.target}
        desc={`${APPEAL_TARGET_LABEL[d.targetType]} · ${appealRoundLabel(d.round)} · 发起于 ${f.dateTime(d.createdAt)}`}
        side={
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 6 }}>
            <Pill tone={APPEAL_TONE[d.status]}>{APPEAL_STATUS_LABEL[d.status]}</Pill>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
              {d.round === 2 ? `${appealRoundLabel(2)} · 不经复评，直接由你终裁` : `${appealRoundLabel(1)} · 复评进度 ${decidedCount}/${d.handlers.length}`}
            </span>
          </div>
        }
      />

      <ArbitrationSubject
        label="当前终裁学生"
        name={d.student}
        sid={d.studentId}
        meta={`申诉对象：${d.target} · ${APPEAL_TARGET_LABEL[d.targetType]} · ${appealRoundLabel(d.round)}`}
      />


      {queue.length > 1 && (
        <div style={{ paddingBottom: 20 }}>
          <StepBar
            at={at}
            total={queue.length}
            prevLabel={at > 0 ? `${queue[at - 1].student} · ${queue[at - 1].target}` : ''}
            nextLabel={at >= 0 && at < queue.length - 1 ? `${queue[at + 1].student} · ${queue[at + 1].target}` : ''}
            onPrev={() => at > 0 && onOpen(queue[at - 1].id)}
            onNext={() => at >= 0 && at < queue.length - 1 && onOpen(queue[at + 1].id)}
          />
        </div>
      )}

      <div style={{ marginBottom: 22 }}>
        <AppealBrief
          reason={d.reason}
          evidence={appealQuotable}
          note={appealRoundLabel(d.round)}
          originalPath={d.targetType === 'submission' ? schemePathName(scheme.data, d.originalCategory ?? d.category, d.originalItemKey ?? d.itemKey) : undefined}
          originalScore={d.baselineScore}
          proposedPath={d.targetType === 'submission' ? schemePathName(scheme.data, d.proposedCategory ?? d.category, d.proposedItemKey ?? d.itemKey) : undefined}
          proposedScore={d.proposedScore}
        />
      </div>

      {/* 班管终裁前也要能对着完整的档位表看：改判到哪一档、那一档几分。 */}
      {d.ruleSnapshot && (
        <div style={{ marginBottom: 22 }}>
          <RuleCard item={d.ruleSnapshot.item} categoryName={d.ruleSnapshot.categoryName} capturedAt={d.ruleSnapshot.capturedAt} currentItem={findCurrentItem(scheme.data, d.ruleSnapshot.item.key)} />
        </div>
      )}

      {/* 第 2 次申诉必须把上一轮摆在眼前：学生反驳的正是那一轮的结论 */}
      {d.previousRound && (
        <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '16px 18px', marginBottom: 22, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>{appealRoundLabel(1)}的结果 · #{d.previousRound.id}</span>
            <span style={{ marginLeft: 'auto', ...mono('11px', '0') }}>{f.dateTime(d.previousRound.resolvedAt)}</span>
          </div>
          <div><Sub title="当时的申诉理由" /><RichText value={d.previousRound.reason} evidence={appealQuotable} /></div>
          <Row label="复评定的分" value={f.score(d.previousRound.resolutionScore)} />
          <RichText value={d.previousRound.resolutionReason ?? ''} evidence={appealQuotable} />
          <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            {d.previousRound.handlers.map((h) => (
              <RereviewCard key={h.id} h={h} evidence={appealQuotable} path={h.rereview?.category && h.rereview.itemKey ? schemePathName(scheme.data, h.rereview.category, h.rereview.itemKey) : undefined} />
            ))}
          </div>
        </div>
      )}

      {d.handlers.length > 0 && (
        <>
          <Sub title="本轮复评" note={d.status === 'escalated' ? '两人复评不一致，所以升到了你这里' : '复评人的结论，姓名对你可见'} />
          <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, paddingBottom: 26 }}>
            {d.handlers.map((h) => (
              <RereviewCard key={h.id} h={h} evidence={appealQuotable} path={h.rereview?.category && h.rereview.itemKey ? schemePathName(scheme.data, h.rereview.category, h.rereview.itemKey) : undefined} />
            ))}
          </div>
        </>
      )}

      {named.length > 0 && (
        <>
          <Sub title="第一轮双评结论" />
          <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, paddingBottom: 26 }}>
            {named.map((r) => (
              <ReviewCard key={r.id} r={r} evidence={appealQuotable} />
            ))}
          </div>
        </>
      )}

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '10px 0 30px' }}>
            <Sub title="轨迹" note="旧结论还在" />
            <Timeline
              layout="vertical"
              items={readableTrail(d.trail).map((t) => ({
                title: t.title,
                at: f.dateTime(t.at),
                tone: t.action.includes('final') || t.action.includes('escalat') ? 'var(--red)' : 'var(--fg3)',
              }))}
            />
            <div style={{ paddingTop: 14 }}>
              <Row label="这一轮的起点分" value={f.score(d.baselineScore)} />
              <Row label="当前分值" value={f.score(d.currentScore)} />
              {d.targetType !== 'submission' && (
                <>
                  <Row label="满分" value={d.fullScore === undefined || d.fullScore === null ? '—' : f.score(d.fullScore)} />
                  <Row label="录入依据" value={d.basis ?? '—'} />
                </>
              )}
            </div>
            {(d.evidence?.length ?? 0) > 0 && (
              <div style={{ paddingTop: 14 }}>
                <Sub title="申诉时补传的佐证" />
                <EvidenceFiles items={d.evidence} />
              </div>
            )}
            {(d.originalEvidence?.length ?? 0) > 0 && (
              <div style={{ paddingTop: 18 }}>
                <Sub title="原有佐证" />
                <EvidenceFiles items={d.originalEvidence!} />
              </div>
            )}
          </div>
        </SplitCol>
        <SplitCol>
          <div style={{ padding: '10px 0 30px' }}>
            {closed ? (
              <>
                <Sub title="已终裁" />
                <Row label="裁定分" value={f.score(d.resolutionScore)} />
                {d.targetType === 'submission' && <Row label="裁定归类" value={schemePathName(scheme.data, d.resolutionCategory ?? d.category, d.resolutionItemKey ?? d.itemKey)} />}
                <Row label="结案时间" value={f.dateTime(d.resolvedAt)} />
                <div style={{ marginTop: 12 }}><RichText value={d.resolutionReason ?? ''} evidence={appealQuotable} /></div>
                <div style={{ marginTop: 18 }}>
                  <Note>你裁完就不再接受申诉，还有意见只能线下解决。</Note>
                </div>
              </>
            ) : d.status === 'resolved' ? (
              <>
                <Sub title="一审已结案" note="两名复评人结论一致，不用你出手" />
                <Row label="复评定的分" value={f.score(d.resolutionScore)} />
                {d.targetType === 'submission' && <Row label="复评归类" value={schemePathName(scheme.data, d.resolutionCategory ?? d.category, d.resolutionItemKey ?? d.itemKey)} />}
                <Row label="结案时间" value={f.dateTime(d.resolvedAt)} />
                <div style={{ marginTop: 12 }}><RichText value={d.resolutionReason ?? ''} evidence={appealQuotable} /></div>
                <div style={{ marginTop: 18 }}>
                  <Note tone="warn">学生不服还能再提一次，那一次直接到你手上。</Note>
                </div>
              </>
            ) : selfRecused ? (
              <>
                <Sub title="你要回避" note="这是你自己的申诉" />
                <Note tone="warn">
                  {d.recusalReason || DEPUTY_RECUSAL_NOTE}
                </Note>
              </>
            ) : !mine ? (
              <>
                <Sub title="复评进行中" note={`${decidedCount}/${d.handlers.length} 已提交`} />
                <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.8, textWrap: 'pretty' }}>
                  原来那两个人复评，谈不拢才到你这里——现在还不需要你出手。
                </div>
                <div style={{ marginTop: 16 }}>
                  <Btn onClick={() => setTakeover(true)}>复评停滞？直接接管终裁</Btn>
                </div>
                <div style={{ marginTop: 18 }}>
                  <Note>接管会跳过还没提交的复评人，并把"由管理员接管"记进审计。只有复评人退学、或者长期联系不上，才该这么做。</Note>
                </div>
              </>
            ) : (
              <>
                <Sub
                  title="终裁"
                  note={d.status === 'reviewing' ? '接管模式 · 跳过还没提交的复评人' : d.round === 2 ? `${appealRoundLabel(2)} · 你的裁定即为最终结论` : '复评不一致，由你定分'}
                />
                {d.targetType === 'submission' && <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 9, paddingBottom: 16 }}>
                  <select value={category} onChange={(event) => { const next = event.target.value as CategoryKey; setCategory(next); const config = scheme.data?.categories.find((row) => row.key === next); setItemKey(config ? claimableItems(config)[0]?.key ?? '' : '') }} style={fieldStyle}>
                    {scheme.data?.categories.map((row) => <option key={row.key} value={row.key}>{row.name}</option>)}
                  </select>
                  <select value={itemKey} onChange={(event) => setItemKey(event.target.value)} style={fieldStyle}>
                    {categoryItems.map((item) => <option key={item.key} value={item.key}>{item.name}</option>)}
                  </select>
                </div>}
                <label style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingBottom: 16 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>裁定分</span>
                  <input
                    value={score}
                    onChange={(e) => setScore(e.target.value)}
                    inputMode="decimal"
                    placeholder={
                      d.handlers.some((h) => h.rereview)
                        ? `复评给的是 ${d.handlers.filter((h) => h.rereview).map((h) => f.score(h.rereview!.score)).join(' 与 ')}`
                        : `当前 ${f.score(d.currentScore)}`
                    }
                    style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--warn)', padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
                  />
                </label>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>裁定理由</span>
                  <MarkdownEditor
                    onUploadingChange={setUploading}
                    value={reason}
                    onChange={setReason}
                    minHeight={92}
                    evidence={appealQuotable}
                    uploadTarget={{ kind: 'note', owner: 'appeal', id: d.id }}
                    placeholder="维持原判也要写清为什么——学生要的是一个说得通的理由，不是一个结果"
                  />
                </div>
                <div style={{ display: 'flex', gap: 10, marginTop: 16, flexWrap: 'wrap' }}>
                  <Btn
                    primary
                    disabled={!ok || finalize.isPending}
                    onClick={() =>
                      finalize.mutate(
                        { id: d.id, category: d.targetType === 'submission' ? category : undefined, itemKey: d.targetType === 'submission' ? itemKey : undefined, score: num2, reason: reason.trim() },
                        {
                          onSuccess: () => {
                            say('已定分 · 学生已经能看到了，也记进了审计')
                            onBack()
                          },
                          onError: (e) => say(e instanceof ApiError ? e.message : '终裁失败'),
                        },
                      )
                    }
                  >
                    {finalize.isPending ? '提交中…' : '定分并发出'}
                  </Btn>
                  {d.status === 'reviewing' && <Btn onClick={() => setTakeover(false)}>取消接管</Btn>}
                </div>
                <div style={{ marginTop: 18 }}>
                  <Note>你签字之后，这一笔就不再接受申诉了，不管学生之前提过几次。这是他第 {d.round} 次提。</Note>
                </div>
              </>
            )}
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}

/* ---- 小组异议终裁 ---- */

function ObjectionDetail({ item, onBack }: { item: Objection; onBack: () => void }) {
  const [uploading, setUploading] = useState(false)
  const say = useApp((s) => s.say)
  const me = useApp((s) => s.user)
  const selfRecused = isAdjudicationRecused(item, item.studentId, me?.sid)
  const { decide } = useObjectionDecisionActions()
  const [action, setAction] = useState<'apply' | 'adjust' | 'dismiss'>('apply')
  const [score, setScore] = useState('')
  const [reason, setReason] = useState('')

  const num2 = Number(score)
  const scoreBad = action === 'adjust' && (score.trim() === '' || Number.isNaN(num2))
  const ok = !uploading && !selfRecused && !scoreBad && reason.trim().length >= 6 && !decide.isPending
  const closed = item.status !== 'submitted'

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <BackLink label="回到仲裁台" onClick={onBack} />

      <PageHead
        en={`OBJECTION · #${item.id}`}
        title={item.itemName}
        desc={`${OBJECTION_KIND_LABEL[item.kind]} · ${item.categoryName} · 由 ${item.proposer ?? '综测小组'} 于 ${f.dateTime(item.submittedAt)} 提交`}
        side={<Pill tone={OBJECTION_TONE[item.status]}>{OBJECTION_STATUS_LABEL[item.status]}</Pill>}
      />

      <ArbitrationSubject
        label="这一笔提案落在谁头上"
        name={item.student}
        sid={item.studentId}
        meta={`${OBJECTION_KIND_LABEL[item.kind]} · ${item.categoryName} / ${item.itemName}${item.quantity == null ? '' : ` · ${item.quantity} 次`}`}
      />

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '10px 0 30px' }}>
            <Sub title="提案内容" />
            {/* 一眼看清这一笔要把分改成多少。原来当前分和建议分是两行等宽的灰字，
                得对着读才知道差多少——而这正是要签字的那个数。 */}
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, flexWrap: 'wrap', border: '1px solid var(--line)', background: 'var(--sub)', padding: '14px 16px', marginBottom: 16 }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>当前</span>
              <span style={{ fontSize: 22, fontWeight: 600, letterSpacing: '-.04em', color: 'var(--fg3)', ...num }}>{f.score(item.currentScore)}</span>
              <span style={{ fontSize: 13, color: 'var(--fg3)' }}>→</span>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>小组建议</span>
              <span style={{ fontSize: 30, fontWeight: 600, letterSpacing: '-.045em', color: 'var(--red)', ...num }}>{f.score(item.proposedScore)}</span>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>分</span>
            </div>
            <Row label="对象" value={`${item.categoryName} / ${item.itemName}`} />
            {item.quantity != null && <Row label="次数" value={`${item.quantity} 次`} />}
            <Row label="提出人" value={item.proposer ?? '—'} />
            <div style={{ paddingTop: 20 }}><ObjectionMaterial item={item} /></div>
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '10px 0 30px' }}>
            {closed ? (
              <>
                <Sub title="已裁定" />
                <Row label="裁定分" value={f.score(item.decidedScore)} />
                <Row label="裁定时间" value={f.dateTime(item.decidedAt)} />
                <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.8, marginTop: 12 }}><RichText value={item.decisionReason ?? ''} evidence={item.noteEvidence} /></div>
              </>
            ) : (
              <>
                <Sub title="终裁" note={selfRecused ? '这是你自己的条目，你要回避' : '你确认了才生效。之后学生可以申诉'} />
                {selfRecused && (
                  <div style={{ marginBottom: 16 }}>
                    <Note tone="warn">{item.recusalReason || DEPUTY_RECUSAL_NOTE}</Note>
                  </div>
                )}
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 9, paddingBottom: 18 }}>
                  <ChoiceChip on={action === 'apply'} tone="ok" onClick={() => setAction('apply')}>
                    照准 · {f.score(item.proposedScore)} 分
                  </ChoiceChip>
                  <ChoiceChip on={action === 'adjust'} tone="warn" onClick={() => setAction('adjust')}>
                    改分后生效
                  </ChoiceChip>
                  <ChoiceChip on={action === 'dismiss'} tone="bad" onClick={() => setAction('dismiss')}>
                    驳回
                  </ChoiceChip>
                </div>

                {action === 'adjust' && (
                  <label style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingBottom: 16 }}>
                    <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>你认定的分值</span>
                    <input
                      value={score}
                      onChange={(e) => setScore(e.target.value)}
                      inputMode="decimal"
                      placeholder={`小组建议 ${f.score(item.proposedScore)}`}
                      style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--warn)', padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
                    />
                  </label>
                )}

                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>裁定理由 · 至少 6 字</span>
                  <MarkdownEditor
                    onUploadingChange={setUploading}
                    evidence={item.noteEvidence}
                    uploadTarget={{ kind: 'note', owner: 'objection', id: item.id }}
                    value={reason}
                    onChange={setReason}
                    minHeight={92}
                    placeholder={action === 'dismiss' ? '写清为什么不成立，这段话会原封不动发给提出人' : '照准也要留一句——以后学生要是申诉，这就是你当时的判断依据'}
                  />
                </div>

                <div style={{ display: 'flex', gap: 10, marginTop: 16, flexWrap: 'wrap' }}>
                  <Btn
                    primary
                    tone={action === 'dismiss' ? 'bad' : action === 'adjust' ? 'warn' : 'ok'}
                    disabled={!ok}
                    onClick={() =>
                      decide.mutate(
                        { id: item.id, action, score: action === 'adjust' ? num2 : undefined, reason: reason.trim() },
                        {
                          onSuccess: (r) => {
                            say(action === 'dismiss' ? '已驳回 · 提出人会看到这条理由' : `已生效 · 认定 ${f.score(r.score)} 分，已写入学生的计分`)
                            onBack()
                          },
                          onError: (e) => say(e instanceof ApiError ? e.message : '裁定失败'),
                        },
                      )
                    }
                  >
                    {decide.isPending ? '提交中…' : action === 'dismiss' ? '驳回这条提案' : '确认并生效'}
                  </Btn>
                </div>
              </>
            )}
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}

/* ---- 学生举报 ---- */

/* 只有两名复核人谈不拢的才到这里。举报人是谁，这一页也拿不到——后端不下发，
   审计里也没有。班管能看到的是两位复核人的姓名和他们各自的判断，
   以及举报人写的那段说明。 */
function ReportPanel({ rows }: { rows: AdminReport[] }) {
  const [uploading, setUploading] = useState(false)
  const say = useApp((s) => s.say)
  const me = useApp((s) => s.user)
  const finalize = useReportFinalActions()
  const [openId, setOpenId] = useState<string | null>(null)
  const [score, setScore] = useState('')
  const [reason, setReason] = useState('')

  const open = rows.find((row) => row.id === openId) ?? null
  const pending = rows.filter((row) => row.status === 'escalated')

  if (rows.length === 0) {
    return <Empty title="没有学生举报" desc="两名审核人先分头复核，谈不拢才到你这里。" />
  }

  if (open) {
    const scoreNum = Number(score)
    /* 扣分项的上界是 0（再扣多少），另外两类的上界是当前分（改成多少）。 */
    const ceiling = open.kind === 'penalty' ? 0 : (open.currentScore ?? 0)
    const scoreBad = score.trim() === '' || Number.isNaN(scoreNum) || scoreNum > ceiling
    const recused = isAdjudicationRecused(open, open.studentId, me?.sid)
    const ok = !uploading && !recused && !scoreBad && [...reason.trim()].length >= 6 && !finalize.isPending
    return (
      <div style={{ animation: 'rise .28s ease both' }}>
        <BackLink label="回到举报列表" onClick={() => { setOpenId(null); setScore(''); setReason('') }} />
        <ArbitrationSubject
          label="被举报的学生"
          name={open.student}
          sid={open.studentId}
          meta={`${REPORT_KIND_LABEL[open.kind]} · ${open.categoryName} / ${open.itemName}${open.quantity == null ? '' : ` · ${open.quantity} 次`} · 举报主张 ${f.score(open.proposedScore)} 分${open.currentScore == null ? '' : `（现为 ${f.score(open.currentScore)}）`}`}
        />
        <Split cols="1fr 1fr">
          <SplitCol first>
            <div style={{ padding: '4px 0 30px' }}>
              <ReportMaterial key={open.id} report={open} />
              <div style={{ paddingTop: 22 }}>
                <Sub title="两名复核人的判断" note="他们不一致，所以升到了你这里" />
                <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                  {open.reviews.map((review, at) => (
                    <div key={at} style={{ border: '1px solid var(--line)', padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 9, minWidth: 0 }}>
                      <div style={{ display: 'flex', alignItems: 'baseline', gap: 9, flexWrap: 'wrap' }}>
                        <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg)' }}>{review.reviewer}</span>
                        <span style={mono('11px', '.02em')}>{review.reviewerSid}</span>
                      </div>
                      {review.decided ? (
                        <>
                          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
                            <Pill tone={review.score === 0 ? 'idle' : 'bad'}>{REPORT_DECISION_LABEL[review.decision!]}</Pill>
                            <span style={{ fontSize: 22, fontWeight: 600, letterSpacing: '-.04em', ...num }}>{f.score(review.score)}</span>
                            <span style={{ marginLeft: 'auto', ...mono('11px', '0') }}>{f.dateTime(review.at)}</span>
                          </div>
                          <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75 }}><RichText value={review.reason ?? ''} evidence={[...open.evidence, ...(open.noteEvidence ?? [])]} /></div>
                        </>
                      ) : (
                        <Pill tone="warn">尚未复核</Pill>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </SplitCol>
          <SplitCol>
            <div style={{ padding: '4px 0 30px' }}>
              {open.status === 'escalated' ? (
                <>
                  <Sub title="终裁" note={open.kind === 'penalty' ? '你填的分就是最终扣分。判举报不成立就填 0' : `你填的分就是这一条的最终得分。判举报不成立就填回现在的 ${f.score(open.currentScore)}`} />
                  {recused && <div style={{ marginBottom: 16 }}><Note tone="warn">{open.recusalReason || DEPUTY_RECUSAL_NOTE}</Note></div>}
                  <label style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingBottom: 16 }}>
                    <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>
                    {open.kind === 'penalty' ? '裁定扣分 · 填负数；不成立填 0' : '裁定认定分 · 不成立就填回现在的分'}
                  </span>
                    <input
                      value={score}
                      onChange={(e) => setScore(e.target.value)}
                      inputMode="decimal"
                      placeholder={`复核人给的是 ${open.reviews.filter((r) => r.decided).map((r) => f.score(r.score)).join(' 与 ')}`}
                      style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${scoreBad && score.trim() ? 'var(--red)' : 'var(--warn)'}`, padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
                    />
                    {scoreBad && score.trim() !== '' && <span style={{ fontSize: 12, color: 'var(--red)' }}>{open.kind === 'penalty' ? '只能填负值或 0' : '请填一个不高于当前分的数字'}</span>}
                  </label>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>裁定理由 · 至少 6 字</span>
                    <MarkdownEditor onUploadingChange={setUploading} evidence={[...open.evidence, ...(open.noteEvidence ?? [])]} uploadTarget={{ kind: 'note', owner: 'report-review', id: open.id }} value={reason} onChange={setReason} minHeight={92} placeholder="写清你信了哪一边，依据的是细则里哪一条" />
                  </div>
                  <div style={{ display: 'flex', gap: 10, marginTop: 16 }}>
                    <Btn
                      primary
                      danger={scoreNum < 0}
                      disabled={!ok}
                      onClick={() =>
                        finalize.mutate({ id: open.id, score: scoreNum, reason: reason.trim() }, {
                          onSuccess: () => {
                            say(scoreNum === ceiling ? '已裁定 · 举报不成立，分数不动' : `已终裁 · 认定 ${f.score(scoreNum)} 分`)
                            setOpenId(null)
                            setScore('')
                            setReason('')
                          },
                          onError: (e) => say(e instanceof ApiError ? e.message : '终裁失败'),
                        })
                      }
                    >
                      {finalize.isPending ? '提交中…' : '定分并发出'}
                    </Btn>
                  </div>
                  <div style={{ marginTop: 18 }}>
                    <Note>扣分接着扣，基础分和已定分条目直接覆盖。学生不服还能申诉。</Note>
                  </div>
                </>
              ) : (
                <>
                  <Sub title="已处理" />
                  <Row label="状态" value={REPORT_STATUS_LABEL[open.status]} />
                  <Row label="认定分" value={open.finalScore === null ? '—' : f.score(open.finalScore)} />
                  <Row label="处理时间" value={f.dateTime(open.decidedAt)} />
                  {open.decisionReason && <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.8, marginTop: 12 }}><RichText value={open.decisionReason} evidence={[...open.evidence, ...(open.noteEvidence ?? [])]} /></div>}
                </>
              )}
            </div>
          </SplitCol>
        </Split>
      </div>
    )
  }

  return (
    <>
      {pending.length > 0 && (
        <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, paddingBottom: 12, textWrap: 'pretty' }}>
          有 {pending.length} 条举报两名复核人没谈拢，等你定分。举报人匿名。
        </div>
      )}
      <Table cols="124px 84px minmax(150px,1.4fr) 92px 92px 104px 76px">
        <THead cells={['被举报人', '类型', '举报说明', '主张分', '认定分', '状态', '']} />
        {rows.map((row) => (
          <TRow
            key={row.id}
            label={`打开 ${row.student} 的举报`}
            onClick={() => setOpenId(row.id)}
            cells={[
              <span key="a" style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
                <span style={{ fontSize: 15, fontWeight: 600, letterSpacing: '-.02em', color: 'var(--fg)' }}>{row.student}</span>
                <span style={mono('11px', '.02em')}>{row.studentId}</span>
              </span>,
              <span key="b" style={{ fontSize: 12, color: 'var(--fg3)' }}>{REPORT_KIND_LABEL[row.kind]}</span>,
              <span key="c" style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.itemName}</span>
                <span style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.basis}</span>
              </span>,
              <span key="d" style={{ ...num, color: 'var(--red)' }}>{f.score(row.proposedScore)}</span>,
              <span key="e" style={{ ...num, fontWeight: 600 }}>{row.finalScore === null ? '—' : f.score(row.finalScore)}</span>,
              <Pill key="f" tone={row.status === 'escalated' ? 'bad' : row.status === 'reviewing' ? 'warn' : row.status === 'dismissed' ? 'idle' : 'ok'}>
                {REPORT_STATUS_LABEL[row.status]}
              </Pill>,
              <TextBtn key="g" onClick={() => setOpenId(row.id)}>{isAdjudicationRecused(row, row.studentId, me?.sid) ? '回避 · 查看' : row.status === 'escalated' ? '终裁' : '查看'}</TextBtn>,
            ]}
          />
        ))}
      </Table>
    </>
  )
}

/* ---- 小组异议列表 ---- */

function ObjectionPanel({ rows, onOpen }: { rows: Objection[]; onOpen: (o: Objection) => void }) {
  const say = useApp((s) => s.say)
  const me = useApp((s) => s.user)
  const { decideBatch } = useObjectionDecisionActions()
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [reason, setReason] = useState('')

  const waiting = rows.filter((r) => r.status === 'submitted' && !isAdjudicationRecused(r, r.studentId, me?.sid))
  const picked = waiting.filter((row) => checked.has(row.id))
  const toggle = (id: string) =>
    setChecked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const run = (action: 'apply' | 'dismiss') =>
    decideBatch.mutate(
      { ids: picked.map((row) => row.id), action, reason: reason.trim() },
      {
        onSuccess: (r) => {
          setChecked(new Set())
          setReason('')
          say(`已${action === 'apply' ? '照准' : '驳回'} ${r.decided} 条`)
        },
        onError: (e) => say(e instanceof ApiError ? e.message : '批量裁定失败'),
      },
    )

  if (rows.length === 0) {
    return <Empty title="没有小组异议" desc="综测小组在「扣分与异议」里提交的提案会出现在这里。你确认了，才会真的加到学生分上。" />
  }

  return (
    <>
      {waiting.length > 0 && (
        <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '14px 18px', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>
            已选 <span style={{ fontWeight: 600, ...num }}>{picked.length}</span> / {waiting.length} 条待终裁
          </span>
          <input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="批量裁定理由 · 至少 6 字"
            style={{ flex: 1, minWidth: 200, background: 'var(--bg)', border: '1px solid var(--line)', padding: '9px 12px', color: 'var(--fg)', font: 'inherit', fontSize: 13 }}
          />
          <Btn onClick={() => setChecked(new Set(waiting.map((r) => r.id)))}>全选待终裁</Btn>
          <Btn primary tone="ok" disabled={picked.length === 0 || reason.trim().length < 6 || decideBatch.isPending} onClick={() => run('apply')}>
            {decideBatch.isPending ? '处理中…' : '照准所选'}
          </Btn>
          <Btn danger disabled={picked.length === 0 || reason.trim().length < 6 || decideBatch.isPending} onClick={() => run('dismiss')}>
            驳回所选
          </Btn>
        </div>
      )}

      {/* 整行可点：原来只有行尾的「终裁」能进详情，标题看着像链接却点不动。
          勾选框自己吞掉点击，不连带打开详情。 */}
      <Table cols="36px 124px 84px minmax(150px,1.4fr) 88px 88px 96px 76px">
        <THead cells={['', '学生', '类型', '对象与依据', '当前分', '建议分', '状态', '']} />
        {rows.map((o) => (
          <TRow
            key={o.id}
            label={`打开 ${o.student} 的提案 ${o.itemName}`}
            onClick={() => onOpen(o)}
            cells={[
              <input
                key="chk"
                type="checkbox"
                checked={checked.has(o.id)}
                disabled={o.status !== 'submitted' || isAdjudicationRecused(o, o.studentId, me?.sid)}
                onChange={() => toggle(o.id)}
                onClick={(event) => event.stopPropagation()}
                aria-label={`选中 ${o.student} 的 ${o.itemName}`}
                style={{ width: 15, height: 15, accentColor: 'var(--red)', cursor: o.status === 'submitted' ? 'pointer' : 'not-allowed' }}
              />,
              /* 姓名与学号成组放大：这一栏回答的是"这一笔扣到谁头上"，
                 和详情页那张 ReviewSubject 是同一件事，不该比条目名还小。 */
              <span key="a" style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
                <span style={{ fontSize: 15, fontWeight: 600, letterSpacing: '-.02em', color: 'var(--fg)' }}>{o.student}</span>
                <span style={mono('11px', '.02em')}>{o.studentId}</span>
              </span>,
              <span key="b" style={{ fontSize: 12, color: 'var(--fg3)' }}>{OBJECTION_KIND_LABEL[o.kind]}</span>,
              <span key="c" style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{o.itemName}</span>
                <span style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {o.proposer ? `${o.proposer} · ` : ''}{o.basis}
                </span>
              </span>,
              <span key="d" style={num}>{f.score(o.currentScore)}</span>,
              <span key="e" style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
                <span style={{ ...num, fontWeight: 600 }}>{f.score(o.proposedScore)}</span>
                {/* 有附件就在列表上标出来：班管得先知道这一条值不值得点开看证据。 */}
                {!!o.noteEvidence?.length && (
                  <span title={`${o.noteEvidence.length} 份佐证`} style={{ display: 'flex', alignItems: 'center', gap: 2, fontSize: 11, color: 'var(--fg3)' }}>
                    <Paperclip size={11} strokeWidth={1.8} />{o.noteEvidence.length}
                  </span>
                )}
              </span>,
              <Pill key="f" tone={OBJECTION_TONE[o.status]}>{OBJECTION_STATUS_LABEL[o.status]}</Pill>,
              <TextBtn key="g" onClick={() => onOpen(o)}>{isAdjudicationRecused(o, o.studentId, me?.sid) ? '回避 · 查看' : o.status === 'submitted' ? '终裁' : '查看'}</TextBtn>,
            ]}
          />
        ))}
      </Table>
    </>
  )
}





function AppealPanel({ rows, mySid, onOpen }: { rows: Appeal[]; mySid?: string; onOpen: (id: string) => void }) {
  const say = useApp((s) => s.say)
  const finalize = useAppealFinalActions()
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [reason, setReason] = useState('')
  const [running, setRunning] = useState(0)

  /* 只有升到管理员手上、且不是自己的那几条能批量签。 */
  const actionable = rows.filter((row) => row.status === 'escalated' && !isAdjudicationRecused(row, row.studentId, mySid))
  const picked = actionable.filter((row) => checked.has(row.id))

  const toggle = (id: string) =>
    setChecked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const upholdAll = async () => {
    let done = 0
    for (const row of picked) {
      setRunning(done + 1)
      try {
        await finalize.mutateAsync({
          id: row.id,
          decision: 'uphold',
          category: row.targetType === 'submission' ? row.originalCategory ?? row.category : undefined,
          itemKey: row.targetType === 'submission' ? row.originalItemKey ?? row.itemKey : undefined,
          score: row.baselineScore ?? row.currentScore ?? 0,
          reason: reason.trim(),
        })
        done += 1
      } catch (error) {
        setRunning(0)
        say(`${error instanceof ApiError ? error.message : '终裁失败'} · 已签 ${done} 条，停在「${row.student} · ${row.target}」`)
        setChecked(new Set(picked.slice(done).map((r) => r.id)))
        return
      }
    }
    setRunning(0)
    setChecked(new Set())
    setReason('')
    say(`已维持原判 ${done} 条 · 学生各自还剩一次申诉机会`)
  }

  return (
    <>
      {actionable.length > 0 && (
        <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '14px 18px', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>
            已选 <span style={{ fontWeight: 600, ...num }}>{picked.length}</span> / {actionable.length} 条待终裁
          </span>
          <input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="统一的终裁理由"
            style={{ flex: 1, minWidth: 240, background: 'var(--bg)', border: '1px solid var(--line)', padding: '9px 12px', color: 'var(--fg)', font: 'inherit', fontSize: 13 }}
          />
          <Btn onClick={() => setChecked(new Set(actionable.map((r) => r.id)))}>全选待终裁</Btn>
          <Btn primary tone="ok" disabled={picked.length === 0 || reason.trim().length < 6 || running > 0} onClick={() => void upholdAll()}>
            {running > 0 ? `处理中 ${running}/${picked.length}…` : '批量维持原判'}
          </Btn>
        </div>
      )}
      {actionable.length > 0 && (
        <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, paddingBottom: 12, textWrap: 'pretty' }}>
          批量只能「维持原判」。想改分或者改归类，得点开那一条单独裁——每条的分不一样，理由也该不一样。
        </div>
      )}

      <div data-r="scroll">
        <div style={{ minWidth: 760 }}>
          {rows.map((a) => {
            const mine = isAdjudicationRecused(a, a.studentId, mySid)
            const selectable = a.status === 'escalated' && !mine
            return (
              <DeskLine
                key={a.id}
                student={a.student}
                title={a.target}
                cat={appealLine(a)}
                score={f.score(a.baselineScore ?? a.currentScore)}
                state={mine ? '本人回避' : APPEAL_STATUS_LABEL[a.status]}
                stateColor={mine ? 'var(--warn)' : a.status === 'escalated' ? 'var(--red)' : 'var(--warn)'}
                action={a.status === 'escalated' ? '处理' : '查看'}
                onHandle={() => onOpen(a.id)}
                checked={checked.has(a.id)}
                onCheck={() => selectable && toggle(a.id)}
                checkDisabled={!selectable}
                checkLabel={`选中 ${a.student} 的 ${a.target}`}
              />
            )
          })}
        </div>
      </div>
    </>
  )
}



const appealLine = (appeal: Appeal) => appealRoundLabel(appeal.round)

/* ---- 台面 ---- */

export default function AdminArbitration() {
  const scope = useAdjudicationScope()
  const [tab, setTab] = useState<Tab>('arbitrating')
  const [openSub, setOpenSub] = useState<AdminSubmission | null>(null)
  const openedSubmission = useAdminSubmissions({ id: openSub?.id, page_size: 1 }, !!openSub)
  const [rejectId, setRejectId] = useState<string | null>(null)
  const [forceScore, setForceScore] = useState(false)
  const [openClassificationId, setOpenClassificationId] = useState<string | null>(null)
  const [openAppeal, setOpenAppeal] = useState<string | null>(null)
  const [openObjection, setOpenObjection] = useState<Objection | null>(null)
	const [student, setStudent] = useState('')
	const [category, setCategory] = useState('')
	const [query, setQuery] = useState('')
	const [page, setPage] = useState(1)


  const scheme = useScheme()
	const me = useApp((state) => state.user)
  const restore = useApp((state) => state.restore)
  const agentFocus = useApp((state) => state.agentFocus)
  const focusedSubmission = useAdminSubmissions({ id: agentFocus?.resourceId, page_size: 1 }, !!agentFocus && agentFocus.resourceKind === 'submission' && agentFocus.view === (scope === 'deputy' ? 'revDeputy' : 'admSubs'))
  useEffect(() => {
    if (!agentFocus || agentFocus.view !== (scope === 'deputy' ? 'revDeputy' : 'admSubs')) return
    if (agentFocus.resourceKind === 'submission' && focusedSubmission.isLoading) return
    const target = focusedSubmission.data?.items.find((row) => row.id === agentFocus.resourceId)
    if (agentFocus.resourceKind === 'submission' && !target) {
      useApp.getState().say('原事项已变化或不可读取，请刷新仲裁台')
    } else {
      setOpenSub(agentFocus.resourceKind === 'submission' ? target ?? null : null); setOpenAppeal(agentFocus.resourceKind === 'appeal' ? agentFocus.resourceId ?? null : null)
      setOpenClassificationId(null); setOpenObjection(null)
    }
    useApp.getState().setAgentFocus(null)
  }, [agentFocus, scope, focusedSubmission.data, focusedSubmission.isLoading])
	const common = { student: student || undefined, category: category || undefined, q: query || undefined, page, page_size: 50 }
	const subs = useAdminSubmissions({ ...common, status: 'arbitrating' })
  const rejectable = useAdminSubmissions({ ...common, forceRejectable: true }, scope === 'deputy' && tab === 'forceReject')
  const classificationSubmission = useAdminSubmissions({ id: openClassificationId ?? undefined, page_size: 1 }, !!openClassificationId)
	const appeals = useAdminAppeals(common)
	const objections = useAdminObjections(common)
	const reports = useAdminReports()
  const classification = useClassificationSuggestions()

  const appealRows = appeals.data?.items ?? []
  const objectionRows = objections.data?.items ?? []
  const reportRows = reports.data?.items ?? []
  const classificationRows = (classification.data?.items ?? []).filter((item) =>
    (!student || `${item.studentSid} ${item.student}`.toLowerCase().includes(student.toLowerCase())) &&
    (!category || item.fromCategory === category || item.toCategory === category) &&
    (!query || `${item.title} ${item.reason}`.toLowerCase().includes(query.toLowerCase())),
  )
  const escalatedReports = reportRows.filter((row) => row.status === 'escalated').length
  const rows = subs.data?.items ?? []
  const conflictCount = subs.data?.total ?? rows.length
  const catName = (k: string) => scheme.data?.categories.find((c) => c.key === k)?.name ?? k
  /* 分差大的排前面。详情页的「下一条」照搬这个顺序，不然翻页和列表对不上。 */
  const conflictRows = [...rows].sort((a, b) => {
    const ga = a.reviews.length === 2 ? Math.abs(a.reviews[0].score - a.reviews[1].score) : 0
    const gb = b.reviews.length === 2 ? Math.abs(b.reviews[0].score - b.reviews[1].score) : 0
    return gb - ga
  })

  const activeQuery = tab === 'forceReject' ? rejectable : tab === 'appeal' ? appeals : tab === 'objection' ? objections : tab === 'report' ? reports : tab === 'classification' ? classification : subs
  if (scope === 'deputy' && activeQuery.error instanceof ApiError && activeQuery.error.status === 403) {
    return <div><PageHead en="DEPUTY ARBITRATION" title="副班管仲裁" /><Empty title="当前无法使用副班管仲裁" desc={activeQuery.error.message} /><Btn onClick={() => void restore()}>刷新身份</Btn></div>
  }

  if (openClassificationId) {
    const item = classificationSubmission.data?.items.find((row) => row.id === openClassificationId)
    if (classificationSubmission.isLoading || !item) return <div>
      <BackLink label="回到分类确认" onClick={() => setOpenClassificationId(null)} />
      {classificationSubmission.isLoading ? <div className="load-bar"><span /></div> : <Empty title="分类事项暂时无法打开" desc={classificationSubmission.error instanceof ApiError ? classificationSubmission.error.message : '这一条可能已经变了，也可能不归当前仲裁管。返回刷新一下再试。'} />}
    </div>
    return <ConflictDetail key={item.id} item={item} queue={[item]} classificationSuggestions={classification.data?.items.filter((suggestion) => suggestion.submissionId === item.id && suggestion.scope === 'cross_category')} onOpen={(row) => setOpenClassificationId(row.id)} onBack={() => setOpenClassificationId(null)} />
  }

  if (openSub) {
    const current = openedSubmission.data?.items.find((row) => row.id === openSub.id)
    if (openedSubmission.isError || (openedSubmission.data && !current)) return <div><BackLink label="回到仲裁台" onClick={() => setOpenSub(null)} /><Empty title="原事项已变化或不可读取" desc="请返回仲裁台刷新后重试。" /></div>
    return <ConflictDetail key={openSub.id} item={current ?? openSub} queue={conflictRows} classificationSuggestions={classification.data?.items.filter((suggestion) => suggestion.submissionId === openSub.id && suggestion.scope === 'cross_category')} onOpen={setOpenSub} onBack={() => setOpenSub(null)} />
  }
  if (openAppeal) return <AppealDetailView key={openAppeal} id={openAppeal} queue={appealRows} onOpen={setOpenAppeal} onBack={() => setOpenAppeal(null)} />
  if (openObjection) return <ObjectionDetail item={openObjection} onBack={() => setOpenObjection(null)} />

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="04 — YOUR DESK"
        title={scope === 'deputy' ? '副班管仲裁' : '仲裁与申诉'}
        side={scope === 'admin' ? <Btn onClick={() => useApp.getState().go('admHist')}>处理历史</Btn> : undefined}
      />

      <LineTabs
        items={[
          { key: 'arbitrating', label: '冲突待裁', count: subs.data?.total ?? conflictCount },
          { key: 'appeal', label: '申诉', count: appeals.data?.total ?? appealRows.length },
          { key: 'objection', label: '小组提案', count: objections.data?.total ?? objectionRows.length },
          { key: 'report', label: '学生举报', count: escalatedReports },
          { key: 'classification', label: '分类确认', count: classificationRows.length },
          ...(scope === 'deputy' ? [{ key: 'forceReject' as const, label: '小项强制处理' }] : []),
        ]}
        value={tab}
        onChange={(value) => { setTab(value); setPage(1); setRejectId(null) }}
      />
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', padding: '16px 0 6px' }}>
        <input value={student} onChange={(e) => { setStudent(e.target.value); setPage(1) }} placeholder="学生姓名 / 学号" style={{ ...fieldStyle, width: 180 }} />
        <select value={category} onChange={(e) => { setCategory(e.target.value); setPage(1) }} style={{ ...fieldStyle, width: 180 }}>
          <option value="">全部大项</option>
          {(scheme.data?.categories ?? []).map((item) => <option key={item.key} value={item.key}>{item.name}</option>)}
        </select>
        <input value={query} onChange={(e) => { setQuery(e.target.value); setPage(1) }} placeholder="条目 / 申诉理由关键词" style={{ ...fieldStyle, width: 240 }} />

      </div>

      {activeQuery.isError ? (
        <Empty title="仲裁事项读取失败" desc={activeQuery.error instanceof ApiError ? activeQuery.error.message : '请稍后重试。'} />
      ) : activeQuery.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : tab === 'forceReject' ? (
        <div style={{ paddingTop: 18 }}>
          <Note>只显示你自己的材料。误判的可以强制驳回记 0 分，理由会给到本人。</Note>
          {(rejectable.data?.items ?? []).filter((row) => row.status !== 'draft').map((row) => <div key={row.id} style={{ borderBottom: '1px solid var(--line)', padding: '16px 0' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
              <div style={{ flex: 1, minWidth: 180, fontSize: 13, lineHeight: 1.7 }}><strong>{row.student} · {row.title}</strong><div style={{ fontSize: 12, color: 'var(--fg3)' }}>{row.studentId} · {catName(row.category)} · {STATUS_LABEL[row.status]}</div></div>
              <span style={{ fontSize: 13, ...num }}>{f.score(row.finalScore)} 分</span>
              <Btn primary danger disabled={!row.canForceReject || !!row.forceRejection} title={row.forceRejectBlockedReason} onClick={() => { setForceScore(false); setRejectId(row.id) }}>{row.forceRejection ? '已强制驳回' : '强制驳回'}</Btn>
              <Btn disabled={!row.canForceScore} title={row.forceScoreBlockedReason} onClick={() => { setForceScore(true); setRejectId(row.id) }}>强制改分</Btn>
            </div>
            {!row.canForceReject && !row.forceRejection && row.forceRejectBlockedReason && <div style={{ fontSize: 12, color: 'var(--fg3)', marginTop: 8 }}>{row.forceRejectBlockedReason}</div>}
            {row.forcedScore && !row.forceRejection && <div style={{ marginTop: 12 }}><ForcedRejectionNotice rejection={row.forcedScore} /></div>}
            {row.forceRejection && <div style={{ marginTop: 12 }}><ForcedRejectionNotice rejection={row.forceRejection} /></div>}
            {rejectId === row.id && row.canForceReject && !row.forceRejection && <ForceRejectSubmissionForm key={`${row.id}:${forceScore}`} target={row} forceScore={forceScore} onClose={() => setRejectId(null)} />}
          </div>)}
          {!rejectable.data?.items.some((row) => row.status !== 'draft') && <Empty title="没有已提交的小项" desc="班级管理员本人提交材料后，可在这里处理误判。" />}
          <div style={{ display: 'flex', gap: 10, alignItems: 'center', paddingTop: 16 }}><Btn disabled={page <= 1} onClick={() => setPage((value) => value - 1)}>上一页</Btn><span style={mono('11px', '0')}>第 {page} 页 · 共 {rejectable.data?.total ?? 0} 条</span><Btn disabled={page * 50 >= (rejectable.data?.total ?? 0)} onClick={() => setPage((value) => value + 1)}>下一页</Btn></div>
        </div>
      ) : tab === 'classification' ? (
        <ClassificationConfirmation suggestions={classificationRows} onOpenArbitration={setOpenClassificationId} showEmpty />
      ) : tab === 'report' ? (
        <ReportPanel rows={reportRows} />
      ) : tab === 'objection' ? (
        <ObjectionPanel rows={objectionRows} onOpen={setOpenObjection} />
      ) : tab === 'appeal' ? (
        appealRows.length === 0 ? (
          <Empty title="没有申诉" desc="第一轮由原来那两个人复评，谈不拢才到你这里。" />
        ) : <AppealPanel rows={appealRows} mySid={me?.sid} onOpen={setOpenAppeal} />
      ) : subs.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty title="没有待仲裁的冲突" desc="闸门的「无待终裁冲突」这一条已经满足。" />
      ) : <div data-r="scroll"><div style={{ minWidth: 720 }}>{conflictRows.map((r) => {
        const scores = r.reviews.map((x) => x.score)
        const gap = scores.length === 2 ? Math.abs(scores[0] - scores[1]) : null
        const mine = isAdjudicationRecused(r, r.studentId, me?.sid)
        return (
          <DeskLine
            key={r.id}
            student={r.student}
            title={r.title}
            cat={catName(r.category)}
            score={gap === null ? '—' : f.score(gap)}
            state={mine ? '本人回避' : '冲突待裁'}
            stateColor={mine ? 'var(--warn)' : 'var(--red)'}
            action="处理"
            onHandle={() => setOpenSub(r)}
          />
        )
      })}</div></div>}
      {tab !== 'forceReject' && tab !== 'report' && tab !== 'classification' && (() => { const total = tab === 'appeal' ? appeals.data?.total ?? 0 : tab === 'objection' ? objections.data?.total ?? 0 : subs.data?.total ?? 0; return <div style={{ display: 'flex', gap: 10, alignItems: 'center', paddingTop: 16 }}><Btn disabled={page <= 1} onClick={() => setPage((value) => value - 1)}>上一页</Btn><span style={mono('11px', '0')}>第 {page} / {Math.max(1, Math.ceil(total / 50))} 页 · 共 {total} 条</span><Btn disabled={page * 50 >= total} onClick={() => setPage((value) => value + 1)}>下一页</Btn></div> })()}
    </div>
  )
}

/* 一行一条。原来只有行尾那个「处理」是可点的——标题看着像链接却点不动，
   谁都会先去点标题。现在整行都能点，勾选框自己吞掉点击，不连带打开详情。 */
function DeskLine({ student, title, cat, score, state, stateColor, action, onHandle, checked, onCheck, checkDisabled, checkLabel }: {
  student: string
  title: string
  cat: string
  score: string
  state: string
  stateColor: string
  action: string
  onHandle: () => void
  checked?: boolean
  /** 不传就不显示勾选框（冲突待裁没有批量口子）。 */
  onCheck?: () => void
  /** 这一行不能批量处理，但格子要占着——否则同一张表的列会左右错开。 */
  checkDisabled?: boolean
  checkLabel?: string
}) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-label={`打开 ${student} 的 ${title}`}
      className="hv-sub"
      onClick={onHandle}
      onKeyDown={(event) => {
        if (event.key !== 'Enter' && event.key !== ' ') return
        event.preventDefault()
        onHandle()
      }}
      style={{ display: 'grid', gridTemplateColumns: `${onCheck ? '32px ' : ''}92px minmax(180px,1.6fr) 112px 80px 108px 52px`, gap: 16, alignItems: 'center', padding: '16px 6px', borderBottom: '1px solid var(--line2)', cursor: 'pointer' }}
    >
      {onCheck && (
        <input
          type="checkbox"
          checked={!!checked}
          disabled={checkDisabled}
          onChange={onCheck}
          onClick={(event) => event.stopPropagation()}
          aria-label={checkLabel}
          style={{ width: 15, height: 15, accentColor: 'var(--red)', cursor: checkDisabled ? 'not-allowed' : 'pointer', opacity: checkDisabled ? 0.35 : 1 }}
        />
      )}
      <div style={{ fontSize: 13, fontWeight: 500 }}>{student}</div>
      <div title={title} style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{title}</div>
      <div style={{ fontSize: 12, color: 'var(--fg3)' }}>{cat}</div>
      <div style={{ font: "500 13px/1 'JetBrains Mono',monospace", color: 'var(--red)' }}>{score}</div>
      <div style={{ fontSize: 12, color: stateColor, display: 'flex', alignItems: 'center', gap: 7 }}>
        <i style={{ width: 6, height: 6, display: 'block', flex: 'none', background: stateColor }} />{state}
      </div>
      <div style={{ textAlign: 'right' }}>
        <TextBtn onClick={onHandle}>{action}</TextBtn>
      </div>
    </div>
  )
}
