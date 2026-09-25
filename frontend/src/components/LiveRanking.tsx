import { useState } from 'react'
import type { AdminStats } from '@/api/types'
import type { SchemeConfig } from '@/lib/types'
import { Btn, Empty, Note } from '@/components/ui'
import { ScoreRefresh } from '@/components/ScoreRefresh'
import { orderedRanking, rankingPlace, rankingScore, rankingSummary } from '@/lib/liveRanking'
import * as f from '@/lib/format'
import '@/styles/live-ranking.css'


export function LiveRanking({ ranking, categories, refreshing, failed, onRefresh }: {
  ranking: AdminStats['ranking']; categories: SchemeConfig['categories']; refreshing: boolean; failed: boolean; onRefresh: () => void
}) {
  const [metric, setMetric] = useState('total')
  const [search, setSearch] = useState('')
  const [all, setAll] = useState(false)
  const [table, setTable] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const metrics = [{ key: 'total', name: '综合素质' }, ...categories.map(({ key, name }) => ({ key, name }))]
  const active = metrics.find((entry) => entry.key === metric) ?? metrics[0]
  const rows = ranking?.items ?? []
  const ordered = orderedRanking(rows, active.key, search)
  const visible = all || search.trim() ? ordered : ordered.slice(0, 15)
  const summary = rankingSummary(rows, active.key)
  const person = rows.find((row) => row.sid === selected)
  const ready = active.key !== 'total' && active.key !== 'major' || ranking?.rankingReady === true
  const minimum = Math.min(0, summary.min ?? 0)
  const maximum = Math.max(1, summary.max ?? 1)
  const span = maximum - minimum
  const zero = -minimum / span * 100
  const color = 'var(--red)'
  const formatScore = (value: number | null | undefined) => value == null ? '—' : f.score(value)

  return <div className="live-ranking" id="admin-live-ranking">
    <div className="ranking-toolbar">
      <span className="ranking-update"><i aria-hidden="true" />{failed ? '更新失败，以下为上次读取结果' : refreshing ? '正在更新成绩…' : '当前认定成绩'}{ranking?.calculatedAt && ` · ${f.dateTime(ranking.calculatedAt)}`}<span>按需刷新成绩</span></span>
      <ScoreRefresh fetching={refreshing} onRefresh={onRefresh} label="刷新排名" />
    </div>
    {failed && <Note tone="bad">成绩更新失败，请重试。上次结果可能已有变化。</Note>}
    <div className="ranking-metrics" aria-label="选择排名大项">
      {metrics.map((entry) => {
        const stats = rankingSummary(rows, entry.key)
        const values = orderedRanking(rows, entry.key).slice(0, 16)
        const max = Math.max(1, stats.max ?? 1)
        return <button type="button" key={entry.key} aria-pressed={active.key === entry.key} className="ranking-metric" onClick={() => { setMetric(entry.key); setAll(false) }}>
          <span className="ranking-metric-name">{entry.name}<span>↗</span></span>
          <strong>{formatScore(stats.average)}<small>平均分</small></strong>
          <span className="ranking-spark" aria-hidden="true">{values.map((row) => <i key={row.sid} style={{ height: `${Math.max(3, Math.max(0, rankingScore(row, entry.key) ?? 0) / max * 100)}%` }} />)}</span>
          <span className="ranking-metric-foot">最高 {formatScore(stats.max)} · {stats.count} 人有成绩</span>
        </button>
      })}
    </div>
    {!ranking?.rankingReady && <Note>专业素质分已导入 {ranking?.gpaImported ?? 0} / {ranking?.classSize ?? rows.length} 人。专业与综合名次待全班导齐；其余大项正常排名。已有分数仍展示，缺失成绩显示“—”。</Note>}
    <div className="ranking-chart-head">
      <div><h3>{active.name} · {ready ? '实时排名' : '当前分数'}</h3><p>{ready ? '同分并列，名次随认定结果更新。' : '暂按已有分数排列，尚不产生名次。'}分项为原始分，综合分按方案权重计算。</p></div>
      <div className="ranking-controls"><input aria-label="搜索排名学生" placeholder="搜索姓名 / 学号" value={search} onChange={(event) => setSearch(event.target.value)} /><Btn onClick={() => setTable((value) => !value)}>{table ? '查看排名图' : '查看分数表'}</Btn></div>
    </div>
    {rows.length === 0 ? <Empty title="暂无班级成绩" desc="有效名单成员的当前成绩会显示在这里。" /> : ordered.length === 0 ? <Empty title="没有找到该成员" desc="试试姓名或完整学号。" /> : table ? <div className="ranking-table-wrap"><table className="ranking-table">
      <caption>全班当前成绩 · 按{active.name}排列</caption>
      <thead><tr><th>学生</th>{metrics.map((entry) => <th key={entry.key}>{entry.name}<small>分数 / 名次</small></th>)}<th>待定条目</th></tr></thead>
      <tbody>{ordered.map((row) => <tr key={row.sid}><th>{row.name}<small>{row.sid}</small></th>{metrics.map((entry) => <td key={entry.key}>{formatScore(rankingScore(row, entry.key))}<small>{rankingPlace(row, entry.key) == null ? '排名待定' : `第 ${rankingPlace(row, entry.key)} 名`}</small></td>)}<td>{row.pending}</td></tr>)}</tbody>
    </table></div> : <>
      <div className="ranking-axis" aria-hidden="true"><span>{formatScore(minimum)}</span><span>分数 · {formatScore(maximum)}</span></div>
      <ol className="ranking-bars" aria-label={`${active.name}分数排名图`}>
        {visible.map((row) => {
          const score = rankingScore(row, active.key)
          const rank = rankingPlace(row, active.key)
          return <li key={row.sid}><button type="button" className="ranking-bar-row" aria-pressed={selected === row.sid} aria-label={`${row.name}，${score == null ? '成绩未导入' : `${formatScore(score)} 分`}，${rank == null ? '排名待定' : `第 ${rank} 名`}，查看各项成绩`} onClick={() => setSelected(selected === row.sid ? null : row.sid)}>
            <span className="ranking-place">{rank == null ? '—' : String(rank).padStart(2, '0')}</span>
            <span className="ranking-person"><strong>{row.name}</strong><small>{row.sid}</small></span>
            <span className="ranking-track"><i className="ranking-zero" style={{ left: `${zero}%` }} /><i className="ranking-fill" style={{ left: `${score != null && score < 0 ? zero + score / span * 100 : zero}%`, width: `${score == null ? 0 : Math.abs(score) / span * 100}%`, background: color }} />{score == null && <small>尚未导入</small>}</span>
            <strong className="ranking-value">{formatScore(score)}</strong>
          </button></li>
        })}
      </ol>
      {!search.trim() && ordered.length > 15 && <div className="ranking-expand"><Btn onClick={() => setAll((value) => !value)}>{all ? '收起至前 15 人' : `查看全班 ${ordered.length} 人`}</Btn><span>已显示 {visible.length} / {ordered.length} 人</span></div>}
    </>}
    {person && !table && <section className="ranking-person-detail" aria-label={`${person.name}各项成绩`}>
      <div className="ranking-toolbar"><strong>{person.name} · 各项成绩</strong><Btn onClick={() => setSelected(null)}>收起</Btn></div>
      <div className="ranking-person-scores">{metrics.map((entry) => <div key={entry.key}><span>{entry.name}</span><strong>{formatScore(rankingScore(person, entry.key))}</strong><small>{rankingPlace(person, entry.key) == null ? '排名待定' : `第 ${rankingPlace(person, entry.key)} 名`}</small></div>)}</div>
      <p>已定分 {person.scored} 条 · 待定 {person.pending} 条 · 待终裁 {person.conflicts} 条</p>
    </section>}
    <p className="ranking-footnote">共 {ranking?.classSize ?? rows.length} 名有效成员，包含未注册成员。待审自报分不计入，申诉中沿用已有认定分；评优结果以正式结算为准。</p>
  </div>
}
