/* 班级管理员 · 分发。

   策略是**按条均衡**（balanced_per_submission）：分配单位是一条提交，不是一个大项。
   算法逐条挑当前累计工作量最少的两个人，目标是所有审核人背的条数尽量相等。

   为什么取消大项限制：固定二人是想让审核人在一个大项里形成稳定口径，
   但代价是各大项的条目数天差地别（"社会实践"三条、"竞赛获奖"两百条），
   拿到大项的人之间工作量能差一个数量级。口径可以靠细则与规则快照统一，
   工作量差不能——它只会让人不想干。

   三件事仍然不变（§5.3）：
   1. **随机种子必须记录并显示** —— 均衡算法在"谁少谁先拿"打平时用种子决定，
      事后拿同一个种子重跑一遍，结果必须逐条相同；
   2. avoid_self —— 没人会审到自己的材料；
   3. 人工改派要填理由，写 dispatch.reassigned 审计。

   这一页只盯一个数：**累计分配量的极差**。极差 0 或 1 就是均衡，别的都是有人在多干活。 */

import { useState } from 'react'
import { Bar, Btn, Empty, Note, PageHead, Pill, Row, Split, SplitCol, Stat, StatGrid, Sub, Table, TextBtn, THead, TRow, Toggle } from '@/components/ui'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useDispatch, useDispatchActions, useDispatchQueue, useScheme } from '@/api/queries'
import type { DispatchPlan, DispatchReviewer } from '@/api/types'

/** 极差到人话。这是全页唯一的健康指标，所以口径写死在一个地方。 */
function spreadTone(spread: number): { tone: string; text: string } {
  if (spread <= 1) return { tone: 'var(--ok)', text: '均衡' }
  if (spread <= 3) return { tone: 'var(--warn)', text: '略有偏差' }
  return { tone: 'var(--red)', text: '不均，建议重排' }
}

/* ---- 某个审核人的待审队列 ---- */

function ReviewerQueue({ reviewer, pool }: { reviewer: DispatchReviewer; pool: DispatchReviewer[] }) {
  const say = useApp((s) => s.say)
  const queue = useDispatchQueue(reviewer.userId)
  const { reassign, handover } = useDispatchActions()
  const [target, setTarget] = useState('')
  const [reason, setReason] = useState('')
  const [row, setRow] = useState<string | null>(null)

  const others = pool.filter((r) => r.userId !== reviewer.userId && !r.paused)
  const rows = queue.data?.items ?? []
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')

  return (
    <div style={{ border: '1px solid var(--line)', padding: '18px 20px', marginTop: 14, display: 'flex', flexDirection: 'column', gap: 16, animation: 'rise .2s ease both' }}>
      <Sub
        title={`${reviewer.name} 的待审条目 ${rows.length} 条`}
        note="仅列出尚未提交结论的条目。已提交结论的不可改派"
        actions={
          <>
            <select
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              style={{ ...fieldStyle, width: 'auto', border: '1px solid var(--line)', padding: '7px 10px', fontSize: 12.5 }}
            >
              <option value="">整体转出给…（留空 = 按均衡重新摊）</option>
              {others.map((r) => (
                <option key={r.userId} value={r.userId}>
                  {r.name}（当前 {r.assigned} 条）
                </option>
              ))}
            </select>
            <Btn
              danger
              disabled={rows.length === 0 || reason.trim().length < 4 || handover.isPending}
              onClick={() =>
                handover.mutate(
                  { from: reviewer.userId, to: target || undefined, reason: reason.trim() },
                  {
                    onSuccess: (r) => {
                      setReason('')
                      setTarget('')
                      say(`已转出 ${r.moved} 条 · 写入 dispatch.reassigned 审计`)
                    },
                    onError: fail,
                  },
                )
              }
            >
              {handover.isPending ? '转出中…' : '全部转出'}
            </Btn>
          </>
        }
      />

      <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>改派理由 · 至少 4 个字</span>
        <input
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="比如：本人退出综测小组，剩下的条目转给别人"
          style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--line)', padding: '9px 12px', color: 'var(--fg)', font: 'inherit', fontSize: 13 }}
        />
      </label>

      {queue.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>该审核人暂无待审条目。</div>
      ) : (
        <Table cols="minmax(150px,1.6fr) 96px 110px 96px 110px">
          <THead cells={['条目', '学生', '另一名审核人', '提交时间', '操作']} />
          {rows.map((q) => (
            <TRow
              key={q.submissionId}
              cells={[
                <span key="a" style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                  <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{q.title}</span>
                  <span style={mono('11px', '.02em')}>#{q.submissionId}</span>
                </span>,
                <span key="b">{q.student}</span>,
                <span key="c" style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
                  {q.peer ?? '待分配'}{q.peerDecided ? ' · 已交' : ''}
                </span>,
                <span key="d" style={mono('11.5px', '0')}>{f.dayMonth(q.submittedAt)}</span>,
                <span key="e" style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                  {row === q.submissionId ? (
                    <>
                      <select
                        value={target}
                        onChange={(e) => setTarget(e.target.value)}
                        style={{ ...fieldStyle, width: 'auto', border: '1px solid var(--line)', padding: '5px 8px', fontSize: 12 }}
                      >
                        <option value="">换成…</option>
                        {others.map((r) => (
                          <option key={r.userId} value={r.userId}>
                            {r.name}（{r.assigned}）
                          </option>
                        ))}
                      </select>
                      <TextBtn
                        disabled={!target || reason.trim().length < 4 || reassign.isPending}
                        onClick={() =>
                          reassign.mutate(
                            { submissionId: q.submissionId, from: reviewer.userId, to: target, reason: reason.trim() },
                            {
                              onSuccess: () => {
                                setRow(null)
                                setTarget('')
                                say('已改派 · 写入 dispatch.reassigned 审计')
                              },
                              onError: fail,
                            },
                          )
                        }
                      >
                        确认
                      </TextBtn>
                    </>
                  ) : (
                    <TextBtn onClick={() => { setRow(q.submissionId); setTarget('') }}>换人</TextBtn>
                  )}
                </span>,
              ]}
            />
          ))}
        </Table>
      )}
    </div>
  )
}

/* ---- 试算结果 ---- */

function PlanPanel({ plan, onClear }: { plan: DispatchPlan; onClear: () => void }) {
  const max = Math.max(1, ...plan.rows.map((r) => r.after))
  const health = spreadTone(plan.spreadAfter)

  return (
    <div style={{ border: '1px solid var(--warn)', padding: '18px 20px', marginTop: 18, display: 'flex', flexDirection: 'column', gap: 16 }}>
      <Sub
        title={`试算结果 · ${plan.targets} 条待分配`}
        note={`执行后极差 ${plan.spreadBefore} → ${plan.spreadAfter}（${health.text}）`}
        actions={<TextBtn onClick={onClear}>丢弃试算</TextBtn>}
      />
      {plan.rows.map((r) => (
        <div key={r.reviewerId} style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <span style={{ width: 84, flex: 'none', fontSize: 12.5, color: 'var(--fg)' }}>{r.name}</span>
          <span style={{ flex: 1, minWidth: 120 }}>
            <Bar pct={`${(r.after / max) * 100}%`} tone={r.delta > 0 ? 'var(--red)' : 'var(--line)'} h={6} />
          </span>
          <span style={{ width: 108, flex: 'none', textAlign: 'right', fontSize: 12.5, color: 'var(--fg3)', ...num }}>
            {r.before} → <span style={{ color: 'var(--fg)', fontWeight: 600 }}>{r.after}</span>
            {r.delta > 0 && <span style={{ color: 'var(--red)' }}> +{r.delta}</span>}
          </span>
        </div>
      ))}

      {plan.blocked.length > 0 && (
        <div style={{ borderTop: '1px solid var(--line2)', paddingTop: 12 }}>
          <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--red)', paddingBottom: 8 }}>
            {plan.blocked.length} 条分不出去
          </div>
          {plan.blocked.map((b) => (
            <div key={b.submissionId} style={{ display: 'flex', gap: 10, fontSize: 12.5, color: 'var(--fg3)', padding: '4px 0', flexWrap: 'wrap' }}>
              <span style={{ color: 'var(--fg2)' }}>{b.student} · {b.title}</span>
              <span style={{ marginLeft: 'auto' }}>{b.reason}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/* ---- 页面 ---- */

function SubmissionDispatch() {
  const say = useApp((s) => s.say)
  const scheme = useScheme()
  const state = useDispatch()
  const { preview, run, setAuto, setPaused } = useDispatchActions()

  const [plan, setPlan] = useState<DispatchPlan | null>(null)
  const [redo, setRedo] = useState(false)
	const [seed, setSeed] = useState('')
  const [openReviewer, setOpenReviewer] = useState<string | null>(null)

  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')

  if (scheme.isError) return <Empty title="还没有已发布的方案" desc="发布方案之后才能分发审核任务。" />
  if (scheme.isLoading || state.isLoading) return <div className="load-bar"><span /></div>

  const d = state.data
  const pool = d?.reviewers ?? []
  const activePool = pool.filter((r) => !r.paused)
  const notEnough = activePool.length < 3
  const health = spreadTone(d?.spread ?? 0)
  const maxAssigned = Math.max(1, ...pool.map((r) => r.assigned))
	const opts = { includeAssigned: redo, ...(seed.trim() ? { seed: seed.trim() } : {}) }
	const seedInvalid = !!seed && !/^\d{1,18}$/.test(seed)

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <Sub
        title="初审任务分配"
        note="为申报条目分配两名审核人，并均衡各人任务量。"
        actions={
          <>
            <Btn
			  disabled={notEnough || seedInvalid || preview.isPending}
              onClick={() =>
                preview.mutate(opts, {
                  onSuccess: (p) => {
                    setPlan(p)
                    say(p.targets === 0 ? '没有需要分配的条目' : '试算完成 · 确认后开始分发')
                  },
                  onError: fail,
                })
              }
            >
              {preview.isPending ? '试算中…' : '试算'}
            </Btn>
            <Btn
              primary
			  disabled={notEnough || seedInvalid || run.isPending}
              onClick={() =>
                run.mutate(opts, {
                  onSuccess: (r) => {
                    setPlan(null)
                    say(`分发完成 · 已分配 ${r.assigned} 条 · 当前极差 ${r.spreadAfter}`)
                  },
                  onError: fail,
                })
              }
            >
              {run.isPending ? '分发中…' : redo ? '重排全部未开评条目' : '分发未分配条目'}
            </Btn>
          </>
        }
      />

      {notEnough && (
        <div style={{ border: '1px solid var(--red)', background: 'var(--redBg)', padding: '13px 16px', marginBottom: 18, fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75 }}>
          当前审核人 {activePool.length} 名。每条材料须双人审核，且须回避申报人本人。请先在「班级成员」中增补综测小组成员。
        </div>
      )}

      <StatGrid cols={4}>
        <Stat
          en="累计极差"
          value={String(d?.spread ?? 0)}
          unit="条"
          note={`最多和最少的人差 ${d?.spread ?? 0} 条 · ${health.text}`}
          tone={health.tone}
        />
        <Stat en="待分配" value={String(d?.unassigned ?? 0)} unit="条" note="还没派审核人" tone={(d?.unassigned ?? 0) > 0 ? 'var(--warn)' : undefined} />
        <Stat en="参与分发" value={String(activePool.length)} unit="人" note={`共 ${pool.length} 名审核人，${pool.length - activePool.length} 名已暂停`} tone={notEnough ? 'var(--red)' : undefined} />
        <Stat en="全班待审" value={String(d?.pendingTotal ?? 0)} unit="条" note={`累计已分配 ${d?.assignedTotal ?? 0} 条`} />
      </StatGrid>

      <div style={{ display: 'flex', alignItems: 'center', gap: 16, borderBottom: '1px solid var(--line)', padding: '18px 0', flexWrap: 'wrap' }}>
        <Toggle
          on={!!d?.auto}
          size="sm"
          onClick={() =>
            setAuto.mutate(!d?.auto, {
              onSuccess: (r) => say(r.auto ? '自动分发已开 · 学生一提交就分配' : '自动分发已关 · 新提交的会堆在「待分配」里，等你手动分'),
              onError: fail,
            })
          }
        />
        <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
          <span style={{ fontSize: 13, fontWeight: 600 }}>自动分发</span>
          <span style={{ fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>
            打开之后，学生新提交的材料会自动分给审核人。
          </span>
        </span>
        <label style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 9, fontSize: 12.5, color: 'var(--fg2)', cursor: 'pointer' }}>
		  <span>复现种子（可选）</span><input value={seed} onChange={(e) => setSeed(e.target.value)} placeholder="留空自动生成" inputMode="numeric" style={{ ...fieldStyle, width: 150, padding: '7px 9px', borderColor: seedInvalid ? 'var(--red)' : undefined }} />
		</label>
		<label style={{ display: 'flex', alignItems: 'center', gap: 9, fontSize: 12.5, color: 'var(--fg2)', cursor: 'pointer' }}>
          <input
            type="checkbox"
            checked={redo}
            onChange={(e) => setRedo(e.target.checked)}
            style={{ width: 15, height: 15, accentColor: 'var(--red)', cursor: 'pointer' }}
          />
          已分配、还没开评的也一起重排
        </label>
      </div>

      {plan && <PlanPanel plan={plan} onClear={() => setPlan(null)} />}

      <div style={{ padding: '26px 0 0' }}>
        <Sub title="工作量分布" note="展开可改派，或转出全部待审" />
        {pool.length === 0 ? (
          <Empty title="还没有可用审核人" desc="请在「班级成员」中将学生设为综测小组。" />
        ) : (
          pool.map((r) => {
            const on = openReviewer === r.userId
            return (
              <div key={r.userId} style={{ background: on ? 'var(--sub)' : 'transparent' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0', flexWrap: 'wrap' }}>
                  <button
                    type="button"
                    className="hv-fg"
                    onClick={() => setOpenReviewer(on ? null : r.userId)}
                    style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'none', border: 0, margin: 0, padding: 0, font: 'inherit', cursor: 'pointer', textAlign: 'left', minWidth: 150 }}
                  >
                    <span style={{ fontSize: 13, fontWeight: 500, color: 'var(--fg)' }}>{r.name}</span>
                    <span style={mono('11px', '.02em')}>{r.sid}</span>
                    {r.role === 'class_admin' && <Pill tone="idle">管理员</Pill>}
                    {r.paused && <Pill tone="warn">已暂停</Pill>}
                  </button>

                  <span style={{ flex: 1, minWidth: 140 }}>
                    <Bar pct={`${(r.assigned / maxAssigned) * 100}%`} tone={r.paused ? 'var(--line)' : 'var(--red)'} h={6} />
                  </span>

                  <span style={{ flex: 'none', display: 'flex', gap: 14, fontSize: 12.5, color: 'var(--fg3)', ...num }}>
                    <span>累计 <span style={{ color: 'var(--fg)', fontWeight: 600 }}>{r.assigned}</span></span>
                    <span>待审 <span style={{ color: r.pending ? 'var(--warn)' : 'var(--fg3)' }}>{r.pending}</span></span>
                    <span>已评 {r.done}</span>
                  </span>

                  <TextBtn
                    onClick={() =>
                      setPaused.mutate(
                        { uid: r.userId, paused: !r.paused },
                        {
                          onSuccess: () => say(r.paused ? `${r.name} 已恢复参与分发` : `${r.name} 已暂停 · 不再分新条目，手上的还得处理完`),
                          onError: fail,
                        },
                      )
                    }
                  >
                    {r.paused ? '恢复' : '暂停'}
                  </TextBtn>
                </div>
                {on && <ReviewerQueue reviewer={r} pool={pool} />}
              </div>
            )
          })
        )}
      </div>

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '30px 0' }}>
            <Sub title="分发规则" note="按累计工作量逐条分配" />
            <Row label="策略" value={d?.policy ?? 'balanced_per_submission'} />
            <Row label="第一顺位" value="累计分配量最少" />
            <Row label="第二顺位" value="当前待审最少" />
            <Row label="第三顺位" value="种子哈希定序" />
            <Row label="每条人数" value="2 人 · 互相看不到对方" />
            <Row label="随机种子" value={d?.seed ?? '执行时生成'} />
            <Row label="自我回避" value={d?.avoidSelf === false ? '关闭' : '开启'} tone={d?.avoidSelf === false ? 'var(--red)' : undefined} />
            <Row label="最近一次分发" value={d?.lastRunAt ? `${f.dateTime(d.lastRunAt)} · ${d.lastRunBy ?? ''}` : '尚未分发'} />
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '30px 0' }}>
            <Sub title="分发说明" />
            <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.85, textWrap: 'pretty' }}>
              先看每个人累计审了多少，尽量拉平，再参考手上还剩多少没审。
            </div>
            <div style={{ marginTop: 18 }}>
              <Note>填同一个随机种子，能重现同一次分发结果。</Note>
            </div>
            <div style={{ marginTop: 14, fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8, textWrap: 'pretty' }}>
              重排只动还没开始审的条目。
            </div>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}

export default function AdminDispatch() {
  return <div><PageHead en="DISPATCH" title="审核分发" desc="按材料分配双人审核任务。" /><SubmissionDispatch /></div>
}
