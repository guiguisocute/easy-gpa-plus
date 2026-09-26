/* 常驻的 Agent 入口保持轻量；首次展开时才下载聊天实现及 assistant-ui。
   浮窗位置由入口保留，关闭再打开不会重置拖动与缩放结果。 */
import { lazy, Suspense, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { Bot, X } from 'lucide-react'
import { useAgentStatus } from '@/api/queries'
import { clampFrame, FLOAT_EDGES, moveFrame, resizeFrame, type FloatEdge, type FloatFrame } from '@/lib/floatPanel'

const AgentWorkspace = lazy(() => import('./AgentPanel').then((module) => ({ default: module.AgentWorkspace })))

export function AgentLauncher() {
  const status = useAgentStatus()
  const [open, setOpen] = useState(false)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const frame = useFloatPanel()

  if (!status.data?.enabled) return null

  const close = () => {
    setOpen(false)
    window.setTimeout(() => buttonRef.current?.focus(), 0)
  }

  /* 浮窗和入口按钮抢同一个角落，所以开着的时候把按钮收起来；关闭时它会重新挂载，
     close() 里那个 setTimeout 正好等到 ref 重新绑上。 */
  return open ? (
    <aside
      ref={frame.ref}
      className="agent-float"
      style={frame.style}
      onPointerDown={frame.onPointerDown}
      role="dialog"
      aria-label="班级知识 Agent"
    >
      <Suspense fallback={
        <div className="agent-shell" aria-busy="true">
          <header className="agent-head">
            <strong>班级知识 Agent</strong>
            <button type="button" onClick={close} aria-label="关闭 Agent"><X size={16} /></button>
          </header>
          <div className="agent-muted" role="status">正在打开 Agent…</div>
        </div>
      }>
        <AgentWorkspace status={status.data} onClose={close} />
      </Suspense>
      {FLOAT_EDGES.map((edge) => (
        <span key={edge} className={`agent-resize agent-resize-${edge}`} data-edge={edge} />
      ))}
    </aside>
  ) : (
    <button
      ref={buttonRef}
      type="button"
      aria-label="打开班级知识 Agent"
      aria-expanded={false}
      className="agent-launcher hv-op82"
      onClick={() => setOpen(true)}
    >
      <Bot size={20} strokeWidth={1.7} aria-hidden="true" />
      <span>问 Agent</span>
    </button>
  )
}

/* 标题栏拖动，边角改尺寸。hook 挂在常驻的 AgentLauncher 上，关掉再打开还在原处。
   窄屏是贴底抽屉，两个手势都关掉。窗口尺寸变了只夹回视口，不丢用户刚拉的大小。 */
function useFloatPanel() {
  const ref = useRef<HTMLElement>(null)
  const [frame, setFrame] = useState<FloatFrame | null>(null)

  useEffect(() => {
    const fit = () => setFrame((current) => current ? clampFrame(current, window.innerWidth, window.innerHeight) : current)
    window.addEventListener('resize', fit)
    return () => window.removeEventListener('resize', fit)
  }, [])

  const onPointerDown = (event: ReactPointerEvent<HTMLElement>) => {
    const node = ref.current
    const target = event.target as HTMLElement
    if (!node || event.button !== 0) return
    if (window.matchMedia('(max-width: 600px)').matches) return
    const edge = target.closest('[data-edge]')?.getAttribute('data-edge') as FloatEdge | undefined
    const dragging = !edge && !!target.closest('.agent-head') && !target.closest('button')
    if (!edge && !dragging) return
    const box = node.getBoundingClientRect()
    const start: FloatFrame = { left: box.left, top: box.top, width: box.width, height: box.height }
    const originX = event.clientX
    const originY = event.clientY
    const move = (moved: PointerEvent) => {
      const dx = moved.clientX - originX
      const dy = moved.clientY - originY
      setFrame(edge
        ? resizeFrame(start, edge, dx, dy, window.innerWidth, window.innerHeight)
        : moveFrame(start, dx, dy, window.innerWidth, window.innerHeight))
    }
    const stop = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', stop)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
    event.preventDefault()
  }

  return {
    ref,
    onPointerDown,
    style: frame ? {
      left: frame.left,
      top: frame.top,
      width: frame.width,
      height: frame.height,
      right: 'auto',
      bottom: 'auto',
    } : undefined,
  }
}
