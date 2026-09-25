import { useReportTarget, useScheme } from '@/api/queries'
import { REPORT_KIND_LABEL, type ReportTaskDetail } from '@/api/types'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { RichText } from '@/components/Markdown'
import { RuleCard } from '@/components/RuleCard'
import { ScoreHistory } from '@/components/ScoreHistory'
import { StudentNote } from '@/components/StudentNote'
import { Note, Row, Sub, TextBtn } from '@/components/ui'
import * as f from '@/lib/format'
import { findCurrentItem } from '@/lib/ruleDiff'

type Report = Pick<ReportTaskDetail, 'id' | 'kind' | 'categoryName' | 'itemName' | 'basis' | 'evidence' | 'currentScore' | 'proposedScore' | 'quantity'>

/** 举报复核与班管终裁共用审核台的原件、规则和匿名轨迹。 */
export function ReportMaterial({ report }: { report: Report }) {
  const query = useReportTarget(report.id)
  const scheme = useScheme()
  const target = query.data
  const submission = target?.submission
  const effective = submission?.ruleSnapshot
  const filed = submission?.filedRuleSnapshot ?? effective
  const reclassified = !!filed && !!effective && (filed.categoryKey !== effective.categoryKey || filed.item.key !== effective.item.key)
  const files = target?.evidence ?? []
  const currentScore = target?.currentScore ?? report.currentScore

  return <div style={{ display: 'flex', flexDirection: 'column', gap: 24, minWidth: 0 }}>
    <section aria-label="举报对象与类目">
      <Sub title="举报对象与类目" note={REPORT_KIND_LABEL[report.kind]} />
      <h2 style={{ fontSize: 20, lineHeight: 1.5, margin: '0 0 12px', overflowWrap: 'anywhere' }}>{submission?.title ?? report.itemName}</h2>
      {filed && <Row label="原始归类" value={`${filed.categoryName} / ${filed.item.name}`} />}
      <Row label={effective ? '有效归类' : '所属类目'} value={effective ? `${effective.categoryName} / ${effective.item.name}` : `${report.categoryName} / ${report.itemName}`} />
      <Row label={report.kind === 'penalty' ? '已记在册的扣分' : '当前认定分'} value={currentScore == null ? '这一项还没有记录' : `${f.score(currentScore)} 分`} />
      <Row label="举报主张" value={`${report.quantity == null ? '' : `${report.quantity} 次 · `}${f.score(report.proposedScore)} 分`} />
    </section>

    {query.isLoading ? <div className="load-bar"><span /></div>
      : query.isError ? <Note tone="bad">原始材料加载失败。<TextBtn onClick={() => void query.refetch()}>重试</TextBtn></Note>
        : submission && <section aria-label="被举报条目的原始附件">
          <Sub title="被举报条目的原始附件" note={`${files.length} 份 · 点击预览或下载`} />
          {files.length ? <EvidenceFiles items={files} /> : <Note>这条申报没有原始附件。</Note>}
        </section>}

    {report.evidence.length > 0 && <section aria-label="举报人附上的材料">
      <Sub title="举报人附上的材料" note="点击预览或下载 · 举报人匿名" />
      <EvidenceFiles items={report.evidence} />
    </section>}

    <section aria-label="举报说明">
      <Sub title="举报说明" note={report.evidence.length ? '举报人匿名' : '举报人匿名 · 未附材料'} />
      <div style={{ border: '1px solid var(--line)', borderLeft: '3px solid var(--red)', background: 'var(--sub)', padding: '15px 17px', fontSize: 13.5, lineHeight: 1.85, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{report.basis}</div>
    </section>

    {submission && <>
      <section aria-label="学生原始填报">
        <Sub title="学生原始填报" note={`提交于 ${f.dateTime(submission.submittedAt)}`} />
        <Row label="学生自报" value={f.claimText(submission.claim, filed?.item.scoreRule.type === 'per_unit' ? filed.item.scoreRule.unit : undefined)} />
        <Row label="学生期望分" value={submission.requestedScore == null ? '未填写' : `${f.score(submission.requestedScore)} 分`} />
        <div style={{ marginTop: 16 }}><StudentNote value={submission.note} evidence={files} /></div>
      </section>
      {effective && <section aria-label="申报评分规则">
        <RuleCard item={effective.item} categoryName={effective.categoryName} capturedAt={effective.capturedAt}
          currentItem={findCurrentItem(scheme.data, effective.item.key)}
          claim={reclassified ? undefined : submission.claim} requestedScore={reclassified ? undefined : submission.requestedScore} />
        {reclassified && filed && <details style={{ marginTop: 16 }}>
          <summary style={{ cursor: 'pointer', fontSize: 13, paddingBlock: 10 }}>查看原始归类规则与申报档位</summary>
          <RuleCard item={filed.item} categoryName={filed.categoryName} capturedAt={filed.capturedAt} claim={submission.claim} requestedScore={submission.requestedScore} />
        </details>}
      </section>}
    </>}

    {target && report.kind !== 'submission' && <section aria-label="基础分与扣分规则">
      <Sub title="计分规则与已有记录" />
      {target.fullScore != null && <Row label="基础项满分" value={`${f.score(target.fullScore)} 分`} />}
      {target.perScore != null && <Row label="每次扣分" value={`${f.score(target.perScore)} 分`} />}
      {target.recordedBasis ? <RichText value={target.recordedBasis} /> : <Note>这一项尚无调整记录。</Note>}
    </section>}

    {target && <ScoreHistory kind={report.kind} targetId={target.targetId} audience="reviewer" shownEvidenceIds={files.map(file => file.id)} />}
  </div>
}
