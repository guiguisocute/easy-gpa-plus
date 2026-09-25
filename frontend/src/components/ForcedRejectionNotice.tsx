import type { ForceRejection } from '@/api/types'
import * as f from '@/lib/format'

export function ForcedRejectionNotice({ rejection, title }: { rejection: ForceRejection; title?: string }) {
  const adjusting = rejection.score != null
  const label = adjusting ? '改分' : '驳回'
  return (
    <div role="note" aria-label={`强制${label}通知`} style={{ border: '1px solid var(--red)', borderLeftWidth: 4, background: 'var(--redBg)', padding: '14px 16px', lineHeight: 1.7, overflowWrap: 'anywhere' }}>
      <div style={{ fontSize: 13.5, fontWeight: 600, color: 'var(--red)' }}>{rejection.selfRejected ? '本人已主动强制驳回' : `已被强制${label}`}{title ? ` · ${f.submissionTitle(title)}` : ''}</div>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--red)', marginTop: 4 }}>认定分：{rejection.previousScore === null ? '未定分' : `${f.score(rejection.previousScore)} 分`} → {f.score(rejection.score ?? 0)} 分</div>
      <div style={{ fontSize: 13, color: 'var(--fg)', whiteSpace: 'pre-wrap', marginTop: 5 }}>{label}理由：{rejection.reason}</div>
      <div style={{ fontSize: 12, color: 'var(--fg2)', marginTop: 5 }}>{f.dateTime(rejection.rejectedAt)} · {rejection.selfRejected ? '材料还在，不能反悔，也不能再申诉。' : '已终裁，不能再申诉。有疑问找班管。'}</div>
    </div>
  )
}
