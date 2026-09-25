import { useScoreHistory } from '@/api/queries'
import type { ReportKind } from '@/api/types'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { RichText } from '@/components/Markdown'
import { Sub, TextBtn } from '@/components/ui'
import { usePublicityWindow } from '@/components/usePublicityWindow'
import * as f from '@/lib/format'

const LABELS: Record<string, string> = {
  admin_grant: '班管直接加分 · 无需审核',
  collective_grant: '共同加分决议', collective_decision: '共治评审裁决',
  review: '审核员初审', arbitration: '班管仲裁', force_score: '管理员强制改分', force_reject: '管理员强制驳回', self_reject: '本人主动放弃分数',
  appeal_filed: '学生提出申诉', appeal_review: '审核员申诉复评', appeal_final: '班管申诉终裁',
  appeal_resolved: '申诉处理结论', appeal_withdrawn: '学生撤回申诉',
  objection_filed: '小组提出异议', objection_decided: '班管异议终裁',
}
const STATUSES: Record<string, string> = {
  accepted: '通过', adjusted: '调整', rejected: '驳回', upheld: '维持', uphold: '维持', adjust: '调整',
  filed: '已提交', reviewing: '复评中', escalated: '待终裁', final: '已终裁', resolved: '已处理',
  withdrawn: '已撤回', scored: '已定分', locked: '已锁定', applied: '已生效', dismissed: '已驳回',
}

export function ScoreHistory({ kind, targetId, audience, shownEvidenceIds = [], collective = false }: { kind: ReportKind; targetId: string | null; audience: 'student' | 'reviewer'; shownEvidenceIds?: string[]; collective?: boolean }) {
  const history = useScoreHistory(kind, targetId, audience, collective)
  const publicity = usePublicityWindow(history.data?.publicity, history.data?.serverNow, history.dataUpdatedAt)
  const files = audience === 'reviewer' || publicity ? history.data?.evidence ?? [] : []
  const extraFiles = files.filter((file) => !shownEvidenceIds.includes(file.id))
  return (
    <section aria-label="审核与申诉轨迹" style={{ padding: '16px 0', minWidth: 0 }}>
      <Sub title="审核与申诉轨迹" note={audience === 'student' ? publicity ? '处理人身份隐藏 · 公示期间可查看佐证原件' : '处理人身份隐藏 · 原件仅公示期间可查看' : '身份隐藏 · 附件可预览或下载'} />
      {!targetId ? <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>暂无审核或申诉记录。</div>
        : history.isLoading ? <div className="load-bar"><span /></div>
        : history.isError ? <div style={{ fontSize: 12.5, color: 'var(--red)' }}>轨迹加载失败。<TextBtn onClick={() => void history.refetch()}>重试</TextBtn></div>
        : <>
          {extraFiles.length > 0 && <section aria-label={audience === 'student' ? '公示佐证原件' : '轨迹附件'} style={{ paddingBottom: 16 }}>
            {audience === 'student' && <Sub title="公示佐证原件" note={`公示至 ${f.dateTime(history.data?.publicity?.close)}`} />}
            <EvidenceFiles items={extraFiles} />
          </section>}
          {history.data?.events.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>暂无审核或申诉记录。</div>}
          {history.data?.events.map((event, index) => (
            <div key={`${event.at}-${event.kind}-${index}`} style={{ borderLeft: '2px solid var(--line)', padding: '0 0 16px 14px', marginLeft: 4 }}>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px 12px', alignItems: 'baseline', paddingBottom: 6 }}>
                <span style={{ fontSize: 13, fontWeight: 600 }}>{LABELS[event.kind] ?? '处理记录'}{event.round > 0 && ` · 第 ${event.round} 轮`}</span>
                <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{f.dateTime(event.at)} · {STATUSES[event.status] ?? '已记录'}{event.superseded && ' · 已被后续审核替代'}</span>
                {event.score !== null && <span style={{ fontSize: 13, marginLeft: 'auto', fontVariantNumeric: 'tabular-nums' }}>{event.beforeScore !== null && `${f.score(event.beforeScore)} → `}{f.score(event.score)} 分</span>}
              </div>
              {audience === 'student'
                ? <div style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: 13, lineHeight: 1.8, color: 'var(--fg2)' }}>{event.reason}</div>
                : <RichText value={event.reason} evidence={files} />}
            </div>
          ))}
        </>}
    </section>
  )
}
