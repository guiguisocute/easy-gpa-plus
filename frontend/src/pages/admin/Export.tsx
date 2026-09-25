import { readableErrorMessage } from '@/api/errorMessages'
import { ExportProducts } from '@/components/ExportProducts'
import { EXPORT_PRODUCTS as PRODUCTS } from '@/lib/exportProducts'
import { ExportDownload } from '@/components/ExportDownload'
/* 班级管理员 · 导出中心。

   三个不可动摇的点（§9 与 §2.4）：
   1. 导出永远读结算快照，不实时查库——否则同一份报表两次导出可能不一样；
   2. 结算器四步顺序写死：取数 → 算分 → 封顶 → 合成，不可配置、不经模型；
   3. 闸门未开时只能预览，不能生成产物。

   导出任务在 worker 里跑，页面拿到 jobId 后轮询到终态为止；
   worker 没起的开发环境会一直停在 queued，这一点在界面上说清楚而不是让人干等。 */

import { useEffect, useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Row, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, TRow, TextBtn } from '@/components/ui'
import { ClassificationConfirmation } from '@/components/ClassificationConfirmation'
import { CollegeExportGuide } from '@/components/CollegeExportGuide'
import { SettlementConditions, SettlementSteps } from '@/components/SettlementGate'
import { mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useClassificationSuggestions, useExportJob, useGate, useSettlementActions, useStats } from '@/api/queries'
import type { ExportKind, SettlementResult } from '@/api/types'

export default function AdminExport() {
  const say = useApp((s) => s.say)
  const go = useApp((s) => s.go)
  const agentHandoff = useApp((s) => s.agentHandoff)
  const setAgentHandoff = useApp((s) => s.setAgentHandoff)
  const gate = useGate()
  const stats = useStats()
	const classification = useClassificationSuggestions()
  const { settle, requestExport } = useSettlementActions()
  const [jobId, setJobId] = useState<string | null>(null)
  const [downloading, setDownloading] = useState(false)
  const [result, setResult] = useState<SettlementResult | null>(null)
  const [prefillKind, setPrefillKind] = useState<ExportKind | null>(null)
  const job = useExportJob(jobId)

  useEffect(() => {
    if (agentHandoff?.kind !== 'export_draft') return
    const kind = agentHandoff.prefill?.kind
    if (typeof kind !== 'string' || !PRODUCTS.some((product) => product.key === kind)) return
    setPrefillKind(kind as ExportKind)
    setAgentHandoff(null)
    say('Agent 已预填导出类型；没有创建导出任务')
  }, [agentHandoff, say, setAgentHandoff])

  if (gate.isError) {
    return <Empty title="还没有已发布的方案" desc="把方案发布出去、审核走完，才能结算和导出。" />
  }
  if (gate.isLoading || !gate.data) return <div className="load-bar"><span /></div>

  const gateOpen = gate.data.open
  const settlement = stats.data?.settlement
  const settled = !!settlement?.runId
  const stale = !!settlement?.stale
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="EXPORT"
        title="导出中心"
        desc="汇总表、逐人明细和归档包"
        side={
          <>
            <Pill tone={gateOpen ? 'ok' : 'warn'}>{gateOpen ? (gate.data.forced ? '闸门已强制开启' : '闸门已开启') : '闸门未开启 · 只能预览'}</Pill>
            <Btn
              primary
              disabled={!gateOpen || settle.isPending}
              onClick={() =>
                settle.mutate(undefined, {
                  onSuccess: (r) => {
                    setResult(r)
                    say(r.reused ? '已使用最近一次结算结果' : `结算完成 · 覆盖 ${r.items.length} 人`)
                  },
                  onError: fail,
                })
              }
            >
              {settle.isPending ? '结算中…' : stale ? '重新结算' : '执行结算'}
            </Btn>
          </>
        }
      />

      <StatGrid cols={4}>
        <Stat
          en="结算结果"
          value={!settled ? '无' : stale ? '已过期' : '最新'}
          note={settled ? `${f.dateTime(settlement!.completedAt)} 生成` : '还没结算，现在导出是空的'}
          tone={!settled ? 'var(--fg3)' : stale ? 'var(--warn)' : 'var(--ok)'}
        />
        <Stat
          en="闸门状态"
          value={gateOpen ? '已开启' : '未开启'}
          note={gate.data.conditions.filter((c) => !c.ok).map((c) => c.label).join(' · ') || '全部条件满足'}
          tone={gateOpen ? 'var(--ok)' : 'var(--red)'}
        />
        <Stat en="覆盖人数" value={`${gate.data.sealedCount} / ${gate.data.studentCount}`} note="全班都完成了才能结算" />
        <Stat en="下载方式" value="断点续传" note="链接自动更新，断线后可继续" />
      </StatGrid>

	  <ClassificationConfirmation suggestions={classification.data?.items ?? []} onOpenArbitration={() => go('admSubs')} />

      <SettlementConditions gate={gate.data} />
      <SettlementSteps />

      <Split cols="1.3fr 1fr">
        <SplitCol first>
          <div style={{ padding: '30px 0' }}>
            <Sub title="导出文件" note="用的是最近一次结算的数据" />
            {prefillKind && <div style={{ marginBottom: 13 }}><Note>Agent 已预选“{PRODUCTS.find((item) => item.key === prefillKind)?.name}”，请确认后生成。</Note></div>}
            <ExportProducts selected={prefillKind} badge={<Pill tone="warn">Agent 已预填</Pill>} action={(kind) => (
              <Btn
                disabled={!settled || stale || !gateOpen || requestExport.isPending || downloading || job.data?.status === 'queued' || job.data?.status === 'running'}
                onClick={() => requestExport.mutate(kind, {
                  onSuccess: (r) => {
                    setJobId(r.jobId)
                    say(`${PRODUCTS.find(p => p.key === kind)?.name} 已提交生成`)
                  },
                  onError: fail,
                })}
              >
                {!settled ? '先去结算' : stale ? '先重新结算' : !gateOpen ? '闸门没开' : '生成'}
              </Btn>
            )} />
            <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8, marginTop: 14, textWrap: 'pretty' }}>
              逐条核对在页面上翻。这里只生成要交上去的文件。
            </div>

            {job.data && (
              <div style={{ border: '1px solid var(--line)', padding: '16px 18px', marginTop: 18, display: 'flex', flexDirection: 'column', gap: 10 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                  <span style={mono('11px', '.02em')}>{job.data.jobId.slice(0, 8)}</span>
                  <Pill tone={job.data.status === 'complete' ? 'ok' : job.data.status === 'failed' ? 'bad' : 'warn'}>
                    {{ queued: '排队中', running: '生成中', complete: '已完成', failed: '失败' }[job.data.status]}
                  </Pill>
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{PRODUCTS.find((p) => p.key === job.data!.kind)?.name}</span>
                </div>
                {job.data.downloadUrl && <ExportDownload key={job.data.jobId} job={job.data} onBusyChange={setDownloading} />}
                {job.data.error && <div style={{ fontSize: 12.5, color: 'var(--red)' }}>{readableErrorMessage(job.data.error, '导出文件生成失败，请稍后重新导出')}</div>}
                {(job.data.status === 'queued' || job.data.status === 'running') && (
                  <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7 }}>
                    <div className="load-bar" role="progressbar" aria-label="文件生成进度"><span /></div>
                    {job.data.status === 'queued' ? '任务已排队，准备生成文件。' : '正在生成文件，大型佐证包需要更长时间。'}完成后可查看下载进度。
                  </div>
                )}
              </div>
            )}
            {job.isError && <div role="alert" style={{ marginTop: 14 }}><Note tone="warn">{job.error.message} <TextBtn disabled={job.isFetching} onClick={() => { void job.refetch() }}>刷新任务状态</TextBtn></Note></div>}
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '30px 0' }}>
            <Sub title="结算信息" />
            <Row label="生成时刻" value={f.dateTime(settlement?.completedAt)} tone={settled ? undefined : 'var(--fg3)'} />
            <Row label="结算批次" value={settlement?.runId ?? '—'} tone={settled ? undefined : 'var(--fg3)'} />
            <Row label="方案版本" value={`${gate.data.schemeVersion}（已发布）`} />
            <Row label="纳入人数" value={result ? `${result.items.length} 人` : settled ? `${gate.data.studentCount} 人` : '—'} tone={settled ? undefined : 'var(--fg3)'} />
            <Row label="待终裁" value={`${gate.data.pendingConflicts} 条`} tone={gate.data.pendingConflicts ? 'var(--red)' : undefined} />
            <div style={{ marginTop: 18 }}>
              <Note tone={stale ? 'warn' : undefined}>
                {stale
                  ? '分数或评优规则变过了，重新结算一次。'
                  : settled
                    ? '以后分数有变动，这里会提醒你重新结算。'
                    : '上面的条件全满足了才能生成文件。'}
              </Note>
            </div>
          </div>
        </SplitCol>
      </Split>

      <CollegeExportGuide />

      {result && (
        <div style={{ paddingTop: 10 }}>
          <Sub title="本次结算结果" actions={<TextBtn onClick={() => setResult(null)}>收起</TextBtn>} />
          <Table cols="56px 108px 88px minmax(120px,1fr) 88px 72px 132px">
            <THead cells={['排名', '学号', '姓名', '各项得分', '总分', '三好', '奖学金档位']} />
            {result.items.map((r) => (
              <TRow
                key={r.userId}
                cells={[
                  <span key="a" style={{ ...mono('11.5px', '.02em'), color: 'var(--fg2)' }}>{r.classRank}</span>,
                  <span key="b" style={mono('11.5px', '.02em')}>{r.sid}</span>,
                  <span key="c" style={{ color: 'var(--fg)', fontWeight: 500 }}>{r.name}</span>,
                  <span key="d" style={{ fontSize: 12, color: 'var(--fg3)' }}>
                    {Object.entries(r.categoryScores).map(([k, v]) => `${k} ${f.score(v)}`).join(' · ')}
                  </span>,
                  <span key="e" style={{ ...num, fontWeight: 600, color: 'var(--fg)' }}>{f.score(r.totalScore)}</span>,
                  <Pill key="f" tone={r.honor ? 'ok' : 'idle'}>{r.honor ? '是' : '否'}</Pill>,
                  <span key="g" style={{ fontSize: 12, color: r.awardTier ? 'var(--fg)' : 'var(--fg3)' }}>{r.awardTier || '—'}</span>,
                ]}
              />
            ))}
          </Table>
        </div>
      )}
    </div>
  )
}
