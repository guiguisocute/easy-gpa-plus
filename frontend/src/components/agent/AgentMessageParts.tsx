/* 一条 Agent 回答的正文区：思维链 + 工具调用链路 + 答案。

   分组交给 assistant-ui 的 GroupedParts：相邻的 reasoning 与 tool-call 会先并进
   一个 group-cot，再各自并成 group-reasoning / group-tool。于是“思考 → 调工具 →
   再思考 → 再调工具”天然折叠成一段可展开的过程记录，而不是一长串平铺的行。 */

import { useState, type ReactNode } from 'react'
import { ChevronDown, Loader } from 'lucide-react'
import { MessagePrimitive, groupPartByType, useAuiState, useMessagePartReasoning, useMessagePartText, useSmooth } from '@assistant-ui/react'
import { RichText } from '@/components/Markdown'
import { AGENT_TOOL_LABEL } from '@/lib/knowledgeAgent'
import type { AgentToolName } from '@/api/types'
import { mono } from '@/lib/style'
import { useAgentExtras } from './extras'

const groupBy = groupPartByType({
  reasoning: ['group-cot', 'group-reasoning'],
  'tool-call': ['group-cot', 'group-tool'],
})

export function AgentMessageParts() {
  const extras = useAgentExtras()
  /* 展开与否看整条消息跑没跑完，而不是看这一组 part 的状态。assistant-ui 只让
     最后一个 part 处于 running，所以正文一开始写，思考过程这一组就已经是完成
     态了——按组状态判断会在答案还在长的时候就把过程收起来。 */
  const running = extras?.status === 'queued' || extras?.status === 'running'
  return (
    <MessagePrimitive.GroupedParts groupBy={groupBy}>
      {({ part, children }) => {
        switch (part.type) {
          /* 只有最外层可折叠。思考和工具调用是交替发生的（想一下 → 查一下 →
             再想一下），拆成两三个各自能开合的小块反而读不出先后；这里合成
             一段“思考过程”，里面保持时间顺序。 */
          case 'group-cot':
            return (
              <Collapsible label={extras?.steps ? `思考过程 · ${extras.steps} 步` : '思考过程'} running={running}>
                {children}
              </Collapsible>
            )
          case 'group-reasoning':
          case 'group-tool':
            return <div className="agent-cot-group">{children}</div>
          case 'reasoning':
            return <ReasoningText />
          case 'tool-call':
            return <ToolCallRow name={part.toolName as AgentToolName} result={part.result} isError={part.isError} />
          case 'text':
            return <AnswerText />
          case 'indicator':
            return (
              <p className="agent-indicator">
                <Loader size={12} aria-hidden="true" />
                <span>{extras?.runLabel ?? '正在思考'}…</span>
              </p>
            )
          default:
            return null
        }
      }}
    </MessagePrimitive.GroupedParts>
  )
}

/* 答案正文。Worker 边生成边把已成形的部分写回 agent_message.content，前端每
   800ms 拉一次，于是文本是一段段到的；useSmooth 把这种块状增长摊成逐字显示。

   drainMs 取得比轮询间隔略长，让上一块还没显示完下一块就到了，看起来才是连续
   的打字而不是一顿一顿。minCommitMs 限制真正提交渲染的频率——每帧提交会让
   react-markdown 每秒重解析几十次整篇正文。系统开了「减少动态效果」时
   useSmooth 会自动整段直出。 */
function AnswerText() {
  const part = useMessagePartText()
  const { text } = useSmooth(part, reveal(part.status.type))
  return <div className="agent-answer"><RichText value={text} /></div>
}

/* 只让还在写的那一段逐字显现。已经写完的照旧整段直出——否则每次打开面板，
   历史上的每条回答都会从头再打一遍字，既没有意义，又让布局持续抖动。 */
function reveal(status: string) {
  return status === 'running' ? { drainMs: 900, minCommitMs: 50 } : false
}

/* 进度说明。它和答案出自同一段流：schema 里 thought 排在 answer 前面，所以模型
   一开口这句就先到，正文随后才长出来。同样过一遍 useSmooth，两者的显现节奏才
   一致——一句突然整行蹦出、另一句逐字显现会很割裂。 */
function ReasoningText() {
  const part = useMessagePartReasoning()
  const { text } = useSmooth(part, reveal(part.status.type))
  return <p className="agent-reasoning">{text}</p>
}

/* 折叠壳。运行中默认展开——过程正在发生的时候把它藏起来毫无意义；
   跑完之后收起，让答案本身留在视线中心。 */
function Collapsible({ label, running, children }: { label: string; running: boolean; children: ReactNode }) {
  const [openedByUser, setOpenedByUser] = useState<boolean | null>(null)
  const open = openedByUser ?? running
  return (
    <section className={running ? 'agent-cot is-running' : 'agent-cot'}>
      <button type="button" aria-expanded={open} onClick={() => setOpenedByUser(!open)}>
        <span style={mono('10px', '.1em')}>{label}</span>
        <ChevronDown size={12} aria-hidden="true" className={open ? 'is-open' : undefined} />
      </button>
      {open && <div className="agent-cot-body">{children}</div>}
    </section>
  )
}

function ToolCallRow({ name, result, isError }: { name: AgentToolName; result: unknown; isError?: boolean }) {
  /* result 为 undefined 就是这一步还在跑：Worker 先落 running 行、执行完再回填
     摘要，转换器据此决定要不要带 result。 */
  const pending = result === undefined
  const summary = typeof result === 'string' ? result : ''
  return (
    <div className={isError ? 'agent-tool-row is-error' : 'agent-tool-row'}>
      <span className="agent-tool-dot" data-state={pending ? 'running' : isError ? 'error' : 'done'} aria-hidden="true" />
      <span className="agent-tool-name" style={mono('10.5px', '.06em')}>{AGENT_TOOL_LABEL[name] ?? name}</span>
      <span className="agent-tool-summary">{pending ? '执行中…' : summary}</span>
    </div>
  )
}

/** 会话为空时的欢迎区。 */
export function AgentWelcome() {
  const isEmpty = useAuiState((state) => state.thread.isEmpty)
  if (!isEmpty) return null
  return (
    <div className="agent-welcome">
      <strong>问点什么都行</strong>
      <span>我可以直接回答，也可以搜本班已授权的文件、表格和方案。用到的来源会附在答案下面。</span>
    </div>
  )
}
