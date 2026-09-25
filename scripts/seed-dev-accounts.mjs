#!/usr/bin/env node
/* 为本地开发创建三个固定业务账号：班管 / 综测小组 / 学生。
   依赖：postgres/redis 已起，API 在 8080。开发账号使用学号登录，不自动绑定邮箱。
   用法：node scripts/seed-dev-accounts.mjs */

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

function dotenv() {
  try {
    const raw = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', '.env'), 'utf8')
    return Object.fromEntries(
      raw
        .split('\n')
        .map((l) => l.trim())
        .filter((l) => l && !l.startsWith('#'))
        .map((l) => {
          const i = l.indexOf('=')
          return [l.slice(0, i).trim(), l.slice(i + 1).split('#')[0].trim()]
        }),
    )
  } catch {
    return {}
  }
}

const env = dotenv()
const port = (env.HTTP_ADDR ?? ':8080').split(':').pop() || '8080'
const API = (process.env.API_BASE ?? `http://127.0.0.1:${port}`).replace(/\/$/, '') + '/api/v1'
const OPS_ACCOUNT = process.env.OPS_ACCOUNT ?? env.OPS_ACCOUNT ?? 'ops@localhost'
const OPS_PASSWORD = process.env.OPS_PASSWORD ?? env.OPS_PASSWORD ?? 'ops-dev-change-me'
const PASSWORD = process.env.DEV_PASSWORD ?? 'devpass123'

const ACCOUNTS = [
  { role: 'class_admin', sid: 'DEV001', name: '王管理' },
  { role: 'group', sid: 'DEV002', name: '李审核' },
  { role: 'student', sid: 'DEV003', name: '张三' },
]

let cookies = {}

async function call(method, path, { token, body, raw } = {}) {
  const headers = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`
  const jar = Object.entries(cookies)
    .map(([k, v]) => `${k}=${v}`)
    .join('; ')
  if (jar) headers.Cookie = jar
  const res = await fetch(API + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  for (const sc of res.headers.getSetCookie?.() ?? []) {
    const [pair] = sc.split(';')
    const i = pair.indexOf('=')
    cookies[pair.slice(0, i)] = pair.slice(i + 1)
  }
  const text = await res.text()
  let parsed = null
  try {
    parsed = text ? JSON.parse(text) : null
  } catch {
    parsed = text
  }
  if (raw) return { status: res.status, body: parsed }
  if (res.status >= 400) {
    throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(parsed)}`)
  }
  return parsed
}

async function tryLogin(account) {
  cookies = {}
  return call('POST', '/auth/login', { body: { account, password: PASSWORD }, raw: true })
}

async function register({ sid, name, password }) {
  cookies = {}
  const ticket = await call('POST', '/auth/register/check', {
    body: { sid, name },
  })
  const session = await call('POST', '/auth/register/complete', {
    body: { ticket: ticket.ticket, password },
  })
  return session
}

function listTenants(payload) {
  if (Array.isArray(payload)) return payload
  if (Array.isArray(payload?.items)) return payload.items
  return []
}

async function ensureTenant(opsToken) {
  const tenants = listTenants(await call('GET', '/ops/tenants', { token: opsToken }))
  const found = tenants.find((t) => t.slug === 'dev-test' || t.name === '开发测试班')
  if (found) {
    await call('PUT', `/ops/tenants/${found.id}`, { token: opsToken, body: { archived: false } })
    await call('PUT', `/ops/tenants/${found.id}/admin`, { token: opsToken, body: { sid: 'DEV001', name: '王管理' } })
    return { id: found.id, reused: true }
  }
  const tenant = await call('POST', '/ops/tenants', {
    token: opsToken,
    body: { name: '开发测试班', slug: 'dev-test', adminSid: 'DEV001', adminName: '王管理' },
  })
  return { id: tenant.id, reused: false }
}

async function main() {
  const results = []

  // 已存在则直接复用
  for (const a of ACCOUNTS) {
    const bySid = await tryLogin(a.sid)
    if (bySid.status < 400) {
      results.push({
        ...a,
        role: bySid.body.user.role,
        password: PASSWORD,
        status: 'exists',
      })
    }
  }
  if (results.length === ACCOUNTS.length) {
    printTable(results)
    return
  }

  cookies = {}
  const ops = await call('POST', '/auth/login', {
    body: { account: OPS_ACCOUNT, password: OPS_PASSWORD },
  })
  const tenant = await ensureTenant(ops.access_token)
  console.log(`租户 #${tenant.id}${tenant.reused ? '（已有）' : '（新建）'}`)

  // 班管
  let adminSession
  const adminExisting = await tryLogin('DEV001')
  if (adminExisting.status < 400) {
    const login = adminExisting
    adminSession = login.body
  } else {
    console.log('注册班管 DEV001 …')
    adminSession = await register({
      sid: 'DEV001',
      name: '王管理',
      password: PASSWORD,
    })
    results.push({
      role: adminSession.user.role,
      sid: adminSession.user.sid,
      name: adminSession.user.name,
      password: PASSWORD,
      status: 'created',
    })
  }

  const A = adminSession.access_token
  const csv = ['sid,name,role', 'DEV002,李审核,group', 'DEV003,张三,student'].join('\n')
  const wl = await call('POST', '/admin/whitelist/import', {
    token: A,
    body: { csv },
    raw: true,
  })
  if (wl.status >= 400 && wl.status !== 409) {
    // import 对已存在记录通常仍返回 200 + imported 计数；真失败再报
    console.warn('白名单导入响应', wl.status, JSON.stringify(wl.body))
  } else {
    console.log(`白名单导入：${JSON.stringify(wl.body)}`)
  }

  for (const a of ACCOUNTS.filter((x) => x.role !== 'class_admin')) {
    if (results.some((r) => r.sid === a.sid)) continue
    console.log(`注册 ${a.role} ${a.sid} …`)
    const sess = await register({
      sid: a.sid,
      name: a.name,
      password: PASSWORD,
    })
    results.push({
      role: sess.user.role,
      sid: sess.user.sid,
      name: sess.user.name,
      password: PASSWORD,
      status: 'created',
    })
  }

  // 最终按 ACCOUNTS 顺序排，并校验可登录
  const ordered = []
  for (const a of ACCOUNTS) {
    const hit = results.find((r) => r.sid === a.sid) ?? {
      ...a,
      password: PASSWORD,
      status: 'missing',
    }
    const login = await tryLogin(a.sid)
    if (login.status >= 400) {
      throw new Error(`登录校验失败 ${a.sid}: ${JSON.stringify(login.body)}`)
    }
    ordered.push({
      role: login.body.user.role,
      sid: login.body.user.sid,
      name: login.body.user.name,
      account: a.sid,
      password: PASSWORD,
      status: hit.status ?? 'ok',
    })
  }

  printTable(ordered)
}

function printTable(rows) {
  console.log('')
  console.log('开发测试账号（租户：开发测试班 / slug=dev-test）')
  console.log('─'.repeat(72))
  console.log(
    pad('角色', 14) + pad('学号', 10) + pad('姓名', 10) + pad('登录账号', 22) + pad('密码', 14) + '状态',
  )
  console.log('─'.repeat(72))
  const roleLabel = {
    class_admin: '班级管理员',
    group: '综测小组',
    student: '学生',
    ops: '运维超管',
  }
  for (const r of rows) {
    console.log(
      pad(roleLabel[r.role] ?? r.role, 14) +
        pad(r.sid, 10) +
        pad(r.name, 10) +
        pad(r.account ?? r.sid, 22) +
        pad(r.password, 14) +
        (r.status ?? 'ok'),
    )
  }
  console.log('─'.repeat(72))
  console.log('另：运维超管  ops@localhost  /  ops-dev-change-me  （环境变量签发）')
  console.log('以上开发账号使用学号登录；邮箱绑定请在配置好腾讯云 SES API 后单独验证。')
}

function pad(s, n) {
  const str = String(s ?? '')
  // 中文按约 2 宽估算，保证大致对齐
  let w = 0
  for (const ch of str) w += /[\u4e00-\u9fff]/.test(ch) ? 2 : 1
  return str + ' '.repeat(Math.max(1, n - w))
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
