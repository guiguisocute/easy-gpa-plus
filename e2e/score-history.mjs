// Real anonymous item history and evidence authorization, using synthetic local accounts.
import assert from 'node:assert/strict'
import { mkdirSync, writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
await assertLocalDevelopmentAPI(origin)
const stamp = Date.now().toString(36)
const password = 'score-history-synthetic-password'
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64')
async function call(method, path, person, body, expected) {
  const headers = e2eHeaders({ Accept: 'application/json' })
  if (person) headers.Authorization = `Bearer ${person.token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(`${origin}/api/v1${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await res.json().catch(() => null)
  const label = `${method} ${path}: HTTP ${res.status} (${data?.code ?? ''})`
  if (expected) assert.equal(res.status, expected, label)
  else assert.ok(res.ok, label)
  return data
}
async function register(sid, name) {
  const check = await call('POST', '/auth/register/check', null, { sid, name })
  const auth = await call('POST', '/auth/register/complete', null, { ticket: check.ticket, password })
  return { sid, name, token: auth.access_token }
}
async function upload(path, person, filename) {
  const pre = await call('POST', `${path}/presign`, person, { filename, mediaType: 'image/png', sizeBytes: png.length })
  const form = new FormData()
  for (const [key, value] of Object.entries(pre.uploadFields)) form.append(key, value)
  form.append('file', new Blob([png], { type: 'image/png' }), filename)
  assert.ok((await fetch(pre.uploadUrl, { method: 'POST', body: form })).ok, 'Synthetic image uploaded')
  await call('POST', `${path}/${pre.evidenceId}/complete`, person)
  return pre.evidenceId
}
const opsAuth = await call('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' })
const ops = { token: opsAuth.access_token }
await call('POST', '/ops/tenants', ops, { name: `计分轨迹回归 ${stamp}`, slug: `score-history-${stamp}`, adminSid: `SA${stamp}`, adminName: '轨迹回归班管' })
const admin = await register(`SA${stamp}`, '轨迹回归班管')
const roster = [
  { sid: `SS${stamp}`, name: '轨迹回归学生', role: 'student' },
  { sid: `ST${stamp}`, name: '轨迹回归同学', role: 'student' },
  ...Array.from({ length: 4 }, (_, i) => ({ sid: `SG${i}${stamp}`, name: `轨迹审核${i + 1}`, role: 'group' })),
]
await call('POST', '/admin/whitelist/import', admin, { csv: ['sid,name,role', ...roster.map(p => `${p.sid},${p.name},${p.role}`)].join('\n') })
const people = []
for (const p of roster) people.push(await register(p.sid, p.name))
const [student, classmate, ...reviewers] = people
const members = (await call('GET', '/admin/users', admin)).items
for (const p of [admin, ...people]) p.id = members.find(row => row.sid === p.sid).id
await call('PUT', `/admin/dispatch/reviewers/${admin.id}`, admin, { paused: true })
const config = {
  schemeName: '匿名计分轨迹回归', weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
  categories: [
    { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    { key: 'moral', name: '思想道德', maxTotal: 100, baseItems: [{ key: 'base', name: '基础分', full: 80 }], penaltyItems: [{ key: 'absence', name: '无故缺勤', per: -1 }], items: [] },
    { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [{ key: 'activity', name: '实践活动', scoreRule: { type: 'free', min: 0, max: 10 }, evidence: { required: false, types: ['png'], maxMb: 10 } }] },
    { key: 'health', name: '身体心理', maxTotal: 100, baseItems: [], penaltyItems: [], items: [{ key: 'contest', name: '文体竞赛获奖', scoreRule: { type: 'enum', options: [{ label: '校级一等奖', score: 4 }, { label: '院级一等奖', score: 2 }] }, evidence: { required: false, types: ['png'], maxMb: 10 } }] },
  ],
}
const scheme = await call('POST', '/admin/scheme', admin, { name: config.schemeName, config })
await call('POST', `/admin/scheme/${scheme.id}/publish`, admin)
await call('PUT', '/admin/window', admin, { open: new Date(Date.now() - 3600000).toISOString(), close: new Date(Date.now() + 30 * 86400000).toISOString(), honorRollTopPercent: 20 })
for (const key of ['submit', 'edit', 'appeal', 'review', 'arbitrate', 'studentReport']) await call('PUT', '/admin/window/capabilities', admin, { key, on: true })
await call('POST', '/ops/tenants', ops, { name: `轨迹外班 ${stamp}`, slug: `score-history-other-${stamp}`, adminSid: `SO${stamp}`, adminName: '轨迹外班班管' })
const outsider = await register(`SO${stamp}`, '轨迹外班班管')

const sub = await call('POST', '/submissions', student, { category: 'practice', itemKey: 'activity', title: '可追溯实践活动', claim: { score: 4 } })
const claimFile = await upload(`/submissions/${sub.id}/evidence`, student, '学生申报原件.png')
const studentNoteFile = await upload(`/submissions/${sub.id}/evidence`, student, '学生说明附件.png')
await call('PUT', `/submissions/${sub.id}`, student, { category: 'practice', itemKey: 'activity', title: '可追溯实践活动', claim: { score: 4 }, note: `活动补充说明 ![学生说明附件](evidence:${studentNoteFile})` })
const path = `/review/score-history/submission/${sub.id}`
const studentPath = `/class-penalties/submission/${sub.id}/history`
await call('GET', path, null, undefined, 401)
await call('GET', path, classmate, undefined, 403)
await call('GET', path, reviewers[0], undefined, 404)
await call('POST', `/submissions/${sub.id}/submit`, student)
let assigned = []
for (let i = 0; i < 60 && assigned.length !== 2; i++) {
  assigned = []
  for (const p of reviewers) if ((await call('GET', '/review/tasks', p)).items.some(row => row.id === sub.id)) assigned.push(p)
  if (assigned.length !== 2) await new Promise(resolve => setTimeout(resolve, 500))
}
assert.equal(assigned.length, 2)
const observer = reviewers.find(p => !assigned.includes(p))
const initialFile = await upload(`/submissions/${sub.id}/notes`, assigned[0], '审核依据原件.png')
const stagedFile = await upload(`/submissions/${sub.id}/notes`, admin, '尚未公开的班管备注.png')
const adminDetail = (await call('GET', `/admin/submissions?id=${sub.id}`, admin)).items[0]
assert.deepEqual(adminDetail.evidence.map(file => file.id), [claimFile, studentNoteFile], '材料详情的附件包含学生原件与说明，审核备注仍由已公开轨迹提供')
await call('GET', `/evidence/${studentNoteFile}/url`, admin)
const ref = (id, label = '保密文件名') => `![${label}](evidence:${id})`
await call('POST', `/review/tasks/${sub.id}/decision`, assigned[0], { decision: 'accepted', reason: `初审核验活动记录 ${ref(initialFile)}，跨作者引用不应公开草稿 ${ref(stagedFile)}`, spentSeconds: 10 })
await call('GET', path, observer, undefined, 404)
await call('GET', `/evidence/${initialFile}/url`, observer, undefined, 403)
await call('POST', `/review/tasks/${sub.id}/decision`, assigned[1], { decision: 'accepted', reason: '第二份初审核验一致', spentSeconds: 10 })

function privateProjection(history, files) {
  for (const row of history.events) assert.deepEqual(Object.keys(row).sort(), ['kind', 'at', 'status', 'score', 'beforeScore', 'reason', 'round', 'superseded'].sort())
  for (const who of [admin, ...people]) {
    assert.ok(!JSON.stringify(history).includes(who.sid), 'Student IDs omitted from history')
    assert.ok(!JSON.stringify(history).includes(who.name), 'Names omitted from history')
  }
  for (const file of history.evidence) {
    assert.equal(file.key, undefined)
    assert.equal(file.createdBy, undefined)
    assert.equal(file.objectKey, undefined)
  }
  if (!files) {
    assert.deepEqual(history.evidence, [])
    assert.ok(!JSON.stringify(history).includes('evidence:'))
    assert.ok(!JSON.stringify(history).includes('保密文件名'))
  }
}
async function verify() {
  const groupHistory = await call('GET', path, observer)
  const publicHistory = await call('GET', studentPath, classmate)
  privateProjection(groupHistory, true)
  privateProjection(publicHistory, false)
  assert.deepEqual(groupHistory.events.map(({ reason, ...rest }) => rest), publicHistory.events.map(({ reason, ...rest }) => rest), 'Both audiences see the same decisions')
  return groupHistory
}
let history = await verify()
assert.equal(history.events.filter(e => e.kind === 'review').length, 2)
assert.ok(history.evidence.some(e => e.id === initialFile))
assert.ok(!history.evidence.some(e => e.id === stagedFile))
const rosterPublic = await call('GET', '/class-penalties', classmate)
assert.ok(rosterPublic.items.every(p => p.entries.every(e => e.evidence.length === 0)))
for (const id of [claimFile, initialFile]) {
  const download = await call('GET', `/evidence/${id}/url`, observer)
  const content = await fetch(download.url)
  assert.ok(content.ok)
  assert.deepEqual(Buffer.from(await content.arrayBuffer()), png)
  await call('GET', `/evidence/${id}/url`, classmate, undefined, 403)
  await call('GET', `/evidence/${id}/url`, outsider, undefined, 404)
}
await call('GET', `/evidence/${stagedFile}/url`, observer, undefined, 403)
await call('GET', path, outsider, undefined, 404)
console.log('PASS: anonymous history, unpublished review boundary, file metadata, real downloads, roles and tenants')

// A report desk must open the actual scored submission without exposing its
// reporter or giving an unassigned reviewer access to the report context.
const report = await call('POST', '/reports', classmate, { kind: 'submission', studentUserId: student.id, targetId: sub.id, category: 'practice', itemKey: 'activity', proposedScore: 2, basis: '活动原始证明与申报分不符，请对照原件和历次认定复核' })
const reportFile = await upload(`/reports/${report.id}/notes`, classmate, '举报人补充证明.png')
const reportReviewers = []
for (const person of reviewers) if ((await call('GET', '/review/tasks', person)).items.some(row => row.id === `report:${report.id}`)) reportReviewers.push(person)
assert.equal(reportReviewers.length, 2)
const reportPath = `/review/reports/${report.id}/target`
for (const person of [admin, ...reportReviewers]) {
  const material = await call('GET', reportPath, person)
  assert.equal(material.targetId, sub.id)
  assert.equal(material.submission.title, '可追溯实践活动')
  assert.equal(material.submission.ruleSnapshot.item.key, 'activity')
  assert.equal(material.submission.filedRuleSnapshot.item.key, 'activity')
  assert.equal(material.submission.requestedScore, 4)
  assert.equal(material.currentScore, 4)
  assert.deepEqual(material.evidence.map(file => file.id), [claimFile, studentNoteFile])
  assert.ok(!JSON.stringify(material).includes(classmate.sid))
  assert.ok(!JSON.stringify(material).includes(classmate.name))
  assert.ok(!JSON.stringify(material).includes('objectKey'))
  assert.ok(!material.evidence.some(file => [stagedFile, initialFile, reportFile].includes(file.id)))
  for (const id of [claimFile, studentNoteFile, reportFile]) {
    const download = await call('GET', `/evidence/${id}/url`, person)
    assert.ok((await fetch(download.url)).ok, 'Report desk files really download')
  }
}
await call('GET', reportPath, null, undefined, 401)
await call('GET', reportPath, classmate, undefined, 403)
await call('GET', reportPath, reviewers.find(person => !reportReviewers.includes(person)), undefined, 404)
await call('GET', reportPath, outsider, undefined, 404)
const reportPeerFile = await upload(`/report-reviews/${report.id}/notes`, reportReviewers[0], '未公开的举报复核备注.png')
await call('POST', `/review/reports/${report.id}/decision`, reportReviewers[0], { decision: 'uphold', reason: `对照原件认定举报属实 ${ref(reportPeerFile)}`, spentSeconds: 10 })
assert.ok(!(await call('GET', `/review/reports/${report.id}`, reportReviewers[1])).noteEvidence.some(file => file.id === reportPeerFile))
assert.ok(!(await call('GET', reportPath, reportReviewers[1])).evidence.some(file => file.id === reportPeerFile))
await call('GET', `/evidence/${reportPeerFile}/url`, reportReviewers[1], undefined, 403)

const baseReports = {}
for (const kind of ['base', 'penalty']) {
  const r = await call('POST', '/reports', classmate, { kind, studentUserId: student.id, category: 'moral', itemKey: kind === 'base' ? 'base' : 'absence', proposedScore: kind === 'base' ? 60 : undefined, quantity: kind === 'penalty' ? 1 : undefined, basis: '原始计分与实际记录不一致，请审核人员核对这一类目' })
  baseReports[kind] = r.id
  const target = await call('GET', `/review/reports/${r.id}/target`, admin)
  assert.equal(target.targetId, null, 'A not-yet-recorded base or penalty has no invented history ID')
  assert.equal(target.submission, null)
  assert.deepEqual(target.evidence, [])
  assert.equal(kind === 'base' ? target.fullScore : target.perScore, kind === 'base' ? 80 : -1)
}
console.log('PASS: report material, real original/reporter downloads, anonymous report peer notes, unassigned/role/tenant boundaries')

const appeal = await call('POST', '/appeals/draft', student, { targetType: 'submission', targetId: sub.id })
const appealClaim = await upload(`/appeals/${appeal.id}/evidence`, student, '申诉佐证.png')
assert.ok(!(await verify()).evidence.some(e => e.id === appealClaim))
await call('GET', `/evidence/${appealClaim}/url`, observer, undefined, 403)
await call('POST', `/appeals/${appeal.id}/submit`, student, { reason: `补充活动证明申请复评 ${ref(appealClaim)}`, category: 'practice', itemKey: 'activity', score: 5 })
const peerFile = await upload(`/appeals/${appeal.id}/notes`, assigned[0], '申诉复评依据.png')
await call('POST', `/review/appeals/${appeal.id}/rereview`, assigned[0], { decision: 'uphold', score: 4, reason: `第一份复评维持原分 ${ref(peerFile)}`, spentSeconds: 10 })
history = await verify()
assert.equal(history.events.filter(e => e.kind === 'appeal_review').length, 0, 'Peer answers stay hidden until both reviewers decide')
assert.ok(!history.evidence.some(e => e.id === peerFile))
assert.equal((await call('GET', reportPath, reportReviewers[1])).submission.id, sub.id, 'Report originals remain readable during an appeal')
await call('GET', `/evidence/${peerFile}/url`, observer, undefined, 403)
await call('GET', `/evidence/${peerFile}/url`, assigned[1], undefined, 403)
const card = await call('GET', `/review/students/${student.id}/scorecard`, observer)
assert.ok(card.submissions.some(e => e.id === sub.id && e.locked), 'Appealing item remains available to expand')
await call('POST', `/review/appeals/${appeal.id}/rereview`, assigned[1], { decision: 'adjust', score: 5, reason: '第二份复评建议调整分数', spentSeconds: 10 })
const finalFile = await upload(`/appeals/${appeal.id}/notes`, admin, '申诉终裁依据.png')
await call('POST', `/admin/appeals/${appeal.id}/final`, admin, { score: 5, reason: `班管核验后终裁五分 ${ref(finalFile)}` })
history = await verify()
assert.equal(history.events.filter(e => e.kind === 'appeal_review').length, 2)
assert.ok(history.events.some(e => e.kind === 'appeal_final' && e.score === 5))
for (const id of [appealClaim, peerFile, finalFile]) {
  assert.ok(history.evidence.some(e => e.id === id))
  await call('GET', `/evidence/${id}/url`, observer)
  await call('GET', `/evidence/${id}/url`, classmate, undefined, 403)
}
console.log('PASS: appeal drafts, peer reveal, original/review/final attachments and in-progress scorecard')

for (const kind of ['base', 'penalty']) {
  const proposal = await call('POST', '/review/objections', observer, { kind, studentUserId: student.id, category: 'moral', itemKey: kind === 'base' ? 'base' : 'absence', targetId: null, proposedScore: kind === 'base' ? 70 : -2, quantity: kind === 'penalty' ? 2 : null, basis: '匿名轨迹回归提案依据' })
  const file = await upload(`/objections/${proposal.id}/notes`, observer, `${kind}-提案依据.png`)
  await call('PUT', `/review/objections/${proposal.id}`, observer, { kind, studentUserId: student.id, category: 'moral', itemKey: kind === 'base' ? 'base' : 'absence', targetId: null, proposedScore: kind === 'base' ? 70 : -2, quantity: kind === 'penalty' ? 2 : null, basis: `匿名轨迹回归提案依据 ${ref(file)}` })
  await call('POST', '/review/objections/submit', observer, { ids: [proposal.id] })
  await call('POST', `/admin/objections/${proposal.id}/decide`, admin, { action: 'apply', reason: '班管核验提案后照准生效' })
  const base = (await call('GET', `/review/students/${student.id}/scorecard`, observer)).baseItems.find(e => e.kind === kind && e.id)
  const reportTarget = await call('GET', `/review/reports/${baseReports[kind]}/target`, admin)
  assert.equal(reportTarget.targetId, base.id, 'Report resolves the new base record even when target_id was originally null')
  assert.equal(reportTarget.currentScore, kind === 'base' ? 70 : -2)
  const path = `/review/score-history/${kind}/${base.id}`
  const publicPath = `/class-penalties/${kind}/${base.id}/history`
  assert.equal((await call('GET', path, observer)).events.filter(e => e.kind === 'objection_decided').length, 1, 'Null target proposal is matched to the resulting base score')
  await call('GET', `/evidence/${file}/url`, reviewers.find(p => p !== observer))
  if (kind === 'base') {
    const a = await call('POST', '/appeals', student, { targetType: 'base_score', targetId: base.id, reason: '基础分复核申请请检查原始记录', proposedScore: 75 })
    await call('POST', `/admin/appeals/${a.id}/final`, admin, { score: 75, reason: '基础分复核后终裁七十五分' })
    assert.ok((await call('GET', path, observer)).events.some(e => e.kind === 'appeal_final' && e.score === 75))
  }
  privateProjection(await call('GET', publicPath, classmate), false)
}
await call('POST', `/admin/submissions/${sub.id}/force-reject`, admin, { reason: '后续查明材料不符合条件，保留历史并强制驳回' })
history = await verify()
assert.ok(history.events.some(e => e.kind === 'force_reject' && e.score === 0))
assert.ok(history.events.some(e => e.kind === 'appeal_final' && e.score === 5), 'Past decisions retain their original scores')
await call('PUT', '/admin/window/capabilities', admin, { key: 'studentReport', on: false })
await call('GET', studentPath, classmate, undefined, 403)
await call('GET', path, observer)
await call('PUT', '/admin/window/capabilities', admin, { key: 'studentReport', on: true })

// Publicity is a separate read window, never an implicit reporting switch.
const originalWindow = (await call('GET', '/window', admin)).window
const setPublicity = (publicity, extra = {}) => call('PUT', '/admin/timeline', admin, { ...originalWindow, ...extra, publicity })
await call('PUT', '/admin/timeline', classmate, { ...originalWindow, publicity: null }, 403)
await call('PUT', '/admin/timeline', observer, { ...originalWindow, publicity: null }, 403)
await call('PUT', '/admin/timeline', admin, { ...originalWindow, publicity: { open: new Date().toISOString() } }, 422)
await call('PUT', '/admin/window/capabilities', admin, { key: 'studentReport', on: false })
await setPublicity({ open: new Date(Date.now()+60000).toISOString(), close: new Date(Date.now()+120000).toISOString() })
await call('GET', '/class-penalties', classmate, undefined, 403)
await call('GET', studentPath, classmate, undefined, 403)
await call('GET', `/evidence/${claimFile}/url`, classmate, undefined, 403)
const publicWindow = { open: new Date(Date.now()-60000).toISOString(), close: new Date(Date.now()+60000).toISOString() }
await setPublicity(publicWindow)
// Both a legacy timeline save and a feature switch preserve the public window.
const { publicity: ignoredPublicity, ...legacyWindow } = originalWindow
await call('PUT', '/admin/timeline', admin, legacyWindow)
await call('PUT', '/admin/window/capabilities', admin, { key: 'edit', on: true })
assert.deepEqual((await call('GET', '/window', admin)).window.publicity, publicWindow)
const publicHistory = await call('GET', studentPath, classmate)
privateProjection(publicHistory, true)
assert.deepEqual(publicHistory.evidence.map(file=>file.id), (await call('GET', path, observer)).evidence.map(file=>file.id))
assert.ok(!publicHistory.evidence.some(file=>file.id===stagedFile))
await call('GET', '/class-penalties', classmate)
await call('GET', studentPath, null, undefined, 401)
await call('GET', studentPath, outsider, undefined, 403)
for (const id of [claimFile, studentNoteFile, initialFile, appealClaim, peerFile, finalFile]) {
  const link = await call('GET', `/evidence/${id}/url`, classmate)
  assert.ok(link.expiresIn > 0 && link.expiresIn <= 60, 'Public URL cannot outlive the configured window')
  assert.deepEqual(Buffer.from(await (await fetch(link.url)).arrayBuffer()), png)
  await call('GET', `/evidence/${id}/url`, outsider, undefined, 404)
}
for (const id of [stagedFile, reportFile, reportPeerFile]) await call('GET', `/evidence/${id}/url`, classmate, undefined, 403)
const unpublished = await call('POST', '/submissions', student, { category: 'practice', itemKey: 'activity', title: '公示期间仍未提交的材料', claim: { score: 3 } })
const unpublishedFile = await upload(`/submissions/${unpublished.id}/evidence`, student, '不应公示的草稿.png')
await call('GET', `/evidence/${unpublishedFile}/url`, classmate, undefined, 403)
await call('GET', `/class-penalties/submission/${unpublished.id}/history`, classmate, undefined, 404)
const blockedReport = await call('POST', '/reports', classmate, { kind: 'submission', studentUserId: student.id, targetId: sub.id, category: 'practice', itemKey: 'activity', proposedScore: 0, basis: '公示期间只允许查看，不能绕过匿名举报开关' }, 409)
assert.equal(blockedReport.code, 'report_closed')
// Freezing business writes does not freeze scheduled read access.
await setPublicity(publicWindow, { open: new Date(Date.now()-180000).toISOString(), close: new Date(Date.now()-120000).toISOString(), lockdown: new Date(Date.now()-60000).toISOString() })
await call('GET', studentPath, classmate)
await call('GET', `/evidence/${claimFile}/url`, classmate)
await call('PUT', '/admin/window/capabilities', admin, { key: 'studentReport', on: true }, 409)
const ending = { open: new Date(Date.now()-60000).toISOString(), close: new Date(Date.now()+5000).toISOString() }
await setPublicity(ending)
const expiring = await call('GET', `/evidence/${claimFile}/url`, classmate)
assert.ok(expiring.expiresIn > 0 && expiring.expiresIn <= 5)
await new Promise(resolve=>setTimeout(resolve, Math.max(0, Date.parse(ending.close)-Date.now()+1100)))
await call('GET', studentPath, classmate, undefined, 403)
await call('GET', `/evidence/${claimFile}/url`, classmate, undefined, 403)
assert.ok(!(await fetch(expiring.url)).ok, 'Already-issued public URL expires by the scheduled end')
await call('GET', `/evidence/${claimFile}/url`, student)
await call('PUT', '/admin/window/capabilities', admin, { key: 'studentReport', on: true })
privateProjection(await call('GET', studentPath, classmate), false)
await setPublicity(null)
assert.equal((await call('GET', '/window', admin)).window.publicity ?? null, null)
console.log('PASS: publicity dates, tenant/role/draft/peer-note boundaries, real originals, independent reporting, lockdown reads and expiring links')

// A 1/2 appeal may be finalized before the second original reviewer submits.
// Keep another 1/2 appeal open to compare the pending/all tabs in the browser.
for (const person of reviewers.filter(person => !assigned.includes(person))) {
  await call('PUT', `/admin/dispatch/reviewers/${person.id}`, admin, { paused: true })
}
async function halfReviewedAppeal(title, finalize) {
  const submission = await call('POST', '/submissions', student, { category: 'practice', itemKey: 'activity', title, claim: { score: 4 } })
  await call('POST', `/submissions/${submission.id}/submit`, student)
  for (let attempt = 0; attempt < 60; attempt++) {
    const queues = await Promise.all(assigned.map(person => call('GET', '/review/tasks', person)))
    if (queues.every(queue => queue.items.some(item => item.id === submission.id))) break
    assert.ok(attempt < 59, 'The original reviewers received the synthetic task')
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  for (const person of assigned) await call('POST', `/review/tasks/${submission.id}/decision`, person, { decision: 'accepted', reason: '核验原始材料符合申报条件', spentSeconds: 10 })
  const appeal = await call('POST', '/appeals', student, { targetType: 'submission', targetId: submission.id, reason: '补充材料申请重新核定认定分', category: 'practice', itemKey: 'activity', score: 5 })
  await call('POST', `/review/appeals/${appeal.id}/rereview`, assigned[0], { decision: 'uphold', score: 4, reason: '第一位复评人核验后维持原分', spentSeconds: 10 })
  const pending = (await call('GET', '/review/appeals?scope=pending', assigned[1])).items.find(item => item.id === appeal.id)
  assert.equal(pending.status, 'reviewing')
  assert.equal(pending.handlers.filter(handler => handler.decided).length, 1)
  assert.ok(pending.handlers.some(handler => handler.mine && !handler.decided))
  const all = (await call('GET', '/review/appeals?scope=all', assigned[1])).items.find(item => item.id === appeal.id)
  assert.deepEqual(all, pending, 'Active 1/2 appeal has identical state in pending and all')
  if (finalize) {
    await call('POST', `/admin/appeals/${appeal.id}/final`, admin, { score: 5, reason: '班管提前接管并终裁，复评无需继续' })
    assert.ok(!(await call('GET', '/review/appeals?scope=pending', assigned[1])).items.some(item => item.id === appeal.id))
    const closed = await call('GET', `/review/appeals/${appeal.id}`, assigned[1])
    assert.equal(closed.status, 'final')
    assert.equal(closed.currentScore, 5)
    assert.equal(closed.baselineScore, 4)
    assert.equal(closed.handlers.filter(handler => handler.decided).length, 1, 'Finalization preserves the unfinished reviewer record')
    assert.equal((await call('GET', '/review/appeals?scope=all', assigned[1])).items.find(item => item.id === appeal.id).status, 'final')
    const rejected = await call('POST', `/review/appeals/${appeal.id}/rereview`, assigned[1], { decision: 'adjust', score: 6, reason: '模拟过时表单在终裁之后仍然提交', spentSeconds: 10 }, 409)
    assert.equal(rejected.code, 'not_reconsiderable')
    assert.match(rejected.message, /已终裁/)
    assert.equal((await call('GET', `/review/appeals/${appeal.id}`, assigned[1])).currentScore, 5, 'Stale submission cannot overwrite the final score')
  }
  return appeal.id
}
const finalizedAppealId = await halfReviewedAppeal('提前终裁的 1/2 申诉', true)
const pendingAppealId = await halfReviewedAppeal('仍待我复评的 1/2 申诉', false)
console.log('PASS: active 1/2 appeal is pending; finalized 1/2 appeal is read-only with a precise stale-submit error')
const reportSubmission = await call('POST', '/submissions', student, { category: 'health', itemKey: 'contest', title: '竞赛获奖原始材料与归类核对', claim: { option: '校级一等奖', score: 4 }, note: '学生原始说明：参加校园活动，获奖证明与评分规则请一并核对。' })
await upload(`/submissions/${reportSubmission.id}/evidence`, student, '获奖证明原件.png')
await call('POST', `/submissions/${reportSubmission.id}/submit`, student)
for (let attempt = 0; attempt < 60; attempt++) {
  const queues = await Promise.all(assigned.map(person => call('GET', '/review/tasks', person)))
  if (queues.every(queue => queue.items.some(item => item.id === reportSubmission.id))) break
  assert.ok(attempt < 59, 'Report fixture reviewers assigned')
  await new Promise(resolve => setTimeout(resolve, 500))
}
for (const person of assigned) await call('POST', `/review/tasks/${reportSubmission.id}/decision`, person, { decision: 'accepted', reason: '初审核验获奖原件并确认分数', spentSeconds: 10 })
await call('POST', `/admin/submissions/${reportSubmission.id}/classification/suggest`, admin, { category: 'practice', itemKey: 'activity', reason: '核对后建议本项归入实践活动' })
await call('POST', `/admin/submissions/${reportSubmission.id}/arbitrate`, admin, { category: 'practice', itemKey: 'activity', score: 4, reason: '核对后确认本项属于实践活动，保留学生原始归类' })
const browserReport = await call('POST', '/reports', classmate, { kind: 'submission', studentUserId: student.id, targetId: reportSubmission.id, category: 'practice', itemKey: 'activity', proposedScore: 2, basis: '原始材料为院级一等奖，应根据原始附件和历史审核认定两分。' })
await upload(`/reports/${browserReport.id}/notes`, classmate, '举报说明佐证.png')
const reclassifiedTarget = await call('GET', `/review/reports/${browserReport.id}/target`, assigned[1])
assert.equal(reclassifiedTarget.submission.filedRuleSnapshot.categoryKey, 'health')
assert.equal(reclassifiedTarget.submission.filedRuleSnapshot.item.key, 'contest')
assert.equal(reclassifiedTarget.submission.ruleSnapshot.categoryKey, 'practice')
assert.equal(reclassifiedTarget.submission.ruleSnapshot.item.key, 'activity')
assert.equal(reclassifiedTarget.submission.claim.option, '校级一等奖')
console.log('PASS: reclassified report retains filed category, effective category, original claim and rule snapshots')
mkdirSync('output/playwright', { recursive: true })
writeFileSync('output/playwright/score-history-fixture.json', JSON.stringify({ adminSid: admin.sid, studentSid: student.sid, classmateSid: classmate.sid, reviewerSid: observer.sid, submissionId: sub.id, pendingReviewerSid: assigned[1].sid, finalizedAppealId, pendingAppealId, reportId: browserReport.id, reportReviewerSid: assigned[1].sid, baseReports }))
console.log('PASS: base and penalty history, null-target proposals, appeals, forced rejection, report capability')
