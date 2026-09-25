import { useState } from 'react'
import { Btn, Pill, TextBtn } from './ui'
import { fieldStyle } from '@/lib/style'
import { ApiError } from '@/api/client'
import { createAgentConnection, useAgentConnectionActions, useAgentConnections, useAgentOperations, type CreatedConnection } from '@/api/mcp'
import { useApp } from '@/stores/app'

const time = (value: string | null) => value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '尚未使用'
const pretty = (value: unknown) => JSON.stringify(value, null, 2)
const statusLabel = { pending: '待本人批准', approved: '已批准 · 等待 Agent 执行', rejected: '已拒绝', applied: '已执行' }

export default function AgentConnections() {
  const connections = useAgentConnections()
  const operations = useAgentOperations()
  const actions = useAgentConnectionActions()
  const say = useApp((s) => s.say)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('我的电脑')
  const [password, setPassword] = useState('')
  const [minutes, setMinutes] = useState(60)
  const [scopes, setScopes] = useState(['read', 'draft'])
  const [busy, setBusy] = useState(false)
  const [secret, setSecret] = useState<CreatedConnection | null>(null)
  const [showSecret, setShowSecret] = useState(false)
  const data = connections.data
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '连接操作失败，请重试')
  const copy = async (value: string) => { try { await navigator.clipboard.writeText(value); say('已复制') } catch { say('浏览器未允许复制，请手动选择内容') } }
  async function create() {
    setBusy(true)
    try {
      const result = await createAgentConnection({ name: name.trim(), password, ttlMinutes: minutes, scopes })
      setSecret(result); setShowSecret(false); setCreating(false); actions.refresh()
    } catch (e) { fail(e) } finally { setPassword(''); setBusy(false) }
  }
  return <section style={{ padding: '26px 0', borderBottom: '1px solid var(--line)' }} aria-label="连接本地 Agent">
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
      <div><h2 style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>连接本地 Agent</h2><p style={{ margin: '8px 0 0', fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8 }}>把服务地址和临时密钥配置到支持 MCP 的客户端，让你的 Agent 分析资料或代办已授权的操作。</p></div>
      <div style={{ display: 'flex', gap: 10 }}><TextBtn onClick={actions.refresh}>刷新连接与操作</TextBtn><Btn primary disabled={!data?.enabled || creating || !!secret} onClick={() => setCreating(true)}>新建连接</Btn></div>
    </div>
    {connections.isLoading && <div className="load-bar" style={{ marginTop: 18 }}><span /></div>}
    {connections.isError && <p role="alert" style={{ color: 'var(--red)', fontSize: 13 }}>暂时无法读取连接设置，请刷新重试。</p>}
    {data && !data.enabled && <p style={{ color: 'var(--fg3)', fontSize: 13 }}>部署者已关闭外部 Agent 接入。</p>}
    {data?.demo && <p style={{ color: 'var(--fg3)', fontSize: 12.5 }}>此处演示连接管理流程；演示密钥不可连接真实 MCP 服务，实际使用需要部署后端。</p>}
    {data && <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginTop: 16 }}><input aria-label="MCP 服务地址" readOnly value={data.url} style={{ ...fieldStyle, flex: 1, minWidth: 0, fontFamily: 'monospace' }} /><Btn onClick={() => void copy(data.url)}>复制地址</Btn></div>}
    {creating && <div style={{ marginTop: 20, border: '1px solid var(--line)', padding: 20 }}>
      <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap' }}>
        <label style={{ display: 'grid', gap: 8, flex: 1, minWidth: 190, fontSize: 13 }}>连接名称<input style={fieldStyle} value={name} maxLength={80} onChange={(e) => setName(e.target.value)} /></label>
        <label style={{ display: 'grid', gap: 8, flex: 1, minWidth: 190, fontSize: 13 }}>有效期（分钟）<input style={fieldStyle} type="number" min={1} max={data?.maxTTLMinutes ?? 1440} value={minutes} onChange={(e) => setMinutes(Number(e.target.value))} /></label>
      </div>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', margin: '12px 0 18px' }}>{[15, 60, 480, 1440].filter((n) => n <= (data?.maxTTLMinutes ?? 1440)).map((n) => <Btn key={n} onClick={() => setMinutes(n)}>{n < 60 ? `${n} 分钟` : `${n / 60} 小时`}</Btn>)}</div>
      <fieldset style={{ border: 0, padding: 0, margin: 0 }}><legend style={{ fontSize: 13, fontWeight: 600, marginBottom: 12 }}>允许这个连接做什么</legend>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(230px, 1fr))', gap: 14 }}>{data?.scopes.map((scope) => <label key={scope.key} style={{ display: 'flex', alignItems: 'flex-start', gap: 10, cursor: 'pointer', fontSize: 13 }}><input type="checkbox" checked={scopes.includes(scope.key)} onChange={(e) => setScopes((old) => e.target.checked ? [...old, scope.key] : old.filter((x) => x !== scope.key))} /><span>{scope.label}<small style={{ display: 'block', color: 'var(--fg3)', lineHeight: 1.7, marginTop: 4 }}>{scope.description}</small></span></label>)}</div>
      </fieldset>
      <p style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8, marginTop: 18 }}>客户端可能把读取的资料发送给你配置的模型服务。密钥到期即失效，可随时撤销；网页「问 Agent」开关不会撤销外部连接。</p>
      <label style={{ display: 'grid', gap: 8, maxWidth: 380, fontSize: 13 }}>用当前密码确认授权<input style={fieldStyle} type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} /></label>
      <div style={{ display: 'flex', gap: 10, marginTop: 18 }}><Btn primary disabled={busy || !name.trim() || !password || scopes.length === 0 || !Number.isInteger(minutes) || minutes < 1 || minutes > (data?.maxTTLMinutes ?? 1440)} onClick={() => void create()}>{busy ? '正在生成…' : '生成临时密钥'}</Btn><Btn disabled={busy} onClick={() => { setCreating(false); setPassword('') }}>取消</Btn></div>
    </div>}
    {secret && <div role="status" style={{ marginTop: 20, border: '1px solid var(--line)', padding: 20 }}>
      <strong style={{ fontSize: 14 }}>连接已创建 · 密钥只在此处显示一次</strong><p style={{ color: 'var(--fg3)', fontSize: 12.5 }}>有效至 {time(secret.expiresAt)}。保存到客户端的凭据或环境变量中。</p>
      <div style={{ display: 'flex', gap: 10 }}><input aria-label="新生成的临时密钥" type={showSecret ? 'text' : 'password'} readOnly value={secret.token} autoComplete="off" style={{ ...fieldStyle, flex: 1, minWidth: 0, fontFamily: 'monospace' }} /><Btn onClick={() => void copy(secret.token)}>复制密钥</Btn></div>
      <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12.5, marginTop: 12 }}><input type="checkbox" checked={showSecret} onChange={(e) => setShowSecret(e.target.checked)} />显示密钥</label>
      <p style={{ color: 'var(--fg3)', fontSize: 12.5, lineHeight: 1.8 }}>远程连接使用上方 URL，请求头为 Authorization: Bearer &lt;临时密钥&gt;。仅支持 stdio 的客户端可使用仓库 scripts/mcp-bridge.mjs；兼容配置见接入文档。</p>
      <Btn onClick={() => { setSecret(null); setShowSecret(false) }}>我已保存，关闭密钥</Btn>
    </div>}
    <div style={{ marginTop: 20 }}>{data?.items.map((connection) => {
      const active = !connection.revokedAt && new Date(connection.expiresAt).getTime() > Date.now()
      return <div key={connection.id} style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap', borderTop: '1px solid var(--line2)', padding: '15px 0' }}><div style={{ flex: 1, minWidth: 220 }}><strong style={{ fontSize: 13 }}>{connection.name}</strong> <Pill tone={active ? 'ok' : 'idle'}>{connection.revokedAt ? '已撤销' : active ? '有效' : '已到期'}</Pill><div style={{ color: 'var(--fg3)', fontSize: 12, marginTop: 7, lineHeight: 1.8 }}>{connection.scopes.map((key) => data.scopes.find((s) => s.key === key)?.label ?? key).join(' · ')}<br />到期 {time(connection.expiresAt)} · 最近使用 {time(connection.lastUsedAt)}</div></div>{active && <Btn danger disabled={actions.revoke.isPending} onClick={() => actions.revoke.mutate(connection.id, { onSuccess: () => say('连接已撤销'), onError: fail })}>撤销连接</Btn>}</div>
    })}{data?.items.length === 0 && <p style={{ fontSize: 12.5, color: 'var(--fg3)' }}>还没有外部连接。</p>}</div>
    <div style={{ marginTop: 22 }}><h3 style={{ fontSize: 14, margin: '0 0 12px' }}>Agent 操作记录</h3>
      {operations.isError && <p style={{ color: 'var(--red)', fontSize: 12.5 }}>操作记录读取失败，请刷新重试。</p>}
      {operations.data?.items.length === 0 && <p style={{ fontSize: 12.5, color: 'var(--fg3)' }}>需要批准的变更和已完成的代办会显示在这里。</p>}
      {operations.data?.items.map((operation) => {
        const expired = new Date(operation.expiresAt).getTime() <= Date.now() || !operation.connectionActive
        const canDecide = operation.status === 'pending' && !expired
        return <details key={operation.id} style={{ borderTop: '1px solid var(--line2)', padding: '14px 0', fontSize: 13 }}><summary style={{ cursor: 'pointer', lineHeight: 1.8 }}><strong>{operation.preview.title ?? operation.tool}</strong> · {operation.connectionName} · {expired && operation.status !== 'applied' && operation.status !== 'rejected' ? '已失效' : statusLabel[operation.status]}</summary><p style={{ color: 'var(--fg3)', fontSize: 12 }}>{time(operation.createdAt)} · 操作 {operation.id}</p>
          {operation.preview.before !== undefined && <><strong>变更前</strong><pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: 12, lineHeight: 1.7 }}>{pretty(operation.preview.before)}</pre></>}
          {operation.preview.input !== undefined && <><strong>本次变更内容</strong><pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: 12, lineHeight: 1.7 }}>{pretty({ ...operation.preview.targets as object, input: operation.preview.input })}</pre></>}
          {operation.preview.notice && <p style={{ color: 'var(--fg3)', lineHeight: 1.8 }}>{operation.preview.notice}</p>}
          {canDecide && <div style={{ display: 'flex', gap: 10 }}><Btn primary disabled={actions.decide.isPending} onClick={() => actions.decide.mutate({ id: operation.id, approve: true }, { onSuccess: () => say('本次操作已批准，等待 Agent 执行'), onError: fail })}>批准本次操作</Btn><Btn danger disabled={actions.decide.isPending} onClick={() => actions.decide.mutate({ id: operation.id, approve: false }, { onSuccess: () => say('已拒绝'), onError: fail })}>拒绝</Btn></div>}
        </details>
      })}
    </div>
  </section>
}
