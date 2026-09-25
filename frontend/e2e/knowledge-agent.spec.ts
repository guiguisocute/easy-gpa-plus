import { expect, request, test, type APIRequestContext, type Page } from '@playwright/test'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  e2eHeaders,
  requireLoopbackOrigin,
  requireSyntheticProviderURL,
} from '../../e2e/support/local-environment.mjs'

const API_ORIGIN = requireLoopbackOrigin(
  'PLAYWRIGHT_API_BASE',
  process.env.PLAYWRIGHT_API_BASE ?? 'http://127.0.0.1:48080',
)
const API = API_ORIGIN + '/api/v1/'
const OPS_ACCOUNT = process.env.OPS_ACCOUNT ?? 'ops@e2e.local'
const OPS_PASSWORD = process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password'
const FAKE_OPENAI_BASE_URL = requireSyntheticProviderURL(
  'PLAYWRIGHT_FAKE_OPENAI_BASE_URL',
  process.env.PLAYWRIGHT_FAKE_OPENAI_BASE_URL ?? 'http://127.0.0.1:18082/v1',
)
const FAKE_OPENAI_HEALTH_URL = process.env.PLAYWRIGHT_FAKE_OPENAI_HEALTH_URL ?? 'http://127.0.0.1:48082/health'
const fakeHealth = new URL(FAKE_OPENAI_HEALTH_URL)
requireLoopbackOrigin('PLAYWRIGHT_FAKE_OPENAI_HEALTH_URL', fakeHealth.origin)
if (fakeHealth.pathname !== '/health' || fakeHealth.search || fakeHealth.hash) {
  throw new Error('PLAYWRIGHT_FAKE_OPENAI_HEALTH_URL 必须是 loopback /health 地址')
}
const stamp = Date.now().toString(36)

interface SeedState {
  api: APIRequestContext
  opsToken: string
  adminSid: string
  adminPassword: string
  adminToken: string
  studentSid: string
  studentPassword: string
  studentToken: string
  reviewerSid: string
  reviewerPassword: string
  previous: Record<string, unknown>
  classDocumentId: string
  supplementalDocumentId: string
  classEntryId: string
  supplementalEntryId: string
}

let seed: SeedState

test.describe.serial('班级知识库与工具型 Agent', () => {
  test.beforeAll(async () => {
    const api = await request.newContext({ baseURL: API, extraHTTPHeaders: e2eHeaders({ Accept: 'application/json' }) })
    await waitFor(async () => fetch(fakeHealth).then((response) => response.ok).catch(() => false))

    const ops = await apiCall(api, 'POST', '/auth/login', undefined, { account: OPS_ACCOUNT, password: OPS_PASSWORD })
    const opsToken = stringField(ops, 'access_token')
    const previous = await apiCall(api, 'GET', '/ops/agent', opsToken) as Record<string, unknown>
    if (previous.storedApiKeySet === true) {
      await api.dispose()
      throw new Error('Playwright 拒绝覆盖已保存的模型 Key；请使用没有数据库 Key 的隔离测试栈。')
    }
    await apiCall(api, 'PUT', '/ops/agent', opsToken, {
      baseUrl: FAKE_OPENAI_BASE_URL, apiKey: 'fake-playwright-key',
      textModel: 'fake-text', visionModel: 'fake-vision', agentModel: 'fake-agent',
      agentMaxSteps: 8, agentTimeoutSeconds: 60, agentMaxAnswerKb: 128, agentToolResultKb: 32, agentToolScanMb: 64,
      agentDailyMessages: 50, knowledgeMaxFilesPerClass: 1000, knowledgeMaxStorageMbPerClass: 1024,
    })
    for (const key of ['aiEnabled', 'knowledgeEnabled', 'agentActionsEnabled', 'knowledgeEgressEnabled']) {
      await apiCall(api, 'PUT', `/ops/flags/${key}`, opsToken, { value: true })
    }

    const adminSid = `PWA${stamp}`
    const adminPassword = 'playwright-admin-123'
    await apiCall(api, 'POST', '/ops/tenants', opsToken, {
      name: `Playwright 知识班 ${stamp}`, slug: `pw-knowledge-${stamp}`, adminSid, adminName: '浏览器班管',
    })
    const admin = await register(api, adminSid, '浏览器班管', adminPassword)
    const adminToken = stringField(admin, 'access_token')
    const studentSid = `PWS${stamp}`
    const reviewerSid = `PWG${stamp}`
    const reviewer2Sid = `PWH${stamp}`
    const studentPassword = 'playwright-student-123'
    const reviewerPassword = 'playwright-reviewer-123'
    await apiCall(api, 'POST', '/admin/whitelist/import', adminToken, {
      csv: ['sid,name,role', `${studentSid},浏览器学生,student`, `${reviewerSid},浏览器审核,group`, `${reviewer2Sid},备用审核,group`].join('\n'),
    })
    const student = await register(api, studentSid, '浏览器学生', studentPassword)
    await register(api, reviewerSid, '浏览器审核', reviewerPassword)
    await register(api, reviewer2Sid, '备用审核', 'playwright-reviewer-456')
    const studentToken = stringField(student, 'access_token')
    const draft = await apiCall(api, 'POST', '/admin/scheme', adminToken, { name: 'Playwright 方案', config: schemeConfig() })
    await apiCall(api, 'POST', `/admin/scheme/${stringField(draft, 'id')}/publish`, adminToken)
    const day = 86400_000
    await apiCall(api, 'PUT', '/admin/window', adminToken, {
      open: new Date(Date.now() - 3600_000).toISOString(),
      close: new Date(Date.now() + 30 * day).toISOString(),
      honorRollTopPercent: 30,
    })
    for (const key of ['submit', 'edit', 'appeal', 'review', 'arbitrate']) {
      await apiCall(api, 'PUT', '/admin/window/capabilities', adminToken, { key, on: true })
    }
    await apiCall(api, 'PUT', '/admin/knowledge/policy', adminToken, { externalProcessingApproved: true })

    const classDocumentId = await uploadText(api, adminToken, '学院规则.txt', '制度/学院规则.txt', '总分权重为专业 60%、思想 15%、实践 15%、身心 10%。')
    const supplementalDocumentId = await uploadText(api, adminToken, '班级登记表.txt', '表格/班级登记表.txt', '班级公开的补充登记表。')
    const knowledge = await waitFor(async () => {
      const current = await apiCall(api, 'GET', '/admin/knowledge', adminToken)
      const documents = arrayField(current, 'documents')
      return documents.length >= 2 && documents.every((item) => !['uploading', 'queued', 'processing'].includes(String(record(item).status))) ? current : false
    })
    const classDetail = await apiCall(api, 'GET', `/admin/knowledge/documents/${classDocumentId}`, adminToken)
    const supplementalDetail = await apiCall(api, 'GET', `/admin/knowledge/documents/${supplementalDocumentId}`, adminToken)
    const classEntryId = stringField(record(arrayField(classDetail, 'entriesPreview')[0]), 'id')
    const supplementalEntryId = stringField(record(arrayField(supplementalDetail, 'entriesPreview')[0]), 'id')
    expect(arrayField(knowledge, 'documents')).toHaveLength(2)

    seed = {
      api, opsToken, adminSid, adminPassword, adminToken,
      studentSid, studentPassword, studentToken,
      reviewerSid, reviewerPassword, previous,
      classDocumentId, supplementalDocumentId, classEntryId, supplementalEntryId,
    }
  })

  test.afterAll(async () => {
    if (!seed) return
    try {
      const previous = seed.previous
      const config = record(previous.config)
      const sources = record(previous.sources)
      const limits = record(previous.limits)
      await apiCall(seed.api, 'DELETE', '/ops/agent', seed.opsToken)
      const restored: Record<string, unknown> = { ...limits }
      for (const key of ['baseUrl', 'textModel', 'visionModel', 'agentModel']) {
        if (sources[key] === 'database') restored[key] = config[key]
      }
      await apiCall(seed.api, 'PUT', '/ops/agent', seed.opsToken, restored)
      for (const key of ['aiEnabled', 'knowledgeEnabled', 'agentActionsEnabled', 'knowledgeEgressEnabled']) {
        const value = key === 'aiEnabled' ? previous.enabled : previous[key]
        try {
          await apiCall(seed.api, 'PUT', `/ops/flags/${key}`, seed.opsToken, { value: value === true })
        } catch {
          // A dev stack with no environment model key cannot re-enable AI after
          // the fake database config is removed; leaving it off is the safe
          // cleanup state and must not hide the test result.
        }
      }
    } finally {
      await seed.api.dispose()
    }
  })

  test('班管导航顺序、班级资料刷新恢复与默认全班可见', async ({ page }) => {
    await login(page, seed.adminSid, seed.adminPassword)
    const labels = await page.locator('nav button').allTextContents()
    expect(labels.indexOf('专业素质分')).toBeLessThan(labels.indexOf('班级资料'))
    expect(labels.indexOf('班级资料')).toBeLessThan(labels.indexOf('导出中心'))
    await page.getByRole('button', { name: '班级资料' }).click()
    await expect(page).toHaveURL(/\?v=admKnowledge/)
    await page.reload()
    await expect(page.locator('[data-r="hdr"]').getByText('班级资料', { exact: true })).toBeVisible()
    await expect(page.locator('tr', { hasText: '班级登记表.txt' })).toBeVisible()
    await expect(page.getByText('班级公开资料', { exact: true })).toBeVisible()
    await expect(page.getByRole('combobox')).toHaveCount(0)
    await expect(page.getByText('本班已确认')).toBeVisible()
  })

  test('班管上传后立即全班可见，且可优先预览与下载原件', async ({ page }) => {
    await login(page, seed.adminSid, seed.adminPassword)
    await page.goto('/?v=admKnowledge')
    await page.locator('main input[type=file]').first().setInputFiles({
      name: '浏览器补充说明.txt', mimeType: 'text/plain', buffer: Buffer.from('新闻拍照 0.25，写稿 0.5，共享上限 5。'),
    })
    const row = page.locator('tr', { hasText: '浏览器补充说明.txt' })
    await expect(row).toBeVisible({ timeout: 30_000 })
    await expect(row.getByText(/可检索|部分可检索/)).toBeVisible({ timeout: 60_000 })
    await row.getByRole('button', { name: '预览' }).click()
    const preview = page.getByRole('dialog', { name: '知识文件预览' })
    await expect(preview.getByRole('link', { name: /下载原件/ })).toBeVisible()
    await expect(preview.getByText('新闻拍照 0.25')).toBeVisible()
  })

  test('学生无班管知识库导航，但可从班级细则页读取全部原件', async ({ page }) => {
    const source = await seed.api.get(`agent/sources/${seed.supplementalDocumentId}/${seed.supplementalEntryId}`, {
      headers: { Authorization: `Bearer ${seed.studentToken}` },
    })
    expect(source.status()).toBe(200)
    expect(stringField(await source.json(), 'downloadUrl')).toMatch(/^http:/)
    await login(page, seed.studentSid, seed.studentPassword)
    await expect(page.getByRole('button', { name: '知识库' })).toHaveCount(0)
    await page.goto('/?v=admKnowledge')
    await expect(page).toHaveURL(/\?v=stuHome/)
    await page.goto('/?v=stuResources')
    await expect(page.getByText('班级登记表.txt', { exact: true })).toBeVisible()
    await expect(page.getByText('学院规则.txt', { exact: true })).toBeVisible()
  })

  test('桌面 Agent 右栏完成问答、工具轨迹、来源下载与学生草稿双确认', async ({ page }) => {
    await login(page, seed.studentSid, seed.studentPassword)
    const launcher = page.getByRole('button', { name: '打开班级知识 Agent' })
    await launcher.click()
    const panel = page.getByRole('dialog', { name: '班级知识 Agent' })
    const box = await panel.boundingBox()
    expect(box?.width).toBeGreaterThan(400)
    await panel.getByLabel('发送给知识 Agent').fill('四项权重是多少？')
    await panel.getByRole('button', { name: '发送消息' }).click()
    await expect(panel.getByText(/60% \/ 15% \/ 15% \/ 10%/)).toBeVisible({ timeout: 60_000 })
    /* 思维链与工具链路合成一段“思考过程”，跑完后收起，展开仍能看到每一步。
       这一问只调了一次工具，标题就得写「1 步」——按 parts 数量算会变成 3 步。

       先等引用出现再点：引用只在消息完成时才下发，而运行中折叠块是默认展开的，
       正文一出现就去点会把它关上。正文比消息完成早到，这一步不能只等正文。 */
    await expect(panel.locator('.agent-citations')).toBeVisible({ timeout: 60_000 })
    await panel.getByRole('button', { name: '思考过程 · 1 步' }).click()
    await expect(panel.getByText('先看当前已发布方案')).toBeVisible()
    await expect(panel.locator('.agent-tool-row').getByText('读取当前方案')).toBeVisible()
    /* 当前已发布方案是现算出来的，没有原件可下，所以它只是一行灰字而不是链接。
       以前这里点开会弹出一整份配置 JSON。 */
    await expect(panel.locator('.agent-citation.is-plain')).toBeVisible()
    await expect(panel.locator('.agent-citations button')).toHaveCount(0)

    // 引用到文件时是可点的链接，点了直接下原件。
    await panel.getByLabel('发送给知识 Agent').fill('原件下载探针：把那份文件给我。')
    await panel.getByRole('button', { name: '发送消息' }).click()
    const fileCitation = panel.locator('.agent-citations button').last()
    await expect(fileCitation).toBeVisible({ timeout: 60_000 })
    await expect(fileCitation).toContainText('学院规则.txt')
    const [download] = await Promise.all([page.waitForEvent('download'), fileCitation.click()])
    expect(download.suggestedFilename()).toContain('学院规则')

    // 闲聊不再被“资料性回答缺少有效来源”拦掉，只是标记为未引用资料。
    await panel.getByLabel('发送给知识 Agent').fill('你好')
    await panel.getByRole('button', { name: '发送消息' }).click()
    await expect(panel.getByText('未引用班级资料')).toBeVisible({ timeout: 60_000 })
    await expect(panel.getByText('已拒绝展示')).toHaveCount(0)

    await panel.getByLabel('发送给知识 Agent').fill('请提出一个申报草稿。')
    await panel.getByRole('button', { name: '发送消息' }).click()
    const card = panel.locator('.agent-action-card').last()
    await expect(card.getByText('志愿服务申报草稿')).toBeVisible({ timeout: 60_000 })
    await card.getByRole('button', { name: '查看并确认' }).click()
    const confirm = panel.getByRole('dialog', { name: '确认 Agent 草稿操作' })
    await expect(confirm.getByText(/只创建草稿/)).toBeVisible()
    await confirm.getByRole('button', { name: '应用草稿' }).click()
    await expect(page).toHaveURL(/v=stuSubmit.*draft=/)
    const submissions = await apiCall(seed.api, 'GET', '/submissions', seed.studentToken)
    const aiDraft = arrayField(submissions, 'items').find((item) => record(item).source === 'ai')
    expect(record(aiDraft).status).toBe('draft')
  })

  test('Agent 可选择或粘贴截图，并自动路由到识图模型', async ({ page }) => {
    await login(page, seed.studentSid, seed.studentPassword)
    await page.getByRole('button', { name: '打开班级知识 Agent' }).click()
    const panel = page.getByRole('dialog', { name: '班级知识 Agent' })
    await expect(panel.getByRole('button', { name: '添加图片或截图' })).toBeVisible()
    await panel.getByLabel('发送给知识 Agent').evaluate((textarea, encoded) => {
      const bytes = Uint8Array.from(atob(encoded), (value) => value.charCodeAt(0))
      const files = [new File([bytes], '课程截图.png', { type: 'image/png' })]
      const event = new Event('paste', { bubbles: true, cancelable: true })
      Object.defineProperty(event, 'clipboardData', { value: { files } })
      textarea.dispatchEvent(event)
    }, 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=')
    await expect(panel.getByText('课程截图.png')).toBeVisible({ timeout: 30_000 })
    await panel.getByLabel('发送给知识 Agent').fill('这张截图里有什么？')
    await panel.getByRole('button', { name: '发送消息' }).click()
    await expect(panel.getByText('图片消息已由识图模型处理')).toBeVisible({ timeout: 60_000 })
    await expect(panel.locator('.agent-message-user').last().locator('img')).toBeVisible()

    const health = await fetch(fakeHealth).then((response) => response.json())
    expect(record(health.stats).lastImageModel).toBe('fake-vision')
  })

  // 逐字输出：正文必须在模型还没写完时就开始出现，并且只增不减。
  // 合成模型对这句探针会放慢 SSE 节奏，否则整轮 60ms 就结束了，采样不到中间态。
  test('答案在生成过程中逐段显现，而不是整块出现', async ({ page }) => {
    await login(page, seed.studentSid, seed.studentPassword)
    await page.getByRole('button', { name: '打开班级知识 Agent' }).click()
    const panel = page.getByRole('dialog', { name: '班级知识 Agent' })
    const conversationCreated = page.waitForResponse((response) =>
      response.request().method() === 'POST' && /\/api\/v1\/agent\/conversations$/.test(response.url()),
    )
    await panel.getByRole('button', { name: '新对话' }).click()
    await conversationCreated
    await panel.getByLabel('发送给知识 Agent').fill('逐字流式探针：请写一段长回答。')
    const messageQueued = page.waitForResponse((response) =>
      response.request().method() === 'POST' && /\/api\/v1\/agent\/conversations\/\d+\/messages$/.test(response.url()),
    )
    await panel.getByRole('button', { name: '发送消息' }).click()
    expect((await messageQueued).ok()).toBe(true)

    const message = panel.locator('.agent-message-assistant').last()
    const answer = message.locator('.agent-answer')
    await expect(answer.getByText(/这是一段用于验证逐字输出的长回答/)).toBeVisible({ timeout: 60_000 })
    // schema 里 thought 排在 answer 前面，所以那句进度说明必须在正文之前就到位。
    await expect(message.locator('.agent-reasoning')).toHaveText('按要求慢慢写一段长文')
    /* 只量正文。运行标记、「未引用班级资料」和跑完自动收起的思考过程都在同一条
       消息里，把整条消息的文字长度当作正文长度会被它们的出现和收起带偏。 */
    const lengths: number[] = []
    for (let i = 0; i < 25; i++) {
      lengths.push(((await answer.textContent()) ?? '').length)
      await page.waitForTimeout(120)
    }
    const distinct = [...new Set(lengths)]
    expect(lengths).toEqual([...lengths].sort((a, b) => a - b))
    expect(distinct.length).toBeGreaterThan(2)
    await expect(answer.getByText(/美育与劳育 10%。$/)).toBeVisible({ timeout: 60_000 })
  })

  /* 浮窗而不是模态抽屉：只占右下角一块，页面照常能点能滚，抓标题栏还能挪走。 */
  test('Agent 浮窗停在右下角、可拖动，且不挡住页面其余部分', async ({ page }) => {
    await login(page, seed.studentSid, seed.studentPassword)
    await page.getByRole('button', { name: '打开班级知识 Agent' }).click()
    const panel = page.getByRole('dialog', { name: '班级知识 Agent' })
    /* 一条长消息（连带同样长的会话标签）不能把内容顶到面板外面去。以前 .agent-shell
       那列是隐式 auto，按 min-content 撑成 658px；贴右边的抽屉时溢出正好在屏幕外，
       浮窗一往左拖就整片露在外面。 */
    await panel.getByLabel('发送给知识 Agent').fill('示例学院学生综合素质考评实施细则的pdf你能够提供一下吗？原件下载探针')
    await panel.getByRole('button', { name: '发送消息' }).click()
    await expect(panel.locator('.agent-citations')).toBeVisible({ timeout: 60_000 })
    const overflow = await page.evaluate(() => {
      const root = document.querySelector('.agent-float') as HTMLElement
      return root.scrollWidth - root.clientWidth
    })
    expect(overflow).toBeLessThanOrEqual(1)

    const viewport = page.viewportSize() ?? { width: 0, height: 0 }
    // 先悬停：hover 的可操作性检查会等入场动画停下来，量到的才是最终位置。
    await panel.locator('.agent-head').hover({ position: { x: 40, y: 18 } })
    const before = await panel.boundingBox()
    expect(before?.height).toBeLessThan(viewport.height - 20)
    expect((before?.x ?? 0) + (before?.width ?? 0)).toBeGreaterThan(viewport.width - 48)

    // 没有遮罩、没有 overflow:hidden——面板左边的页面仍然可点、可滚。
    expect(await page.evaluate(() => getComputedStyle(document.body).overflow)).not.toBe('hidden')
    const hitsPanel = await page.evaluate(() => !!document.elementFromPoint(60, window.innerHeight / 2)?.closest('.agent-float'))
    expect(hitsPanel).toBe(false)

    await page.mouse.down()
    await page.mouse.move(220, 60, { steps: 8 })
    await page.mouse.up()
    // 抓点落在标题栏的 (40,18)，松手后浮窗左上角就该停在 (220-40, 60-18)。
    const after = await panel.boundingBox()
    expect(after?.x).toBeGreaterThan(174)
    expect(after?.x).toBeLessThan(186)
    expect(after?.y).toBeGreaterThan(36)
    expect(after?.y).toBeLessThan(48)

    // 往视口外拖：整块必须留在屏幕里，否则最底下那个输入框会被拖出去。
    await panel.locator('.agent-head').hover({ position: { x: 40, y: 18 } })
    await page.mouse.down()
    await page.mouse.move(2, viewport.height - 1, { steps: 8 })
    await page.mouse.up()
    const clamped = await panel.boundingBox()
    expect(clamped?.x).toBe(0)
    expect(clamped?.y).toBeGreaterThanOrEqual(0)
    expect((clamped?.y ?? 0) + (clamped?.height ?? 0)).toBeLessThanOrEqual(viewport.height)
  })

  test('390px 使用底部面板，Esc 关闭并把焦点还给入口', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await login(page, seed.reviewerSid, seed.reviewerPassword)
    const launcher = page.getByRole('button', { name: '打开班级知识 Agent' })
    await launcher.click()
    const panel = page.getByRole('dialog', { name: '班级知识 Agent' })
    const box = await panel.boundingBox()
    expect(box?.width).toBe(390)
    expect((box?.y ?? 0) + (box?.height ?? 0)).toBeGreaterThanOrEqual(840)
    await page.keyboard.press('Escape')
    await expect(panel).toBeHidden()
    await expect(launcher).toBeFocused()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })

  test('Ops 供应商、五路模型、三开关与预算可配置，Key 不回显', async ({ page }) => {
    await login(page, OPS_ACCOUNT, OPS_PASSWORD)
    await page.goto('/?v=opsAgent')
    await expect(page.getByText('知识问答', { exact: true })).toBeVisible()
    await expect(page.getByText('知识内容处理与问答', { exact: true })).toBeVisible()
    await expect(page.getByText('Agent 草稿操作', { exact: true })).toBeVisible()
    await expect(page.getByText('允许知识内容发送到模型端点', { exact: true })).toBeVisible()
    await expect(page.getByText('最大模型/工具步骤')).toBeVisible()
    await expect(page.getByText('AI 批量整理材料', { exact: true })).toBeVisible()
    for (const route of ['材料逐份识别', '材料整批归组', '知识文件 OCR', 'Agent 文字问答', 'Agent 图片问答']) {
      await expect(page.getByText(route, { exact: true }).first()).toBeVisible()
    }
    await expect(page.getByLabel('每批图片与 PDF 页')).toHaveValue('100')
    for (const format of ['JPEG', 'PNG', 'WebP', 'PDF']) await expect(page.getByLabel(`允许 ${format}`)).toBeChecked()
    await page.getByLabel('允许 WebP').uncheck()
    await page.getByLabel('并行识别材料').fill('3')
    await page.getByRole('button', { name: '保存使用限制' }).click()
    await expect(page.getByText('使用限制已保存')).toBeVisible()

    const providerName = `Playwright Fake ${stamp}`
    const providerKey = 'fake-playwright-provider-key'
    await page.getByRole('button', { name: '添加供应商' }).click()
    await page.getByLabel('供应商名称').fill(providerName)
    await page.getByLabel('OpenAI 兼容 /v1 URL').fill(FAKE_OPENAI_BASE_URL)
    await page.getByLabel('API Key').fill(providerKey)
    await page.getByRole('button', { name: '保存供应商' }).click()

    const providerRow = page.getByRole('row').filter({ hasText: providerName })
    await expect(providerRow.getByText('Key 已保存', { exact: true })).toBeVisible()
    await providerRow.getByRole('button', { name: '编辑' }).click()
    await expect(page.getByLabel('API Key')).toHaveValue('')
    await expect(page.getByLabel('API Key')).toHaveAttribute('placeholder', /已配置/)
    const keyStillInDOM = await page.locator('input').evaluateAll(
      (inputs, secret) => inputs.some((input) => (input as HTMLInputElement).value === secret),
      providerKey,
    )
    expect(keyStillInDOM).toBe(false)
    await page.getByRole('button', { name: '取消' }).click()

    const textRoute = page.getByText('Agent 文字问答', { exact: true }).first().locator('xpath=ancestor::div[.//select][1]')
    await textRoute.locator('select').selectOption({ label: providerName })
    await textRoute.getByPlaceholder('厂商模型 ID').fill('fake-agent-ui')
    await textRoute.getByRole('button', { name: '保存', exact: true }).click()
    await expect(page.getByText('Agent 文字问答路由已保存')).toBeVisible()
    await textRoute.getByRole('button', { name: '测试', exact: true }).click()
    await expect(page.getByText(/Agent 文字问答连接与能力正常/)).toBeVisible({ timeout: 30_000 })
    await expect(page.getByText('密钥不回传', { exact: true })).toBeVisible()
    await expect(page.getByText(/运维台不展示班级资料、对话内容或学生材料/)).toBeVisible()
  })

  test('关闭知识处理后 Agent 消失，但班级原件与手工提交仍正常', async ({ page }) => {
    await apiCall(seed.api, 'PUT', '/ops/flags/knowledgeEnabled', seed.opsToken, { value: false })
    try {
      const filename = '知识关闭仍可发布.txt'
      const content = '原件上传、发布和下载不依赖知识处理开关。'
      await uploadText(seed.api, seed.adminToken, filename, `制度/${filename}`, content)
      const resources = await apiCall(seed.api, 'GET', '/class-resources', seed.studentToken)
      const resource = arrayField(resources, 'items').find((item) => record(item).filename === filename)
      expect(resource).toBeTruthy()
      const download = await apiCall(seed.api, 'GET', `/class-resources/${stringField(resource, 'id')}/url`, seed.studentToken)
      const original = await fetch(stringField(download, 'url'))
      expect(await original.text()).toBe(content)

      await login(page, seed.studentSid, seed.studentPassword)
      await expect(page.getByRole('button', { name: '打开班级知识 Agent' })).toHaveCount(0)
      await page.goto('/?v=stuResources')
      await expect(page.getByText(filename, { exact: true })).toBeVisible()
      await page.goto('/?v=stuSubmit')
      await expect(page.locator('[data-r="hdr"]').getByText('提交材料', { exact: true })).toBeVisible()
    } finally {
      await apiCall(seed.api, 'PUT', '/ops/flags/knowledgeEnabled', seed.opsToken, { value: true })
    }
  })

  /* 班级成员：名单与账号并成一张表，且已封存的人能被班管填理由解封。
     解封此前完全没有入口——接口一直在，只是没人调。 */
  /* 「用 AI 批量整理材料」的浮层：铺满视口（页面根节点带 animation:…both，Chrome 会
     拿它当 fixed 的包含块，不挂到 body 上遮罩只盖住正文那一块），并且能拖文件进来。 */
  test('AI 批量整理浮层铺满视口，且能把文件拖进投放区', async ({ page }) => {
    await login(page, seed.studentSid, seed.studentPassword)
    await page.goto('/?v=stuSubmit')
    await page.getByRole('button', { name: '用 AI 批量整理' }).click()
    const dialog = page.getByRole('dialog', { name: 'AI 批量整理材料' })
    const viewport = page.viewportSize() ?? { width: 0, height: 0 }
    const box = await dialog.boundingBox()
    expect(box).toEqual({ x: 0, y: 0, width: viewport.width, height: viewport.height })

    await expect(dialog.getByText('将文件或文件夹拖至此区域')).toBeVisible()

    /* 必须走 CDP 的真实拖放。合成的 `new DataTransfer()` 会漏掉两件真事：它的
       `webkitGetAsEntry()` 一律返回 null，而且直接 dispatch drop 跳过了
       dragenter/dragover——线上恰恰是提交页根节点在 dragover 里把 dropEffect 设成
       'none'（React 的 portal 事件走 React 树），浏览器于是根本不触发 drop。 */
    const folder = mkdtempSync(join(tmpdir(), 'pw-drop-'))
    const dropped = join(folder, '拖进来的.png')
    writeFileSync(dropped, Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64'))
    const cdp = await page.context().newCDPSession(page)
    const zone = (await dialog.locator('[data-r="ai-drop"]').boundingBox())!
    const spot = { x: Math.round(zone.x + zone.width / 2), y: Math.round(zone.y + zone.height / 2) }
    const payload = { items: [], files: [dropped], dragOperationsMask: 1 }
    for (const type of ['dragEnter', 'dragOver', 'drop'] as const) {
      await cdp.send('Input.dispatchDragEvent', { type, x: spot.x, y: spot.y, data: payload })
    }
    await expect(dialog.getByText('拖进来的.png')).toBeVisible({ timeout: 30_000 })
    await expect(dialog.locator('img[alt="拖进来的.png"]')).toBeVisible()
    await dialog.getByRole('button', { name: '删除 拖进来的.png' }).click()
    await expect(dialog.getByText('拖进来的.png')).toHaveCount(0)
  })

  test('班级成员一张表管名单与账号，已封存可填理由解封', async ({ page }) => {
    const pendingSid = `PWU${stamp}`
    await apiCall(seed.api, 'POST', '/admin/whitelist/import', seed.adminToken, {
      csv: `sid,name,role\n${pendingSid},未注册同学,student`,
    })
    await apiCall(seed.api, 'POST', '/me/seal', seed.studentToken, { phrase: '全部提交完成', sid: seed.studentSid })

    await login(page, seed.adminSid, seed.adminPassword)
    await page.getByRole('button', { name: '班级成员' }).click()
    await expect(page).toHaveURL(/\?v=admRoster/)

    // 未注册的名单行和已注册的账号行现在在同一张表里。
    const table = page.locator('[role=table]')
    await expect(table.getByText(pendingSid, { exact: true })).toBeVisible()
    await expect(table.getByText('注册后', { exact: true })).toBeVisible()
    await expect(table.getByText(seed.studentSid, { exact: true })).toBeVisible()

    await page.getByRole('button', { name: '已封存', exact: true }).click()
    await expect(table.getByText(pendingSid, { exact: true })).toHaveCount(0)
    await expect(table.getByText('已封存但未审核', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: '解封', exact: true }).click()
    const confirm = page.getByRole('button', { name: '确认解封' })
    await expect(confirm).toBeDisabled() // 理由至少 4 字
    await page.getByLabel('解封理由').fill('班会确认后允许补交')
    await confirm.click()
    await expect(page.getByText(/已解封/)).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('button', { name: '解封', exact: true })).toHaveCount(0)
  })
})

async function login(page: Page, account: string, password: string) {
  await page.goto('/')
  const enter = page.getByRole('button', { name: '进入登录' })
  const accountInput = page.getByLabel('学号 或 邮箱')
  await expect(enter.or(accountInput)).toBeVisible()
  if (await enter.isVisible()) await enter.click()
  await accountInput.fill(account)
  await page.locator('input[type="password"][autocomplete="current-password"]').fill(password)
  const submit = page.getByRole('button', { name: '登录', exact: true })
  const responsePromise = page.waitForResponse((response) => response.request().method() === 'POST' && /\/api\/v1\/auth\/login$/.test(response.url()))
  await submit.click()
  const response = await responsePromise
  if (!response.ok()) throw new Error(`登录失败：HTTP ${response.status()}`)
  await expect(page.locator('[data-r="shell"]')).toBeVisible()
}

async function register(api: APIRequestContext, sid: string, name: string, password: string) {
  const checked = await apiCall(api, 'POST', '/auth/register/check', undefined, { sid, name })
  return apiCall(api, 'POST', '/auth/register/complete', undefined, { ticket: stringField(checked, 'ticket'), password })
}

async function uploadText(api: APIRequestContext, token: string, filename: string, logicalPath: string, content: string) {
  const bytes = Buffer.from(content)
  const pre = await apiCall(api, 'POST', '/admin/knowledge/documents/presign', token, {
    filename, logicalPath, mediaType: 'text/plain', sizeBytes: bytes.length,
  })
  const documentId = stringField(pre, 'documentId')
  const uploadUrl = stringField(pre, 'uploadUrl')
  const fields = record(record(pre).uploadFields)
  const form = new FormData()
  for (const [key, value] of Object.entries(fields)) form.append(key, String(value))
  form.append('file', new Blob([bytes], { type: 'text/plain' }), filename)
  const upload = await fetch(uploadUrl, { method: 'POST', body: form })
  expect(upload.ok).toBeTruthy()
  await apiCall(api, 'POST', `/admin/knowledge/documents/${documentId}/complete`, token, { etag: upload.headers.get('etag') ?? '' })
  return documentId
}

async function apiCall(api: APIRequestContext, method: string, path: string, token?: string, data?: unknown) {
  const response = await api.fetch(path.replace(/^\/+/, ''), {
    method,
    headers: e2eHeaders(token ? { Authorization: `Bearer ${token}`, Accept: 'application/json' } : { Accept: 'application/json' }),
    data,
  })
  const text = await response.text()
  const body: unknown = text ? JSON.parse(text) : null
  if (!response.ok()) throw new Error(`${method} ${path} → ${response.status()} ${text}`)
  return body
}

async function waitFor<T>(check: () => Promise<T | false>, timeoutMs = 90_000): Promise<T> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const value = await check()
    if (value !== false) return value
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error('等待异步处理超时')
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('API response is not an object')
  return value as Record<string, unknown>
}

function stringField(value: unknown, key: string) {
  const field = record(value)[key]
  if (typeof field !== 'string') throw new Error(`API field ${key} is not a string`)
  return field
}

function arrayField(value: unknown, key: string): unknown[] {
  const field = record(value)[key]
  if (!Array.isArray(field)) throw new Error(`API field ${key} is not an array`)
  return field
}

function schemeConfig() {
  return {
    schemeName: 'Playwright 方案',
    weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
    categories: [
      { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
      { key: 'moral', name: '思想品德', maxTotal: 100, baseItems: [], penaltyItems: [], items: [{ key: 'moral_service', name: '志愿服务', scoreRule: { type: 'per_unit', unit: '次', per: 1, cap: 10 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } }] },
      { key: 'practice', name: '实践创新', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
      { key: 'health', name: '身心素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
    ],
  }
}
