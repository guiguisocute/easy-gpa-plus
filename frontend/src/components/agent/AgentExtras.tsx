import { readableErrorMessage } from '@/api/errorMessages'
/* 挂在正文下面的三样东西：来源引用、草稿操作卡、以及失败/收回提示。
   类名沿用重构前的 .agent-citations / .agent-action-card，E2E 依赖它们定位。 */

import type { AgentAction, AgentCitation } from '@/api/types'
import { Btn, Note, Pill } from '@/components/ui'
import { AGENT_ACTION_LABEL, actionUnavailable, citationDownloadable, citationKey, formatLocator } from '@/lib/knowledgeAgent'
import { mono } from '@/lib/style'
import { useAgentExtras } from './extras'
import { currentAgentPage, pageKey, useAgentPage } from '@/stores/agentPage'

export function AgentExtras({ onCitation, onPrepare, onReject }: {
  onCitation: (citation: AgentCitation) => void
  onPrepare: (action: AgentAction) => void
  onReject: (action: AgentAction) => void
}) {
  const extras = useAgentExtras()
  useAgentPage()
  if (!extras) return null
  return (
    <>
      {extras.sourceRevoked && <Note tone="warn">来源权限已被收回，原回答和摘录已隐藏。</Note>}
      {extras.error && <Note tone="warn">{readableErrorMessage(extras.error, '助手暂时无法完成此操作，请稍后重试')}</Note>}
      {extras.citations.length > 0 && (
        /* 引用就是一行灰色链接：点开下载原件。以前这里是带摘录的卡片，点开弹一个
           原文预览框——对「当前已发布方案」那种来源，预览出来的是一整份配置 JSON，
           没人读得下去。真要核对的人要的是原件，不是被截断的抽取文本。 */
        <div className="agent-citations">
          <span style={mono('10px', '.1em')}>来源</span>
          {extras.citations.map((item) => (
            <CitationLink key={citationKey(item)} citation={item} onOpen={onCitation} />
          ))}
        </div>
      )}
      {extras.actions.map((action) => (
        <div key={action.id} className="agent-action-card">
          <div>
            <span>{AGENT_ACTION_LABEL[action.kind]}</span>
            <Pill tone={action.status === 'proposed' || action.status === 'prepared' ? 'warn' : action.status === 'applied' ? 'ok' : 'bad'}>{{ proposed: '待确认', prepared: '待确认', applied: '已应用', rejected: '已拒绝', expired: '已过期', stale: '已失效', failed: '失败' }[action.status]}</Pill>
          </div>
          <strong>{action.title}</strong>
          <p>{action.summary}</p>
          <ActionDiff action={action} />
          <div className="agent-action-buttons">
            <Btn primary disabled={actionUnavailable(action) || (action.kind === 'arbitration_reason_draft' && pageKey(action.context) !== pageKey(currentAgentPage()))} onClick={() => onPrepare(action)}>查看并确认</Btn>
            <Btn disabled={actionUnavailable(action)} onClick={() => onReject(action)}>拒绝</Btn>
          </div>
        </div>
      ))}
    </>
  )
}

/* 当前已发布方案没有原件可下，渲染成不可点的灰字；其余引用是链接，点了才去换
   下载地址（预签名地址只活 10 分钟，提前取好等于取了个过期的）。 */
function CitationLink({ citation, onOpen }: { citation: AgentCitation; onOpen: (citation: AgentCitation) => void }) {
  /* 文件级引用指向整份文件（entryId 为空），没有页码行号可标；只有段落级引用才
     值得写「第 3 页」。 */
  const location = formatLocator(citation.locator)
  const label = citation.entryId && location && location !== citation.filename ? `${citation.filename} · ${location}` : citation.filename
  if (!citationDownloadable(citation)) return <span className="agent-citation is-plain">{label}</span>
  return (
    <button type="button" className="agent-citation" onClick={() => onOpen(citation)} title={citation.logicalPath}>
      {label}
    </button>
  )
}

export function ActionDiff({ action }: { action: Pick<AgentAction, 'diff'> }) {
  return (
    <dl className="agent-diff">
      {action.diff.map((item) => (
        <div key={item.field}>
          <dt>{item.field}</dt>
          <dd>{displayValue(item.before)}<span>→</span>{displayValue(item.after)}</dd>
        </div>
      ))}
    </dl>
  )
}

function displayValue(value: unknown) {
  if (value === undefined || value === null || value === '') return <i>空</i>
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return <code>{String(value)}</code>
  return <code>{JSON.stringify(value)}</code>
}
