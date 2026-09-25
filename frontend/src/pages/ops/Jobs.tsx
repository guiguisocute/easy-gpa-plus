/* 运维超管 · 队列与任务。

   事件流是 Redis Stream + 消费组，手写 XADD / XREADGROUP / XACK / XAUTOCLAIM。
   死信不自动重投——重试耗尽的消息多半是数据本身有问题，自动重投只会把同一个错反复放大。 */

import { useEffect, useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, TRow } from '@/components/ui'
import { mono, num } from '@/lib/style'
import { useApp } from '@/stores/app'
import type { WorkerState } from '@/api/types'
import { ApiError } from '@/api/client'
import { useCron, useDeadLetters, useOpsActions, useQueues, useWorkers } from '@/api/queries'

const WORKERS = [
	{ name: 'dispatch', desc: '把新提交分到审核队列' },
	{ name: 'notify', desc: '发邮件、日报和静默顺延' },
	{ name: 'export', desc: '出表格和佐证包' },
	{ name: 'maintenance', desc: '窗口提醒、到期封存、清理导出' },
	{ name: 'backup', desc: '每日备份和恢复演练' },
	{ name: 'ai', desc: '识别材料、生成候选草稿' },
	{ name: 'agent', desc: '知识问答和草稿建议' },
]

function workerStatusText(state: WorkerState, lag?: number) {
  const heartbeat = state.heartbeatStatus === 'alive' || state.heartbeatAlive
    ? '心跳正常'
    : state.heartbeatStatus === 'missing'
      ? '心跳缺失'
      : state.heartbeatStatus === 'error'
        ? (state.heartbeatError ? `心跳失败：${state.heartbeatError}` : '心跳失败')
        : '心跳超时'
  return lag == null ? heartbeat : `${heartbeat} · lag ${lag}`
}

function workerTone(state?: WorkerState) {
  if (!state) return 'var(--warn)'
  if (state.heartbeatStatus === 'alive' || state.heartbeatAlive) return 'var(--ok)'
  return 'var(--warn)'
}

export default function OpsJobs() {
  const say = useApp((s) => s.say)
  const queues = useQueues()
  const cron = useCron()
	const workers = useWorkers()
	const [deadCursor, setDeadCursor] = useState('')
	const [deadHistory, setDeadHistory] = useState<string[]>([])
	const deadLetters = useDeadLetters(deadCursor)
  const { redeliver } = useOpsActions()
	const [selectedDead, setSelectedDead] = useState<string[]>([])
	useEffect(() => setSelectedDead([]), [deadCursor])

  const groups = queues.data?.items ?? []
  const dead = queues.data?.deadLetters ?? 0
  const deliveryAlertCount = queues.data?.deliveryAlertCount ?? 0
  const deliveryAlerts = queues.data?.deliveryAlerts ?? []
  const pending = groups.reduce((s, g) => s + g.pending, 0)
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="QUEUES"
        title="队列与任务"
        desc="队列、后台任务和工作进程"
        side={<Pill tone={dead > 0 || deliveryAlertCount > 0 ? 'bad' : pending > 0 ? 'warn' : 'ok'}>{dead > 0 ? `${dead} 条失败任务` : deliveryAlertCount > 0 ? `${deliveryAlertCount} 条投递延误` : pending > 0 ? `${pending} 条待确认` : '队列干净'}</Pill>}
      />

      {dead > 0 && <div role="alert" style={{ marginBottom: 16 }}><Note tone="bad">有 {dead} 条任务已耗尽重试并停止自动处理。请检查下方失败原因，修复后选择记录重投；重投会重置原工作组的重试次数。</Note></div>}
      {deliveryAlertCount > 0 && <div role="alert" style={{ marginBottom: 16 }}><Note tone="bad">有 {deliveryAlertCount} 条通知因限流或结果待确认而持续延误超过 2 小时，请检查邮件日志、发送额度与供应商状态。限流会自动重试，结果待确认的记录需在邮件页核对。静默时段和每日聚合等待不计入此告警。{deliveryAlerts.slice(0, 5).map((alert) => <div key={`${alert.group}:${alert.eventId}`} style={{ marginTop: 6, overflowWrap: 'anywhere' }}>{alert.group} · {alert.eventType} · {alert.eventId} · {alert.reason} · 起始 {new Date(alert.since).toLocaleString('zh-CN')}</div>)}</Note></div>}

      <StatGrid cols={4}>
        <Stat en="工作组" value={String(groups.length)} unit="个" note="按任务类型分组" />
        <Stat en="处理中" value={String(pending)} unit="条" note="已投递，等待确认" tone={pending > 0 ? 'var(--warn)' : undefined} />
        <Stat en="失败队列" value={String(dead)} unit="条" note="多次重试失败，需人工处理" tone={dead > 0 ? 'var(--red)' : undefined} />
        <Stat en="等待投递" value={String(queues.data?.outboxPending ?? 0)} unit="条" note="尚未进入处理队列" />
      </StatGrid>

      <div style={{ padding: '26px 0 0' }}>
        <Sub
          title="消费组"
          note="lag 是消费组落后流尾的条数"
          actions={
            <Btn
			  disabled={selectedDead.length === 0 || redeliver.isPending}
			  onClick={() => {
				if (!window.confirm(`重新投递选中的 ${selectedDead.length} 条失败的任务？先确认原来那个错已经修好了。`)) return
				redeliver.mutate(selectedDead, {
                  onSuccess: (r) => say(`已重新投递 ${r.redelivered} 条失败任务`),
                  onError: fail,
                })
			  }}
            >
			  {redeliver.isPending ? '重投中…' : `重投选中项（${selectedDead.length}）`}
            </Btn>
          }
        />
        {queues.isLoading ? (
          <div className="load-bar"><span /></div>
        ) : groups.length === 0 ? (
          <Empty title="还没有工作进程" desc="工作进程启动后会在这里显示。" />
        ) : (
          <Table cols="minmax(140px,1.4fr) 120px 96px 96px 96px minmax(140px,1fr)">
            <THead cells={['消费组', '流', '长度', '未确认', '滞后', '最后投递 ID']} />
            {groups.map((g) => (
              <TRow
                key={g.group}
                cells={[
                  <span key="a" style={{ color: 'var(--fg)', fontWeight: 500 }}>{g.group}</span>,
                  <span key="b" style={mono('11px', '.02em')}>{g.stream}</span>,
                  <span key="c" style={num}>{g.length}</span>,
                  <span key="d" style={{ ...num, color: g.pending > 0 ? 'var(--warn)' : 'var(--fg3)' }}>{g.pending}</span>,
                  <span key="e" style={{ ...num, color: g.lag > 0 ? 'var(--warn)' : 'var(--fg3)' }}>{g.lag}</span>,
                  <span key="f" style={mono('11px', '0')}>{g.lastDeliveredId}</span>,
                ]}
              />
            ))}
          </Table>
        )}
		{(deadLetters.data?.items ?? []).length > 0 && (
		  <div style={{ marginTop: 20 }}>
			<Sub title="失败任务" note="确认原错误已处理后再重新投递" />
			<Table cols="42px 150px 110px 150px minmax(180px,1fr) minmax(180px,1fr)">
			  <THead cells={['', 'Stream ID', '原工作组', '事件类型', '事件 ID', '失败原因 / 载荷摘要']} />
			  {(deadLetters.data?.items ?? []).map((item) => <TRow key={item.id} cells={[
				<input key="pick" type="checkbox" checked={selectedDead.includes(item.id)} onChange={(e) => setSelectedDead((rows) => e.target.checked ? [...rows, item.id] : rows.filter((id) => id !== item.id))} />,
				<span key="id" style={mono('10.5px', '0')}>{item.id}</span>,
				<span key="group">{item.values.group || '—'}</span>,
				<span key="type">{item.values.type || '—'}</span>,
				<span key="event" style={mono('10.5px', '0')}>{item.values.event_id || '—'}</span>,
				<span key="error" style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{item.values.error || item.values.last_error || (item.values.payload_sha256 ? `载荷 SHA-256 ${item.values.payload_sha256.slice(0, 16)}…` : '—')}</span>,
			  ]} />)}
			</Table>
			<div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 12 }}><Btn disabled={deadHistory.length === 0} onClick={() => { const previous = deadHistory[deadHistory.length - 1] ?? ''; setDeadHistory((rows) => rows.slice(0, -1)); setDeadCursor(previous) }}>上一页</Btn><Btn disabled={!deadLetters.data?.nextCursor} onClick={() => { const next = deadLetters.data?.nextCursor; if (!next) return; setDeadHistory((rows) => [...rows, deadCursor]); setDeadCursor(next) }}>下一页</Btn></div>
		  </div>
		)}
      </div>

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '30px 0' }}>
			<Sub title="Worker 状态" note="心跳与任务积压" />
            {WORKERS.map((w) => (
			  (() => {
				const state = workers.data?.items.find((item) => item.name === w.name)
				const group = groups.find((item) => item.group === `${w.name}-cg`)
				return (
              <div key={w.name} style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0', flexWrap: 'wrap' }}>
                <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 180, flex: 1 }}>
                  <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>{w.name}</span>
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>{w.desc}</span>
                </span>
				<span style={{ fontSize: 11.5, color: workers.isError ? 'var(--warn)' : workerTone(state) }}>
				  {workers.isError ? 'Worker 状态不可用' : state ? workerStatusText(state, group?.lag) : '读取中…'}
				</span>
              </div>
				)
			  })()
            ))}
            <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8, marginTop: 14, textWrap: 'pretty' }}>
			  工作进程的运行状态由最近一次心跳判断。
            </div>
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '30px 0' }}>
            <Sub title="定时任务" note="由维护进程跑" />
            {(cron.data?.items ?? []).map((c) => (
              <div key={c.name} style={{ display: 'flex', flexDirection: 'column', gap: 5, borderTop: '1px solid var(--line2)', padding: '13px 0' }}>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
                  <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>{c.name}</span>
                  <span style={{ marginLeft: 'auto', ...mono('11px', '0') }}>{c.schedule}</span>
                </div>
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
				  最近一次：{c.last ?? '还没跑过（Worker 没起的时候这里是空的）'}{c.durationMs != null ? ` · ${c.durationMs} ms` : ''}
                </span>
				{c.result && <span style={{ fontSize: 12, color: c.result === 'failed' ? 'var(--red)' : c.result === 'running' || c.result === 'queued' ? 'var(--warn)' : 'var(--ok)' }}>结果：{c.result}{c.error ? ` · ${c.error}` : ''}</span>}
				<span style={{ fontSize: 12, color: 'var(--fg3)' }}>下次预计：{c.next ?? '—'}{c.config ? ` · ${JSON.stringify(c.config)}` : ''}</span>
              </div>
            ))}
            <div style={{ marginTop: 18 }}>
              <Note>
                先把原来那个错处理掉，再挑要重投的记录。
              </Note>
            </div>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}
