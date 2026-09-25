import { useAgentPageContext } from '@/api/queries'
import type { AgentPageContext as PageContext } from '@/api/types'
import { currentAgentPage, effectiveAgentPage, pageKey, selectAgentEvidence, toggleAgentPagePin, useAgentPage } from '@/stores/agentPage'
import { useApp } from '@/stores/app'
import type { View } from '@/lib/nav'

function returnToAgentResource(context: PageContext) {
  if (context.view !== 'admSubs' && context.view !== 'revDeputy') return
  useApp.getState().setAgentFocus(context)
  useApp.getState().go(context.view as View)
}

export function AgentPageContext() {
  const state = useAgentPage()
  const view = useApp((s) => s.view)
  const page = effectiveAgentPage()
  const snapshot = useAgentPageContext(page, page?.observedVersion ?? '')
  if (!page) return <div className="agent-page-context"><span>随页面 · {view === 'admSubs' || view === 'revDeputy' ? '打开一条事项即可直接提问' : '当前页面未选定具体事项'}</span></div>
  return <div className="agent-page-context" aria-live="polite">
    <div className="agent-page-context-row">
      <span title={page.resourceLabel}>当前：{page.resourceLabel}</span>
      <button type="button" onClick={toggleAgentPagePin}>{state.pinned ? '取消固定' : '随页面 · 固定'}</button>
    </div>
    {snapshot.isError ? <button type="button" onClick={() => void snapshot.refetch()}>事项暂不可读取，点击重试</button> : <details>
      <summary>{snapshot.isLoading ? '正在核对当前事项…' : `自动包含：申报 · 审核意见 · 规则快照 · ${snapshot.data?.evidenceCount ?? page.evidenceCount} 份附件`}</summary>
      <span>提问时按需读取原件，读取结果会显示在回答中。</span>
      {snapshot.data?.evidence.map((file) => <button key={file.id} type="button" className={file.id === page.evidenceId ? 'is-selected' : ''}
        onClick={() => selectAgentEvidence(file.id === page.evidenceId ? '' : file.id)}>{file.filename}{file.id === page.evidenceId ? ' · 当前查看' : ''}</button>)}
    </details>}
    {pageKey(page) !== pageKey(currentAgentPage()) && <button type="button" onClick={() => returnToAgentResource(page)}>返回固定事项</button>}
  </div>
}

export function AgentMessageContext({ context }: { context?: PageContext }) {
  useAgentPage()
  if (!context?.resourceKind) return null
  const current = currentAgentPage()
  return <div className="agent-message-context">
    <span>{context.resourceLabel || `本条事项 · #${context.resourceId}`}</span>
    {pageKey(context) !== pageKey(current) && <button type="button" onClick={() => returnToAgentResource(context)}>返回原事项</button>}
  </div>
}
