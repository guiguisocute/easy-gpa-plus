#!/usr/bin/env node
// Real API/Garage regression. Run against a disposable, loopback-only E2E stack.
import assert from 'node:assert/strict'
import { mkdirSync, writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:38080')
const stamp = Date.now().toString(36)
const password = 'markdown-e2e-synthetic-password'
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64')
const pdf = Buffer.from('%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n')

async function call(method, path, person, body, expected) {
  const headers = e2eHeaders({ Accept: 'application/json' })
  if (person) headers.Authorization = `Bearer ${person.token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(`${origin}/api/v1${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const result = await res.json().catch(() => null)
  const label = `${method} ${path}: ${res.status} (${result?.code ?? 'ok'})`
  if (expected) assert.equal(res.status, expected, label)
  else assert.ok(res.ok, label)
  return result
}
async function denied(method, path, person, body) {
  const res = await fetch(`${origin}/api/v1${path}`, { method, headers: e2eHeaders({ 'Content-Type': 'application/json', Authorization: `Bearer ${person.token}` }), body: body === undefined ? undefined : JSON.stringify(body) })
  assert.ok(res.status === 403 || res.status === 404, `${method} ${path} must deny access (${res.status})`)
}
async function register(sid, name) {
  const check = await call('POST', '/auth/register/check', null, { sid, name })
  const auth = await call('POST', '/auth/register/complete', null, { ticket: check.ticket, password })
  return { sid, name, token: auth.access_token }
}
async function waitFor(check, label) {
  const until = Date.now() + 60000
  do {
    const value = await check()
    if (value) return value
    await new Promise(resolve => setTimeout(resolve, 500))
  } while (Date.now() < until)
  throw new Error(`Timed out: ${label}`)
}
function metadata(image = false) { return { filename: image ? '截图[核对].png' : '核验说明.pdf', mediaType: image ? 'image/png' : 'application/pdf', sizeBytes: (image ? png : pdf).length } }
async function upload(base, person, image = false) {
  const pre = await call('POST', `${base}/presign`, person, metadata(image))
  const form = new FormData()
  for (const [key, value] of Object.entries(pre.uploadFields)) form.append(key, value)
  form.append('file', new Blob([image ? png : pdf], { type: metadata(image).mediaType }), metadata(image).filename)
  const res = await fetch(pre.uploadUrl, { method: 'POST', body: form })
  assert.ok(res.ok, `Object upload: ${res.status}`)
  const done = await call('POST', `${base}/${pre.evidenceId}/complete`, person)
  assert.equal(done.status, 'ready')
  return done.evidenceId
}
const ref = id => `![核验文件](evidence:${id})`
const link = (id, person) => call('GET', `/evidence/${id}/url?disposition=inline`, person)
const noteBase = (owner, id) => `/${owner}/${id}/notes`
const config = {
  schemeName: 'Markdown 附件回归', weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
  categories: [
    { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    { key: 'moral', name: '思想道德', maxTotal: 100, baseItems: [{ key: 'base', name: '基础分', full: 80 }], penaltyItems: [], items: [] },
    { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [{ key: 'activity', name: '实践活动', scoreRule: { type: 'free', min: 0, max: 10 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } }] },
    { key: 'health', name: '身体心理', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
  ],
}

await assertLocalDevelopmentAPI(origin)
const opsAuth = await call('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' })
const ops = { token: opsAuth.access_token }
await call('POST', '/ops/tenants', ops, { name: `附件回归 ${stamp}`, slug: `markdown-${stamp}`, adminSid: `MA${stamp}`, adminName: '附件回归班管' })
const admin = await register(`MA${stamp}`, '附件回归班管')
const roster = [
  { sid: `MS${stamp}`, name: '附件回归学生', role: 'student' },
  { sid: `MT${stamp}`, name: '附件回归同学', role: 'student' },
  ...Array.from({ length: 4 }, (_, i) => ({ sid: `MG${i}${stamp}`, name: `附件回归审核${i + 1}`, role: 'group' })),
]
await call('POST', '/admin/whitelist/import', admin, { csv: ['sid,name,role', ...roster.map(p => `${p.sid},${p.name},${p.role}`)].join('\n') })
const people = []
for (const p of roster) people.push(await register(p.sid, p.name))
const [student, classmate, ...reviewers] = people
const members = (await call('GET', '/admin/users', admin)).items
for (const p of [admin, ...people]) p.id = members.find(row => row.sid === p.sid).id
// 班管有全班读取权限；背靠背测试要使用两个普通小组席位。
await call('PUT', `/admin/dispatch/reviewers/${admin.id}`, admin, { paused: true })
const draft = await call('POST', '/admin/scheme', admin, { name: config.schemeName, config })
await call('POST', `/admin/scheme/${draft.id}/publish`, admin)
await call('PUT', '/admin/window', admin, { open: new Date(Date.now() - 3600000).toISOString(), close: new Date(Date.now() + 30 * 86400000).toISOString(), honorRollTopPercent: 20 })
for (const key of ['submit', 'edit', 'appeal', 'review', 'arbitrate', 'studentReport']) await call('PUT', '/admin/window/capabilities', admin, { key, on: true })
await call('POST', '/ops/tenants', ops, { name: `附件外班 ${stamp}`, slug: `markdown-other-${stamp}`, adminSid: `MO${stamp}`, adminName: '外班班管' })
const outsider = await register(`MO${stamp}`, '外班班管')

async function submission(person, title) {
  const row = await call('POST', '/submissions', person, { category: 'practice', itemKey: 'activity', title, claim: { score: 4 }, note: '仅用于本机附件回归的合成说明' })
  await call('POST', `/submissions/${row.id}/submit`, person)
  const assigned = await waitFor(async () => {
    const assigned = []
    for (const p of [admin, ...reviewers]) {
      const inbox = await call('GET', '/review/tasks', p)
      if (inbox.items.some(t => t.id === row.id)) assigned.push(p)
    }
    return assigned.length === 2 ? assigned : null
  }, 'initial review assignments')
  return { ...row, assigned }
}
const initial = await submission(student, '附件回归已定分材料')
const base = noteBase('submissions', initial.id)
const imageID = await upload(base, initial.assigned[0], true)
assert.equal((await link(imageID, initial.assigned[0])).inline, true)
await denied('GET', `/evidence/${imageID}/url`, initial.assigned[1])
await denied('POST', `${base}/presign`, classmate, metadata())
await denied('POST', `${base}/presign`, outsider, metadata())
await denied('GET', `/evidence/${imageID}/url`, outsider)
await call('POST', `${base}/presign`, initial.assigned[0], { filename: 'active.svg', mediaType: 'image/svg+xml', sizeBytes: 20 }, 422)
for (const p of initial.assigned) await call('POST', `/review/tasks/${initial.id}/decision`, p, { decision: 'accepted', reason: `核验图片确认材料一致 ${p === initial.assigned[0] ? ref(imageID) : ''}`, spentSeconds: 20 })
const stored = await call('GET', `/submissions/${initial.id}`, student)
assert.ok(stored.noteEvidence.some(e => e.id === imageID))
assert.ok(stored.reviews.some(r => r.why.includes(`evidence:${imageID}`)))
console.log('PASS initial review: upload, persisted reason, cross-class denial, peer draft privacy, active-content rejection')

const appeal = await call('POST', '/appeals/draft', student, { targetType: 'submission', targetId: initial.id })
const claimFile = await upload(`/appeals/${appeal.id}/evidence`, student)
await call('POST', `/appeals/${appeal.id}/submit`, student, { reason: `补充核验材料请求复评 ${ref(claimFile)}`, category: 'practice', itemKey: 'activity', score: 5 })
const appealBase = noteBase('appeals', appeal.id)
const appealFile = await upload(appealBase, initial.assigned[0])
await denied('GET', `/evidence/${appealFile}/url`, initial.assigned[1])
await call('POST', `/review/appeals/${appeal.id}/rereview`, initial.assigned[0], { decision: 'uphold', score: 4, reason: `复核文件维持结论 ${ref(appealFile)}`, spentSeconds: 20 })
await call('POST', `/review/appeals/${appeal.id}/rereview`, initial.assigned[1], { decision: 'adjust', score: 5, reason: '复核补充材料应当增加一分', spentSeconds: 20 })
await link(appealFile, initial.assigned[1])
const finalFile = await upload(appealBase, admin)
await call('POST', `/admin/appeals/${appeal.id}/final`, admin, { score: 4, reason: `最终核对附件维持原分 ${ref(finalFile)}`, category: 'practice', itemKey: 'activity' })
const appealStored = await call('GET', `/appeals/${appeal.id}`, student)
assert.ok(appealStored.noteEvidence.some(e => e.id === finalFile))
console.log('PASS appeal: student file, reviewer privacy/reveal, administrator attachment, student can reopen result')

const report = await call('POST', '/reports', classmate, { kind: 'base', studentUserId: student.id, category: 'moral', itemKey: 'base', targetId: null, proposedScore: 60, basis: '合成核验记录与基础分存在差异，请复核' })
const reportReviewers = await waitFor(async () => {
  const assigned = []
  for (const p of [admin, ...reviewers]) {
    if ((await call('GET', '/review/tasks', p)).items.some(t => t.id === `report:${report.id}`)) assigned.push(p)
  }
  return assigned.length === 2 ? assigned : null
}, 'report review assignments')
const reportBase = noteBase('report-reviews', report.id)
const reportFile = await upload(reportBase, reportReviewers[0])
await denied('GET', `/evidence/${reportFile}/url`, reportReviewers[1])
await denied('POST', `${reportBase}/presign`, classmate, metadata())
await denied('GET', `/evidence/${reportFile}/url`, student)
assert.equal((await call('GET', `/review/reports/${report.id}`, reportReviewers[1])).noteEvidence.length, 0)
await call('POST', `/review/reports/${report.id}/decision`, reportReviewers[0], { decision: 'uphold', reason: `核验附件符合举报主张 ${ref(reportFile)}`, spentSeconds: 20 })
await call('POST', `/review/reports/${report.id}/decision`, reportReviewers[1], { decision: 'reject', reason: '核验说明不足维持原基础分', spentSeconds: 20 })
await link(reportFile, reportReviewers[1])
const reportFinal = await upload(reportBase, admin)
await call('POST', `/admin/reports/${report.id}/final`, admin, { score: 80, reason: `核验原件不调整分数 ${ref(reportFinal)}` })
assert.ok((await call('GET', '/admin/reports', admin)).items.find(r => r.id === report.id).noteEvidence.some(e => e.id === reportFinal))
console.log('PASS report: review attachments separated from anonymous claim, peer reveal and final attachment')

const live = await call('GET', '/me/scorecard', student)
assert.ok(live.scorecard.items.some((row) => row.id === initial.id))
await call('POST', '/me/scorecard/confirm', student, { revision: live.revision })
console.log('PASS live scorecard: own evidence and lightweight acknowledgement')

// Keep one initial-review item available for interactive browser verification.
const interactive = await submission(classmate, '浏览器验证上传按钮')
mkdirSync('output/playwright', { recursive: true })
writeFileSync('output/playwright/markdown-fixtures.json', JSON.stringify({ password, admin: { sid: admin.sid }, reviewer: { sid: interactive.assigned[0].sid }, submissionId: interactive.id }, null, 2))
writeFileSync('output/playwright/markdown-test.png', png)
writeFileSync('output/playwright/markdown-test.pdf', pdf)
writeFileSync('output/playwright/unsupported.svg', '<svg/>')
console.log('PASS fixtures ready for browser verification; no production data used')
