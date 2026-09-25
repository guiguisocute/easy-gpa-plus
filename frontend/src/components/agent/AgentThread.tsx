import { createContext, useContext, useEffect, useMemo } from 'react'
import { ImagePlus, Send, Square, X } from 'lucide-react'
import { AttachmentPrimitive, ComposerPrimitive, MessagePrimitive, ThreadPrimitive, useAuiState } from '@assistant-ui/react'
import type { AgentAction, AgentCitation } from '@/api/types'
import { useAgentAttachmentLink } from '@/api/queries'
import { Pill } from '@/components/ui'
import { AgentExtras } from './AgentExtras'
import { AgentMessageParts, AgentWelcome } from './AgentMessageParts'
import { useAgentExtras } from './extras'
import { AgentPageContext, AgentMessageContext } from './AgentPageContext'

interface AgentThreadCallbacks {
  onCitation: (citation: AgentCitation) => void
  onPrepare: (action: AgentAction) => void
  onReject: (action: AgentAction) => void
}

/* 三个回调走 context 而不是 props。ThreadPrimitive.Messages 的 components 是按
   引用记忆的：在这里写一个内联箭头组件，每次轮询（800ms）都会换掉组件类型，
   整棵消息子树被卸载重建，折叠状态和滚动位置全部丢失。 */
const CallbackContext = createContext<AgentThreadCallbacks | null>(null)
const MESSAGE_COMPONENTS = { UserMessage, AssistantMessage }
const USER_ATTACHMENT_COMPONENTS = { Attachment: UserAttachment }
const COMPOSER_ATTACHMENT_COMPONENTS = { Attachment: ComposerAttachment }

export function AgentThread(callbacks: AgentThreadCallbacks) {
  const { onCitation, onPrepare, onReject } = callbacks
  const value = useMemo(() => ({ onCitation, onPrepare, onReject }), [onCitation, onPrepare, onReject])
  return (
    <CallbackContext value={value}>
      <ThreadPrimitive.Root className="agent-thread">
        <ThreadPrimitive.Viewport className="agent-messages" autoScroll>
          <AgentWelcome />
          <ThreadPrimitive.Messages components={MESSAGE_COMPONENTS} />
        </ThreadPrimitive.Viewport>
        <AgentComposer />
      </ThreadPrimitive.Root>
    </CallbackContext>
  )
}

function UserMessage() {
  const extras = useAgentExtras()
  return (
    <MessagePrimitive.Root className="agent-message agent-message-user">
      <div className="agent-message-meta"><span>你</span></div>
      <AgentMessageContext context={extras?.context} />
      <MessagePrimitive.Attachments components={USER_ATTACHMENT_COMPONENTS} />
      <MessagePrimitive.Parts />
    </MessagePrimitive.Root>
  )
}

function AssistantMessage() {
  const callbacks = useContext(CallbackContext)
  const extras = useAgentExtras()
  const running = extras?.status === 'queued' || extras?.status === 'running'
  return (
    <MessagePrimitive.Root className="agent-message agent-message-assistant">
      <div className="agent-message-meta">
        <span>Agent</span>
        {running && <Pill tone="warn">{extras.runLabel}</Pill>}
        {extras?.status === 'canceled' && <Pill>已停止</Pill>}
        {extras?.status === 'failed' && <Pill tone="bad">失败</Pill>}
        {/* 无引用不再是错误，但读的人有权知道这个答案没有落在班级资料上。 */}
        {extras?.status === 'complete' && !extras.grounded && !extras.sourceRevoked && <Pill>未引用班级资料</Pill>}
      </div>
      <AgentMessageContext context={extras?.context} />
      <AgentMessageParts />
      {callbacks && <AgentExtras {...callbacks} />}
    </MessagePrimitive.Root>
  )
}

function AgentComposer() {
  const isRunning = useAuiState((state) => state.thread.isRunning)
  return (
    <ComposerPrimitive.Root className="agent-composer">
      <AgentPageContext />
      <ComposerPrimitive.AttachmentDropzone asChild>
        <div className="agent-composer-shell">
          <ComposerPrimitive.Attachments components={COMPOSER_ATTACHMENT_COMPONENTS} />
          <ComposerPrimitive.Input
            rows={3}
            maxLength={10000}
            aria-label="发送给知识 Agent"
            placeholder="问当前事项、班级规则，或者说说你想处理什么…"
          />
          <div className="agent-composer-actions">
            <ComposerPrimitive.AddAttachment className="agent-attach" aria-label="添加图片或截图">
              <ImagePlus size={15} /> <span>图片</span>
            </ComposerPrimitive.AddAttachment>
            <span className="agent-attach-hint">支持粘贴 / 拖入</span>
            {isRunning ? (
              <ComposerPrimitive.Cancel className="agent-send" aria-label="停止处理">
                <Square size={14} fill="currentColor" /> 停止
              </ComposerPrimitive.Cancel>
            ) : (
              <ComposerPrimitive.Send className="agent-send" aria-label="发送消息">
                <Send size={14} /> 发送
              </ComposerPrimitive.Send>
            )}
          </div>
        </div>
      </ComposerPrimitive.AttachmentDropzone>
    </ComposerPrimitive.Root>
  )
}

function ComposerAttachment() {
  const attachment = useAuiState((state) => state.attachment)
  const preview = useMemo(() => attachment?.file ? URL.createObjectURL(attachment.file) : '', [attachment?.file])
  useEffect(() => () => {
    if (preview) URL.revokeObjectURL(preview)
  }, [preview])
  if (!attachment) return null
  return (
    <AttachmentPrimitive.Root className="agent-composer-attachment">
      {preview ? <img src={preview} alt="" /> : <span aria-hidden="true"><ImagePlus size={16} /></span>}
      <AttachmentPrimitive.Name />
      {attachment.status.type === 'running' && <small>{attachment.status.progress}%</small>}
      <AttachmentPrimitive.Remove aria-label={`移除 ${attachment.name}`}><X size={13} /></AttachmentPrimitive.Remove>
    </AttachmentPrimitive.Root>
  )
}

function UserAttachment() {
  const attachment = useAuiState((state) => state.attachment)
  const link = useAgentAttachmentLink(attachment?.id ?? '', !!attachment)
  if (!attachment) return null
  const body = (
    <>
      {link.data?.downloadUrl
        ? <img src={link.data.downloadUrl} alt={attachment.name} />
        : <span className="agent-attachment-loading"><ImagePlus size={18} aria-hidden="true" /></span>}
      <span><strong>{attachment.name}</strong><small>{formatImageSize(link.data?.sizeBytes)}</small></span>
    </>
  )
  return link.data?.downloadUrl ? (
    <a className="agent-message-attachment" href={link.data.downloadUrl} target="_blank" rel="noopener noreferrer">{body}</a>
  ) : <div className="agent-message-attachment">{body}</div>
}

function formatImageSize(bytes: number | undefined) {
  if (!bytes) return '图片附件'
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`
}
