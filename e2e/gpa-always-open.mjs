#!/usr/bin/env node
// Real HTTP and migration regression, restricted to synthetic local E2E data.
// Run with: node e2e/run.mjs gpa
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const stamp = Date.now().toString(36)
const password = 'gpa-synthetic-password'
function sql(statement) {
  return execFileSync('docker', ['exec', '-i', 'easygpa-plus-e2e-postgres-1', 'psql', '-U', 'easygpa', '-d', 'easygpa_e2e', '-X', '-Atq', '-v', 'ON_ERROR_STOP=1'], {
    input: statement, encoding: 'utf8', windowsHide: true,
  }).trim()
}
async function call(method, path, token, body, status = 200) {
  const multipart = body instanceof FormData
  const response = await fetch(`${origin}/api/v1${path}`, {
    method, headers: e2eHeaders({ ...(!multipart ? { 'Content-Type': 'application/json' } : {}), ...(token ? { Authorization: `Bearer ${token}` } : {}) }),
    body: body === undefined ? undefined : multipart ? body : JSON.stringify(body),
  })
  const result = await response.json()
  assert.equal(response.status, status, `${method} ${path}: ${result?.code ?? ''}`)
  return result
}
async function register(sid, name) {
  const ticket = await call('POST', '/auth/register/check', null, { sid, name }, 202)
  return call('POST', '/auth/register/complete', null, { ticket: ticket.ticket, password }, 201)
}

await assertLocalDevelopmentAPI(origin)
const up = readFileSync(new URL('../backend/db/migrations/000053_gpa_always_open.up.sql', import.meta.url), 'utf8')
const down = readFileSync(new URL('../backend/db/migrations/000053_gpa_always_open.down.sql', import.meta.url), 'utf8')
assert.equal(sql(`BEGIN;
  CREATE TEMP TABLE class_timeline (class_id bigint, capabilities jsonb, close_at timestamptz, updated_at timestamptz);
  INSERT INTO class_timeline VALUES
    (1,'{"submit":false,"gpa":{"on":false,"close":"2020-01-01T00:00:00Z"}}','2026-01-01',now()),
    (2,'{"submit":true,"gpa":{"on":true,"close":"2020-01-01T00:00:00Z"}}','2026-01-01',now()),
    (3,'{"submit":true}','2026-01-01',now());
  ${up}
  SELECT count(*)=3 AND bool_and(NOT capabilities ? 'gpa') AND bool_and(capabilities ? 'submit') FROM class_timeline;
  ${down}
  SELECT bool_and((capabilities->'gpa'->>'on')::boolean) FROM class_timeline;
  ROLLBACK;`), 't\nt')
console.log('✓ 迁移清除各班旧 GPA 配置、保留其他开关；回退恢复开放的兼容配置')

const ops = await call('POST', '/auth/login', null, { account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password' })
const adminSid = `GA${stamp}`
const tenant = await call('POST', '/ops/tenants', ops.access_token, { name: `GPA回归 ${stamp}`, slug: `gpa-${stamp}`, adminSid, adminName: '导入班管' }, 201)
const admin = await register(adminSid, '导入班管')
const token = admin.access_token
const paste = () => call('POST', '/admin/gpa/paste', token, { rows: [{ sid: adminSid, score: 88 }] })
assert.equal((await paste()).imported, 1)
assert.equal('gpa' in (await call('GET', '/window', token)).capabilities, false)
const legacy = await call('PUT', '/admin/window/capabilities', token, { key: 'gpa', on: false, close: '2020-01-01T00:00:00Z' }, 400)
assert.equal(legacy.code, 'invalid_capability')
console.log('✓ 新班级无需开启 GPA 即可导入，旧客户端不能重新设置开关或截止')

const classID = Number(tenant.id)
assert.ok(Number.isSafeInteger(classID) && classID > 0)
for (const [open, close] of [['2090-01-01T00:00:00Z', '2091-01-01T00:00:00Z'], ['2020-01-01T00:00:00Z', '2021-01-01T00:00:00Z']]) {
  // Seed a legacy closed switch after setting each window to prove stale data is ignored.
  await call('PUT', '/admin/timeline', token, { open, close })
  sql(`UPDATE class_timeline SET capabilities=capabilities || '{"gpa":{"on":false,"close":"2020-01-01T00:00:00Z"}}'::jsonb WHERE class_id=${classID};`)
  assert.equal((await paste()).imported, 1)
  const form = new FormData()
  form.append('file', new Blob([`sid,score\n${adminSid},89`], { type: 'text/csv' }), 'synthetic-gpa.csv')
  assert.equal((await call('POST', '/admin/gpa/import', token, form)).imported, 1)
}
console.log('✓ 旧班关闭开关、过期 GPA 截止、材料窗口未开或已封存时，粘贴及文件导入均成功')

await call('POST', '/admin/gpa/paste', null, { rows: [{ sid: adminSid, score: 80 }] }, 401)
const studentSid = `GS${stamp}`
await call('POST', '/admin/whitelist', token, { sid: studentSid, name: '导入学生' }, 201)
const student = await register(studentSid, '导入学生')
await call('POST', '/admin/gpa/paste', student.access_token, { rows: [{ sid: studentSid, score: 80 }] }, 403)
await call('POST', '/admin/gpa/paste', token, { rows: [{ sid: `OUT${stamp}`, score: 80 }] }, 422)
await call('PUT', '/admin/timeline', token, { open: '2020-01-01T00:00:00Z', close: '2021-01-01T00:00:00Z', lockdown: '2022-01-01T00:00:00Z' })
const locked = await call('POST', '/admin/gpa/paste', token, { rows: [{ sid: adminSid, score: 80 }] }, 409)
assert.equal(locked.code, 'class_locked')
await call('GET', '/admin/gpa', token)
await call('PUT', '/admin/timeline', token, { open: '2020-01-01T00:00:00Z', close: '2021-01-01T00:00:00Z', lockdown: null })
assert.equal((await paste()).imported, 1)
console.log('✓ 登录、班管权限、班级名单和全系统封锁仍受校验，解除全系统封锁后可立即导入')

mkdirSync('.ai-eval', { recursive: true })
writeFileSync('.ai-eval/gpa-fixture.json', JSON.stringify({ adminSid, password, classID }))
