// Local demo state only. No request from this module reaches a production API.
import { db, nid, nowIso, persist, userById, DEMO_PASSWORD, type DemoUser } from './db'
import { fail } from './errors'
import type { GovernanceProposal, GovernanceState } from '@/api/governance'
import type { Evidence, RuleSnapshot } from '@/api/types'
import { scoreBounds } from '@/lib/claim'

export const MOCK_GOVERNANCE_EXECUTION = Symbol('mock-governance-execution')
type Body = Record<string, unknown>
type Member = { joinedAt: string; leftAt: string | null; reviewer: boolean }
type Opinion = {
  score: number
  decision: string
  category: string
  itemKey: string
  reason: string
}
type Seat = {
  choice?: string
  locked?: boolean
  opinion?: Opinion
  candidate?: string
}
interface Proposal extends GovernanceProposal {
  author: string
  request: string
  snapshot: string
  voters: Record<string, Seat>
  comments: {
    id: string
    body: string
    kind: string
    createdAt: string
    actor: string
  }[]
  subject?: string
  target?: string
  round: number
  executed?: boolean
}
export interface MockGovernance {
  mode: GovernanceState['config']['mode']
  version: number
  enrollmentCloseAt: string | null
  members: Record<string, Member>
  proposals: Proposal[]
  scenario: boolean
  uploads?: Record<string, { owner: string; files: Evidence[] }>
}
const day = 86_400_000
const iso = (offset = 0) => new Date(Date.now() + offset).toISOString()
const roster = () =>
  db().users.filter((u) => u.role !== 'ops' && u.status === 'active')
const state = (): MockGovernance =>
  (db().governance ??= {
    mode: 'centralized',
    version: 0,
    enrollmentCloseAt: null,
    members: {},
    proposals: [],
    scenario: false,
  })
const eligible = (u: DemoUser) =>
  !!u.registered &&
  u.status === 'active' &&
  !!state().members[u.id] &&
  !state().members[u.id].leftAt &&
  Date.parse(state().members[u.id].joinedAt) <= Date.now() - day
const electorate = () => roster().filter(eligible)
const required = (kind: string, count: number) =>
  kind === 'protected'
    ? Math.max(5, Math.ceil((2 * count) / 3), Math.ceil(roster().length / 3))
    : kind === 'activate'
      ? Math.max(3, Math.ceil((2 * count) / 3))
      : Math.max(3, Math.floor(count / 2) + 1)
const isCase = (p: Proposal) => p.kind === 'review' || p.kind === 'appeal'
const canRead = (p: Proposal, u: DemoUser) =>
  !isCase(p) || p.subject === u.id || !!p.voters[u.id]
function needMember(u: DemoUser) {
  if (!eligible(u))
    fail(403, 'membership_required', '请主动加入共治并等待 24 小时')
}
const fingerprint = () =>
  JSON.stringify({
    roster: roster()
      .map((u) => u.id)
      .sort(),
    scheme: db().scheme,
    timeline: db().timeline,
  })
const candidate = (opinion: Opinion) =>
  JSON.stringify([
    opinion.decision,
    opinion.score,
    opinion.category,
    opinion.itemKey,
  ])
export function governanceMode() {
  return state().mode
}
/* 演示站首次进入直接落在普通模式的登录页，不再先弹模式选择。
   写入的状态与初始化班管「选择普通模式」相同，共治模式仍从顶部入口进入。 */
export function startCentralizedDemo() {
  const s = state()
  if (db().demoMode || s.version > 0) return
  db().demoMode = 'centralized'
  s.version = 1
  persist()
}
export function governanceHelper(
  method: string,
  path: string,
  u: DemoUser,
): string | null {
  const exact: Record<string, string> = {
    'GET /governance/students': '/review/students',
    'GET /governance/bonus-grants': '/admin/bonus-grants',
    'GET /governance/gpa': '/admin/gpa',

    'GET /governance/objections': '/review/objections',
    'POST /governance/objections': '/review/objections',
  }
  let mapped = exact[`${method} ${path}`]
  if (
    /^\/governance\/students\/[^/]+\/scorecard$/.test(path) &&
    method === 'GET'
  )
    mapped = path.replace('/governance/', '/review/')
  if (/^\/governance\/score-history\//.test(path) && method === 'GET')
    mapped = path.replace('/governance/', '/review/')

  if (/^\/governance\/objections\//.test(path))
    mapped = path.replace(
      '/governance/',
      path.includes('/notes/') ? '/' : '/review/',
    )
  if (!mapped) return null
  if (state().mode !== 'collective')
    fail(403, 'collective_required', '本班未启用共治')
  needMember(u)
  return mapped
}

/** 闸门挂在 business 组上（router.go），不要求加入共治，任何班级成员都读得到。 */
export function governanceOpenHelper(method: string, path: string): string | null {
  if (method !== 'GET' || path !== '/governance/gate') return null
  if (state().mode !== 'collective')
    fail(403, 'collective_required', '本班未启用共治')
  return '/admin/gate'
}
function visible(p: Proposal, u: DemoUser) {
  const {
    author: _author,
    request: _request,
    snapshot: _snapshot,
    voters,
    comments: _comments,
    subject: _subject,
    target: _target,
    round: _round,
    executed: _executed,
    ...proposal
  } = p
  const payload =
    p.action === 'gpa' && !voters[u.id] && p.author !== u.id ? {} : p.payload
  const counted = Object.values(voters).filter((v) => v.choice || v.opinion)
  const tally = Object.fromEntries(
    ['yes', 'no', 'abstain'].map((key) => [
      key,
      Object.values(voters).filter((v) => v.choice === key).length,
    ]),
  )
  return {
    proposal: { ...proposal, payload },
    eligible: !!voters[u.id],
    myChoice: voters[u.id]?.choice ?? null,
    mySubmitted: p.status === 'deliberating' ? !!voters[u.id]?.candidate : !!(voters[u.id]?.opinion || voters[u.id]?.choice),
    participated: counted.length,
    evidence:
      voters[u.id] || p.author === u.id
        ? (state().uploads?.[String(p.payload.uploadId)]?.files ?? [])
        : [],
    summary: p.action === 'timeline' ? JSON.stringify(payload) : undefined,
    ...(!['discussion', 'voting'].includes(p.status) && !isCase(p)
      ? { tally }
      : {}),
  }
}
function newProposal(u: DemoUser, b: Body): Proposal {
  let kind = String(b.kind),
    action = String(b.action)
  const payload = structuredClone((b.payload ?? {}) as Body)
  let voters = electorate()
  if (action === 'bonus') {
    const ids = Array.isArray(payload.studentIds)
      ? payload.studentIds.map(String)
      : []
    if (
      !ids.length ||
      new Set(ids).size !== ids.length ||
      ids.some((id) => !roster().some((u) => u.id === id))
    )
      fail(422, 'bonus_members', '请选择有效受益成员')
    if (ids.length === roster().length) kind = 'protected'
    else voters = voters.filter((u) => !ids.includes(u.id))
  }
  if (action === 'gpa' || action === 'export')
    voters = voters.filter((v) => v.id !== u.id)
  const yes = required(kind, voters.length)
  if (yes > voters.length)
    fail(
      409,
      'quorum_unreachable',
      `目前只有 ${voters.length} 名有效参与者，本事项需要 ${yes} 票，不能降低门槛`,
    )
  const wait = ['protected', 'activate'].includes(kind) ? 2 * day : day
  return {
    id: nid('governance'),
    kind,
    action,
    title: String(b.title),
    body: String(b.body),
    payload,
    status: 'discussion',
    electorateCount: voters.length,
    rosterCount: roster().length,
    requiredYes: yes,
    opensAt: iso(wait),
    closesAt: iso(
      wait + (kind === 'ordinary' || kind === 'bonus' ? 2 : 3) * day,
    ),
    author: u.id,
    request: JSON.stringify(b),
    snapshot: fingerprint(),
    voters: Object.fromEntries(voters.map((u) => [u.id, {}])),
    comments: [],
    round: 1,
  }
}
function seedDemo(u: DemoUser) {
  const s = state()
  for (let i = 0; roster().length < 40 || i < 2; i++) {
    const id = `u-gov-${i}`,
      registered = i < 2
    if (userById(id)) continue
    const member: DemoUser = {
      ...u,
      id,
      sid: `20249${String(i).padStart(3, '0')}`,
      name: registered ? `共治志愿者${i + 1}` : `在册成员${i + 1}`,
      role: 'student',
      isDeputy: false,
      initial: '共',
      registered,
      password: registered ? DEMO_PASSWORD : '',
      primaryEmail: null,
    }
    db().users.push(member)
    db().whitelist.push({
      id: nid('wl'),
      sid: member.sid,
      name: member.name,
      role: 'student',
      active: true,
      registered,
      registeredAt: registered ? nowIso() : null,
      createdAt: nowIso(),
    })
  }
  const volunteers = roster().filter(
    (v) => v.id.startsWith('u-gov-') && v.registered,
  )
  const optins = [
    u,
    ...volunteers,
    ...roster().filter(
      (v) => v.registered && v.id !== u.id && !volunteers.includes(v),
    ),
  ].slice(0, 12)
  s.mode = 'collective'
  s.version++
  s.scenario = true
  s.members = Object.fromEntries(
    optins.map((v) => [
      v.id,
      { joinedAt: iso(-2 * day), leftAt: null, reviewer: true },
    ]),
  )
  s.proposals = []
  const p = newProposal(u, {
    requestId: crypto.randomUUID(),
    kind: 'ordinary',
    action: 'motion',
    title: '演示：统一整理活动证明的文件命名',
    body: '这是预置的演示表决。已有六名虚构成员赞成，你可以投出自己的选择；沉默不会被计为同意。',
    payload: {},
  })
  p.opensAt = iso(-day)
  p.closesAt = iso(day)
  p.status = 'voting'
  optins
    .filter((v) => v.id !== u.id)
    .slice(0, 6)
    .forEach((v) => {
      p.voters[v.id] = { choice: 'yes', locked: true }
    })
  s.proposals.push(p)
  const sub =
    db().submissions.find(
      (v) =>
        ![u.id, u.sid].includes(v.studentId) &&
        v.ruleSnapshot.item.scoreRule.type === 'free',
    ) ?? db().submissions.find((v) => ![u.id, u.sid].includes(v.studentId))
  if (sub) {
    const subject =
      userById(sub.studentId) ?? db().users.find((v) => v.sid === sub.studentId)
    const second = optins.find((v) => v.id !== u.id && v.id !== subject?.id)!
    const panel = {
      ...newProposal(u, {
        kind: 'ordinary',
        action: 'motion',
        title: '演示：两名成员独立认定活动分',
        body: '根据材料和现有规则独立给分，分歧时随机增补其他评审。',
        payload: {},
      }),
      kind: 'review',
      action: 'submission',
      target: sub.id,
      subject: subject?.id,
      electorateCount: 2,
      requiredYes: 2,
      opensAt: iso(-day),
      closesAt: iso(2 * day),
      status: 'voting',
      voters: {
        [u.id]: {},
        [second.id]: {
          opinion: {
            score: sub.finalScore ?? sub.wantScore ?? 0,
            decision: 'accepted',
            category: sub.category,
            itemKey: sub.itemKey,
            reason: '演示意见：已按材料和规则核对。',
          },
        },
      },
    }
    s.proposals.unshift(panel)
  }
  return {
    notice: '已载入本机共治示例。全部成员与票数为演示数据，不影响生产。',
  }
}
type Execute = (
  action: string,
  payload: Body,
  target: string | undefined,
  author: string,
) => Promise<unknown>
async function execute(p: Proposal, run: Execute) {
  if (p.executed) return { status: p.status }
  if (p.snapshot !== fingerprint() && !isCase(p)) {
    p.status = 'stale'
    return {
      status: p.status,
      notice: '班级名单、方案或时间线已变化，请重新核对提案',
    }
  }
  if (p.action === 'activate') {
    state().mode = 'collective'
    state().version++
  } else if (p.action !== 'motion')
    await run(p.action, p.payload, p.target, p.author)
  p.status = 'applied'
  p.executed = true
  return { status: p.status, notice: '决议已执行' }
}
async function finishCase(p: Proposal, run: Execute) {
  const seats = Object.values(p.voters),
    opinions = seats.flatMap((v) => (v.opinion ? [v.opinion] : []))
  if (opinions.length < seats.length)
    return { status: p.status, notice: '等待其他评审独立提交' }
  const winner = opinions.find(
    (o) =>
      opinions.filter((v) => candidate(v) === candidate(o)).length >=
      p.requiredYes,
  )
  if (winner && p.status !== 'deliberating') {
    p.payload = {
      ...winner,
      decision: winner.decision === 'rejected' ? 'rejected' : 'adjust',
      action: 'adjust',
    }
    return execute(p, run)
  }
  if (p.status === 'deliberating') {
    const key = seats.find(
      (v) =>
        v.candidate &&
        v.candidate !== 'abstain' &&
        seats.filter((x) => x.candidate === v.candidate).length >=
          p.requiredYes,
    )?.candidate
    if (key) {
      const chosen = opinions.find((o) => candidate(o) === key)!
      p.payload = {
        ...chosen,
        decision: chosen.decision === 'rejected' ? 'rejected' : 'adjust',
        action: 'adjust',
      }
      return execute(p, run)
    }
    if (seats.every((v) => v.candidate)) {
      p.status = 'blocked'
      return { notice: '没有同一裁决形成多数，保留待解决状态' }
    }
    return { notice: '已记录本轮表决' }
  }
  const next = seats.length === 2 ? 3 : 5
  if (seats.length < 5) {
    const pool = electorate().filter(
      (u) =>
        state().members[u.id].reviewer && !p.voters[u.id] && u.id !== p.subject,
    )
    if (pool.length < next - seats.length + 5) {
      p.status = 'blocked'
      return { notice: '独立评审人数不足，保留原意见，等待自愿成员补齐' }
    }
    const random = new Uint32Array(pool.length)
    crypto.getRandomValues(random)
    pool
      .map((u, at) => ({ u, key: random[at] }))
      .sort((a, b) => a.key - b.key)
      .slice(0, next - seats.length)
      .forEach(({ u }) => {
        p.voters[u.id] = {}
      })
    p.electorateCount = next
    p.requiredYes = Math.floor(next / 2) + 1
    return { notice: '出现分歧，已随机增补评审，原意见保留' }
  }
  p.status = 'deliberating'
  p.closesAt = iso(2 * day)
  return { notice: '请阅读匿名意见后选择有依据的裁决方案' }
}
export async function mockGovernance(
  method: string,
  path: string,
  b: Body,
  u: DemoUser,
  run: Execute,
): Promise<unknown> {
  const s = state()
  if (u.role === 'ops') fail(403, 'class_required', '请使用班级成员体验共治')
  if (path.startsWith('/governance/bonus-grant-uploads')) {
    needMember(u)
    const uploads = (s.uploads ??= {})
    if (method === 'POST' && path === '/governance/bonus-grant-uploads') {
      const id = nid('upload')
      uploads[id] = { owner: u.id, files: [] }
      return { id }
    }
    const bits = path.split('/'),
      upload = uploads[bits[3]]
    if (!upload || upload.owner !== u.id)
      fail(403, 'upload_owner', '只能操作本人的佐证')
    if (
      s.proposals.some(
        (p) =>
          p.payload.uploadId === bits[3] &&
          !['rejected', 'stale'].includes(p.status),
      )
    )
      fail(409, 'proof_frozen', '提案原件已冻结')
    if (method === 'POST' && bits[5] === 'presign') {
      const id = nid('e')
      db().pendingUploads[id] = {
        id,
        kind: 'note',
        ownerId: bits[3],
        filename: String(b.filename),
        mediaType: String(b.mediaType),
        sizeBytes: Number(b.sizeBytes),
      }
      return {
        evidenceId: id,
        uploadUrl: '/mock-upload',
        uploadFields: { key: id },
        expiresIn: 600,
        method: 'POST',
      }
    }
    const id = bits[5],
      pending = db().pendingUploads[id]
    if (method === 'POST' && bits[6] === 'complete' && pending) {
      upload.files.push({
        id,
        name: pending.filename,
        mediaType: pending.mediaType,
        sizeBytes: pending.sizeBytes,
        sha256: null,
        status: 'ready',
        uploadedAt: nowIso(),
      })
      db().evidence[bits[3]] = upload.files
      delete db().pendingUploads[id]
      return {
        evidenceId: id,
        filename: pending.filename,
        mediaType: pending.mediaType,
        status: 'ready',
      }
    }
    if (method === 'DELETE') {
      upload.files = upload.files.filter((f) => f.id !== id)
      db().evidence[bits[3]] = upload.files
      delete db().pendingUploads[id]
      return {}
    }
    fail(404, 'upload_missing', '上传不存在')
  }
  if (method === 'POST' && path === '/governance/demo') {
    if (u.role !== 'class_admin') fail(403, 'forbidden', '首次配置由班级管理员完成')
    if (db().demoMode || s.version > 0) fail(409, 'mode_chosen', '本次演示已选择工作方式；重置演示后才能重新选择')
    if (!['centralized', 'collective'].includes(String(b.mode))) fail(422, 'invalid_mode', '请选择工作方式')
    if (b.mode === 'collective') {
      db().demoMode = 'collective'
      return seedDemo(u)
    }
    startCentralizedDemo()
    return { notice: '已进入普通模式，保留原有角色分工' }
  }
  if (method === 'POST' && path === '/governance/demo/advance') {
    if (!s.scenario) fail(409, 'demo_required', '先载入共治示例')
    // Moving existing timestamps is a demo-only shortcut; production has no such route.
    s.proposals.forEach((p) => {
      p.opensAt = new Date(Date.parse(p.opensAt) - 4 * day).toISOString()
      p.closesAt = new Date(Date.parse(p.closesAt) - 4 * day).toISOString()
    })
    Object.values(s.members).forEach((m) => {
      m.joinedAt = new Date(Date.parse(m.joinedAt) - 4 * day).toISOString()
    })
    return { notice: '已模拟经过四天，可检查投票结果；未代投任何票' }
  }
  if (method === 'GET' && path === '/governance') {
    const members = roster(),
      live = electorate(),
      reviewers = live.filter((u) => s.members[u.id].reviewer)
    return {
      config: {
        mode: s.mode,
        version: s.version,
        profile: s.mode === 'collective' ? 'standard' : null,
        enrollmentCloseAt: s.enrollmentCloseAt,
      },
      counts: {
        roster: members.length,
        registered: members.filter((u) => u.registered).length,
        submitted: new Set(
          db()
            .submissions.filter((v) => v.status !== 'draft' && ['manual', 'ai'].includes(v.source))
            .map((v) => v.studentId),
        ).size,
        enrolled: Object.values(s.members).filter((m) => !m.leftAt).length,
        electorate: live.length,
        reviewers: reviewers.length,
      },
      mine: {
        id: u.id,
        ...(s.members[u.id] ?? {
          joinedAt: null,
          leftAt: null,
          reviewer: false,
        }),
      },
      canConfigure:
        u.role === 'class_admin' &&
        !db().submissions.some((v) => v.status !== 'draft') &&
        (s.version === 0 || s.mode === 'enrolling'),
      ordinaryRequired: required('ordinary', live.length),
      protectedRequired: required('protected', live.length),
      capacityReason:
        reviewers.length < 7
          ? '至少需要 7 名生效评审成员；不要求提交材料。'
          : '',
      suggestedProfile: {
        name: reviewers.length < 11 ? 'compact' : 'standard',
        maximum: reviewers.length < 11 ? 3 : 5,
        appeal: reviewers.length < 11 ? 3 : 5,
      },
    }
  }
  if (method === 'PUT' && path === '/governance/config') {
    if (
      u.role !== 'class_admin' ||
      s.version > 0 ||
      db().submissions.some((v) => v.status !== 'draft')
    )
      fail(
        409,
        'governance_started',
        '已选择或已开始的周期不能中途切换模式',
      )
    s.mode = b.mode === 'collective' ? 'enrolling' : 'centralized'
    s.version++
    s.enrollmentCloseAt = iso(7 * day)
    return { notice: '已保存初始化模式' }
  }
  if (method === 'PUT' && path === '/governance/membership') {
    if (s.mode === 'centralized')
      fail(409, 'collective_required', '本班未开始共治招募')
    const old = s.members[u.id]
    s.members[u.id] = b.join
      ? {
          joinedAt: old && !old.leftAt ? old.joinedAt : nowIso(),
          leftAt: null,
          reviewer: !!b.reviewer,
        }
      : {
          joinedAt: old?.joinedAt ?? nowIso(),
          leftAt: nowIso(),
          reviewer: false,
        }
    return { notice: '已保存参与方式，已有提案名单保持冻结' }
  }
  if (method === 'GET' && path === '/governance/proposals')
    return {
      items: s.proposals.filter((p) => canRead(p, u)).map((p) => visible(p, u)),
    }
  if (method === 'POST' && path === '/governance/proposals') {
    needMember(u)
    const prior = s.proposals.find(
      (p) =>
        p.author === u.id && JSON.parse(p.request).requestId === b.requestId,
    )
    if (prior) {
      if (prior.request !== JSON.stringify(b))
        fail(409, 'request_changed', '请求已使用')
      return { id: prior.id }
    }
    if (
      !['motion', 'timeline', 'bonus', 'gpa', 'export', 'activate'].includes(
        String(b.action),
      )
    )
      fail(422, 'action_unknown', '不支持此事项')
    const p = newProposal(u, b)
    s.proposals.unshift(p)
    return { id: p.id, notice: '已公示提案并冻结本次名单' }
  }
  if (path === '/governance/settle')
    fail(
      409,
      'gate_not_ready',
      '演示班仍有未结案件，不能跳过专业分、审核或争议条件',
    )
  const match = path.match(
    /^\/governance\/(proposals|cases)\/([^/]+)(?:\/(vote|close|comments|opinion|ballot|restart))?$/,
  )
  if (!match) fail(404, 'not_found', '未找到共治功能')
  const p = s.proposals.find((p) => p.id === match[2])
  if (!p || !canRead(p, u)) fail(404, 'not_found', '未找到有权限查看的事项')
  const operation = match[3]
  if (operation === 'comments') {
    if (method === 'GET')
      return {
        items: p.comments.map(({ actor, ...comment }) => ({
          ...comment,
          mine: actor === u.id,
        })),
      }
    if (['applied', 'rejected', 'stale'].includes(p.status))
      fail(409, 'closed', '事项已归档')
    if (
      isCase(p) &&
      (p.subject !== u.id ||
        b.kind !== 'evidence' ||
        String(b.body).length < 20 ||
        p.round >= 2)
    )
      fail(403, 'subject_only', '只有当事人可补充一次新事实，至少 20 字')
    p.comments.push({
      id: nid('comment'),
      actor: u.id,
      kind: String(b.kind ?? 'discussion'),
      body: String(b.body),
      createdAt: nowIso(),
    })
    if (isCase(p)) {
      p.round++
      Object.values(p.voters).forEach((v) => {
        delete v.opinion
        delete v.candidate
      })
      p.status = 'voting'
      p.closesAt = iso(2 * day)
    }
    return { notice: '已保存说明' }
  }
  if (operation === 'vote') {
    const seat = p.voters[u.id]
    if (!seat || isCase(p))
      fail(403, 'electorate_required', '你不在本次冻结名单内')
    if (
      seat.locked ||
      !['discussion', 'voting'].includes(p.status) ||
      Date.now() < Date.parse(p.opensAt) ||
      Date.now() >= Date.parse(p.closesAt)
    )
      fail(409, 'vote_closed', '当前不能投票')
    if (!['yes', 'no', 'abstain'].includes(String(b.choice)))
      fail(422, 'choice_invalid', '请选择赞成、反对或弃权')
    seat.choice = String(b.choice)
    seat.locked = !!b.lock
    return { notice: '已记录本人的选择' }
  }
  if (operation === 'close') {
    if (isCase(p)) return finishCase(p, run)
    if (['applied', 'rejected', 'stale'].includes(p.status))
      return { status: p.status }
    if (
      Date.now() < Date.parse(p.closesAt) &&
      !Object.values(p.voters).every((v) => v.locked)
    )
      fail(409, 'vote_open', '投票尚未截止')
    if (
      Object.values(p.voters).filter((v) => v.choice === 'yes').length <
      p.requiredYes
    ) {
      p.status = 'rejected'
      return { notice: '未达到赞成票门槛，未参与不计同意' }
    }
    if (
      ['protected', 'activate'].includes(p.kind) &&
      Date.now() < Date.parse(p.closesAt) + day
    ) {
      p.status = 'passed'
      return { notice: '已通过，等待 24 小时生效公示' }
    }
    return execute(p, run)
  }
  const sub = db().submissions.find((v) => v.id === p.target)
  if (!sub) fail(404, 'case_missing', '演示案件材料不存在')
  if (method === 'GET') {
    const subject =
      (p.subject ? userById(p.subject) : undefined) ??
      db().users.find((v) => [sub.studentId, sub.id].includes(v.id) || v.sid === sub.studentId)
    return {
      proposal: visible(p, u).proposal,
      title: p.title,
      body: sub.note || sub.title,
      category: sub.category,
      itemKey: sub.itemKey,
      currentScore: sub.finalScore,
      ruleSnapshot: sub.ruleSnapshot as RuleSnapshot,
      evidence: (db().evidence[sub.id] ?? []) as Evidence[],
      canReview: !!p.voters[u.id],
      myOpinion: p.voters[u.id]?.opinion ?? {},
      version: String(p.round),
      changed: false,
      // The subject is visible to the seats and to the subject; reviewer and
      // reporter identities are never part of any response.
      student: subject?.name ?? '本班成员',
      studentId: subject?.sid ?? '',
      submitted: Object.values(p.voters).filter((v) => v.opinion).length,
      claim: sub.claim,
      requestedScore: sub.wantScore,
      submittedAt: sub.submittedAt,
      ...(['deliberating', 'blocked'].includes(p.status)
        ? {
            opinions: Object.values(p.voters).flatMap((v) =>
              v.opinion
                ? [
                    {
                      opinion: v.opinion,
                      reason: v.opinion.reason,
                      hash: candidate(v.opinion),
                    },
                  ]
                : [],
            ),
          }
        : {}),
    }
  }
  const seat = p.voters[u.id]
  if (!seat || p.subject === u.id)
    fail(403, 'seat_required', '本案没有你的有效评审席位')
  if (operation === 'opinion') {
    if (
      p.status !== 'voting' ||
      seat.opinion ||
      String(b.expectedVersion) !== String(p.round)
    )
      fail(409, 'opinion_locked', '意见已提交或材料版本已变化')
    const bounds = scoreBounds(sub.ruleSnapshot.item.scoreRule),
      rejected = b.decision === 'reject'
    const score = rejected ? 0 : Number(b.score)
    if (
      !Number.isFinite(score) ||
      (!rejected && (score < bounds.lo || score > bounds.hi)) ||
      String(b.reason).length < 4
    )
      fail(422, 'opinion_invalid', '请按现有规则填写分数与依据')
    seat.opinion = {
      score,
      decision: rejected ? 'rejected' : 'accepted',
      category: sub.category,
      itemKey: sub.itemKey,
      reason: String(b.reason),
    }
    return finishCase(p, run)
  }
  if (operation === 'ballot') {
    if (p.status !== 'deliberating' || seat.candidate)
      fail(409, 'ballot_closed', '当前不能重复表决')
    const key = String(b.candidate)
    if (
      key !== 'abstain' &&
      !Object.values(p.voters).some(
        (v) => v.opinion && candidate(v.opinion) === key,
      )
    )
      fail(422, 'candidate_missing', '裁决方案无效')
    seat.candidate = key
    return finishCase(p, run)
  }
  fail(409, 'operation_unavailable', '当前阶段不支持此操作')
}
