/* 班级管理员 · 班级看板。结构与桌面原型「班级看板.dc.html」一一对照：
   01 结算闸门 · 02 审核进度 · 03 学生进度与封存 · 04 你的任务 · 05 实时排名。

   闸门条件不在前端拼——直接渲染后端 /admin/gate 给的 conditions 数组。 */

import { useState, type ReactNode } from 'react'
import { Empty, LineTabs, Btn } from '@/components/ui'
import { LiveRanking } from '@/components/LiveRanking'
import { mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import {
  useAdminAppeals,
  useAdminObjections,
  useAdminSeals,
  useAdminSubmissions,
  useGate,
  useRosterActions,
  useScheme,
  useSettlementActions,
  useStats,
  useWindow,
} from '@/api/queries'
import { appealRoundLabel } from '@/api/types'
import type { AdminSeal, Appeal, Objection } from '@/api/types'
import type { View } from '@/lib/nav'
import { PROGRESS_LIFECYCLE as LIFE, openReviewProgress } from '@/lib/reviewProgress'

const SEAL_TABS = [
  { key: 'all', label: '全部' },
  { key: 'sealed', label: '已封存' },
  { key: 'active', label: '仍在提交' },
  { key: 'none', label: '零提交' },
] as const

const SEAL_META: Record<AdminSeal['sealState'], { label: string; color: string }> = {
  sealed: { label: '已封存', color: 'var(--ok)' },
  active: { label: '仍在提交', color: 'var(--warn)' },
  none: { label: '零提交', color: 'var(--red)' },
}

const RECORD_META: { key: string; label: string; color: string }[] = [
  { key: 'pending', label: '待审核', color: 'color-mix(in srgb, var(--fg) 30%, transparent)' },
  { key: 'consensus', label: '合议中', color: 'color-mix(in srgb, var(--fg) 52%, transparent)' },
  { key: 'scored', label: '已定分', color: 'var(--ok)' },
  { key: 'appealing', label: '申诉中', color: 'var(--warn)' },
  { key: 'arbitrating', label: '冲突待仲裁', color: 'var(--red)' },
  { key: 'locked', label: '结算锁定', color: 'var(--ok)' },
  { key: 'draft', label: '草稿（不计入结算）', color: 'var(--line)' },
]

function termLabel(close?: string) {
  if (!close) return ''
  const d = new Date(close)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()} ${d.getMonth() >= 7 ? '秋' : '春'}`
}

function mergeCounts(a: Record<string, number> = {}, b: Record<string, number> = {}) {
  const out: Record<string, number> = {}
  for (const part of LIFE) out[part.key] = (a[part.key] ?? 0) + (b[part.key] ?? 0)
  return out
}

function BoardSection({
  index,
  en,
  title,
  note,
  accent,
  actions,
  children,
}: {
  index: string
  en: string
  title: string
  note?: string
  accent?: boolean
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <section style={{ borderTop: `1px solid ${accent ? 'var(--fg)' : 'var(--line)'}`, marginTop: index === '01' ? 0 : 'clamp(48px,6vw,84px)', paddingTop: 22 }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'flex-start', gap: '10px 18px' }}>
        <div style={{ flex: '1 1 280px', minWidth: 0 }}>
          <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '8px 14px' }}>
            <span style={{ ...mono(), color: accent ? 'var(--red)' : 'var(--fg3)' }}>{index} — {en}</span>
            <span style={{ fontSize: 19, fontWeight: 600, letterSpacing: '-0.01em', lineHeight: 1.25 }}>{title}</span>
          </div>
          {note && <div style={{ marginTop: 8, fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.6, maxWidth: '72ch' }}>{note}</div>}
        </div>
        {actions && <div style={{ marginLeft: 'auto', paddingTop: 4 }}>{actions}</div>}
      </div>
      {children}
    </section>
  )
}

function Linkish({ children, onClick, danger }: { children: ReactNode; onClick?: () => void; danger?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="hv-red"
      style={{
        background: 'none',
        border: 0,
        padding: 0,
        font: 'inherit',
        fontSize: 12.5,
        color: danger ? 'var(--red)' : 'var(--fg2)',
        cursor: 'pointer',
        borderBottom: `1px solid ${danger ? 'var(--red)' : 'var(--line)'}`,
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </button>
  )
}

export default function AdminBoard() {
  const say = useApp((s) => s.say)
  const go = useApp((s) => s.go)
  const user = useApp((s) => s.user)
  const [tab, setTab] = useState<(typeof SEAL_TABS)[number]['key']>('all')
  const [desk, setDesk] = useState<'conflict' | 'appeal' | 'proposal'>('conflict')
  const [forceOpen, setForceOpen] = useState(false)
  const [forceReason, setForceReason] = useState('')
  const stats = useStats()
  const gate = useGate()
  const seals = useAdminSeals()
  const scheme = useScheme()
  const win = useWindow()
  const conflicts = useAdminSubmissions({ status: 'arbitrating', page_size: 50 })
  const appeals = useAdminAppeals({ page_size: 50 })
  const objections = useAdminObjections({ status: 'submitted', page_size: 50 })
  const { remind } = useRosterActions()
  const { force } = useSettlementActions()
  if (stats.isError && !stats.data) {
    return <><Empty title="看板读取失败" desc={stats.error instanceof ApiError ? stats.error.message : '请检查网络后重试。'} /><Btn onClick={() => void stats.refetch()}>重新读取</Btn></>
  }
  if (stats.isLoading || !stats.data) return <div className="load-bar"><span /></div>

  const s = stats.data
  const sealRows = (seals.data?.items ?? []).filter((r) => tab === 'all' || (tab === 'none' ? r.submitted === 0 : r.sealState === tab))
  const conflictRows = conflicts.data?.items ?? []
  const appealRows = appeals.data?.items ?? []
  const proposalRows = objections.data?.items ?? []
  const waitingAppeals = appealRows.filter((a) => a.status === 'escalated' || a.status === 'reviewing' || a.status === 'filed')
  const totalSubs = Object.values(s.submissions).reduce((a, b) => a + b, 0)
  const unsealed = s.users - s.sealed
  const left = f.daysUntil(win.data?.window.close)
  const conditions = gate.data?.conditions ?? []
  const gateOk = conditions.filter((c) => c.ok).length
  const gateLeft = Math.max(0, conditions.length - gateOk)
  const firstBlocker = conditions.find((c) => !c.ok)
  const lifeSource = mergeCounts(s.reviewProgress?.item, s.reviewProgress?.scorecard)
  const overdue = (s.reviewProgress?.overdue.item ?? 0) + (s.reviewProgress?.overdue.scorecard ?? 0)
  const lifeTotal = Object.values(lifeSource).reduce((a, b) => a + b, 0)
  const lifeParts = LIFE.filter((part) => (lifeSource[part.key] ?? 0) > 0)

  const maxSealUnit = Math.max(1, ...((seals.data?.items ?? []).map((r) => r.submitted + r.drafts)))

  function openDesk(view: View = 'admSubs') {
    go(view)
  }

  return (
    <div style={{ animation: 'rise .28s ease both', maxWidth: 1440, margin: '0 auto' }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'flex-end', gap: 24, padding: '38px 0 clamp(30px,4vw,46px)' }}>
        <div style={{ flex: 1, minWidth: 280 }}>
          <div style={mono()}>CLASS BOARD{termLabel(win.data?.window.close) ? ` · ${termLabel(win.data?.window.close)}` : ''}</div>
          <h1 style={{ margin: '16px 0 0', fontSize: 'clamp(30px,3.6vw,44px)', lineHeight: 1.04, fontWeight: 600, letterSpacing: '-0.025em' }}>班级看板</h1>
          <p style={{ margin: '12px 0 0', maxWidth: '60ch', fontSize: 13.5, lineHeight: 1.6, color: 'var(--fg2)', textWrap: 'pretty' }}>
            {user?.className ?? ''} · {s.users} 人 · 方案 {scheme.data?.version ?? '—'}（已发布）· 提交与审核并行，{s.sealed} / {s.users} 人已封存。
          </p>
        </div>
        <div style={{ display: 'flex', gap: 22, alignItems: 'center' }}>
          <Linkish onClick={() => document.getElementById('admin-live-ranking')?.scrollIntoView({ behavior: 'smooth' })}>实时排名 ↓</Linkish>
          <Linkish onClick={() => go('admTimeline')}>时间窗口</Linkish>
          <Linkish onClick={() => go('admExport')}>导出中心</Linkish>
        </div>
      </div>

      <BoardSection index="01" en="SETTLEMENT GATE" title="结算闸门" note="全部条件满足后自动开启" accent>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(min(100%,320px),1fr))', gap: 'clamp(28px,4vw,64px)', padding: 'clamp(28px,3.6vw,44px) 0 0' }}>
          <div>
            <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>距开启还差</div>
            <div style={{ display: 'flex', alignItems: 'flex-end', gap: 12, marginTop: 6 }}>
              <span style={{ font: '600 clamp(56px,7vw,86px)/0.86 Instrument Sans,sans-serif', letterSpacing: '-0.04em', fontVariantNumeric: 'tabular-nums', color: gateLeft ? 'var(--red)' : 'var(--ok)' }}>{gateLeft}</span>
              <span style={{ fontSize: 14, color: 'var(--fg2)', paddingBottom: 10 }}>个条件 · 已满足 {gateOk} / {conditions.length || 6}</span>
            </div>
            {firstBlocker && (
              <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start', marginTop: 22, paddingTop: 16, borderTop: '1px solid var(--line2)', maxWidth: '46ch' }}>
                <i style={{ width: 5, height: 5, background: 'var(--red)', display: 'block', marginTop: 6, flex: 'none' }} />
                <div style={{ lineHeight: 1.55 }}>
                  <div style={{ fontSize: 13, fontWeight: 600 }}>最先卡住的是：{firstBlocker.label}</div>
                  <div style={{ fontSize: 12.5, color: 'var(--fg3)', marginTop: 4 }}>{firstBlocker.detail}</div>
                </div>
              </div>
            )}
            {!firstBlocker && (
              <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start', marginTop: 22, paddingTop: 16, borderTop: '1px solid var(--line2)', maxWidth: '46ch' }}>
                <i style={{ width: 5, height: 5, background: 'var(--ok)', display: 'block', marginTop: 6, flex: 'none' }} />
                <div style={{ lineHeight: 1.55 }}>
                  <div style={{ fontSize: 13, fontWeight: 600 }}>闸门条件已全部满足</div>
                  <div style={{ fontSize: 12.5, color: 'var(--fg3)', marginTop: 4 }}>可以到导出中心执行结算。</div>
                </div>
              </div>
            )}
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 24 }}>
              <button
                type="button"
                className="hv-op82"
                disabled={remind.isPending || unsealed === 0}
                onClick={() =>
                  remind.mutate({}, {
                    onSuccess: (r) => say(`提醒已入队 · 将发给 ${r.recipientCount} 名未封存者，静默时段顺延到次日`),
                    onError: (e) => say(e instanceof ApiError ? e.message : '提醒失败'),
                  })
                }
                style={{ background: 'var(--fg)', color: 'var(--bg)', border: '1px solid var(--fg)', padding: '10px 16px', fontSize: 12.5, fontWeight: 600, cursor: unsealed === 0 ? 'not-allowed' : 'pointer', opacity: unsealed === 0 ? 0.45 : 1 }}
              >
                提醒未封存者
              </button>
              <button
                type="button"
                onClick={() => setForceOpen((v) => !v)}
                disabled={gate.data?.forced}
                style={{ background: 'none', color: 'var(--fg2)', border: '1px solid var(--line)', padding: '10px 16px', fontSize: 12.5, cursor: gate.data?.forced ? 'not-allowed' : 'pointer' }}
              >
                {gate.data?.forced ? '已强制开启' : '强制开启结算…'}
              </button>
            </div>
            {forceOpen && !gate.data?.forced && (
              <div style={{ marginTop: 16, border: '1px solid var(--line)', padding: 16, animation: 'rise .2s ease both', maxWidth: '52ch' }}>
                <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.7 }}>强制开启会跳过还没满足的条件。理由会记进审计，综测小组和学生都能看到。</div>
                <textarea
                  value={forceReason}
                  onChange={(e) => setForceReason(e.target.value)}
                  placeholder="写清理由，比如：学院要求 8-20 前上报，剩下的条目线下裁"
                  rows={2}
                  style={{ width: '100%', marginTop: 12, background: 'var(--sub)', border: '1px solid var(--line)', padding: 10, fontSize: 12.5, lineHeight: 1.6, resize: 'vertical', color: 'var(--fg)', font: 'inherit' }}
                />
                <div style={{ display: 'flex', gap: 10, marginTop: 12 }}>
                  <button
                    type="button"
                    className="hv-op82"
                    disabled={forceReason.trim().length < 8 || force.isPending}
                    onClick={() =>
                      force.mutate(forceReason.trim(), {
                        onSuccess: () => {
                          setForceOpen(false)
                          setForceReason('')
                          say('已强制开启结算 · 理由记进了审计')
                        },
                        onError: (e) => say(e instanceof ApiError ? e.message : '强制开启失败'),
                      })
                    }
                    style={{ background: 'var(--red)', color: 'var(--onAccent)', border: 0, padding: '9px 14px', fontSize: 12.5, fontWeight: 600, cursor: 'pointer', opacity: forceReason.trim().length < 8 ? 0.45 : 1 }}
                  >
                    {force.isPending ? '提交中…' : '确认强制开启'}
                  </button>
                  <button type="button" onClick={() => setForceOpen(false)} style={{ background: 'none', border: '1px solid var(--line)', padding: '9px 14px', fontSize: 12.5, color: 'var(--fg2)', cursor: 'pointer' }}>取消</button>
                </div>
              </div>
            )}
            {gate.data?.forced && (
              <div style={{ marginTop: 16, border: '1px solid var(--warn)', background: 'var(--warnBg)', padding: '12px 14px', fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.7 }}>
                已强制开闸（{f.dateTime(gate.data.forcedAt)}）：{f.readableText(gate.data.forceReason)}
              </div>
            )}
          </div>
          <div>
            {conditions.map((g) => (
              <div key={g.key} style={{ display: 'flex', alignItems: 'baseline', gap: 14, padding: '13px 0', borderBottom: '1px solid var(--line2)' }}>
                <i style={{ width: 5, height: 5, display: 'block', flex: 'none', marginTop: 6, background: g.ok ? 'var(--ok)' : 'var(--fg3)' }} />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 13, fontWeight: 500 }}>{g.label}</div>
                  <div style={{ fontSize: 12, color: 'var(--fg3)', marginTop: 3 }}>{g.detail}</div>
                </div>
                <span style={{ ...mono('11px', '.06em'), whiteSpace: 'nowrap', color: g.ok ? 'var(--ok)' : 'var(--fg3)' }}>{g.ok ? '满足' : '未满足'}</span>
              </div>
            ))}
            <div style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.8, marginTop: 14, textWrap: 'pretty' }}>
              闸门未开时导出页只能预览。窗口还剩 {left} 天，到期未封存的账号由系统自动封存。
            </div>
          </div>
        </div>
      </BoardSection>

      <BoardSection index="02" en="REVIEW" title="审核进度" note="点阶段进审核明细" actions={<Linkish danger onClick={() => openReviewProgress(go)}>查看审核明细</Linkish>}>
        <div style={{ marginTop: 'clamp(26px,3vw,38px)' }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 12 }}>
            <span style={{ fontSize: 13, fontWeight: 600 }}>任务生命周期</span>
            <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{lifeTotal} 个任务</span>
          </div>
          <div style={{ display: 'flex', gap: 2, marginTop: 14, height: 8 }}>
            {lifeParts.map((part) => (
              <button
                key={part.key}
                type="button"
                onClick={() => openReviewProgress(go, part.key)}
                aria-label={`${part.label} ${lifeSource[part.key]} 条 · ${part.hint}`}
                title={`${part.label} ${lifeSource[part.key]} 条 · ${part.hint}`}
                style={{ border: 0, padding: 0, height: 8, cursor: 'pointer', width: f.pct(lifeSource[part.key], lifeTotal || 1), background: part.bg }}
              />
            ))}
          </div>
          <div style={{ height: 2, marginTop: 3, background: 'var(--line2)' }}>
            <i style={{ display: 'block', height: 2, background: 'var(--red)', width: f.pct(overdue, lifeTotal || 1) }} />
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px 22px', marginTop: 16 }}>
            {lifeParts.map((part) => (
              <button
                key={part.key}
                type="button"
                onClick={() => openReviewProgress(go, part.key)}
                style={{ display: 'flex', alignItems: 'center', gap: 8, background: 'none', border: 0, padding: '2px 0', fontSize: 12.5, cursor: 'pointer', color: 'var(--fg2)' }}
              >
                <i style={{ width: 8, height: 8, display: 'block', background: part.bg }} />
                <span>{part.label}</span>
                <span style={{ ...mono('12px', '0'), color: 'var(--fg)' }}>{lifeSource[part.key]}</span>
              </button>
            ))}
            <span style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12.5, color: 'var(--red)' }}>
              <i style={{ width: 8, height: 2, display: 'block', background: 'var(--red)' }} />逾期 {overdue} 条
            </span>
          </div>
          <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.75, marginTop: 14, maxWidth: '74ch', textWrap: 'pretty' }}>点击阶段查看对应任务的材料、轨迹与处理操作。</div>
        </div>

        <div style={{ marginTop: 'clamp(30px,3.4vw,44px)', paddingTop: 26, borderTop: '1px solid var(--line2)' }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 12 }}>
            <span style={{ fontSize: 13, fontWeight: 600 }}>记录状态分布</span>
            <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{totalSubs} 条记录</span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(180px,1fr))', gap: 2, marginTop: 16 }}>
            {RECORD_META.filter((r) => (s.submissions[r.key as keyof typeof s.submissions] ?? 0) > 0).map((r) => {
              const n = s.submissions[r.key as keyof typeof s.submissions] ?? 0
              return (
                <div key={r.key} style={{ paddingRight: 16 }}>
                  <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 8 }}>
                    <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>{r.label}</span>
                    <span style={{ ...mono('12px', '0'), color: r.color }}>{n} / {totalSubs}</span>
                  </div>
                  <div style={{ height: 4, background: 'var(--line2)', marginTop: 8 }}>
                    <i style={{ display: 'block', height: 4, background: r.color, width: f.pct(n, totalSubs || 1) }} />
                  </div>
                </div>
              )
            })}
          </div>
        </div>

        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 20, marginTop: 24 }}>
          <Linkish onClick={() => openReviewProgress(go)}>查看全部任务</Linkish>
          <Linkish onClick={() => openReviewProgress(go, 'complete', 'item')}>查看已定分项</Linkish>
          <Linkish danger onClick={() => openReviewProgress(go, 'overdue')}>查看逾期任务</Linkish>
        </div>
      </BoardSection>

      <BoardSection
        index="03"
        en="STUDENTS"
        title="学生进度与封存"
        note="不能替学生封存，只能提醒他。窗口一到点，系统会自动封存"
      >
        <LineTabs
          items={SEAL_TABS.map((t) => ({
            key: t.key,
            label: t.label,
            count: t.key === 'all'
              ? (seals.data?.items.length ?? 0)
              : t.key === 'none'
                ? (seals.data?.items ?? []).filter((r) => r.submitted === 0).length
                : (seals.data?.items ?? []).filter((r) => r.sealState === t.key).length,
          }))}
          value={tab}
          onChange={setTab}
          trailing={
            <Linkish
              danger
              onClick={() =>
                remind.mutate({}, {
                  onSuccess: (r) => say(`提醒已入队 · 将发给 ${r.recipientCount} 名未封存者，静默时段顺延到次日`),
                  onError: (e) => say(e instanceof ApiError ? e.message : '提醒失败'),
                })
              }
            >
              提醒全部未封存者 →
            </Linkish>
          }
        />
        {sealRows.length === 0 ? (
          <Empty title="这一类下没有学生" desc="换个筛选看看。" />
        ) : <div data-r="scroll"><div style={{ minWidth: 760 }}>{sealRows.map((p) => {
          const unit = 100 / maxSealUnit
          const pending = Math.max(0, p.submitted - p.scored)
          const state = p.submitted === 0 ? SEAL_META.none : SEAL_META[p.sealState]
          const stateLabel = state.label
          const stateColor = state.color
          return (
            <div key={p.userId} style={{ display: 'grid', gridTemplateColumns: 'minmax(132px,180px) minmax(200px,1.3fr) minmax(120px,160px) 120px 48px', gap: 16, alignItems: 'center', padding: '16px 6px', borderBottom: '1px solid var(--line2)' }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 14, fontWeight: 600, letterSpacing: '-0.005em', lineHeight: 1.35 }}>{p.name}</div>
                <div style={{ ...mono('11.5px', '.02em'), marginTop: 4, lineHeight: 1.4 }}>{p.sid}</div>
              </div>
              <div>
                <div style={{ display: 'flex', gap: 2, height: 6 }}>
                  <i style={{ display: 'block', height: 6, background: 'var(--ok)', width: `${p.scored * unit}%` }} />
                  <i style={{ display: 'block', height: 6, background: 'color-mix(in srgb, var(--fg) 34%, transparent)', width: `${pending * unit}%` }} />
                  <i style={{ display: 'block', height: 6, background: 'var(--warnBg)', width: `${p.drafts * unit}%` }} />
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 14, marginTop: 8, fontSize: 12, color: 'var(--fg3)', lineHeight: 1.45 }}>
                  <span>提交 <b style={{ color: 'var(--fg)', fontVariantNumeric: 'tabular-nums' }}>{p.submitted}</b></span>
                  <span>已定分 <b style={{ color: 'var(--ok)', fontVariantNumeric: 'tabular-nums' }}>{p.scored}</b></span>
                  <span>草稿 <b style={{ color: p.drafts ? 'var(--warn)' : 'var(--fg3)', fontVariantNumeric: 'tabular-nums' }}>{p.drafts}</b></span>
                </div>
              </div>
              <div>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: stateColor }}>
                  <i style={{ width: 6, height: 6, display: 'block', background: stateColor }} />{stateLabel}
                </span>
              </div>
              <div style={{ ...mono('11.5px', '0'), lineHeight: 1.5 }}>{f.dateTime(p.lastActivity)}</div>
              <div style={{ textAlign: 'right' }}>
                {p.sealState !== 'sealed' && (
                  <Linkish
                    danger
                    onClick={() =>
                      remind.mutate({ userId: p.userId }, {
                        onSuccess: () => say(`已提醒 ${p.name} · 单条提醒不聚合，立即投递`),
                        onError: (e) => say(e instanceof ApiError ? e.message : '提醒失败'),
                      })
                    }
                  >
                    提醒
                  </Linkish>
                )}
              </div>
            </div>
          )
        })}</div></div>}
      </BoardSection>

      <BoardSection
        index="04"
        en="YOUR DESK"
        title="你的任务"
        note="冲突、申诉、小组提案三类都在这里"
        accent
        actions={<Linkish danger onClick={() => openDesk()}>去仲裁台</Linkish>}
      >
        <div style={{ marginTop: 24 }}>
          <LineTabs
            items={[
              { key: 'conflict', label: '冲突待裁', count: conflictRows.length },
              { key: 'appeal', label: '申诉', count: waitingAppeals.length || appealRows.length },
              { key: 'proposal', label: '小组提案', count: proposalRows.length },
            ]}
            value={desk}
            onChange={setDesk}
          />
        </div>
        <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.75, marginTop: 16, maxWidth: '76ch', textWrap: 'pretty' }}>
          {desk === 'conflict'
            ? '处理审核结论不一致的条目。'
            : desk === 'appeal'
              ? '查看复评进度并处理待终裁申诉。'
              : '审核综测小组提交的分数调整提案。'}
        </div>
        {desk === 'conflict' && <DeskConflicts rows={conflictRows} meSid={user?.sid} catName={(k) => scheme.data?.categories.find((c) => c.key === k)?.name ?? k} onOpen={() => openDesk()} />}
        {desk === 'appeal' && <DeskAppeals rows={appealRows} meSid={user?.sid} onOpen={() => openDesk()} />}
        {desk === 'proposal' && <DeskProposals rows={proposalRows} meSid={user?.sid} onOpen={() => openDesk()} />}
      </BoardSection>

      <BoardSection index="05" en="LIVE RANKING" title="实时排名" note="按当前认定分排" actions={<Linkish onClick={() => go('admExport')}>去导出中心预览</Linkish>}>
        <LiveRanking ranking={s.ranking} categories={scheme.data?.categories ?? []} refreshing={stats.isFetching} failed={stats.isError} onRefresh={() => void stats.refetch()} />
      </BoardSection>

      <div style={{ borderTop: '1px solid var(--line)', marginTop: 'clamp(48px,6vw,84px)', paddingTop: 20, display: 'flex', flexWrap: 'wrap', gap: 14, alignItems: 'baseline' }}>
        <span style={mono()}>EASYGPA · 综测统计平台</span>
        <span style={{ fontSize: 12, color: 'var(--fg3)' }}>材料 · 审核 · 申诉 · 结算</span>
      </div>
    </div>
  )
}

function DeskConflicts({ rows, meSid, catName, onOpen }: { rows: { id: string; student: string; studentId: string; title: string; category: string; reviews: { score: number }[] }[]; meSid?: string; catName: (k: string) => string; onOpen: () => void }) {
  if (rows.length === 0) return <Empty title="没有待仲裁的冲突" desc="闸门的「无待终裁冲突」这一条已经满足。" />
  const ordered = [...rows].sort((a, b) => {
    const ga = a.reviews.length === 2 ? Math.abs(a.reviews[0].score - a.reviews[1].score) : 0
    const gb = b.reviews.length === 2 ? Math.abs(b.reviews[0].score - b.reviews[1].score) : 0
    return gb - ga
  })
  return (
    <div data-r="scroll"><div style={{ minWidth: 720 }}>
      {ordered.map((q) => {
        const scores = q.reviews.map((r) => r.score)
        const gap = scores.length === 2 ? Math.abs(scores[0] - scores[1]) : null
        const mine = meSid === q.studentId
        return (
          <DeskRow
            key={q.id}
            student={q.student}
            title={q.title}
            cat={catName(q.category)}
            score={gap === null ? '—' : f.score(gap)}
            state={mine ? '本人回避' : '冲突待裁'}
            stateColor={mine ? 'var(--warn)' : 'var(--red)'}
            onHandle={onOpen}
          />
        )
      })}
    </div></div>
  )
}

function DeskAppeals({ rows, meSid, onOpen }: { rows: Appeal[]; meSid?: string; onOpen: () => void }) {
  if (rows.length === 0) return <Empty title="没有申诉" desc="学生对已经定分的条目、基础分或扣分提了申诉，就会出现在这里。" />
  return (
    <div data-r="scroll"><div style={{ minWidth: 720 }}>
      {rows.map((a) => (
        <DeskRow
          key={a.id}
          student={a.student}
          title={a.target}
          cat={appealRoundLabel(a.round)}
          score={f.score(a.baselineScore ?? a.currentScore)}
          state={meSid === a.studentId ? '本人回避' : (a.status === 'escalated' ? '待终裁' : a.status === 'reviewing' ? '复评中' : a.status === 'final' ? '已终裁' : '进行中')}
          stateColor={meSid === a.studentId ? 'var(--warn)' : a.status === 'escalated' ? 'var(--red)' : 'var(--warn)'}
          onHandle={onOpen}
        />
      ))}
    </div></div>
  )
}

function DeskProposals({ rows, meSid, onOpen }: { rows: Objection[]; meSid?: string; onOpen: () => void }) {
  if (rows.length === 0) return <Empty title="没有小组提案" desc="综测小组在「扣分与异议」里提交的提案会出现在这里。" />
  return (
    <div data-r="scroll"><div style={{ minWidth: 720 }}>
      {rows.map((o) => (
        <DeskRow
          key={o.id}
          student={o.student}
          title={o.itemName}
          cat={o.categoryName}
          score={f.score(o.proposedScore)}
          state={meSid === o.studentId ? '本人回避' : '待确认'}
          stateColor={meSid === o.studentId ? 'var(--warn)' : 'var(--red)'}
          onHandle={onOpen}
        />
      ))}
    </div></div>
  )
}

function DeskRow({ student, title, cat, score, state, stateColor, onHandle }: { student: string; title: string; cat: string; score: string; state: string; stateColor: string; onHandle: () => void }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '92px minmax(180px,1.6fr) 112px 80px 108px 52px', gap: 16, alignItems: 'center', padding: '16px 6px', borderBottom: '1px solid var(--line2)' }}>
      <div style={{ fontSize: 13, fontWeight: 500 }}>{student}</div>
      <div title={title} style={{ fontSize: 12.5, color: 'var(--fg2)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{title}</div>
      <div style={{ fontSize: 12, color: 'var(--fg3)' }}>{cat}</div>
      <div style={{ font: "500 13px/1 'JetBrains Mono',monospace", color: 'var(--red)' }}>{score}</div>
      <div style={{ fontSize: 12, color: stateColor, display: 'flex', alignItems: 'center', gap: 7 }}>
        <i style={{ width: 6, height: 6, display: 'block', flex: 'none', background: stateColor }} />{state}
      </div>
      <div style={{ textAlign: 'right' }}><Linkish danger onClick={onHandle}>处理</Linkish></div>
    </div>
  )
}
