import { useState } from 'react'
import { Btn, Empty, PageHead, Pill, Stat, StatGrid, Sub, Note, TextBtn } from '@/components/ui'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { RichText } from '@/components/Markdown'
import { ForcedRejectionNotice } from '@/components/ForcedRejectionNotice'
import { useMyScorecard, useSubmissions, useBaseItems, useResultConfirmActions } from '@/api/queries'
import { ApiError } from '@/api/client'
import type { MyScorecardItem, MyScorecardBaseItem, Submission, BaseItem } from '@/api/types'
import { useApp } from '@/stores/app'
import { mono, num } from '@/lib/style'
import * as f from '@/lib/format'

const DECISION_LABEL: Record<string, string> = { accepted: '认可', adjusted: '调整', rejected: '驳回' }
function ItemRow({ item, live, resultConfirmed }: { item: MyScorecardItem; live?: Submission; resultConfirmed: boolean }) {
  const [open, setOpen] = useState(false)
  const goAppeal = useApp((s) => s.goAppeal)
  const quotable = [...item.evidence, ...item.noteEvidence]
  const canAppeal = !!live?.canAppeal
  const forceRejection = item.forceRejection ?? live?.forceRejection
  const forcedScore = item.forcedScore ?? live?.forcedScore
  const nextRound = ((live?.appealsUsed ?? 0) + 1) as 1 | 2

  return (
    <div style={{ display: 'flex', flexDirection: 'column', background: open ? 'var(--sub)' : 'transparent' }}>
      <button
        type="button"
        className="hv-sub"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '13px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
      >
        <span style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span className="result-item-title" style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em', color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {f.submissionTitle(item.title)}
          </span>
          <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
            {item.ruleSnapshot.categoryName} · {item.ruleSnapshot.item.name}
          </span>
          {forceRejection && <span style={{ color: 'var(--red)', fontSize: 12, fontWeight: 600 }}>已强制驳回 · {forceRejection.previousScore === null ? '未定分' : f.score(forceRejection.previousScore)} → 0 分</span>}
        </span>
        <span data-r="hidesm" style={{ width: 74, flex: 'none', textAlign: 'right', fontSize: 12.5, color: 'var(--fg3)', ...num }}>
          期望 {f.score(item.requestedScore)}
        </span>
        <span className="result-item-score" style={{ width: 74, flex: 'none', textAlign: 'right', fontSize: 14, fontWeight: 600, ...num }}>{f.score(item.score)}</span>
        <span style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
      </button>
      {open && (
        <div style={{ borderTop: '1px solid var(--line2)', padding: '16px 0 20px', display: 'flex', flexDirection: 'column', gap: 12, animation: 'rise .2s ease both' }}>
          <EvidenceFiles items={quotable} />
          {item.note && <RichText value={item.note} evidence={quotable} />}
          {forceRejection && <ForcedRejectionNotice rejection={forceRejection} />}
          {forcedScore && !forceRejection && <ForcedRejectionNotice rejection={forcedScore} />}
          {/* 结论有理由、没人名——单盲口径与「我的提交」一致 */}
          {item.reviews.map((r, i) => (
            <div key={i} style={{ border: '1px solid var(--line)', padding: '11px 13px', display: 'flex', flexDirection: 'column', gap: 6 }}>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
                <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>审核人 {i + 1}</span>
                <Pill tone={r.decision === 'accepted' ? 'ok' : r.decision === 'adjusted' ? 'warn' : 'bad'}>{DECISION_LABEL[r.decision]}</Pill>
                <span style={{ fontSize: 13.5, fontWeight: 600, ...num }}>{f.score(r.score)}</span>
                <span style={{ ...mono('11px', '0'), marginLeft: 'auto' }}>{f.dateTime(r.at)}</span>
              </div>
              <RichText value={r.reason} evidence={quotable} />
            </div>
          ))}
          {!forceRejection && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              {/* 编辑器不在这一页开：跳到「我的申诉」并就地展开这一条。 */}
              <Btn onClick={() => goAppeal('submission', item.id)} disabled={!canAppeal}>
                {nextRound === 1 ? '发起申诉 · 交回原审核人复评' : '再次申诉 · 直接送管理员终裁'}
              </Btn>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
                {resultConfirmed
                  ? '确认记录不影响申诉'
                  : canAppeal
                    ? `${(live?.appealsUsed ?? 0) > 0 ? `已申诉 ${live?.appealsUsed} 次 · 再提一次就由班级管理员终裁` : '交回原审核人复评'} · 处理结果会更新当前成绩`
                    : '班级管理员对这一条已经下过结论，不再受理申诉'}
              </span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/** 基础分/扣分行。live 来自 /me/base-items，申诉权与目标 id 以它为准。 */
function BaseRow({ row, live, resultConfirmed }: { row: MyScorecardBaseItem; live?: BaseItem; resultConfirmed: boolean }) {
  const goAppeal = useApp((s) => s.goAppeal)
  const canAppeal = !!live?.canAppeal && !!live?.id
  return (
    <div style={{ display: 'flex', flexDirection: 'column' }}>
      <div className="result-base-row" style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '11px 0' }}>
        <span style={{ ...mono('11px', '.04em'), width: 32, flex: 'none', color: row.kind === 'penalty' ? 'var(--red)' : 'var(--fg3)' }}>
          {row.kind === 'penalty' ? '扣分' : '基础'}
        </span>
        <span style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 3 }}>
          <span style={{ fontSize: 13, color: 'var(--fg)' }}>{row.itemName}</span>
          <span className="result-base-basis" style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.basis}</span>
        </span>
        <span style={{ width: 66, flex: 'none', textAlign: 'right', fontSize: 13.5, fontWeight: 600, color: row.kind === 'penalty' ? 'var(--red)' : 'var(--fg)', ...num }}>
          {row.kind === 'penalty' ? f.delta(row.score) : f.score(row.score)}
        </span>
        <span style={{ width: 76, flex: 'none', display: 'flex', justifyContent: 'flex-end' }}>
          <Btn
            onClick={() => live?.id && goAppeal(row.kind === 'base' ? 'base_score' : 'penalty_score', live.id)}
            disabled={!canAppeal}
            title={resultConfirmed ? '确认记录不影响申诉' : undefined}
          >
            申诉
          </Btn>
        </span>
      </div>
    </div>
  )
}


export default function StudentResult() {
 const card = useMyScorecard()
 const subs = useSubmissions()
 const base = useBaseItems()
 const confirm = useResultConfirmActions()
 const say = useApp((s) => s.say)
 const go = useApp((s) => s.go)
 if (card.isLoading) return <div className="load-bar"><span /></div>
 if (card.isError || !card.data) return <><Empty title="成绩暂时无法读取" desc="请确认班级已经发布方案。" /><Btn onClick={() => void card.refetch()}>重新读取</Btn></>
 const data = card.data
 const sc = data.scorecard
 const liveSubs = new Map((subs.data?.items ?? []).map((s) => [s.id, s]))
 const liveBase = new Map((base.data?.items ?? []).map((b) => [`${b.category}\x00${b.kind}\x00${b.itemKey}`, b]))
 return <div style={{ animation: 'rise .28s ease both' }}>
  <PageHead en="LIVE SCORECARD" title="我的成绩表" desc="实时查看认定分与处理进度，核对后可一键确认。" side={<Btn onClick={() => { void card.refetch(); void subs.refetch(); void base.refetch() }} disabled={card.isFetching}>{card.isFetching ? '更新中…' : '刷新成绩'}</Btn>} />
  {data.confirmation.recheck && <Note tone="warn">成绩或处理进度已更新，请核对当前版本。上次确认记录仍会保留。</Note>}
  {sc && <>
   <div style={{ paddingTop: 24 }}><StatGrid cols={4}>
    {sc.categories.map((c) => <Stat key={c.key} en={c.name} value={f.score(c.total)} unit={`/ ${c.maxTotal}`} note={`申报 ${f.score(c.itemTotal)} · 基础 ${f.score(c.baseTotal)} · 扣 ${f.score(Math.abs(c.penaltyTotal))}`} pct={f.pct(c.total, c.maxTotal)} />)}
    <Stat en="专业素质分" value={sc.gpa.imported ? f.score(sc.gpa.score) : '—'} note={sc.gpa.imported ? '已导入' : '尚未导入'} />
    <Stat en="当前总分" value={sc.estimatedTotal == null ? '—' : f.score(sc.estimatedTotal)} note={sc.estimateNote} />
   </StatGrid></div>
   <section style={{ paddingTop: 28 }} aria-label="待处理事项"><Sub title="处理进度" note="每 8 秒自动更新" />
    {!sc.issues.length ? <Note>当前没有待处理的审核、申诉或计分异议。</Note> : sc.issues.map((issue) => <div key={`${issue.kind}:${issue.id}`} style={{ padding: '14px 0', borderBottom: '1px solid var(--line)', display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap' }}>
     <span style={{ flex: 1 }}>{issue.title}</span><Pill tone="warn">{issue.kind === 'appeal' ? '申诉处理中' : issue.kind === 'objection' ? '异议待处理' : issue.status === 'arbitrating' ? '待仲裁' : issue.status === 'appealing' ? '申诉中' : '审核中'}</Pill>
     {issue.kind !== 'objection' && <TextBtn onClick={() => go(issue.kind === 'appeal' ? 'stuAppeals' : 'stuList')}>查看进度</TextBtn>}
    </div>)}
   </section>
   <section style={{ paddingTop: 28 }}><Sub title="逐项认定" note="审核人信息不公开" />
    {!sc.items.length ? <Empty title="尚无已定分材料" desc="待审核材料会显示在处理进度中。" /> : sc.items.map((item) => <ItemRow key={item.id} item={item} live={liveSubs.get(item.id)} resultConfirmed={false} />)}
   </section>
   <section style={{ paddingTop: 28 }}><Sub title="基础分与扣分" />
    {sc.baseItems.filter((r) => r.recorded || r.kind === 'base').map((row) => <BaseRow key={`${row.category}:${row.kind}:${row.itemKey}`} row={row} live={liveBase.get(`${row.category}\x00${row.kind}\x00${row.itemKey}`)} resultConfirmed={false} />)}
   </section>
   <section aria-label="成绩核对" style={{ border: '1px solid var(--line)', padding: 24, marginTop: 28 }}>
    <Sub title="核对当前成绩" />
    <p style={{ color: 'var(--fg2)', lineHeight: 1.8 }}>确认表示你已查看当前版本，仍可继续申诉。确认情况不会影响班级结算。</p>
    {data.confirmation.confirmed ? <Pill tone="ok">已核对当前版本 · {f.dateTime(data.confirmation.confirmedAt)}</Pill> : <Btn primary disabled={confirm.isPending} onClick={() => confirm.mutate({ revision: data.revision }, { onSuccess: () => say('当前版本已确认'), onError: (error) => { say(error instanceof ApiError ? error.message : '确认失败，请重试'); void card.refetch() } })}>{confirm.isPending ? '确认中…' : '我已核对当前成绩'}</Btn>}
   </section>
  </>}
 </div>
}
