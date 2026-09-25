#!/usr/bin/env node
// Real HTTP regression. Synthetic identities and loopback development data only.
import assert from 'node:assert/strict'
import { mkdirSync, writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const stamp = Date.now().toString(36)
const password = 'review-revision-synthetic-password'
async function call(method, path, token, body, status) {
  const response = await fetch(`${origin}/api/v1${path}`, {
    method, headers: e2eHeaders({ 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const result = await response.json().catch(() => null)
  const label = `${method} ${path}: ${response.status} ${result?.code ?? ''}`
  if (status === undefined) assert.ok(response.ok, label)
  else if (status !== null) assert.equal(response.status, status, label)
  return status === null ? { status: response.status, body: result } : result
}
async function register(sid, name) {
  const check = await call('POST', '/auth/register/check', null, { sid, name })
  const result = await call('POST', '/auth/register/complete', null, { ticket: check.ticket, password }, 201)
  return { sid, token: result.access_token }
}
async function waitFor(check) {
  const deadline = Date.now() + 30000
  while (Date.now() < deadline) {
    const value = await check()
    if (value) return value
    await new Promise((resolve) => setTimeout(resolve, 300))
  }
  throw new Error('Review assignment timed out')
}
const item = (key, name) => ({ key, name, scoreRule: { type: 'free', min: 0, max: 20 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } })
const config = {
  schemeName: '历史审核修改合成方案',
  weights: { major: .6, moral: .15, practice: .15, health: .1 },
  categories: [
    { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    { key: 'moral', name: '思想道德', maxTotal: 100, baseItems: [], penaltyItems: [], items: [item('service', '志愿服务')] },
    { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [item('activity', '实践活动'), item('contest', '竞赛获奖')] },
    { key: 'health', name: '身体心理', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
  ],
}
const accepted = { decision: 'accepted', reason: '已核对本地合成佐证', spentSeconds: 12 }
const adjusted = (reviewId, score = 7, extra = {}) => ({ reviewId, decision: 'adjusted', score, reason: '重新核对后调整认定分', spentSeconds: 8, ...extra })
const detail = (sub, who) => call('GET', `/review/tasks/${sub.id}`, who.token)
const history = (who) => call('GET', '/review/history', who.token)
const revise = (sub, who, value, status = 200) => call('PUT', `/review/tasks/${sub.id}/decision`, who.token, value, status)

await assertLocalDevelopmentAPI(origin)
const ops = (await call('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' })).access_token
await call('POST', '/ops/tenants', ops, { name: `审核修改回归 ${stamp}`, slug: `review-revision-${stamp}`, adminSid: `VA${stamp}`, adminName: '修改班管' }, 201)
const admin = await register(`VA${stamp}`, '修改班管')
const roster = [{ sid: `VS${stamp}`, name: '修改学生', role: 'student' }, ...['甲', '乙', '丙'].map((name, index) => ({ sid: `VG${index}${stamp}`, name: `修改审核${name}`, role: 'group' }))]
await call('POST', '/admin/whitelist/import', admin.token, { csv: ['sid,name,role', ...roster.map((row) => `${row.sid},${row.name},${row.role}`)].join('\n') })
const student = await register(roster[0].sid, roster[0].name)
const reviewers = []
for (const row of roster.slice(1)) reviewers.push(await register(row.sid, row.name))
const candidates = [...reviewers, admin]
const scheme = await call('POST', '/admin/scheme', admin.token, { name: config.schemeName, config }, 201)
await call('POST', `/admin/scheme/${scheme.id}/publish`, admin.token)
const open = new Date(Date.now() - 3600000).toISOString()
const close = new Date(Date.now() + 30 * 86400000).toISOString()
await call('PUT', '/admin/timeline', admin.token, { open, close })
for (const key of ['submit', 'edit', 'review', 'arbitrate']) await call('PUT', '/admin/window/capabilities', admin.token, { key, on: true, close })
async function submission(title) {
  const sub = await call('POST', '/submissions', student.token, { category: 'practice', itemKey: 'activity', title, claim: { score: 10 }, note: '用于审核历史修改的本地合成材料。' }, 201)
  await call('POST', `/submissions/${sub.id}/submit`, student.token)
  sub.assigned = await waitFor(async () => {
    const result = []
    for (const who of candidates) if ((await call('GET', '/review/tasks', who.token)).items.some((task) => task.id === sub.id)) result.push(who)
    return result.length === 2 ? result : null
  })
  return sub
}
const sub = await submission('历史修改与一致定分')
const [mine, peer] = sub.assigned
const outsider = candidates.find((who) => !sub.assigned.includes(who))
assert.equal((await detail(sub, mine)).canEdit, false)
await revise(sub, mine, adjusted('1'), 409)
const first = await call('POST', `/review/tasks/${sub.id}/decision`, mine.token, { ...accepted, classificationSuggestion: { category: 'practice', itemKey: 'contest', reason: '建议按竞赛获奖审核' } }, 201)
await call('POST', `/review/tasks/${sub.id}/decision`, mine.token, accepted, 409)
const firstHistory = (await history(mine)).items.find((row) => row.submissionId === sub.id)
assert.equal(firstHistory.canEdit, true)
assert.equal(firstHistory.studentId, student.sid)
assert.equal(firstHistory.student, roster[0].name)
assert.equal((await detail(sub, mine)).peerSubmitted, false)
const peerDetail = await detail(sub, peer)
assert.equal(peerDetail.myReview, null)
assert.equal(peerDetail.myClassificationSuggestion, null)
assert.equal(peerDetail.peerSubmitted, true)
assert.equal('reviews' in peerDetail, false)
await revise(sub, peer, adjusted(first.reviewId), 409)
await revise(sub, outsider, adjusted(first.reviewId), 404)
await revise(sub, student, adjusted(first.reviewId), 403)
await call('PUT', `/review/tasks/${sub.id}/decision`, null, adjusted(first.reviewId), 401)
await revise(sub, mine, adjusted(first.reviewId, 99), 422)
await revise(sub, mine, adjusted(first.reviewId, 7, { reason: '' }), 422)
assert.equal((await detail(sub, mine)).myReview.id, first.reviewId)
const changed = await revise(sub, mine, adjusted(first.reviewId, 7, { classificationSuggestion: { category: 'moral', itemKey: 'service', reason: '重新核对后建议归为志愿服务' } }))
assert.notEqual(changed.reviewId, first.reviewId)
assert.equal(changed.submissionStatus, 'consensus')
assert.equal(changed.finalScore, null)
assert.equal((await detail(sub, mine)).myReview.spentSeconds, 20)
assert.equal((await detail(sub, mine)).myClassificationSuggestion.category, 'moral')
const revisedHistory = (await history(mine)).items.filter((row) => row.submissionId === sub.id)
assert.equal(revisedHistory.length, 1)
assert.equal(revisedHistory[0].studentId, student.sid)
assert.equal(revisedHistory[0].student, roster[0].name)
await revise(sub, mine, adjusted(first.reviewId), 409)
const suggestions = (await call('GET', '/admin/classification-suggestions?status=', admin.token)).items.filter((row) => row.submissionId === sub.id)
assert.equal(suggestions.filter((row) => row.status === 'pending').length, 1)
assert.equal(suggestions.filter((row) => row.status === 'withdrawn').length, 1)
const audits = (await call('GET', '/admin/audit-log?action=review.revised', admin.token)).items
const audit = audits.find((row) => row.resourceId === sub.id)
assert.equal(audit.before.score, 10)
assert.equal(audit.after.score, 7)
console.log('✓ 仅本人有效结论可改；校验失败不破坏原结论；旧结论与分类建议留存；同行结论保持不可见')

const edits = await Promise.all([revise(sub, mine, adjusted(changed.reviewId, 6), null), revise(sub, mine, adjusted(changed.reviewId, 6), null)])
assert.deepEqual(edits.map((result) => result.status).sort(), [200, 409])
const latest = (await detail(sub, mine)).myReview
assert.equal((await detail(sub, mine)).myClassificationSuggestion, null)
assert.equal((await call('GET', '/admin/classification-suggestions', admin.token)).items.filter((row) => row.submissionId === sub.id).length, 0)
assert.equal((await call('GET', '/review/category', mine.token)).load.done, 1)
const final = await call('POST', `/review/tasks/${sub.id}/decision`, peer.token, adjusted(undefined, 6), 201)
assert.equal(final.submissionStatus, 'scored')
assert.equal(final.finalScore, 6)
assert.equal((await detail(sub, mine)).canEdit, false)
await revise(sub, mine, adjusted(latest.id), 409)
console.log('✓ 同一旧版本并发修改仅一个成功；撤销分类建议不残留仲裁；采用修改后的分值定分，工作量不重复')

const conflict = await submission('另一位先提交后不能覆盖')
const [conflictMine, conflictPeer] = conflict.assigned
const conflictReview = await call('POST', `/review/tasks/${conflict.id}/decision`, conflictMine.token, accepted, 201)
await call('POST', `/review/tasks/${conflict.id}/decision`, conflictPeer.token, adjusted(undefined, 3), 201)
assert.equal((await detail(conflict, conflictMine)).status, 'arbitrating')
assert.equal((await detail(conflict, conflictMine)).canEdit, false)
await revise(conflict, conflictMine, adjusted(conflictReview.reviewId, 3), 409)

const racing = await submission('修改与同行提交并发')
const [racingMine, racingPeer] = racing.assigned
const racingReview = await call('POST', `/review/tasks/${racing.id}/decision`, racingMine.token, accepted, 201)
const race = await Promise.all([
  revise(racing, racingMine, adjusted(racingReview.reviewId, 4), null),
  call('POST', `/review/tasks/${racing.id}/decision`, racingPeer.token, adjusted(undefined, 4), 201),
])
const raced = await detail(racing, racingMine)
assert.ok([200, 409].includes(race[0].status))
assert.equal(raced.canEdit, false)
assert.equal(raced.status, race[0].status === 200 ? 'scored' : 'arbitrating')
assert.equal(raced.myReview.score, race[0].status === 200 ? 4 : 10)
console.log('✓ 仲裁中不能改历史；修改与同行提交串行落库，不会覆盖已经形成的双评结果')

const ui = await submission('可修改的历史审核示例')
const [uiMine, uiPeer] = ui.assigned
const uiReview = await call('POST', `/review/tasks/${ui.id}/decision`, uiMine.token, adjusted(undefined, 5, { reason: '原审核意见：按活动材料认定五分', classificationSuggestion: { category: 'practice', itemKey: 'contest', reason: '原分类建议：按竞赛获奖复核' } }), 201)
const sealed = await submission('自动封存后仍可完成初审')
const browser = await submission('自动封存后的初审工作台验收')
const draftInput = { category: 'practice', itemKey: 'activity', title: '截止后不能补交的草稿', claim: { score: 10 } }
const draft = await call('POST', '/submissions', student.token, draftInput, 201)
await call('PUT', '/admin/window/capabilities', admin.token, { key: 'review', on: false, close })
assert.equal((await detail(ui, uiMine)).canEdit, false)
assert.equal((await history(uiMine)).items.find((row) => row.submissionId === ui.id).canEdit, false)
await revise(ui, uiMine, adjusted(uiReview.reviewId), 409)
await call('PUT', '/admin/window/capabilities', admin.token, { key: 'review', on: true, close })
const pastClose = new Date(Date.now() - 2000).toISOString()
await call('PUT', '/admin/timeline', admin.token, { open, close: pastClose, lockdown: null })
for (const who of [student, ...candidates]) {
  const seal = await call('GET', '/me/seal', who.token)
  assert.equal(seal.sealed, true)
  assert.equal(seal.source, 'auto')
}
for (const [method, path, body] of [
  ['POST', '/submissions', draftInput],
  ['PUT', `/submissions/${draft.id}`, draftInput],
  ['POST', `/submissions/${draft.id}/submit`, undefined],
]) await call(method, path, student.token, body, 409)
const [sealedMine, sealedPeer] = sealed.assigned
await call('PUT', '/admin/window/capabilities', admin.token, { key: 'review', on: false })
assert.equal((await call('POST', `/review/tasks/${sealed.id}/decision`, sealedMine.token, accepted, 409)).code, 'review_closed')
await call('PUT', '/admin/window/capabilities', admin.token, { key: 'review', on: true })
const sealedFirst = await call('POST', `/review/tasks/${sealed.id}/decision`, sealedMine.token, accepted, 201)
assert.equal(sealedFirst.submissionStatus, 'consensus')
assert.equal((await detail(sealed, sealedMine)).canEdit, true)
assert.equal((await history(sealedMine)).items.find((row) => row.submissionId === sealed.id).canEdit, true)
await revise(sealed, sealedMine, adjusted(sealedFirst.reviewId, 7))
const sealedPeerView = await detail(sealed, sealedPeer)
assert.equal(sealedPeerView.myReview, null)
assert.equal(sealedPeerView.peerSubmitted, true)
assert.equal('reviews' in sealedPeerView, false)
const sealedFinal = await call('POST', `/review/tasks/${sealed.id}/decision`, sealedPeer.token, adjusted(undefined, 7), 201)
assert.equal(sealedFinal.submissionStatus, 'scored')
assert.equal(sealedFinal.finalScore, 7)
assert.equal((await detail(sealed, sealedMine)).canEdit, false)
await call('PUT', '/admin/window/capabilities', admin.token, { key: 'appeal', on: true })
const sealedAppeal = await call('POST', '/appeals', student.token, { targetType: 'submission', targetId: sealed.id, reason: '自动封存后仍应能对认定分提出申诉' }, 201)
const appealedFinal = await call('POST', `/admin/appeals/${sealedAppeal.id}/final`, admin.token, { score: 8, reason: '封存后核对材料并完成申诉终裁' })
assert.equal(appealedFinal.status, 'final')
assert.equal((await call('GET', `/submissions/${sealed.id}`, student.token)).submission.finalScore, 8)
console.log('✓ 自然封存后初审提交、历史修改、双评定分、申诉及终裁正常；材料新增/修改/补交仍关闭')

await call('PUT', '/admin/timeline', admin.token, { open, close: pastClose, lockdown: pastClose })
assert.equal((await detail(ui, uiMine)).canEdit, false)
await revise(ui, uiMine, adjusted(uiReview.reviewId), 409)
assert.equal((await call('POST', `/review/tasks/${browser.id}/decision`, browser.assigned[0].token, accepted, 409)).code, 'class_locked')
await call('PUT', '/admin/timeline', admin.token, { open, close: pastClose, lockdown: null })
assert.equal((await detail(ui, uiMine)).canEdit, true)
console.log('✓ 审核关闭、全系统封锁时，历史入口和修改接口同时关闭')

await call('POST', '/ops/tenants', ops, { name: `审核外班 ${stamp}`, slug: `review-other-${stamp}`, adminSid: `VO${stamp}`, adminName: '外班班管' }, 201)
const other = await register(`VO${stamp}`, '外班班管')
await revise(ui, other, adjusted(uiReview.reviewId), 404)
console.log('✓ 外班无法修改本班历史审核')

mkdirSync('.ai-eval', { recursive: true })
writeFileSync('.ai-eval/review-revision-fixture.json', JSON.stringify({ reviewerSid: uiMine.sid, peerSid: uiPeer.sid, adminSid: admin.sid, submissionId: ui.id }))
writeFileSync('.ai-eval/auto-seal-review-fixture.json', JSON.stringify({ reviewerSid: browser.assigned[0].sid, peerSid: browser.assigned[1].sid, adminSid: admin.sid, studentSid: student.sid, submissionId: browser.id, close: pastClose }))
console.log('✓ 本地合成示例已就绪，可继续浏览器验收')
