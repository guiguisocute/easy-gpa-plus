#!/usr/bin/env node
/* 副班管真实 HTTP 回归：任命即时生效、班管回避本人、裁决范围和批量原子性。
   用法：node e2e/run.mjs deputy
   只接受 loopback 且 /healthz 明确为 dev 的合成测试环境。 */
import assert from 'node:assert/strict'
import { writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const api = `${origin}/api/v1`
const stamp = Date.now().toString(36)
const password = 'deputy-e2e-synthetic-password'
const deputyPath = '/review/deputy'

async function call(method, path, { token, body, status } = {}) {
  const headers = e2eHeaders({ Accept: 'application/json' })
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`
  const response = await fetch(api + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const result = await response.json().catch(() => null)
  // Never include a successful auth response or request body in test output.
  if (status !== undefined) {
    assert.equal(response.status, status, `${method} ${path}: expected ${status}, received ${response.status} (${result?.code ?? 'no error code'})`)
  } else {
    assert.ok(response.ok, `${method} ${path}: HTTP ${response.status} (${result?.code ?? 'no error code'})`)
  }
  return result
}

async function denied(method, path, token, body, code) {
  const result = await call(method, path, { token, body, status: 403 })
  if (code) assert.equal(result?.code, code, `${path} error code`)
}

async function register(sid, name) {
  const ticket = await call('POST', '/auth/register/check', { body: { sid, name } })
  const session = await call('POST', '/auth/register/complete', { body: { ticket: ticket.ticket, password } })
  return { sid, name, token: session.access_token }
}

async function waitFor(check, label, timeoutMs = 20_000) {
  const until = Date.now() + timeoutMs
  do {
    const result = await check()
    if (result) return result
    await new Promise((resolve) => setTimeout(resolve, 200))
  } while (Date.now() < until)
  throw new Error(`等待超时：${label}`)
}

const config = {
  schemeName: '副班管权限回归方案',
  weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
  categories: [
    { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    { key: 'moral', name: '思想品德', maxTotal: 100, baseItems: [{ key: 'moral_base', name: '基础分', full: 80 }], penaltyItems: [{ key: 'moral_penalty', name: '违纪扣分', per: -5 }], items: [] },
    { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [
      { key: 'practice_activity', name: '实践活动', scoreRule: { type: 'per_unit', unit: '项', per: 2, cap: 8 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } },
    ] },
    { key: 'health', name: '身体心理', maxTotal: 100, baseItems: [{ key: 'health_base', name: '体测基础分', full: 60 }], penaltyItems: [], items: [
      { key: 'health_meet', name: '运动会', scoreRule: { type: 'per_unit', unit: '项', per: 2, cap: 8 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } },
      { key: 'health_activity', name: '健康活动', scoreRule: { type: 'per_unit', unit: '项', per: 2, cap: 8 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } },
    ] },
  ],
}

async function main() {
  await assertLocalDevelopmentAPI(origin)
  const ops = await call('POST', '/auth/login', { body: { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' } })
  await call('POST', '/ops/tenants', { token: ops.access_token, body: { name: `副班管回归 ${stamp}`, slug: `deputy-${stamp}`, adminSid: `DA${stamp}`, adminName: '回归班管' } })
  const admin = await register(`DA${stamp}`, '回归班管')
  const people = [
    { sid: `DS${stamp}`, name: '回归学生', role: 'student' },
    { sid: `DG1${stamp}`, name: '回归副班管', role: 'group' },
    { sid: `DG2${stamp}`, name: '回归审核乙', role: 'group' },
    { sid: `DG3${stamp}`, name: '回归审核丙', role: 'group' },
    { sid: `DG4${stamp}`, name: '回归审核丁', role: 'group' },
    { sid: `DG5${stamp}`, name: '回归未注册', role: 'group' },
  ]
  await call('POST', '/admin/whitelist/import', { token: admin.token, body: { csv: ['sid,name,role', ...people.map((person) => `${person.sid},${person.name},${person.role}`)].join('\n') } })
  const sessions = []
  for (const person of people.slice(0, -1)) sessions.push(await register(person.sid, person.name))
  const [student, deputy, reviewer, reviewer2, reviewer3] = sessions
  const draft = await call('POST', '/admin/scheme', { token: admin.token, body: { name: config.schemeName, config } })
  await call('POST', `/admin/scheme/${draft.id}/publish`, { token: admin.token })
  const users = (await call('GET', '/review/students', { token: admin.token })).items
  for (const person of [admin, ...sessions, people.at(-1)]) {
    person.id = users.find((row) => row.sid === person.sid)?.userId
    assert.ok(person.id, `${person.name} has a stable business identity`)
  }
  const A = admin.token
  const D = deputy.token
  const R = reviewer.token
  const S = student.token
  const appoint = (id) => call('PUT', '/admin/deputy', { token: A, body: { userId: id === null ? null : Number(id) } })

  await denied('PUT', '/admin/deputy', R, { userId: Number(deputy.id) })
  await denied('GET', `${deputyPath}/objections`, D)
  await call('PUT', '/admin/deputy', { token: A, body: {}, status: 400 })
  await call('PUT', '/admin/deputy', { token: A, body: { userId: Number(student.id) }, status: 409 })
  await call('PUT', '/admin/deputy', { token: A, body: { userId: Number(people.at(-1).id) }, status: 409 })
  // Imported group identities can receive ordinary review assignments before
  // registration; retire this eligibility-only fixture from the reviewer pool.
  await call('PUT', `/admin/users/${people.at(-1).id}/role`, { token: A, body: { role: 'student' } })
  await appoint(deputy.id)
  const me = await call('GET', '/me', { token: D })
  assert.equal(me.role, 'group')
  assert.equal(me.isDeputy, true)
  await call('GET', `${deputyPath}/objections`, { token: D })
  await denied('GET', '/admin/users', D)
  assert.equal((await call('GET', '/admin/users', { token: A })).items.filter((row) => row.isDeputy).length, 1)
  await appoint(reviewer.id)
  assert.equal((await call('GET', '/me', { token: D })).isDeputy, false)
  await denied('GET', `${deputyPath}/objections`, D)
  await call('GET', `${deputyPath}/objections`, { token: R })
  await appoint(null)
  await denied('GET', `${deputyPath}/objections`, R)
  await call('POST', '/ops/tenants', { token: ops.access_token, body: { name: `副班管外班 ${stamp}`, slug: `deputy-other-${stamp}`, adminSid: `DOA${stamp}`, adminName: '外班班管' } })
  const otherAdmin = await register(`DOA${stamp}`, '外班班管')
  await call('POST', '/admin/whitelist/import', { token: otherAdmin.token, body: { csv: `sid,name,role\nDOG${stamp},外班小组,group` } })
  await register(`DOG${stamp}`, '外班小组')
  const otherGroup = (await call('GET', '/admin/users', { token: otherAdmin.token })).items.find((row) => row.sid === `DOG${stamp}`)
  await call('PUT', '/admin/deputy', { token: A, body: { userId: Number(otherGroup.id) }, status: 404 })
  await appoint(reviewer3.id)
  await call('PUT', `/admin/users/${reviewer3.id}/status`, { token: A, body: { status: 'disabled' } })
  await call('PUT', '/admin/deputy', { token: A, body: { userId: Number(reviewer3.id) }, status: 409 })
  await call('GET', `${deputyPath}/objections`, { token: reviewer3.token, status: 401 })
  await call('PUT', `/admin/users/${reviewer3.id}/status`, { token: A, body: { status: 'active' } })
  reviewer3.token = (await call('POST', '/auth/login', { body: { account: reviewer3.sid, password } })).access_token
  assert.equal((await call('GET', '/me', { token: reviewer3.token })).isDeputy, false)
  await denied('GET', `${deputyPath}/objections`, reviewer3.token)
  await appoint(reviewer3.id)
  await call('PUT', `/admin/users/${reviewer3.id}/role`, { token: A, body: { role: 'student' } })
  await call('PUT', `/admin/users/${reviewer3.id}/role`, { token: A, body: { role: 'group' } })
  reviewer3.token = (await call('POST', '/auth/login', { body: { account: reviewer3.sid, password } })).access_token
  assert.equal((await call('GET', '/me', { token: reviewer3.token })).isDeputy, false)
  await denied('GET', `${deputyPath}/objections`, reviewer3.token)
  await appoint(deputy.id)
  console.log('✓ 任命、替换、撤销即时生效；外班、未注册和停用成员不可任命；恢复账号不恢复任命')

  await call('PUT', '/admin/window', { token: A, body: { open: new Date(Date.now() - 3600_000).toISOString(), close: new Date(Date.now() + 30 * 86400_000).toISOString(), honorRollTopPercent: 20 } })
  for (const key of ['submit', 'edit', 'appeal', 'review', 'arbitrate', 'studentReport']) {
    await call('PUT', '/admin/window/capabilities', { token: A, body: { key, on: true } })
  }
  const reviewerSessions = [admin, deputy, reviewer, reviewer2, reviewer3]
  async function submit(person, title, conflict) {
    const submission = await call('POST', '/submissions', { token: person.token, body: { category: 'health', itemKey: 'health_meet', title, claim: { quantity: 2 } } })
    await call('POST', `/submissions/${submission.id}/submit`, { token: person.token })
    if (person === admin) {
      const deputyQueue = (await call('GET', `${deputyPath}/submissions?status=`, { token: D })).items
      assert.ok(!deputyQueue.some((row) => row.id === submission.id), 'Pending reviews must not be exposed in deputy arbitration')
      await call('GET', `/review/tasks/${submission.id}`, { token: A, status: 404 })
    }
    const assigned = await waitFor(async () => {
      const found = []
      for (const candidate of reviewerSessions) {
        const queue = await call('GET', '/review/tasks', { token: candidate.token })
        if (queue.items.some((row) => row.id === submission.id)) found.push(candidate)
      }
      return found.length === 2 ? found : null
    }, '双人审核分配')
    await call('POST', `/review/tasks/${submission.id}/decision`, { token: assigned[0].token, body: { decision: 'accepted', reason: '合成记录核对完整', spentSeconds: 10 } })
    const result = await call('POST', `/review/tasks/${submission.id}/decision`, { token: assigned[1].token, body: { decision: conflict ? 'adjusted' : 'accepted', ...(conflict ? { score: 2 } : {}), reason: '合成记录独立核对', spentSeconds: 10 } })
    assert.equal(Boolean(result.conflict), conflict)
    return { ...submission, assigned }
  }

  const adminConflict = await submit(admin, '班管待仲裁材料', true)
  const studentConflict = await submit(student, '学生待仲裁材料', true)
  const arbitration = { score: 3, reason: '副班管核对原始审核记录后裁定' }
  await denied('POST', `/admin/submissions/${adminConflict.id}/arbitrate`, A, arbitration, 'avoid_self')
  await denied('POST', `${deputyPath}/submissions/${adminConflict.id}/arbitrate`, R, arbitration)
  await denied('POST', `${deputyPath}/submissions/${studentConflict.id}/arbitrate`, D, arbitration, 'deputy_scope')
  const deputyConflicts = (await call('GET', `${deputyPath}/submissions?status=arbitrating`, { token: D })).items
  assert.ok(deputyConflicts.some((row) => row.id === adminConflict.id))
  assert.ok(!deputyConflicts.some((row) => row.id === studentConflict.id))
  const arbitrated = await call('POST', `${deputyPath}/submissions/${adminConflict.id}/arbitrate`, { token: D, body: arbitration })
  assert.equal(arbitrated.finalScore, 3)
  await call('POST', `/admin/submissions/${studentConflict.id}/arbitrate`, { token: A, body: arbitration })
  console.log('✓ 班管不能仲裁本人；副班管只看到并处理班管冲突，原班管学生仲裁仍可用')

  async function proposal(person, category = 'moral', itemKey = 'moral_base', proposedScore = 70) {
    const row = await call('POST', '/review/objections', { token: R, body: { kind: 'base', studentUserId: person.id, category, itemKey, targetId: null, proposedScore, basis: '合成记录存在差异，需要终裁复核' } })
    await call('POST', '/review/objections/submit', { token: R, body: { ids: [row.id] } })
    return row
  }
  const adminProposal = await proposal(admin)
  const studentProposal = await proposal(student)
  const decision = { action: 'apply', reason: '依据合成记录复核后采纳' }
  await denied('POST', `/admin/objections/${adminProposal.id}/decide`, A, decision, 'avoid_self')
  await denied('POST', `${deputyPath}/objections/${studentProposal.id}/decide`, D, decision, 'deputy_scope')
  await denied('POST', `${deputyPath}/objections/decide-batch`, D, { ...decision, ids: [adminProposal.id, studentProposal.id] }, 'deputy_scope')
  const pending = (await call('GET', `${deputyPath}/objections?status=submitted`, { token: D })).items
  assert.ok(pending.some((row) => row.id === adminProposal.id), 'Mixed batch must roll back its in-scope first item')
  assert.ok(!pending.some((row) => row.id === studentProposal.id), 'Deputy proposal queue must exclude ordinary students')
  assert.equal((await call('POST', `${deputyPath}/objections/${adminProposal.id}/decide`, { token: D, body: decision })).status, 'applied')
  await call('POST', `/admin/objections/${studentProposal.id}/decide`, { token: A, body: decision })
  console.log('✓ 基础分提案终裁受范围约束；混合范围批量请求原子回滚')

  async function escalatedAppeal(person) {
    const submission = await submit(person, `${person.name}两轮申诉材料`, false)
    const input = { targetType: 'submission', targetId: submission.id, reason: '合成回归核验两轮申诉的回避规则' }
    const first = await call('POST', '/appeals', { token: person.token, body: input })
    assert.equal(first.status, 'reviewing')
    for (const candidate of submission.assigned) {
      await call('POST', `/review/appeals/${first.id}/rereview`, { token: candidate.token, body: { decision: 'uphold', score: 4, reason: '复核合成材料维持原结论', spentSeconds: 10 } })
    }
    const second = await call('POST', '/appeals', { token: person.token, body: input })
    assert.equal(second.status, 'escalated')
    return second
  }
  const adminAppeal = await escalatedAppeal(admin)
  const studentAppeal = await escalatedAppeal(student)
  const final = { score: 4, reason: '独立核对两轮记录后签发最终结论' }
  await denied('POST', `/admin/appeals/${adminAppeal.id}/final`, A, final, 'avoid_self')
  await denied('POST', `${deputyPath}/appeals/${studentAppeal.id}/final`, D, final, 'deputy_scope')
  await denied('GET', `${deputyPath}/appeals/${studentAppeal.id}`, D, undefined, 'deputy_scope')
  await call('GET', `${deputyPath}/appeals/${adminAppeal.id}`, { token: D })
  assert.equal((await call('POST', `${deputyPath}/appeals/${adminAppeal.id}/final`, { token: D, body: final })).status, 'final')
  await call('POST', `/admin/appeals/${studentAppeal.id}/final`, { token: A, body: final })
  console.log('✓ 两轮申诉与详情权限遵守回避范围；副班管可终裁班管申诉')

  const classify = { category: 'health', itemKey: 'health_activity', score: 3, reason: '材料应按同大项另一小项认定' }
  await denied('POST', `/admin/submissions/${adminConflict.id}/classification/resolve`, A, classify, 'avoid_self')
  await denied('POST', `${deputyPath}/submissions/${studentConflict.id}/classification/resolve`, D, classify, 'deputy_scope')
  await call('POST', `${deputyPath}/submissions/${adminConflict.id}/classification/resolve`, { token: D, body: classify })
  console.log('✓ 分类终裁同样回避班管本人并限制副班管范围')

  async function escalatedReport(person, reporter, target = {}) {
    const report = await call('POST', '/reports', { token: reporter.token, body: { kind: 'base', studentUserId: person.id, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 60, basis: '此为合成举报记录，用于验证副班管独立终裁权限', ...target } })
    const assigned = []
    for (const candidate of reviewerSessions) {
      const queue = await call('GET', '/review/tasks', { token: candidate.token })
      if (queue.items.some((row) => row.id === `report:${report.id}`)) assigned.push(candidate)
    }
    assert.equal(assigned.length, 2)
    await call('POST', `/review/reports/${report.id}/decision`, { token: assigned[0].token, body: { decision: 'uphold', reason: '合成依据完整同意举报主张', spentSeconds: 10 } })
    const decision = await call('POST', `/review/reports/${report.id}/decision`, { token: assigned[1].token, body: { decision: 'reject', reason: '合成依据不足维持当前分数', spentSeconds: 10 } })
    assert.equal(decision.reportStatus, 'escalated')
    return report
  }
  const adminReport = await escalatedReport(admin, student)
  const studentReport = await escalatedReport(student, reviewer)
  const reportFinal = { score: 65, reason: '独立核验合成举报与双方复核意见后裁定' }
  await denied('POST', `/admin/reports/${adminReport.id}/final`, A, reportFinal, 'avoid_self')
  await denied('POST', `${deputyPath}/reports/${studentReport.id}/final`, D, reportFinal, 'deputy_scope')
  const reports = (await call('GET', `${deputyPath}/reports`, { token: D })).items
  assert.ok(reports.some((row) => row.id === adminReport.id))
  assert.ok(!reports.some((row) => row.id === studentReport.id))
  assert.equal((await call('GET', `/review/reports/${adminReport.id}/target`, { token: D })).kind, 'base')
  assert.equal((await call('POST', `${deputyPath}/reports/${adminReport.id}/final`, { token: D, body: reportFinal })).status, 'final')
  await call('POST', `/admin/reports/${studentReport.id}/final`, { token: A, body: reportFinal })
  const submissionReport = await escalatedReport(admin, student, { kind: 'submission', category: 'health', itemKey: 'health_activity', targetId: adminConflict.id, proposedScore: 1 })
  assert.equal((await call('GET', `/review/reports/${submissionReport.id}/target`, { token: D })).submission.id, adminConflict.id)
  await call('POST', `${deputyPath}/reports/${submissionReport.id}/final`, { token: D, body: { score: 2, reason: '副班管复核合成申报记录认定为两分' } })
  assert.equal((await call('GET', '/submissions', { token: A })).items.find((row) => row.id === adminConflict.id)?.finalScore, 2)
  const penaltyReport = await escalatedReport(admin, student, { kind: 'penalty', itemKey: 'moral_penalty', quantity: 2, proposedScore: -10 })
  assert.equal((await call('POST', `${deputyPath}/reports/${penaltyReport.id}/final`, { token: D, body: { score: -5, reason: '副班管复核合成记录认定一次扣分' } })).score, -5)
  console.log('✓ 举报终裁覆盖基础分正分、申报改分和扣分负分，并保持学生与班管权限隔离')

  // Keep one meaningful pending record for optional real-browser verification.
  await proposal(admin, 'health', 'health_base', 55)
  await call('POST', `/admin/users/${deputy.id}/reset-password`, { token: A })
  const resetAppointment = (await call('GET', '/admin/deputy', { token: A })).deputy
  assert.equal(resetAppointment?.id, deputy.id)
  assert.equal(resetAppointment.registered, false)
  await call('GET', `${deputyPath}/objections`, { token: D, status: 401 })
  const registeredAgain = await register(deputy.sid, deputy.name)
  assert.equal((await call('GET', '/me', { token: registeredAgain.token })).isDeputy, true)
  await call('GET', `${deputyPath}/objections`, { token: registeredAgain.token })
  console.log('✓ 重置密码保留副班管任命；重新注册后恢复原职责')
  if (process.env.DEPUTY_E2E_UI_FIXTURE) {
    await call('POST', `${deputyPath}/submissions/${adminConflict.id}/classification/suggest`, { token: registeredAgain.token, body: { category: 'health', itemKey: 'health_activity', reason: '浏览器验收合成分类建议，请从分类确认进入仲裁' } })
    writeFileSync(process.env.DEPUTY_E2E_UI_FIXTURE, JSON.stringify({ adminSid: admin.sid, deputySid: deputy.sid, reviewerSid: reviewer.sid, studentSid: student.sid, submissionId: adminConflict.id, className: `副班管回归 ${stamp}` }, null, 2))
  }
  console.log('✓ 副班管真实 API 回归通过')
}

main().catch((error) => {
  console.error(`副班管 E2E 失败：${error.message}`)
  process.exitCode = 1
})
