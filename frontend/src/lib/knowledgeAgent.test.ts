import assert from 'node:assert/strict'
import test from 'node:test'
import type { AgentAction, AgentCitation, AgentMessage, AgentToolTrace } from '../api/types.ts'
import type { AgentMessageExtras } from './knowledgeAgent.ts'
import {
  KNOWLEDGE_STATUS_LABEL,
  actionUnavailable,
  agentContextLabel,
  agentModelInputValue,
  agentRunLabel,
  answerGrounded,
  canShowAgent,
  citationDownloadable,
  fileLogicalPath,
  formatLocator,
  runUploadQueue,
  safeAgentLink,
  shouldPollAgent,
  toThreadMessage,
  visibleAgentContent,
} from './knowledgeAgent.ts'

test('文件状态和文件夹相对路径保留', () => {
  assert.equal(KNOWLEDGE_STATUS_LABEL.unsupported, '仅保留原件')
  assert.equal(fileLogicalPath({ name: '规则.pdf', webkitRelativePath: '班级\\制度\\规则.pdf' }), '班级/制度/规则.pdf')
  assert.equal(fileLogicalPath({ name: '规则.pdf', webkitRelativePath: '' }), '规则.pdf')
})

test('引用定位覆盖 PDF、表格、行号和方案', () => {
  assert.equal(formatLocator({ page: 5 }), 'PDF 第 5 页')
  assert.equal(formatLocator({ sheet: '实践创新素质', range: 'B23' }), '实践创新素质 · B23')
  assert.equal(formatLocator({ startLine: 4, endLine: 9 }), '第 4–9 行')
  assert.equal(formatLocator({ version: 2 }), '当前方案 v2')
})

test('Agent 轮询只在 queued/running 时继续', () => {
  assert.equal(shouldPollAgent([message('queued')]), true)
  assert.equal(shouldPollAgent([message('running')]), true)
  assert.equal(shouldPollAgent([message('complete')]), false)
  assert.equal(shouldPollAgent([message('failed')]), false)
})

test('来源被收回后隐藏旧回答，角色入口不向 Ops 开放', () => {
  const revoked = { ...message('complete'), content: '不应继续显示', sourceRevoked: true }
  assert.equal(visibleAgentContent(revoked), '')
  assert.equal(canShowAgent('student', true), true)
  assert.equal(canShowAgent('group', true), true)
  assert.equal(canShowAgent('class_admin', true), true)
  assert.equal(canShowAgent('ops', true), false)
  assert.equal(canShowAgent('student', false), false)
})

test('Agent 上下文同时展示真实身份、回答视角和当前页面', () => {
  assert.equal(agentContextLabel('class_admin', 'student', '提交材料'), '真实身份 班级管理员 · 当前视角 学生 · 提交材料')
})

test('草稿操作过期、stale 和已应用都不可再次确认', () => {
  const base = action('proposed', new Date(Date.now() + 60_000).toISOString())
  assert.equal(actionUnavailable(base), false)
  assert.equal(actionUnavailable({ ...base, expiresAt: new Date(Date.now() - 1).toISOString() }), true)
  assert.equal(actionUnavailable({ ...base, status: 'stale' }), true)
  assert.equal(actionUnavailable({ ...base, status: 'applied' }), true)
})

test('agentModel 留空继承文字模型，独立模型仍可编辑', () => {
  assert.equal(agentModelInputValue('deepseek-v4-flash', 'textModel'), '')
  assert.equal(agentModelInputValue('agent-special', 'database'), 'agent-special')
  assert.equal(agentModelInputValue('deepseek-v4-flash', 'textModel', 'agent-new'), 'agent-new')
})

test('危险 Markdown 链接协议不会成为可点击地址', () => {
  assert.equal(safeAgentLink('javascript:alert(1)'), '')
  assert.equal(safeAgentLink('data:text/html,<script>alert(1)</script>'), '')
  assert.match(safeAgentLink('https://example.com/rule'), /^https:\/\/example\.com/)
})

test('上传调度最大并发为 3 且保留输入顺序', async () => {
  let running = 0
  let peak = 0
  const results = await runUploadQueue([1, 2, 3, 4, 5, 6], async (item) => {
    running++
    peak = Math.max(peak, running)
    await new Promise((resolve) => setTimeout(resolve, 8))
    running--
    return item * 2
  }, { concurrency: 9 })
  assert.equal(peak, 3)
  assert.deepEqual(results.map((result) => result.value), [2, 4, 6, 8, 10, 12])
})

test('上传调度支持一次重试和取消', async () => {
  const attempts = new Map<number, number>()
  const retried = await runUploadQueue([1, 2], async (item) => {
    const count = (attempts.get(item) ?? 0) + 1
    attempts.set(item, count)
    if (item === 2 && count === 1) throw new Error('temporary')
    return item
  }, { retries: 1 })
  assert.equal(retried[1].status, 'fulfilled')
  assert.equal(attempts.get(2), 2)

  const controller = new AbortController()
  controller.abort()
  const canceled = await runUploadQueue([1], async (item) => item, { signal: controller.signal })
  assert.equal(canceled[0].status, 'rejected')
  assert.equal(canceled[0].error?.name, 'AbortError')
})

test('运行状态文案跟着当前工具走，而不是永远显示“正在查资料”', () => {
  assert.equal(agentRunLabel(message('running')), '正在思考')
  assert.equal(agentRunLabel({ ...message('running'), toolTrace: [trace(1, 'grep', 'running')] }), '正在检索内容')
  assert.equal(agentRunLabel({ ...message('running'), toolTrace: [trace(1, 'current_scheme', 'running')] }), '正在读取当前方案')
  // 工具跑完但答案还没回来，这时既不在搜也不在读。
  assert.equal(agentRunLabel({ ...message('running'), toolTrace: [trace(1, 'grep', 'complete')] }), '正在整理回答')
})

test('无引用的回答仍然展示，只是不标记为有来源', () => {
  const plain = { ...message('complete'), content: '你好，我可以帮你查本班规则。' }
  assert.equal(answerGrounded(plain), false)
  assert.equal(visibleAgentContent(plain), '你好，我可以帮你查本班规则。')
  assert.equal(answerGrounded({ ...plain, citations: [citation()] }), true)
  // 还在跑的时候不算“有来源”，免得中途就先亮出结论。
  assert.equal(answerGrounded({ ...plain, status: 'running', citations: [citation()] }), false)
})

test('消息摊平成 parts：每步先思考再调工具，最后才是答案', () => {
  const converted = toThreadMessage({
    ...message('complete'),
    content: '总分权重是 60/15/15/10。',
    finalThought: '方案里写得很清楚，直接回答',
    toolTrace: [trace(1, 'find_files', 'complete', '先找找细则在哪'), trace(2, 'grep', 'complete', '再搜权重')],
    citations: [citation()],
  })
  assert.deepEqual(partTypes(converted), [
    'reasoning', 'tool-call', 'reasoning', 'tool-call', 'reasoning', 'text',
  ])
  assert.equal(converted.status?.type, 'complete')
  // 引用和操作卡不进 parts，它们由消息级组件从 metadata 读。
  assert.equal(extras(converted).grounded, true)
  /* 折叠标题上的步数是真实工具步数，不是 parts 数量：这里 2 次工具调用摊成了
     5 个 part，按 part 数算就会写成「5 步」。 */
  assert.equal(extras(converted).steps, 2)
})

test('运行中的工具步骤不带 result，assistant-ui 据此显示为进行中', () => {
  const converted = toThreadMessage({
    ...message('running'),
    toolTrace: [trace(1, 'find_files', 'complete', '先找找细则'), trace(2, 'read_text', 'running', '读第 3 页')],
  })
  const parts = converted.content as readonly { type: string; result?: unknown; isError?: boolean }[]
  const calls = parts.filter((part) => part.type === 'tool-call')
  assert.equal(calls[0].result, 'find_files 的结果')
  assert.equal(calls[1].result, undefined)
  assert.equal(converted.status?.type, 'running')
  assert.equal(extras(converted).runLabel, '正在读取原文')
})

test('失败的工具步骤标成 isError，取消和失败映射到 incomplete', () => {
  const failed = toThreadMessage({ ...message('complete'), toolTrace: [trace(1, 'grep', 'failed', '试着搜一下')] })
  const call = (failed.content as readonly { type: string; isError?: boolean }[]).find((part) => part.type === 'tool-call')
  assert.equal(call?.isError, true)
  assert.equal(toThreadMessage(message('canceled')).status?.type, 'incomplete')
  assert.equal(toThreadMessage(message('failed')).status?.type, 'incomplete')
})

/* Worker 边流式边写 finalThought，所以它在消息跑完之前就已经在长了。它是唯一
   一条不写死状态的 reasoning：assistant-ui 只让最后一个 part 处于 running，而
   显式状态会在消息运行期间盖过这条规则。已完成步骤的那几句则必须写死 complete，
   否则运行中的最后一步会被误判成还在写。 */
test('只有最后一段进度说明不写死状态，交给 assistant-ui 判断', () => {
  const converted = toThreadMessage({
    ...message('running'),
    toolTrace: [trace(1, 'find_files', 'complete', '先找找细则在哪')],
    finalThought: '正在按方案',
  })
  const reasoning = (converted.content as readonly { type: string; status?: { type: string } }[])
    .filter((part) => part.type === 'reasoning')
  assert.equal(reasoning[0].status?.type, 'complete')
  assert.equal(reasoning[1].status, undefined)
})

/* 「当前已发布方案」是现算出来的，没有原件可下，界面据此不把它渲染成链接。 */
test('只有来自文件的引用可以下载原件', () => {
  assert.equal(citationDownloadable({ ...citation(), documentId: '7' }), true)
  assert.equal(citationDownloadable({ ...citation(), documentId: 'scheme-3', entryId: '0' }), false)
})

test('来源被收回时不下发任何过程与正文', () => {
  const converted = toThreadMessage({
    ...message('complete'),
    content: '不应继续显示',
    finalThought: '也不该露出来',
    toolTrace: [trace(1, 'read_text', 'complete', '读原文')],
    citations: [citation()],
    sourceRevoked: true,
  })
  assert.deepEqual(partTypes(converted), [])
  assert.deepEqual(extras(converted).citations, [])
})

test('用户消息只转成一条纯文本', () => {
  const converted = toThreadMessage({ ...message('complete'), role: 'user', content: '总分权重是多少？' })
  assert.equal(converted.role, 'user')
  assert.deepEqual(converted.content, [{ type: 'text', text: '总分权重是多少？' }])
})

test('用户图片保留为私有附件 ID，不把二进制塞进消息正文', () => {
  const converted = toThreadMessage({
    ...message('complete'), role: 'user', content: '',
    attachments: [{ id: '42', filename: '截图.png', mediaType: 'image/png', sizeBytes: 2048 }],
  })
  assert.deepEqual(converted.content, [])
  assert.equal(converted.attachments?.[0]?.id, '42')
  assert.deepEqual(converted.attachments?.[0]?.content, [{
    type: 'file', filename: '截图.png', mimeType: 'image/png', data: '42', sourceType: 'id',
  }])
})

function message(status: AgentMessage['status']): AgentMessage {
  return {
    id: '1', role: 'assistant', status, content: '', attachments: [], citations: [], toolTrace: [], finalThought: '', actions: [],
    sourceRevoked: false, error: null, createdAt: new Date().toISOString(), finishedAt: null,
  }
}

function trace(seq: number, tool: AgentToolTrace['tool'], status: AgentToolTrace['status'], thought = ''): AgentToolTrace {
  return { seq, tool, status, thought, summary: `${tool} 的结果`, durationMs: 120 }
}

function citation(): AgentCitation {
  return { documentId: '7', entryId: '1', filename: '学院细则.pdf', logicalPath: '制度/学院细则.pdf', locator: { page: 3 }, excerpt: '志愿服务每次 1 分', scope: 'class', sourceKind: 'class', downloadable: true, audienceRole: 'student' }
}

/** parts 里同类节点的顺序就是界面上的时间线顺序，取出来方便断言。 */
function partTypes(value: ReturnType<typeof toThreadMessage>) {
  return (value.content as readonly { type: string }[]).map((part) => part.type)
}

function extras(value: ReturnType<typeof toThreadMessage>): AgentMessageExtras {
  const custom = value.metadata?.custom
  assert.ok(custom, 'assistant 消息必须带上 metadata.custom')
  return custom as AgentMessageExtras
}

function action(status: AgentAction['status'], expiresAt: string): AgentAction {
  return {
    id: '1', kind: 'submission_draft', status, title: '草稿', summary: '', diff: [], citations: [],
    expiresAt, targetView: 'stuSubmit', createdAt: new Date().toISOString(),
  }
}
