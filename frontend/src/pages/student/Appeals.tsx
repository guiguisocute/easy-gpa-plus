/* 学生 · 我的申诉。

   从「我的提交」里拆出来的，理由不是那一页太长，而是申诉本来就是跨对象的：
   学生能申诉三类目标——提交条目、基础项、扣分项，后两类根本不在「我的提交」里
   （它们在「综测总览」和「当前成绩」）。拆之前，学生要看全自己的申诉得在三页之间找。
   这一页是「我所有申诉的唯一去处」。

   三条不能弄错的：
   1. 复评理由对学生开放，人名不开放（§5.1 单盲）——一律显示「复评 1 / 复评 2」；
   2. 申诉的终点是班级管理员下结论，不是「用满两次」：复评一致就结案、学生仍可再申诉一次；
      一旦升到班管并由他签字，这一笔就到此为止——哪怕学生只申诉过一次；
   3. 能不能再申诉由后端的 canAppeal 说了算，这里不自己推——推错就是给学生一个点不动的按钮。

   发起动作已经拆到独立的「发起申诉」页；这一页只负责历史、进度、结论和进入详情。 */

import { useMemo, useState } from 'react'
import { AppealBrief, StepBar } from '@/components/AppealBrief'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { RichText } from '@/components/Markdown'
import { RuleCard } from '@/components/RuleCard'
import { StudentNote } from '@/components/StudentNote'
import { BackLink, Btn, Empty, Note, PageHead, Pill, Row, Seg, Stat, StatGrid, Sub, Timeline } from '@/components/ui'
import { useAppeal, useAppealActions, useAppeals, useBaseItems, useMyScorecard, useScheme, useSubmission, useSubmissions, useWindow } from '@/api/queries'
import { ApiError } from '@/api/client'
import { APPEAL_STATUS_LABEL, REREVIEW_LABEL, type Appeal, type AppealStatus, type AppealTargetType, type Evidence } from '@/api/types'
import * as f from '@/lib/format'
import { collectAppealTargets, type AppealTarget } from '@/lib/appealTargets'
import { schemePathName } from '@/lib/schemeTree'
import { findCurrentItem } from '@/lib/ruleDiff'
import { mono, num } from '@/lib/style'
import { readableTrail } from '@/lib/trail'
import { useApp } from '@/stores/app'

const TONE: Record<AppealStatus, 'ok' | 'warn' | 'bad' | 'idle'> = {
  filed: 'idle',
  reviewing: 'warn',
  escalated: 'bad',
  resolved: 'ok',
  final: 'ok',
}

const TARGET_LABEL: Record<AppealTargetType, string> = {
  submission: '提交条目',
  base_score: '基础项',
  penalty_score: '扣分项',
}

const OPEN: AppealStatus[] = ['filed', 'reviewing', 'escalated']
const isOpen = (a: Appeal) => OPEN.includes(a.status)

function RereviewList({
  handlers,
  path,
  quotable,
}: {
  handlers: Appeal['handlers']
  path: (category: string, itemKey: string) => string
  quotable: Evidence[]
}) {
  const rows = handlers.filter((h) => h.rereview)
  if (rows.length === 0) return null
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {rows.map((h, i) => (
        <div key={h.id} style={{ border: '1px solid var(--line)', padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 9 }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>复评 {i + 1}</span>
            <Pill tone={h.rereview!.decision === 'uphold' ? 'idle' : 'warn'}>{REREVIEW_LABEL[h.rereview!.decision]}</Pill>
            <span style={{ fontSize: 16, fontWeight: 600, ...num }}>{f.score(h.rereview!.score)}</span>
          </div>
          {h.rereview!.category && h.rereview!.itemKey && (
            <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{path(h.rereview!.category, h.rereview!.itemKey)}</span>
          )}
          <RichText value={h.rereview!.reason} evidence={quotable} />
        </div>
      ))}
    </div>
  )
}

const STUDENT_TRAIL: Record<string, string> = {
  'appeal.filed': '提交申诉',
  'appeal.evidence_completed': '补传佐证',
  'appeal.assigned': '交回原审核人',
  'appeal.rereviewed': '复评提交',
  'appeal.resolved': '复评结案',
  'appeal.escalated': '升至班级管理员',
  'appeal.escalated_round2': '交班级管理员终裁',
  'appeal.final': '终裁',
  'appeal.withdrawn': '撤回',
}

/* 排序按「要我关注的在前」，不按时间：进行中 → 待受理 → 已结案，组内最近更新在前。
   学生打开这一页是来问"我那条走到哪了"，不是来翻流水账。 */
const WEIGHT: Record<AppealStatus, number> = { escalated: 0, reviewing: 1, filed: 2, resolved: 3, final: 4 }
const byAttention = (a: Appeal, b: Appeal) => WEIGHT[a.status] - WEIGHT[b.status] || b.updatedAt.localeCompare(a.updatedAt)

/* ---- 详情 ---- */

function AppealDetail({ id, queue, candidates, onOpen, onBack, onAppealAgain }: {
  id: string
  queue: Appeal[]
  /** 还能申诉的对象。结案后要不要给「再申诉一次」，以它为准，不自己推。 */
  candidates: AppealTarget[]
  onOpen: (id: string) => void
  onBack: () => void
  onAppealAgain: (targetType: AppealTargetType, targetId: string) => void
}) {
  const scheme = useScheme()
  const say = useApp((s) => s.say)
  const { withdraw, deleteDraft } = useAppealActions()
  const detail = useAppeal(id)
  const at = queue.findIndex((row) => row.id === id)
  const [showFiled, setShowFiled] = useState(false)

  const d = detail.data
  /* 提交条目的原始材料按需拉：折叠着的时候不该为它多打一次请求。 */
  const source = useSubmission(showFiled && d?.targetType === 'submission' ? d.targetId : null)

  if (detail.isLoading) return <div className="load-bar"><span /></div>
  if (!d) return <Empty title="找不到这条申诉" desc="可能已撤回。" />

  const quotable = [...(d.evidence ?? []), ...(d.originalEvidence ?? []), ...(d.noteEvidence ?? [])]
  const path = (category: string, itemKey: string) => schemePathName(scheme.data, category, itemKey)
  const rereviews = d.handlers.filter((h) => h.rereview)
  const stillOpen = isOpen(d)
  const againstThis = candidates.find((row) => row.key === `${d.targetType}:${d.targetId}`)
  /* 还没有人下结论：已提交但复评人一个都没交。一旦有人交了或升到班管，就不能再改这份材料。 */
  const canRework = !d.collective && stillOpen && d.status !== 'escalated' && d.handlers.every((h) => !h.decided)
  const reworkPending = withdraw.isPending || deleteDraft.isPending

  const trail = readableTrail(d.trail)
    .filter((step) => step.action !== 'appeal.draft_created')
    .map((step) => ({ title: STUDENT_TRAIL[step.action] ?? step.title, at: f.dateTime(step.at) }))

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <BackLink label="申诉列表" onClick={onBack} />

      <PageHead
        en={`APPEAL · ROUND ${d.round}`}
        title={d.target}
        desc={`${TARGET_LABEL[d.targetType]} · ${f.dateTime(d.createdAt)}`}
        side={<Pill tone={TONE[d.status]}>{APPEAL_STATUS_LABEL[d.status]}</Pill>}
      />

      <AppealBrief
        heading="申诉理由"
        proposedLabel="主张"
        reason={d.reason}
        evidence={quotable}
        originalPath={d.targetType === 'submission' ? path(d.originalCategory ?? d.category, d.originalItemKey ?? d.itemKey) : undefined}
        originalScore={d.baselineScore}
        proposedPath={d.targetType === 'submission' ? path(d.proposedCategory ?? d.category, d.proposedItemKey ?? d.itemKey) : undefined}
        proposedScore={d.proposedScore}
      />

      {d.previousRound && (
        <div style={{ padding: '26px 0 0', display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Sub title="上一轮复评" />
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 9 }}>
            <span style={{ fontSize: 30, fontWeight: 600, letterSpacing: '-.045em', ...num }}>{f.score(d.previousRound.resolutionScore)}</span>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>分</span>
            <span style={{ marginLeft: 'auto', ...mono('11px', '0') }}>{f.dateTime(d.previousRound.resolvedAt)}</span>
          </div>
          <RereviewList handlers={d.previousRound.handlers} path={path} quotable={quotable} />
        </div>
      )}

      {d.status === 'final' && (
        <div style={{ padding: '26px 0 0' }}>
          <Sub title={d.collective ? '共同评审结论' : '终裁结论'} />
          <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '18px 20px', display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>{d.handler || '班级管理员'}</span>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{d.collective ? '独立小组共同决定' : '班级管理员'}</span>
              <span style={{ marginLeft: 'auto', ...mono('11px', '0') }}>{f.dateTime(d.resolvedAt)}</span>
            </div>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 9 }}>
              <span style={{ fontSize: 30, fontWeight: 600, letterSpacing: '-.045em', ...num }}>{f.score(d.resolutionScore)}</span>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>分</span>
              {d.baselineScore !== null && d.resolutionScore !== null && d.resolutionScore !== d.baselineScore && (
                <span style={{ fontSize: 12.5, color: 'var(--ok)' }}>{f.delta(d.resolutionScore - d.baselineScore)}</span>
              )}
            </div>
            {d.targetType === 'submission' && d.resolutionCategory && d.resolutionItemKey
              && path(d.resolutionCategory, d.resolutionItemKey) !== path(d.originalCategory ?? d.category, d.originalItemKey ?? d.itemKey) && (
              <Row label="生效归类" value={path(d.resolutionCategory, d.resolutionItemKey)} />
            )}
            {d.resolutionReason && <RichText value={d.resolutionReason} evidence={quotable} size="lg" />}
            <Note>本条不再接受申诉。</Note>
          </div>
        </div>
      )}

      {d.status === 'resolved' && (
        <div style={{ padding: '26px 0 0', display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Sub title="复评意见" />
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 9 }}>
            <span style={{ fontSize: 30, fontWeight: 600, letterSpacing: '-.045em', ...num }}>{f.score(d.resolutionScore)}</span>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>分</span>
            {d.baselineScore !== null && d.resolutionScore !== null && d.resolutionScore !== d.baselineScore && (
              <span style={{ fontSize: 12.5, color: 'var(--ok)' }}>{f.delta(d.resolutionScore - d.baselineScore)}</span>
            )}
            <span style={{ marginLeft: 'auto', ...mono('11px', '0') }}>{f.dateTime(d.resolvedAt)}</span>
          </div>
          <RereviewList handlers={d.handlers} path={path} quotable={quotable} />
          {againstThis && (
            <div>
              <Btn primary onClick={() => onAppealAgain(againstThis.type, againstThis.id)}>
                再申诉一次
              </Btn>
            </div>
          )}
        </div>
      )}

      {stillOpen && (
        <div style={{ padding: '26px 0 0' }}>
          <Sub title="进度" />
          {d.round === 1 && d.status !== 'escalated' && d.handlers.length > 0 && (
            <div style={{ marginBottom: 16 }}>
              {d.handlers.map((h, i) => (
                <div key={h.id} style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '11px 0' }}>
                  <span style={{ width: 5, height: 5, flex: 'none', background: h.decided ? 'var(--ok)' : 'var(--warn)' }} />
                  <span style={{ fontSize: 13, color: 'var(--fg)' }}>复评 {i + 1}</span>
                  <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>{h.decided ? '已提交' : '待提交'}</span>
                </div>
              ))}
            </div>
          )}
          <Timeline
            items={[
              ...trail,
              {
                title: d.collective ? '独立共治评审中，可在班级共治查看进度' : d.status === 'escalated' || d.round === 2 ? '待终裁' : '待复评',
                at: f.dateTime(d.updatedAt),
                state: 'active' as const,
              },
            ]}
          />
          {canRework && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', marginTop: 16 }}>
              <Btn
                disabled={reworkPending}
                onClick={() =>
                  withdraw.mutate(d.id, {
                    onSuccess: () => {
                      say('已撤回，可继续修改')
                      onAppealAgain(d.targetType, d.targetId)
                    },
                    onError: (e) => say(e instanceof ApiError ? e.message : '撤回失败'),
                  })
                }
              >
                撤回并修改
              </Btn>
              <Btn
                danger
                disabled={reworkPending}
                onClick={() => {
                  if (!window.confirm(`删除对「${d.target}」的本次申诉？理由与佐证将一并删除，不可恢复。`)) return
                  withdraw.mutate(d.id, {
                    onSuccess: () => {
                      deleteDraft.mutate(d.id, {
                        onSuccess: () => {
                          say('已删除')
                          onBack()
                        },
                        onError: (e) => {
                          say(e instanceof ApiError ? `申诉已撤回，但删除失败：${e.message}` : '申诉已撤回，但草稿删除失败')
                          onAppealAgain(d.targetType, d.targetId)
                        },
                      })
                    },
                    onError: (e) => say(e instanceof ApiError ? e.message : '撤回失败，未执行删除'),
                  })
                }}
              >
                删除
              </Btn>
            </div>
          )}
        </div>
      )}

      {d.status !== 'resolved' && rereviews.length > 0 && (
        <div style={{ padding: '26px 0 0', display: 'flex', flexDirection: 'column', gap: 10 }}>
          <Sub title="复评意见" />
          <RereviewList handlers={d.handlers} path={path} quotable={quotable} />
        </div>
      )}

      {queue.length > 1 && (
        <div style={{ padding: '26px 0 0' }}>
          <StepBar
            at={at}
            total={queue.length}
            prevLabel={at > 0 ? queue[at - 1].target : ''}
            nextLabel={at >= 0 && at < queue.length - 1 ? queue[at + 1].target : ''}
            onPrev={() => at > 0 && onOpen(queue[at - 1].id)}
            onNext={() => at >= 0 && at < queue.length - 1 && onOpen(queue[at + 1].id)}
          />
        </div>
      )}

      <div style={{ borderTop: '1px solid var(--line)', marginTop: 26, paddingTop: 4 }}>
        <button
          type="button"
          className="hv-fg"
          aria-expanded={showFiled}
          onClick={() => setShowFiled((v) => !v)}
          style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', border: 0, background: 'none', padding: '16px 0 6px', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
        >
          <span style={{ width: 12, color: 'var(--fg3)' }}>{showFiled ? '▾' : '▸'}</span>
          <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em' }}>原申报材料</span>
        </button>
        {showFiled && (
          <div style={{ padding: '10px 0 24px', display: 'flex', flexDirection: 'column', gap: 18 }}>
            {d.ruleSnapshot && (
              <RuleCard item={d.ruleSnapshot.item} categoryName={d.ruleSnapshot.categoryName} capturedAt={d.ruleSnapshot.capturedAt} currentItem={findCurrentItem(scheme.data, d.ruleSnapshot.item.key)} />
            )}
            {d.targetType !== 'submission' && (
              <div>
                <Sub title="录入依据" />
                {d.basis ? <RichText value={d.basis} size="lg" /> : <Note>没有录入依据。</Note>}
              </div>
            )}
            {d.targetType === 'submission' && <StudentNote value={source.data?.submission.note} evidence={quotable} />}
            {(d.originalEvidence?.length ?? 0) > 0 && (
              <div>
                <Sub title="原有佐证" note={`${d.originalEvidence!.length} 份`} />
                <EvidenceFiles items={d.originalEvidence!} />
              </div>
            )}
            {(d.originalReviews?.length ?? 0) > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                <Sub title="首次审核结论" />
                {d.originalReviews!.map((r, i) => (
                  <div key={i} style={{ border: '1px solid var(--line)', padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 7 }}>
                    <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
                      <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>审核人 {i + 1}</span>
                      <span style={{ fontSize: 14, fontWeight: 600, ...num }}>{f.score(r.score)}</span>
                    </div>
                    <RichText value={'reason' in r ? r.reason : r.why} evidence={quotable} />
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

/* ---- 列表 ---- */

export default function StudentAppeals() {
  const go = useApp((s) => s.go)
  const goAppeal = useApp((s) => s.goAppeal)
  const scheme = useScheme()
  const appeals = useAppeals()
  const submissions = useSubmissions()
  const baseItems = useBaseItems()
  const win = useWindow()
  const scorecard = useMyScorecard({ poll: false })

  const [scope, setScope] = useState<'all' | 'open' | 'closed'>('all')
  const [openId, setOpenId] = useState<string | null>(null)

  const rows = useMemo(() => [...(appeals.data?.items ?? [])].sort(byAttention), [appeals.data])
  const shown = rows.filter((a) => (scope === 'open' ? isOpen(a) : scope === 'closed' ? !isOpen(a) : true))

  const canAppeal = !!win.data?.capabilities.appeal
  const confirmed = !!scorecard.data?.confirmation?.confirmed

  const candidates = useMemo(
    () => collectAppealTargets({ canAppeal, confirmed, submissions: submissions.data, baseItems: baseItems.data, scheme: scheme.data }),
    [canAppeal, confirmed, submissions.data, baseItems.data, scheme.data],
  )

  if (openId) {
    return (
      <AppealDetail
        id={openId}
        queue={shown}
        onOpen={setOpenId}
        onBack={() => setOpenId(null)}
        onAppealAgain={goAppeal}
        candidates={candidates}
      />
    )
  }

  const openCount = rows.filter((a) => a.status === 'reviewing' || a.status === 'escalated').length
  const filedCount = rows.filter((a) => a.status === 'filed').length
  const closedCount = rows.filter((a) => !isOpen(a)).length

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="MY APPEALS"
        title="我的申诉"
        desc="进度与结论。"
        side={
          <Btn primary disabled={!canAppeal || candidates.length === 0} onClick={() => go('stuFileAppeal')}>
            {candidates.length === 0 ? '暂无可申诉的条目' : '发起申诉'}
          </Btn>
        }
      />

      <StatGrid cols={4}>
        <Stat en="待受理" value={String(filedCount)} unit="条" note="未分派" />
        <Stat en="进行中" value={String(openCount)} unit="条" note="复评或终裁中" tone={openCount > 0 ? 'var(--warn)' : undefined} />
        <Stat en="已结案" value={String(closedCount)} unit="条" />
        <Stat en="还能申诉" value={String(candidates.length)} unit="笔" note={canAppeal ? '尚未终裁的条目' : '当前未开放'} />
      </StatGrid>

      <div style={{ padding: '22px 0 14px' }}>
        <Seg
          items={[
            { key: 'all' as const, label: `全部 ${rows.length}` },
            { key: 'open' as const, label: `进行中 ${openCount + filedCount}` },
            { key: 'closed' as const, label: `已结案 ${closedCount}` },
          ]}
          value={scope}
          onChange={setScope}
        />
      </div>

      {appeals.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : shown.length === 0 ? (
        <>
          <Empty
            title={rows.length === 0 ? '暂无申诉' : '这一组没有记录'}
            desc={rows.length === 0 ? '可从右上角发起。' : undefined}
          />
          {rows.length === 0 && candidates.length > 0 && (
            <div style={{ marginTop: 14 }}>
              <Btn primary onClick={() => go('stuFileAppeal')}>发起申诉</Btn>
            </div>
          )}
        </>
      ) : (
        <div data-r="scroll">
          <div style={{ minWidth: 720 }}>
            {shown.map((a) => {
              const decided = a.handlers.filter((h) => h.decided).length
              return (
                <div
                  key={a.id}
                  role="button"
                  tabIndex={0}
                  aria-label={`打开申诉 ${a.target}`}
                  className="hv-sub"
                  onClick={() => setOpenId(a.id)}
                  onKeyDown={(event) => {
                    if (event.key !== 'Enter' && event.key !== ' ') return
                    event.preventDefault()
                    setOpenId(a.id)
                  }}
                  style={{ display: 'grid', gridTemplateColumns: 'minmax(180px,1.6fr) 92px 78px minmax(150px,1fr) 96px 104px', gap: 16, alignItems: 'center', padding: '16px 6px', borderBottom: '1px solid var(--line2)', cursor: 'pointer' }}
                >
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                    <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.target}</span>
                    <span style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {schemePathName(scheme.data, a.category, a.itemKey)}
                    </span>
                  </div>
                  <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{TARGET_LABEL[a.targetType]}</span>
                  <span style={{ fontSize: 12.5, color: a.round === 2 ? 'var(--red)' : 'var(--fg3)' }}>第 {a.round} 次</span>
                  <span style={{ display: 'flex', alignItems: 'baseline', gap: 7, ...num }}>
                    <span style={{ fontSize: 13, color: 'var(--fg3)' }}>{f.score(a.baselineScore)}</span>
                    <span style={{ fontSize: 12, color: 'var(--fg3)' }}>→</span>
                    <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--red)' }}>{f.score(a.resolutionScore ?? a.proposedScore)}</span>
                  </span>
                  <Pill tone={TONE[a.status]}>{APPEAL_STATUS_LABEL[a.status]}</Pill>
                  <span style={{ fontSize: 12, color: 'var(--fg3)', textAlign: 'right' }}>
                    {isOpen(a) ? (a.collective ? '独立共治评审中' : a.round === 2 || a.status === 'escalated' ? '待终裁' : `${decided}/${a.handlers.length} 已复评`) : f.dayMonth(a.resolvedAt)}
                  </span>
                </div>
              )
            })}
          </div>
        </div>
      )}

      <div style={{ paddingTop: 26, display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
        <Btn onClick={() => go('stuList')}>回「我的提交」</Btn>
      </div>
    </div>
  )
}
