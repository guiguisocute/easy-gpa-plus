#!/usr/bin/env node
// API/Worker/Garage regression in a new local synthetic class. The existing
// forced gate isolates file generation from the separate review regressions.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
await assertLocalDevelopmentAPI(origin)
const stamp = Date.now().toString(36)
async function call(method, path, token, body, expected = 200) {
  const response = await fetch(`${origin}/api/v1${path}`, {
    method, headers: e2eHeaders({ 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const result = await response.json()
  assert.equal(response.status, expected, `${method} ${path}: ${result.code ?? ''} ${result.message ?? ''}`)
  return result
}
const ops = await call('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' })
const adminSid = `CE${stamp}`
await call('POST', '/ops/tenants', ops.access_token, { name: `学院报表回归 ${stamp}`, slug: `college-${stamp}`, adminSid, adminName: '报表班管' }, 201)
const ticket = await call('POST', '/auth/register/check', null, { sid: adminSid, name: '报表班管' }, 202)
const password = 'college-export-test-password'
const auth = await call('POST', '/auth/register/complete', null, { ticket: ticket.ticket, password }, 201)
const admin = auth.access_token
const sids = Array.from({ length: 9 }, (_, i) => `CE${stamp}${i}`)
await call('POST', '/admin/whitelist/import', admin, { csv: ['sid,name,role,gender', ...sids.map((sid, i) => `${sid},报表同学${i},student,${i % 2 ? '男' : '女'}`)].join('\n') })
const config = {
  schemeName: '学院报表回归方案', weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
  categories: ['major', 'moral', 'practice', 'health'].map((key) => ({ key, name: { major: '专业素质', moral: '思想道德', practice: '实践创新', health: '身体心理' }[key], maxTotal: 100, baseItems: [], penaltyItems: [], items: [] })),
}
const scheme = await call('POST', '/admin/scheme', admin, { name: config.schemeName, config }, 201)
await call('POST', `/admin/scheme/${scheme.id}/publish`, admin)
const timeline = { open: new Date(Date.now() - 3600_000).toISOString(), close: new Date(Date.now() + 86400_000).toISOString(), lockdown: null, collegeName: '报表测试学院', enrollmentClass: '24级计算机科学与技术2班', academicYear: '2025-2026', awards: [{ name: '一等综合素质奖学金', topPercent: 20 }, { name: '二等综合素质奖学金', topPercent: 30 }, { name: '三等综合素质奖学金', topPercent: 30 }] }
await call('PUT', '/admin/timeline', admin, timeline)
await call('POST', '/admin/gpa/paste', admin, { text: [adminSid, ...sids].map((sid, i) => `${sid},${90.125 - i}`).join('\n') })
await call('POST', '/admin/gate/force', admin, { reason: '本地合成数据学院报表导出回归' })
await call('POST', '/admin/settle', admin, undefined, 201)
const job = await limitedExport('college')
let completed
for (let i = 0; i < 90; i++) {
  const current = await call('GET', `/admin/export/${job.jobId}`, admin)
  assert.notEqual(current.status, 'failed', current.error)
  if (current.status === 'complete') { completed = current; break }
  await new Promise((done) => setTimeout(done, 1000))
}
assert.ok(completed, 'export worker did not complete')
const download = await fetch(completed.downloadUrl)
assert.equal(download.status, 200)
assert.match(download.headers.get('content-type'), /^application\/zip/)
assert.match(decodeURIComponent(download.headers.get('content-disposition')), /学院报表\.zip/)
const zip = Buffer.from(await download.arrayBuffer())
assert.equal(completed.sizeBytes, zip.length)
assert.equal(completed.filename, '学院报表.zip')
assert.match(new URL(completed.downloadUrl).pathname, /-college-round2-v2\.zip$/, 'the worker must version rounded reports so old cached files are not reused')
assert.ok(completed.etag)
const rangeDownload = await fetch(completed.downloadUrl, { headers: { Range: 'bytes=7-1030', 'If-Match': `"${completed.etag}"` } })
assert.equal(rangeDownload.status, 206)
assert.equal(rangeDownload.headers.get('content-range'), `bytes 7-1030/${zip.length}`)
assert.deepEqual(Buffer.from(await rangeDownload.arrayBuffer()), zip.subarray(7, 1031))
const preflight = await fetch(completed.downloadUrl, { method: 'OPTIONS', headers: { Origin: process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:45173', 'Access-Control-Request-Method': 'GET', 'Access-Control-Request-Headers': 'range,if-match' } })
assert.ok(preflight.ok, 'Range/If-Match must pass browser CORS preflight')
// Exercise the real limiter, not the fixture-only bypass. Four products must
// fit the burst and arbitrary reuse must not spend new-generation tokens.
async function limitedExport(kind) {
  const response = await fetch(`${origin}/api/v1/admin/export`, { method: 'POST', headers: { Authorization: `Bearer ${admin}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ kind }) })
  assert.equal(response.status, 202, `real export limiter rejected ${kind}`)
  return response.json()
}
for (const kind of ['summary', 'detail', 'archive']) await limitedExport(kind)
for (let i = 0; i < 6; i++) assert.equal((await limitedExport('college')).jobId, job.jobId)
assert.equal(zip.readUInt32LE(), 0x04034b50)
for (const filename of ['附件1-2.docx', '附件3：综合素质奖学金和“三好学生”名单公示.docx', '附件4：综合素质奖学金获得者信息采集表.xlsx', '附件5：综合素质奖学金、“三好学生”证书套打信息录入模板.xlsx', '强制结算警告.txt']) assert.ok(zip.includes(Buffer.from(filename)), `missing ${filename}`)
const duplicate = await call('POST', '/admin/export', admin, { kind: 'college' }, 202)
assert.equal(duplicate.jobId, job.jobId)
assert.equal(duplicate.deduplicated, true)
await call('PUT', '/admin/timeline', admin, { ...timeline, collegeName: '更新后的报表测试学院' })
const refreshed = await call('POST', '/admin/export', admin, { kind: 'college' }, 202)
assert.notEqual(refreshed.jobId, job.jobId, 'identity changes must invalidate export cache')
await call('GET', `/admin/export/${job.jobId}`, null, undefined, 401)
if (process.env.EASYGPA_COLLEGE_QA_DIR) {
  const dir = resolve(process.env.EASYGPA_COLLEGE_QA_DIR)
  await mkdir(dir, { recursive: true })
  await writeFile(resolve(dir, 'api-export.zip'), zip)
  // Only generated synthetic local accounts are persisted for browser QA.
  await writeFile(resolve(dir, 'browser-fixture.json'), JSON.stringify({ adminSid, password, origin }), { mode: 0o600 })
}
console.log('college_export_e2e=ok: API → Worker → Garage ZIP, four original files, cache reuse/invalidation and 401 boundary')
