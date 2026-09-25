import type { AgentConnection, AgentConnectionsResponse } from '@/api/mcp'
import { db, nid, nowIso, type DemoUser } from './db'
import { fail } from './errors'

const options = [
  { key: 'read', label: '读取与分析', description: '读取账号可见的规则、成绩、材料与任务' },
  { key: 'draft', label: '草稿与上传', description: '创建、修改材料草稿和上传佐证' },
  { key: 'submit', label: '学生代办', description: '正式提交、撤回、申诉及核对成绩' },
  { key: 'review', label: '审核代办', description: '提交本人审核与复评结论、异议提案' },
  { key: 'manage', label: '班级管理', description: '准备班级变更；本人在网页批准后才能执行' },
  { key: 'export', label: '统计与导出', description: '创建和读取本班导出任务' },
]
export function mockMCPSettings(method: string, path: string, body: Record<string, unknown>, user: DemoUser) {
  if (user.role === 'ops') fail(404, 'not_found', '资源不存在')
  const store = db()
  const connections = (store.agentConnections ??= {})
  const operations = (store.agentOperations ??= {})
  const items = (connections[user.id] ??= [])
  const logs = (operations[user.id] ??= [])
  const allowed = options.filter((_scope, index) => index < 3 || index === 3 && user.role !== 'student' || index > 3 && user.role === 'class_admin')
  if (path === '/me/agent-connections' && method === 'GET') return { items, scopes: allowed, enabled: true, demo: true, url: 'https://gpa.example.org/mcp', maxTTLMinutes: 1440 } satisfies AgentConnectionsResponse
  if (path === '/me/agent-connections' && method === 'POST') {
    const name = String(body.name ?? '').trim()
    const minutes = Number(body.ttlMinutes)
    const scopes = [...new Set(Array.isArray(body.scopes) ? body.scopes.map(String) : [])]
    if (!name || name.length > 80 || !Number.isInteger(minutes) || minutes < 1 || minutes > 1440) fail(422, 'invalid_connection', '名称或有效期不正确')
    if (body.password !== user.password) fail(403, 'password_mismatch', '当前密码不正确')
    if (!scopes.length || scopes.some((key) => !allowed.some((scope) => scope.key === key))) fail(403, 'invalid_scope', '账号无权授予此权限')
    if (items.filter((item) => !item.revokedAt && new Date(item.expiresAt).getTime() > Date.now()).length >= 10) fail(409, 'connection_limit', '最多保留 10 条有效连接，请先撤销不再使用的连接')
    const token = `demo_only_${crypto.randomUUID().replaceAll('-', '')}`
    const row: AgentConnection = { id: nid('mcp'), name, prefix: 'demo_only', scopes, expiresAt: new Date(Date.now() + minutes * 60_000).toISOString(), revokedAt: null, createdAt: nowIso(), lastUsedAt: null }
    items.unshift(row)
    if (scopes.includes('manage')) logs.unshift({ id: nid('mcp-op'), connectionId: row.id, connectionName: name, tool: 'class.timeline', status: 'pending', preview: { title: '修改时间窗口（演示）', before: { 提交截止: store.timeline.window.close }, input: { 提交截止: new Date(Date.now() + 7 * 86400_000).toISOString() }, notice: '这是合成的待批准操作，仅演示管理流程。' }, createdAt: nowIso(), expiresAt: new Date(Date.now() + 600_000).toISOString(), approvedAt: null, connectionActive: true })
    return { ...row, token, url: 'https://gpa.example.org/mcp' }
  }
  const revoke = path.match(/^\/me\/agent-connections\/([^/]+)$/)
  if (revoke && method === 'DELETE') {
    const row = items.find((item) => item.id === revoke[1])
    if (!row) return fail(404, 'not_found', '连接不存在')
    row.revokedAt ??= nowIso()
    return null
  }
  if (path === '/me/agent-operations' && method === 'GET') return { items: logs.map((log) => ({ ...log, connectionActive: items.some((item) => item.id === log.connectionId && !item.revokedAt && new Date(item.expiresAt).getTime() > Date.now()) })) }
  const decision = path.match(/^\/me\/agent-operations\/([^/]+)\/decision$/)
  if (decision && method === 'POST') {
    const row = logs.find((log) => log.id === decision[1])
    const connection = items.find((item) => item.id === row?.connectionId)
    if (!row || !connection || connection.revokedAt) return fail(404, 'not_found', '操作不存在或连接已撤销')
    if (row.status !== 'pending' || Date.parse(row.expiresAt) <= Date.now() || Date.parse(connection.expiresAt) <= Date.now()) fail(409, 'operation_stale', '操作已处理或过期')
    row.status = body.approve ? 'approved' : 'rejected'
    row.approvedAt = body.approve ? nowIso() : null
    return { status: row.status }
  }
  return fail(404, 'not_found', '接口不存在')
}
