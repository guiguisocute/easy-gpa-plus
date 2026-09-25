// Opt-in development test. No production endpoints or real student records.
// Setup: DEEPSEEK_API_KEY=<local secret> AGENT_CONTEXT_FIXTURE=<private local path>
//        node e2e/agent-page-context.mjs setup
// Verify: node e2e/agent-page-context.mjs verify (same fixture path)
import assert from 'node:assert/strict'
import { readFileSync, writeFileSync } from 'node:fs'
import { randomBytes } from 'node:crypto'
import { isAbsolute, relative, resolve, sep } from 'node:path'
import { syntheticScheme } from './fake-openai.mjs'
import { assertLocalDevelopmentAPI, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:38080')
const API = `${origin}/api/v1`
const fixturePath = process.env.AGENT_CONTEXT_FIXTURE
if (!fixturePath) throw new Error('Set AGENT_CONTEXT_FIXTURE to a private local file outside the repository')
const fixtureRelative = relative(process.cwd(), resolve(fixturePath))
if (!isAbsolute(fixtureRelative) && fixtureRelative !== '..' && !fixtureRelative.startsWith(`..${sep}`)) throw new Error('The development fixture must be outside the repository')
const mode = process.argv[2] ?? 'verify'
const password = process.env.AGENT_CONTEXT_PASSWORD || (mode === 'setup' ? randomBytes(24).toString('base64url') : JSON.parse(readFileSync(fixturePath, 'utf8')).password)
if (!password) throw new Error('Provide AGENT_CONTEXT_PASSWORD for an older development fixture')
const env = Object.fromEntries(readFileSync('.env.development', 'utf8').split(/\r?\n/).filter((line) => /^[A-Z_]+=/.test(line)).map((line) => { const at = line.indexOf('='); return [line.slice(0, at), line.slice(at + 1).replace(/^['"]|['"]$/g, '')] }))

async function call(method, path, token, body, status) {
  const response = await fetch(API + path, { method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) })
  const parsed = await response.json().catch(() => null)
  if (status) { assert.equal(response.status, status, `${method} ${path}`); return parsed }
  if (!response.ok) throw new Error(`${method} ${path}: ${response.status} ${parsed?.error?.message ?? parsed?.message ?? 'request failed'}`)
  return parsed
}
async function login(account, secret = password) { return (await call('POST', '/auth/login', null, { account, password: secret })).access_token }
async function register(sid, name) {
  const checked = await call('POST', '/auth/register/check', null, { sid, name })
  return (await call('POST', '/auth/register/complete', null, { ticket: checked.ticket, password })).access_token
}
async function waitFor(check, timeout = 120000) {
  const end = Date.now() + timeout
  while (Date.now() < end) { const value = await check(); if (value) return value; await new Promise((r) => setTimeout(r, 900)) }
  throw new Error('Development scenario timed out')
}
const contextPath = (kind, id, view = 'admSubs') => `/agent/context?${new URLSearchParams({ view, resourceKind: kind, resourceId: id })}`

async function setup() {
  const key = process.env.DEEPSEEK_API_KEY || (process.env.AGENT_KEY_STDIN === '1' ? readFileSync(0, 'utf8').trim() : '')
  const ops = await login(env.OPS_ACCOUNT || 'ops@localhost', env.OPS_PASSWORD || 'ops-dev-change-me')
  const providers = await call('GET', '/ops/agent/providers', ops)
  const name = '随页 Agent 开发验证'
  const existing = providers.items.find((row) => row.name === name)
  if (!key && !existing) throw new Error('A development DeepSeek key is required for the initial provider setup; it is never written to the fixture')
  const providerInput = { name, baseUrl: 'https://api.deepseek.com', ...(key ? { apiKey: key } : {}), enabled: true, timeoutSeconds: 120, maxRetries: 1, capabilities: { vision: true, json: true, stream: true, models: true } }
  let providerId = existing?.id
  if (providerId) await call('PUT', `/ops/agent/providers/${providerId}`, ops, providerInput)
  else providerId = (await call('POST', '/ops/agent/providers', ops, providerInput)).id
  for (const purpose of ['agent.text', 'agent.vision']) await call('PUT', `/ops/agent/routes/${purpose}`, ops, { providerId, model: 'deepseek-v4-flash-vision-exp', parameters: {} })
  for (const flag of ['aiEnabled', 'knowledgeEnabled', 'knowledgeEgressEnabled', 'agentActionsEnabled']) await call('PUT', `/ops/flags/${flag}`, ops, { value: true })
  await call('PUT', '/ops/agent', ops, { agentMaxSteps: 16, agentTimeoutSeconds: 300, agentToolResultKb: 64 })
  console.log('✓ Development Agent routes configured; secret stored only in encrypted local provider configuration')

  const stamp = Date.now().toString(36)
  const adminSid = `CA${stamp}`, studentSid = `CS${stamp}`
  const tenant = await call('POST', '/ops/tenants', ops, { name: `随页 Agent 开发验证 ${stamp}`, slug: `agent-context-${stamp}`, adminSid, adminName: '开发班管' })
  const admin = await register(adminSid, '开发班管')
  const people = [[studentSid, '示例学生 A', 'student'], [`CG${stamp}1`, '示例审核甲', 'group'], [`CG${stamp}2`, '示例审核乙', 'group']]
  await call('POST', '/admin/whitelist/import', admin, { csv: ['sid,name,role', ...people.map((row) => row.join(','))].join('\n') })
  const tokens = []
  for (const [sid, name] of people) tokens.push(await register(sid, name))
  const users = await call('GET', '/admin/users', admin)
  const deputy = users.items.find((row) => row.sid === people[1][0])
  await call('PUT', '/admin/deputy', admin, { userId: Number(deputy.id) })
  const scheme = syntheticScheme()
  scheme.categories[2].items = [{ key: 'workshop', name: '课外学术科技活动', note: '参加两项及以上学院认可的学术活动计 40 分，一项计 20 分。', scoreRule: { type: 'free', min: 0, max: 40 }, evidence: { required: false, types: ['png', 'pdf', 'txt'], maxMb: 12 } }]
  const draft = await call('POST', '/admin/scheme', admin, { name: '随页测试 · 提交时规则', config: scheme })
  await call('POST', `/admin/scheme/${draft.id}/publish`, admin)
  await call('PUT', '/admin/window', admin, { open: new Date(Date.now() - 3600000).toISOString(), close: new Date(Date.now() + 30 * 86400000).toISOString(), honorRollTopPercent: 30 })
  for (const key of ['submit', 'edit', 'appeal', 'review', 'arbitrate']) await call('PUT', '/admin/window/capabilities', admin, { key, on: true })
  await call('PUT', '/admin/knowledge/policy', admin, { externalProcessingApproved: true })

  const submissions = []
  for (const [index, person] of [[0, tokens[0]], [1, tokens[0]], [2, admin]]) {
    const sub = await call('POST', '/submissions', person, { category: 'practice', itemKey: 'workshop', title: ['两场学术活动 · 图像核验', '另一条独立事项 · 切换验证', '班管本人事项 · 副班管权限验证'][index], claim: { score: 40 }, note: '开发环境合成资料，仅用于测试 Agent，不是真实学生申报。' })
    if (index === 0) {
      const bytes = readFileSync(process.env.AGENT_CONTEXT_IMAGE)
      const pre = await call('POST', `/submissions/${sub.id}/evidence/presign`, person, { filename: '活动参加记录.png', mediaType: 'image/png', sizeBytes: bytes.length })
      const form = new FormData(); for (const [key, value] of Object.entries(pre.uploadFields)) form.append(key, value)
      form.append('file', new Blob([bytes], { type: 'image/png' }), '活动参加记录.png')
      const uploaded = await fetch(pre.uploadUrl, { method: 'POST', body: form }); assert.ok(uploaded.ok)
      await call('POST', `/submissions/${sub.id}/evidence/${pre.evidenceId}/complete`, person, { etag: uploaded.headers.get('etag') ?? '' })
    }
    await call('POST', `/submissions/${sub.id}/submit`, person)
    const assigned = await waitFor(async () => {
      const result = []
      for (const token of [admin, ...tokens.slice(1)]) {
        const response = await fetch(`${API}/review/tasks/${sub.id}`, { headers: { Authorization: `Bearer ${token}` } })
        if (response.ok) result.push(token)
      }
      return result.length === 2 ? result : null
    })
    await call('POST', `/review/tasks/${sub.id}/decision`, assigned[0], { decision: 'adjusted', score: 20, reason: '只识别到一项有效活动，需核对参加记录', spentSeconds: 20 })
    await call('POST', `/review/tasks/${sub.id}/decision`, assigned[1], { decision: 'accepted', reason: '记录中共有两项活动，按提交时规则认定', spentSeconds: 20 })
    submissions.push(sub.id)
  }
  const second = await call('POST', '/ops/tenants', ops, { name: `隔离测试班 ${stamp}`, slug: `agent-other-${stamp}`, adminSid: `CB${stamp}`, adminName: '外班班管' })
  await register(`CB${stamp}`, '外班班管')
  writeFileSync(fixturePath, JSON.stringify({ classId: tenant.id, otherClassId: second.id, adminSid, studentSid, deputySid: people[1][0], reviewerSid: people[2][0], otherAdminSid: `CB${stamp}`, submissions, providerId, password }, null, 2), { mode: 0o600 })
  console.log('✓ Synthetic arbitration fixtures created, including another tenant and a deputy-only case')
}

async function verify() {
  const fixture = JSON.parse(readFileSync(fixturePath, 'utf8'))
  const A = await login(fixture.adminSid), S = await login(fixture.studentSid), D = await login(fixture.deputySid), R = await login(fixture.reviewerSid), O = await login(fixture.otherAdminSid)
  const [first, second, own] = fixture.submissions
  const initialQuota = (await call('GET', '/agent/status', A)).quota.used
  const state = await call('GET', contextPath('submission', first), A)
  assert.equal(state.context.resourceId, first); assert.equal(state.evidenceCount, 1); assert.ok(state.canDraft)
  assert.equal((await call('GET', '/agent/status', A)).quota.used, initialQuota)
  await call('GET', contextPath('submission', first), S, undefined, 403)
  await call('GET', contextPath('submission', first), O, undefined, 403)
  await call('GET', contextPath('submission', first, 'revDeputy'), D, undefined, 403)
  await call('GET', contextPath('submission', own, 'revDeputy'), R, undefined, 403)
  assert.ok((await call('GET', contextPath('submission', own, 'revDeputy'), D)).canDraft)
  assert.equal((await call('GET', contextPath('submission', own), A)).canDraft, false)
  console.log('✓ Metadata read costs no message quota; tenant, student, deputy scope and self-recusal checks passed')
  const conversation = await call('POST', '/agent/conversations', A, { title: '当前事项与原件 · 开发验证' })
  await call('POST', `/agent/conversations/${conversation.id}/messages`, A, { content: '过期请求', context: { ...state.context, revision: 'stale' } }, 409)
  const sent = await call('POST', `/agent/conversations/${conversation.id}/messages`, A, { content: '请读取这条的原始图片，写出图片中的 Evidence code 和参加的活动数量，结合双方意见和提交时规则给出理由草稿。先说明材料支持什么，不自动定分。', context: { ...state.context, draft: { reason: '', score: '', category: 'practice', itemKey: 'workshop' } } })
  const answer = await waitFor(async () => { const data = await call('GET', `/agent/conversations/${conversation.id}`, A); const row = data.messages.find((m) => m.id === sent.messageId); return row && !['queued', 'running'].includes(row.status) ? row : null }, 330000)
  assert.equal(answer.status, 'complete', answer.error || 'model did not complete')
  assert.match(answer.content, /EV[-－]?742/i, 'Image-only marker must come from the actual image')
  assert.ok(answer.toolTrace.some((step) => step.tool === 'read_evidence' && step.status === 'complete'))
  assert.ok(answer.citations.some((citation) => citation.scope === 'business' && citation.entryId.startsWith('evidence-')))
  assert.equal(answer.context.resourceId, first)
  const action = answer.actions.find((action) => action.kind === 'arbitration_reason_draft')
  assert.ok(action, 'Expected a reason-only draft')
  const prepared = await call('POST', `/agent/actions/${action.id}/prepare`, A)
  assert.deepEqual(prepared.diff.map((row) => row.field), ['终裁理由'])
  // Leave the action un-applied for the real browser confirmation test.
  const switched = await call('GET', contextPath('submission', second), A)
  assert.notEqual(switched.context.resourceId, answer.context.resourceId)
  const current = (await call('GET', `/admin/submissions?id=${first}`, A)).items[0]
  assert.equal(current.status, 'arbitrating'); assert.equal(current.finalScore, null)
  fixture.conversationId = conversation.id; fixture.messageId = sent.messageId; fixture.actionId = action.id
  writeFileSync(fixturePath, JSON.stringify(fixture, null, 2))
  console.log('✓ Real DeepSeek vision read the image-only marker; cited private evidence and produced a reason-only draft; score remains unchanged')
}

// No new model completion is required for these live permission/version checks.
async function boundaries() {
  const fixture = JSON.parse(readFileSync(fixturePath, 'utf8'))
  const A = await login(fixture.adminSid), D = await login(fixture.deputySid), O = await login(fixture.otherAdminSid)
  const first = fixture.submissions[0], own = fixture.submissions[2]
  const current = (await call('GET', `/admin/submissions?id=${first}`, A)).items[0]
  await call('GET', `${contextPath('submission', first)}&observedAt=${encodeURIComponent(current.updatedAt)}`, A)
  await call('GET', `${contextPath('submission', first)}&observedAt=2000-01-01T00%3A00%3A00Z`, A, undefined, 409)
  const state = await call('GET', contextPath('submission', first), A)
  const source = `/agent/sources/business-admSubs-submission-${first}/evidence-${state.evidence[0].id}`
  await call('GET', `${source}?revision=outdated`, A, undefined, 409)
  await call('GET', source, O, undefined, 403)
  const original = await call('GET', `${source}?revision=${state.context.revision}`, A)
  assert.equal(original.mediaType, 'image/png'); assert.ok(original.downloadUrl)
  assert.ok((await fetch(original.downloadUrl)).ok)
  await call('GET', `/agent/actions/${fixture.actionId}`, O, undefined, 404)
  console.log('✓ Visible page timestamps and source revisions are checked; original image is readable only to the authorized actor')

  if (!fixture.appealId) {
    await call('POST', `/review/deputy/submissions/${own}/arbitrate`, D, { score: 20, reason: '合成测试：先认定一项活动，保留申诉验证入口。' })
    const appeal = await call('POST', '/appeals/draft', A, { targetType: 'submission', targetId: own })
    await call('POST', `/appeals/${appeal.id}/submit`, A, { reason: '合成测试：申请核对原始说明及规则，两项活动应重新核查。', score: 40 })
    fixture.appealId = appeal.id
    writeFileSync(fixturePath, JSON.stringify(fixture, null, 2))
  }
  const appeal = await call('GET', contextPath('appeal', fixture.appealId, 'revDeputy'), D)
  assert.ok(appeal.canDraft)
  assert.equal((await call('GET', contextPath('appeal', fixture.appealId), A)).canDraft, false)
  const facts = await call('GET', `/agent/sources/business-revDeputy-appeal-${fixture.appealId}/facts?revision=${appeal.context.revision}`, D)
  assert.match(facts.content, /开发环境合成资料/, 'Appeal context must include the original submission note')
  console.log('✓ Appeal context includes the original submission and reviews; self-recusal also applies to appeals')

  const conversation = await call('POST', '/agent/conversations', D, { title: '权限撤回验证' })
  const sent = await call('POST', `/agent/conversations/${conversation.id}/messages`, D, { content: '合成权限测试消息，无需执行操作。', context: appeal.context })
  await call('POST', `/agent/messages/${sent.messageId}/cancel`, D)
  const users = await call('GET', '/admin/users', A)
  const deputyId = Number(users.items.find((row) => row.sid === fixture.deputySid).id)
  try {
    await call('PUT', '/admin/deputy', A, { userId: null })
    await call('GET', contextPath('appeal', fixture.appealId, 'revDeputy'), D, undefined, 403)
    const history = await call('GET', `/agent/conversations/${conversation.id}`, D)
    assert.equal(history.title, '部分事项已不可读取')
    const summary = (await call('GET', '/agent/conversations', D)).items.find((item) => item.id === conversation.id)
    assert.equal(summary.title, '部分事项已不可读取'); assert.equal(summary.preview, '')
    assert.ok(history.messages.length > 0)
    for (const message of history.messages) {
      assert.equal(message.sourceRevoked, true); assert.equal(message.content, '')
      assert.equal(message.citations.length, 0); assert.equal(message.actions.length, 0)
      assert.equal(message.attachments.length, 0); assert.ok(!message.context?.resourceId)
    }
    console.log('✓ Deputy removal takes effect in the existing session and hides both user/assistant business history, including uncited messages')
  } finally {
    await call('PUT', '/admin/deputy', A, { userId: deputyId })
  }
}

await assertLocalDevelopmentAPI(origin)
await ({ setup, verify, boundaries }[mode] ?? (() => { throw new Error('Use setup, verify or boundaries') }))()
