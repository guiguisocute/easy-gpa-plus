/* 综测小组 · 举报复核。

   举报对象包括基础分、扣分和已定分申报。材料区复用审核台的原件、规则与历史轨迹；
   举报主张和复核结论保留独立的处理语义。

   背靠背和初审一样：只显示对方交了没有，不显示他写了什么。两人一致即落定，
   不一致直接升给班级管理员，不在小组内部再兜一圈。

   举报人是谁，这一页从头到尾拿不到——后端不下发。 */

import { useEffect, useMemo, useState } from 'react'
import { ShieldAlert } from 'lucide-react'
import { Btn, ChoiceChip, Empty, Note, PageHead, Pill, Row, Split, SplitCol, Stat, StatGrid, Sub } from '@/components/ui'
import { ReportMaterial } from '@/components/ReportMaterial'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { RichText } from '@/components/Markdown'
import { ReviewSubject } from '@/components/ReviewSubject'
import { mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useReportReviewActions, useReviewReport, useReviewTasks } from '@/api/queries'
import { REPORT_DECISION_LABEL, REPORT_KIND_LABEL, type ReportDecision, type ReportTask } from '@/api/types'

/** 队列里的 id 是 `report:123`，接口要的是 123。 */
export const reportTaskId = (id: string) => id.replace(/^report:/, '')

const CHOICES: { key: ReportDecision; label: string; tone: 'ok' | 'warn' | 'bad' }[] = [
  { key: 'uphold', label: '属实 · 按举报认定', tone: 'ok' },
  { key: 'adjust', label: '属实 · 改分', tone: 'warn' },
  { key: 'reject', label: '不成立 · 分数不动', tone: 'bad' },
]

export default function ReviewReports() {
  const queue = useReviewTasks('mine')
  const [openId, setOpenId] = useState<string | null>(null)

  const tasks = useMemo(
    () => (queue.data?.items ?? []).filter((t): t is ReportTask => t.type === 'report'),
    [queue.data],
  )

  if (queue.isLoading) return <div className="load-bar"><span /></div>
  if (openId) return <ReportDesk id={openId} onBack={() => setOpenId(null)} />

  const overdue = tasks.filter((t) => t.overdue).length

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="REPORT DESK"
        title="举报复核"
        desc="匿名举报，两人分头复核"
        side={
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--fg3)' }}>
            <ShieldAlert size={15} strokeWidth={1.7} />
            <span style={{ fontSize: 12.5 }}>举报人对你不可见</span>
          </div>
        }
      />

      <StatGrid cols={2}>
        <Stat en="待我复核" value={String(tasks.length)} unit="条" note="学生举报，等你判" tone={tasks.length > 0 ? 'var(--warn)' : undefined} />
        <Stat en="其中已逾期" value={String(overdue)} unit="条" note="超过 SLA 时限" tone={overdue > 0 ? 'var(--red)' : undefined} />
      </StatGrid>

      <div style={{ paddingTop: 22 }}>
        {tasks.length === 0 ? (
          <Empty title="没有待复核的举报" desc="有举报进来会派给你。" />
        ) : (
          <>
            <Sub title="待复核" note="两人独立判断，复核期间互不可见" />
            {tasks.map((task) => (
              <button
                key={task.id}
                type="button"
                className="hv-sub"
                onClick={() => setOpenId(reportTaskId(task.id))}
                style={{ display: 'flex', alignItems: 'center', gap: 14, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '15px 6px', font: 'inherit', textAlign: 'left', cursor: 'pointer', flexWrap: 'wrap' }}
              >
                <span style={{ display: 'flex', flexDirection: 'column', gap: 3, width: 150, flex: 'none', minWidth: 0 }}>
                  <span style={{ fontSize: 14.5, fontWeight: 600, letterSpacing: '-.02em', color: 'var(--fg)' }}>{task.student}</span>
                  <span style={mono('11px', '.02em')}>{task.studentId}</span>
                </span>
                <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0, flex: 1 }}>
                  <span style={{ fontSize: 13, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{task.itemName}</span>
                  <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{REPORT_KIND_LABEL[task.kind]} · {task.categoryName}</span>
                </span>
                <span style={{ fontSize: 16, fontWeight: 600, color: 'var(--red)', flex: 'none', ...num }}>{f.score(task.requestedScore)}</span>
                <span style={{ fontSize: 12, color: task.overdue ? 'var(--red)' : 'var(--fg3)', width: 96, flex: 'none', textAlign: 'right' }}>
                  {task.overdue ? '已逾期' : `${task.reviewCount}/${task.expectedReviews} 已复核`}
                </span>
              </button>
            ))}
          </>
        )}
      </div>

      <div style={{ paddingTop: 26 }}>
        <Note>举报只能要求把分往低了改。学生不服还能申诉。</Note>
      </div>
    </div>
  )
}

/* ---- 单条复核 ---- */

function ReportDesk({ id, onBack }: { id: string; onBack: () => void }) {
  const say = useApp((s) => s.say)
  const detail = useReviewReport(id)
  const { decide } = useReportReviewActions()
  const [choice, setChoice] = useState<ReportDecision>('uphold')
  const [score, setScore] = useState('')
  const [reason, setReason] = useState('')
  const [uploading, setUploading] = useState(false)
  const [startedAt, setStartedAt] = useState(() => Date.now())

  useEffect(() => {
    setChoice('uphold')
    setScore('')
    setReason('')
    setStartedAt(Date.now())
  }, [id])

  if (detail.isLoading) return <div className="load-bar"><span /></div>
  const d = detail.data
  if (!d) return <Empty title="举报不存在" desc="它可能已经处理完了，也可能不再归你复核。" />

  const scoreNum = Number(score)
  /* 维持现状的那个分：扣分项是"再扣 0"，基础项和已定分条目是"保持当前分"。
     改分只能落在举报主张与现状之间——判得比举报还重，用的就是材料以外的东西；
     判得比现状还高，那不是举报该做的事。后端两头都会再拦一次。 */
  const quo = d.kind === 'penalty' ? 0 : (d.currentScore ?? 0)
  const scoreBad = choice === 'adjust' && (score.trim() === '' || Number.isNaN(scoreNum) || scoreNum < d.proposedScore || scoreNum > quo)
  const reasonLength = [...reason.trim()].length
  const canSubmit = !scoreBad && reasonLength >= 6 && !decide.isPending && !d.myReview

  const submit = () => {
    const spent = Math.max(1, Math.round((Date.now() - startedAt) / 1000))
    decide.mutate(
      { id, decision: choice, score: choice === 'adjust' ? scoreNum : undefined, reason: reason.trim(), spentSeconds: spent },
      {
        onSuccess: (r) => {
          say(
            r.conflict
              ? '复核已提交 · 和对方不一致，已经升给班级管理员定'
              : r.bothDecided
                ? r.reportStatus === 'dismissed'
                  ? '复核已提交 · 两人都认为举报不成立，分数不动'
                  : `复核已提交 · 两人一致，认定 ${f.score(r.finalScore)} 分`
                : '复核已提交 · 等另一个人复核（他看不到你写了什么）',
          )
          onBack()
        },
        onError: (e) => say(e instanceof ApiError ? e.message : '提交失败'),
      },
    )
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <button
        type="button"
        className="hv-fg"
        onClick={onBack}
        style={{ display: 'flex', alignItems: 'center', gap: 8, background: 'none', border: 0, margin: '30px 0 0', padding: 0, font: 'inherit', fontSize: 12.5, color: 'var(--fg2)', cursor: 'pointer' }}
      >
        ← 回到举报列表
      </button>

      <PageHead
        en={`REPORT · #${d.id}`}
        title="举报复核"
        desc={`${REPORT_KIND_LABEL[d.kind]} · ${d.categoryName} / ${d.itemName} · 提交于 ${f.dateTime(d.createdAt)}`}
        side={
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 7 }}>
            <Pill tone="bad">举报主张 {f.score(d.proposedScore)} 分</Pill>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)', ...num }}>复核进度 {d.decidedReviews}/{d.expectedReviews}</span>
          </div>
        }
      />

      <div style={{ paddingBottom: 22 }}>
        <ReviewSubject
          label="被举报的学生"
          name={d.student}
          sid={d.studentId}
          meta={`${REPORT_KIND_LABEL[d.kind]} · ${d.categoryName} / ${d.itemName}${d.quantity == null ? '' : ` · 举报主张 ${d.quantity} 次`}`}
        />
      </div>

      <Split cols="1.25fr 1fr">
        <SplitCol first>
          <div style={{ padding: '4px 0 30px' }}>
            <ReportMaterial key={d.id} report={d} />

            <div style={{ paddingTop: 20 }}>
              <Note tone="warn">
                {d.kind === 'penalty'
                  ? '判成立就在这一项已有的扣分上接着扣。上面「已记在册」里要是已经有这件事，说明重复了，该判不成立。'
                  : '判成立的话，这一条的分会改成你填的值。判「不成立」就保持现在的分不动。'}
              </Note>
            </div>
          </div>
        </SplitCol>

        <SplitCol>
          <div data-r="deskside" style={{ paddingBlock: '4px 30px', display: 'flex', flexDirection: 'column', gap: 22 }}>
            {d.myReview ? (
              <div style={{ border: '1px solid var(--line)', padding: 20, background: 'var(--sub)' }}>
                <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', paddingBottom: 10 }}>你已就这条举报给出复核</div>
                <Row label="结论" value={REPORT_DECISION_LABEL[d.myReview.decision]} />
                <Row label="认定分" value={f.delta(d.myReview.score)} />
                <Row label="提交于" value={f.dateTime(d.myReview.at)} />
                <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75, marginTop: 12 }}><RichText value={d.myReview.reason} evidence={[...d.evidence, ...(d.noteEvidence ?? [])]} /></div>
              </div>
            ) : (
              <div style={{ border: '1px solid var(--line)', padding: 20, background: 'var(--sub)' }}>
                <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', paddingBottom: 12 }}>我的复核</div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 9 }}>
                  {CHOICES.map((c) => (
                    <ChoiceChip key={c.key} on={c.key === choice} tone={c.tone} onClick={() => setChoice(c.key)}>
                      {c.label}
                    </ChoiceChip>
                  ))}
                </div>

                {choice === 'adjust' && (
                  <label style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 18 }}>
                    <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>
                      认定分 · 允许 {f.score(d.proposedScore)} — {f.score(quo)}
                    </span>
                    <input
                      value={score}
                      onChange={(e) => setScore(e.target.value)}
                      inputMode="decimal"
                      placeholder={`举报主张 ${f.score(d.proposedScore)}`}
                      style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${scoreBad && score.trim() ? 'var(--red)' : 'var(--warn)'}`, padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
                    />
                    {scoreBad && score.trim() !== '' && (
                      <span style={{ fontSize: 12, color: 'var(--red)' }}>
                        只能落在 {f.score(d.proposedScore)} 与 {f.score(quo)} 之间——要判得更重请走实名的小组提案
                      </span>
                    )}
                  </label>
                )}

                <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 18 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: reason.trim() && reasonLength < 6 ? 'var(--red)' : 'var(--fg2)' }}>
                    复核理由 · 至少 6 字
                  </span>
                  <MarkdownEditor
                    onUploadingChange={setUploading}
                    evidence={[...d.evidence, ...(d.noteEvidence ?? [])]}
                    uploadTarget={{ kind: 'note', owner: 'report-review', id: d.id }}
                    value={reason}
                    onChange={setReason}
                    minHeight={96}
                    invalid={reason.trim() !== '' && reasonLength < 6}
                    placeholder={choice === 'reject' ? '写清哪一点对不上——这段话会跟着结论留档' : '写清你核了什么，依据的是细则里哪一条'}
                  />
                </div>

                <div style={{ display: 'flex', gap: 10, marginTop: 18, flexWrap: 'wrap' }}>
                  <Btn primary tone={CHOICES.find((c) => c.key === choice)?.tone} disabled={uploading || !canSubmit} onClick={submit}>
                    {decide.isPending ? '提交中…' : '提交复核'}
                  </Btn>
                  <Btn onClick={onBack}>先回列表</Btn>
                </div>
              </div>
            )}

            <div>
              <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 12 }}>复核状态</div>
              {[
                { who: '你', decided: !!d.myReview, bold: true },
                { who: '另一名复核人', decided: d.decidedReviews - (d.myReview ? 1 : 0) > 0, bold: false },
              ].map((p) => (
                <div key={p.who} style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0' }}>
                  <span style={{ width: 5, height: 5, flex: 'none', background: p.decided ? 'var(--ok)' : 'var(--warn)' }} />
                  <span style={{ fontSize: 12.5, fontWeight: p.bold ? 600 : 400, color: 'var(--fg)' }}>{p.who}</span>
                  <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>{p.decided ? '已提交复核' : '尚未提交'}</span>
                </div>
              ))}
              <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 12, textWrap: 'pretty' }}>
                两人给的分一样就定了，不一样交班级管理员。
              </div>
            </div>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}
