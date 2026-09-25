import { expect, request, test, type APIRequestContext, type Page } from '@playwright/test'
import { e2eHeaders, requireLoopbackOrigin } from '../../e2e/support/local-environment.mjs'

const API_ORIGIN = requireLoopbackOrigin(
  'PLAYWRIGHT_API_BASE',
  process.env.PLAYWRIGHT_API_BASE ?? 'http://127.0.0.1:48080',
)
const stamp = Date.now().toString(36)

interface Seed {
  api: APIRequestContext
  opsToken: string
  adminToken: string
  tenantId: string
  studentSid: string
  pendingSid: string
  studentPassword: string
}

let seed: Seed

test.describe.serial('当前名单注册语义', () => {
  test.beforeAll(async () => {
    const api = await request.newContext({
      baseURL: `${API_ORIGIN}/api/v1/`,
      extraHTTPHeaders: e2eHeaders({ Accept: 'application/json' }),
    })
    const ops = await jsonCall(api, 'POST', 'auth/login', {
      account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local',
      password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password',
    })
    const opsToken = stringField(ops, 'access_token')
    const adminSid = `RA${stamp}`
    const tenant = await jsonCall(api, 'POST', 'ops/tenants', {
      name: `名单 E2E 班 ${stamp}`,
      slug: `roster-e2e-${stamp}`,
      adminSid,
      adminName: '名单班管',
    }, opsToken)
    const admin = await register(api, adminSid, '名单班管', 'roster-admin-password')
    const studentSid = `RS${stamp}`
    const pendingSid = `RP${stamp}`
    await jsonCall(api, 'POST', 'admin/whitelist/import', {
      csv: ['sid,name,role', `${studentSid},名单学生,student`, `${pendingSid},待注册学生,student`].join('\n'),
    }, stringField(admin, 'access_token'))
    seed = {
      api,
      opsToken,
      adminToken: stringField(admin, 'access_token'),
      tenantId: stringField(tenant, 'id'),
      studentSid,
      pendingSid,
      studentPassword: 'roster-student-password',
    }
  })

  test.afterAll(async () => {
    await seed?.api.dispose()
  })

  test('浏览器注册必须精确命中学号与真实姓名，并可跳过邮箱', async ({ page }) => {
    await openRegistration(page)
    await page.getByLabel('学号', { exact: true }).fill(seed.studentSid)
    await page.getByLabel('姓名', { exact: true }).fill('名单同学')
    await page.getByRole('button', { name: '下一步' }).click()
    await expect(page.getByRole('alert')).toContainText('学号与姓名未命中可用白名单')

    await page.getByLabel('姓名', { exact: true }).fill('  名单学生  ')
    await page.getByRole('button', { name: '下一步' }).click()
    await expect(page.getByText('设置密码', { exact: true })).toBeVisible()
    await page.getByLabel('密码（至少 10 位）').fill(seed.studentPassword)
    await page.getByRole('button', { name: '创建账号' }).click()
    await expect(page.getByText('绑定邮箱（推荐）', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: '暂不绑定 · 直接进入' }).click()
    await expect(page.locator('[data-r="shell"]')).toBeVisible()
    await expect(page.getByText('名单学生', { exact: true }).first()).toBeVisible()
  })

  test('班管能从实时状态接口看到全班成员是否封存及终审进度', async () => {
    const response = await jsonCall(seed.api, 'GET', 'admin/seals', undefined, seed.adminToken)
    if (!response || typeof response !== 'object' || Array.isArray(response)) throw new Error('admin/seals response is not an object')
    const items = (response as Record<string, unknown>).items
    if (!Array.isArray(items)) throw new Error('admin/seals items is not an array')
    const rows = items as Array<Record<string, unknown>>
    expect(rows.some((row) => row.sid === seed.studentSid && row.sealState !== 'sealed')).toBe(true)
    expect(rows.some((row) => row.sid === seed.pendingSid && row.auditSubjectId === null)).toBe(true)
    expect(rows.every((row) => typeof row.auditSubmitted === 'number' && typeof row.auditAssignments === 'number')).toBe(true)
  })

  test('归档班级同时阻断既有账号登录与名单成员注册', async () => {
    await jsonCall(seed.api, 'PUT', `ops/tenants/${seed.tenantId}`, { archived: true }, seed.opsToken)
    try {
      const login = await seed.api.post('auth/login', {
        data: { account: seed.studentSid, password: seed.studentPassword },
      })
      expect(login.status()).toBe(401)

      const check = await seed.api.post('auth/register/check', {
        data: { sid: seed.pendingSid, name: '待注册学生' },
      })
      expect(check.status()).toBe(403)
      expect((await check.json()).code).toBe('registration_denied')
    } finally {
      await jsonCall(seed.api, 'PUT', `ops/tenants/${seed.tenantId}`, { archived: false }, seed.opsToken)
    }
  })
})

async function openRegistration(page: Page) {
  await page.goto('/')
  const enter = page.getByRole('button', { name: '进入登录' })
  const registration = page.getByRole('button', { name: '首次使用 · 注册' })
  await expect(enter.or(registration)).toBeVisible()
  if (await enter.isVisible()) await enter.click()
  await registration.click()
  await expect(page.getByText('核对身份', { exact: true })).toBeVisible()
}

async function register(api: APIRequestContext, sid: string, name: string, password: string) {
  const checked = await jsonCall(api, 'POST', 'auth/register/check', { sid, name })
  return jsonCall(api, 'POST', 'auth/register/complete', { ticket: stringField(checked, 'ticket'), password })
}

async function jsonCall(api: APIRequestContext, method: string, path: string, data?: unknown, token?: string) {
  const response = await api.fetch(path, {
    method,
    data,
    headers: e2eHeaders(token ? { Authorization: `Bearer ${token}` } : {}),
  })
  const text = await response.text()
  if (!response.ok()) throw new Error(`${method} ${path} → ${response.status()} ${text}`)
  return text ? JSON.parse(text) as unknown : null
}

function stringField(value: unknown, key: string) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('API response is not an object')
  const field = (value as Record<string, unknown>)[key]
  if (typeof field !== 'string') throw new Error(`API field ${key} is not a string`)
  return field
}
