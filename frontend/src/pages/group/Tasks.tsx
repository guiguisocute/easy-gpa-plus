/* 综测小组 · 待办任务。

   四组的分法直接对应 §5 的状态机与 §5.1 的匿名口径：
   我的待审（要做）/ 同行已提交（等我）/ 结论冲突（已升给管理员）/ 申诉受理（排除原审核人后派给我）。
   「同行已提交」这一组只显示有没有交，不显示交了什么——背靠背的意义就在这里。

   四组分别是四个接口调用（tab=mine|peer|conflict|appeal），不是本地过滤：
   后端按不同的 SQL 条件筛，前端拿到什么就显示什么，省得两边对状态机的理解漂移。 */

import { useState } from 'react'
import { Btn, Empty, PageHead, Pill, Seg, Stat, StatGrid, Table, THead, TRow } from '@/components/ui'
import { mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { useReviewTasks, useScheme } from '@/api/queries'
import type { AppealTask, ReportTask, ReviewTab, ReviewTask } from '@/api/types'

const TABS: { key: ReviewTab; label: string; desc: string }[] = [
  { key: 'mine', label: '我的待审', desc: '等我交结论' },
  { key: 'peer', label: '同行已提交', desc: '等另一位交' },
  { key: 'conflict', label: '结论冲突', desc: '已升给管理员仲裁' },
  { key: 'appeal', label: '申诉受理', desc: '派给我的复评' },
]

const TONE: Record<ReviewTab, 'ok' | 'warn' | 'bad' | 'idle'> = {
  mine: 'idle',
  peer: 'idle',
  conflict: 'bad',
  appeal: 'warn',
}

type ReviewListTask = ReviewTask | AppealTask | ReportTask

const isAppeal = (t: ReviewListTask): t is AppealTask => t.type === 'appeal'
/* 举报也派给同一批人，但它判的是扣分，台面在「举报复核」。 */
const isReport = (t: ReviewListTask): t is ReportTask => t.type === 'report'

export default function ReviewTasks() {
  const go = useApp((s) => s.go)
  const say = useApp((s) => s.say)
  const [tab, setTab] = useState<ReviewTab>('mine')

  const scheme = useScheme()
  /* 四个 tab 各自缓存，切换时不必重新等一次网络 */
  const mine = useReviewTasks('mine')
  const peer = useReviewTasks('peer')
  const conflict = useReviewTasks('conflict')
  const appeal = useReviewTasks('appeal')
  const byTab = { mine, peer, conflict, appeal }

  const current = byTab[tab]
  const rows = current.data?.items ?? []
  const catName = (key: string) => scheme.data?.categories.find((c) => c.key === key)?.name ?? key

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="REVIEW TASKS"
        title="待办任务"
        desc="查看分给你的材料审核、复评和举报任务。"
        side={<Btn primary onClick={() => go('revDesk')}>进入初审工作台</Btn>}
      />

      <StatGrid cols={4}>
        {TABS.map((t) => (
          <Stat
            key={t.key}
            en={t.label}
            value={String(byTab[t.key].data?.items.length ?? 0)}
            unit="条"
            note={t.desc}
            tone={t.key === 'conflict' ? 'var(--red)' : undefined}
          />
        ))}
      </StatGrid>

      <div style={{ padding: '22px 0 14px' }}>
        <Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={setTab} />
      </div>

      {current.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty title="这一组现在是空的" desc="目前没有需要处理的条目。" />
      ) : (
        <Table cols="88px minmax(160px,1.6fr) minmax(120px,1fr) 90px 96px 110px 76px">
          <THead cells={['编号', '条目', '大项', '学生期望分', '状态', '分组', '提交时间']} />
          {rows.map((t) => {
            const appealRow = isAppeal(t)
            return (
              <TRow
                key={t.id}
                label={`打开 ${t.id} · ${t.student} · ${appealRow ? t.target : t.title}`}
                onClick={() => {
                  if (isReport(t)) return go('revReports')
                  if (tab === 'mine') return go('revDesk')
                  /* 申诉有自己的台面（复评能看到第一轮的全部结论，工作台刻意看不到），
                     所以这里是跳转而不是提示。 */
                  if (tab === 'appeal') return go('revAppeals')
                  say(
                    tab === 'conflict'
                      ? '已交班级管理员仲裁，你这边只能看'
                      : '等另一名审核人交完才比对',
                  )
                }}
                cells={[
                  <span key="id" style={{ ...mono('11.5px', '.02em'), color: 'var(--fg2)' }}>#{t.id}</span>,
                  <span key="t" style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                    <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {appealRow ? t.target : t.title}
                    </span>
                    <span style={{ fontSize: 12, color: 'var(--fg3)' }}>
                      {t.student} · {appealRow ? '申诉' : `${t.reviewCount}/${t.expectedReviews} 已评`}
                    </span>
                  </span>,
                  <span key="r">{isReport(t) ? '匿名举报' : catName(t.category)}</span>,
                  <span key="w" style={{ ...num, color: 'var(--fg)', fontWeight: 600 }}>
                    {appealRow ? f.score(t.currentScore) : f.score(t.requestedScore)}
                  </span>,
                  <Pill key="p" tone={TONE[tab]}>{TABS.find((x) => x.key === tab)!.label}</Pill>,
                  <span key="g" style={{ fontSize: 12, color: 'var(--fg3)' }}>{TABS.find((x) => x.key === tab)!.desc}</span>,
                  <span key="d" style={mono('11.5px', '0')}>
                    {f.dayMonth(appealRow ? t.createdAt : t.submittedAt)}
                  </span>,
                ]}
              />
            )
          })}
        </Table>
      )}
    </div>
  )
}
