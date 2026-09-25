import { useState } from 'react'
import { api } from '@/api/client'
import { RichText } from '@/components/Markdown'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { RuleCard } from '@/components/RuleCard'
import { ReviewSubject } from '@/components/ReviewSubject'
import { StudentNote } from '@/components/StudentNote'
import {
  DecisionChips,
  DeskPanel,
  DeskScoreField,
  DeskSide,
  EvidencePane,
  ExpectedScoreBar,
  ReviewQueueBar,
  ReviewerProgress,
  deskChoices,
  type QueueItem,
} from '@/components/ReviewDesk'
import {
  PageHead,
  Btn,
  Empty,
  BackLink,
  Note,
  Pill,
  Row,
  Split,
  SplitCol,
  Sub,
} from '@/components/ui'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { useGovernanceAction, useGovernanceCase, type ProposalView } from '@/api/governance'
import { useScheme } from '@/api/queries'
import { userErrorMessage } from '@/api/errorMessages'
import { scoreBounds } from '@/lib/claim'
import { findCurrentItem } from '@/lib/ruleDiff'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import type { ReviewDecision } from '@/api/types'
import GovernanceDiscussion from './GovernanceDiscussion'
import { statusName, statusTone, governanceDate as date } from '@/lib/governanceView'

const CLOSED = ['applied', 'rejected', 'stale']

export function ProposalCard({
  item,
  pending,
  run,
  openCase,
}: {
  item: ProposalView
  pending: boolean
  run: (path: string, body?: unknown) => Promise<boolean>
  openCase: (id: string) => void
}) {
  const say = useApp((s) => s.say)
  const p = item.proposal,
    isCase = p.kind === 'review' || p.kind === 'appeal'
  const voting =
    !isCase &&
    ['discussion', 'voting'].includes(p.status) &&
    Date.now() >= new Date(p.opensAt).getTime() &&
    Date.now() < new Date(p.closesAt).getTime()
  return (
    <article className="gov-proposal">
      <Sub
        title={p.title}
        actions={<Pill tone={statusTone[p.status]}>{statusName[p.status] ?? p.status}</Pill>}
      />
      {!!item.evidence?.length && <EvidenceFiles items={item.evidence} />}
      <RichText value={p.body} evidence={item.evidence ?? []} />
      {item.summary && (
        <p
          className="gov-summary"
          style={{ whiteSpace: 'pre-wrap', maxHeight: 300, overflow: 'auto' }}
        >
          {item.summary}
        </p>
      )}
      <p>
        {p.electorateCount} 人参与范围 · 同一决定至少 {p.requiredYes} 票 · 已参与{' '}
        {item.participated} 人
      </p>
      <small>
        {date(p.opensAt)} 开始 · {date(p.closesAt)} 截止
      </small>
      {item.tally && !isCase && (
        <p>
          赞成 {item.tally.yes} · 反对 {item.tally.no} · 弃权 {item.tally.abstain}
        </p>
      )}
      {item.myChoice && (
        <p>
          我的选择：
          {({ yes: '赞成', no: '反对', abstain: '弃权' } as Record<string, string>)[item.myChoice]}
        </p>
      )}
      <div className="gov-actions">
        {isCase ? (
          <Btn primary onClick={() => openCase(p.id)}>
            查看本案材料与意见
          </Btn>
        ) : (
          voting &&
          item.eligible &&
          (['yes', 'no', 'abstain'] as const).map((choice) => (
            <Btn
              key={choice}
              disabled={pending}
              onClick={() => void run(`/proposals/${p.id}/vote`, { choice, lock: false })}
            >
              {{ yes: '赞成', no: '反对', abstain: '弃权' }[choice]}
            </Btn>
          ))
        )}
        {p.action === 'export' && p.result?.jobId && (
          <Btn
            onClick={async () => {
              try {
                const job = await api.get<{ downloadUrl?: string }>(
                  `/governance/exports/${p.result?.jobId}`,
                )
                if (job.downloadUrl) window.open(job.downloadUrl, '_blank', 'noopener,noreferrer')
                else say('导出仍在生成，请稍后领取')
              } catch (err) {
                say(userErrorMessage(err))
              }
            }}
          >
            领取授权导出
          </Btn>
        )}
        {!CLOSED.includes(p.status) && (
          <Btn disabled={pending} onClick={() => void run(`/proposals/${p.id}/close`)}>
            检查进度与执行
          </Btn>
        )}
      </div>
      {!isCase && <GovernanceDiscussion id={p.id} closed={CLOSED.includes(p.status)} />}
      {voting && !item.eligible && <p>你不在本次冻结的投票名单内；加入共治后可以参与后续事项。</p>}
    </article>
  )
}

/* 共治的独立评审台。和综测小组的初审工作台判的是同一件事，所以用同一套件：
   当前对象条、佐证区、学生填报、档位表、认定分输入、评审进度、队列条。
   不同的只有两点——这里是随机抽到的席位而不是派单，以及分歧要靠再表决收口。 */
export function CaseView({
  id,
  back,
  queue = [],
  onSelect,
}: {
  id: string
  back: () => void
  /** 我手上这一批案件，用来在页尾铺队列条。只给一条时仍然显示，位置看得见。 */
  queue?: ProposalView[]
  onSelect?: (id: string) => void
}) {
  const detail = useGovernanceCase(id),
    action = useGovernanceAction(),
    currentScheme = useScheme(),
    say = useApp((s) => s.say)
  const [score, setScore] = useState(''),
    [reason, setReason] = useState(''),
    [uploading, setUploading] = useState(false),
    /* 默认档位要等材料到手才定得下来（学生没填期望分就没有「按期望分通过」这一档），
       所以存的是"我点过哪一档"，没点过就用下面算出来的那一档。 */
    [picked, setPicked] = useState<ReviewDecision | null>(null)
  const queueItems: QueueItem[] = queue.map((row) => ({
    id: row.proposal.id,
    title: row.proposal.title,
    done: !!row.mySubmitted,
  }))
  const index = Math.max(0, queueItems.findIndex((row) => row.id === id))
  const goTo = (next: number) => {
    const target = queueItems[next]
    if (!target || !onSelect || target.id === id) return
    setScore('')
    setReason('')
    setPicked(null)
    onSelect(target.id)
  }

  if (!detail.data)
    return (
      <>
        <BackLink label="返回我的评审" onClick={back} />
        <Empty
          title="读取评审材料"
          desc={detail.error ? userErrorMessage(detail.error) : undefined}
        />
        <div style={{ display: 'flex', gap: 10, marginTop: 18 }}>
          <Btn onClick={() => void detail.refetch()}>重试</Btn>
          <Btn onClick={back}>返回列表</Btn>
        </div>
      </>
    )

  const d = detail.data
  const closed = CLOSED.includes(d.proposal.status)
  const objection = d.proposal.action === 'objection'
  /* 申报案件才有「学生期望分」可以照着通过；成绩复核判的是另一个数。
     驳回只对申报和异议成立：举报和申诉由后端按规则算，没有清零这一档。 */
  const claimed = d.proposal.action === 'submission'
  const hasExpected = claimed && d.requestedScore != null
  const choices = deskChoices({
    accepted: '通过 · 按期望分',
    adjusted: claimed ? '调整认定分' : '按规则认定分数',
    rejected: objection ? '不成立 · 维持原分' : '驳回 · 计 0 分',
  }).filter((c) => (c.key === 'accepted' ? hasExpected : c.key === 'rejected' ? claimed || objection : true))
  const decision = picked && choices.some((c) => c.key === picked) ? picked : choices[0].key
  const choice = choices.find((c) => c.key === decision)!
  const rule = d.ruleSnapshot.item?.scoreRule
  const { lo, hi } = rule ? scoreBounds(rule) : { lo: 0, hi: Infinity }
  const rejected = decision === 'rejected'
  const needScore = !rejected && choice.needScore
  const scoreNum = Number(score)
  const scoreBad = needScore && (score.trim() === '' || Number.isNaN(scoreNum) || scoreNum < lo || scoreNum > hi)
  const reasonBad = [...reason.trim()].length < 6
  const touched = reason.trim() !== '' || score.trim() !== ''
  const mine = 'score' in d.myOpinion
  const canSubmit = d.canReview && d.proposal.status === 'voting' && !mine
  const submitHint = scoreBad
    ? score.trim() === ''
      ? '请填写认定分值。'
      : `认定分值须在 ${lo} — ${hi === Infinity ? '不封顶' : hi} 之间。`
    : reasonBad
      ? '规则与证据依据至少填写 6 个字。'
      : ''

  const submit = async () => {
    if (!canSubmit || scoreBad || reasonBad || uploading) return
    try {
      await action.mutateAsync({
        path: `/cases/${id}/opinion`,
        body: {
          /* 驳回一律送 0：后端按案件类型决定这是清零还是「异议不成立、维持原分」。 */
          score: rejected ? 0 : needScore ? scoreNum : (d.requestedScore ?? 0),
          decision: rejected ? 'reject' : 'accept',
          reason: reason.trim(),
          category: d.category,
          itemKey: d.itemKey,
          expectedVersion: d.version,
        },
      })
      say('认定已提交，等其他评审独立交齐')
    } catch (err) {
      say(userErrorMessage(err))
    }
  }

  return (
    <div className="governance-page">
      <BackLink label="返回我的评审" onClick={back} />
      <PageHead
        en="INDEPENDENT REVIEW"
        title={d.title}
        desc="先核对证据和规则，再提交自己的判断。本人事项必须回避。"
        side={
          <>
            <Pill tone={statusTone[d.proposal.status]}>
              {statusName[d.proposal.status] ?? d.proposal.status}
            </Pill>
            {!closed && (
              <Btn
                disabled={action.isPending}
                onClick={async () => {
                  try {
                    const result = await action.mutateAsync({ path: `/proposals/${id}/close` })
                    say(result.notice ?? '进度已更新')
                  } catch (error) {
                    say(userErrorMessage(error))
                  }
                }}
              >
                检查进度与补位
              </Btn>
            )}
          </>
        }
      />
      <ReviewSubject
        sticky
        label="本案当事人"
        name={d.student}
        sid={d.studentId}
        meta={`当前条目：${d.title} · ${d.ruleSnapshot.categoryName ?? ''} / ${d.ruleSnapshot.item?.name ?? d.itemKey}${d.submittedAt ? ` · 提交于 ${f.dateTime(d.submittedAt)}` : ''}`}
        side={
          <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
            {d.proposal.electorateCount} 个席位 · 同一裁决至少 {d.proposal.requiredYes} 票
          </span>
        }
      />

      <Split cols="1.25fr 1fr">
        <SplitCol first>
          <div style={{ padding: '24px 0' }}>
            <EvidencePane items={d.evidence} note="点击预览或下载，每看一次都会记进审计" />

            <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', padding: '26px 0 12px' }}>
              事实与申报
            </div>
            <Row label="事项名称" value={d.title} />
            {claimed && (
              <>
                <Row
                  label="学生自报"
                  value={f.claimText(d.claim, rule?.type === 'per_unit' ? rule.unit : undefined)}
                />
                <Row
                  label="学生期望分"
                  value={d.requestedScore == null ? '未填，待你认定' : f.score(d.requestedScore)}
                />
              </>
            )}
            <Row label="已有认定分" value={d.currentScore == null ? '尚未认定' : f.score(d.currentScore)} />
            <div style={{ marginTop: 16 }}>
              <StudentNote
                value={d.body}
                evidence={d.evidence}
                title={claimed ? '学生的补充说明' : '当事人与提案人的说明'}
              />
            </div>

            <div style={{ padding: '26px 0 0' }}>
              {d.ruleSnapshot.item && (
                <RuleCard
                  item={d.ruleSnapshot.item}
                  categoryName={d.ruleSnapshot.categoryName}
                  capturedAt={d.ruleSnapshot.capturedAt}
                  currentItem={findCurrentItem(currentScheme.data, d.ruleSnapshot.item.key)}
                  claim={claimed ? d.claim : undefined}
                  requestedScore={claimed ? d.requestedScore : undefined}
                />
              )}
            </div>

            {d.changed && !closed && (
              <div role="alert" style={{ paddingTop: 20 }}>
                <Note tone="warn">依据已变化，请核对新说明，并检查是否需要开启新版本。</Note>
              </div>
            )}
            <GovernanceDiscussion id={id} evidence closed={d.canReview || closed} />
          </div>
        </SplitCol>

        <SplitCol>
          <DeskSide>
            <ExpectedScoreBar
              label={claimed ? '学生期望分' : '已有认定分'}
              value={
                claimed
                  ? d.requestedScore == null
                    ? '未填'
                    : f.score(d.requestedScore)
                  : d.currentScore == null
                    ? '未定'
                    : f.score(d.currentScore)
              }
              unit={(claimed ? d.requestedScore : d.currentScore) == null ? undefined : '分'}
              tone={(claimed ? d.requestedScore : d.currentScore) == null ? 'var(--warn)' : undefined}
              side={`规则允许 ${lo} — ${hi === Infinity ? '不封顶' : hi}`}
            />

            {mine ? (
              <DeskPanel title={closed ? '本案已执行集体裁决' : '你已就本案给出认定'}>
                <Row label="认定分" value={f.score(Number(d.myOpinion.score))} />
                <div style={{ marginTop: 12 }}>
                  <RichText value={String(d.myOpinion.reason ?? '')} evidence={d.evidence} />
                </div>
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 12 }}>
                  {closed ? '认定与依据已留档。' : '提交后即锁定，其他评审交齐前互相看不到内容。'}
                </div>
              </DeskPanel>
            ) : !d.canReview ? (
              <DeskPanel title="本案无需你提交评审">
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7 }}>
                  你可以查看当前有权访问的材料与处理进度。
                </div>
              </DeskPanel>
            ) : d.proposal.status !== 'voting' ? (
              <DeskPanel title="当前不在独立认定阶段">
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7 }}>
                  本轮已进入共同评议或已结束，请在下面对已提交的裁决方案表态。
                </div>
              </DeskPanel>
            ) : (
              <DeskPanel title="我的独立认定">
                <DecisionChips choices={choices} value={decision} onChange={setPicked} />
                {needScore && (
                  <DeskScoreField
                    label={<>认定分值 · 规则允许 {lo} — {hi === Infinity ? '不封顶' : hi}</>}
                    value={score}
                    onChange={setScore}
                    invalid={scoreBad}
                    hint={<>请填 {lo} — {hi === Infinity ? '任意' : hi} 之间的数字</>}
                    placeholder="按佐证认定"
                  />
                )}
                {rejected && (
                  <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 18 }}>
                    {objection
                      ? `异议不成立，本项保持 ${d.currentScore == null ? '原分' : `${f.score(d.currentScore)} 分`}。`
                      : '材料不成立，本项记 0 分。'}
                  </div>
                )}

                {/* 编辑器里有工具条按钮，不能再套 <label>——一个 label 关联多个控件，
                    点标题会跳到哪个控件是没定论的。 */}
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 20 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: reasonBad && reason.trim() ? 'var(--red)' : 'var(--fg2)' }}>
                    规则与证据依据 · 至少 6 个字
                  </span>
                  <MarkdownEditor
                    value={reason}
                    onChange={setReason}
                    onUploadingChange={setUploading}
                    invalid={reasonBad && reason.trim() !== ''}
                    evidence={d.evidence}
                    placeholder="写清依据的是哪条细则、佐证里的哪一部分。这段话会随裁决留档"
                  />
                </div>

                <div style={{ display: 'flex', gap: 10, marginTop: 20, flexWrap: 'wrap' }}>
                  <Btn
                    primary
                    tone={choice.tone}
                    disabled={uploading || action.isPending || scoreBad || reasonBad}
                    onClick={() => void submit()}
                  >
                    {action.isPending ? '提交中…' : '确认并提交独立认定'}
                  </Btn>
                  {queueItems.length > 1 && onSelect && (
                    <Btn onClick={() => goTo(index + 1 < queueItems.length ? index + 1 : 0)}>先跳过</Btn>
                  )}
                </div>
                {/* 还没动手时这只是"接下来要填什么"，别一进来就是一行红字。 */}
                {submitHint && (
                  <div
                    role="status"
                    style={{ color: touched ? 'var(--red)' : 'var(--fg3)', fontSize: 12.5, lineHeight: 1.7, marginTop: 10 }}
                  >
                    {submitHint}
                  </div>
                )}
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 14, textWrap: 'pretty' }}>
                  提交后锁定，其他评审尚未提交的意见不会展示。
                </div>
              </DeskPanel>
            )}

            <ReviewerProgress
              rows={[
                ...(d.canReview || mine
                  ? [{ who: '你', state: mine ? '已提交认定' : '待提交', tone: mine ? 'var(--ok)' : 'var(--warn)', bold: true }]
                  : []),
                {
                  who: '本轮席位',
                  state: `${d.submitted} / ${d.proposal.electorateCount} 已提交`,
                  tone: d.submitted >= d.proposal.electorateCount ? 'var(--ok)' : 'var(--fg3)',
                },
              ]}
              note={`只显示交了几份，不显示谁交的、交了什么。同一裁决达到 ${d.proposal.requiredYes} 票就生效；有分歧先随机补人，仍无多数再一起评议。`}
            />

            {d.canReview && d.proposal.status === 'deliberating' && (
              <Btn
                disabled={action.isPending}
                onClick={async () => {
                  try {
                    await action.mutateAsync({
                      path: `/cases/${id}/ballot`,
                      body: { candidate: 'abstain' },
                    })
                    say('已记录本轮弃权')
                  } catch (err) {
                    say(userErrorMessage(err))
                  }
                }}
              >
                本轮评议弃权
              </Btn>
            )}

            {d.opinions?.map((opinion, index) => (
              <DeskPanel
                key={index}
                title={`候选裁决 ${index + 1} · ${
                  opinion.opinion.decision === 'rejected' && objection
                    ? '异议不成立，维持原分'
                    : `${f.score(opinion.opinion.score)} 分`
                }`}
              >
                <RichText value={opinion.reason} />
                <div style={{ marginTop: 14 }}>
                  <Btn
                    disabled={action.isPending || d.proposal.status !== 'deliberating'}
                    onClick={async () => {
                      try {
                        await action.mutateAsync({
                          path: `/cases/${id}/ballot`,
                          body: { candidate: opinion.hash },
                        })
                        say('已提交本轮表决')
                      } catch (err) {
                        say(userErrorMessage(err))
                      }
                    }}
                  >
                    支持这份裁决
                  </Btn>
                </div>
              </DeskPanel>
            ))}

            <Note>{objection ? '裁决生效后当事人仍可申诉。' : '驳回记 0 分，当事人仍可申诉。'}</Note>
          </DeskSide>
        </SplitCol>
      </Split>

      {onSelect && (
        <ReviewQueueBar
          items={queueItems}
          index={index}
          onGo={goTo}
          hint={`共 ${queueItems.length} 件 · 待我提交 ${queueItems.filter((row) => !row.done).length} 件`}
        />
      )}
    </div>
  )
}
