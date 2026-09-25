import type { ThreadMessageLike } from '@assistant-ui/react'
import type {
  AgentAction,
  AgentCitation,
  AgentMessage,
  AgentToolName,
  KnowledgeDocumentStatus,
} from '@/api/types'
import { ROLE_LABEL, type Role } from './types.ts'

export const KNOWLEDGE_STATUS_LABEL: Record<KnowledgeDocumentStatus, string> = {
  uploading: '上传中',
  queued: '等待处理',
  processing: '正在转换',
  ready: '可检索',
  partial: '部分可检索',
  unsupported: '仅保留原件',
  failed: '转换失败',
  superseded: '已被复用',
  deleted: '已删除',
}

export const AGENT_ACTION_LABEL: Record<AgentAction['kind'], string> = {
  submission_draft: '申报草稿',
  review_draft: '审核预填',
  scheme_draft: '方案草稿',
  export_draft: '导出预填',
  arbitration_reason_draft: '终裁理由草稿',
}

export const AGENT_ERROR_LABEL: Record<string, string> = {
  agent_disabled: 'Agent 当前未启用',
  knowledge_disabled: '班级知识库当前未启用',
  knowledge_consent_required: '平台或本班尚未完成第三方处理授权',
  quota_exceeded: '今天的 Agent 消息额度已用完',
  source_forbidden: '来源权限已被收回',
  action_expired: '确认已过期，请重新查看操作卡',
  action_stale: '目标资源或权限已变化，请让 Agent 重新生成建议',
  action_forbidden: '确认令牌无效或无权应用此草稿',
}

export function agentContextLabel(actualRole: Role, effectiveRole: Role, page: string) {
  return `真实身份 ${ROLE_LABEL[actualRole]} · 当前视角 ${ROLE_LABEL[effectiveRole]} · ${page}`
}

export function fileLogicalPath(file: Pick<File, 'name'> & { webkitRelativePath?: string }) {
  const path = file.webkitRelativePath?.trim().replaceAll('\\', '/')
  return path || file.name
}

export function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** index
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`
}

export function formatLocator(locator: Record<string, unknown>) {
  if (typeof locator.section === 'string') return locator.section
  const page = numberValue(locator.page)
  if (page) return `PDF 第 ${page} 页`
  const sheet = stringValue(locator.sheet)
  const range = stringValue(locator.range)
  if (sheet) return `${sheet}${range ? ` · ${range}` : ''}`
  const member = stringValue(locator.member)
  if (member) return `压缩包 · ${member}`
  const version = numberValue(locator.version)
  if (version) return `当前方案 v${version}`
  const start = numberValue(locator.startLine)
  const end = numberValue(locator.endLine)
  if (start) return start === end || !end ? `第 ${start} 行` : `第 ${start}–${end} 行`
  return '资料片段'
}

function stringValue(value: unknown) {
  return typeof value === 'string' && value.trim() ? value : ''
}

function numberValue(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

export function shouldPollAgent(messages: AgentMessage[]) {
  return messages.some((message) => message.status === 'queued' || message.status === 'running')
}

export function visibleAgentContent(message: AgentMessage) {
  return message.sourceRevoked ? '' : message.content
}

export const AGENT_TOOL_LABEL: Record<AgentToolName, string> = {
  find_files: '搜索文件',
  grep: '检索内容',
  read_text: '读取原文',
  inspect_table: '查看表格',
  file_stats: '统计文件',
  current_scheme: '读取当前方案',
	search_product_help: '检索产品帮助',
  read_current_context: '读取当前事项',
  read_evidence: '读取佐证原件',
}

/* 运行中消息的状态文案。以前这里恒为“正在查资料”，问一句“你好”也照样显示；
   现在读最后一个工具步骤，Agent 在做什么就写什么。 */
export function agentRunLabel(message: AgentMessage) {
  const last = message.toolTrace.at(-1)
  if (!last) return '正在思考'
  if (last.status === 'running') return `正在${AGENT_TOOL_LABEL[last.tool]}`
  return '正在整理回答'
}

/** 回答是否引用了班级资料。没有引用不再是错误，只是需要如实标出来。 */
export function answerGrounded(message: AgentMessage) {
  return message.role === 'assistant' && message.status === 'complete' && message.citations.length > 0
}

/* 用 type 而不是 interface：metadata.custom 的类型是 Record<string, unknown>，
   只有类型别名才会带上隐式索引签名，interface 不会。 */
export type AgentMessageExtras = {
  context?: AgentMessage['context']
  status: AgentMessage['status']
  runLabel: string
  steps: number
  grounded: boolean
  sourceRevoked: boolean
  error: string | null
  citations: AgentCitation[]
  actions: AgentAction[]
}

/* 把后端的一条 AgentMessage 摊平成 assistant-ui 的消息 parts。

   顺序即时间线：每个工具步骤先出一条 reasoning（那句进度说明），再出一条
   tool-call；最后是给出答案前的 finalThought 和答案正文。running 的工具行
   刻意不带 result——assistant-ui 正是以“有没有 result”判断这一步是否还在跑。

   引用和操作卡不进 parts，它们挂在 metadata.custom 上由消息级组件渲染。 */
export function toThreadMessage(message: AgentMessage): ThreadMessageLike {
  const createdAt = new Date(message.createdAt)
  if (message.role === 'user') {
    return {
      role: 'user',
      id: message.id,
      createdAt,
      metadata: { custom: { context: message.context } },
      content: message.content ? [{ type: 'text', text: message.content }] : [],
      attachments: message.attachments.map((attachment) => ({
        id: attachment.id,
        type: 'image',
        name: attachment.filename,
        contentType: attachment.mediaType,
        status: { type: 'complete' as const },
        content: [{
          type: 'file' as const,
          filename: attachment.filename,
          mimeType: attachment.mediaType,
          data: attachment.id,
          sourceType: 'id' as const,
        }],
      })),
    }
  }

  const running = message.status === 'queued' || message.status === 'running'
  const content: Extract<ThreadMessageLike['content'], readonly unknown[]>[number][] = []
  if (!message.sourceRevoked) {
    for (const trace of message.toolTrace) {
      if (trace.thought) {
        content.push({ type: 'reasoning', text: trace.thought, status: { type: 'complete' } })
      }
      content.push({
        type: 'tool-call',
        toolCallId: `${message.id}-${trace.seq}`,
        toolName: trace.tool,
        args: {},
        ...(trace.status === 'running' ? {} : { result: trace.summary, isError: trace.status === 'failed' }),
      })
    }
    /* finalThought 在生成过程中就在长：Worker 边流式边把它写回消息。这一条刻意
       不写死状态——assistant-ui 的规则是「只有最后一个 part 在跑」，而写死的状态
       在消息运行期间会盖过这条规则（toMessagePartStatus）。交给它判断刚好就是我们
       要的语义：正文还没开始时它是最后一段，随消息一起 running；正文一出现它就
       不再是最后一段，自动转为 complete——而正文一出现，thought 确实已经写完了。 */
    if (message.finalThought) {
      content.push({ type: 'reasoning', text: message.finalThought })
    }
    const body = visibleAgentContent(message)
    if (body) content.push({ type: 'text', text: body, status: { type: running ? 'running' : 'complete' } })
  }

  const extras: AgentMessageExtras = {
    context: message.context,
    status: message.status,
    runLabel: agentRunLabel(message),
    /* 折叠标题上的步数必须是真实的工具步数。用 parts 数量算会翻倍还多一条：
       每一步是「一条 reasoning + 一条 tool-call」，末尾还有 finalThought，
       于是 5 次工具调用会显示成「11 步」。 */
    steps: message.toolTrace.length,
    grounded: answerGrounded(message),
    sourceRevoked: message.sourceRevoked,
    error: message.error,
    citations: message.sourceRevoked ? [] : message.citations,
    actions: message.actions,
  }
  return { role: 'assistant', id: message.id, createdAt, content, status: threadStatus(message), metadata: { custom: extras } }
}

function threadStatus(message: AgentMessage): ThreadMessageLike['status'] {
  switch (message.status) {
    case 'queued':
    case 'running':
      return { type: 'running' }
    case 'canceled':
      return { type: 'incomplete', reason: 'cancelled' }
    case 'failed':
      return { type: 'incomplete', reason: 'error' }
    default:
      return { type: 'complete', reason: 'stop' }
  }
}

export function actionUnavailable(action: AgentAction, now = Date.now()) {
  if (['applied', 'rejected', 'expired', 'stale'].includes(action.status)) return true
  return Date.parse(action.expiresAt) <= now
}

export function canShowAgent(role: Role, enabled: boolean) {
  return enabled && role !== 'ops'
}

export function agentModelInputValue(model: string, source: 'database' | 'environment' | 'textModel' | 'none', edited?: string) {
  if (edited !== undefined) return edited
  return source === 'textModel' ? '' : model
}

export function safeAgentLink(url: string) {
  try {
    const base = typeof location === 'undefined' ? 'https://easygpa.invalid' : location.origin
    const parsed = new URL(url, base)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.href : ''
  } catch {
    return ''
  }
}

export interface QueueProgress<T> {
  item: T
  index: number
  attempt: number
  state: 'running' | 'complete' | 'failed' | 'canceled'
  error?: Error
}

export interface QueueResult<R> {
  status: 'fulfilled' | 'rejected'
  value?: R
  error?: Error
}

/**
 * Small browser-side scheduler for presigned uploads. It limits only concurrent
 * transfers; authorization and all durable state remain on the server.
 */
export async function runUploadQueue<T, R>(
  items: readonly T[],
  worker: (item: T, index: number, attempt: number) => Promise<R>,
  options: { concurrency?: number; retries?: number; signal?: AbortSignal; onProgress?: (event: QueueProgress<T>) => void } = {},
) {
  const concurrency = Math.max(1, Math.min(3, options.concurrency ?? 3))
  const retries = Math.max(0, options.retries ?? 0)
  const results: QueueResult<R>[] = new Array(items.length)
  let cursor = 0

  const consume = async () => {
    while (cursor < items.length) {
      const index = cursor++
      const item = items[index]
      let lastError = new Error('上传失败')
      for (let attempt = 1; attempt <= retries + 1; attempt++) {
        if (options.signal?.aborted) {
          lastError = new DOMException('上传已取消', 'AbortError')
          options.onProgress?.({ item, index, attempt, state: 'canceled', error: lastError })
          break
        }
        options.onProgress?.({ item, index, attempt, state: 'running' })
        try {
          const value = await worker(item, index, attempt)
          results[index] = { status: 'fulfilled', value }
          options.onProgress?.({ item, index, attempt, state: 'complete' })
          lastError = new Error('')
          break
        } catch (error) {
          lastError = error instanceof Error ? error : new Error('上传失败')
          if (lastError.name === 'AbortError' || attempt > retries) {
            options.onProgress?.({ item, index, attempt, state: lastError.name === 'AbortError' ? 'canceled' : 'failed', error: lastError })
            break
          }
        }
      }
      if (!results[index]) results[index] = { status: 'rejected', error: lastError }
    }
  }

  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, consume))
  return results
}

export function citationKey(citation: AgentCitation) {
  return `${citation.documentId}:${citation.entryId}:${JSON.stringify(citation.locator)}`
}

/* 「当前已发布方案」是从数据库现算出来的，不是上传的文件，没有原件可下。后端对
   这种来源回一个空的 downloadUrl，界面这边直接不把它渲染成链接。 */
export function citationDownloadable(citation: AgentCitation) {
	return citation.downloadable !== false && !citation.documentId.startsWith('scheme-')
}
