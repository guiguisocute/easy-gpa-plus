#!/usr/bin/env node
// Real HTTP regression using only a loopback development API and synthetic data.
import assert from 'node:assert/strict'
import { mkdirSync, writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const stamp = Date.now().toString(36)
const password = 'current-score-synthetic-password'
async function call(method, path, token, body, status) {
  const response = await fetch(`${origin}/api/v1${path}`, {
    method, headers: e2eHeaders({ 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const result = await response.json().catch(() => null)
  const label = `${method} ${path}: ${response.status} ${result?.code ?? ''}`
  if (status) assert.equal(response.status, status, label)
  else assert.ok(response.ok, label)
  return result
}
async function register(sid, name) {
  const check = await call('POST', '/auth/register/check', null, { sid, name })
  const auth = await call('POST', '/auth/register/complete', null, { ticket: check.ticket, password })
  return { sid, token: auth.access_token }
}
async function waitFor(check) {
  const end = Date.now() + 30000
  while (Date.now() < end) {
    const result = await check()
    if (result) return result
    await new Promise((resolve) => setTimeout(resolve, 300))
  }
  throw new Error('Synthetic review assignment timed out')
}
const config = {
  schemeName: '当前排名合成回归方案',
  weights: { major: .6, moral: .15, practice: .15, health: .1 },
  categories: [
    { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    { key: 'moral', name: '思想道德', maxTotal: 100, baseItems: [{ key: 'ordinary', name: '日常表现', full: 70 }], penaltyItems: [], items: [] },
    { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [{ key: 'activity', name: '实践活动', scoreRule: { type: 'free', min: 0, max: 40 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } }] },
    { key: 'health', name: '身体心理', maxTotal: 100, baseItems: [{ key: 'health_ordinary', name: '日常表现', full: 70 }], penaltyItems: [], items: [] },
  ],
}

await assertLocalDevelopmentAPI(origin)
const ops = (await call('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' })).access_token
await call('POST', '/ops/tenants', ops, { name: `排名回归 ${stamp}`, slug: `current-score-${stamp}`, adminSid: `RA${stamp}`, adminName: '排名班管' })
const admin = await register(`RA${stamp}`, '排名班管')
const roster = [
  { sid: `RS${stamp}`, name: '排名学生', role: 'student' },
  { sid: `RU${stamp}`, name: '未注册成员', role: 'student' },
  { sid: `RD${stamp}`, name: '停用成员', role: 'student' },
  ...['甲', '乙'].map((n, i) => ({ sid: `RG${i}${stamp}`, name: `排名审核${n}`, role: 'group' })),
]
await call('POST', '/admin/whitelist/import', admin.token, { csv: ['sid,name,role', ...roster.map((p) => `${p.sid},${p.name},${p.role}`)].join('\n') })
const student = await register(roster[0].sid, roster[0].name)
await register(roster[2].sid, roster[2].name)
const groups = []
for (const p of roster.slice(3)) groups.push(await register(p.sid, p.name))
const users = (await call('GET', '/admin/users', admin.token)).items
const disabled = users.find((u) => u.sid === roster[2].sid)
await call('PUT', `/admin/users/${disabled.id}/status`, admin.token, { status: 'disabled' })
const scheme = await call('POST', '/admin/scheme', admin.token, { name: config.schemeName, config })
await call('POST', `/admin/scheme/${scheme.id}/publish`, admin.token)
const close = new Date(Date.now() + 30 * 86400000).toISOString()
await call('PUT', '/admin/timeline', admin.token, { open: new Date(Date.now() - 3600000).toISOString(), close })
for (const key of ['submit', 'edit', 'review', 'appeal', 'arbitrate']) await call('PUT', '/admin/window/capabilities', admin.token, { key, on: true })
const mine = () => call('GET', '/me/score', student.token)
const importGPA = (rows) => call('POST', '/admin/gpa/paste', admin.token, { rows })
const record = (person, score) => ({ sid: person.sid, score })
await call('GET', '/me/score', null, undefined, 401)
let score = await mine()
assert.equal(score.settled, false)
assert.equal(score.current.classSize, 5, 'Include unregistered members, exclude disabled members')
assert.equal(score.current.totalScore, null)
assert.equal(score.current.categoryScores.major, null)
assert.equal(score.current.categoryScores.moral, 70)
assert.deepEqual(score.current.categoryRanks, { moral: 1, practice: 1, health: 1 })
assert.equal(score.current.rankingReady, false)
await importGPA([record(student, 80)])
score = await mine()
assert.equal(score.current.gpaImported, 1)
assert.equal(score.current.totalScore, 65.5)
assert.equal(score.current.classRank, null)
assert.equal(score.current.majorRank, null)
assert.deepEqual(score.current.categoryRanks, { moral: 1, practice: 1, health: 1 })
assert.equal(score.settled, false, 'Reading current ranks must not create a settlement')
console.log('✓ 缺失和部分 GPA 只隐藏专业与总排名；其他三项独立排名，未注册成员参排，停用成员不参排')

const sub = await call('POST', '/submissions', student.token, { category: 'practice', itemKey: 'activity', title: '当前排名合成材料', claim: { score: 30 }, note: '仅用于本地排名回归的合成材料' })
await call('POST', `/submissions/${sub.id}/submit`, student.token)
assert.equal((await mine()).current.totalScore, 65.5, 'Pending self-reported scores must not count')
const assigned = await waitFor(async () => {
  const found = []
  for (const candidate of [admin, ...groups]) {
    const queue = await call('GET', '/review/tasks', candidate.token)
    if (queue.items.some((task) => task.id === sub.id)) found.push(candidate)
  }
  return found.length === 2 ? found : null
})
for (const candidate of assigned) await call('POST', `/review/tasks/${sub.id}/decision`, candidate.token, { decision: 'accepted', reason: '合成材料核验完成，确认认定分', spentSeconds: 10 })
score = await mine()
assert.equal(score.current.totalScore, 70)
assert.equal(score.current.classRank, null)
assert.equal(score.current.majorRank, null)
assert.equal(score.current.categoryRanks.practice, 1)
const peerScore = await call('GET', '/me/score', groups[0].token)
assert.equal(peerScore.current.totalScore, null)
assert.equal(peerScore.current.majorRank, null)
assert.equal(peerScore.current.classRank, null)
assert.equal(peerScore.current.categoryRanks.practice, 2, 'Recognized submissions update peer category ranks before GPA is complete')
const partialRanking = (await call('GET', '/admin/stats', admin.token)).ranking
assert.equal(partialRanking.rankingReady, false)
const partialStudent = partialRanking.items.find((entry) => entry.sid === student.sid)
assert.equal(partialStudent.classRank, null)
assert.equal(partialStudent.categoryRanks.major, undefined)
assert.equal(partialStudent.categoryRanks.practice, 1)

const beforeAppeal = score.current.totalScore
const appeal = await call('POST', '/appeals/draft', student.token, { targetType: 'submission', targetId: sub.id, reason: '申请重新核对本条合成材料的认定分值' })
await call('POST', `/appeals/${appeal.id}/submit`, student.token, { reason: '申请重新核对本条合成材料的认定分值', score: 32 })
assert.equal((await mine()).current.totalScore, beforeAppeal, 'An ongoing appeal must retain the existing decision')
await call('POST', `/appeals/${appeal.id}/withdraw`, student.token)
console.log('✓ 专业未导齐时定分仍更新三项名次；待审自报分不计入，申诉中保留已有认定分')

await importGPA([record(student, 80), record(admin, 90), record(roster[1], 85), record(groups[0], 70), record(groups[1], 60)])
score = await mine()
assert.equal(score.current.rankingReady, true)
assert.equal(score.current.classRank, 2)
assert.equal(score.current.majorRank, 3)
assert.deepEqual(score.current.categoryRanks, { major: 3, moral: 1, practice: 1, health: 1 })
console.log('✓ 专业导齐后开放专业与总排名，其他三项名次保持一致')
const boardRanking = (await call('GET', '/admin/stats', admin.token)).ranking
assert.equal(boardRanking.rankingReady, true)
assert.equal(boardRanking.classSize, score.current.classSize)
const boardStudent = boardRanking.items.find((entry) => entry.sid === student.sid)
assert.equal(boardStudent.total, score.current.totalScore)
assert.equal(boardStudent.classRank, score.current.classRank)
assert.deepEqual(boardStudent.categoryScores, score.current.categoryScores)
assert.deepEqual(boardStudent.categoryRanks, score.current.categoryRanks)
assert.ok(boardRanking.items.some((entry) => entry.sid === roster[1].sid), 'Unregistered active member included in admin ranking')
console.log('✓ 班管排名与学生当前成绩使用相同大项、权重和并列名次，并包含未注册成员')


await call('POST', '/admin/gate/force', admin.token, { reason: '本地合成回归核对当前成绩与正式结算的一致性' })
await call('POST', '/admin/settle', admin.token)
score = await mine()
assert.equal(score.settled, true)
assert.equal(score.stale, false)
assert.deepEqual(score.categoryScores, score.current.categoryScores)
assert.deepEqual(score.categoryRanks, score.current.categoryRanks)
assert.equal(score.totalScore, score.current.totalScore)
assert.equal(score.classRank, score.current.classRank)
assert.equal(score.majorRank, score.current.majorRank)
await call('POST', `/admin/submissions/${sub.id}/force-reject`, admin.token, { reason: '合成材料核验未满足条件，强制驳回以验证当前成绩更新' })
score = await mine()
assert.equal(score.stale, true)
assert.equal(score.totalScore, 70, 'Historical settlement must remain immutable')
assert.equal(score.current.totalScore, 65.5)
assert.equal(score.current.classRank, 3)
assert.equal(score.current.pendingItems, 0)
assert.deepEqual(score.current.categoryRanks, { major: 3, moral: 1, practice: 1, health: 1 })
for (const field of ['awardTier', 'honor', 'sid', 'name', 'items', 'details']) assert.equal(field in score.current, false)
console.log('✓ 当前成绩与正式结算一致；强制驳回使旧结算失效并立即更新当前排名，不泄露他人资料或预发评优')

await call('POST', '/ops/tenants', ops, { name: `排名外班 ${stamp}`, slug: `rank-other-${stamp}`, adminSid: `RO${stamp}`, adminName: '外班班管' })
const other = await register(`RO${stamp}`, '外班班管')
const otherScheme = await call('POST', '/admin/scheme', other.token, { name: config.schemeName, config })
await call('POST', `/admin/scheme/${otherScheme.id}/publish`, other.token)
const otherScore = await call('GET', '/me/score', other.token)
assert.equal(otherScore.current.classSize, 1)
assert.equal(otherScore.current.gpaImported, 0)
assert.equal(otherScore.settled, false)
console.log('✓ 外班无法读取本班分数、导入进度或结算')

// The stored snapshot retains the proposer for admin auditing, but /me/score
// must project it even when the settlement has subsequently become stale.
const targetUser = users.find((u) => u.sid === student.sid)
const objection = await call('POST', '/review/objections', groups[0].token, {
  kind: 'base', studentUserId: targetUser.id, category: 'moral', itemKey: 'ordinary',
  proposedScore: 69, basis: '合成基础分调整，用于验证提案人身份保护',
})
await call('POST', '/review/objections/submit', groups[0].token, { ids: [objection.id] })
const adminProposals = await call('GET', '/admin/objections', admin.token)
assert.equal(adminProposals.items.find((item) => item.id === objection.id).proposer, roster[3].name)
await call('POST', `/admin/objections/${objection.id}/decide`, admin.token, { action: 'apply', reason: '合成身份保护回归，确认基础分调整' })
await call('POST', '/admin/settle', admin.token)
function assertRecorderPrivate(result) {
  const base = result.details.baseItems.find((item) => item.itemKey === 'ordinary')
  assert.equal(base.score, 69)
  assert.equal(base.basis, '合成基础分调整，用于验证提案人身份保护')
  for (const key of ['recorder', 'recorderSid', 'recorderId', 'proposer', 'proposerId']) assert.equal(key in base, false)
  assert.equal(JSON.stringify(result.details.baseItems).includes(roster[3].sid), false)
  assert.equal(JSON.stringify(result.details.baseItems).includes(roster[3].name), false)
}
score = await mine()
assert.equal(score.stale, false)
assertRecorderPrivate(score)
await importGPA([record(student, 81)])
score = await mine()
assert.equal(score.stale, true)
assertRecorderPrivate(score)
console.log('✓ 生效基础分提案的姓名和学号不返回学生，旧结算失效后仍受保护，班管仍可追溯提案人')

mkdirSync('.ai-eval', { recursive: true })
writeFileSync('.ai-eval/current-score-fixture.json', JSON.stringify({ studentSid: student.sid, adminSid: admin.sid, unregisteredSid: roster[1].sid, otherSid: other.sid, schemeId: scheme.id }))
console.log('✓ 已保存本地合成成员标识，供浏览器验收使用')
