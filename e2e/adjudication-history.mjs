/* Personal adjudication history against a loopback development API.
   Uses synthetic members only; auth responses are never printed. */
import assert from 'node:assert/strict'
import { mkdirSync, writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
await assertLocalDevelopmentAPI(origin)
const stamp = Date.now().toString(36)
const password = 'history-e2e-synthetic-password'
async function call(method, path, token, body, expected = 200) {
  const headers = e2eHeaders({ Accept: 'application/json' })
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const response = await fetch(`${origin}/api/v1${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await response.json().catch(() => null)
  if (expected === 200) assert.ok(response.ok, `${method} ${path}: HTTP ${response.status} (${data?.error?.code ?? data?.code ?? ''})`)
  else assert.equal(response.status, expected, `${method} ${path}`)
  return data
}
async function register(sid, name) {
  const check = await call('POST', '/auth/register/check', null, { sid, name })
  const session = await call('POST', '/auth/register/complete', null, { ticket: check.ticket, password })
  return { sid, name, token: session.access_token }
}
const ops = await call('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' })
await call('POST', '/ops/tenants', ops.access_token, { name: `处理历史回归 ${stamp}`, slug: `history-${stamp}`, adminSid: `HA${stamp}`, adminName: '历史回归班管' })
const admin = await register(`HA${stamp}`, '历史回归班管')
const people = [{ sid: `HS${stamp}`, name: '历史回归学生', role: 'student' }, { sid: `HG1${stamp}`, name: '历史审核甲', role: 'group' }, { sid: `HG2${stamp}`, name: '历史审核乙', role: 'group' }]
await call('POST', '/admin/whitelist/import', admin.token, { csv: ['sid,name,role', ...people.map((p) => `${p.sid},${p.name},${p.role}`)].join('\n') })
const [student, group1, group2] = await Promise.all(people.map((p) => register(p.sid, p.name)))
const config = {
  schemeName: '处理历史回归方案', weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
  categories: [
    { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    { key: 'moral', name: '思想道德', maxTotal: 100, baseItems: [{ key: 'moral_base', name: '基础分', full: 80 }], penaltyItems: [], items: [] },
    { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [
      { key: 'activity', name: '实践活动', scoreRule: { type: 'per_unit', unit: '项', per: 2, cap: 8 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } },
    ] },
    { key: 'health', name: '身体心理', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
  ],
}
const scheme = await call('POST', '/admin/scheme', admin.token, { name: config.schemeName, config })
await call('POST', `/admin/scheme/${scheme.id}/publish`, admin.token)
await call('PUT', '/admin/window', admin.token, { open: new Date(Date.now() - 3600_000).toISOString(), close: new Date(Date.now() + 30 * 86400_000).toISOString(), honorRollTopPercent: 20 })
for (const key of ['submit', 'edit', 'appeal', 'review', 'arbitrate']) await call('PUT', '/admin/window/capabilities', admin.token, { key, on: true })
const list = (query = '', token = admin.token) => call('GET', `/admin/adjudication-history${query}`, token)
assert.equal((await list()).total, 0)
await call('GET', '/admin/adjudication-history', null, undefined, 401)
await call('GET', '/admin/adjudication-history', student.token, undefined, 403)
await call('GET', '/admin/adjudication-history', group1.token, undefined, 403)
const submission = await call('POST', '/submissions', student.token, { category: 'practice', itemKey: 'activity', title: '历史回溯实践活动', claim: { quantity: 2 } })
const pdf = Buffer.from('%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n')
const upload = await call('POST', `/submissions/${submission.id}/evidence/presign`, student.token, { filename: 'history-evidence.pdf', mediaType: 'application/pdf', sizeBytes: pdf.length })
const form = new FormData()
for (const [key, value] of Object.entries(upload.uploadFields ?? {})) form.append(key, value)
form.append('file', new Blob([pdf], { type: 'application/pdf' }), 'history-evidence.pdf')
const uploaded = await fetch(upload.uploadUrl, { method: 'POST', body: form })
assert.ok(uploaded.ok, `local evidence upload: HTTP ${uploaded.status}`)
await call('POST', `/submissions/${submission.id}/evidence/${upload.evidenceId}/complete`, student.token)
await call('POST', `/submissions/${submission.id}/submit`, student.token)
let assigned = []
for (let i = 0; i < 40; i++) {
  assigned = []
  for (const who of [admin, group1, group2]) {
    const tasks = await call('GET', '/review/tasks', who.token)
    if (tasks.items.some((row) => row.id === submission.id)) assigned.push(who)
  }
  if (assigned.length === 2) break
  await new Promise((resolve) => setTimeout(resolve, 500))
}
assert.equal(assigned.length, 2, 'two reviewers assigned')
await call('POST', `/review/tasks/${submission.id}/decision`, assigned[0].token, { decision: 'accepted', reason: '回归核对实践活动记录', spentSeconds: 10 })
await call('POST', `/review/tasks/${submission.id}/decision`, assigned[1].token, { decision: 'adjusted', score: 2, reason: '回归核对实践活动次数', spentSeconds: 10 })
assert.equal((await list()).total, 0, 'pending decisions and review actions stay out of adjudication history')
await call('POST', `/admin/submissions/${submission.id}/arbitrate`, admin.token, { score: 3, reason: '仲裁历史理由：核对材料后认定三分' })
const first = (await list()).items[0]
assert.equal(first.kind, 'arbitration')
assert.equal(first.score, 3)
assert.equal(first.studentId, student.sid)
assert.equal((await list('?q=' + encodeURIComponent('仲裁历史理由'))).total, 1)
assert.equal((await list('?student=' + student.sid)).total, 1)
assert.equal((await list('?student=no-such-student')).total, 0)
const detail = await call('GET', `/admin/adjudication-history/${first.id}`, admin.token)
assert.equal(detail.reason, '仲裁历史理由：核对材料后认定三分')
assert.equal(detail.reviews.length, 2)
assert.equal(detail.evidence.length, 1)
assert.equal(detail.evidence[0].name, 'history-evidence.pdf')
assert.equal(detail.evidence[0].objectKey, undefined)
assert.equal(detail.evidence[0].createdBy, undefined)
const download = await call('GET', `/evidence/${detail.evidence[0].id}/url?disposition=attachment`, admin.token)
const downloaded = await fetch(download.url)
assert.ok(downloaded.ok, 'history evidence download URL remains accessible')
assert.deepEqual(Buffer.from(await downloaded.arrayBuffer()), pdf)

// A base-score appeal goes through the real appeal lifecycle and has its own
// immutable final decision without a submission arbitration stopping appeals.
const targetStudent = (await call('GET', '/review/students', group1.token)).items.find((row) => row.sid === student.sid)
const proposal = await call('POST', '/review/objections', group1.token, { kind: 'base', studentUserId: targetStudent.userId, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 70, basis: '回归基础项原始提案说明' })
await call('POST', '/review/objections/submit', group1.token, { ids: [proposal.id] })
await call('POST', `/admin/objections/${proposal.id}/decide`, admin.token, { action: 'apply', reason: '提案历史理由：基础项认定七十分' })
const objectionHistory = (await list('?kind=objection')).items[0]
assert.equal(objectionHistory.score, 70)
assert.equal((await call('GET', `/admin/adjudication-history/${objectionHistory.id}`, admin.token)).sourceReason, '回归基础项原始提案说明')
const bases = (await call('GET', '/me/base-items', student.token)).items
const base = bases.find((row) => row.itemKey === 'moral_base' && row.id)
assert.ok(base, 'base score recorded for appeal')
{
  const appeal = await call('POST', '/appeals', student.token, { targetType: 'base_score', targetId: base.id, reason: '回归申诉请求核对基础分', proposedScore: 75 })
  await call('POST', `/admin/appeals/${appeal.id}/final`, admin.token, { score: 75, reason: '申诉历史理由：核对后认定七十五分' })
  const row = (await list('?kind=appeal')).items[0]
  assert.equal(row.score, 75)
  assert.equal((await call('GET', `/admin/adjudication-history/${row.id}`, admin.token)).sourceReason, '回归申诉请求核对基础分')
}
await call('POST', `/admin/submissions/${submission.id}/force-reject`, admin.token, { reason: '后续回归强制驳回：原材料不符合认定条件' })
const after = await list()
assert.equal(after.items.find((row) => row.id === first.id).score, 3, 'later rejection cannot rewrite original score')
const rejection = after.items.find((row) => row.kind === 'force_reject')
assert.equal(rejection.score, 0, 'zero is retained, not converted to missing score')
assert.equal(rejection.beforeScore, 3)
const old = await call('GET', `/admin/adjudication-history/${first.id}`, admin.token)
assert.equal(old.score, 3)
assert.equal(old.currentScore, 0)
const one = await list('?page_size=1'), two = await list('?page_size=1&page=2')
assert.equal(one.total, after.total)
assert.notEqual(one.items[0].id, two.items[0].id)
assert.equal((await list('?from=2000-01-01T00:00:00Z&until=2100-01-01T00:00:00Z')).total, after.total)
assert.equal((await list('?until=2000-01-01T00:00:00Z')).total, 0)
await call('GET', '/admin/adjudication-history?kind=invalid', admin.token, undefined, 400)
await call('GET', '/admin/adjudication-history?from=invalid', admin.token, undefined, 400)
await call('GET', '/admin/adjudication-history?from=2100-01-01T00:00:00Z&until=2000-01-01T00:00:00Z', admin.token, undefined, 400)
await call('POST', '/ops/tenants', ops.access_token, { name: `历史隔离回归 ${stamp}`, slug: `history-other-${stamp}`, adminSid: `HX${stamp}`, adminName: '其他回归班管' })
const other = await register(`HX${stamp}`, '其他回归班管')
assert.equal((await list('', other.token)).total, 0)
await call('GET', `/admin/adjudication-history/${first.id}`, other.token, undefined, 404)
await call('GET', `/admin/adjudication-history/${first.id}`, group1.token, undefined, 403)
mkdirSync('output/playwright', { recursive: true })
writeFileSync('output/playwright/adjudication-history-fixture.json', JSON.stringify({ sid: admin.sid, studentSid: student.sid, firstId: first.id }))
console.log('PASS: personal history, real arbitration/rejection, snapshots, details, filters, pagination, role and tenant isolation')
