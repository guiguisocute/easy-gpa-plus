import { expect, test, type Page } from '@playwright/test'

// Use the real application entry point, with local API fixtures only. The demo
// has a separate QueryClient and would conceal regressions in main.tsx.
test('会话隔离：切换账号清除旧缓存并丢弃迟到的附件下载响应', async ({ page }) => {
  const secondResources = Promise.withResolvers<void>()
  const oldDownload = Promise.withResolvers<void>()
  let secondResourceRequested = false
  let downloadStarted = false
  let staleDownloadRequests = 0
  const pageErrors: string[] = []
  page.on('pageerror', (error) => pageErrors.push(error.message))
  await page.route('**/stale-private-download', async (route) => {
    staleDownloadRequests++
    await route.fulfill({ body: 'This file belongs to the previous account.' })
  })
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.slice('/api/v1'.length)
    const second = request.headers().authorization === 'Bearer session-two'
    if (path === '/auth/refresh') {
      await route.fulfill({ status: 401, json: { code: 'unauthenticated' } })
    } else if (path === '/auth/login') {
      const account = request.postDataJSON().account as string
      const number = account === 'student-two' ? 2 : 1
      await route.fulfill({ json: {
        access_token: number === 1 ? 'session-one' : 'session-two',
        user: { sid: account, name: `示例学生${number}`, role: 'student', initial: '示', sub: '学生', classId: number, className: `示例班${number}` },
      } })
    } else if (path === '/auth/logout') {
      await route.fulfill({ status: 204 })
    } else if (path === '/governance') {
      await route.fulfill({ json: { config: { mode: 'centralized', version: 1 }, canConfigure: false } })
    } else if (path === '/class-resources') {
      if (second) {
        secondResourceRequested = true
        await secondResources.promise
      }
      await route.fulfill({ json: { items: [{
        id: second ? 'file-two' : 'file-one',
        displayName: second ? '第二班评定细则' : '第一班私有文件',
        logicalPath: 'rules.txt', sizeBytes: 10, updatedAt: '2026-01-01T00:00:00Z',
      }] } })
    } else if (path === '/class-resources/file-one/url') {
      downloadStarted = true
      await oldDownload.promise
      await route.fulfill({ json: { url: '/stale-private-download', filename: 'rules.txt' } })
    } else if (path === '/window') {
      await route.fulfill({ json: null })
    } else {
      await route.fulfill({ status: 404, json: { code: 'not_found' } })
    }
  })

  try {
    await page.goto('/?v=stuResources')
    await signIn(page, 'student-one')
    await expect(page.getByText('第一班私有文件', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: '下载', exact: true }).click()
    await expect.poll(() => downloadStarted).toBe(true)
    await page.locator('[data-shell-account-toggle]:visible').click()
    await page.getByRole('button', { name: '退出登录', exact: true }).click()
    await signIn(page, 'student-two')

    // The old value must disappear even while the next user's network is slow.
    await expect.poll(() => secondResourceRequested).toBe(true)
    await expect(page.getByText('第一班私有文件', { exact: true })).toHaveCount(0)
    oldDownload.resolve()
    secondResources.resolve()
    await expect(page.getByText('第二班评定细则', { exact: true })).toBeVisible()
    await expect(page.getByText('第一班私有文件', { exact: true })).toHaveCount(0)
    expect(staleDownloadRequests).toBe(0)
    expect(pageErrors).toEqual([])
  } finally {
    secondResources.resolve()
    oldDownload.resolve()
  }
})

async function signIn(page: Page, account: string) {
  const enter = page.getByRole('button', { name: '进入登录', exact: true })
  const username = page.getByLabel('学号 或 邮箱', { exact: true })
  await expect(enter.or(username)).toBeVisible()
  if (await enter.isVisible()) await enter.click()
  await username.fill(account)
  await page.getByLabel('密码', { exact: true }).fill('example-password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.locator('[data-r="shell"]')).toBeVisible()
}
