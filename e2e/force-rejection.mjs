#!/usr/bin/env node
/* 班管与本人主动强制驳回真实 HTTP 回归。业务状态全部通过 API 创建；数据库只读核验。
   node e2e/run.mjs force-rejection；也可连接已启动的隔离合成环境，设置
   API_BASE 与 E2E_COMPOSE_PROJECT。禁止访问生产地址和生产数据库。 */
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const project = process.env.E2E_COMPOSE_PROJECT ?? 'easygpa-plus-e2e'
assert.match(project, /^easygpa-(?:e2e|force-reject)$/, 'Only this task’s synthetic Compose projects are allowed')
const stamp = Date.now().toString(36)
const password = 'force-rejection-e2e-synthetic-password'
const reason = '原始材料核验后发现不符合申报条件，本条强制驳回并取消原认定分'

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
  schemeName: '强制驳回合成回归方案',
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
  await call('POST', '/ops/tenants', { token: ops.access_token, body: { name: `强制驳回回归 ${stamp}`, slug: `force-rejection-${stamp}`, adminSid: `FA${stamp}`, adminName: '回归班管' } })
  const admin = await register(`FA${stamp}`, '回归班管')
  const people = [
    { sid: `FS${stamp}`, name: '回归学生', role: 'student' },
    ...['甲', '乙', '丙', '丁'].map((name, index) => ({ sid: `FG${index}${stamp}`, name: `回归审核${name}`, role: 'group' })),
  ]
  await call('POST', '/admin/whitelist/import', { token: admin.token, body: { csv: ['sid,name,role', ...people.map((p) => `${p.sid},${p.name},${p.role}`)].join('\n') } })
  const sessions = []
  for (const person of people) sessions.push(await register(person.sid, person.name))
  const [student, ...reviewers] = sessions
  const members = (await call('GET', '/admin/users', { token: admin.token })).items
  for (const person of [admin, ...sessions]) {
    person.id = members.find((row) => row.sid === person.sid)?.id
    assert.ok(person.id, 'Each fixture has a stable member identity')
  }
  const classId = numericID(admin.classId)
  const scheme = await call('POST', '/admin/scheme', { token: admin.token, body: { name: config.schemeName, config } })
  await call('POST', `/admin/scheme/${scheme.id}/publish`, { token: admin.token })
  const open = new Date(Date.now() - 3600_000).toISOString()
  const close = new Date(Date.now() + 30 * 86400_000).toISOString()
  await call('PUT', '/admin/timeline', { token: admin.token, body: { open, close, honorRollTopPercent: 30 } })
  const capability = (key, on) => call('PUT', '/admin/window/capabilities', { token: admin.token, body: { key, on } })
  for (const key of ['submit', 'edit', 'review', 'appeal', 'arbitrate', 'studentReport']) await capability(key, true)
  const reviewPool = [admin, ...reviewers]
  const endpoint = (submission) => `/admin/submissions/${submission.id}/force-reject`
  const selfEndpoint = (submission) => `/submissions/${submission.id}/force-reject`
  const ownerView = async (submission, owner = student) => (await call('GET', `/submissions/${submission.id}`, { token: owner.token })).submission
  const eventCount = (submission) => db(`SELECT count(*)::int FROM outbox_event WHERE class_id=${classId} AND type='submission.force_rejected' AND payload->>'submissionId'='${numericID(submission.id)}'`)

  async function submit(owner, title) {
    const row = await call('POST', '/submissions', { token: owner.token, body: { category: 'practice', itemKey: 'activity', title, claim: { score: 4 }, note: '仅用于本机隔离回归的合成材料说明。' } })
    await call('POST', `/submissions/${row.id}/submit`, { token: owner.token })
    const assigned = await waitFor(async () => {
      const found = []
      for (const candidate of reviewPool) {
        const queue = await call('GET', '/review/tasks', { token: candidate.token })
        if (queue.items.some((task) => task.id === row.id)) found.push(candidate)
      }
      return found.length === 2 ? found : null
    }, '单项双人初审分配')
    for (const candidate of assigned) await call('POST', `/review/tasks/${row.id}/decision`, { token: candidate.token, body: { decision: 'accepted', reason: '合成材料初审核对完整', spentSeconds: 10 } })
    assert.equal((await ownerView(row, owner)).finalScore, 4)
    return { ...row, assigned }
  }

  const scored = await submit(student, '已定分强制驳回材料')
  const appealing = await submit(student, '申诉在途强制驳回材料')
  const disputed = await submit(student, '异议举报分类在途强制驳回材料')
  const locked = await submit(student, '已确认已结算强制驳回材料')
  const selfScored = await submit(student, '本人已定分主动驳回材料')
  const selfLocked = await submit(student, '本人已结算主动驳回材料')
  const groupOwned = await submit(reviewers[0], '小组成员本人主动驳回材料')
  const self = await submit(admin, '班管本人材料')
  const untouched = await submit(student, '保留供浏览器操作的材料')
  const correction = await submit(student, '强制改分合成材料')
  const adminCorrection = await submit(admin, '副班管强制改分材料')
  const draft = await call('POST', '/submissions', { token: student.token, body: { category: 'practice', itemKey: 'activity', title: '尚未送审的草稿', claim: { score: 4 } } })

  await call('POST', '/ops/tenants', { token: ops.access_token, body: { name: `强制驳回外班 ${stamp}`, slug: `force-rejection-other-${stamp}`, adminSid: `FOA${stamp}`, adminName: '外班班管' } })
  const otherAdmin = await register(`FOA${stamp}`, '外班班管')
  const scoreEndpoint = (row) => `/admin/submissions/${row.id}/force-score`
  const scoreBody = { score: 7.125, previousScore: 4, reason: '核对合成原件后订正认定分，保留材料和原审核意见' }
  await denied(scoreEndpoint(correction), student, scoreBody, 403)
  await denied(scoreEndpoint(correction), reviewers[0], scoreBody, 403)
  await denied(scoreEndpoint(correction), otherAdmin, scoreBody, 404)
  await denied(scoreEndpoint(adminCorrection), admin, scoreBody, 403, 'avoid_self')
  await denied(scoreEndpoint(draft), admin, scoreBody, 409, 'force_score_unavailable')
  await call('POST', scoreEndpoint(correction), { body: scoreBody, status: 401 })
  for (const invalid of [{ ...scoreBody, reason: ' ' }, { ...scoreBody, score: 11 }, { ...scoreBody, score: 0 }, { ...scoreBody, score: 4 }, { ...scoreBody, score: null }]) {
    await call('POST', scoreEndpoint(correction), { token: admin.token, body: invalid, status: 422 })
  }
  await denied(scoreEndpoint(correction), admin, { ...scoreBody, previousScore: 3 }, 409, 'score_changed')
  const correctionBefore = await call('GET', `/submissions/${correction.id}`, { token: student.token })
  const correctionAppeal = await call('POST', '/appeals/draft', { token: student.token, body: { targetType: 'submission', targetId: correction.id, reason: '合成申诉在途改分测试' } })
  await call('POST', `/appeals/${correctionAppeal.id}/submit`, { token: student.token, body: { reason: '合成申诉在途改分测试', score: 5 } })
  for (const key of ['review', 'appeal', 'arbitrate']) await capability(key, false)
  await call('POST', scoreEndpoint(correction), { token: admin.token, body: scoreBody })
  for (const key of ['review', 'appeal', 'arbitrate']) await capability(key, true)
  const corrected = await call('GET', `/submissions/${correction.id}`, { token: student.token })
  assert.equal(corrected.submission.finalScore, 7.125)
  assert.equal(corrected.submission.canAppeal, false)
  assert.equal(corrected.submission.forcedScore.previousScore, 4)
  assert.equal(corrected.submission.forcedScore.score, 7.125)
  assert.equal('actorName' in corrected.submission.forcedScore, false)
  assert.deepEqual(corrected.evidence, correctionBefore.evidence)
  assert.deepEqual(corrected.reviews, correctionBefore.reviews)
  assert.equal(db(`SELECT to_json(status='final' AND resolution_score=7.125) FROM appeal WHERE id=${numericID(correctionAppeal.id)}`), true)
  await denied(scoreEndpoint(correction), admin, scoreBody, 409, 'score_changed')
  await call('POST', `/admin/submissions/${correction.id}/arbitrate`, { token: admin.token, body: { score: 4, reason: '旧仲裁不得覆盖强制改分结论' }, rejected: true })
  await call('POST', '/appeals/draft', { token: student.token, body: { targetType: 'submission', targetId: correction.id, reason: '已强制改分不得新建申诉' }, rejected: true })
  await call('POST', scoreEndpoint(correction), { token: admin.token, body: { ...scoreBody, previousScore: 7.125, score: 6 } })
  assert.equal((await ownerView(correction)).finalScore, 6)
  assert.equal(db(`SELECT count(*)::int FROM audit_log WHERE class_id=${classId} AND action='submission.force_scored' AND resource_id='${numericID(correction.id)}'`), 2)
  assert.equal(db(`SELECT count(*)::int FROM outbox_event WHERE class_id=${classId} AND type='submission.force_scored' AND payload->>'submissionId'='${numericID(correction.id)}'`), 2)
  await call('PUT', '/admin/deputy', { token: admin.token, body: { userId: reviewers[0].id } })
  await call('POST', `/review/deputy/submissions/${adminCorrection.id}/force-score`, { token: reviewers[0].token, body: scoreBody })
  await denied(`/review/deputy/submissions/${correction.id}/force-score`, reviewers[0], { ...scoreBody, previousScore: 6 }, 403)
  await call('PUT', '/admin/deputy', { token: admin.token, body: { userId: null } })
  const adminCorrectionView = (await call('GET', `/admin/submissions?id=${correction.id}`, { token: admin.token })).items[0]
  assert.equal(adminCorrectionView.canForceScore, true)
  const corrections = await call('GET', '/admin/adjudication-history?kind=force_score', { token: admin.token })
  assert.equal(corrections.items.filter((entry) => entry.submissionId === correction.id).length, 2)
  console.log('✓ 强制改分校验已定分、规则范围、原分冲突、本人回避及副班管范围；保留历史并关闭旧申诉')
  await denied(endpoint(scored), student, { reason }, 403)
  await denied(endpoint(scored), reviewers[0], { reason }, 403)
  await denied(endpoint(scored), otherAdmin, { reason }, 404)
  await denied(endpoint(self), admin, { reason }, 403, 'avoid_self')
  await denied(endpoint(draft), admin, { reason }, 409, 'submission_not_submitted')
  await denied(selfEndpoint(draft), student, { reason }, 409, 'self_force_reject_unavailable')
  await call('POST', `/submissions/${draft.id}/submit`, { token: student.token })
  await denied(selfEndpoint(draft), student, { reason }, 409, 'self_force_reject_unavailable')
  await call('POST', `/submissions/${draft.id}/withdraw`, { token: student.token })
  await call('DELETE', `/submissions/${draft.id}`, { token: student.token })
  await call('POST', endpoint(scored), { body: { reason }, status: 401 })
  await call('POST', endpoint(scored), { token: admin.token, body: { reason: ' ' }, status: 422 })
  assert.equal((await ownerView(scored)).finalScore, 4)
  assert.equal(eventCount(scored), 0)
  console.log('✓ 管理员接口保留角色、租户与本人回避边界；空理由无副作用')

  await call('POST', selfEndpoint(selfScored), { body: { reason }, status: 401 })
  await denied(selfEndpoint(selfScored), reviewers[0], { reason }, 404)
  await denied(selfEndpoint(selfScored), admin, { reason }, 404)
  await denied(selfEndpoint(selfScored), otherAdmin, { reason }, 404)
  await denied(selfEndpoint(selfScored), student, { reason: ' ' }, 422, 'reason_required')
  await denied(selfEndpoint(selfScored), student, { reason: '错'.repeat(2001) }, 422, 'reason_required')
  assert.equal((await ownerView(selfScored)).canSelfForceReject, true)
  assert.equal(eventCount(selfScored), 0)
  const selfReason = '本人发现上传了另一场活动的证书，主动放弃此项分数'
  for (const row of [selfScored, groupOwned, self]) {
    const owner = row === groupOwned ? reviewers[0] : row === self ? admin : student
    const before = await call('GET', `/submissions/${row.id}`, { token: owner.token })
    await call('POST', selfEndpoint(row), { token: owner.token, body: { reason: `  ${selfReason}  ` } })
    const after = await call('GET', `/submissions/${row.id}`, { token: owner.token })
    assert.equal(after.submission.finalScore, 0)
    assert.equal(after.submission.canSelfForceReject, false)
    assert.equal(after.submission.canAppeal, false)
    assert.equal(after.submission.forceRejection.selfRejected, true)
    assert.equal(after.submission.forceRejection.previousScore, 4)
    assert.equal(after.submission.forceRejection.reason, selfReason)
    assert.deepEqual(after.reviews, before.reviews)
    assert.deepEqual(after.evidence, before.evidence)
    assert.equal(after.submission.note, before.submission.note)
    await denied(selfEndpoint(row), owner, { reason: '不可覆盖第一次主动驳回' }, 409, 'submission_force_rejected')
    assert.equal(eventCount(row), 1)
    assert.equal(db(`SELECT count(*)::int FROM audit_log WHERE class_id=${classId} AND action='submission.force_rejected' AND resource_id='${numericID(row.id)}' AND actor_id=${numericID(owner.id)} AND metadata->>'selfRejected'='true'`), 1)
  }
  console.log('✓ 本人入口严格校验所有权和已定分状态；学生、小组及班管均可放弃本人分数，历史与审计保留')

  const appealDraft = await call('POST', '/appeals/draft', { token: student.token, body: { targetType: 'submission', targetId: scored.id, reason: '尚未提交的合成申诉草稿' } })
  const objectionDraft = await call('POST', '/review/objections', { token: reviewers[0].token, body: { kind: 'submission', studentUserId: student.id, category: 'practice', itemKey: 'activity', targetId: scored.id, proposedScore: 3, basis: '尚未提交的合成小组异议草稿' } })
  // The ordinary scoring rule has min=1: a forced rejection must still set 0.
  for (const key of ['review', 'appeal', 'arbitrate']) await capability(key, false)
  await call('POST', endpoint(scored), { token: admin.token, body: { reason } })
  for (const key of ['review', 'appeal', 'arbitrate']) await capability(key, true)
  const rejectedView = await ownerView(scored)
  assert.equal(rejectedView.finalScore, 0)
  assert.equal(rejectedView.forceRejection?.reason, reason)
  assert.equal(rejectedView.forceRejection?.previousScore, 4)
  assert.ok(Number.isFinite(Date.parse(rejectedView.forceRejection?.rejectedAt)))
  assert.equal(rejectedView.canAppeal, false)
  assert.equal(eventCount(scored), 1)
  await denied(endpoint(scored), admin, { reason: '第二次点击不得替换原始驳回理由或重复发通知' }, 409, 'submission_force_rejected')
  assert.equal(eventCount(scored), 1)
  assert.deepEqual((await ownerView(scored)).forceRejection, rejectedView.forceRejection)
  await call('POST', `/appeals/${appealDraft.id}/submit`, { token: student.token, rejected: true })
  await call('POST', '/review/objections/submit', { token: reviewers[0].token, body: { ids: [objectionDraft.id] }, rejected: true })
  assert.equal(db(`SELECT count(*)::int FROM appeal WHERE class_id=${classId} AND target_type='submission' AND target_id=${numericID(scored.id)} AND status='draft'`), 0)
  assert.equal(db(`SELECT count(*)::int FROM objection WHERE class_id=${classId} AND kind='submission' AND target_id=${numericID(scored.id)} AND status IN ('draft','submitted')`), 0)
  console.log('✓ 关闭普通审核开关仍可强制清零；学生得到持久驳回提示；重复请求只保留一封通知事件')

  const appeal = await call('POST', '/appeals', { token: student.token, body: { targetType: 'submission', targetId: appealing.id, reason: '合成学生对原始审核提出申诉，请重新核验材料' } })
  assert.equal(appeal.status, 'reviewing')
  assert.equal((await ownerView(appealing)).status, 'appealing')
  await call('POST', selfEndpoint(appealing), { token: student.token, body: { reason } })
  await call('POST', `/review/appeals/${appeal.id}/rereview`, { token: appealing.assigned[0].token, body: { decision: 'uphold', score: 4, reason: '旧申诉请求不得恢复原分', spentSeconds: 10 }, rejected: true })
  await call('POST', `/admin/appeals/${appeal.id}/final`, { token: admin.token, body: { score: 4, reason: '旧终裁请求不得恢复原认定分' }, rejected: true })
  assert.equal((await ownerView(appealing)).finalScore, 0)
  assert.equal(db(`SELECT count(*)::int FROM appeal WHERE class_id=${classId} AND target_type='submission' AND target_id=${numericID(appealing.id)} AND status IN ('draft','filed','reviewing','escalated')`), 0)

  const objectionInput = { kind: 'submission', studentUserId: student.id, category: 'practice', itemKey: 'activity', targetId: disputed.id, proposedScore: 3, basis: '合成小组异议请求修正该材料分数' }
  const objection = await call('POST', '/review/objections', { token: reviewers[0].token, body: objectionInput })
  await call('POST', '/review/objections/submit', { token: reviewers[0].token, body: { ids: [objection.id] } })
  const report = await call('POST', '/reports', { token: reviewers[1].token, body: { ...objectionInput, proposedScore: 2, basis: '合成匿名举报用于核验强制驳回后的旧流程隔离' } })
  const reportReviewers = []
  for (const candidate of reviewPool) {
    const queue = await call('GET', '/review/tasks', { token: candidate.token })
    if (queue.items.some((task) => task.id === `report:${report.id}`)) reportReviewers.push(candidate)
  }
  assert.equal(reportReviewers.length, 2)
  const classification = { category: 'practice', itemKey: 'other_activity', reason: '合成分类建议认为应归入其他实践活动' }
  await call('POST', `/admin/submissions/${disputed.id}/classification/suggest`, { token: admin.token, body: classification })
  await call('POST', selfEndpoint(disputed), { token: student.token, body: { reason } })
  await call('POST', `/admin/objections/${objection.id}/decide`, { token: admin.token, body: { action: 'apply', reason: '旧异议终裁不得恢复原分' }, rejected: true })
  await call('POST', `/review/reports/${report.id}/decision`, { token: reportReviewers[0].token, body: { decision: 'uphold', reason: '旧举报复核不得恢复原分', spentSeconds: 10 }, rejected: true })
  await call('POST', `/admin/submissions/${disputed.id}/classification/resolve`, { token: admin.token, body: { ...classification, score: 3 }, rejected: true })
  assert.equal((await ownerView(disputed)).finalScore, 0)
  assert.deepEqual(db(`SELECT json_build_object('objections',(SELECT count(*) FROM objection WHERE class_id=${classId} AND kind='submission' AND target_id=${numericID(disputed.id)} AND status IN ('draft','submitted')),'reports',(SELECT count(*) FROM report WHERE class_id=${classId} AND kind='submission' AND target_id=${numericID(disputed.id)} AND status IN ('reviewing','escalated')),'classifications',(SELECT count(*) FROM classification_suggestion WHERE class_id=${classId} AND submission_id=${numericID(disputed.id)} AND status IN ('draft','pending')))`), { objections: 0, reports: 0, classifications: 0 })
  console.log('✓ 申诉、异议、举报、分类建议在同次驳回中收尾，旧结论不能把分改回去')

  // New proposals must also be rejected: merely protecting final_score would
  // otherwise leave an unresolvable proposal blocking future confirmation.
  await call('POST', '/appeals/draft', { token: student.token, body: { targetType: 'submission', targetId: scored.id, reason: '被强制驳回后不应再创建新申诉' }, rejected: true })
  await call('POST', '/review/objections', { token: reviewers[0].token, body: { ...objectionInput, targetId: scored.id }, rejected: true })
  await call('POST', `/admin/submissions/${scored.id}/classification/suggest`, { token: admin.token, body: classification, rejected: true })
  await call('POST', `/admin/submissions/${scored.id}/arbitrate`, { token: admin.token, body: { score: 4, reason: '旧仲裁接口不得推翻已记录的强制驳回' }, rejected: true })
  assert.equal((await ownerView(scored)).finalScore, 0)
  await denied(scoreEndpoint(scored), admin, { ...scoreBody, previousScore: 0 }, 409, 'submission_force_rejected')

  await call('POST', '/me/seal', { token: student.token, body: { phrase: '全部提交完成', sid: student.sid } })
  const live = await call('GET', '/me/scorecard', { token: student.token })
  for (const rejected of [scored, appealing, disputed, selfScored]) assert.equal(live.scorecard.items.find((item) => item.id === rejected.id)?.score, 0)
  await call('POST', '/me/scorecard/confirm', { token: student.token, body: { revision: live.revision } })
  await call('POST', '/admin/gate/force', { token: admin.token, body: { reason: '合成回归允许未全部确认的其他成员先生成结算快照' } })
  const settlement = await call('POST', '/admin/settle', { token: admin.token })
  assert.equal((await ownerView(locked)).status, 'locked')
  assert.equal((await ownerView(selfLocked)).status, 'locked')
  // Also verifies the database protection permits scored -> locked on earlier
  // rejected entries during a later, legitimate settlement.
  assert.equal((await ownerView(scored)).status, 'locked')
  // Closing submissions and switching off review/appeal must not prevent an
  // owner from giving up a score; only full class lockdown is a write barrier.
  await call('PUT', '/admin/timeline', { token: admin.token, body: { open, close: new Date(Date.now() - 1000).toISOString() } })
  for (const key of ['review', 'appeal', 'arbitrate']) await capability(key, false)
  await call('POST', scoreEndpoint(correction), { token: admin.token, body: { ...scoreBody, previousScore: 6, score: 5 } })
  assert.equal((await ownerView(correction)).finalScore, 5)
  assert.equal((await call('GET', '/me/score', { token: student.token })).stale, true)
  assert.notEqual((await call('GET', '/me/scorecard', { token: student.token })).state, 'confirmed')
  await call('POST', selfEndpoint(selfLocked), { token: student.token, body: { reason } })
  assert.equal((await ownerView(selfLocked)).finalScore, 0)
  assert.equal((await call('GET', '/me/score', { token: student.token })).stale, true)
  assert.notEqual((await call('GET', '/me/scorecard', { token: student.token })).state, 'confirmed')
  assert.equal(db(`SELECT count(*)::int FROM settlement_invalidation WHERE class_id=${classId} AND run_id=${numericID(settlement.runId)}`), 1)
  assert.equal(db(`SELECT count(*)::int FROM score_acknowledgement WHERE class_id=${classId} AND student_id=${numericID(student.id)}`), 1, 'Historical confirmation must be retained')
  await call('POST', endpoint(locked), { token: admin.token, body: { reason } })
  assert.equal((await ownerView(locked)).finalScore, 0)
  for (const key of ['review', 'appeal', 'arbitrate']) await capability(key, true)
  console.log('✓ 已封存、已确认、已结算条目仍可驳回；原结算与确认失效，历史记录保留')

  const pastClose = new Date(Date.now() - 60_000).toISOString()
  const lockdown = new Date(Date.now() - 30_000).toISOString()
  await call('PUT', '/admin/timeline', { token: admin.token, body: { open, close: pastClose, lockdown } })
  await denied(scoreEndpoint(untouched), admin, scoreBody, 409, 'class_locked')
  assert.equal((await call('GET', `/admin/submissions?id=${untouched.id}`, { token: admin.token })).items[0].canForceScore, false)
  await denied(endpoint(untouched), admin, { reason }, 409, 'class_locked')
  await denied(selfEndpoint(untouched), student, { reason }, 409, 'class_locked')
  assert.equal((await ownerView(untouched)).canSelfForceReject, false)
  assert.equal((await call('GET', '/submissions', { token: student.token })).items.find((row) => row.id === untouched.id)?.canSelfForceReject, false)
  assert.equal((await ownerView(untouched)).finalScore, 4)
  await call('PUT', '/admin/timeline', { token: admin.token, body: { open, close, lockdown: null } })
  for (const row of [scored, appealing, disputed, locked]) assert.equal(eventCount(row), 1)
  assert.equal(eventCount(untouched), 0)
  assert.equal(eventCount(self), 1)
  const board = await call('GET', '/admin/submissions?status=&pageSize=200', { token: admin.token })
  assert.equal(board.items.find((row) => row.id === locked.id)?.forceRejection?.reason, reason)
  console.log('✓ 全系统封锁保持只读；每次有效驳回恰好写入一条学生邮件通知事件')

  if (process.env.FORCE_REJECTION_E2E_UI_FIXTURE) {
    writeFileSync(process.env.FORCE_REJECTION_E2E_UI_FIXTURE, JSON.stringify({ adminSid: admin.sid, studentSid: student.sid, reviewerSid: reviewers[0].sid, className: `强制驳回回归 ${stamp}`, submissionId: untouched.id, rejectedSubmissionId: locked.id }, null, 2))
  }
  console.log('✓ 强制驳回真实 HTTP 回归通过')
}

main().catch((error) => {
  console.error(`强制驳回 E2E 失败：${error.message}`)
  process.exitCode = 1
})
