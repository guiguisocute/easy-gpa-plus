import { Fragment, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { Btn, Empty, LineTabs, PageHead, Pill, Row, Sub, Table, THead, TRow } from '@/components/ui'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { RichText } from '@/components/Markdown'
import { StudentNote } from '@/components/StudentNote'
import { HISTORY_KINDS, useAdjudicationHistory, useAdjudicationHistoryDetail, type AdjudicationHistoryRow, type HistoryKind } from '@/api/adjudicationHistory'
import { useScheme } from '@/api/queries'
import { ApiError } from '@/api/client'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { schemePathName } from '@/lib/schemeTree'

const DECISIONS: Record<string, string> = {
  scored: '已定分', final: '已终裁', applied: '照准生效', adjusted: '调整生效',
  dismissed: '已驳回', resolved: '已处理',
}
const REVIEW_DECISIONS: Record<string, string> = {
  accepted: '通过', adjusted: '调分', rejected: '驳回', adjust: '调分', reject: '驳回', uphold: '维持',
}

function decisionLabel(row: AdjudicationHistoryRow) {
  return row.kind === 'force_reject' ? '强制驳回' : DECISIONS[row.decision] ?? '已处理'
}

const historyTime = (iso: string) => `${f.date(iso)} ${f.dateTime(iso).slice(-5)}`

function historyDate(value: string, nextDay = false) {
  if (!value) return ''
  const date = new Date(`${value}T00:00:00`)
  if (nextDay) date.setDate(date.getDate() + 1)
  return Number.isNaN(date.getTime()) ? '' : date.toISOString()
}

function HistoryDetails({ row }: { row: AdjudicationHistoryRow }) {
  const detail = useAdjudicationHistoryDetail(row.id)
  const d = detail.data
  const scheme = useScheme()
  const pathName = (category: string, key: string) => schemePathName(scheme.data, category, key)
  if (detail.isLoading) return <div role="status" style={{ padding: 22, fontSize: 13 }}>正在读取处理详情…</div>
  if (detail.isError || !d) return <div role="alert" style={{ padding: 22 }}>
    <Empty title="处理详情读取失败" desc={detail.error instanceof ApiError ? detail.error.message : '请稍后重试。'} />
    <Btn onClick={() => void detail.refetch()}>重试</Btn>
  </div>

  return <div style={{ padding: '20px 22px', background: 'var(--sub)', display: 'flex', flexDirection: 'column', gap: 22, minWidth: 0 }}>
    {d.evidence.length > 0 && <div>
      <Sub title="佐证与附件" />
      <EvidenceFiles items={d.evidence} />
    </div>}
    <div>
      <Sub title="我的处理结论" />
      <Row label="处理时间" value={historyTime(d.createdAt)} />
      <Row label="处理类型 / 结果" value={`${HISTORY_KINDS[d.kind]} / ${decisionLabel(d)}`} />
      <Row label="原认定分" value={f.score(d.beforeScore)} />
      <Row label="当时裁定分" value={d.score === null ? '未改分' : `${f.score(d.score)} 分`} />
      {d.beforeCategory && <Row label="原归类" value={pathName(d.beforeCategory, d.beforeItemKey)} />}
      {d.category && <Row label="处理时归类" value={pathName(d.category, d.itemKey)} />}
      <div style={{ marginTop: 14 }}>
        {d.reason.trim() ? <RichText value={d.reason} evidence={d.evidence} /> : <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>这条历史记录没有保存处理理由。</span>}
      </div>
      {d.submissionId && d.score !== null && d.currentScore !== d.score && <div style={{ marginTop: 16, fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8 }}>
        该材料当前认定分为 {f.score(d.currentScore)} 分。上方保留本次处理时的结果，后续处理不会覆盖这条记录。
      </div>}
    </div>
    {d.sourceReason.trim() && <div>
      <Sub title="事项提出时的说明" />
      <RichText value={d.sourceReason} evidence={d.evidence} />
    </div>}
    {d.note.trim() && <StudentNote value={d.note} evidence={d.evidence} />}
    {d.reviews.length > 0 && <div>
      <Sub title="审核与复评意见" />
      {d.reviews.map((review, index) => <div key={`${review.stage}-${index}`} style={{ borderTop: '1px solid var(--line)', padding: '14px 0' }}>
        <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 10, marginBottom: 10, fontSize: 12.5 }}>
          <strong>{review.reviewer}</strong><span>{review.stage} · {REVIEW_DECISIONS[review.decision] ?? '已审核'} · {f.score(review.score)} 分</span>
          {review.superseded && <Pill tone="idle">旧版意见</Pill>}
          <span style={{ ...mono('11px', '0'), color: 'var(--fg3)' }}>{historyTime(review.at)}</span>
        </div>
        <RichText value={review.reason} evidence={d.evidence} />
      </div>)}
    </div>}
  </div>
}

export default function AdminHistory() {
  const [kind, setKind] = useState<'' | HistoryKind>('')
  const [student, setStudent] = useState('')
  const [query, setQuery] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [page, setPage] = useState(1)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const history = useAdjudicationHistory({ kind, student: student.trim(), q: query.trim(), from: historyDate(from), until: historyDate(to, true), page })
  const scheme = useScheme()
  const rows = history.data?.items ?? []
  const total = history.data?.total ?? 0
  const changeFilter = (change: () => void) => { change(); setPage(1); setExpandedId(null) }

  return <div style={{ animation: 'rise .28s ease both', containerType: 'inline-size' }}>
    <PageHead en="DECISION HISTORY" title="处理历史" desc="我处理过的仲裁、申诉和其他终裁。"
      side={<Btn onClick={() => useApp.getState().go('admSubs')}>仲裁与申诉</Btn>} />
    <LineTabs items={[{ key: '' as const, label: '全部' }, ...Object.entries(HISTORY_KINDS).map(([key, label]) => ({ key: key as HistoryKind, label }))]}
      value={kind} onChange={(value) => changeFilter(() => setKind(value))} />
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', padding: '18px 0' }}>
      <input aria-label="学生姓名或学号" placeholder="学生姓名 / 学号" value={student} onChange={(event) => changeFilter(() => setStudent(event.target.value))} style={{ ...fieldStyle, width: 170 }} />
      <input aria-label="条目或处理理由" placeholder="条目 / 处理理由关键词" value={query} onChange={(event) => changeFilter(() => setQuery(event.target.value))} style={{ ...fieldStyle, width: 220 }} />
      <label style={{ fontSize: 12, color: 'var(--fg3)' }}>开始日期 <input type="date" aria-label="开始日期" value={from} max={to || undefined} onChange={(event) => changeFilter(() => setFrom(event.target.value))} style={{ ...fieldStyle, width: 150 }} /></label>
      <label style={{ fontSize: 12, color: 'var(--fg3)' }}>结束日期 <input type="date" aria-label="结束日期" value={to} min={from || undefined} onChange={(event) => changeFilter(() => setTo(event.target.value))} style={{ ...fieldStyle, width: 150 }} /></label>
      {(kind || student || query || from || to) && <Btn onClick={() => changeFilter(() => { setKind(''); setStudent(''); setQuery(''); setFrom(''); setTo('') })}>清空筛选</Btn>}
    </div>

    {history.isError ? <div role="alert">
      <Empty title="处理历史读取失败" desc={history.error instanceof ApiError ? history.error.message : '请稍后重试。'} />
      <Btn onClick={() => void history.refetch()}>重试</Btn>
    </div> : history.isLoading ? <div className="load-bar" role="status" aria-label="正在读取处理历史"><span /></div> : <>
      <div style={{ padding: '0 0 12px', fontSize: 12.5, color: 'var(--fg3)' }}>共 {total} 条记录 · 按处理时间倒序</div>
      {rows.length === 0 ? <Empty title="没有匹配的处理记录" desc={kind || student || query || from || to ? '换个类型、关键词或日期范围看看。' : '你处理过的仲裁、申诉与其他终裁会显示在这里。'} /> : <Table cols="minmax(130px,1fr) minmax(190px,1.6fr) 100px 116px 100px 130px">
        <THead cells={['学生', '条目', '处理类型', '当时裁定分', '处理结果', '处理时间']} />
        {rows.map((row) => {
          const expanded = expandedId === row.id
          const detailId = `adjudication-history-${row.id}`
          const categoryName = scheme.data?.categories.find((cat) => cat.key === row.category)?.name ?? row.category
          const title = row.title === row.itemKey ? schemePathName(scheme.data, row.category, row.itemKey) : row.title
          return <Fragment key={row.id}>
            <TRow label={`${expanded ? '收起' : '展开'} ${row.student} · ${HISTORY_KINDS[row.kind]} · ${title}`}
              onClick={() => setExpandedId(expanded ? null : row.id)} expanded={expanded} controls={detailId} bg={expanded ? 'var(--sub)' : undefined}
              cells={[
                <Fragment key="student"><ChevronRight aria-hidden size={14} style={{ flex: 'none', transform: expanded ? 'rotate(90deg)' : undefined }} /><span style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 0 }}><strong style={{ color: 'var(--fg)' }}>{row.student || '原成员'}</strong><span style={mono('11px', '0')}>{row.studentId}</span></span></Fragment>,
                <span key="title" style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 0 }}><strong style={{ color: 'var(--fg)', overflowWrap: 'anywhere' }}>{title}</strong><span style={{ fontSize: 11.5, color: 'var(--fg3)' }}>{categoryName || '整表事项'}</span></span>,
                <span key="kind">{HISTORY_KINDS[row.kind]}</span>,
                <span key="score" style={mono('12px', '0')}>{row.score === null ? '未改分' : `${f.score(row.beforeScore)} → ${f.score(row.score)}`}</span>,
                <Pill key="decision" tone={row.kind === 'force_reject' || row.decision === 'dismissed' ? 'bad' : 'ok'}>{decisionLabel(row)}</Pill>,
                <span key="time" style={{ ...mono('11px', '0'), lineHeight: 1.8 }}>{historyTime(row.createdAt)}</span>,
              ]} />
            {expanded && <div id={detailId} role="row" style={{ gridColumn: '1 / -1', minWidth: 0 }}>
              <div role="cell" aria-colspan={6} style={{ maxWidth: '100cqw', position: 'sticky', left: 0 }}><HistoryDetails key={row.id} row={row} /></div>
            </div>}
          </Fragment>
        })}
      </Table>}
      {total > 0 && <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', paddingTop: 18 }}>
        <Btn disabled={page <= 1} onClick={() => { setPage((value) => value - 1); setExpandedId(null) }}>上一页</Btn>
        <span style={mono('11px', '0')}>第 {page} / {Math.max(1, Math.ceil(total / 25))} 页</span>
        <Btn disabled={page * 25 >= total} onClick={() => { setPage((value) => value + 1); setExpandedId(null) }}>下一页</Btn>
      </div>}
    </>}
  </div>
}
