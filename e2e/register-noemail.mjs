/* 针对性验证：注册三步 + "不绑邮箱的账号照样完全可用"。

   本次改动把建号从"邮箱验证通过"挪到了"密码设置完成"，邮箱降级为登录后的可选动作。
   这个脚本只跑与之相关的路径，跑完即销毁，不依赖 smoke.mjs 的后续步骤。

   用法：node e2e/run.mjs registration */

import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const API_ORIGIN = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const API = API_ORIGIN + '/api/v1'
const OPS_ACCOUNT = process.env.OPS_ACCOUNT ?? 'ops@e2e.local'
const OPS_PASSWORD = process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password'

let cookies = {}
async function call(method, path, { token, body, raw } = {}) {
  const headers = e2eHeaders({ Accept: 'application/json' })
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`
  const jar = Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ')
  if (jar) headers.Cookie = jar
  const res = await fetch(API + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  for (const sc of res.headers.getSetCookie?.() ?? []) {
    const [pair] = sc.split(';')
    const i = pair.indexOf('=')
    cookies[pair.slice(0, i)] = pair.slice(i + 1)
  }
  const text = await res.text()
  let parsed = null
  try { parsed = text ? JSON.parse(text) : null } catch { parsed = text }
  if (raw) return { status: res.status, body: parsed }
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(parsed)}`)
  return parsed
}

let failed = 0
const ok = (m) => console.log('  \x1b[32m✓\x1b[0m ' + m)
const bad = (m) => { failed++; console.log('  \x1b[31m✗\x1b[0m ' + m) }
const info = (m) => console.log('\x1b[36m▸ ' + m + '\x1b[0m')

const stamp = Date.now().toString(36)

/** 注册三步；不传 email 就停在第二步——这正是要验的"可跳过"。 */
async function register({ sid, name, password }) {
  cookies = {}
  const ticket = await call('POST', '/auth/register/check', {
    body: { sid, name },
  })
  if (!ticket.ticket) bad('第一步没有拿到票据')
  return { ticket, session: await call('POST', '/auth/register/complete', { body: { ticket: ticket.ticket, password } }) }
}

async function main() {
  await assertLocalDevelopmentAPI(API_ORIGIN)
  info('1. 建租户')
  const ops = await call('POST', '/auth/login', { body: { account: OPS_ACCOUNT, password: OPS_PASSWORD } })
  const tenant = await call('POST', '/ops/tenants', {
    token: ops.access_token,
    body: { name: '无邮箱班 ' + stamp, slug: 'noemail-' + stamp, adminSid: 'T' + stamp, adminName: '王管理' },
  })
  ok(`租户 #${tenant.id}`)
  const tenantList = await call('GET', '/ops/tenants', { token: ops.access_token })
  const listedTenant = (tenantList.items ?? []).find((item) => item.id === tenant.id)
  if (listedTenant?.admins?.[0]?.sid !== 'T' + stamp) bad('班级列表没有正确回显已任命班管')
  else ok('班级列表正确回显首位班管')

  info('2. 班级管理员三步注册（全程不碰邮箱）')
  const { ticket, session: admin } = await register({
    sid: 'T' + stamp, name: '王管理', password: 'admin-password-123',
  })
  if (ticket.role !== 'class_admin') bad(`第一步应回显 class_admin，实际 ${ticket.role}`)
  else ok(`第一步回显 name=${ticket.name} role=${ticket.role}，有效期 ${ticket.expires_in}s`)
  if (admin.user?.role !== 'class_admin') bad(`第二步后角色应为 class_admin，实际 ${admin.user?.role}`)
  else ok('第二步建号即登录态，无需任何邮箱')
  const A = admin.access_token

  info('3. 导入白名单')
  const sid = 'S' + stamp
  const csv = ['sid,name,role', `${sid},张三,student`].join('\n')
  ok(`导入 ${(await call('POST', '/admin/whitelist/import', { token: A, body: { csv } })).imported} 条`)

  info('4. 学生三步注册，第三步跳过绑定邮箱')
  const { session: stu } = await register({ sid, name: '张三', password: 'student-password-123' })
  if (stu.user?.role !== 'student') bad(`角色应为 student，实际 ${stu.user?.role}`)
  else ok('学生建号成功（未绑定任何邮箱）')

  info('5. 无邮箱账号的可用性')
  const me = await call('GET', '/me', { token: stu.access_token })
  if (me.sid !== sid) bad(`/me 读不回自己：${JSON.stringify(me)}`)
  else ok(`/me 正常：${me.name} / ${me.role}`)

  const emails = await call('GET', '/me/emails', { token: stu.access_token })
  if ((emails.items ?? []).length !== 0) bad(`新账号不该带邮箱，实际 ${JSON.stringify(emails.items)}`)
  else ok('邮箱列表为空 —— 确认账号不强绑定邮箱')

  cookies = {}
  const relogin = await call('POST', '/auth/login', { body: { account: sid, password: 'student-password-123' } })
  if (relogin.user?.sid !== sid) bad('用学号 + 密码重新登录失败')
  else ok('用学号 + 密码可正常登录（登录本就不依赖邮箱）')

  info('6. 无邮箱账号走找回密码：应静默成功，不报错')
  const forgot = await call('POST', '/auth/forgot-password', { body: { account: sid }, raw: true })
  if (forgot.status >= 400) bad(`找回密码在无邮箱账号上报错了：${forgot.status} ${JSON.stringify(forgot.body)}`)
  else ok(`返回 ${forgot.status}（查不到主邮箱就静默 no-op，不泄露账号是否存在）`)

  /* POST /me/emails 会由 API 直接调用邮件供应商，不经过 notify Worker。重复 E2E
     只验证第三步可跳过；真实邮箱投递属于显式的运维 mail_test 自检。 */
  info('7. 旧接口应已下线')
  for (const p of ['/auth/register', '/auth/verify-email', '/auth/resend-code']) {
    const r = await call('POST', p, { body: {}, raw: true })
    if (r.status !== 404) bad(`${p} 仍然存在（${r.status}）`)
    else ok(`${p} → 404`)
  }

  info('8. 运维任命即时生效，停用班级即时阻断')
  const promoted = await call('PUT', `/ops/tenants/${tenant.id}/admin`, {
    token: ops.access_token,
    body: { sid, name: '张三' },
  })
  if (!promoted.registered) bad('已注册学生被任命后应回显 registered=true')
  else ok('运维可直接把已注册成员任命为班管')

  const stale = await call('GET', '/me', { token: stu.access_token, raw: true })
  if (stale.status !== 401) bad(`任命后旧学生凭证应失效，实际 ${stale.status}`)
  else ok('角色变化后旧登录凭证立即失效')

  cookies = {}
  const promotedLogin = await call('POST', '/auth/login', { body: { account: sid, password: 'student-password-123' } })
  if (promotedLogin.user?.role !== 'class_admin') bad(`重新登录应为 class_admin，实际 ${promotedLogin.user?.role}`)
  else ok('重新登录后班管角色正确')

  await call('PUT', `/ops/tenants/${tenant.id}`, { token: ops.access_token, body: { archived: true } })
  const archivedSession = await call('GET', '/me', { token: promotedLogin.access_token, raw: true })
  const archivedLogin = await call('POST', '/auth/login', { body: { account: sid, password: 'student-password-123' }, raw: true })
  if (archivedSession.status !== 401 || archivedLogin.status !== 401) {
    bad(`班级停用后旧会话/新登录都应为 401，实际 ${archivedSession.status}/${archivedLogin.status}`)
  } else {
    ok('班级停用后旧会话与新登录均被阻断')
  }
  await call('PUT', `/ops/tenants/${tenant.id}`, { token: ops.access_token, body: { archived: false } })

  console.log(failed ? `\n\x1b[31m${failed} 项未通过\x1b[0m` : '\n\x1b[32m全部通过\x1b[0m')
  process.exit(failed ? 1 : 0)
}

main().catch((e) => { console.error('\n\x1b[31m中断：\x1b[0m' + e.message); process.exit(1) })
