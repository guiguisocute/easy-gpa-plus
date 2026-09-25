/* 综测小组 · 历史审核。我做过的裁定，以及其中哪些后来被改判了。

   被改判要显示出来而不是藏起来：改判同步回原审核人（§5.1 末），
   这是审核人校准自己口径的唯一反馈来源。 */

import { Fragment, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { Btn, Empty, PageHead, Pill, Row, Seg, Stat, StatGrid, Table, THead, TRow } from '@/components/ui'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { RichText } from '@/components/Markdown'
import { StudentNote } from '@/components/StudentNote'
import { mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useReviewHistory, useReviewTask, useScheme } from '@/api/queries'
import { DECISION_LABEL, type ReviewHistoryRow } from '@/api/types'
import ReviewDesk from './Desk'

const TABS = [
  { key: 'all', label: '全部' },
  { key: 'editable', label: '可修改' },
  { key: 'changed', label: '被改判' },
  { key: 'kept', label: '维持' },
] as const

function HistoryDetails({ row }: { row: ReviewHistoryRow }) {
  const detail = useReviewTask(row.submissionId, true)
  const d = detail.data
  const quotable = [...(d?.evidence ?? []), ...(d?.noteEvidence ?? [])]

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18, padding: '20px 22px', background: 'var(--sub)', borderBottom: '1px solid var(--line)', minWidth: 0 }}>
      <div>
        <div style={{ fontSize: 12.5, color: 'var(--fg2)', marginBottom: 8 }}>
          申请学生：<strong style={{ color: 'var(--fg)' }}>{row.student}</strong>
          <span style={{ ...mono('11.5px', '0'), marginLeft: 10 }}>{row.studentId}</span>
        </div>
        <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--fg)', overflowWrap: 'anywhere' }}>{row.title}</div>
      </div>

      {detail.isLoading ? (
        <div role="status" style={{ fontSize: 12.5, color: 'var(--fg3)' }}>正在读取申报材料…</div>
      ) : detail.isError ? (
        <div role="alert" style={{ display: 'flex', alignItems: 'center', gap: 12, fontSize: 12.5, color: 'var(--fg3)' }}>
          申报材料暂时无法读取，仍可回看下方审核意见。
          <Btn onClick={() => void detail.refetch()}>重试</Btn>
        </div>
      ) : d ? (
        <>
          {d.evidence.length > 0 && <div>
            <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', marginBottom: 10 }}>佐证材料</div>
            <EvidenceFiles items={d.evidence} />
          </div>}
          <div>
            <Row label="申报归类" value={`${d.ruleSnapshot.categoryName} / ${d.ruleSnapshot.item.name}`} />
            <Row label="学生期望分" value={d.requestedScore == null ? '未填写' : f.score(d.requestedScore)} />
            <Row label="提交时间" value={f.dateTime(d.submittedAt)} />
          </div>
          <StudentNote value={d.note} evidence={quotable} />
        </>
      ) : null}

      <div>
        <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', marginBottom: 10 }}>我的审核意见</div>
        {row.reason.trim() ? <RichText value={row.reason} evidence={quotable} /> : <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>未填写审核意见。</span>}
        <Row label="我的结论" value={`${DECISION_LABEL[row.decision]} · ${f.score(row.score)} 分`} />
        <Row label="最终分" value={row.finalScore == null ? '尚未定分' : `${f.score(row.finalScore)} 分`} tone={row.changed ? 'var(--red)' : undefined} />
        <Row label="审核时间 / 用时" value={`${f.dateTime(row.at)} / ${f.duration(row.spentSeconds)}`} />
      </div>
    </div>
  )
}

export default function ReviewHistory() {
  const [tab, setTab] = useState<(typeof TABS)[number]['key']>('all')
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [editingId, setEditingId] = useState<string | null>(null)
  const history = useReviewHistory()
  const scheme = useScheme()

  const all = history.data?.items ?? []
  const rows = all.filter((r) => (tab === 'all' ? true : tab === 'editable' ? r.canEdit : tab === 'changed' ? r.changed : !r.changed && r.finalScore !== null))
  const changed = all.filter((r) => r.changed).length
  const catName = (key: string) => scheme.data?.categories.find((c) => c.key === key)?.name ?? key

  if (editingId) return <ReviewDesk key={editingId} historySubmissionId={editingId} onBack={() => { setEditingId(null); void history.refetch() }} />

  return (
    <div style={{ animation: 'rise .28s ease both', containerType: 'inline-size' }}>
      <PageHead
        en="HISTORY"
        title="历史审核"
        desc="我审过的条目。对方没交之前还能改。"
      />

      <StatGrid cols={3}>
        <Stat en="累计裁定" value={String(all.length)} unit="条" note="含通过、调分与驳回" />
        <Stat en="被改判" value={String(changed)} unit="条" note="经仲裁或申诉后分值发生变化" tone={changed > 0 ? 'var(--red)' : undefined} />
        <Stat
          en="一致率"
          value={all.length === 0 ? '—' : `${Math.round(((all.length - changed) / all.length) * 100)}%`}
          note="结论未被改动的比例，不作考核用途"
        />
      </StatGrid>

      <div style={{ padding: '22px 0 14px' }}>
        <Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={setTab} />
      </div>

      {history.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty title="没有这一类记录" desc={all.length === 0 ? '你还没有提交过审核结论。' : '换个筛选看看。'} />
      ) : (
        <Table cols="144px minmax(180px,1.8fr) 100px 112px 76px 84px 112px 104px">
          <THead cells={['申请学生', '条目', '大项', '我的结论', '最终分', '结果', '时间', '操作']} />
          {rows.map((r) => {
            const expanded = expandedId === r.submissionId
            const detailId = `review-history-${r.id}`
            return (
              <Fragment key={r.id}>
                <TRow
                  onClick={() => setExpandedId(expanded ? null : r.submissionId)}
                  label={`${expanded ? '收起' : '展开'} ${r.student} · ${r.studentId} · ${r.title}`}
                  expanded={expanded}
                  controls={detailId}
                  bg={expanded ? 'var(--sub)' : undefined}
                  cells={[
                    <Fragment key="student">
                      <ChevronRight aria-hidden size={14} style={{ flex: 'none', transform: expanded ? 'rotate(90deg)' : undefined }} />
                      <span style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 0 }}>
                        <strong style={{ fontSize: 13, color: 'var(--fg)', overflowWrap: 'anywhere' }}>{r.student}</strong>
                        <span style={{ ...mono('11px', '0'), overflowWrap: 'anywhere' }}>{r.studentId}</span>
                      </span>
                    </Fragment>,
                    <span key="t" title={r.title} style={{ fontSize: 13, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.title}</span>,
                    <span key="s">{catName(r.category)}</span>,
                    <span key="m" style={num}>
                      {DECISION_LABEL[r.decision]} {f.score(r.score)}
                    </span>,
                    <span key="f" style={{ ...num, fontWeight: 600, color: r.changed ? 'var(--red)' : 'var(--fg)' }}>
                      {f.score(r.finalScore)}
                    </span>,
                    <Pill key="r" tone={r.changed ? 'bad' : r.finalScore === null ? 'idle' : 'ok'}>
                      {r.changed ? '被改判' : r.finalScore === null ? '未定分' : '维持'}
                    </Pill>,
                    <span key="a" style={mono('11.5px', '0')}>{f.dateTime(r.at)}</span>,
                    r.canEdit ? (
                      <span key="edit" onClick={(event) => event.stopPropagation()}>
                        <Btn onClick={() => setEditingId(r.submissionId)}>修改结论</Btn>
                      </span>
                    ) : <span key="edit" style={{ color: 'var(--fg3)' }}>{expanded ? '收起详情' : '展开详情'}</span>,
                  ]}
                />
                {expanded && <div id={detailId} role="row" style={{ gridColumn: '1 / -1', minWidth: 0 }}>
                  <div role="cell" aria-colspan={8} style={{ maxWidth: '100cqw', position: 'sticky', left: 0 }}><HistoryDetails row={r} /></div>
                </div>}
              </Fragment>
            )
          })}
        </Table>
      )}

      <div style={{ borderTop: '1px solid var(--line)', marginTop: 26, paddingTop: 18, fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8, maxWidth: 720, textWrap: 'pretty' }}>
        修改后保留旧结论与操作记录。另一名审核人提交后，或条目进入仲裁、完成定分、审核关闭时，不能再修改。如多次出现相似分歧，可联系班级管理员补充方案说明。
      </div>
    </div>
  )
}
