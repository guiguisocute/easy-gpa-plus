import type { AdjudicationHistoryDetail, HistoryKind } from '@/api/adjudicationHistory'
import { db, pageOf, userBySid, type DemoUser } from './db'
import { fail } from './errors'

const ACTIONS: Record<string, HistoryKind> = {
  'submission.arbitrated': 'arbitration', 'appeal.final': 'appeal',
  'objection.decided': 'objection', 'objection.applied': 'objection', 'objection.dismissed': 'objection',
  'report.finalized': 'report', 'scorecard_audit.flag_decided': 'scorecard',
  'classification.resolved': 'classification', 'submission.force_rejected': 'force_reject', 'submission.force_scored': 'force_score',
}
const object = (value: unknown): Record<string, unknown> => value && typeof value === 'object' ? value as Record<string, unknown> : {}
const text = (value: unknown) => typeof value === 'string' ? value : ''
const score = (value: unknown) => typeof value === 'number' ? value : null

export function adjudicationHistory(actor: DemoUser, query: URLSearchParams, id?: string) {
  const kind = query.get('kind') ?? ''
  if (kind && !Object.values(ACTIONS).includes(kind as HistoryKind)) fail(400, 'invalid_kind', '处理类型不正确')
  const from = query.get('from'), until = query.get('until')
  if ((from && Number.isNaN(Date.parse(from))) || (until && Number.isNaN(Date.parse(until))) || (from && until && Date.parse(from) >= Date.parse(until))) fail(400, 'invalid_time', '筛选时间不正确')
  const items = db().audit.filter((log) => log.actorId === actor.id && ACTIONS[log.action]).map((log): AdjudicationHistoryDetail => {
    const before = object(log.before), after = object(log.after), metadata = object(log.metadata)
    const appeal = log.resourceType === 'appeal' ? db().appeals.find((row) => row.id === log.resourceId) : undefined
    const objection = log.resourceType === 'objection' ? db().objections.find((row) => row.id === log.resourceId) : undefined
    const report = log.resourceType === 'report' ? db().reports.find((row) => row.id === log.resourceId) : undefined
    const issue = log.resourceType === 'scorecard_audit_flag' ? object(metadata.historySubject) : {}
    const submissionId = log.resourceType === 'submission' ? log.resourceId
      : text(metadata.submissionId) || (appeal?.targetType === 'submission' ? appeal.targetId : null)
        || (objection?.kind === 'submission' ? objection.targetId : null) || (report?.kind === 'submission' ? report.targetId : null) || text(issue.submissionId)
    const sub = db().submissions.find((row) => row.id === submissionId)
    const studentId = sub?.studentId ?? appeal?.studentId ?? objection?.studentId ?? report?.studentId ?? text(issue.studentId)
    const evidence = [...(db().evidence[sub?.id ?? ''] ?? []), ...(db().evidence[appeal?.id ?? ''] ?? []), ...(objection?.noteEvidence ?? []), ...(report?.evidence ?? [])]
    return {
      id: log.id, kind: ACTIONS[log.action], createdAt: log.createdAt, studentId,
      student: sub?.student ?? appeal?.student ?? objection?.student ?? report?.student ?? text(issue.student),
      title: sub?.title ?? appeal?.target ?? objection?.itemName ?? report?.itemName ?? (text(issue.title) || '整表问题'),
      category: text(after.category) || sub?.category || appeal?.category || objection?.category || report?.category || '',
      itemKey: text(after.itemKey) || sub?.itemKey || objection?.itemKey || report?.itemKey || '',
      beforeCategory: text(before.category), beforeItemKey: text(before.itemKey),
      beforeScore: Object.hasOwn(before, 'finalScore') ? score(before.finalScore) : Object.hasOwn(before, 'score') ? score(before.score) : appeal?.baselineScore ?? objection?.currentScore ?? report?.currentScore ?? null,
      score: score(after.finalScore ?? after.score), decision: text(after.status) || 'scored',
      reason: text(metadata.reason) || appeal?.resolutionReason || objection?.decisionReason || report?.decisionReason || '',
      sourceReason: appeal?.reason ?? objection?.basis ?? report?.basis ?? text(issue.reason),
      note: sub?.note ?? '', submissionId: sub?.id ?? null, appealId: appeal?.id ?? null,
      objectionId: objection?.id ?? null, reportId: report?.id ?? null,
      currentScore: sub?.finalScore ?? null, currentStatus: sub?.status ?? null,
      evidence: [...new Map(evidence.map((item) => [item.id, item])).values()],
      reviews: (db().reviews[sub?.id ?? ''] ?? []).map((review) => ({ reviewer: review.reviewer, stage: '原审核', decision: review.decision, score: review.score, reason: review.reason, at: review.at, superseded: false })),
    }
  }).filter((row) => !row.studentId || userBySid(row.studentId)?.classId === actor.classId)
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt) || b.id.localeCompare(a.id, undefined, { numeric: true }))
  if (id) {
    const item = items.find((row) => row.id === id)
    if (!item) fail(404, 'not_found', '处理记录不存在')
    return item
  }
  const student = (query.get('student') ?? '').trim().toLowerCase(), q = (query.get('q') ?? '').trim().toLowerCase()
  return pageOf(items.filter((row) => (!kind || row.kind === kind)
    && (!student || `${row.student} ${row.studentId}`.toLowerCase().includes(student))
    && (!q || `${row.title} ${row.reason}`.toLowerCase().includes(q))
    && (!from || Date.parse(row.createdAt) >= Date.parse(from)) && (!until || Date.parse(row.createdAt) < Date.parse(until))), query)
}
