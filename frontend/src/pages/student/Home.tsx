/* 学生 · 综测总览。

   两个容易做错的地方，这里刻意做对：
   1. 基础项与扣分项没有提交入口，只读展示 + 可申诉（§4.2）；
   2. 右下角列的是"并行开放的能力"，不是串行阶段——系统里没有 phase 这个东西（§2.1）。

   当前成绩由后端复用结算规则计算；其他大项独立排名，专业与总排名等待专业成绩导齐。
   有效结算优先展示，过期快照不再充当有效排名或评优结论。 */

import { useState } from 'react'
import { ScoreRefresh } from '@/components/ScoreRefresh'
import { Bar, Btn, Empty, PageHead, Pill, Split, SplitCol, Stat, StatGrid, Sub, TextBtn } from '@/components/ui'
import { ForcedRejectionNotice } from '@/components/ForcedRejectionNotice'
import { mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { useBaseItems, useMyScore, useMyScorecard, useScheme, useSubmissions, useWindow } from '@/api/queries'
import type { BaseItem, MyScore, Submission } from '@/api/types'
import type { CategoryKey, SchemeBaseItem } from '@/lib/types'
import type { View } from '@/lib/nav'
import { automaticBaseScore, claimableBaseProgress } from '@/lib/schemeTree'
import '@/styles/mobile-home.css'

const CAP_LABEL: { key: string; name: string }[] = [
  { key: 'submit', name: '提交材料' },
  { key: 'edit', name: '修改与补交' },
  { key: 'appeal', name: '申诉' },
  { key: 'review', name: '双评审核' },
  { key: 'arbitrate', name: '仲裁与终裁' },
]

/* 位置条带：一格一人，只显示位置不显示姓名与分数。

   只分三色：你、够得着的那一段、其余。原来还单独标了"后 X%"，那是把人分成
   三六九等，而学生从中拿不到任何能改变处境的信息。

   暂时成绩只标本人名次；最终成绩的深色段在总分行和大项行上是两件不同的事：
   - 总分行画奖学金名额段。名额是各档各自四舍五入后相加的，等于结算实际发出去
     的人数，所以直接读快照给的 awardQuota，不在前端按百分比重推一遍——两种算法
     在不少班级人数下差一个人。
   - 大项行画三好线。学院要求专业素质、思想道德、实践创新、身体心理四个维度的
     名次都进前 topPercent%，所以每一行的深色段就是这一维要够到的位置。
   band() 的四舍五入和服务端 settle.quota() 是同一个口径。 */
function band(size: number, percent: number) {
  return Math.max(0, Math.round((size * percent) / 100))
}

function strip(rank: number | null, size: number, line: number, lineLabel: string) {
  return Array.from({ length: size }, (_, i) => {
    if (rank == null) return { bg: 'var(--line)', tip: '排名待定' }
    const pos = i + 1
    if (pos === rank) return { bg: 'var(--red)', tip: `你 · 第 ${pos} 名` }
    if (pos <= line) return { bg: 'var(--fg)', tip: `第 ${pos} 名 · ${lineLabel}` }
    return { bg: 'var(--line)', tip: `第 ${pos} 名` }
  })
}

/** 一行位置：分数 + 名次 + 条带。总分与四个大项用的是同一个形状，只差字号。 */
function PositionRow({
  label, score, rank, size, unit, line, lineLabel, position = rank, lead, temporary,
}: {
  label: string
  score: number | null
  rank: number | null
  size: number
  unit: string
  /** 深色段到第几名为止 */
  line: number
  /** 深色段的悬浮说明，总分行是奖学金名额、大项行是三好学生线 */
  lineLabel: string
  position?: number | null
  /** 总分那一行放大，四个大项是它的子集，缩进一档 */
  lead?: boolean
  temporary: boolean
}) {
  return (
    <div style={{ borderTop: `1px solid ${lead ? 'var(--line)' : 'var(--line2)'}`, padding: lead ? '16px 0 14px' : '13px 0 12px', paddingLeft: lead ? 0 : 16 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap', paddingBottom: 9 }}>
        <span style={{ fontSize: lead ? 13.5 : 12.5, fontWeight: 600, letterSpacing: '-.015em', width: lead ? 'auto' : 90, flex: 'none' }}>{label}</span>
        <span style={{ fontSize: lead ? 30 : 18, fontWeight: 600, letterSpacing: '-.045em', ...num }}>{score == null ? '—' : f.score(score)}</span>
        <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{unit}</span>
        <span style={{ fontSize: lead ? 13 : 12.5, color: 'var(--fg2)', fontWeight: 500 }}>{rank == null ? '排名待定' : `${temporary ? '暂列' : ''}第 ${rank} 名 / ${size} 人`}</span>
      </div>
      <div role="img" aria-label={`${label}${rank == null ? '排名待定' : `第 ${rank} 名`}，班级共 ${size} 人`} style={{ display: 'flex', gap: lead ? 4 : 3, flexWrap: 'wrap', alignItems: 'center' }}>
        {strip(position, size, line, lineLabel).map((c, i) => (
          <span key={i} title={c.tip} style={{ width: lead ? 17 : 12, height: lead ? 17 : 12, flex: 'none', background: c.bg, borderRadius: 2 }} />
        ))}
      </div>
    </div>
  )
}

/* 总览顶上的提交入口。

   一条发丝线的状态行，不是横幅：登录后本来就落在提交页，侧栏又常年挂着那个红
   按钮，这里再摆一块粉底红边的东西，等于同一件事说第三遍，还把下面四个大项的
   分数往下压了一屏。

   只放事实，不写句子——「开放中」这种每次都成立的状态不出现，出现的都是当下
   跟你有关的：草稿几条、窗口关没关。 */
function SubmitEntry({
  open, drafts, submissions, onSubmit, onList,
}: {
  open: boolean
  drafts: number
  submissions: number
  onSubmit: () => void
  onList: () => void
}) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        flexWrap: 'wrap',
        /* 只画上边那条线，下边借下面 StatGrid 自己的 borderTop——两条发丝线隔 26px
           并排，看着像画错了。 */
        borderTop: '1px solid var(--line)',
        padding: '13px 0',
      }}
    >
      <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>提交材料</span>
      {!open && <Pill tone="idle">本轮已关闭</Pill>}
      {open && drafts > 0 && <Pill tone="warn">草稿 {drafts} 条未提交</Pill>}
      <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
        <TextBtn onClick={onList}>我的提交{submissions > 0 ? ` · ${submissions} 条` : ''}</TextBtn>
        <Btn disabled={!open} onClick={onSubmit}>去提交材料</Btn>
      </div>
    </div>
  )
}

function BaseRowItem({ row, canAppeal, windowClose, resultConfirmed }: { row: BaseItem; canAppeal: boolean; windowClose?: string; resultConfirmed: boolean }) {
  const [open, setOpen] = useState(false)
  const goAppeal = useApp((s) => s.goAppeal)

  const isPenalty = row.kind === 'penalty'
  /* "申诉过"与"还能不能申诉"是两件事：
     用掉一次仍然可以再来一次，所以标签看 appealsUsed，按钮看 canAppeal。 */
  const used = row.appealsUsed
  const exhausted = row.recorded && !row.canAppeal
  const tone = used > 0 ? 'bad' : isPenalty ? 'warn' : 'ok'
  const label = used > 0 ? `已申诉 ${used} 次` : isPenalty ? (row.score < 0 ? '已扣减' : '无扣分') : row.score < (row.fullScore ?? 0) ? '有扣减' : '满分'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', background: open ? 'var(--sub)' : 'transparent' }}>
      <button
        type="button"
        className="hv-sub home-base-heading"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '12px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
      >
        <span className="home-base-kind" style={{ ...mono('11px', '.04em'), width: 32, flex: 'none', color: isPenalty ? 'var(--red)' : 'var(--fg3)' }}>
          {isPenalty ? '扣分' : '基础'}
        </span>
        <span className="home-base-title" style={{ flex: 1, minWidth: 0, fontSize: 13, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--fg)' }}>
          {row.itemName}
        </span>
        <span className="home-base-score" style={{ width: 66, flex: 'none', textAlign: 'right', fontSize: 13.5, fontWeight: 600, color: isPenalty ? 'var(--red)' : 'var(--fg)', ...num }}>
          {isPenalty ? f.delta(row.score) : f.score(row.score)}
        </span>
        <span data-r="hidesm" style={{ ...mono('11.5px', '0'), width: 56, flex: 'none', textAlign: 'right' }}>
          {row.fullScore === null ? '—' : f.score(row.fullScore)}
        </span>
        <span className="home-base-status" style={{ width: 104, flex: 'none', display: 'flex', justifyContent: 'flex-end' }}>
          <Pill tone={tone}>{label}</Pill>
        </span>
        <span className="home-base-expand" style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
      </button>

      {open && (
        <div style={{ borderTop: '1px solid var(--line2)', padding: '16px 0 22px', display: 'flex', flexDirection: 'column', gap: 14, animation: 'rise .2s ease both' }}>
          <div style={{ fontSize: 13, color: 'var(--fg2)', lineHeight: 1.85, maxWidth: 620, textWrap: 'pretty' }}>{row.basis}</div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', fontSize: 12.5, color: 'var(--fg3)' }}>
            {/* 录入人姓名后端不下发给学生——单盲在这里体现为"看得到依据，看不到人" */}
            <span>{row.recorded ? `最近更新 ${f.dateTime(row.updatedAt)}` : '尚无录入记录，按方案默认值计'}</span>
            {windowClose && (
              <>
                <span style={{ width: 1, height: 11, background: 'var(--line)' }} />
                <span>申诉窗口截止 {f.dateTime(windowClose)}</span>
              </>
            )}
          </div>

          {exhausted ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, border: '1px solid var(--line)', padding: '13px 15px', flexWrap: 'wrap' }}>
              <span style={{ width: 5, height: 5, flex: 'none', background: 'var(--red)' }} />
              <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>
                {used >= 2
                  ? '班级管理员已终裁，本条不再接受申诉'
                  : resultConfirmed
                    ? '你已经确认了当前成绩 · 这一版不再受理申诉'
                    : '这一笔在处理中 · 进度在「我的提交」看'}
              </span>
            </div>
          ) : !row.recorded ? (
            <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>这一项还没人录，暂时没什么可申诉的。</div>
          ) : (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              {/* 编辑器不在这一页开：跳到「我的申诉」并就地展开这一笔。 */}
              <button
                type="button"
                className="hv-line-red"
                disabled={!canAppeal || !row.id}
                onClick={() => row.id && goAppeal(row.kind === 'base' ? 'base_score' : 'penalty_score', row.id)}
                style={{ margin: 0, padding: '9px 18px', borderRadius: 999, font: 'inherit', fontSize: 12.5, fontWeight: 500, cursor: canAppeal && row.id ? 'pointer' : 'not-allowed', background: 'none', color: 'var(--fg2)', border: '1px solid var(--line)', opacity: canAppeal && row.id ? 1 : 0.5 }}
              >
                {used === 0 ? '对这一笔发起申诉' : '再提一次 · 直接送管理员终裁'}
              </button>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
                {!canAppeal ? '申诉能力当前未开放' : used === 0 ? '第一次由当初录这条的人复核，名字还是不公开' : `已申诉 ${used} 次 · 再提一次就由班级管理员终裁`}
              </span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function ClaimableBaseRow({
  item,
  submissions,
  submitOpen,
  onGo,
}: {
  item: SchemeBaseItem
  submissions: Submission[]
  submitOpen: boolean
  onGo: (view: View) => void
}) {
  const claim = item.studentClaim!
  const progress = claimableBaseProgress(submissions, item.full)
  const state = {
    unsubmitted: { label: '待申报', tone: 'idle' as const },
    draft: { label: progress.hasDecision ? '有新草稿' : '草稿未提交', tone: 'warn' as const },
    reviewing: { label: '审核中', tone: 'warn' as const },
    appealing: { label: '异议处理中', tone: 'bad' as const },
    scored: { label: '已认定', tone: 'ok' as const },
  }[progress.state]
  const evidence = claim.evidence?.required ? '并上传佐证' : ''

  return (
    <div style={{ borderTop: '1px solid var(--line2)', padding: '12px 0 14px' }}>
      <div className="home-base-heading home-base-heading-claim" style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <span className="home-base-kind" style={{ ...mono('11px', '.04em'), width: 56, flex: 'none', color: 'var(--red)' }}>申报基础</span>
        <span className="home-base-title" style={{ flex: 1, minWidth: 220, fontSize: 13, color: 'var(--fg)' }}>{item.name}</span>
        <span className="home-base-score" style={{ width: 66, flex: 'none', textAlign: 'right', fontSize: 13.5, fontWeight: 600, color: 'var(--fg)', ...num }}>
          {progress.hasDecision ? f.score(progress.score) : '—'}
        </span>
        <span data-r="hidesm" style={{ ...mono('11.5px', '0'), width: 56, flex: 'none', textAlign: 'right' }}>{f.score(item.full)}</span>
        <span className="home-base-status" style={{ width: 104, flex: 'none', display: 'flex', justifyContent: 'flex-end' }}><Pill tone={state.tone}>{state.label}</Pill></span>
      </div>
      <div className="home-base-claim-note" style={{ display: 'flex', alignItems: 'baseline', gap: 12, padding: '9px 0 0 68px', flexWrap: 'wrap' }}>
        <span style={{ flex: 1, minWidth: 240, fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7 }}>
          需在「提交材料」至少申报 {claim.minimum} {claim.unit}{evidence}，满分 {f.score(item.full)} 分；最终由审核人认定。
        </span>
        <TextBtn disabled={!submitOpen && submissions.length === 0} onClick={() => onGo(submissions.length > 0 ? 'stuList' : 'stuSubmit')}>
          {submissions.length > 0 ? '查看提交' : submitOpen ? '去申报' : '提交未开放'}
        </TextBtn>
      </div>
    </div>
  )
}

/** 一条扣分都没有的那些扣分项，收成一行。默认收起，点开还是原来的 BaseRowItem。 */
function CleanPenalties({ rows, canAppeal, windowClose, resultConfirmed }: { rows: BaseItem[]; canAppeal: boolean; windowClose?: string; resultConfirmed: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button
        type="button"
        className="hv-sub"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '12px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
      >
        <span style={{ ...mono('11px', '.04em'), width: 32, flex: 'none' }}>扣分</span>
        <span style={{ flex: 1, minWidth: 0, fontSize: 13, color: 'var(--fg3)' }}>
          {rows.length} 项无扣分记录
        </span>
        <span style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
      </button>
      {open && rows.map((r) => (
        <BaseRowItem key={`${r.category}-${r.kind}-${r.itemKey}`} row={r} canAppeal={canAppeal} windowClose={windowClose} resultConfirmed={resultConfirmed} />
      ))}
    </>
  )
}


export default function StudentHome() {
  const go = useApp((s) => s.go)

  const scheme = useScheme()
  const win = useWindow()
  const mine = useMyScore()
  const base = useBaseItems()
  const subs = useSubmissions()
  const rejectedSubs = useSubmissions({ forceRejected: true, page_size: 5 })
  /* 只读一眼成绩核对确认状态：待办提醒 + 申诉提示。poll:false，8 秒轮询只属于「当前成绩」页 */
  const scorecard = useMyScorecard({ poll: false })

  if (scheme.isLoading || win.isLoading || mine.isLoading) {
    return <div className="load-bar"><span /></div>
  }
  if (scheme.isError) {
    return <Empty title="还没有已发布的方案" desc="等班级管理员发布方案，这一页才会有内容。" />
  }

  const cfg = scheme.data!
  const score = mine.isError ? undefined : mine.data
  const settled: Extract<MyScore, { settled: true }> | null = score?.settled === true && !score.stale ? score : null
  // 四栏、总分和位置图始终选择同一份成绩，不能把旧结算的名次拼到暂时分数上。
  const displayedScore = settled ?? score?.current
  const totalScore = displayedScore?.totalScore
  const classRank = displayedScore?.classRank
  const classSize = displayedScore?.classSize ?? 0
  const scoreStatus = score && <Pill tone={settled ? 'ok' : 'warn'}>{settled ? '最终成绩' : '暂时成绩'}</Pill>
  const displayedConfig = settled?.configSnapshot ?? cfg
  // 评优线只属于最终成绩，沿用该次结算冻结的口径。
  const topPercent = settled?.honorTopPercent ?? 0
  const categories = displayedConfig.categories.filter((c) => (displayedConfig.weights[c.key] ?? 0) > 0)

  /* 位置图与四栏共享同一次取数。暂时成绩不预发奖学金或三好结论。 */
  const awardQuota = settled?.awardQuota ?? 0
  const honorLine = band(settled?.classSize ?? 0, topPercent)
  const positions = [
    {
      key: 'total', label: '总分', rank: classRank ?? null, score: totalScore ?? null, unit: '分',
      line: awardQuota, lineLabel: '奖学金名额',
    },
    ...categories.map((c) => ({
      key: c.key as string,
      label: c.name,
      rank: displayedScore?.categoryRanks?.[c.key] ?? null,
      score: displayedScore?.categoryScores[c.key] ?? null,
      unit: `/ ${c.maxTotal} · 权重 ${displayedConfig.weights[c.key]}`,
      line: honorLine, lineLabel: `三好学生线 · 前 ${topPercent}%`,
    })),
  ]

  const rows = subs.data?.items ?? []
  const forcedRejections = (rejectedSubs.data?.items ?? []).filter((row) => row.forceRejection)
  const forcedRejectionCount = rejectedSubs.data?.total ?? forcedRejections.length
  const drafts = rows.filter((r) => r.status === 'draft')
  const pending = rows.filter((r) => r.status === 'pending' || r.status === 'consensus')
  const appealing = rows.filter((r) => r.status === 'appealing' || r.status === 'arbitrating')
  const resultConfirmed = false
  const confirmable = scorecard.data?.state === 'confirmable'
  const todos = [
    confirmable && { title: '核对当前成绩', desc: '自愿确认，成绩变化后可再次核对', meta: '成绩', tone: 'var(--fg3)', view: 'stuResult' as View },
    drafts.length && { title: `${drafts.length} 条草稿未提交`, desc: drafts.map((d) => f.submissionTitle(d.title)).slice(0, 2).join(' · ') || '草稿不参与结算', meta: '草稿', tone: 'var(--warn)', view: 'stuSubmit' as View },
    appealing.length && { title: `${appealing.length} 条申诉或仲裁中`, desc: appealing.map((d) => d.title).slice(0, 2).join(' · '), meta: '处理中', tone: 'var(--red)', view: 'stuAppeals' as View },
    pending.length && { title: `${pending.length} 条待审核`, desc: '等两名审核人各自出结论，他们互相看不到', meta: '双评', tone: 'var(--fg3)', view: 'stuList' as View },
  ].filter(Boolean) as { title: string; desc: string; meta: string; tone: string; view: View }[]

  const caps = win.data?.capabilities
  const windowClose = win.data?.window.close
  const openAt = win.data?.window.open
  const left = f.daysUntil(windowClose)
  /* 进度条按"窗口已走过多少"算，跟倒计时是同一个事实的两种说法 */
  const span = openAt && windowClose ? new Date(windowClose).getTime() - new Date(openAt).getTime() : 0
  const gone = openAt ? Date.now() - new Date(openAt).getTime() : 0
  const progress = span > 0 ? Math.min(100, Math.max(0, Math.round((gone / span) * 100))) : 0
  const detailGroups = categories
    .map((category) => ({
      cat: category,
      rows: (base.data?.items ?? []).filter((row) => row.category === (category.key as CategoryKey)),
      claimable: category.baseItems
        .filter((item) => item.studentClaim)
        .map((item) => ({
          item,
          submissions: rows.filter((row) => row.category === category.key && row.itemKey === item.key),
        })),
    }))
    .filter((group) => group.rows.length > 0 || group.claimable.length > 0)

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="MY SCORE"
        title="我的综测总览"
        desc="看各项得分、待办事项和班级排名。"
        side={<>
          {totalScore != null && <span style={{ fontSize: 13, color: 'var(--fg2)', ...num }}>
            {settled ? '最终总分' : '暂时总分'} <strong>{f.score(totalScore)}</strong>
            {classRank != null && ` · ${settled ? '班级' : '暂列'}第 ${classRank} 名 / ${classSize} 人`}
          </span>}
          {score?.settled && score.stale && <Pill tone="warn">待重新结算</Pill>}
          {settled?.awardTier && <Pill tone="ok">{settled.awardTier} · 候选</Pill>}
          {settled?.honor && <Pill tone="ok">三好候选</Pill>}
          <ScoreRefresh fetching={mine.isFetching} onRefresh={() => void mine.refetch()} />
        </>}
      />

      {mine.isError && <div role="alert" style={{ color: 'var(--red)', fontSize: 13, marginBottom: 26 }}>
        成绩读取失败，请重试。
      </div>}

      {(subs.data?.items ?? []).some((row) => row.forcedScore && !row.forceRejection) && <section aria-label="材料强制改分提醒" style={{ display: 'flex', flexDirection: 'column', gap: 12, marginBottom: 26 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', color: 'var(--red)' }}><strong style={{ fontSize: 16 }}>近期材料已强制改分，请查看新分数与理由</strong><TextBtn tone="var(--red)" onClick={() => go('stuList')}>查看我的提交 →</TextBtn></div>
        {(subs.data?.items ?? []).filter((row) => row.forcedScore && !row.forceRejection).slice(0, 5).map((row) => <ForcedRejectionNotice key={row.id} rejection={row.forcedScore!} title={row.title} />)}
      </section>}
      {forcedRejectionCount > 0 && <section aria-label="材料强制驳回提醒" style={{ display: 'flex', flexDirection: 'column', gap: 12, marginBottom: 26 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', color: 'var(--red)' }}>
          <strong style={{ fontSize: 16 }}>{forcedRejectionCount} 条材料被强制驳回，请查看理由</strong>
          <TextBtn tone="var(--red)" onClick={() => { const url = new URL(window.location.href); url.searchParams.set('force_rejected', 'true'); window.history.replaceState(null, '', url); go('stuList') }}>查看全部驳回材料 →</TextBtn>
        </div>
        {forcedRejections.map((row) => <ForcedRejectionNotice key={row.id} rejection={row.forceRejection!} title={row.title} />)}
        {forcedRejectionCount > forcedRejections.length && <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>这里只展示最近 {forcedRejections.length} 条，请在「我的提交 · 强制驳回」查看全部 {forcedRejectionCount} 条。</span>}
      </section>}
      {rejectedSubs.isError && <div role="alert" style={{ color: 'var(--red)', fontSize: 13, marginBottom: 20 }}>驳回通知读取失败。<TextBtn tone="var(--red)" onClick={() => void rejectedSubs.refetch()}>重新读取</TextBtn></div>}

      {/* 紧跟在四个大项上面：它和 StatGrid 共用那一条发丝线，读起来是这组数据的抬头，
          而不是又一块横幅。 */}
      <SubmitEntry
        open={!!caps?.submit}
        drafts={drafts.length}
        submissions={rows.length}
        onSubmit={() => go('stuSubmit')}
        onList={() => go('stuList')}
      />

      <section aria-label="分项成绩" style={{ paddingTop: 14 }}>
        <Sub title="分项成绩" actions={scoreStatus} />
        <StatGrid cols={4}>
          {categories.map((c) => {
            const got = displayedScore?.categoryScores[c.key]
            const rank = c.key === 'major' ? displayedScore?.majorRank : displayedScore?.categoryRanks?.[c.key]
            const baseScore = automaticBaseScore(c)
            return (
              <Stat
                key={c.key}
                en={c.name}
                value={got == null ? '—' : f.score(got)}
                unit={`× ${displayedConfig.weights[c.key]}`}
                note={rank != null
                  ? `${settled ? '' : '暂列'}第 ${rank} 名 / ${classSize} 人`
                  : c.key === 'major' ? got == null ? '教务系统导入' : '排名待定'
                    : baseScore > 0 ? `基础分 ${f.score(baseScore)}，满分 ${c.maxTotal}` : `满分 ${c.maxTotal}`}
                pct={got == null ? undefined : f.pct(got, c.maxTotal)}
              />
            )
          })}
        </StatGrid>
      </section>

      {score && (
        /* 总分一行，四个大项作为它的子集依次排在下面，形状相同、缩进一档。
           原来这里是一排胶囊，一次只看得到一条 —— 想知道自己哪个大项拖了后腿，
           得把四个挨个点一遍再靠记忆比较。五条并排就直接看出来了。 */
        <section aria-label="我在班级中的位置" style={{ padding: '26px 0 0' }}>
          <Sub title="我在班级中的位置" actions={scoreStatus} />
          {positions.map((p) => (
            <PositionRow
              key={p.key}
              label={p.label}
              score={p.score}
              rank={p.rank}
              size={classSize}
              unit={p.unit}
              line={p.line}
              lineLabel={p.lineLabel}
              position={p.key === 'total' && settled ? settled.awardPosition : p.rank}
              lead={p.key === 'total'}
              temporary={!settled}
            />
          ))}
          <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', borderTop: '1px solid var(--line2)', paddingTop: 12 }}>
            {[
              ...(positions.some((p) => p.rank != null) ? [['var(--red)', settled ? '你的名次' : '你的暂列名次']] : []),
              ...(classRank == null ? [['var(--line)', '专业与总排名待导齐']] : []),
              ...(settled ? [
                ['var(--fg)', '奖学金名额（总分）'],
                ['var(--fg)', '三好学生线（各大项）'],
              ] : []),
            ].map(([bg, label]) => (
              <span key={label} style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: 'var(--fg3)' }}>
                <span style={{ width: 9, height: 9, borderRadius: 2, background: bg }} />
                {label}
              </span>
            ))}
          </div>
        </section>
      )}

      <div style={{ padding: '34px 0 0' }}>
        <Split cols="1.35fr 1fr">
          <SplitCol first>
            <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 14 }}>当前待办</div>
            {todos.length === 0 ? (
              <Empty title="没有待办" desc="所有条目都定分了，也没有还在走的申诉。" />
            ) : (
              todos.map((t) => (
                <button
                  key={t.title}
                  type="button"
                  className="hv-sub"
                  onClick={() => go(t.view)}
                  style={{ display: 'flex', alignItems: 'center', gap: 14, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '15px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
                >
                  <span style={{ width: 6, height: 6, flex: 'none', background: t.tone }} />
                  <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0, flex: 1 }}>
                    <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em', color: 'var(--fg)' }}>{t.title}</span>
                    <span style={{ fontSize: 12.5, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{t.desc}</span>
                  </span>
                  <span style={{ ...mono('11.5px', '0'), flex: 'none' }}>{t.meta}</span>
                </button>
              ))
            )}
          </SplitCol>
          <SplitCol>
            <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 14 }}>本轮开放窗口 · 并行进行</div>
            {CAP_LABEL.map((c) => {
              const on = !!caps?.[c.key as keyof typeof caps]
              return (
                <div key={c.key} style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0' }}>
                  <span style={{ width: 5, height: 5, flex: 'none', background: on ? 'var(--red)' : 'var(--line)' }} />
                  <span style={{ fontSize: 12.5, color: on ? 'var(--fg)' : 'var(--fg3)', fontWeight: on ? 600 : 400 }}>{c.name}</span>
                  <span style={{ ...mono('11.5px', '0'), marginLeft: 'auto', whiteSpace: 'nowrap' }}>{on ? '开放中' : '已关闭'}</span>
                </div>
              )
            })}
            <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 16, textWrap: 'pretty' }}>
              开放窗口内可提交、修改与申诉。完成后请至「当前成绩」封存并确认。
            </div>
            <div style={{ marginTop: 16 }}>
              <Bar pct={`${progress}%`} />
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginTop: 8, flexWrap: 'wrap' }}>
                <span style={mono('11.5px', '0')}>
                  {f.dateTime(openAt)} — {f.dateTime(windowClose)}
                </span>
                <span style={{ fontSize: 12.5, color: left <= 3 ? 'var(--red)' : 'var(--fg3)' }}>剩 {left} 天</span>
                <TextBtn onClick={() => go('stuResult')}>去我的成绩表</TextBtn>
              </div>
            </div>
          </SplitCol>
        </Split>
      </div>
      <div style={{ padding: '34px 0 0' }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, paddingBottom: 12, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>基础分与扣分明细</span>
          <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)', maxWidth: 420, textAlign: 'right', textWrap: 'pretty' }}>
            基础分和扣分由班级审核。要你自己报的，会写明缺什么材料。
          </span>
        </div>
        {base.isLoading || subs.isLoading ? (
          <div className="load-bar"><span /></div>
        ) : base.isError || subs.isError ? (
          <Empty title="明细读取失败" desc="刷新一下再试。" />
        ) : detailGroups.length === 0 ? (
          <Empty title="方案里没有基础项与扣分项" desc="当前方案没配这两类小项。" />
        ) : (
          detailGroups
            .map((g) => {
              const total = g.rows.reduce((sum, row) => sum + row.score, 0)
                + g.claimable.reduce((sum, row) => sum + claimableBaseProgress(row.submissions, row.item.full).score, 0)
              /* 一个没被扣过分的人，方案里十几条扣分项会铺成一整屏「0.0 — 无扣分」，
                 把真正有内容的基础项挤到看不见。没扣就收起来，只留一行说明和一个开关——
                 收起不等于藏起来：想核对"我到底有没有被扣"的人点一下就能看全。 */
              const clean = g.rows.filter((r) => r.kind === 'penalty' && r.score === 0 && r.appealsUsed === 0)
              const shown = g.rows.filter((r) => !clean.includes(r))
              return (
                <div key={g.cat.key} style={{ display: 'flex', flexDirection: 'column', paddingBottom: 16 }}>
                  <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, borderTop: '1px solid var(--line)', padding: '13px 0 3px' }}>
                    <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em' }}>{g.cat.name}</span>
                    <span style={{ ...mono('11.5px', '0'), marginLeft: 'auto' }}>合计 {f.score(total)}</span>
                  </div>
                  {shown.map((r) => (
                    <BaseRowItem key={`${r.category}-${r.kind}-${r.itemKey}`} row={r} canAppeal={!!caps?.appeal} windowClose={windowClose} resultConfirmed={resultConfirmed} />
                  ))}
                  {g.claimable.map((row) => (
                    <ClaimableBaseRow
                      key={row.item.key}
                      item={row.item}
                      submissions={row.submissions}
                      submitOpen={!!caps?.submit}
                      onGo={go}
                    />
                  ))}
                  {clean.length > 0 && (
                    <CleanPenalties rows={clean} canAppeal={!!caps?.appeal} windowClose={windowClose} resultConfirmed={resultConfirmed} />
                  )}
                </div>
              )
            })
        )}
      </div>
    </div>
  )
}
