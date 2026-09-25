import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createInterface } from 'node:readline'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const frontend = requireLoopbackOrigin('PLAYWRIGHT_BASE_URL', process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:45173')
await assertLocalDevelopmentAPI(origin)
async function api(method, path, token, body, expected) {
  const res = await fetch(origin + '/api/v1' + path, { method, headers: e2eHeaders({ 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }), body: body === undefined ? undefined : JSON.stringify(body) })
  const data = res.status === 204 ? null : await res.json()
  assert.ok(expected === undefined ? res.ok : res.status === expected, `${method} ${path}: HTTP ${res.status}; ${data?.error?.code ?? data?.code ?? "unexpected status"}`)
  return data
}
async function register(sid, name, password) {
  const { ticket } = await api('POST', '/auth/register/check', null, { sid, name })
  return api('POST', '/auth/register/complete', null, { ticket, password })
}
const stamp = Date.now().toString(36)
const ops = await api('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT, password: process.env.OPS_PASSWORD })
await api('POST', '/ops/tenants', ops.access_token, { name: `MCP 合成班 ${stamp}`, slug: `mcp-${stamp}`, adminSid: `MA${stamp}`, adminName: '合成班管' }, 201)
const owner = await register(`MA${stamp}`, '合成班管', 'mcp-admin-test-password')
await api('POST', '/admin/whitelist', owner.access_token, { sid: `MS${stamp}`, name: '合成学生' }, 201)
const student = await register(`MS${stamp}`, '合成学生', 'mcp-student-test-password')
const lease = await api('POST', '/me/agent-connections', student.access_token, { name: 'CLI 回归', password: 'mcp-student-test-password', ttlMinutes: 15, scopes: ['read', 'draft'] }, 201)
const endpoint = frontend + '/mcp'
const env = { ...process.env, EASYGPA_MCP_URL: endpoint, EASYGPA_MCP_TOKEN: lease.token }
const child = spawn(process.execPath, ['scripts/mcp-bridge.mjs'], { env, stdio: ['pipe', 'pipe', 'pipe'] })
const pending = new Map()
let seq = 0, stderr = ''
child.stderr.on('data', b => { stderr += b })
const reader = createInterface({ input: child.stdout })
reader.on('line', line => {
  const message = JSON.parse(line)
  pending.get(message.id)?.(message); pending.delete(message.id)
})
async function rpc(method, params = {}) {
  const id = ++seq
  const response = new Promise(resolve => pending.set(id, resolve))
  child.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n')
  const timer = setTimeout(() => { throw new Error(`MCP request timed out: ${method}`) }, 15000)
  try { return await response } finally { clearTimeout(timer) }
}
async function tool(name, args = {}) {
  const response = await rpc('tools/call', { name, arguments: args })
  assert.equal(response.error, undefined)
  assert.ok(!response.result.isError, JSON.stringify(response.result.structuredContent))
  return response.result.structuredContent.data
}
const folder = await mkdtemp(join(tmpdir(), 'easygpa-mcp-e2e-'))
try {
  const initialized = await rpc('initialize', { protocolVersion: '2025-11-25', capabilities: {}, clientInfo: { name: 'EasyGPA stdio E2E', version: '1' } })
  assert.equal(initialized.error, undefined, initialized.error?.message)
  assert.equal(initialized.result.serverInfo.name, 'EasyGPA Plus')
  const tools = (await rpc('tools/list')).result.tools
  assert.ok(tools.some(t => t.name === 'submissions.draft'))
  assert.ok(!tools.some(t => t.name === 'operations.commit' || t.name === 'submissions.submit'))
  assert.equal((await tool('account.me')).sid, `MS${stamp}`)
  const draft = await tool('submissions.draft', { idempotencyKey: crypto.randomUUID(), input: { category: 'moral', itemKey: '__other_self_report_moral', title: 'MCP 合成佐证', claim: { score: 2 }, note: 'CLI 上传回归' } })
  // A small valid PNG lives only in the temporary test directory.
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a1V8AAAAASUVORK5CYII=', 'base64')
  const file = join(folder, 'synthetic.png')
  await writeFile(file, png)
  const upload = spawn(process.execPath, ['scripts/mcp-upload.mjs', draft.id, file], { env, stdio: ['ignore', 'pipe', 'pipe'] })
  let uploadOut = '', uploadError = ''
  upload.stdout.on('data', b => { uploadOut += b }); upload.stderr.on('data', b => { uploadError += b })
  const [code] = await once(upload, 'close')
  assert.equal(uploadOut.includes(lease.token) || uploadError.includes(lease.token), false)
  assert.equal(code, 0, uploadError)
  const detail = await tool('submissions.get', { id: draft.id })
  assert.equal(detail.evidence.length, 1)
  const link = await tool('evidence.read', { eid: detail.evidence[0].id })
  const downloaded = await fetch(link.url, { headers: { Authorization: `Bearer ${lease.token}` } })
  assert.equal(downloaded.status, 200)
  assert.deepEqual(Buffer.from(await downloaded.arrayBuffer()), png)
  await api('DELETE', '/me/agent-connections/' + lease.id, student.access_token, undefined, 204)
  const revoked = await rpc('tools/list')
  assert.match(revoked.error.message, /密钥已失效/)
  assert.equal((await fetch(link.url, { headers: { Authorization: `Bearer ${lease.token}` } })).status, 401)
  assert.equal(stderr.includes(lease.token), false)
  console.log('MCP E2E passed: real registration, connection creation, stdio handshake, scoped tools, local-file upload, authenticated download and revocation through the frontend proxy.')
} finally {
  child.kill(); reader.close()
  await rm(folder, { recursive: true, force: true })
}
