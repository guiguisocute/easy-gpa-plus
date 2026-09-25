// Synthetic fixtures in the local development stack only. No email is sent.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, assertLocalFrontend, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:38080')
const frontend = requireLoopbackOrigin('FRONTEND_BASE', process.env.FRONTEND_BASE ?? 'http://127.0.0.1:35173')
await assertLocalDevelopmentAPI(origin)
await assertLocalFrontend(frontend)
const stamp = Date.now().toString(36)
const password = 'mail-preferences-synthetic-password'
function sql(statement) {
  return execFileSync('docker', ['exec', '-i', 'easygpa-plus-dev-postgres-1', 'psql', '-U', 'easygpa', '-d', 'easygpa', '-X', '-At', '-v', 'ON_ERROR_STOP=1'], { input: statement, encoding: 'utf8', windowsHide: true }).trim()
}
async function call(method, path, token, body, expected = 200) {
  const response = await fetch(`${origin}/api/v1${path}`, { method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) })
  const result = await response.json().catch(() => null)
  assert.equal(response.status, expected, `${method} ${path}: ${result?.code ?? response.status}`)
  return result
}
const classID = sql(`INSERT INTO class(name,slug,archived) VALUES('邮件系统回归','mail-e2e-${stamp}',false) RETURNING id;`).split('\n')[0]
assert.match(classID, /^\d+$/)
const users = []
for (const [index, role] of ['student', 'group', 'class_admin'].entries()) {
  const sid = `MAIL${index}${stamp}`
  const name = ['邮件测试学生', '邮件测试审核', '邮件测试班管'][index]
  sql(`INSERT INTO whitelist(class_id,sid,name,role,active) VALUES(${classID},'${sid}','${name}','${role}',true);`)
  const checked = await call('POST', '/auth/register/check', null, { sid, name }, 202)
  const session = await call('POST', '/auth/register/complete', null, { ticket: checked.ticket, password }, 201)
  const token = session.access_token
  const initial = await call('GET', '/me/mail-preferences', token)
  assert.equal(initial.notificationsPaused, true)
  assert.equal(initial.deliveryState, 'no_primary_email')
  assert.equal(initial.preferences.categories.progress, 'off')
  assert.equal(initial.preferences.categories.receipts, 'off')
  assert.equal(initial.preferences.dailyLimit, 3)
  assert.equal('opt_out_token' in initial, false)
  const changed = { ...initial.preferences, digestTime: '19:15', dailyLimit: 2, categories: { ...initial.preferences.categories, results: 'off' } }
  await call('PUT', '/me/mail-preferences', token, changed, 204)
  assert.deepEqual((await call('GET', '/me/mail-preferences', token)).preferences, changed)
  const frequent = { ...changed, dailyLimit: 20, categories: { ...changed.categories, receipts: 'frequent', progress: 'frequent' } }
  await call('PUT', '/me/mail-preferences', token, frequent, 204)
  assert.deepEqual((await call('GET', '/me/mail-preferences', token)).preferences, frequent)
  await call('PUT', '/me/mail-preferences', token, { ...frequent, categories: { ...frequent.categories, tasks: 'unlimited' } }, 422)
  await call('PUT', '/me/mail-preferences', token, changed, 204)
  await call('PUT', '/me/mail-preferences', token, { ...changed, dailyLimit: 99 }, 422)
  const capability = sql(`SELECT opt_out_token FROM mail_preference p JOIN app_user u ON u.id=p.user_id WHERE u.sid='${sid}';`)
  // A GET preview cannot unsubscribe. The opaque capability never leaves this
  // local fixture file; test output contains no auth tokens or addresses.
  await call('GET', '/mail/opt-out', null, undefined, 404)
  assert.equal((await call('GET', '/me/mail-preferences', token)).preferences.enabled, true)
  await call('POST', '/mail/opt-out', null, { token: capability, category: 'tasks' })
  assert.equal((await call('GET', '/me/mail-preferences', token)).preferences.categories.tasks, 'off')
  await call('POST', '/mail/opt-out', null, { token: capability, category: 'all' })
  await call('POST', '/mail/opt-out', null, { token: capability, category: 'all' })
  assert.equal((await call('GET', '/me/mail-preferences', token)).preferences.enabled, false)
  await call('POST', '/mail/opt-out', null, { token: 'x'.repeat(43), category: 'all' }, 404)
  await call('PUT', '/me/mail-preferences', token, initial.preferences, 204)
  users.push({ sid, name, role, password, optOutURL: `${frontend}/?mail_opt_out=1#${capability}` })
}
await call('GET', '/me/mail-preferences', null, undefined, 401)
const ops = await call('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT ?? 'ops@localhost', password: process.env.OPS_PASSWORD ?? 'ops-dev-change-me' })
const mail = await call('GET', '/ops/mail', ops.access_token)
assert.equal(mail.config.notificationsEnabled, false)
await call('PUT', '/ops/mail', ops.access_token, { notificationsEnabled: true }, 422)
await call('PUT', '/ops/mail', ops.access_token, { notificationsEnabled: false }, 204)
await call('POST', '/mail/ses-events', null, { event: 'unsubscribe', bulkId: `not-a-provider-id-${stamp}`, email: 'invented@example.invalid' }, 204)
assert.equal(sql(`SELECT count(*) FROM mail_delivery WHERE class_id=${classID};`), '0')
mkdirSync('output/playwright', { recursive: true })
writeFileSync('output/playwright/mail-fixtures.json', JSON.stringify({ classID, users }, null, 2))
console.log('mail_preferences_http=passed roles=3 public_opt_out=idempotent auth_boundary=passed business_paused=true provider_calls=0')
