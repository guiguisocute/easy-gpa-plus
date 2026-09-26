/* 运维超管 · 组件健康。

   这一页只显示基础设施状态，不含任何业务数据——连"某班有几条提交"都不显示。
   查看健康状态本身也会写平台审计（记录的是运维看了什么，不是运维看到了什么）。 */

import { Btn, Dot, Note, PageHead, Row, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, TRow } from '@/components/ui'
import { OpsLoadError } from '@/components/OpsLayout'
import { mono } from '@/lib/style'
import { useDeploy, useHealth, useQueues } from '@/api/queries'

const LABEL: Record<string, string> = {
	postgresApp: 'PostgreSQL · 业务连接',
	postgresOps: 'PostgreSQL · 运维连接',
  redis: 'Redis · 事件流与会话',
  objectStore: 'Garage · 佐证与导出产物',
  ses: '邮件投递通道',
  mail: '邮件投递通道',
}

export default function OpsHealth() {
  const health = useHealth()
  const deploy = useDeploy()
  const queues = useQueues()

  const checks = health.data?.components ?? []
  const bad = checks.filter((c) => c.status === 'down').length
  const degraded = health.data?.status === 'degraded'

  return (
    <div className="ops-page">
      <PageHead
        en="HEALTH"
        title="组件健康"
        desc="核心服务、通知通道和数据库"
        side={
          <span style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
            <Dot tone={!health.data || degraded ? 'warn' : 'ok'} />
            <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>{!health.data ? '状态未确认' : bad ? `${bad} 项不可用` : degraded ? '部分依赖需要检查' : '全部正常'}</span>
            <Btn disabled={health.isFetching || queues.isFetching} onClick={() => { void health.refetch(); void queues.refetch(); void deploy.refetch() }}>刷新状态</Btn>
          </span>
        }
      />

      <StatGrid cols={4}>
        <Stat
          en="总体状态"
          value={health.data?.status === 'ok' ? '正常' : health.data?.status === 'degraded' ? '降级' : '—'}
          note="任一依赖不可用即为降级"
          tone={degraded ? 'var(--red)' : health.data ? 'var(--ok)' : undefined}
        />
        <Stat en="迁移版本" value={String(deploy.data?.migrationVersion ?? '—')} note="已应用的最高一号数据库迁移" />
        <Stat en="事件流积压" value={String(queues.data?.items.reduce((s, g) => s + g.pending, 0) ?? '—')} unit="条" note="消费组未确认的消息" />
        <Stat
          en="失败队列"
          value={String(queues.data?.deadLetters ?? '—')}
          unit="条"
          note="多次重试失败，需人工处理"
          tone={(queues.data?.deadLetters ?? 0) > 0 ? 'var(--red)' : undefined}
        />
      </StatGrid>

      <div style={{ padding: '26px 0 0' }}>
		<Sub title="依赖组件" note="数据库、缓存与文件存储" />
        {health.isError ? <OpsLoadError title="组件状态暂时无法读取" onRetry={() => void health.refetch()} /> : health.isLoading ? (
          <div className="load-bar"><span /></div>
        ) : (
          <Table cols="minmax(180px,2fr) 110px minmax(160px,2fr)">
            <THead cells={['组件', '状态', '说明']} />
            {checks.map((c) => (
              <TRow
                key={c.name}
                cells={[
                  <span key="a" style={{ color: 'var(--fg)', fontWeight: 500 }}>{LABEL[c.name] ?? c.name}</span>,
                  <span key="b" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <Dot tone={c.status === 'down' ? 'bad' : c.status === 'ok' ? 'ok' : 'warn'} />
                    <span style={{ fontSize: 12.5, color: c.status === 'down' ? 'var(--red)' : 'var(--fg2)' }}>
                      {c.status === 'ok' ? '正常' : c.status === 'down' ? '不可用' : c.status}
                    </span>
                  </span>,
                  <span key="c" style={{ fontSize: 12, color: 'var(--fg3)', overflowWrap: 'anywhere', lineHeight: 1.8 }}>
                    {c.detail ?? '—'}
                  </span>,
                ]}
              />
            ))}
          </Table>
        )}
      </div>

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '30px 0' }}>
            <Sub title="消息队列" note="事件流与消费组状态" />
            {(queues.data?.items ?? []).map((g) => (
              <div key={JSON.stringify([g.stream, g.group])} style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0', flexWrap: 'wrap' }}>
                <span style={{ ...mono('11px', '.02em'), color: 'var(--fg2)' }}>{g.group}</span>
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{g.consumers} 个消费者</span>
                <span style={{ marginLeft: 'auto', fontSize: 12.5, color: g.pending > 0 ? 'var(--warn)' : 'var(--fg3)' }}>
                  未确认 {g.pending} · 滞后 {g.lag}
                </span>
              </div>
            ))}
            {queues.isError && <OpsLoadError title="队列状态暂时无法读取" onRetry={() => void queues.refetch()} />}
            {!queues.isError && (queues.data?.items.length ?? 0) === 0 && (
              <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>工作进程启动后会显示消费组。</div>
            )}
          </div>
        </SplitCol>
        <SplitCol>
          <div style={{ padding: '30px 0' }}>
            <Sub title="运行环境" />
            <Row label="环境" value={deploy.data?.appEnv ?? '—'} />
            <Row label="镜像标签" value={deploy.data?.imageTag ?? '—'} />
            <Row label="Git SHA" value={deploy.data?.gitSha ?? '—'} />
            <Row label="数据库角色" value={deploy.data?.databaseRole.name ?? '—'} />
            <Row
              label="RLS 绕过"
              value={deploy.data?.databaseRole.bypassRls ? '是（异常）' : '否'}
              tone={deploy.data?.databaseRole.bypassRls ? 'var(--red)' : undefined}
            />
            <div style={{ marginTop: 18 }}>
              <Note tone={deploy.data?.databaseRole.bypassRls ? 'warn' : undefined}>
                业务数据库那个角色不能是超级用户，也不能有绕过数据隔离的权限。
              </Note>
            </div>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}
