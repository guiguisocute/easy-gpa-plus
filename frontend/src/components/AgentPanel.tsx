/* Agent 面板的外壳：入口按钮、右下角浮窗、配额头部、来源下载和草稿确认。

   聊天本身（消息流、思维链、工具链路、输入框、会话列表）由 assistant-ui 的
   primitives 渲染，见 ./agent/。这里只留下那些 assistant-ui 不该知道的东西：
   综测系统自己的权限门槛、来源重鉴权和“草稿只到草稿”的二次确认。 */

import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { Bot, X } from 'lucide-react'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { ApiError } from '@/api/client'
import type { AgentAction, AgentCitation, AgentStatus, PreparedAgentAction } from '@/api/types'
import { useAgentActions, useAgentSourceDownload, useAgentStatus } from '@/api/queries'
import { ActionDiff } from '@/components/agent/AgentExtras'
import { AgentThread } from '@/components/agent/AgentThread'
import { AgentThreadList } from '@/components/agent/AgentThreadList'
import { useAgentRuntime } from '@/components/agent/runtime'
import { Btn, Empty, Note } from '@/components/ui'
import { AGENT_ACTION_LABEL, AGENT_ERROR_LABEL, agentContextLabel } from '@/lib/knowledgeAgent'
import { clampFrame, FLOAT_EDGES, moveFrame, resizeFrame, type FloatEdge, type FloatFrame } from '@/lib/floatPanel'
import { navEntry, NAV, type View } from '@/lib/nav'
import { mono } from '@/lib/style'
import type { Role } from '@/lib/types'
import { useApp } from '@/stores/app'
import { currentAgentPage, pageKey, sameAgentDraft } from '@/stores/agentPage'
import { agentPageContextSchema } from '@/api/knowledgeAgentSchemas'
import { RichText } from '@/components/Markdown'

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
      <AgentWorkspace status={status.data} onClose={close} />
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

export function AgentWorkspace({ status: suppliedStatus, onClose, embedded = false }: { status?: AgentStatus; onClose?: () => void; embedded?: boolean }) {
  const statusQuery = useAgentStatus()
  const status = suppliedStatus ?? statusQuery.data

  if (!status?.enabled) {
    return (
      <div className={embedded ? 'agent-embedded-empty' : 'agent-shell'}>
        {!embedded && <AgentHeader title="班级知识 Agent" quota="—" onClose={onClose} />}
        <Empty title="Agent 暂不可用" desc={AGENT_ERROR_LABEL[status?.reason ?? 'agent_disabled'] ?? '找平台运维或者班管看看开关和授权。'} />
      </div>
    )
  }
  return <AgentSession status={status} onClose={onClose} embedded={embedded} />
}

/* 单独一层，是因为 useAgentRuntime 里有一串 hook：Agent 没开启时上面那个分支
   直接返回，不能让这些 hook 有时执行有时不执行。 */
function AgentSession({ status, onClose, embedded }: { status: AgentStatus; onClose?: () => void; embedded: boolean }) {
  const user = useApp((state) => state.user)
  const viewAs = useApp((state) => state.viewAs)
  const view = useApp((state) => state.view)
  const go = useApp((state) => state.go)
  const setAgentHandoff = useApp((state) => state.setAgentHandoff)
  const say = useApp((state) => state.say)
  const actions = useAgentActions()
  const [preparing, setPreparing] = useState<AgentAction | null>(null)
  const [prepared, setPrepared] = useState<PreparedAgentAction | null>(null)
  const shellRef = useRef<HTMLDivElement>(null)
  const download = useAgentSourceDownload()
  const preparedPage = useRef<ReturnType<typeof currentAgentPage>>(null)
  const [businessSource, setBusinessSource] = useState<{ filename: string; content: string; downloadUrl: string; mediaType: string } | null>(null)

  const fail = (error: unknown) => {
    if (error instanceof ApiError) say(AGENT_ERROR_LABEL[error.code] ?? error.message)
    else say('Agent 操作失败，请重试')
  }

	const { runtime, title, isInitializing } = useAgentRuntime(true, fail, status.attachmentQuota)

  /* 只接 Esc。浮窗不是模态的——页面照常能点、能滚，所以这里没有焦点陷阱，Tab
     会正常走出面板。 */
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (businessSource) setBusinessSource(null)
      else if (preparing) {
        setPreparing(null)
        setPrepared(null)
      } else onClose?.()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [preparing, businessSource, onClose])

  /* 点引用 → 现换一个 10 分钟有效的下载地址 → 交给浏览器下原件。用建 <a> 再点的
     写法而不是 window.open：异步回调里开新窗口会被拦，而带 Content-Disposition
     的地址本来就不需要新窗口。链接必须先进 DOM 再点——游离的 <a> 在 Chrome 里
     点了不会真的发起下载。 */
  const openSource = (citation: AgentCitation) => {
    download.mutate(citation, {
      onSuccess: (source) => {
        if (citation.scope === 'business') { setBusinessSource(source); return }
        const contentUrl = !source.downloadUrl && source.content
          ? URL.createObjectURL(new Blob([source.content], { type: source.mediaType || 'text/markdown;charset=utf-8' }))
          : ''
        const href = source.downloadUrl || contentUrl
        if (!href) return
        const link = document.createElement('a')
        link.href = href
        link.rel = 'noopener'
        if (contentUrl) link.download = source.filename
        document.body.append(link)
        link.click()
        link.remove()
        if (contentUrl) URL.revokeObjectURL(contentUrl)
      },
      onError: fail,
    })
  }

  const prepare = async (action: AgentAction) => {
    if (action.kind === 'arbitration_reason_draft' && pageKey(action.context) !== pageKey(currentAgentPage())) { say('请先返回这份草稿对应的原事项'); return }
    if (action.kind === 'arbitration_reason_draft' && !sameAgentDraft(action.context?.draft, currentAgentPage()?.draft)) { say('生成草稿后表单已修改，请基于现有内容重新生成理由'); return }
    preparedPage.current = currentAgentPage()
    setPreparing(action)
    setPrepared(null)
    try {
      setPrepared(await actions.prepareAction.mutateAsync(action.id))
    } catch (error) {
      fail(error)
      setPreparing(null)
    }
  }

  const apply = async () => {
    if (!prepared) return
    try {
      if (prepared.kind === 'arbitration_reason_draft' && (pageKey(prepared.context) !== pageKey(currentAgentPage()) ||
        !sameAgentDraft(preparedPage.current?.draft, currentAgentPage()?.draft))) { say('页面或表单已变化，请重新查看草稿'); return }
      const result = await actions.applyAction.mutateAsync({ id: prepared.id, confirmToken: prepared.confirmToken })
      setPreparing(null)
      setPrepared(null)
      if (result.kind === 'arbitration_reason_draft') {
        const bound = agentPageContextSchema.parse(result.prefill?.context)
        const current = currentAgentPage()
        if (pageKey(current) !== pageKey(bound) || typeof result.prefill?.reason !== 'string' || !current?.applyReason(result.prefill.reason, bound.draft)) {
          say('页面或表单内容已变化，未覆盖现有内容；请在当前事项重新生成理由')
          return
        }
        say('理由已填入本条事项，尚未提交')
        return
      }
      const target = knownView(result.navigateTo)
      if (target) {
        setAgentHandoff(result)
        go(target, target === 'stuSubmit' ? result.resourceId : undefined)
        const url = new URL(location.href)
        url.searchParams.set('agentAction', result.id)
        url.searchParams.set('resourceId', result.resourceId)
        history.replaceState(null, '', url)
      }
      say('草稿已经建好或者填上了。真要生效，还得回原来那一页确认')
      onClose?.()
    } catch (error) {
      fail(error)
    }
  }

  return (
    <div ref={shellRef} className={embedded ? 'agent-shell agent-shell-embedded' : 'agent-shell'}>
    <AgentHeader
      title={title}
      quota={`${status.quota.used}/${status.quota.limit} 条 · 附件 ${(status.attachmentQuota.dailyUsedBytes / 1048576).toFixed(1)}/${status.attachmentQuota.dailyMb} MB`}
      actualRole={user?.role ?? 'student'}
      effectiveRole={viewAs ?? user?.role ?? 'student'}
      page={navEntry(viewAs ?? user?.role ?? 'student', view).label}
      onClose={onClose}
    />
      <AssistantRuntimeProvider runtime={runtime}>
        <AgentThreadList />
        {isInitializing ? <div className="load-bar"><span /></div> : (
          <AgentThread
            onCitation={openSource}
            onPrepare={prepare}
            onReject={(action) => actions.rejectAction.mutate(action.id, { onError: fail })}
          />
        )}
      </AssistantRuntimeProvider>

      {businessSource && <div className="agent-subdialog" role="dialog" aria-label="查看本条事项来源">
        <div className="agent-subdialog-head"><div><strong>{businessSource.filename}</strong><span>原始业务来源 · 已重新核对读取权限</span></div>
          <button type="button" aria-label="关闭来源" onClick={() => setBusinessSource(null)}><X size={15} /></button></div>
        {businessSource.content && <RichText value={businessSource.content} />}
        {/^image\/(png|jpeg|webp|gif)$/.test(businessSource.mediaType) && businessSource.downloadUrl && <img className="agent-source-image" src={businessSource.downloadUrl} alt={businessSource.filename} />}
        {businessSource.downloadUrl && <a href={businessSource.downloadUrl} target="_blank" rel="noopener noreferrer">打开原件</a>}
      </div>}

      {preparing && (
        <div className="agent-subdialog" role="dialog" aria-modal="true" aria-label="确认 Agent 草稿操作">
          <div className="agent-subdialog-head">
            <div><strong>{preparing.title}</strong><span>{AGENT_ACTION_LABEL[preparing.kind]} · 只到草稿</span></div>
            <button type="button" onClick={() => { setPreparing(null); setPrepared(null) }} aria-label="关闭确认"><X size={15} /></button>
          </div>
          {!prepared ? <div className="agent-muted">正在重新核对权限、方案和目标资源…</div> : (
            <>
              <Note tone="warn">{prepared.risk}</Note>
              <ActionDiff action={prepared} />
              <div className="agent-confirm-actions">
                <Btn onClick={() => { setPreparing(null); setPrepared(null) }}>取消</Btn>
                <Btn primary disabled={actions.applyAction.isPending} onClick={() => void apply()}>{actions.applyAction.isPending ? '应用中…' : prepared.kind === 'arbitration_reason_draft' ? '填入本条理由' : '应用草稿'}</Btn>
              </div>
              <span className="agent-muted">确认令牌仅本次有效，{new Date(prepared.confirmExpiresAt).toLocaleTimeString('zh-CN')} 前可用。</span>
            </>
          )}
        </div>
      )}
    </div>
  )
}

function AgentHeader({ title, quota, actualRole, effectiveRole, page, onClose }: { title: string; quota: string; actualRole?: Role; effectiveRole?: Role; page?: string; onClose?: () => void }) {
  return (
    <header className="agent-head">
      <div><span style={mono('10px', '.14em')}>KNOWLEDGE AGENT</span><strong>{title}</strong>{actualRole && effectiveRole && page && <span className="agent-context">{agentContextLabel(actualRole, effectiveRole, page)}</span>}</div>
      <span className="agent-quota">今日 {quota}</span>
      {onClose && <button type="button" onClick={onClose} aria-label="关闭 Agent"><X size={16} /></button>}
    </header>
  )
}

function knownView(value: string): View | null {
  return Object.values(NAV).flat().some((entry) => entry.view === value) ? value as View : null
}
