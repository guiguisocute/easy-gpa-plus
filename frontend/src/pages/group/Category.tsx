/* 综测小组 · 我的审核量。

   这一页原来叫「本大项汇总」，前提是"每人固定负责一个大项"。
   分发改成按条均衡之后（§5.3）那个前提没了：我什么大项的条目都可能拿到。

   现在它只回答三个问题，顺序就是审核人关心的顺序：
   1. 我还欠多少 —— 待我裁定、审到哪儿了；
   2. 分给我的量公不公平 —— 一个数就够，均衡是承诺，得让被分的人自己看得见；
   3. 我的口径偏不偏 —— 通过／调分／驳回的比例跟全组比，这是背靠背之下唯一能自查口径的途径。

   这里不显示"我 vs 某某人"的逐人对比：小组之间没有互相读结论的权限，
   工作量只跟全组合计比，比到人头上就变成排名了。所以后端虽然仍下发 rank，这一页不渲染它。

   页面上不画柱状图。条目基本全部已定分时三段柱就是三块等高的纯绿板砖，
   168px 的高度里能读出的只有柱顶那几个数字——那还不如把数字直接列出来。
   横条统一用 Board 那套「标签 + 计数 + 细 Bar」，全站一个形状。 */

import { useState } from 'react'
import { Bar, Btn, Empty, Note, PageHead, Stat, StatGrid, Sub } from '@/components/ui'
import { RuleSheet } from '@/components/RuleSheet'
import { num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { useReviewCategory, useScheme } from '@/api/queries'
import { DECISION_LABEL } from '@/api/types'
import type { ReviewDecision } from '@/api/types'

const DECISIONS: { key: ReviewDecision; note: string; tone?: string }[] = [
  { key: 'accepted', note: '材料与规则一致，按学生自报计分' },
  { key: 'adjusted', note: '认定分与自报不同，必须写理由' },
  { key: 'rejected', note: '计 0 分 + 理由，学生可申诉', tone: 'var(--red)' },
]

export default function ReviewWorkload() {
  const go = useApp((s) => s.go)
  const summary = useReviewCategory()
  const scheme = useScheme()
  const [rulesOpen, setRulesOpen] = useState(false)

  if (summary.isLoading) return <div className="load-bar"><span /></div>

  const load = summary.data?.load
  const rows = summary.data?.items ?? []
  const categories = scheme.data?.categories ?? []
  const catName = (key: string) => categories.find((c) => c.key === key)?.name ?? key

  if (!load || load.assigned === 0) {
    return (
      <div style={{ animation: 'rise .28s ease both' }}>
        <PageHead en="MY WORKLOAD" title="我的审核量" desc="我审了多少，和全班比一比" />
        <Empty title="还没有条目分给你" desc="新条目会出现在「待办任务」。" />
      </div>
    )
  }

  /* 待审必须跟 assigned / done 同源。这个数曾经取 /review/tasks?tab=mine 的列表长度，
     那是个混合收件箱——条目初审、成绩表、举报复核塞在同一个数组里（后端 reviewTasks
     分三段查询往 items 里 append），数出来的东西跟「累计」根本不是一类。
     load.pending 与管理员「工作量分布」读的是同一个分发池，口径天然一致。 */
  const pending = load.pending
  /* 跟人均比：正数表示我比人均多背了几条。均衡承诺兑现与否，看的就是这个数。 */
  const gap = load.assigned - load.classAverage
  const gapTone = Math.abs(gap) <= 1 ? 'var(--ok)' : Math.abs(gap) <= 3 ? 'var(--warn)' : 'var(--red)'

  const myDecisions = DECISIONS.reduce((sum, d) => sum + load.decisions[d.key], 0)
  const groupDecisions = DECISIONS.reduce((sum, d) => sum + load.groupDecisions[d.key], 0)
  const totalRows = rows.reduce((sum, r) => sum + r.total, 0)

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="MY WORKLOAD"
        title="我的审核量"
        desc="我审了多少，和全班比一比"
        /* 页眉右上不再重复"累计分配多少条"——那个数下面的 stat 里就有。
           还欠着条目时这里给一条出路，审完了就没必要催。 */
        side={pending > 0 ? <Btn primary onClick={() => go('revDesk')}>去工作台审</Btn> : undefined}
      />

      <StatGrid cols={4}>
        {/* 「条目」二字不能省：成绩表与举报复核也派给同一批人，但都不在这一页的口径里。 */}
        <Stat en="待我裁定" value={String(pending)} unit="条" note="分给我的条目里还没交结论的" tone={pending > 0 ? 'var(--warn)' : undefined} />
        <Stat en="我已评" value={String(load.done)} unit="条" note="已提交结论，含被改判的" />
        <Stat
          en="与人均之差"
          value={(gap > 0 ? '+' : '') + gap.toFixed(1)}
          unit="条"
          note={`累计分给我 ${load.assigned} 条`}
          tone={gapTone}
        />
        <Stat
          en="平均耗时"
          value={f.duration(Math.round(load.avgSpentSeconds))}
          note={`全组 ${f.duration(Math.round(load.groupAvgSpentSeconds))}`}
        />
      </StatGrid>

      {/* 均衡收成一行。原来这里是两根几乎一样长的满宽横条，画的是 103 与 102——
          为了表达"差 1 条"占掉半屏，而那个数上面的 stat 里已经有了。 */}
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 18, flexWrap: 'wrap', borderBottom: '1px solid var(--line)', padding: '18px 0' }}>
        {[
          ['我的累计', load.assigned.toFixed(0)],
          ['全班人均', load.classAverage.toFixed(1)],
          ['全班极差', String(load.spread)],
          ['参与分发', `${load.reviewerCount} 人`],
        ].map(([label, value]) => (
          <span key={label} style={{ display: 'flex', alignItems: 'baseline', gap: 7 }}>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{label}</span>
            <span style={{ fontSize: 15, fontWeight: 600, ...num }}>{value}</span>
          </span>
        ))}
        <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, maxWidth: 460, textWrap: 'pretty' }}>
          {load.spread <= 1
            ? '极差 ≤ 1 是均衡的正常状态。'
            : '差得多，通常是有人暂停分发，或者有条目被改派过——两种都记了审计，可以找班级管理员核对。'}
        </span>
      </div>

      {/* 审核人真正想知道的一件事：我比别人松还是严。背靠背之下看不到同行的结论，
          只能靠这个合计口径自查。 */}
      <div style={{ padding: '26px 0 0' }}>
        <Sub title="我判得怎么样" note={`我提交过的 ${myDecisions} 条结论 · 右边是全组加起来的数，不拆到每个人`} />
        {DECISIONS.map((d) => {
          const n = load.decisions[d.key]
          const groupN = load.groupDecisions[d.key]
          return (
            <div key={d.key} style={{ display: 'flex', flexDirection: 'column', gap: 9, borderTop: '1px solid var(--line2)', padding: '14px 0' }}>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, flexWrap: 'wrap' }}>
                <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em', color: d.tone }}>{DECISION_LABEL[d.key]}</span>
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{d.note}</span>
                <span style={{ marginLeft: 'auto', fontSize: 13, fontWeight: 600, ...num }}>
                  {n} 条 · {f.pct(n, myDecisions)}
                </span>
                <span style={{ width: 92, textAlign: 'right', flex: 'none', fontSize: 12.5, color: 'var(--fg3)', ...num }}>
                  全组 {f.pct(groupN, groupDecisions)}
                </span>
              </div>
              <Bar pct={f.pct(n, myDecisions || 1)} tone={d.tone ?? 'var(--fg)'} />
            </div>
          )
        })}

      </div>

      <div style={{ padding: '30px 0 0' }}>
        <Sub title="我审的条目按大项分布" note={`共 ${totalRows} 条，跨 ${rows.length} 个大项`} />
        {rows.map((r) => {
          const reviewing = r.total - r.scored - r.conflicts
          return (
            <div key={r.category} style={{ display: 'flex', flexDirection: 'column', gap: 9, borderTop: '1px solid var(--line2)', padding: '14px 0' }}>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, flexWrap: 'wrap' }}>
                <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em' }}>{catName(r.category)}</span>
                {/* 条长表示占比，颜色不再兼职表示"有没有冲突"——一根条同时编码两件事，
                    读的人只会记住红色而记不住它红在哪一维。冲突用文字标红。 */}
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
                  已定分 {r.scored}
                  {reviewing > 0 && ` · 在审 ${reviewing}`}
                </span>
                {r.conflicts > 0 && (
                  <span style={{ fontSize: 12.5, color: 'var(--red)' }}>冲突待裁 {r.conflicts}</span>
                )}
                <span style={{ marginLeft: 'auto', fontSize: 13, fontWeight: 600, ...num }}>{r.total} / {totalRows}</span>
              </div>
              <Bar pct={f.pct(r.total, totalRows || 1)} tone="var(--fg)" />
            </div>
          )
        })}
      </div>

      <div style={{ marginTop: 26 }}>
        <Note>
          当前方案有 {categories.reduce((sum, category) => sum + category.items.filter((item) => item.note).length, 0)} 个小项有特殊说明。照细则来，并在审核意见里写清依据。
        </Note>
      </div>

      {/* 速查表是备查资料，不是常驻的半个屏幕：裁定当下有效力的是工作台里那份冻结快照，
          这里默认收起。折叠的写法与学生「提交材料」页保持一致。 */}
      <div style={{ borderTop: '1px solid var(--line)', marginTop: 26, paddingTop: 4 }}>
        <button
          type="button"
          className="hv-fg"
          aria-expanded={rulesOpen}
          onClick={() => setRulesOpen((v) => !v)}
          style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', border: 0, background: 'none', padding: '16px 0 6px', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
        >
          <span style={{ width: 12, color: 'var(--fg3)' }}>{rulesOpen ? '▾' : '▸'}</span>
          <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em' }}>规则速查</span>
          <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
            {scheme.data ? `照已发布方案的原文列出来，和学生看到的是同一份 · ${f.schemeStamp(scheme.data)}` : '还没有已发布的方案'}
          </span>
        </button>
        {rulesOpen && scheme.data && (
          <div style={{ paddingBottom: 24 }}>
            <RuleSheet config={scheme.data} heading={false} />
          </div>
        )}
      </div>
    </div>
  )
}
