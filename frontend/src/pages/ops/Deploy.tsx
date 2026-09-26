import { useState } from 'react'
import { Btn, Note, PageHead, Pill, Row, Stat, StatGrid } from '@/components/ui'
import { OpsLoadError, OpsSection, OpsTabPanel, OpsTabs } from '@/components/OpsLayout'
import { mono } from '@/lib/style'
import { useDeploy, useHealth } from '@/api/queries'

const SERVICES = [
  ['api', '应用接口与鉴权'],
  ['worker:dispatch', '分发事件与审核派单'],
  ['worker:notify', '业务通知、认证邮件与投递反馈'],
  ['worker:export', '成绩表、学院报送文件与佐证包'],
  ['worker:maintenance', '窗口提醒、到期封存与过期产物清理'],
  ['worker:backup', '数据库与对象备份、隔离恢复演练'],
  ['worker:ai', '材料识别、归组与候选草稿'],
  ['worker:agent', '知识文件转换、检索与对话任务'],
]
function ttl(seconds: number) {
  return seconds % 86400 === 0 ? seconds / 86400 + ' 天' : seconds % 3600 === 0 ? seconds / 3600 + ' 小时' : seconds % 60 === 0 ? seconds / 60 + ' 分钟' : seconds + ' 秒'
}

export default function OpsDeploy() {
  const deploy = useDeploy()
  const health = useHealth()
  const [tab, setTab] = useState('version')
  if (deploy.isLoading) return <div className="load-bar"><span /></div>
  if (!deploy.data) return <OpsLoadError title="部署信息暂时无法读取" onRetry={() => void deploy.refetch()} />
  const d = deploy.data
  const config = d.configuration
  return <div className="ops-page">
    <PageHead en="DEPLOYMENT" title="部署与版本" desc="核对当前版本、连接地址与启动配置。这里的部署项为只读，修改部署后需重启相应服务。" side={<><Pill tone={d.appEnv === 'prod' ? 'ok' : 'warn'}>{d.appEnv === 'prod' ? '生产环境' : d.appEnv + ' 环境'}</Pill><Btn disabled={deploy.isFetching} onClick={() => { void deploy.refetch(); void health.refetch() }}>{deploy.isFetching ? '刷新中…' : '刷新信息'}</Btn></>} />
    <StatGrid cols={3}><Stat en="后端版本" value={d.imageTag || '—'} note="当前运行的镜像标签" /><Stat en="数据库迁移" value={String(d.migrationVersion)} note="已应用的最新迁移" /><Stat en="依赖状态" value={health.data?.status === 'ok' ? '正常' : health.data?.status === 'degraded' ? '需检查' : '未知'} note={health.isError ? '健康信息暂不可用' : '详细状态见组件健康'} tone={health.data?.status === 'degraded' ? 'var(--warn)' : undefined} /></StatGrid>
    <OpsTabs id="deploy" value={tab} onChange={setTab} items={[{ id: 'version', label: '版本与进程' }, { id: 'security', label: '会话与安全' }, { id: 'connections', label: '连接与存储' }]} />
    <OpsTabPanel id="deploy" name="version" active={tab}>
      <OpsSection title="版本信息" desc="前端与后端独立发布。前端版本标识可在本站 /release.txt 查看。"><Row label="运行环境" value={d.appEnv} /><Row label="后端镜像" value={d.imageTag || '—'} /><Row label="Git SHA" value={<code>{d.gitSha || '—'}</code>} /><Row label="数据库版本" value={'第 ' + d.migrationVersion + ' 号迁移'} /><div style={{ marginTop: 18 }}><Note>版本升级与数据库迁移由部署流程执行。回退迁移需要在终端评估数据影响后操作。</Note></div></OpsSection>
      <OpsSection title="服务分工" desc="这是部署进程的职责说明，实际心跳和积压状态请查看队列与任务。">{SERVICES.map(([command, description]) => <Row key={command} label={<code style={mono('11px', '0')}>{command}</code>} value={description} />)}</OpsSection>
    </OpsTabPanel>
    <OpsTabPanel id="deploy" name="security" active={tab}>
      <OpsSection title="数据库权限" desc="业务连接必须使用受限角色。运维连接独立承担授权的跨班级管理。"><Row label="业务连接角色" value={d.databaseRole.name} /><Row label="业务超级用户" value={d.databaseRole.superuser ? '是 · 需要调整' : '否'} tone={d.databaseRole.superuser ? 'var(--red)' : undefined} /><Row label="业务绕过租户隔离" value={d.databaseRole.bypassRls ? '是 · 需要调整' : '否'} tone={d.databaseRole.bypassRls ? 'var(--red)' : undefined} /><Row label="运维连接角色" value={d.opsDatabaseRole?.name ?? '未提供'} /><Row label="运维跨租户权限" value={d.opsDatabaseRole ? d.opsDatabaseRole.bypassRls && !d.opsDatabaseRole.superuser ? '已按运维职责配置' : '需要检查角色权限' : '未提供'} /></OpsSection>
      {config ? <>
        <OpsSection title="会话与代理" desc="由启动配置决定，控制登录有效期与真实来源 IP 的识别。"><Row label="安全 Cookie" value={config.auth.cookieSecure ? '仅通过 HTTPS 发送' : '允许 HTTP · 仅适合本地开发'} tone={!config.auth.cookieSecure && d.appEnv === 'prod' ? 'var(--red)' : undefined} /><Row label="访问令牌有效期" value={ttl(config.auth.accessTokenTtlSeconds)} /><Row label="刷新会话有效期" value={ttl(config.auth.refreshTokenTtlSeconds)} /><Row label="可信代理网段" value={config.trustedProxies.length ? config.trustedProxies.join('、') : '未设置'} /></OpsSection>
        <OpsSection title="MCP 接入" desc="开启入口后仍受账号权限、租户隔离和授权范围限制。"><Row label="服务入口" value={config.mcp.enabled ? '已开启' : '已关闭'} /><Row label="授权最长有效期" value={config.mcp.maxTtlHours + ' 小时'} /></OpsSection>
        <OpsSection title="凭据保护准备" desc="只显示加密密钥是否就绪，页面不会获取任何密钥原文。">{[{ label: '邮件凭据加密', ready: config.secrets.mailReady }, { label: '模型凭据加密', ready: config.secrets.aiReady }, { label: '远程备份凭据加密', ready: config.secrets.backupRemoteReady }, { label: '远程备份 age 公钥', ready: config.backup.remoteRecipientReady }].map((item) => <Row key={item.label} label={item.label} value={<Pill tone={item.ready ? 'ok' : 'warn'}>{item.ready ? '已准备' : '未配置'}</Pill>} />)}</OpsSection>
      </> : <p className="ops-empty-message">当前后端未提供启动配置摘要。</p>}
    </OpsTabPanel>
    <OpsTabPanel id="deploy" name="connections" active={tab}>
      {config ? <>
        <OpsSection title="站点入口" desc="用于生成邮件与外部访问链接，应与实际访问地址一致。"><Row label="公开地址" value={config.publicUrl || '未设置'} /></OpsSection>
        <OpsSection title="对象存储" desc="内部连接用于服务进程，公开连接用于浏览器上传下载。"><Row label="内部端点" value={config.storage.endpoint || '未设置'} /><Row label="内部 HTTPS" value={config.storage.useSsl ? '开启' : '关闭'} /><Row label="公开端点" value={config.storage.publicEndpoint || '未设置'} /><Row label="公开 HTTPS" value={config.storage.publicUseSsl ? '开启' : '关闭'} /><Row label="存储桶" value={config.storage.bucket || '未设置'} /><Row label="地域" value={config.storage.region || '未设置'} /></OpsSection>
        <OpsSection title="备份位置" desc="此处显示部署挂载目录。自动计划、保留时间与远程副本可在备份与恢复中配置。"><Row label="本地备份目录" value={config.backup.directory || '未设置'} /><Row label="第二副本目录" value={config.backup.offsiteDirectory || '未设置'} /><div style={{ marginTop: 18 }}><Note>同机的第二份目录不能替代异地备份。请结合远程副本与恢复演练检查可恢复性。</Note></div></OpsSection>
      </> : <p className="ops-empty-message">当前后端未提供连接与存储配置摘要。</p>}
    </OpsTabPanel>
  </div>
}
