import type { AdjudicationPermission } from '@/lib/adjudication'
import type { Submission } from '@/api/types'
import type { CategoryKey } from '@/lib/types'
import { scoreBounds } from '@/lib/claim'
import { db, findItem, nid, nowIso, snapshot, userBySid, type DemoUser } from './db'
import { fail } from './errors'

const SELF_REASON = '不能终裁涉及自己的事项，请由副班管处理'

// 与真实副班管路由共用管理员处理逻辑，只开放已注册的仲裁接口。
export function deputyPath(method: string, path: string, actor: DemoUser | null): string {
  if (!actor) fail(401, 'unauthenticated', '请先登录')
  if (actor.role !== 'group' || !actor.isDeputy || actor.status !== 'active') {
    fail(403, 'deputy_required', '此页面仅限班管任命的副班管使用')
  }
  const suffix = path.slice('/review/deputy'.length)
  const allowed = method === 'GET'
    ? /^\/(submissions|classification-suggestions|appeals|objections|reports|review-progress\/issues)$/.test(suffix)
      || /^\/(appeals|final-scorecards)\/[^/]+$/.test(suffix)
    : method === 'POST' && (
      /^\/submissions\/[^/]+\/(arbitrate|force-reject|force-score|classification\/(suggest|resolve))$/.test(suffix)
      || /^\/(appeals|reports)\/[^/]+\/final$/.test(suffix)
      || /^\/objections\/([^/]+\/decide|decide-batch)$/.test(suffix)
      || /^\/review-progress\/issues\/[^/]+\/decide$/.test(suffix)
    )
  if (!allowed) fail(404, 'not_found', '副班管没有这项操作')
  return '/admin' + suffix
}

export function adjudicationPermission(actor: DemoUser, sid: string): AdjudicationPermission {
  const target = userBySid(sid)
  const self = actor.sid === sid
  return {
    canAdjudicate: !!target && actor.classId === target.classId && !self && (
      actor.role === 'class_admin' || (actor.role === 'group' && !!actor.isDeputy && target.role === 'class_admin')
    ),
    recusalReason: self ? SELF_REASON : '',
  }
}

export function requireAdjudicationRead(actor: DemoUser, sid: string, deputy: boolean) {
  const target = userBySid(sid)
  if (!target || target.classId !== actor.classId) fail(404, 'not_found', '班级成员不存在')
  if (deputy && target.role !== 'class_admin') {
    fail(403, 'deputy_scope', '副班管只能处理有关班管本人的仲裁与终裁事项')
  }
}

export function requireAdjudicationTarget(actor: DemoUser, sid: string) {
  requireAdjudicationRead(actor, sid, actor.role === 'group')
  if (actor.sid === sid) fail(403, 'avoid_self', SELF_REASON)
  if (!adjudicationPermission(actor, sid).canAdjudicate) fail(403, 'forbidden', '无权终裁这项事项')
}

export function adjudicationRows<T extends object>(rows: T[], actor: DemoUser, deputy: boolean, sid: (row: T) => string): (T & AdjudicationPermission)[] {
  return rows.filter((row) => {
    const target = userBySid(sid(row))
    return target?.classId === actor.classId && (!deputy || target.role === 'class_admin')
  }).map((row) => ({ ...row, ...adjudicationPermission(actor, sid(row)) }))
}

export function appendAudit(actor: DemoUser | null, action: string, resourceType: string, resourceId: string, before: unknown, after: unknown, metadata: Record<string, unknown> = {}) {
  db().audit.unshift({
    id: nid('audit'), actorId: actor?.id, actorRole: actor?.role ?? 'system',
    actorSid: actor?.sid ?? null, actor: actor?.name ?? null, action, resourceType, resourceId,
    before, after, metadata, ip: null, userAgent: 'EasyGPA Plus demo', createdAt: nowIso(),
  })
}

export function clearIneligibleDeputy(user: DemoUser) {
  // 重置密码只收回登录能力；角色或启用状态变化才撤销任命。
  if (!user.isDeputy || (user.role === 'group' && user.status === 'active')) return
  user.isDeputy = false
  appendAudit(null, 'user.deputy_revoked', 'user', user.id, { isDeputy: true }, { isDeputy: false }, {
    reason: 'member_eligibility_changed', role: user.role, status: user.status,
  })
}

export function decisionReason(value: unknown): string {
  const reason = typeof value === 'string' ? value.trim() : ''
  if (reason.length < 4 || reason.length > 5000) fail(422, 'reason_required', '处理理由须为 4—5000 字符')
  return reason
}

export function decisionScore(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) fail(422, 'score_invalid', '请填写有效的认定分')
  return value
}

export function applySubmissionDecision(sub: Submission, body: Record<string, unknown>, canAppeal = true) {
  if (sub.forceRejection) fail(409, 'submission_force_rejected', '该条目已被强制驳回，不能再次改分')
  if (sub.forcedScore) fail(409, 'submission_force_scored', '该条目已强制改分，请使用强制改分入口继续调整')
  const category = ((typeof body.category === 'string' && body.category) || sub.category) as CategoryKey
  const itemKey = (typeof body.itemKey === 'string' && body.itemKey) || sub.itemKey
  const target = findItem(db().scheme, category, itemKey)
  if (!target) fail(422, 'classification_invalid', '所选小项不存在')
  const rejected = body.decision === 'reject'
  const score = rejected ? 0 : decisionScore(body.score ?? sub.finalScore)
  const bounds = scoreBounds(target.item.scoreRule)
  if (!rejected && (score < bounds.lo || score > bounds.hi)) fail(422, 'score_invalid', '认定分不在小项允许的范围内')
  sub.filedCategory ??= sub.ruleSnapshot.categoryKey
  sub.filedItemKey ??= sub.ruleSnapshot.item.key
  sub.filedRuleSnapshot ??= sub.ruleSnapshot
  if (sub.category !== category || sub.itemKey !== itemKey) sub.ruleSnapshot = snapshot(db().scheme, category, itemKey)
  sub.category = category
  sub.categoryName = target.category.name
  sub.itemKey = itemKey
  sub.itemName = target.item.name
  sub.finalScore = score
  sub.status = 'scored'
  sub.canAppeal = canAppeal && sub.appealsUsed < 2
  sub.updatedAt = nowIso()
  for (const suggestion of db().classificationSuggestions) {
    if (suggestion.submissionId === sub.id && suggestion.status === 'pending') {
      suggestion.status = suggestion.toCategory === category && suggestion.toItemKey === itemKey ? 'accepted' : 'rejected'
    }
  }
}
