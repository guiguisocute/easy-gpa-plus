#!/usr/bin/env node
/* 班管解封与个人补交真实 HTTP 回归。业务状态全部通过 API 创建；数据库只读核验。
   node e2e/run.mjs unseal；也可连接已启动的隔离合成环境，设置
   API_BASE 与 E2E_COMPOSE_PROJECT。禁止访问生产地址和生产数据库。 */
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const project = process.env.E2E_COMPOSE_PROJECT ?? 'easygpa-plus-e2e'
assert.match(project, /^easygpa-(?:e2e|unseal)$/, 'Only this task’s synthetic Compose projects are allowed')
const stamp = Date.now().toString(36)
const password = 'unseal-e2e-synthetic-password'
const reason = '遗漏一份志愿服务证明，允许补交后重新封存'

async function call(method, path, { token, body, status, rejected = false } = {}) {
  const headers = e2eHeaders({ Accept: 'application/json' })
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`
  const response = await fetch(`${origin}/api/v1${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const result = await response.json().catch(() => null)
  // Auth responses and request bodies must never appear in test output.
  const label = `${method} ${path}: HTTP ${response.status} (${result?.code ?? 'no error code'})`
  if (rejected) assert.ok(response.status >= 400 && response.status < 500, label)
  else if (status !== undefined) assert.equal(response.status, status, label)
  else assert.ok(response.ok, label)
  return result
}

async function denied(path, person, body, status, code) {
  const result = await call('POST', path, { token: person.token, body, status })
  if (code) assert.equal(result?.code, code, `${path} error code`)
}

async function register(sid, name) {
  const check = await call('POST', '/auth/register/check', { body: { sid, name } })
  const auth = await call('POST', '/auth/register/complete', { body: { ticket: check.ticket, password } })
  const me = await call('GET', '/me', { token: auth.access_token })
  return { sid, name, token: auth.access_token, classId: me.classId }
}

async function waitFor(check, label, timeoutMs = 30_000) {
  const until = Date.now() + timeoutMs
  do {
    const value = await check()
    if (value) return value
    await new Promise((resolve) => setTimeout(resolve, 250))
  } while (Date.now() < until)
  throw new Error(`等待超时：${label}`)
}

function docker(args) {
  const result = spawnSync('docker', args, { encoding: 'utf8', shell: false })
  assert.equal(result.status, 0, 'Synthetic database inspection command failed')
  return result.stdout.trim()
}

function databaseReader() {
  const container = docker(['ps', '--filter', `label=com.docker.compose.project=${project}`, '--filter', 'label=com.docker.compose.service=postgres', '--format', '{{.ID}}'])
  assert.match(container, /^[a-f0-9]+$/, 'Expected exactly one synthetic PostgreSQL container')
  return (query) => {
    assert.match(query.trim(), /^SELECT\b/i, 'E2E database checks must remain read-only')
    return JSON.parse(docker(['exec', container, 'psql', '-U', 'easygpa', '-d', 'easygpa_e2e', '-At', '-v', 'ON_ERROR_STOP=1', '-c', query]))
  }
}

function numericID(value) {
  assert.match(String(value), /^\d+$/, 'Fixture ID must be numeric')
  return String(value)
}

const item = (key, name) => ({ key, name, scoreRule: { type: 'free', min: 1, max: 10 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } })
const config = {
  schemeName: '解封补交合成回归方案',
  weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
  categories: [
    { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    { key: 'moral', name: '思想道德', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [item('activity', '实践活动'), item('other_activity', '其他实践活动')] },
    { key: 'health', name: '身体心理', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
  ],
}

async function main() {
  await assertLocalDevelopmentAPI(origin)
  const db = databaseReader()
  const ops = await call('POST', '/auth/login', { body: { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' } })
  await call('POST', '/ops/tenants', { token: ops.access_token, body: { name: `解封补交回归 ${stamp}`, slug: `unseal-${stamp}`, adminSid: `UA${stamp}`, adminName: '回归班管' } })
  const admin = await register(`UA${stamp}`, '回归班管')
  const roster = [
    { sid: `US${stamp}`, name: '补交同学', role: 'student' },
    { sid: `UP${stamp}`, name: '同班同学', role: 'student' },
    ...['甲', '乙', '丙'].map((name, i) => ({ sid: `UG${i}${stamp}`, name: `回归审核${name}`, role: 'group' })),
  ]
  await call('POST', '/admin/whitelist/import', { token: admin.token, body: { csv: ['sid,name,role', ...roster.map((p) => `${p.sid},${p.name},${p.role}`)].join('\n') } })
  const sessions = []
  for (const row of roster) sessions.push(await register(row.sid, row.name))
  const [student, peer, ...reviewers] = sessions
  const members = (await call('GET', '/admin/users', { token: admin.token })).items
  const everyone = [admin, ...sessions]
  const reviewPool = [admin, ...reviewers]
  for (const person of everyone) person.id = members.find((row) => row.sid === person.sid).id
  const classId = numericID(admin.classId)
  const scheme = await call('POST', '/admin/scheme', { token: admin.token, body: { name: config.schemeName, config } })
  await call('POST', `/admin/scheme/${scheme.id}/publish`, { token: admin.token })
  const open = new Date(Date.now() - 3600_000).toISOString()
  const close = new Date(Date.now() + 86400_000).toISOString()
  await call('PUT', '/admin/timeline', { token: admin.token, body: { open, close, honorRollTopPercent: 30 } })
  for (const key of ['submit', 'edit', 'review', 'appeal', 'arbitrate']) await call('PUT', '/admin/window/capabilities', { token: admin.token, body: { key, on: true } })
  await call('POST', '/admin/gpa/paste', { token: admin.token, body: { text: everyone.map((p) => `${p.sid},85`).join('\n') } })

  const seal = (p) => call('POST', '/me/seal', { token: p.token, body: { phrase: '全部提交完成', sid: p.sid } })
  const confirm = async (p) => call('POST', '/me/scorecard/confirm', { token: p.token, body: { revision: (await scorecard(p)).revision } })
  const scorecard = (p) => call('GET', '/me/scorecard', { token: p.token })
  const seals = async () => (await call('GET', '/admin/seals', { token: admin.token })).items
  async function material(title, withEvidence = false) {
    const row = await call('POST', '/submissions', { token: student.token, body: { category: 'practice', itemKey: 'activity', title, claim: { score: 4 }, note: '仅供本机隔离测试的合成证明。' } })
    if (withEvidence) {
      const pdf = new Blob(['%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\n%%EOF'], { type: 'application/pdf' })
      const presign = await call('POST', `/submissions/${row.id}/evidence/presign`, { token: student.token, body: { filename: 'supplement.pdf', mediaType: 'application/pdf', sizeBytes: pdf.size } })
      const uploadURL = new URL(presign.uploadUrl)
      assert.ok(['127.0.0.1', 'localhost'].includes(uploadURL.hostname), 'Upload remains in the synthetic local object store')
      const form = new FormData()
      for (const [key, value] of Object.entries(presign.uploadFields)) form.append(key, value)
      form.append('file', pdf, 'supplement.pdf')
      const uploaded = await fetch(uploadURL, { method: 'POST', body: form })
      assert.ok(uploaded.ok, `Supplement object upload: HTTP ${uploaded.status}`)
      const complete = await call('POST', `/submissions/${row.id}/evidence/${presign.evidenceId}/complete`, { token: student.token })
      assert.equal(complete.status, 'ready')
      const detail = await call('GET', `/submissions/${row.id}`, { token: student.token })
      assert.ok(detail.evidence.some((e) => e.id === presign.evidenceId && e.status === 'ready'))
    }
    await call('POST', `/submissions/${row.id}/submit`, { token: student.token })
    const assigned = await waitFor(async () => {
      const found = []
      for (const reviewer of reviewPool) {
        const queue = await call('GET', '/review/tasks', { token: reviewer.token })
        if (queue.items.some((a) => a.id === row.id)) found.push(reviewer)
      }
      return found.length === 2 ? found : null
    }, '补交材料双人初审')
    for (const reviewer of assigned) await call('POST', `/review/tasks/${row.id}/decision`, { token: reviewer.token, body: { decision: 'accepted', reason: '合成证明核验完整', spentSeconds: 10 } })
    return row
  }

  const reason = '补交遗漏的合成证明'
  await confirm(student)
  const before = await scorecard(student)
  const original = await material('原有材料')
  assert.notEqual((await scorecard(student)).revision, before.revision)
  for (const p of everyone) await seal(p)
  await confirm(student); await confirm(peer)
  const peerBefore = await scorecard(peer)
  const endpoint = '/admin/seals/' + student.id + '/unseal'
  await call('POST', endpoint, { body: { reason }, status: 401 })
  await denied(endpoint, student, { reason }, 403)
  for (const short of [' ', '补交']) await denied(endpoint, admin, { reason: short }, 400, 'reason_required')
  await call('POST', endpoint, { token: admin.token, body: { reason } })
  assert.equal((await call('GET', '/me/seal', { token: student.token })).sealed, false)
  assert.equal((await call('GET', '/admin/gate', { token: admin.token })).open, false)
  assert.equal((await scorecard(peer)).revision, peerBefore.revision)
  await confirm(student)
  const acknowledged = await scorecard(student)
  await call('PUT', '/admin/window/capabilities', { token: admin.token, body: { key: 'submit', on: false } })
  await call('POST', '/submissions', { token: student.token, body: { category: 'practice', itemKey: 'activity', title: '关闭时不得补交', claim: { score: 4 } }, rejected: true })
  await call('PUT', '/admin/window/capabilities', { token: admin.token, body: { key: 'submit', on: true } })
  const supplement = await material('补交的第二份证明', true)
  assert.notEqual(supplement.id, original.id)
  const changed = await scorecard(student)
  assert.ok(changed.scorecard.items.some((row) => row.id === supplement.id))
  assert.equal(changed.confirmation.recheck, true)
  await denied('/me/scorecard/confirm', student, { revision: acknowledged.revision }, 409, 'scorecard_changed')
  await seal(student)
  assert.equal((await call('GET', '/admin/gate', { token: admin.token })).open, true)
  const settlement = await call('POST', '/admin/settle', { token: admin.token })
  await call('POST', endpoint, { token: admin.token, body: { reason } })
  assert.equal((await call('GET', '/me/score', { token: peer.token })).stale, true)
  assert.equal((await scorecard(peer)).confirmation.confirmed, true)
  assert.equal(db('SELECT count(*)::int FROM settlement_invalidation WHERE class_id=' + classId + ' AND run_id=' + numericID(settlement.runId)), 1)
  await denied(endpoint, admin, { reason }, 409, 'not_sealed')
  await call('PUT', '/admin/timeline', { token: admin.token, body: { open, close: new Date(Date.now() - 60000).toISOString(), lockdown: new Date(Date.now() - 30000).toISOString() } })
  await denied('/admin/seals/' + peer.id + '/unseal', admin, { reason }, 409, 'class_locked')
  await call('PUT', '/admin/timeline', { token: admin.token, body: { open, close, lockdown: null } })
  console.log('✓ 解封补交真实 HTTP 回归通过')
}

main().catch((error) => {
  console.error(`解封补交 E2E 失败：${error.message}`)
  process.exitCode = 1
})
