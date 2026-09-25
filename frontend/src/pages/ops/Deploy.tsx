/* 运维超管 · 部署与版本。

   这一页回答两个问题：现在跑的是哪一版，以及数据库迁移到了哪一号。
   不提供"一键回滚"按钮——回滚数据库迁移是有损操作，必须有人在终端里想清楚再敲。 */

import { Note, PageHead, Pill, Row, Split, SplitCol, Stat, StatGrid, Sub } from '@/components/ui'
import { mono } from '@/lib/style'
import { useDeploy, useHealth } from '@/api/queries'

export default function OpsDeploy() {
  const deploy = useDeploy()
  const health = useHealth()

  const d = deploy.data
  if (deploy.isLoading) return <div className="load-bar"><span /></div>

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="DEPLOY"
        title="部署与版本"
        desc="后端版本、进程和迁移进度；前端版本可从 /release.txt 查看，两者独立发布"
        side={
          <>
            <Pill tone={d?.appEnv === 'prod' ? 'ok' : 'warn'}>{d?.appEnv === 'prod' ? '生产环境' : `${d?.appEnv ?? '—'} 环境`}</Pill>
            <Pill tone={d?.aiEnabled ? 'warn' : 'idle'}>AI {d?.aiEnabled ? '开启' : '关闭'}</Pill>
          </>
        }
      />

      <StatGrid cols={4}>
        <Stat en="后端镜像标签" value={d?.imageTag ?? '—'} note="构建时经 -ldflags 注入" />
        <Stat en="迁移版本" value={String(d?.migrationVersion ?? '—')} note="已应用的最高一号迁移" />
        <Stat
          en="服务状态"
          value={health.data?.status === 'ok' ? '正常' : health.data?.status === 'degraded' ? '降级' : '—'}
          note="取自 /ops/health 的总体判定"
          tone={health.data?.status === 'degraded' ? 'var(--red)' : 'var(--ok)'}
        />
        <Stat en="AI 能力" value={d?.aiEnabled ? '已开启' : '已关闭'} note="由 Agent 配置页运行时控制" />
      </StatGrid>

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '30px 0' }}>
            <Sub title="版本信息" />
            <Row label="环境" value={d?.appEnv ?? '—'} />
            <Row label="后端镜像标签" value={d?.imageTag ?? '—'} />
            <Row label="后端 Git SHA" value={d?.gitSha ?? '—'} />
            <Row label="数据库迁移" value={`第 ${d?.migrationVersion ?? '—'} 号`} />
            <div style={{ marginTop: 18 }}>
              <Note>
                迁移是"只进不退"的默认姿势。<code style={mono('11.5px', '0')}>zongce migrate:down</code> 存在，
                但它一次只退一号，而且要求你明确知道那一号做了什么——所以它只在终端里，不在这个页面上。
              </Note>
            </div>
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '30px 0' }}>
            <Sub title="数据库角色" note="班级数据隔离" />
            <Row label="连接角色" value={d?.databaseRole.name ?? '—'} />
            <Row
              label="超级用户"
              value={d?.databaseRole.superuser ? '是' : '否'}
              tone={d?.databaseRole.superuser ? 'var(--red)' : undefined}
            />
			<Row
			  label="绕过 RLS"
	              value={d?.databaseRole.bypassRls ? '是' : '否'}
	              tone={d?.databaseRole.bypassRls ? 'var(--red)' : undefined}
			/>
			<Row
			  label="运维连接角色"
			  value={d?.opsDatabaseRole
			    ? `${d.opsDatabaseRole.name} · ${d.opsDatabaseRole.bypassRls ? '按设计可跨租户读取运维资源' : '未获运维所需权限'}`
			    : '—'}
			  tone={d?.opsDatabaseRole && (!d.opsDatabaseRole.bypassRls || d.opsDatabaseRole.superuser) ? 'var(--red)' : undefined}
			/>
            <div style={{ marginTop: 18 }}>
              <Note tone={d?.databaseRole.superuser || d?.databaseRole.bypassRls ? 'warn' : undefined}>
                {d?.databaseRole.superuser || d?.databaseRole.bypassRls
                  ? '业务数据库这个角色权限太大了，马上换成受限角色。'
                  : '业务数据库这个角色的权限是收着的，符合要求。'}
              </Note>
            </div>
          </div>
        </SplitCol>
      </Split>

      <div style={{ borderTop: '1px solid var(--line)', marginTop: 26, paddingTop: 18 }}>
        <Sub title="服务进程" />
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          {[
            ['api', '应用接口服务'],
            ['worker:dispatch', '分发事件消费'],
            ['worker:notify', '邮件通知投递'],
            ['worker:export', '导出产物生成'],
            ['worker:maintenance', '定时提醒、自动封存与导出清理'],
            ['worker:backup', '数据库与对象备份、隔离恢复演练'],
            ['worker:ai', 'M5 前直接退出，不参与运行'],
          ].map(([cmd, desc]) => (
            <div key={cmd} style={{ display: 'flex', alignItems: 'center', gap: 14, borderTop: '1px solid var(--line2)', padding: '12px 0', flexWrap: 'wrap' }}>
              <span style={{ ...mono('11.5px', '.02em'), color: 'var(--fg2)', width: 170, flex: 'none' }}>{cmd}</span>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{desc}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
