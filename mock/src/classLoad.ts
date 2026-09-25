import type { Appeal, AuditEntry, Claim, Evidence, NamedReview, Objection, Submission } from '@/api/types'
import type { CategoryKey, SchemeConfig, SchemeItem } from '@/lib/types'

export interface LoadUser {
  id: string
  sid: string
  name: string
}

export interface LoadAssignment {
  id: string
  submissionId: string
  reviewerId: string
  decided: boolean
}

export const EXTRA_STUDENTS: { sid: string; name: string }[] = [
  { sid: '20240011', name: '林晓' },
  { sid: '20240012', name: '黄可' },
  { sid: '20240013', name: '郑岚' },
  { sid: '20240014', name: '何远' },
  { sid: '20240015', name: '罗宁' },
  { sid: '20240016', name: '谢知' },
  { sid: '20240017', name: '邓夏' },
  { sid: '20240018', name: '曹启' },
  { sid: '20240019', name: '韩越' },
  { sid: '20240020', name: '冯川' },
  { sid: '20240021', name: '蒋默' },
  { sid: '20240022', name: '蔡青' },
  { sid: '20240023', name: '彭舟' },
  { sid: '20240024', name: '董衡' },
  { sid: '20240025', name: '袁野' },
  { sid: '20240026', name: '吕安' },
  { sid: '20240027', name: '苏晴' },
  { sid: '20240028', name: '叶航' },
  { sid: '20240029', name: '潘予' },
  { sid: '20240030', name: '丁一' },
  { sid: '20240031', name: '任秋' },
  { sid: '20240032', name: '姚望' },
  { sid: '20240033', name: '傅南' },
  { sid: '20240034', name: '谭晓' },
  { sid: '20240035', name: '邹衡' },
  { sid: '20240036', name: '汪清' },
  { sid: '20240037', name: '钟夏' },
  { sid: '20240038', name: '戴远' },
]

export interface Catalog {
  category: CategoryKey
  itemKey: string
  title: (name: string) => string
  claim: Claim
  note: string
  file: string
}

/** 审核队列与实时成绩共用同一份申报目录。 */
export const CATALOG: Catalog[] = [
  {
    category: 'moral',
    itemKey: 'moral_cadre_class',
    title: (n) => `${n} · 班级宣传委员`,
    claim: { score: 4 },
    note: '本学年任宣传委员，负责班群通知与两次主题班会材料。2026 版细则把班委从固定 4 分改成由班级考评小组在 0—8 分内认定。',
    file: '班委分工表.png',
  },
  {
    category: 'moral',
    itemKey: 'moral_blood',
    title: () => '无偿献血一次',
    claim: { quantity: 1 },
    note: '2026 年 3 月在校献血车献血 200ml，献血证已上传。',
    file: '献血证.png',
  },
  {
    category: 'moral',
    itemKey: 'moral_dorm',
    title: () => '院级文明寝室',
    claim: { option: '院级文明寝室', score: 2 },
    note: '学院 4 月公示，寝室号已在截图中标出。',
    file: '文明寝室公示.png',
  },
  {
    category: 'moral',
    itemKey: 'moral_volunteer',
    title: () => '志愿服务 6 小时',
    claim: { quantity: 6 },
    note: '- 迎新引导 2 小时\n- 图书馆值班 2 小时\n- 社区清洁 2 小时',
    file: '志愿工时表.pdf',
  },
  {
    category: 'moral',
    itemKey: 'moral_volunteer',
    title: () => '校运会志愿者 8 小时',
    claim: { quantity: 8 },
    note: '校运会检录处两天共 8 小时，签到表与任务安排已附。',
    file: '校运会志愿者签到.png',
  },
  {
    category: 'moral',
    itemKey: 'moral_organize',
    title: () => '组织班级篮球赛',
    claim: { quantity: 2 },
    note: '组织策划与现场执裁各计一次，未同时以参赛加分。',
    file: '活动策划与照片.pdf',
  },
  {
    category: 'moral',
    itemKey: 'moral_public_good',
    title: () => '社区公益活动表现突出',
    claim: { option: '表现突出', score: 1 },
    note: '寒假社区敬老服务，居委会出具了表现突出的证明。',
    file: '社区证明.png',
  },
  {
    category: 'moral',
    itemKey: 'moral_audience',
    title: () => '受委派听讲座 4 人次',
    claim: { quantity: 4 },
    note: '四次均为学院指定观众，有签到。',
    file: '讲座签到表.png',
  },
  {
    category: 'moral',
    itemKey: 'moral_news_photo',
    title: () => '院活动新闻拍照 3 次',
    claim: { quantity: 3 },
    note: '三篇推送均有署名拍照。',
    file: '推送截图.png',
  },
  {
    category: 'moral',
    itemKey: 'moral_news_article',
    title: () => '院活动新闻写稿 2 次',
    claim: { quantity: 2 },
    note: '两篇学院公众号稿件，作者栏已圈出。',
    file: '稿件截图.png',
  },
  {
    category: 'moral',
    itemKey: 'moral_practice_team',
    title: () => '暑期三下乡队员',
    claim: { option: '队员', score: 5 },
    note: '学院组织的暑期社会实践队，结项证书已传。',
    file: '三下乡结项证书.pdf',
  },
  {
    category: 'practice',
    itemKey: 'practice_base',
    title: () => '课外学术科技活动两项',
    claim: { score: 28 },
    note: '蓝桥杯省赛报名入围 + 学院大创立项，佐证各一份。未申报不计基础分。',
    file: '科创材料.zip',
  },
  {
    category: 'practice',
    itemKey: 'practice_contest',
    title: () => '数学建模校赛二等奖',
    claim: { option: '校级·二等', score: 6 },
    note: '三人队，本人第二作者，证书已传。',
    file: '数模获奖证书.png',
  },
  {
    category: 'practice',
    itemKey: 'practice_contest',
    title: () => '程序设计院赛三等奖',
    claim: { option: '院级·三等', score: 1 },
    note: '学院 ACM 选拔赛。',
    file: '院赛公示.png',
  },
  /* 2026 版新增的 A1／A2／A3／B 赛事分类：国家级和省级各摆一条，
     审核台上才看得出「同样是一等奖，A1 给 30、A3 给 8」这件事。 */
  {
    category: 'practice',
    itemKey: 'practice_contest',
    title: () => 'ICPC 亚洲区域赛银奖（国家级 A1 类）',
    claim: { option: '国家级 A1 类·二等', score: 20 },
    note: '教务处 A1 类目录内赛事，银奖按二等认定。队内排名第一，获奖名单已圈出。',
    file: 'ICPC获奖名单.pdf',
  },
  {
    category: 'practice',
    itemKey: 'practice_contest',
    title: () => '蓝桥杯省赛一等奖（省级 A3 类）',
    claim: { option: '省级 A3 类·一等', score: 8 },
    note: '按教务处最新目录属 A3 类，省赛一等。证书与目录截图各一份。',
    file: '蓝桥杯证书.png',
  },
  {
    category: 'practice',
    itemKey: 'practice_contest',
    title: () => '美赛 H 奖（按省级一等认定）',
    claim: { option: '省级 A1 类·一等', score: 15 },
    note: '美国大学生数学建模竞赛 H 奖，细则明确按省级一等认定。三人队，本人排名第一。',
    file: '美赛成绩单.pdf',
  },
  {
    category: 'practice',
    itemKey: 'practice_skill_contest',
    title: () => '学院演讲比赛二等奖',
    claim: { option: '院级·二等奖', score: 1 },
    note: '学院团委主办。',
    file: '演讲比赛证书.png',
  },
  {
    category: 'practice',
    itemKey: 'practice_cet',
    title: () => '大学英语四级',
    claim: { option: '四级', score: 5 },
    note: '成绩单截图，姓名与准考证号可见。',
    file: '四级成绩单.png',
  },
  {
    category: 'practice',
    itemKey: 'practice_cert',
    title: () => '计算机技术与软件专业技术资格（初级）',
    claim: { quantity: 1 },
    note: '软考初级证书，国家行政机关颁发。',
    file: '软考证书.png',
  },
  {
    category: 'health',
    itemKey: 'health_contest',
    title: () => '校运会 4×100 第五名',
    claim: { option: '校级·第五名', score: 4 },
    note: '集体项目主力棒。秩序册已圈名次。',
    file: '校运会秩序册.png',
  },
  {
    category: 'health',
    itemKey: 'health_contest',
    title: () => '学院羽毛球单打第三名',
    claim: { option: '院级·第三名', score: 2 },
    note: '学院体协杯。',
    file: '羽毛球奖状.png',
  },
  {
    category: 'health',
    itemKey: 'health_performance',
    title: () => '迎新晚会节目',
    claim: { option: '院级（按第六名标准）', score: 2 },
    note: '合唱节目，节目单与现场照。',
    file: '迎新晚会节目单.png',
  },
  /* 2026 版新增的跨大项折算：运动会的参与／带训时长记到身体心理素质，
     不记到思想道德的「组织活动」。演示里要有一条，否则这条规则只存在于说明文字里。 */
  {
    category: 'health',
    itemKey: 'health_sports_conversion',
    title: () => '校运会班级带训 12 小时',
    claim: { score: 3 },
    note: '春季运动会赛前带训，出勤表 12 次。已在思想道德「组织活动」里撤回，不重复报。',
    file: '带训出勤表.png',
  },
]

const LI = 'u-li'
const ZHOU = 'u-zhou'
/* 班级管理员默认也在审核池里（后端分发的 role IN ('group','class_admin')），
   所以每三条里有一条的第二位审核人是班管本人，分发页上他才有真实的量。 */
const WANG = 'u-wang'
const PEERS = [
  { id: ZHOU, reviewer: '周复评', reviewerSid: '20240006' },
  { id: WANG, reviewer: '王管理', reviewerSid: '20240003' },
]

function iso(day: number, hour = 9) {
  return `2026-08-${String(day).padStart(2, '0')}T${String(hour).padStart(2, '0')}:16:00.000Z`
}

export function buildReviewLoad(opts: {
  scheme: SchemeConfig
  snapshot: (scheme: SchemeConfig, category: CategoryKey, itemKey: string) => Submission['ruleSnapshot']
  wantScore: (item: SchemeItem, claim: Claim) => number | null
  students: LoadUser[]
}): {
  submissions: Submission[]
  evidence: Record<string, Evidence[]>
  reviews: Record<string, NamedReview[]>
  assignments: LoadAssignment[]
  appeals: Appeal[]
  objections: Objection[]
  audit: AuditEntry[]
} {
  const { scheme, snapshot, wantScore, students } = opts
  const submissions: Submission[] = []
  const evidence: Record<string, Evidence[]> = {}
  const reviews: Record<string, NamedReview[]> = {}
  const assignments: LoadAssignment[] = []
  const appeals: Appeal[] = []
  const objections: Objection[] = []
  const audit: AuditEntry[] = []

  const laneOf = (i: number): 'consensus' | 'mine' | 'peer' | 'conflict' | 'scored' | 'draft' => {
    if (i < 8) return 'consensus'
    if (i < 36) return 'mine'
    if (i < 44) return 'peer'
    if (i < 48) return 'conflict'
    if (i < 68) return 'scored'
    return 'draft'
  }

  /* 已定分的那一段要够长：申诉只能挂在已定分的条目上，而演示要覆盖复评的每一种状态。 */
  const total = 72
  for (let i = 0; i < total; i++) {
    const stu = students[i % students.length]
    const cat = CATALOG[i % CATALOG.length]
    const foundSnap = snapshot(scheme, cat.category, cat.itemKey)
    const id = `s-r${String(i + 1).padStart(2, '0')}`
    const lane = laneOf(i)
    const submittedDay = 10 + (i % 15)
    const status =
      lane === 'draft'
        ? 'draft'
        : lane === 'scored'
          ? 'scored'
          : lane === 'conflict'
            ? 'arbitrating'
            : lane === 'consensus' || lane === 'peer'
              ? 'consensus'
              : 'pending'
    const want = wantScore(foundSnap.item, cat.claim)
    /* 认定分必须按方案规则算出来，不能拿 claim.quantity 顶替。
       按量小项的 quantity 是「几小时／几次」不是「几分」：「志愿服务 6 小时」
       实得 6×0.25=1.5 分，直接显示 6 就和右边那张规则卡自相矛盾——
       两位审核人给的 score 用的本来就是 want，只有这里没跟上。 */
    const finalScore = lane === 'scored' ? want : null
    const submittedAt = lane === 'draft' ? null : iso(submittedDay, 8 + (i % 6))
    const row: Submission = {
      id,
      student: stu.name,
      studentId: stu.sid,
      category: cat.category,
      categoryName: foundSnap.categoryName,
      itemKey: cat.itemKey,
      itemName: foundSnap.item.name,
      title: cat.title(stu.name),
      claim: cat.claim,
      wantScore: want,
      finalScore: typeof finalScore === 'number' ? Number(finalScore) : null,
      status,
      submittedAt,
      ruleSnapshot: { ...foundSnap, capturedAt: submittedAt ?? iso(12) },
      evidenceCount: lane === 'draft' ? 0 : 1,
      note: cat.note,
      source: i % 11 === 0 ? 'ai' : 'manual',
      appealsUsed: 0,
      canAppeal: lane === 'scored',
      updatedAt: submittedAt ?? iso(12),
    }
    submissions.push(row)

    if (lane !== 'draft') {
      evidence[id] = [
        {
          id: `e-${id}`,
          name: cat.file,
          mediaType: cat.file.endsWith('.pdf') ? 'application/pdf' : cat.file.endsWith('.zip') ? 'application/zip' : 'image/png',
          sizeBytes: 80_000 + i * 1200,
          sha256: null,
          status: 'ready',
          uploadedAt: submittedAt ?? iso(12),
        },
      ]
    }

    const asgLi = 4100 + i * 2
    const asgZhou = asgLi + 1
    const peer = PEERS[i % 3 === 2 ? 1 : 0]
    if (lane !== 'draft') {
      const liDecided = lane === 'peer' || lane === 'conflict' || lane === 'scored'
      const zhouDecided = lane === 'consensus' || lane === 'conflict' || lane === 'scored'
      assignments.push(
        { id: String(asgLi), submissionId: id, reviewerId: LI, decided: liDecided },
        { id: String(asgZhou), submissionId: id, reviewerId: peer.id, decided: zhouDecided },
      )
      const list: NamedReview[] = []
      if (liDecided) {
        const score = lane === 'conflict' ? Math.max(0, (want ?? 0) - 1) : (want ?? 0)
        list.push({
          id: `rv-${id}-li`,
          reviewer: '李审核',
          reviewerSid: '20240002',
          reviewerId: LI,
          decision: lane === 'conflict' ? 'adjusted' : 'accepted',
          score,
          reason: lane === 'conflict' ? '材料能对上，但等次按学院口径下调一档。' : '佐证与申报档一致，按期望分通过。',
          spentSeconds: 70 + (i % 40),
          at: iso(submittedDay + 1, 14),
        })
      }
      if (zhouDecided) {
        list.push({
          id: `rv-${id}-peer`,
          reviewer: peer.reviewer,
          reviewerSid: peer.reviewerSid,
          reviewerId: peer.id,
          decision: 'accepted',
          score: want ?? 0,
          reason: lane === 'consensus' ? '材料齐全，先交结论。' : '同意按材料认定。',
          spentSeconds: 50 + (i % 30),
          at: iso(submittedDay + 1, 16),
        })
      }
      if (list.length) reviews[id] = list
    }
  }

  /* 申诉时补传的佐证。学生发起申诉多半是因为"我还有一份材料你们没看到"，
     少了这一份，复评台和仲裁台上的「申诉时补传的佐证」永远是空的。 */
  const appealFiles = ['补充证明材料.pdf', '完整获奖名单.png', '主办方盖章说明.pdf', '现场照片补充.png', '成绩公示原件.png']

  /* 复评走到哪一步，决定了两个台面各自能看到什么。全班一起申诉时这几种状态是并存的，
     所以演示数据必须把它们摆齐——否则终裁页翻十条全是"尚未复评"，看不出这一页要干什么。

       fresh     两位复评人都还没交
       half      一位交了、一位没交（班管在这一步可以选择接管）
       split     两人交了但不一致 → 已升给班管终裁
       agreed    两人一致 → 一审自行结案，不需要班管签字
       closed    班管已经终裁，只读回放
       round2    学生用掉第二次机会，不再经复评，直接落到班管手上 */
  type AppealShape = 'fresh' | 'half' | 'split' | 'agreed' | 'closed' | 'round2'
  const SHAPES: AppealShape[] = ['split', 'split', 'half', 'half', 'agreed', 'agreed', 'closed', 'round2', 'round2', 'fresh', 'fresh', 'split', 'half']

  const REASONS = [
    '证书上的等次高于当前认定，申请按证书档计分。',
    '集体项目本人是主力队员，不该按减半算，请复评。',
    '这一项被记在了错的小项里，应该按技能类比赛认定。',
    '补传了主办方盖章的名单，原来那份看不出级别。',
    '同一场活动我只报了一次，请核对是不是和别人的记录搞混了。',
  ]

  const scored = submissions.filter((s) => s.status === 'scored')
  SHAPES.forEach((shape, i) => {
    const s = scored[i]
    if (!s) return
    const appealId = `ap-r${i + 2}`
    const base = s.finalScore ?? 0
    const want = base + (i % 3 === 0 ? 3 : 2)
    const day = 20 + (i % 5)
    evidence[appealId] = [
      {
        id: `e-${appealId}`,
        name: appealFiles[i % appealFiles.length],
        mediaType: appealFiles[i % appealFiles.length].endsWith('.pdf') ? 'application/pdf' : 'image/png',
        sizeBytes: 140_000 + i * 4200,
        sha256: null,
        status: 'ready',
        uploadedAt: iso(day, 10),
      },
    ]

    const rereview = (who: 'li' | 'zhou', decision: 'uphold' | 'adjust', score: number, reason: string, hour: number) => ({
      id: `rr-${appealId}-${who}`,
      decision,
      score,
      category: s.category,
      itemKey: s.itemKey,
      reason,
      spentSeconds: 120 + i * 17,
      at: iso(day + 1, hour),
    })
    const li = (r: ReturnType<typeof rereview> | null) => ({ id: LI, name: '李审核', sid: '20240002', decided: !!r, mine: false, rereview: r })
    const zhou = (r: ReturnType<typeof rereview> | null) => ({ id: ZHOU, name: '周复评', sid: '20240006', decided: !!r, mine: false, rereview: r })

    const upheld = rereview('li', 'uphold', base, '补传的材料还是看不出级别，公示名单里查不到，维持原判。', 15)
    const adjusted = rereview('zhou', 'adjust', want, '盖章的名单能对上，按学生主张改判。', 17)
    const bothUphold = [li(upheld), zhou(rereview('zhou', 'uphold', base, '同意维持，材料没有新的东西。', 17))]

    const handlers =
      shape === 'fresh' ? [li(null), zhou(null)]
      : shape === 'half' ? [li(upheld), zhou(null)]
      : shape === 'split' ? [li(upheld), zhou(adjusted)]
      : shape === 'agreed' || shape === 'closed' ? bothUphold
      : [] /* round2 不经复评 */

    const status =
      shape === 'fresh' || shape === 'half' ? 'reviewing'
      : shape === 'split' || shape === 'round2' ? 'escalated'
      : shape === 'agreed' ? 'resolved'
      : 'final'

    const resolved = shape === 'agreed' || shape === 'closed'
    appeals.push({
      id: appealId,
      targetType: 'submission',
      targetId: s.id,
      target: s.title,
      category: s.category,
      itemKey: s.itemKey,
      student: s.student,
      studentId: s.studentId,
      reason: REASONS[i % REASONS.length],
      round: shape === 'round2' ? 2 : 1,
      status,
      baselineScore: base,
      currentScore: resolved ? base : base,
      proposedScore: want,
      originalCategory: s.category,
      originalItemKey: s.itemKey,
      proposedCategory: s.category,
      proposedItemKey: s.itemKey,
      resolutionScore: resolved ? base : null,
      resolutionReason: shape === 'agreed'
        ? '两位复评人都认为原认定成立，本轮维持原判。'
        : shape === 'closed'
          ? '补传材料仍不能证明级别，维持原认定。学生的两次申诉机会到此用完。'
          : null,
      resolutionCategory: resolved ? s.category : undefined,
      resolutionItemKey: resolved ? s.itemKey : undefined,
      previousAppealId: shape === 'round2' ? `${appealId}-r1` : undefined,
      handlers,
      createdAt: iso(day, 10),
      updatedAt: iso(day + 1, 18),
      resolvedAt: resolved ? iso(day + 2, 11) : null,
    })

    /* 二次申诉要回放第一轮。把第一轮也造成一条真实存在的申诉记录，
       前端的 previousRound 才有东西可读，而不是凭空拼一段文字。 */
    if (shape === 'round2') {
      appeals.push({
        id: `${appealId}-r1`,
        targetType: 'submission',
        targetId: s.id,
        target: s.title,
        category: s.category,
        itemKey: s.itemKey,
        student: s.student,
        studentId: s.studentId,
        reason: '第一次申诉：证书等次和认定对不上，请再看一遍。',
        round: 1,
        status: 'resolved',
        baselineScore: base,
        currentScore: base,
        proposedScore: want,
        originalCategory: s.category,
        originalItemKey: s.itemKey,
        proposedCategory: s.category,
        proposedItemKey: s.itemKey,
        resolutionScore: base,
        resolutionReason: '两位复评人一致维持原判。',
        resolutionCategory: s.category,
        resolutionItemKey: s.itemKey,
        handlers: bothUphold,
        createdAt: iso(day - 6, 9),
        updatedAt: iso(day - 5, 16),
        resolvedAt: iso(day - 5, 16),
      })
    }

    s.status = resolved ? 'scored' : 'appealing'
    s.canAppeal = shape === 'agreed'
    s.appealsUsed = shape === 'round2' ? 2 : 1
  })

  const penaltyKeys = [
    { key: 'moral_pen_absence' as const, name: '考勤缺勤（每次；迟到早退累计 2 次记 1 次）', qty: 2, score: -2 },
    { key: 'moral_pen_appliance' as const, name: '违规使用大功率用电器（未受处分）', qty: 1, score: -3 },
    { key: 'moral_pen_notice_college' as const, name: '院级通报批评', qty: 1, score: -5 },
  ]
  for (let i = 0; i < 9; i++) {
    const stu = students[i]
    const p = penaltyKeys[i % penaltyKeys.length]
    objections.push({
      id: `ob-r${i + 2}`,
      kind: 'penalty',
      studentId: stu.sid,
      studentUserId: stu.id,
      student: stu.name,
      category: 'moral',
      categoryName: '思想道德素质',
      itemKey: p.key,
      itemName: p.name,
      targetId: null,
      currentScore: null,
      proposedScore: p.score,
      quantity: p.qty,
      basis: i % 2 === 0 ? '班委考勤表与辅导员确认记录一致。' : '宿舍检查记录，现场照片已作为提案附图。',
      status: i < 6 ? 'submitted' : 'draft',
      batchId: i < 6 ? 'ob-batch-2' : null,
      proposer: '李审核',
      proposerId: LI,
      decidedScore: null,
      decisionReason: null,
      createdAt: iso(18, 11),
      submittedAt: i < 6 ? iso(19, 9) : null,
      decidedAt: null,
    })
  }

  audit.push(
    {
      id: 'au-r1',
      actorRole: 'group',
      actorSid: '20240002',
      actor: '李审核',
      action: 'review.decision',
      resourceType: 'submission',
      resourceId: submissions.find((s) => s.status === 'scored')?.id ?? null,
      metadata: { decision: 'accepted' },
      ip: '10.145.0.2',
      userAgent: 'Mozilla/5.0',
      createdAt: iso(21, 15),
    },
    {
      id: 'au-r2',
      actorRole: 'class_admin',
      actorSid: '20240003',
      actor: '王管理',
      action: 'dispatch.run',
      resourceType: 'dispatch',
      resourceId: null,
      metadata: { assigned: assignments.length / 2 },
      ip: '10.145.0.2',
      userAgent: 'Mozilla/5.0',
      createdAt: iso(16, 9),
    },
    {
      id: 'au-r3',
      actorRole: 'group',
      actorSid: '20240006',
      actor: '周复评',
      action: 'review.decision',
      resourceType: 'submission',
      resourceId: submissions.find((s) => s.status === 'consensus')?.id ?? null,
      metadata: { decision: 'accepted' },
      ip: '10.145.0.2',
      userAgent: 'Mozilla/5.0',
      createdAt: iso(20, 16),
    },
  )

  return { submissions, evidence, reviews, assignments, appeals, objections, audit }
}

export function isOverdueSubmission(id: string) {
  const n = Number(id.replace('s-r', ''))
  return Number.isFinite(n) && n <= 36 && n % 7 === 1
}
