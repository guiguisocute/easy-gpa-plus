import type { ReportTargetDetail, ScoreHistory, ScoreHistoryEvent } from '@/api/types'
import { db, userById, type DemoUser } from './db'
import { fail } from './errors'

export function reportTargetDetail(id: string, actor: DemoUser): ReportTargetDetail {
  const report = db().reports.find(row => row.id === id)
  if (!report || !(actor.role === 'class_admin'
    || (actor.isDeputy && userById(report.studentUserId)?.role === 'class_admin' && report.status !== 'reviewing')
    || (report.studentUserId !== actor.id && report.reviewerIds.includes(actor.id)))) fail(404, 'not_found', '举报不存在')
  const result: ReportTargetDetail = { kind: report.kind, targetId: null, currentScore: null, recordedBasis: '', fullScore: null, perScore: null, evidence: [], submission: null }
  if (report.kind === 'submission') {
    const sub = db().submissions.find(row => row.id === report.targetId && row.studentId === report.studentId && row.finalScore != null && ['scored', 'locked', 'appealing', 'arbitrating'].includes(row.status))
    if (!sub) fail(404, 'not_found', '已定分材料不存在')
    result.targetId = sub.id
    result.currentScore = sub.finalScore
    result.evidence = (db().evidence[sub.id] ?? []).filter(file => file.status === 'ready')
    result.submission = { id: sub.id, title: sub.title, note: sub.note ?? '', claim: sub.claim, requestedScore: sub.wantScore, submittedAt: sub.submittedAt, ruleSnapshot: sub.ruleSnapshot, filedRuleSnapshot: sub.filedRuleSnapshot ?? sub.ruleSnapshot }
  } else {
    const category = db().scheme.categories.find(row => row.key === report.category)
    result.fullScore = report.kind === 'base' ? category?.baseItems.find(row => row.key === report.itemKey)?.full ?? null : null
    result.perScore = report.kind === 'penalty' ? category?.penaltyItems.find(row => row.key === report.itemKey)?.per ?? null : null
    const base = db().bases.find(row => row.userId === report.studentUserId && row.category === report.category && row.itemKey === report.itemKey && row.kind === report.kind && row.recorded)
    if (base) {
      result.targetId = `${base.userId}:${base.itemKey}`
      result.currentScore = base.score
      result.recordedBasis = base.basis
    }
  }
  return result
}

/** Same anonymous event shape as the real reviewer history; staged notes stay out. */
export function reportScoreHistory(kind: string, id: string): ScoreHistory {
  const sub = kind === 'submission' ? db().submissions.find(row => row.id === id && row.finalScore != null && ['scored', 'locked', 'appealing', 'arbitrating'].includes(row.status)) : null
  const base = kind !== 'submission' ? db().bases.find(row => `${row.userId}:${row.itemKey}` === id && row.kind === kind && row.recorded) : null
  if (!sub && !base) fail(404, 'not_found', '计分条目不存在')
  const events: ScoreHistoryEvent[] = []
  const add = (event: Pick<ScoreHistoryEvent, 'kind' | 'at' | 'status' | 'score' | 'reason'> & Partial<ScoreHistoryEvent>) => events.push({ beforeScore: null, round: 0, superseded: false, ...event })
  if (sub) {
    if (sub.source === 'admin_grant') add({ kind: 'admin_grant', at: sub.submittedAt ?? sub.updatedAt, status: 'scored', score: sub.wantScore, reason: sub.note ?? '' })
    for (const review of db().reviews[id] ?? []) add({ kind: 'review', at: review.at, status: review.decision, score: review.score, reason: review.reason })
  }
  const targetType = kind === 'submission' ? 'submission' : kind === 'base' ? 'base_score' : 'penalty_score'
  for (const appeal of db().appeals.filter(row => row.targetType === targetType && row.targetId === id && row.reason.trim())) {
    const common = { beforeScore: appeal.baselineScore, round: appeal.round }
    add({ ...common, kind: 'appeal_filed', at: appeal.createdAt, status: appeal.status, score: appeal.proposedScore, reason: appeal.reason })
    if (appeal.handlers.every(row => row.decided)) for (const handler of appeal.handlers) {
      const review = handler.rereview
      if (review) add({ ...common, kind: 'appeal_review', at: review.at, status: review.decision, score: review.score, reason: review.reason })
    }
    if (appeal.resolvedAt) add({ ...common, kind: appeal.status === 'final' ? 'appeal_final' : 'appeal_resolved', at: appeal.resolvedAt, status: appeal.status, score: appeal.resolutionScore, reason: appeal.resolutionReason ?? '' })
  }
  for (const objection of db().objections.filter(row => row.kind === kind && ['applied', 'adjusted', 'dismissed'].includes(row.status)
    && (sub ? row.targetId === id : row.studentUserId === base!.userId && row.category === base!.category && row.itemKey === base!.itemKey))) {
    if (objection.submittedAt) add({ kind: 'objection_filed', at: objection.submittedAt, status: objection.status, score: objection.proposedScore, beforeScore: objection.currentScore, reason: objection.basis })
    if (objection.decidedAt) add({ kind: 'objection_decided', at: objection.decidedAt, status: objection.status, score: objection.decidedScore, beforeScore: objection.currentScore, reason: objection.decisionReason ?? '' })
  }
  return { events: events.sort((a, b) => a.at.localeCompare(b.at)), evidence: sub ? (db().evidence[id] ?? []).filter(file => file.status === 'ready') : [] }
}
