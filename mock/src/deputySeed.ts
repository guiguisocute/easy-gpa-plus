import type { Appeal, ClassificationSuggestion, Evidence, NamedReview, Submission } from '@/api/types'
import type { CategoryKey, SchemeConfig } from '@/lib/types'
import type { Store } from './db'

type Snapshot = (scheme: SchemeConfig, category: CategoryKey, itemKey: string) => Submission['ruleSnapshot']

/** 六个页签共用班管本人的业务记录；普通学生样例仍留在原队列中。 */
export function seedDeputyCases(store: Store, snapshot: Snapshot) {
  const subject = store.users.find((user) => user.id === 'u-wang')!
  const reviewers = ['u-li', 'u-zhou'].map((id) => store.users.find((user) => user.id === id)!)
  const createdAt = '2026-08-27T08:00:00.000Z'
  const reviewedAt = '2026-08-28T10:00:00.000Z'
  const evidence = (id: string, name: string): Evidence => ({
    id, name, mediaType: 'image/png', sizeBytes: 120_000, sha256: null, status: 'ready', uploadedAt: createdAt,
  })
  const review = (submissionId: string, index: number, score: number, reason: string, decision: NamedReview['decision'] = 'accepted'): NamedReview => ({
    id: `rv-${submissionId}-${index + 1}`,
    reviewerId: reviewers[index].id,
    reviewer: reviewers[index].name,
    reviewerSid: reviewers[index].sid,
    decision, score, reason, spentSeconds: 140 + index * 40, at: reviewedAt,
  })
  const submission = (
    id: string, category: CategoryKey, itemKey: string, title: string,
    claim: Submission['claim'], score: number, status: Submission['status'] = 'scored', owner = subject,
  ): Submission => {
    const ruleSnapshot = snapshot(store.scheme, category, itemKey)
    const row: Submission = {
      id, student: owner.name, studentId: owner.sid, category,
      categoryName: ruleSnapshot.categoryName, itemKey, itemName: ruleSnapshot.item.name,
      title, claim, wantScore: score, finalScore: status === 'arbitrating' ? null : score,
      status, submittedAt: createdAt, updatedAt: reviewedAt, ruleSnapshot,
      evidenceCount: 1, note: '已提交原始材料，请按本学年细则核对。', source: 'manual',
      appealsUsed: 0, canAppeal: status === 'scored',
    }
    store.submissions.push(row)
    store.evidence[id] = [evidence(`e-${id}`, `${title}证明.png`)]
    store.reviews[id] = reviewers.map((_, index) => review(id, index, score, '已核对证明材料与申报档位。'))
    store.assignments.push(...reviewers.map((who, index) => ({ id: `as-${id}-${index + 1}`, submissionId: id, reviewerId: who.id, decided: true })))
    return row
  }

  const conflict = submission('s-wang-conflict', 'moral', 'moral_cadre', '担任班级团支书的任职认定', { option: '班长、团支书、辅导员助理、新生助导员', score: 8 }, 8, 'arbitrating')
  store.reviews[conflict.id] = [
    review(conflict.id, 0, 8, '任命名单显示本学年担任团支书，按对应档位认定。'),
    review(conflict.id, 1, 4, '任命从下学期开始，是否按半学年折算仍有分歧。', 'adjusted'),
  ]

  const appealed = submission('s-wang-appeal', 'moral', 'moral_dorm', '校级文明寝室认定申诉', { option: '校级文明寝室', score: 3 }, 3, 'appealing')
  appealed.finalScore = 2
  appealed.appealsUsed = 1
  store.reviews[appealed.id] = reviewers.map((_, index) => review(appealed.id, index, 2, '初审时仅有院级公示，按院级文明寝室认定。', 'adjusted'))
  const appeal: Appeal = {
    id: 'ap-wang-escalated', targetType: 'submission', targetId: appealed.id,
    target: appealed.title, category: appealed.category, itemKey: appealed.itemKey,
    student: subject.name, studentId: subject.sid,
    reason: '补交了校级文明寝室正式公示，我的寝室在名单中，请将原先认定的院级档调整为校级档。',
    round: 1, status: 'escalated', baselineScore: 2, currentScore: 2, proposedScore: 3,
    resolutionScore: null, resolutionReason: null,
    originalCategory: appealed.category, originalItemKey: appealed.itemKey,
    handlers: reviewers.map((who, index) => ({
      id: who.id, name: who.name, sid: who.sid, decided: true, mine: false,
      rereview: {
        decision: index === 0 ? 'uphold' : 'adjust', score: index === 0 ? 2 : 3,
        reason: index === 0 ? '补交名单未写获评学年，暂维持原判。' : '公示日期与学院通知互相对应，应按本学年校级档认定。',
        spentSeconds: 180, at: reviewedAt,
      },
    })),
    createdAt, updatedAt: reviewedAt, resolvedAt: null,
  }
  store.appeals.unshift(appeal)
  store.evidence[appeal.id] = [evidence('e-ap-wang-escalated', '校级文明寝室正式公示.png')]

  const within = submission('s-wang-class-within', 'moral', 'moral_news_photo', '学院迎新新闻报道写稿一篇', { quantity: 1 }, 0.25)
  const cross = submission('s-wang-class-cross', 'moral', 'moral_organize', '校运会方阵带训及参与', { quantity: 4 }, 4, 'arbitrating')
  const reported = submission('s-wang-report', 'moral', 'moral_volunteer', '社区志愿服务十二小时', { quantity: 12 }, 3)
  const classifications: ClassificationSuggestion[] = [
    {
      id: 'cs-wang-within', submissionId: within.id, scope: 'within_category', status: 'pending',
      fromCategory: within.category, fromItemKey: within.itemKey,
      toCategory: 'moral', toItemKey: 'moral_news_article',
      reason: '推送署名与学院记录显示承担的是写稿工作，建议从新闻拍照改为新闻写稿，并按对应单价认定。',
      source: 'item_review', createdAt: reviewedAt,
      studentId: subject.id, studentSid: subject.sid, student: subject.name, title: within.title, finalScore: within.finalScore,
    },
    {
      id: 'cs-wang-cross', submissionId: cross.id, scope: 'cross_category', status: 'pending',
      fromCategory: cross.category, fromItemKey: cross.itemKey,
      toCategory: 'health', toItemKey: 'health_sports_conversion',
      reason: '体育部证明记载的是大型体育活动带训与参与，应归身体心理素质，需同时裁定归类和认定分。',
      source: 'item_review', createdAt: reviewedAt,
      studentId: subject.id, studentSid: subject.sid, student: subject.name, title: cross.title, finalScore: null,
    },
  ]
  store.classificationSuggestions.push(...classifications)
  const student = store.users.find((user) => user.id === 'u-chen')!
  const studentClass = submission('s-chen-class-within', 'moral', 'moral_news_photo', '运动会新闻报道写稿一篇', { quantity: 1 }, 0.25, 'scored', student)
  store.classificationSuggestions.push({
    id: 'cs-chen-within', submissionId: studentClass.id, scope: 'within_category', status: 'pending',
    fromCategory: studentClass.category, fromItemKey: studentClass.itemKey,
    toCategory: 'moral', toItemKey: 'moral_news_article',
    reason: '本条工作为运动会新闻写稿，建议改到同大项的新闻写稿小项。',
    source: 'item_review', createdAt: reviewedAt,
    studentId: student.id, studentSid: student.sid, student: student.name, title: studentClass.title, finalScore: studentClass.finalScore,
  })

  const moral = store.scheme.categories.find((category) => category.key === 'moral')!
  const absence = moral.penaltyItems.find((item) => item.key === 'moral_pen_absence')!
  const appliance = moral.penaltyItems.find((item) => item.key === 'moral_pen_appliance')!
  const collective = moral.baseItems.find((item) => item.key === 'moral_base_collective')!
  store.objections.unshift({
    id: 'ob-wang-penalty', kind: 'penalty', studentUserId: subject.id, studentId: subject.sid, student: subject.name,
    category: moral.key, categoryName: moral.name, itemKey: absence.key, itemName: absence.name,
    targetId: null, currentScore: null, proposedScore: absence.per, quantity: 1,
    basis: '第八周班会缺勤一次，已核对签到表及请假登记，请按班级细则计入本学年扣分。',
    status: 'submitted', batchId: 'ob-batch-wang', proposerId: reviewers[1].id, proposer: reviewers[1].name,
    noteEvidence: [evidence('e-ob-wang-penalty', '第八周班会签到及请假登记.png')],
    decidedScore: null, decisionReason: null, createdAt, submittedAt: reviewedAt, decidedAt: null,
  })

  const reportCases = [
    {
      id: 'rp-wang-base', kind: 'base' as const, itemKey: collective.key, itemName: collective.name,
      targetId: null, currentScore: collective.full, quantity: null, proposedScore: collective.full - 2,
      basis: '本学年两次集体公益活动未到场，建议按参与记录核减这项基础分；一位复核人认为其中一次已经请假。',
      file: '班级公益活动参与记录.png',
    },
    {
      id: 'rp-wang-penalty', kind: 'penalty' as const, itemKey: appliance.key, itemName: appliance.name,
      targetId: null, currentScore: null, quantity: 1, proposedScore: appliance.per,
      basis: '寝室检查登记了违规电器一次，是否属于本人使用存在分歧，请结合宿管记录核实。',
      file: '寝室检查登记与整改记录.png',
    },
    {
      id: 'rp-wang-submission', kind: 'submission' as const, itemKey: reported.itemKey, itemName: reported.itemName,
      targetId: reported.id, currentScore: reported.finalScore, quantity: null, proposedScore: 2,
      basis: '志愿服务签到表只有八小时，提交的十二小时包含了四小时培训，培训是否计入服务时长存在分歧。',
      file: '社区志愿服务签到原件.png',
    },
  ]
  store.reports.unshift(...reportCases.map(({ file, ...entry }) => ({
    ...entry, studentUserId: subject.id, studentId: subject.sid, student: subject.name,
    category: moral.key, categoryName: moral.name, status: 'escalated' as const,
    finalScore: null, decisionReason: null, createdAt, decidedAt: null,
    reviewerIds: reviewers.map((who) => who.id),
    reviews: reviewers.map((who, index) => ({
      reviewerId: who.id, decision: index === 0 ? 'uphold' as const : 'reject' as const,
      score: index === 0 ? entry.proposedScore : entry.currentScore ?? 0,
      reason: index === 0 ? '记录与举报所述一致，建议采纳举报中的分值。' : '材料仍有疑点，建议维持原有认定。',
      spentSeconds: 160 + index * 30, at: reviewedAt,
    })),
    evidence: [evidence(`e-${entry.id}`, file)], mine: false,
  })))

  store.sealed[subject.id] = { sealed: true, sealedAt: createdAt, source: 'manual' }
  store.audit.unshift({
    id: 'au-deputy-seed', actorId: subject.id, actorRole: 'class_admin', actorSid: subject.sid, actor: subject.name,
    action: 'class.deputy_changed', resourceType: 'class', resourceId: String(subject.classId),
    before: { userId: null }, after: { userId: reviewers[0].id }, metadata: {},
    ip: null, userAgent: null, createdAt,
  })
}
