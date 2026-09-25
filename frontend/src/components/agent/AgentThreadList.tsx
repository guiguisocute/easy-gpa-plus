/* 会话列表。数据仍然来自 /agent/conversations，只是切换、新建、删除三个动作
   由 assistant-ui 的 ThreadList 驱动，runtime.ts 里的 threadList adapter 把它们
   接回既有的 mutation。 */

import { MessageSquarePlus, X } from 'lucide-react'
import { ThreadListItemPrimitive, ThreadListPrimitive } from '@assistant-ui/react'

export function AgentThreadList() {
  return (
    <ThreadListPrimitive.Root className="agent-threadlist" aria-label="最近对话">
      <ThreadListPrimitive.New className="agent-toolbar-button">
        <MessageSquarePlus size={13} aria-hidden="true" /> 新对话
      </ThreadListPrimitive.New>
      <div className="agent-threadlist-items">
        <ThreadListPrimitive.Items components={{ ThreadListItem }} />
      </div>
    </ThreadListPrimitive.Root>
  )
}

function ThreadListItem() {
  return (
    <ThreadListItemPrimitive.Root className="agent-threadlist-row">
      <ThreadListItemPrimitive.Trigger>
        <ThreadListItemPrimitive.Title fallback="新对话" />
      </ThreadListItemPrimitive.Trigger>
      <ThreadListItemPrimitive.Delete aria-label="删除对话">
        <X size={12} aria-hidden="true" />
      </ThreadListItemPrimitive.Delete>
    </ThreadListItemPrimitive.Root>
  )
}
