/* 把后端的“建会话 / 发消息 / 轮询 / 取消”接到 assistant-ui 的 external store 上。

   为什么是 external store 而不是 assistant-ui 自己的 runtime：消息的真实状态在
   PostgreSQL 里，由独立的 worker:agent 进程推进，API 与 worker 不同进程，浏览器
   拿不到模型流。这里的数据源仍然是既有的 React Query 轮询，assistant-ui 只负责
   把它渲染成一个标准聊天界面。 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useExternalStoreRuntime, type AppendMessage, type ExternalStoreThreadData } from '@assistant-ui/react'
import { useQueryClient } from '@tanstack/react-query'
import { readAgentPageContext, useAgentActions, useAgentConversation, useAgentConversations } from '@/api/queries'
import type { AgentMessage, AgentStatus } from '@/api/types'
import { toThreadMessage } from '@/lib/knowledgeAgent'
import { useApp } from '@/stores/app'
import { agentComposerDrafts, effectiveAgentPage, pageKey, useAgentPage } from '@/stores/agentPage'
import { AgentImageAttachmentAdapter } from './attachments'

export interface AgentRuntimeHandle {
  runtime: ReturnType<typeof useExternalStoreRuntime<AgentMessage>>
  conversationId: string | null
  title: string
  messages: AgentMessage[]
  isPending: boolean
  isInitializing: boolean
}

export function useAgentRuntime(enabled: boolean, onError: (error: unknown) => void, quota?: AgentStatus['attachmentQuota']): AgentRuntimeHandle {
  const queryClient = useQueryClient()
  const view = useApp((state) => state.view)
  const user = useApp((state) => state.user)
  useAgentPage()
  const scopeKey = `${user?.classId}:${user?.sid}:${pageKey(effectiveAgentPage() ?? { view })}`
  const list = useAgentConversations(enabled)
  const actions = useAgentActions()
  const [selectedConversationId, setConversationId] = useState<string | null>(null)
  const items = list.data?.items
  const conversationId = selectedConversationId ?? items?.[0]?.id ?? null
  // 新会话的第一次失效通知可能撞上“空会话”首次取数。记住发送前的消息数，
  // 在服务端新消息真正读回来前保持短轮询，避免数据已经入库而面板仍停在空状态。
  const [awaitingMessageCount, setAwaitingMessageCount] = useState<number | null>(null)
  const conversation = useAgentConversation(conversationId, enabled, awaitingMessageCount !== null)
  const errorRef = useRef(onError)
  errorRef.current = onError
	const maxAttachmentFileMb = quota?.maxFileMb ?? 5
	const maxAttachmentMessageMb = quota?.maxMessageMb ?? 12
	const maxAttachmentCount = quota?.maxCount ?? 4
	const attachmentAdapter = useMemo(() => new AgentImageAttachmentAdapter(
		(error) => errorRef.current(error),
		{ maxFileMb: maxAttachmentFileMb, maxMessageMb: maxAttachmentMessageMb, maxCount: maxAttachmentCount },
	), [maxAttachmentFileMb, maxAttachmentMessageMb, maxAttachmentCount])
  const messages = useMemo(() => conversation.data?.messages ?? [], [conversation.data?.messages])
  useEffect(() => {
    if (awaitingMessageCount !== null && messages.length > awaitingMessageCount) setAwaitingMessageCount(null)
  }, [awaitingMessageCount, messages.length])
  const running = messages.find((message) => message.role === 'assistant' && (message.status === 'queued' || message.status === 'running'))

  const { createConversation, deleteConversation, sendMessage, cancelMessage } = actions
  /* 会话是懒创建的：用户可以直接开始打字，第一条消息才真正建会话，
     这样侧栏不会因为点开面板就多出一堆空对话。 */
  const ensureConversation = useCallback(async () => {
    if (conversationId) return conversationId
    const created = await createConversation.mutateAsync(undefined)
    setConversationId(created.id)
    return created.id
  }, [conversationId, createConversation])

  const onNew = useCallback(async (message: AppendMessage) => {
    const content = message.content
      .filter((part): part is Extract<typeof part, { type: 'text' }> => part.type === 'text')
      .map((part) => part.text)
      .join('\n')
      .trim()
    const attachmentIds = (message.attachments ?? []).map((attachment) => attachment.id)
    if (!content && attachmentIds.length === 0) return
    setAwaitingMessageCount(messages.length)
    try {
      // Capture before any await. Navigating during metadata refresh must not
      // rebind this message or silently write the draft into another item.
      const page = effectiveAgentPage()
      const captured = page ? { view: page.view, resourceKind: page.resourceKind, resourceId: page.resourceId, evidenceId: page.evidenceId, draft: page.draft } : { view }
      const context = page ? { ...(await readAgentPageContext(captured, page.observedVersion)).context, draft: captured.draft } : captured
      await sendMessage.mutateAsync({ conversationId: await ensureConversation(), content, attachmentIds, context })
      attachmentAdapter.markSent(attachmentIds)
    } catch (error) {
      setAwaitingMessageCount(null)
      await attachmentAdapter.discard(attachmentIds)
      void queryClient.invalidateQueries({ queryKey: ['admin'] })
      void queryClient.invalidateQueries({ queryKey: ['review'] })
      void queryClient.invalidateQueries({ queryKey: ['appeal'] })
      onError(error)
      throw error
    }
  }, [attachmentAdapter, ensureConversation, messages.length, onError, queryClient, sendMessage, view])

  const onCancel = useCallback(async () => {
    if (!running || !conversationId) return
    try {
      await cancelMessage.mutateAsync({ messageId: running.id, conversationId })
    } catch (error) {
      onError(error)
    }
  }, [cancelMessage, conversationId, onError, running])

  const threads = useMemo<ExternalStoreThreadData<'regular'>[]>(
    () => (items ?? []).map((item) => ({ id: item.id, status: 'regular', title: item.title })),
    [items],
  )

  const runtime = useExternalStoreRuntime<AgentMessage>({
    messages,
    isRunning: !!running,
    convertMessage: toThreadMessage,
    onNew,
    onCancel,
    adapters: {
      attachments: attachmentAdapter,
      threadList: {
        threadId: conversationId ?? undefined,
        threads,
        isLoading: list.isLoading,
        onSwitchToThread: (id) => setConversationId(id),
        onSwitchToNewThread: async () => {
          try {
            const created = await createConversation.mutateAsync(undefined)
            setConversationId(created.id)
          } catch (error) {
            onError(error)
          }
        },
        onDelete: async (id) => {
          try {
            await deleteConversation.mutateAsync(id)
            setConversationId((current) => (current === id ? null : current))
          } catch (error) {
            onError(error)
          }
        },
      },
    },
  })

  const composer = runtime.thread.composer
  useEffect(() => {
    composer.setText(agentComposerDrafts.get(scopeKey) ?? '')
    // Attachments belong to the composer where they were added; changing item
    // must never send A's image with B's question.
    return composer.subscribe(() => {
      agentComposerDrafts.delete(scopeKey); agentComposerDrafts.set(scopeKey, composer.getState().text)
      if (agentComposerDrafts.size > 50) agentComposerDrafts.delete(agentComposerDrafts.keys().next().value!)
    })
  }, [composer, scopeKey])
  const priorScope = useRef(scopeKey)
  useEffect(() => {
    if (priorScope.current !== scopeKey) {
      priorScope.current = scopeKey
      void composer.clearAttachments().catch((error: unknown) => errorRef.current(error))
    }
  }, [composer, scopeKey])

  return {
    runtime,
    conversationId,
    title: conversation.data?.title || '班级知识 Agent',
    messages,
    isPending: sendMessage.isPending || createConversation.isPending,
    isInitializing: list.isLoading || (!!conversationId && conversation.isLoading),
  }
}
