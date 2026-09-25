#!/usr/bin/env node
/* 端到端回归：发布第二版方案之后，已经分发出去的条目不能从管理员视野里消失。

   这是线上报上来的问题——"管理员只能看到最后这几条的分配情况，前面的也分配出去了
   但管理员看不到"。成因是 submission.scheme_id 在提交那一刻钉死，而分发侧的统计
   一律按"当前方案"过滤：publishScheme 只把一份 draft 行改成 published，不迁移任何
   submission，于是旧提交在发版瞬间从工作量池里整体消失。审核人那边照旧看得见
   （/review/tasks 全程没有方案过滤），也照旧能审——两边因此对不上账。

   smoke 覆盖不到它：那条链路从头到尾只发布一次方案。

   用法：node e2e/run.mjs smoke 起栈之后，node e2e/scheme-republish.mjs
   或直接 node e2e/scheme-republish.mjs（需要 easygpa-plus-e2e 栈已在跑）。 */

import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const API_ORIGIN = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
const API = API_ORIGIN + '/api/v1'
const OPS_ACCOUNT = process.env.OPS_ACCOUNT ?? 'ops@e2e.local'
const OPS_PASSWORD = process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password'

let cookies = {}
const failures = []
const ok = (m) => console.log('  \x1b[32m✓\x1b[0m ' + m)
const info = (m) => console.log('\x1b[36m▸ ' + m + '\x1b[0m')
const bad = (m) => { failures.push(m); console.log('  \x1b[31m✗\x1b[0m ' + m) }

async function call(method, path, { token, body, raw } = {}) {
  const headers = e2eHeaders({ Accept: 'application/json' })
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`
  const jar = Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ')
  if (jar) headers.Cookie = jar
  const res = await fetch(API + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  for (const sc of res.headers.getSetCookie?.() ?? []) {
    const [pair] = sc.split(';')
    const i = pair.indexOf('=')
    cookies[pair.slice(0, i)] = pair.slice(i + 1)
  }
  const text = await res.text()
  let parsed = null
  try { parsed = text ? JSON.parse(text) : null } catch { parsed = text }
  if (raw) return { status: res.status, body: parsed }
  if (res.status >= 400) throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(parsed)}`)
  return parsed
}

async function waitFor(check, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs
  let last
  while (Date.now() < deadline) {
    last = await check()
    if (last) return last
    await new Promise((resolve) => setTimeout(resolve, 300))
  }
  throw new Error(`等待异步处理超时（最后状态：${JSON.stringify(last)}）`)
}

async function register({ sid, name, password }) {
  cookies = {}
  const ticket = await call('POST', '/auth/register/check', { body: { sid, name } })
  return call('POST', '/auth/register/complete', { body: { ticket: ticket.ticket, password } })
}

const stamp = Date.now().toString(36)
const sid = (n) => `R${stamp}${n}`

/* 只用 health_meet 一个小项：它不要求佐证，能把这条用例收敛到"分发口径"本身，
   不去碰对象存储。同一个小项允许提交多条（smoke 第 7.1 步已经这么用）。 */
function schemeConfig(name) {
  return {
    schemeName: name,
    weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
    categories: [
      { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
      { key: 'moral', name: '思想品德', maxTotal: 100, baseItems: [{ key: 'moral_base', name: '基础分', full: 80 }], penaltyItems: [], items: [] },
      { key: 'practice', name: '实践能力', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
      {
        key: 'health', name: '身心健康', maxTotal: 100,
        baseItems: [{ key: 'health_base', name: '体测基础分', full: 60 }], penaltyItems: [],
        items: [{ key: 'health_meet', name: '运动会', scoreRule: { type: 'per_unit', unit: '项', per: 2, cap: 8 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } }],
      },
    ],
  }
}

async function publish(token, name) {
  const draft = await call('POST', '/admin/scheme', { token, body: { name, config: schemeConfig(name) } })
  return call('POST', `/admin/scheme/${draft.id}/publish`, { token })
}

/** 提交一条并等它被自动分发落库。返回 submission id。 */
async function submitOne(token, title, note = '') {
  const draft = await call('POST', '/submissions', {
    token, body: { category: 'health', itemKey: 'health_meet', title, note, claim: { quantity: 1 } },
  })
  await call('POST', `/submissions/${draft.id}/submit`, { token })
  return draft.id
}

async function main() {
  await assertLocalDevelopmentAPI(API_ORIGIN)

  info('1. 建租户、导名单、发方案 v1')
  const ops = await call('POST', '/auth/login', { body: { account: OPS_ACCOUNT, password: OPS_PASSWORD } })
  await call('POST', '/ops/tenants', {
    token: ops.access_token,
    body: { name: '换版班 ' + stamp, slug: 'republish-' + stamp, adminSid: 'T' + stamp, adminName: '王管理' },
  })
  const admin = await register({ sid: 'T' + stamp, name: '王管理', password: 'admin-password-123' })
  const A = admin.access_token
  const csv = [
    'sid,name,role',
    `${sid(1)},张三,student`,
    `${sid(2)},李四,group`,
    `${sid(3)},王五,group`,
    `${sid(4)},赵六,group`,
  ].join('\n')
  await call('POST', '/admin/whitelist/import', { token: A, body: { csv } })
  const v1 = await publish(A, '综测方案（换版回归 v1）')
  ok(`方案 v${v1.version} 已发布`)

  const day = 864e5
  await call('PUT', '/admin/window', {
    token: A,
    body: { open: new Date(Date.now() - 3600e3).toISOString(), close: new Date(Date.now() + 30 * day).toISOString(), honorRollTopPercent: 20 },
  })
  for (const key of ['submit', 'edit', 'appeal', 'review', 'arbitrate', 'studentReport']) {
    await call('PUT', '/admin/window/capabilities', { token: A, body: { key, on: true } })
  }

  const stu = await register({ sid: sid(1), name: '张三', password: 'student-password-1' })
  const rev = []
  rev.push(await register({ sid: sid(2), name: '李四', password: 'reviewer-password-1' }))
  rev.push(await register({ sid: sid(3), name: '王五', password: 'reviewer-password-2' }))
  rev.push(await register({ sid: sid(4), name: '赵六', password: 'reviewer-password-3' }))
  const S = stu.access_token

  info('2. v1 下提交三条，等自动分发落地')
  const v1Items = []
  for (let n = 1; n <= 3; n += 1) v1Items.push(await submitOne(S, `v1 条目 ${n}`, n === 1 ? '参与校园执勤，检索标记 Needle_100%' : '参加体育活动'))
  const beforeState = await waitFor(async () => {
    const state = await call('GET', '/admin/dispatch', { token: A })
    return state.unassigned === 0 && state.assignedTotal === v1Items.length * 2 ? state : null
  })
  ok(`v1 分发完成：累计 ${beforeState.assignedTotal} 人次，极差 ${beforeState.spread}`)
  const beforeByUser = Object.fromEntries(beforeState.reviewers.map((r) => [r.userId, r.assigned]))
  const original = (await call('GET', `/submissions/${v1Items[0]}`, { token: S })).submission

  info('3. 发布方案 v2 —— 关键动作')
  const v2 = await publish(A, '综测方案（换版回归 v2）')
  if (v2.version <= v1.version) bad(`v2 版本号应大于 v1，实际 ${v2.version} <= ${v1.version}`)
  ok(`方案 v${v2.version} 已发布，v1 的三条提交仍在途`)

  info('4. 换版后管理员必须仍看得见这三条')
  const after = await call('GET', '/admin/dispatch', { token: A })
  if (after.assignedTotal !== beforeState.assignedTotal) {
    bad(`换版后累计分配量从 ${beforeState.assignedTotal} 变成 ${after.assignedTotal}——旧方案条目从管理员视野里消失了`)
  } else {
    ok(`累计分配量保持 ${after.assignedTotal} 人次`)
  }
  for (const r of after.reviewers) {
    if (beforeByUser[r.userId] !== r.assigned) {
      bad(`换版后 ${r.name} 的累计量从 ${beforeByUser[r.userId]} 变成 ${r.assigned}`)
    }
  }
  if (!failures.length) ok('逐人累计量逐个对上，没有人被清零')

  info('5. 管理员点开某人的待审队列，旧方案条目要在里面')
  let queuedTotal = 0
  const queues = {}
  for (const r of after.reviewers) {
    const queue = await call('GET', `/admin/dispatch/reviewers/${r.userId}/queue`, { token: A })
    queues[r.userId] = queue.items
    queuedTotal += queue.items.length
    if (queue.items.length !== r.pending) {
      bad(`${r.name} 的队列 ${queue.items.length} 条与工作量分布的待审 ${r.pending} 对不上`)
    }
  }
  if (queuedTotal !== v1Items.length * 2) {
    bad(`所有队列合计应为 ${v1Items.length * 2} 条，实际 ${queuedTotal}`)
  } else {
    ok(`队列合计 ${queuedTotal} 条，与工作量分布逐人一致`)
  }

  info('6. 审核人自己那一页要跟管理员对上')
  for (let i = 0; i < rev.length; i += 1) {
    const token = rev[i].access_token
    const category = await call('GET', '/review/category', { token })
    const poolRow = after.reviewers.find((r) => r.userId === String(rev[i].user.id ?? rev[i].user.userId ?? ''))
      ?? after.reviewers.find((r) => r.sid === sid(i + 2))
    if (!poolRow) { bad(`在工作量分布里找不到审核人 ${sid(i + 2)}`); continue }
    if (category.load.assigned !== poolRow.assigned || category.load.pending !== poolRow.pending) {
      bad(`${poolRow.name}：我的审核量 ${category.load.assigned}/${category.load.pending} 与管理员 ${poolRow.assigned}/${poolRow.pending} 对不上`)
    }
    const rows = category.items.reduce((sum, item) => sum + item.total, 0)
    if (rows !== poolRow.assigned) {
      bad(`${poolRow.name}：按大项分布合计 ${rows} 与累计 ${poolRow.assigned} 对不上`)
    }
  }
  ok('三名审核人的「我的审核量」与管理员逐人一致，按大项分布合计也对得上')

  info('7. v2 下再提交一条：均衡基数不能被换版重置')
  const v2Item = await submitOne(S, 'v2 条目 1')
  const grown = await waitFor(async () => {
    const state = await call('GET', '/admin/dispatch', { token: A })
    return state.unassigned === 0 && state.assignedTotal === (v1Items.length + 1) * 2 ? state : null
  })
  ok(`跨版累计到 ${grown.assignedTotal} 人次`)
  if (grown.spread > 1) {
    bad(`换版后均衡应继续生效，极差应 ≤1，实际 ${grown.spread}——说明基数被清零重算了`)
  } else {
    ok(`极差 ${grown.spread}，换版没有把任何人当成闲人`)
  }

  info('8. 旧方案条目要能被改派（守卫也不能按当前方案卡）')
  const target = v1Items[0]
  const holders = []
  for (const r of grown.reviewers) {
    const queue = await call('GET', `/admin/dispatch/reviewers/${r.userId}/queue`, { token: A })
    if (queue.items.some((item) => item.submissionId === target || item.id === target)) holders.push(r)
  }
  if (holders.length < 1) {
    bad('找不到持有 v1 首条条目的审核人，无法验证改派')
  } else {
    const from = holders[0]
    const to = grown.reviewers.find((r) => !holders.some((h) => h.userId === r.userId) && r.sid !== sid(1))
    if (!to) {
      bad('没有可接手的审核人，改派用例跳过')
    } else {
      const moved = await call('PUT', `/admin/dispatch/submissions/${target}`, {
        token: A,
        body: { from: from.userId, to: to.userId, reason: '换版回归用例：验证旧方案条目仍可改派' },
        raw: true,
      })
      if (moved.status >= 400) {
        bad(`旧方案条目改派失败 ${moved.status}：${JSON.stringify(moved.body)}`)
      } else {
        ok(`旧方案条目从 ${from.name} 改派给 ${to.name} 成功`)
      }
    }
  }

  /* 「连已分配但还没开评的一起重排」会把旧的 submission_reviewer 行置 active=false
     再插新行。工作量池若把失效行也数进「累计」，每重排一次每条提交就 +2，累计量滚雪球、
     进度条拉满、极差失真，而 Balance 拿累计量当第一排序键，于是越重排派得越偏。 */
  info('9. 反复重排不能让累计虚增')
  const baseline = await call('GET', '/admin/dispatch', { token: A })
  for (let round = 1; round <= 3; round += 1) {
    const body = { includeAssigned: true, seed: String(20260903 + round) }
    const preview = await call('POST', '/admin/dispatch/preview', { token: A, body })
    const predictedTotal = preview.rows.reduce((sum, row) => sum + row.after, 0)
    if (predictedTotal !== baseline.assignedTotal) {
      bad(`第 ${round} 次重排试算累计 ${predictedTotal} 应保持 ${baseline.assignedTotal}，旧任务被重复加进试算`)
    }
    const result = await call('POST', '/admin/dispatch/run', { token: A, body })
    const now = await call('GET', '/admin/dispatch', { token: A })
    if (preview.spreadAfter !== now.spread || result.spreadAfter !== now.spread) {
      bad(`第 ${round} 次重排极差：试算 ${preview.spreadAfter}、执行 ${result.spreadAfter}、实际 ${now.spread} 不一致`)
    }
    if (now.assignedTotal !== baseline.assignedTotal) {
      bad(`第 ${round} 次重排后累计从 ${baseline.assignedTotal} 变成 ${now.assignedTotal}——失效的分配行被重复计数了`)
      break
    }
    for (const r of now.reviewers) {
      const predicted = preview.rows.find((row) => row.reviewerId === r.userId)
      const executed = result.rows.find((row) => row.reviewerId === r.userId)
      if (predicted?.after !== r.assigned || executed?.after !== r.assigned) {
        bad(`第 ${round} 次重排 ${r.name}：试算 ${predicted?.after}、执行返回 ${executed?.after}、实际 ${r.assigned} 不一致`)
      }
      if (r.assigned !== r.pending + r.done) {
        bad(`第 ${round} 次重排后 ${r.name} 的累计 ${r.assigned} ≠ 待审 ${r.pending} + 已评 ${r.done}`)
      }
    }
    for (const reviewer of [admin, ...rev]) {
      const category = await call('GET', '/review/category', { token: reviewer.access_token })
      const categoryTotal = category.items.reduce((sum, item) => sum + item.total, 0)
      if (categoryTotal !== category.load.assigned) {
        bad(`第 ${round} 次重排后大项分布 ${categoryTotal} 不等于当前累计 ${category.load.assigned}`)
      }
    }
  }
  const settled = await call('GET', '/admin/dispatch', { token: A })
  if (settled.assignedTotal === baseline.assignedTotal) {
    ok(`重排三次，累计稳定在 ${settled.assignedTotal} 人次，且逐人满足 累计 = 待审 + 已评`)
  }

  info('10. 换版后看板、结算闸门与学生总览必须包含全部正式提交')
  const allIDs = [...v1Items, v2Item].sort()
  const drafts = []
  for (let index = 0; index < 2; index += 1) {
    drafts.push(await call('POST', '/submissions', { token: S, body: {
      category: 'health', itemKey: 'health_meet', title: `未提交草稿 ${index + 1}`, claim: { quantity: 1 },
    } }))
  }
  const progress = await call('GET', '/admin/review-progress?kind=item&status=unfinished&page_size=100', { token: A })
  assert.deepEqual(progress.items.map((row) => row.submissionId).sort(), allIDs)
  assert.equal(progress.total, 4)
  const stats = await call('GET', '/admin/stats', { token: A })
  assert.equal(stats.gate.unfinalizedReviews, 4)
  assert.equal(Object.values(stats.reviewProgress.item).reduce((a, b) => a + b, 0), 4)
  assert.equal(stats.ranking.items.find((row) => row.sid === sid(1)).pending, 4)
  assert.equal((await call('GET', '/me/score', { token: S })).current.pendingItems, 4)
  const retained = (await call('GET', `/submissions/${v1Items[0]}`, { token: S })).submission
  for (const key of ['id', 'title', 'claim', 'submittedAt', 'ruleSnapshot', 'filedRuleSnapshot']) {
    assert.deepEqual(retained[key], original[key], `换版不能重写 ${key}`)
  }
  ok('四条正式提交全部可见，两条草稿独立保留，旧条目的 ID、内容和规则快照不变')

  async function scoreItem(id) {
    let decisions = 0
    for (const reviewer of [admin, ...rev]) {
      const token = reviewer.access_token
      const queue = await call('GET', '/review/tasks', { token })
      if (!queue.items.some((row) => row.id === id)) continue
      await call('POST', `/review/tasks/${id}/decision`, { token, body: {
        decision: 'accepted', reason: '本地换版回归确认合成材料认定分', spentSeconds: 10,
      } })
      decisions += 1
    }
    assert.equal(decisions, 2)
  }
  await scoreItem(v2Item)
  const listProgress = (query) => call('GET', `/admin/review-progress?page_size=100&${query}`, { token: A })
  const allProgress = await listProgress('status=all')
  const unfilteredProgress = await listProgress('')
  assert.equal(allProgress.total, 4, '全部状态必须包含已定分和未完成任务')
  assert.deepEqual(allProgress.items.map((row) => row.id), unfilteredProgress.items.map((row) => row.id), '全部状态与不传状态条件应返回相同任务')
  const completedProgress = await listProgress('status=complete')
  const unfinishedProgress = await listProgress('status=unfinished')
  assert.deepEqual(completedProgress.items.map((row) => row.submissionId), [v2Item])
  assert.deepEqual(unfinishedProgress.items.map((row) => row.submissionId).sort(), v1Items.slice().sort())
  assert.equal(completedProgress.total + unfinishedProgress.total, allProgress.total)
  for (const status of new Set(allProgress.items.map((row) => row.status))) {
    const filtered = await listProgress(`status=${encodeURIComponent(status)}`)
    assert.deepEqual(filtered.items.map((row) => row.id), allProgress.items.filter((row) => row.status === status).map((row) => row.id))
  }
  const pageIDs = []
  for (const page of [1, 2]) {
    const result = await call('GET', `/admin/review-progress?status=all&page_size=2&page=${page}`, { token: A })
    assert.equal(result.total, 4, '全部状态分页时保留完整命中数')
    assert.equal(result.items.length, 2)
    pageIDs.push(...result.items.map((row) => row.id))
  }
  assert.deepEqual(pageIDs, allProgress.items.map((row) => row.id))
  assert.equal((await listProgress(`status=all&kind=item&student=${sid(1)}`)).total, 4)
  assert.equal((await listProgress('status=all&student=no-such-student')).total, 0)
  const searchProgress = (q, extra = '') => listProgress(`q=${encodeURIComponent(q)}&${extra}`)
  for (const [keyword, expected] of [
    ['v1 条目 2', [v1Items[1]]], [' 校园执勤 ', [v1Items[0]]], ['needle_100%', [v1Items[0]]],
    ['%', [v1Items[0]]], ['张三', allIDs], [sid(1), allIDs], ['运动会', allIDs], ['没有这个自述', []],
  ]) {
    const result = await searchProgress(keyword)
    assert.deepEqual(result.items.map((row) => row.submissionId).sort(), expected.slice().sort(), `关键词 ${keyword}`)
    assert.equal(result.total, expected.length)
  }
  assert.equal((await searchProgress('校园执勤', 'student=no-such-student')).total, 0)
  assert.deepEqual((await searchProgress('运动会', 'status=complete')).items.map((row) => row.submissionId), [v2Item])
  const searchPages = []
  for (const page of [1, 2]) {
    const result = await call('GET', `/admin/review-progress?q=${encodeURIComponent('运动会')}&page_size=2&page=${page}`, { token: A })
    assert.equal(result.total, 4, '搜索须先筛选全量任务，再分页')
    searchPages.push(...result.items.map((row) => row.id))
  }
  assert.deepEqual(searchPages, allProgress.items.map((row) => row.id))
  assert.ok(v1Items.every((id) => /^\d+$/.test(id)))
  const ageAssignments = spawnSync('docker', ['exec', 'easygpa-plus-e2e-postgres-1', 'psql', '-U', 'easygpa', '-d', 'easygpa_e2e', '-v', 'ON_ERROR_STOP=1', '-c',
    `UPDATE submission_reviewer SET assigned_at=now()-interval '2 days' WHERE submission_id IN (${v1Items.join(',')})`], { encoding: 'utf8' })
  assert.equal(ageAssignments.status, 0, ageAssignments.stderr)
  const remind = (q) => call('POST', '/admin/review-progress/remind', { token: A, body: { q, kind: 'item' } })
  assert.deepEqual(await remind('校园执勤'), { recipientCount: 2, overdueCount: 2 })
  assert.deepEqual(await remind('needle_100%'), { recipientCount: 2, overdueCount: 2 })
  assert.deepEqual(await remind('没有这个自述'), { recipientCount: 0, overdueCount: 0 })
  ok('审核明细搜索覆盖姓名、学号、标题、自述和小项，跨版本分页、组合筛选及逾期提醒范围一致')
  ok('全部状态包含已定分和未完成任务，具体状态、分页及组合筛选正常')
  await call('POST', '/me/seal', { token: S, body: { phrase: '全部提交完成', sid: sid(1) } })
  assert.equal((await call('GET', '/admin/gate', { token: A })).unfinalizedReviews, 3)
  assert.equal((await call('GET', '/admin/review-progress?kind=scorecard', { token: A })).total, 0,
    '旧版材料未定分时，不能生成遗漏旧材料的整表快照')
  for (const id of v1Items) await scoreItem(id)
  assert.equal((await call('GET', '/me/score', { token: S })).current.categoryScores.health, 68)
  const reviewStudents = await call('GET', '/review/students', { token: A })
  const targetStudent = reviewStudents.items.find((row) => row.sid === sid(1))
  assert.equal(targetStudent.scoredCount, 6, '四条材料加两个基础项')
  const scorecard = await call('GET', `/review/students/${targetStudent.userId}/scorecard`, { token: A })
  assert.deepEqual(scorecard.submissions.map((row) => row.id).sort(), allIDs)
  await call('GET', '/class-penalties', { token: S })
  const oldAppeal = await call('POST', '/appeals/draft', { token: S, body: {
    targetType: 'submission', targetId: v1Items[0], reason: '本地回归验证旧方案材料申诉仍计入当前待处理事项',
  } })
  await call('POST', `/appeals/${oldAppeal.id}/submit`, { token: S, body: {
    reason: '本地回归验证旧方案材料申诉仍计入当前待处理事项', score: 3,
  } })
  assert.equal((await call('GET', '/admin/gate', { token: A })).pendingConflicts, 1)
  assert.equal((await call('GET', '/me/scorecard', { token: S })).state, 'confirmable')
  await call('POST', `/appeals/${oldAppeal.id}/withdraw`, { token: S })
  await call('POST', '/me/seal', { token: S, body: { phrase: '全部提交完成', sid: sid(1) } })
  ok('未定分材料不计分但可核对，定分后按各自快照全部计入当前成绩')

  info('11. 实时成绩、结算与强制驳回覆盖旧版材料')
  const live = await call('GET', '/me/scorecard', { token: S })
  assert.deepEqual(live.scorecard.items.map((row) => row.id).sort(), allIDs)
  await call('POST', '/me/scorecard/confirm', { token: S, body: { revision: live.revision } })
  await call('POST', '/admin/gate/force', { token: A, body: { reason: '本地换版回归验证跨方案材料计入同一份结算' } })
  await call('POST', '/admin/settle', { token: A })
  const finalScore = await call('GET', '/me/score', { token: S })
  assert.equal(finalScore.categoryScores.health, 68)
  for (const id of allIDs) assert.equal((await call('GET', `/submissions/${id}`, { token: S })).submission.status, 'locked')
  await call('POST', `/admin/submissions/${v1Items[0]}/force-reject`, { token: A, body: { reason: '本地回归验证旧版条目变更会使当前版成绩核对和结算失效' } })
  assert.equal((await call('GET', '/me/score', { token: S })).stale, true)
  assert.equal((await call('GET', '/me/score', { token: S })).current.categoryScores.health, 66)
  assert.equal((await call('GET', '/me/scorecard', { token: S })).confirmation.recheck, true)
  for (const draft of drafts) assert.equal((await call('GET', `/submissions/${draft.id}`, { token: S })).submission.status, 'draft')
  ok('成绩核对和结算包含全部版本材料，旧条目改判使当前快照失效，草稿始终保留')

  const otherTenant = await call('POST', '/ops/tenants', { token: ops.access_token, body: {
    name: `换版隔离班 ${stamp}`, slug: `republish-other-${stamp}`, adminSid: `O${stamp}`, adminName: '隔离班管',
  } })
  assert.ok(otherTenant)
  const other = await register({ sid: `O${stamp}`, name: '隔离班管', password: 'admin-password-123' })
  await publish(other.access_token, '隔离方案')
  assert.equal((await call('GET', '/admin/review-progress?kind=item', { token: other.access_token })).total, 0)
  assert.equal((await call('GET', '/admin/review-progress?status=all', { token: other.access_token })).total, 0,
    '全部状态不能显示其他班级的任务')
  assert.equal((await call('GET', `/admin/review-progress?q=${encodeURIComponent('校园执勤')}`, { token: other.access_token })).total, 0,
    '自述搜索不能命中其他班级材料')
  assert.equal((await call('GET', '/admin/stats', { token: other.access_token })).gate.unfinalizedReviews, 0)
  assert.equal((await call('GET', `/submissions/${v1Items[0]}`, { token: other.access_token, raw: true })).status, 404)
  ok('去掉方案过滤后，跨班隔离仍然生效')

  mkdirSync('.ai-eval', { recursive: true })
  writeFileSync('.ai-eval/republish-fixture.json', JSON.stringify({ studentSid: sid(1), adminSid: `T${stamp}`, draftIds: drafts.map((row) => row.id) }))

  console.log('')
  if (failures.length) {
    console.log(`\x1b[31m✗ ${failures.length} 项未通过\x1b[0m`)
    for (const f of failures) console.log('  · ' + f)
    process.exit(1)
  }
  console.log('\x1b[32m✓ 换版后材料全流程回归全部通过\x1b[0m')
}

main().catch((error) => {
  console.error('\x1b[31m用例异常终止：\x1b[0m', error)
  process.exit(1)
})
