import { useAuiState } from '@assistant-ui/react'
import type { AgentMessageExtras } from '@/lib/knowledgeAgent'

/* 引用、操作卡、失败原因这些不属于“消息正文”，不该进 parts；转换器把它们挂在
   metadata.custom 上，消息级组件从这里读回来。 */
export function useAgentExtras() {
  return useAuiState((state) => state.message.metadata.custom) as AgentMessageExtras | undefined
}
