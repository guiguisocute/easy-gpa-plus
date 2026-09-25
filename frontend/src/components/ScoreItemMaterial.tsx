import { useAdminSubmissions, useScorecard, useScheme } from '@/api/queries'
import type { Evidence, Objection } from '@/api/types'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { RichText } from '@/components/Markdown'
import { RuleCard } from '@/components/RuleCard'
import { ScoreHistory } from '@/components/ScoreHistory'
import { StudentNote } from '@/components/StudentNote'
import { Note, Row, Sub, TextBtn } from '@/components/ui'
import * as f from '@/lib/format'
import { findCurrentItem } from '@/lib/ruleDiff'

interface Props {
  item: Pick<Objection, 'kind' | 'targetId' | 'studentUserId' | 'itemKey'> & { category: string }
  context: { title: string; note?: string; reason: string; evidenceTitle: string; evidence?: Evidence[] }
}

/** 提案与副班管台共用原件、规则和匿名计分轨迹。 */
export function ScoreItemMaterial({ item, context }: Props) {
  const isSubmission = item.kind === 'submission'
  const submissions = useAdminSubmissions({ id: item.targetId ?? undefined, page_size: 1 }, isSubmission && !!item.targetId)
  const scorecard = useScorecard(isSubmission ? null : item.studentUserId)
  const scheme = useScheme()
  const submission = submissions.data?.items.find(row => row.id === item.targetId)
  // 初次基础分/扣分提案没有 targetId，生效后按同一小项找到实际计分记录。
  const base = scorecard.data?.baseItems.find(row => row.kind === item.kind && row.category === item.category && row.itemKey === item.itemKey)
  const targetId = isSubmission ? item.targetId : base?.id ?? item.targetId
  const query = isSubmission ? submissions : scorecard
  const files = submission?.evidence ?? []
  const contextFiles = context.evidence ?? []
  const rule = submission?.ruleSnapshot

  return <div style={{ display: 'flex', flexDirection: 'column', gap: 24, minWidth: 0 }}>
    {isSubmission && <section aria-label="原始申报附件">
      <Sub title="原始申报附件" note="点击预览或下载" />
      {query.isLoading ? <div className="load-bar"><span /></div>
        : query.isError ? <Note tone="bad">原始材料加载失败。<TextBtn onClick={() => void query.refetch()}>重试</TextBtn></Note>
          : !submission ? <Note>原始申报当前不可读取。</Note>
            : files.length ? <EvidenceFiles items={files} /> : <Note>这条申报没有原始附件。</Note>}
    </section>}

    {contextFiles.length > 0 && <section aria-label={context.evidenceTitle}>
      <Sub title={context.evidenceTitle} note={`${contextFiles.length} 份 · 点击预览或下载`} />
      <EvidenceFiles items={contextFiles} />
    </section>}

    <section aria-label={context.title}>
      <Sub title={context.title} note={context.note} />
      <div style={{ border: '1px solid var(--line)', padding: '13px 15px' }}>
        <RichText value={context.reason} evidence={[...files, ...contextFiles]} />
      </div>
    </section>

    {submission && <section aria-label="学生原始填报">
      <Sub title="学生原始填报" note={`提交于 ${f.dateTime(submission.submittedAt)}`} />
      <Row label="申报标题" value={submission.title} />
      <Row label="学生期望分" value={f.score(submission.requestedScore)} />
      <Row label="当前认定分" value={f.score(submission.finalScore)} />
      <div style={{ marginTop: 16 }}><StudentNote value={submission.note} evidence={files} /></div>
    </section>}

    {rule && <section aria-label="申报评分规则">
      <RuleCard item={rule.item} categoryName={rule.categoryName} capturedAt={rule.capturedAt}
        currentItem={findCurrentItem(scheme.data, rule.item.key)}
        claim={submission?.claim} requestedScore={submission?.requestedScore} />
    </section>}

    {!isSubmission && <section aria-label="已有计分记录">
      <Sub title="已有计分记录" />
      {query.isLoading ? <div className="load-bar"><span /></div>
        : query.isError ? <Note tone="bad">计分记录加载失败。<TextBtn onClick={() => void query.refetch()}>重试</TextBtn></Note>
          : base ? <>
            <Row label="当前认定分" value={f.score(base.score)} />
            {base.fullScore != null && <Row label="基础项满分" value={f.score(base.fullScore)} />}
            {base.perScore != null && <Row label="每次扣分" value={f.score(base.perScore)} />}
          </> : <Note>这一项尚无计分记录。</Note>}
    </section>}

    <ScoreHistory kind={item.kind} targetId={targetId} audience="reviewer" shownEvidenceIds={[...files, ...contextFiles].map(file => file.id)} />
  </div>
}
