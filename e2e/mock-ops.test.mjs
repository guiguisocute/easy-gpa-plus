import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import path from 'node:path'
import { before, after, beforeEach, test } from 'node:test'
import { fileURLToPath, pathToFileURL } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const frontend = path.join(root, 'frontend')
const require = createRequire(path.join(frontend, 'package.json'))
const { createServer } = await import(pathToFileURL(require.resolve('vite')).href)
let server, handle, database, token
before(async () => {
  server = await createServer({ root: frontend, configFile: false, appType: 'custom', logLevel: 'error', resolve: { alias: { '@': path.join(frontend, 'src') } }, server: { middlewareMode: true, hmr: false, watch: null, fs: { allow: [root] } } })
  ;({ handle } = await server.ssrLoadModule(path.join(root, 'mock/src/handle.ts')))
  database = await server.ssrLoadModule(path.join(root, 'mock/src/db.ts'))
})
after(async () => { await server?.close() })
const call = (method, url, body) => handle(method, url, body, token)
beforeEach(async () => {
  database.resetStore()
  token = database.tokenFor(database.db().users.find(user => user.role === 'ops'))
})

test('demo mail saves all four providers without ever retaining submitted credentials', async () => {
  assert.equal((await call('GET', '/ops/mail')).activeProvider, 'tencent_ses')
  await call('PUT', '/ops/mail', { provider: 'smtp', smtpHost: 'smtp.example.org', smtpPort: 587, smtpSecurity: 'starttls', smtpUsername: 'secret-user-sentinel', smtpPassword: 'secret-password-sentinel' })
  let state = await call('GET', '/ops/mail')
  assert.equal(state.activeProvider, 'smtp')
  assert.equal(state.configured, true)
  assert.equal(state.feedbackSupported, false)
  assert.equal(state.smtpPasswordSet, true)
  assert.equal(JSON.stringify(database.db()).includes('secret-user-sentinel'), false)
  assert.equal(JSON.stringify(database.db()).includes('secret-password-sentinel'), false)
  await call('PUT', '/ops/mail', { sesFromName: '演示账号邮件' })
  assert.equal((await call('GET', '/ops/mail')).smtpPasswordSet, true, 'omitted credential must be retained')
  await assert.rejects(() => call('PUT', '/ops/mail', { smtpHost: 'another.example.org' }), e => e.code === 'mail_config_invalid')
  await call('PUT', '/ops/mail', { provider: 'aliyun_dm', aliyunRegion: 'cn-hangzhou', aliyunAccessKeyId: 'aliyun-id-sentinel', aliyunAccessKeySecret: 'aliyun-secret-sentinel' })
  state = await call('GET', '/ops/mail')
  assert.equal(state.activeProvider, 'aliyun_dm')
  assert.equal(state.configured, true)
  assert.equal(JSON.stringify(database.db()).includes('aliyun-secret-sentinel'), false)
  await call('PUT', '/ops/mail', { aliyunAccessKeySecret: '' })
  assert.equal((await call('GET', '/ops/mail')).configured, false)
  await assert.rejects(() => call('POST', '/ops/mail/test', { to: 'test@example.org' }), e => e.code === 'mail_config_invalid')
  await call('PUT', '/ops/mail', { provider: 'resend', resendApiKey: 'resend-secret-sentinel' })
  state = await call('GET', '/ops/mail')
  assert.equal(state.activeProvider, 'resend')
  assert.equal(state.configured, true)
  assert.equal(state.feedbackSupported, false)
  assert.equal(state.resendApiKeySet, true)
  assert.equal(JSON.stringify(database.db()).includes('resend-secret-sentinel'), false)
  await call('POST', '/ops/mail/rotate-key')
  state = await call('GET', '/ops/mail')
  assert.equal(state.activeProvider, 'tencent_ses')
  assert.equal(state.smtpPasswordSet, false)
  assert.equal(state.aliyunAccessKeySecretSet, false)
  assert.equal(state.resendApiKeySet, false)
})

test('demo exposes operational limits and non-secret deployment configuration', async () => {
  const flags = (await call('GET', '/ops/flags')).flags
  for (const name of ['authRefreshPerMinute', 'evidenceDailyMb', 'agentMessagesPerMinute', 'knowledgeReprocessPerHour', 'nativeToolsEnabled']) assert.notEqual(flags[name], undefined, name)
  await call('PUT', '/ops/flags/authRefreshPerMinute', { value: 90 })
  assert.equal((await call('GET', '/ops/flags')).flags.authRefreshPerMinute, 90)
  const deployment = await call('GET', '/ops/deploy')
  assert.equal(deployment.configuration.auth.cookieSecure, true)
  assert.equal(deployment.configuration.mcp.maxTtlHours, 24)
  token = database.tokenFor(database.db().users.find(user => user.role === 'student'))
  await assert.rejects(() => call('GET', '/ops/mail'), e => e.status === 403)
})
