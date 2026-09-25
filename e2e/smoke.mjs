#!/usr/bin/env node
/* 端到端冒烟：拿真实 HTTP 把主线走一遍。
   ops 建租户 → 班管凭令牌注册 → 导白名单 → 发方案 → 学生提交(含佐证直传 Garage)
   → 分发 → 背靠背双评 → 冲突仲裁 → 两轮申诉 → 扣分与异议 → GPA → 闸门 → 结算 → 导出 → 越权边界。

   为什么是脚本而不是 Go 测试：这条链路要同时碰 API、PostgreSQL、Redis 和 Garage，
   任何一环用桩替掉，验的就不再是"部署起来能不能用"。腾讯云 SES 属于外部服务，
   上线检查通过运维页的 mail_test 模板自检完成，不在重复运行的业务冒烟里群发邮件。

   用法：node e2e/run.mjs smoke

   统一入口会启动 disposable easygpa-plus-e2e 本地开发容器并注入合成凭据。
   本脚本只接受 loopback API，且在任何登录或写操作前要求 /healthz 明确报告 dev。
   每次运行新建一个租户；测试数据随 E2E 容器一同销毁。
   失败时退出码非 0。 */

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

async function waitFor(check, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs
  let last
  while (Date.now() < deadline) {
    last = await check()
    if (last) return last
    await new Promise((resolve) => setTimeout(resolve, 300))
  }
  throw new Error(`等待异步处理超时（最后状态：${JSON.stringify(last)}）`)
}

/* 业务冒烟只验证核对身份 → 设密码。邮箱绑定与 SES 投递通过运维自检单独验证，
   避免每次端到端测试向外部地址发送事务邮件。 */
async function register({ sid, name, password }) {
  cookies = {} // 每个身份一套 refresh cookie，别串味
  const ticket = await call('POST', '/auth/register/check', {
    body: { sid, name },
  })
  const session = await call('POST', '/auth/register/complete', { body: { ticket: ticket.ticket, password } })
  // 不只检查注册响应：立即走一次真实鉴权中间件，避免到流程中段
  // 才发现某个测试身份拿到了不可用的 access token。
  const me = await call('GET', '/me', { token: session.access_token })
  if (me.sid !== sid || me.role !== session.user.role) {
    throw new Error(`注册后鉴权身份不一致：期望 ${sid}/${session.user.role}，实际 ${me.sid}/${me.role}`)
  }
  return session
}

const PDF = Buffer.from('%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n')

/** presign → 带 policy 字段的 POST 直传对象存储 → complete。中间那步不经过后端。 */
async function uploadEvidence(token, submissionId, filename) {
  const pre = await call('POST', `/submissions/${submissionId}/evidence/presign`, {
    token, body: { filename, mediaType: 'application/pdf', sizeBytes: PDF.length },
  })
  const form = new FormData()
  for (const [key, value] of Object.entries(pre.uploadFields ?? {})) form.append(key, value)
  form.append('file', new Blob([PDF], { type: 'application/pdf' }), filename)
  const upload = await fetch(pre.uploadUrl, { method: 'POST', body: form })
  if (!upload.ok) throw new Error(`对象存储 POST 失败 ${upload.status} ${await upload.text()}`)
  return call('POST', `/submissions/${submissionId}/evidence/${pre.evidenceId}/complete`, { token })
}

const stamp = Date.now().toString(36)
const sid = (n) => `S${stamp}${n}`

function schemeConfig() {
  return {
    schemeName: '综合测评方案（冒烟）',
    weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
    categories: [
      { key: 'major', name: '专业素质', maxTotal: 100, baseItems: [], penaltyItems: [], items: [] },
      {
        key: 'moral', name: '思想品德', maxTotal: 100,
        baseItems: [{ key: 'moral_base', name: '基础分', full: 80 }],
        penaltyItems: [{ key: 'moral_penalty', name: '违纪扣分', per: -5 }],
        items: [
          { key: 'moral_volunteer', name: '志愿服务', scoreRule: { type: 'per_unit', unit: '小时', per: 0.5, cap: 10 }, evidence: { required: true, types: ['pdf'], maxMb: 10 } },
          { key: 'moral_award', name: '荣誉称号', scoreRule: { type: 'enum', options: [{ label: '校级', score: 3 }, { label: '省级', score: 6 }] }, evidence: { required: true, types: ['pdf'], maxMb: 10 } },
        ],
      },
      {
        key: 'practice', name: '实践能力', maxTotal: 100,
        baseItems: [{
          key: 'practice_base', name: '参加课外学术科技活动', full: 40,
          studentClaim: { minimum: 2, unit: '项', evidence: { required: true, types: ['pdf'], maxMb: 10 } },
        }], penaltyItems: [],
        items: [
          { key: 'practice_contest', name: '学科竞赛', scoreRule: { type: 'enum', options: [{ label: '省级一等', score: 8 }] }, capGroup: { key: 'contest', cap: 20 }, evidence: { required: true, types: ['pdf'], maxMb: 10 } },
          { key: 'practice_paper', name: '论文发表', scoreRule: { type: 'free', min: 0, max: 12 }, evidence: { required: true, types: ['pdf'], maxMb: 20 } },
        ],
      },
      {
        key: 'health', name: '身心健康', maxTotal: 100,
        baseItems: [{ key: 'health_base', name: '体测基础分', full: 60 }], penaltyItems: [],
        items: [{ key: 'health_meet', name: '运动会', scoreRule: { type: 'per_unit', unit: '项', per: 2, cap: 8 }, evidence: { required: false, types: ['pdf'], maxMb: 10 } }],
      },
    ],
  }
}

async function main() {
  await assertLocalDevelopmentAPI(API_ORIGIN)
  info('1. 运维登录 / 建租户')
  const ops = await call('POST', '/auth/login', { body: { account: OPS_ACCOUNT, password: OPS_PASSWORD } })
  const crest = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="50" cy="50" r="40"/></svg>'
  const savedCrest = await call('PUT', '/ops/branding', { token: ops.access_token, body: { svg: crest } })
  const publicCrest = await call('GET', '/branding')
  if (publicCrest.svg !== crest || publicCrest.revision !== savedCrest.revision) throw new Error('校徽保存后公开读取不一致')
  const unsafeCrest = await call('PUT', '/ops/branding', { token: ops.access_token, body: { svg: '<svg onload="alert(1)"/>' }, raw: true })
  if (unsafeCrest.status !== 400 || (await call('GET', '/branding')).svg !== crest) throw new Error('危险 SVG 必须拒绝且保留原配置')
  const unauthorizedCrest = await call('PUT', '/ops/branding', { body: { svg: '' }, raw: true })
  if (unauthorizedCrest.status !== 401) throw new Error('公开访客不能修改校徽')
  await call('PUT', '/ops/branding', { token: ops.access_token, body: { svg: '' } })
  if ((await call('GET', '/branding')).svg !== '') throw new Error('移除校徽未生效')
  ok('校徽保存、匿名读取、权限限制、危险 SVG 拒绝与移除通过')
  const tenant = await call('POST', '/ops/tenants', {
    token: ops.access_token,
    body: { name: '冒烟班 ' + stamp, slug: 'smoke-' + stamp, adminSid: 'T' + stamp, adminName: '王管理' },
  })
  ok(`租户 #${tenant.id}，首位班管 ${tenant.admin.name}`)

  info('2. 班级管理员按运维任命注册 + 导入白名单')
  const admin = await register({ sid: 'T' + stamp, name: '王管理', password: 'admin-password-123' })
  const A = admin.access_token
  if (admin.user.role !== 'class_admin') bad(`任命后注册角色应为 class_admin，实际 ${admin.user.role}`)
  const csv = ['sid,name,role', `${sid(1)},张三,student`, `${sid(2)},李四,group`, `${sid(3)},王五,group`, `${sid(4)},赵六,student`].join('\n')
  ok(`白名单导入 ${(await call('POST', '/admin/whitelist/import', { token: A, body: { csv } })).imported} 条`)

  info('3. 发布方案')
  const draft = await call('POST', '/admin/scheme', { token: A, body: { name: '综合测评方案（冒烟）', config: schemeConfig() } })
  ok(`已发布 v${(await call('POST', `/admin/scheme/${draft.id}/publish`, { token: A })).version}`)

  info('3.1 用时间线独立开放本轮窗口和业务能力')
  const day = 864e5
  const windowOpen = new Date(Date.now() - 3600e3).toISOString()
  const windowClose = new Date(Date.now() + 30 * day).toISOString()
  await call('PUT', '/admin/window', {
    token: A,
    body: { open: windowOpen, close: windowClose, honorRollTopPercent: 20 },
  })
  for (const key of ['submit', 'edit', 'appeal', 'review', 'arbitrate']) {
    await call('PUT', '/admin/window/capabilities', { token: A, body: { key, on: true } })
  }
  ok('评分结构与运行时间线分开配置')

  info('3.2 未注册学生也可进入综测业务')
  const importedStudents = await call('GET', '/review/students', { token: A })
  const importedSids = importedStudents.items.map((item) => item.sid).sort()
  const wantImported = [sid(1), sid(2), sid(3), sid(4), 'T' + stamp].sort()
  if (JSON.stringify(importedSids) !== JSON.stringify(wantImported)) {
    bad(`导入后名册应含全部 active 成员（含查看者本人），实际 ${JSON.stringify(importedSids)}`)
  }
  const importedTarget = importedStudents.items.find((item) => item.sid === sid(4))
  if (!importedTarget) throw new Error('导入后、注册前没有为目标学生建立成员身份')
  const importedCard = await call('GET', `/review/students/${importedTarget.userId}/scorecard`, { token: A })
  if (!importedCard.baseItems.some((row) => row.kind === 'base' && row.itemKey === 'moral_base')) {
    bad('未注册学生的计分卡没有按已发布方案渲染')
  }
  const importedDraft = await call('POST', '/review/objections', {
    token: A,
    body: { kind: 'base', studentUserId: importedTarget.userId, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 75, basis: '冒烟测试：未注册学生也可建立基础分提案' },
  })
  if (importedDraft.status !== 'draft') bad('未注册学生的异议提案未保存为草稿')
  await call('DELETE', `/review/objections/${importedDraft.id}`, { token: A })
  ok('名单导入即建立成员身份：可搜索、查看计分卡并建立提案')

  info('4. 学生与综测小组注册')
  const stu = await register({ sid: sid(1), name: '张三', password: 'student-password-1' })
  const stu2 = await register({ sid: sid(4), name: '赵六', password: 'student-password-2' })
  const rev1 = await register({ sid: sid(2), name: '李四', password: 'reviewer-password-1' })
  const rev2 = await register({ sid: sid(3), name: '王五', password: 'reviewer-password-2' })
  const S = stu.access_token
	const registeredUsers = await call('GET', '/admin/users', { token: A })
	const registeredTarget = registeredUsers.items.find((item) => item.sid === sid(4))
	if (registeredTarget?.id !== importedTarget.userId) bad('学生注册后没有复用导入时建立的成员身份')
	/* 自动分发已固定开启，关不掉：下面第 6 步一提交就会被分发，
	   所以第 7 步等异步分发落地，而不是再手工跑一次 run。 */
	const autoOff = await call('PUT', '/admin/dispatch/auto', { token: A, body: { on: false }, raw: true })
	if (autoOff.status !== 409) bad(`关闭自动分发应被拒绝为 409，实际 ${autoOff.status}`)
  ok(`4 个业务账号就绪且注册前后身份连续（角色：${[stu, stu2, rev1, rev2].map((x) => x.user.role).join('/')}）`)

  info('5. 学生提交 —— 每个小项都要能提交')
  const attempts = [
    { category: 'moral', itemKey: 'moral_volunteer', title: '敬老院志愿服务', claim: { quantity: 12 }, want: 6 },
    { category: 'moral', itemKey: 'moral_award', title: '校三好学生', claim: { option: '校级' }, want: 3 },
    { category: 'practice', itemKey: 'practice_contest', title: 'ACM 省赛一等奖', claim: { option: '省级一等' }, want: 8 },
    { category: 'practice', itemKey: 'practice_paper', title: '第一作者论文', claim: { score: 9 }, want: 9 },
    { category: 'practice', itemKey: 'practice_base', title: '两项课外学术科技活动', claim: { score: 40 }, want: 40 },
    { category: 'health', itemKey: 'health_meet', title: '校运会 800m', claim: { quantity: 2 }, want: 4 },
  ]
  const created = []
  for (const a of attempts) {
    const r = await call('POST', '/submissions', { token: S, body: a, raw: true })
    if (r.status >= 400) { bad(`提交 ${a.itemKey} 失败：${JSON.stringify(r.body)}`); continue }
    if (r.body.requestedScore !== a.want) bad(`${a.itemKey} 换算分应为 ${a.want}，实际 ${r.body.requestedScore}`)
    created.push({ ...a, id: r.body.id })
  }
  ok(`${created.length}/${attempts.length} 个小项建成草稿并换算正确`)

  info('6. 佐证直传 + 正式提交')
  for (const s of created) {
    await uploadEvidence(S, s.id, `${s.itemKey}.pdf`)
    await call('POST', `/submissions/${s.id}/submit`, { token: S })
  }
  const mine = await call('GET', '/submissions', { token: S })
  ok(`我的提交 ${mine.total} 条，状态 ${[...new Set(mine.items.map((i) => i.status))].join(',')}`)

	/* 撤回：在审的条目退回草稿，佐证留着。学生想改一条已提交的材料时走这条，
	   而不是删掉重传。退回后必须重新提交，否则后面的分发少一条。 */
	const backToDraft = created[0]
	const pulled = await call('POST', `/submissions/${backToDraft.id}/withdraw`, { token: S, raw: true })
	if (pulled.status >= 400 || pulled.body.status !== 'draft') {
		bad(`撤回失败：${JSON.stringify(pulled.body)}`)
	} else {
		const after = await call('GET', `/submissions/${backToDraft.id}`, { token: S })
		if (after.submission.status !== 'draft') bad(`撤回后状态应为 draft，实际 ${after.submission.status}`)
		else if (after.submission.submittedAt) bad('撤回后 submittedAt 应清空')
		else if (after.evidence.length === 0) bad('撤回不该动佐证，但佐证没了')
		else ok(`撤回回草稿，佐证保留 ${after.evidence.length} 份`)
		/* 幂等：再撤一次仍是 draft，不该报冲突。 */
		const again = await call('POST', `/submissions/${backToDraft.id}/withdraw`, { token: S, raw: true })
		if (again.status >= 400) bad(`重复撤回应当幂等，实际 ${JSON.stringify(again.body)}`)
		await call('POST', `/submissions/${backToDraft.id}/submit`, { token: S })
	}

  info('7. 分发（按条均衡，提交即自动分发）')
	/* 每条提交由 worker:dispatch 异步分发。unassigned 归零只说明每条都拿到了
	   第一名审核人，不代表两名都落库——所以把"两名审核人全部到位"本身当作等待条件，
	   下面的队列快照才不会读到分发到一半的状态。 */
	const readBack = await waitFor(async () => {
		const state = await call('GET', '/admin/dispatch', { token: A })
		return state.unassigned === 0 && state.assignedTotal === created.length * 2 ? state : null
	})
	const seed = readBack.seed
  if (typeof seed !== 'string') bad(`种子必须以字符串下发，实际类型 ${typeof seed}——数字会被 JS 取整`)
	if (!readBack.auto) bad('自动分发应固定开启')
	if (readBack.spread > 1) bad(`逐条均衡分发极差应 ≤1，实际 ${readBack.spread}`)
	if (readBack.policy !== 'balanced_per_submission') bad('GET /admin/dispatch 没有读回均衡分发状态')
  ok(`自动分发完成，种子 ${seed}，${readBack.assignedTotal} 人次，极差 ${readBack.spread}`)
	if (readBack.reviewers.some((r) => typeof r.userId !== 'string')) bad('审核人 userId 必须以字符串下发')
	ok('分发状态可读回（逐人累计、待审、极差与种子均持久化）')

  /* 审核人池含班级管理员；逐个拉队列，反查每一条真正分给了谁。 */
  const users = await call('GET', '/admin/users', { token: A })
	const tokenBySid = { [sid(2)]: rev1.access_token, [sid(3)]: rev2.access_token, ['T' + stamp]: A }
	const tokenByUserId = new Map(users.items.map((u) => [String(u.id), tokenBySid[u.sid]]).filter(([, t]) => t))
	const userIdBySid = new Map(users.items.map((u) => [u.sid, String(u.id)]))
	const rev1ID = userIdBySid.get(sid(2))
	const stu2ID = userIdBySid.get(sid(4))
	/* 队列每次现查，不留快照：下面 7.1 会暂停审核人并触发重排，
	   被暂停那位的分配会转成 inactive 换人接手。拿旧快照去做第 8 步的双评，
	   就会对着一个已经不在岗的审核人要任务，然后收到 404。 */
	const tokensForSubmission = async (submissionId) => {
		const state = await call('GET', '/admin/dispatch', { token: A })
		const tokens = []
		for (const reviewer of state.reviewers) {
			const queue = await call('GET', `/admin/dispatch/reviewers/${reviewer.userId}/queue`, { token: A })
			if (queue.items.some((row) => row.submissionId === submissionId)) tokens.push(tokenByUserId.get(reviewer.userId))
		}
		return tokens
	}
	const userIdByToken = new Map([...tokenByUserId].map(([id, token]) => [token, id]))

	info('7.1 自动分发、暂停与单条改派')
	await call('PUT', `/admin/dispatch/reviewers/${rev1ID}`, { token: A, body: { paused: true } })
	await call('PUT', '/admin/dispatch/auto', { token: A, body: { on: true } })
	const autoDraft = await call('POST', '/submissions', {
		token: stu2.access_token,
		body: { category: 'health', itemKey: 'health_meet', title: '自动分发验证', claim: { quantity: 1 } },
	})
	await call('POST', `/submissions/${autoDraft.id}/submit`, { token: stu2.access_token })
	const autoAssigned = await waitFor(async () => {
		const state = await call('GET', '/admin/dispatch', { token: A })
		if (state.unassigned !== 0) return null
		const owners = []
		for (const reviewer of state.reviewers) {
			const queue = await call('GET', `/admin/dispatch/reviewers/${reviewer.userId}/queue`, { token: A })
			if (queue.items.some((row) => row.submissionId === autoDraft.id)) owners.push(reviewer.userId)
		}
		return owners.length === 2 ? owners : null
	})
	if (autoAssigned.includes(rev1ID)) bad('暂停的审核人仍收到了自动分发任务')
	else ok('自动分发生成且仅生成两行，暂停审核人未接新任务')
	const selfMove = await call('PUT', `/admin/dispatch/submissions/${autoDraft.id}`, {
		token: A, body: { from: autoAssigned[0], to: stu2ID, reason: '验证不能改派给学生本人' }, raw: true,
	})
	if (selfMove.status !== 422) bad(`改派给学生本人应 422，实际 ${selfMove.status}`)
	await call('PUT', `/admin/dispatch/reviewers/${rev1ID}`, { token: A, body: { paused: false } })
	const reassigned = await call('PUT', `/admin/dispatch/submissions/${autoDraft.id}`, {
		token: A, body: { from: autoAssigned[0], to: rev1ID, reason: '冒烟测试单条换人' },
	})
	if (!reassigned.reviewers.some((r) => r.userId === rev1ID)) bad('单条改派后新审核人未生效')
	else ok('单条改派保留历史并写入新活跃分配')
	const raceDraft = await call('POST', '/submissions', {
		token: stu2.access_token,
		body: { category: 'health', itemKey: 'health_meet', title: '并发分发验证', claim: { quantity: 1 } },
	})
	await call('POST', `/submissions/${raceDraft.id}/submit`, { token: stu2.access_token })
	await call('POST', '/admin/dispatch/run', { token: A, body: {} })
	const raceOwners = await waitFor(async () => {
		const state = await call('GET', '/admin/dispatch', { token: A })
		const owners = []
		for (const reviewer of state.reviewers) {
			const queue = await call('GET', `/admin/dispatch/reviewers/${reviewer.userId}/queue`, { token: A })
			if (queue.items.some((row) => row.submissionId === raceDraft.id)) owners.push(reviewer.userId)
		}
		return state.unassigned === 0 ? owners : null
	})
	if (raceOwners.length !== 2) bad(`自动 worker 与手动 run 并发后应恰好两名审核人，实际 ${raceOwners.length}`)
	else ok('自动 worker 与手动 run 按班串行，未产生第三名审核人')

  info('8. 背靠背双评')
	const moral = created.filter((row) => row.category === 'moral')
	const [ma, mb] = await tokensForSubmission(moral[0].id)
	const [mc, md] = await tokensForSubmission(moral[1].id)
	if (!ma || !mb || !mc || !md) throw new Error('逐条队列没有为每条材料返回两名审核人')
	const workload = await call('GET', '/review/category', { token: rev1.access_token })
	if (!workload.load || typeof workload.load.classAverage !== 'number') bad('/review/category 缺少 ReviewLoad')
	await call('POST', `/review/tasks/${moral[0].id}/decision`, { token: ma, body: { decision: 'accepted', reason: '材料属实', spentSeconds: 30 } })
	const untouched = readBack.reviewers.find((r) => ![userIdByToken.get(ma), userIdByToken.get(mb)].includes(r.userId))
	const reviewedMove = await call('PUT', `/admin/dispatch/submissions/${moral[0].id}`, {
		token: A, body: { from: userIdByToken.get(ma), to: untouched.userId, reason: '验证已有结论不能改派' }, raw: true,
	})
	if (reviewedMove.status !== 409) bad(`已有结论的审核人改派应 409，实际 ${reviewedMove.status}`)
	const protectedPlan = await call('POST', '/admin/dispatch/preview', { token: A, body: { includeAssigned: true, seed } })
	if (protectedPlan.targets !== created.length + 1) bad(`重排应排除已有结论条目，目标数应为 ${created.length + 1}，实际 ${protectedPlan.targets}`)
	else ok('includeAssigned 重排会保留已经出现结论的条目')
  const peerView = await call('GET', `/review/tasks/${moral[0].id}`, { token: mb })
  if (peerView.myReview) bad('背靠背被打破：另一名审核人看到了对方的结论')
  else ok('背靠背成立：对方交完后，我这边 myReview 仍为空')
  const agreed = await call('POST', `/review/tasks/${moral[0].id}/decision`, { token: mb, body: { decision: 'accepted', reason: '材料属实', spentSeconds: 25 } })
  if (agreed.submissionStatus !== 'scored') bad(`双评一致应定分，实际 ${agreed.submissionStatus}`)
  else ok(`双评一致 → 定分 ${agreed.finalScore}`)

	await call('POST', `/review/tasks/${moral[1].id}/decision`, { token: mc, body: { decision: 'accepted', reason: '通过', spentSeconds: 20 } })
	const conflict = await call('POST', `/review/tasks/${moral[1].id}/decision`, { token: md, body: { decision: 'adjusted', score: 1, reason: '佐证不足，调低', spentSeconds: 40 } })
  if (!conflict.conflict) bad('双评分歧应升为冲突')
  else ok('双评分歧 → 升为冲突，等管理员仲裁')

  info('9. 管理员仲裁')
  const arb = await call('POST', `/admin/submissions/${moral[1].id}/arbitrate`, { token: A, body: { score: 2, reason: '按佐证核定为 2 分' } })
  ok(`终裁 → ${arb.status} ${arb.finalScore}`)

  info('10. 两轮申诉：原审核人复评 → 第二次升班级管理员')
	const scored = (await call('GET', '/submissions', { token: S })).items.find((i) => i.id === moral[0].id)
  const appeal = await call('POST', '/appeals', { token: S, body: { targetType: 'submission', targetId: scored.id, reason: '认定分低于规则应得，请复核' } })
  if (appeal.round !== 1 || appeal.status !== 'reviewing' || appeal.handlers !== 2) bad(`第一次申诉路由不正确：${JSON.stringify(appeal)}`)
  else ok(`第一次申诉 #${appeal.id} 已原封不动交回 2 名原审核人`)
  const dup = await call('POST', '/appeals', { token: S, body: { targetType: 'submission', targetId: scored.id, reason: '再申诉一次试试看行不行' }, raw: true })
  if (dup.status !== 409) bad(`同一对象重复申诉应 409，实际 ${dup.status}`)
  else ok('第一次申诉未结时不能重叠申诉（409）')

  /* 分发池可能含班管。让班管先交、普通小组后看，才能验证小组视角的背靠背；
     班管视角按契约始终可以看全量。 */
  const [appealFirstToken, appealSecondToken] = ma === A ? [ma, mb] : mb === A ? [mb, ma] : [ma, mb]
  const firstReviewerView = await call('GET', `/review/appeals/${appeal.id}`, { token: appealFirstToken })
  if (!firstReviewerView.handlers.some((h) => h.mine)) bad('复评详情没有标出当前处理人')
  await call('POST', `/review/appeals/${appeal.id}/rereview`, {
    token: appealFirstToken, body: { decision: 'uphold', score: scored.finalScore, reason: '重新核对材料后维持原认定分', spentSeconds: 35 },
  })
  const peerBefore = await call('GET', `/review/appeals/${appeal.id}`, { token: appealSecondToken })
  const leaked = peerBefore.handlers.find((h) => !h.mine)?.rereview
  if (leaked) bad('申诉背靠背被打破：同行提交前看到了另一人的本轮复评')
  else ok('申诉复评背靠背成立：同行未提交前复评内容为 null')
  const rereviewed = await call('POST', `/review/appeals/${appeal.id}/rereview`, {
    token: appealSecondToken, body: { decision: 'uphold', score: scored.finalScore, reason: '复核原佐证与申诉理由后维持原分', spentSeconds: 28 },
  })
  if (!rereviewed.bothDecided || rereviewed.conflict || rereviewed.status !== 'resolved') bad(`复评同分应一审结案：${JSON.stringify(rereviewed)}`)
  else ok(`两名原审核人复评同分 → resolved ${rereviewed.resolvedScore}`)

  const appeal2 = await call('POST', '/appeals', { token: S, body: { targetType: 'submission', targetId: scored.id, reason: '对复评结果仍有异议，请班级管理员终裁' } })
  if (appeal2.round !== 2 || appeal2.status !== 'escalated' || appeal2.handlers !== 0) bad(`第二次申诉应直达管理员：${JSON.stringify(appeal2)}`)
  else ok(`第二次申诉 #${appeal2.id} 无复评人，直达班级管理员`)
  const finalAppeal = await call('POST', `/admin/appeals/${appeal2.id}/final`, { token: A, body: { score: scored.finalScore, reason: '核对两轮材料后作出班级最终裁定' } })
  if (finalAppeal.status !== 'final') bad(`管理员终裁应进入 final，实际 ${finalAppeal.status}`)
  else ok('第二次申诉已由班级管理员终裁，后续永久关闭')
  const exhausted = await call('POST', '/appeals', { token: S, body: { targetType: 'submission', targetId: scored.id, reason: '终裁后不应再受理这次申诉请求' }, raw: true })
  if (exhausted.status !== 409) bad(`终裁后第三次申诉应 409，实际 ${exhausted.status}`)

  info('10.1 扣分与异议：小组提案 → 管理员签字生效')
	const groupToken = rev1.access_token
	const reviewStudents = await call('GET', '/review/students', { token: groupToken })
  const targetStudent = reviewStudents.items.find((u) => u.sid === sid(4))
  if (!targetStudent) throw new Error('扣分与异议名册没有返回目标学生')
  const peerGroup = reviewStudents.items.find((u) => u.sid === sid(3))
  const classAdminOnRoster = reviewStudents.items.find((u) => u.sid === 'T' + stamp)
  if (!peerGroup) bad('名册没有返回另一名综测小组成员')
  if (!classAdminOnRoster) bad('名册没有返回班级管理员，班长将无法被录入考勤/处分')
  else {
    const adminCard = await call('GET', `/review/students/${classAdminOnRoster.userId}/scorecard`, { token: groupToken })
    if (!adminCard.baseItems.some((row) => row.kind === 'base' && row.itemKey === 'moral_base')) {
      bad('班级管理员计分卡没有按已发布方案渲染')
    }
    const selfCard = await call('GET', `/review/students/${rev1ID}/scorecard`, { token: groupToken, raw: true })
    if (selfCard.status !== 200 || selfCard.body?.student?.self !== true) {
      bad(`综测小组应能查看自己的计分卡，实际 ${selfCard.status} ${JSON.stringify(selfCard.body)}`)
    }
    const selfDraft = await call('POST', '/review/objections', {
      token: groupToken,
      body: { kind: 'base', studentUserId: rev1ID, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 75, basis: '冒烟测试：本人提案仍需班级管理员终裁' },
    })
    if (selfDraft.status !== 'draft') bad('综测小组针对本人的提案未保存为草稿')
    await call('DELETE', `/review/objections/${selfDraft.id}`, { token: groupToken })
    const officerDraft = await call('POST', '/review/objections', {
      token: groupToken,
      body: { kind: 'base', studentUserId: classAdminOnRoster.userId, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 70, basis: '冒烟测试：班长也要吃基础项调整' },
    })
    if (officerDraft.status !== 'draft') bad('针对班级管理员的异议提案未保存为草稿')
    await call('DELETE', `/review/objections/${officerDraft.id}`, { token: groupToken })
    ok('全部 active 成员（含当前审核人）均在名册中，可查看计分卡并建待终裁提案')
  }
	const card = await call('GET', `/review/students/${targetStudent.userId}/scorecard`, { token: groupToken })
  const penalty = card.baseItems.find((row) => row.kind === 'penalty' && row.itemKey === 'moral_penalty')
  if (!penalty || penalty.perScore !== -5) bad('计分卡未按方案渲染扣分项单价')
  const objection = await call('POST', '/review/objections', {
    token: groupToken,
    body: { kind: 'penalty', studentUserId: targetStudent.userId, category: 'moral', itemKey: 'moral_penalty', targetId: penalty?.id ?? null, proposedScore: -10, quantity: 2, basis: '冒烟测试：两次违纪记录' },
  })
  if (objection.status !== 'draft') bad('新提案应先保存为草稿')
	await call('POST', '/review/objections/submit', { token: groupToken, body: { ids: [objection.id] } })
  const adminQueue = await call('GET', '/admin/objections?status=submitted', { token: A })
  if (!adminQueue.items.some((item) => item.id === objection.id && item.proposer)) bad('管理员终裁台没有看到已提交提案或提出人')
  const objectionDecision = await call('POST', `/admin/objections/${objection.id}/decide`, { token: A, body: { action: 'apply', reason: '记录与依据完整，同意按两次扣分' } })
  if (objectionDecision.status !== 'applied' || objectionDecision.score !== -10) bad(`扣分提案未正确生效：${JSON.stringify(objectionDecision)}`)
  else ok('扣分提案经管理员签字后生效，提出人和依据均保留')
  const appliedBase = await call('GET', '/me/base-items', { token: stu2.access_token })
  const appliedPenalty = appliedBase.items.find((item) => item.itemKey === 'moral_penalty' && item.score === -10)
  if (!appliedPenalty) bad('学生端未读到已生效扣分')
  else {
    const penaltyAppeal = await call('POST', '/appeals', { token: stu2.access_token, body: { targetType: 'penalty_score', targetId: appliedPenalty.id, reason: '对违纪次数有异议，请原提出人重新核对记录' } })
    if (penaltyAppeal.status !== 'reviewing' || penaltyAppeal.handlers !== 1) bad(`扣分申诉应回到原提出人：${JSON.stringify(penaltyAppeal)}`)
		const penaltyRereview = await call('POST', `/review/appeals/${penaltyAppeal.id}/rereview`, { token: groupToken, body: { decision: 'uphold', score: -10, reason: '复核两次原始记录后维持扣分', spentSeconds: 20 } })
    if (penaltyRereview.status !== 'resolved' || penaltyRereview.resolvedScore !== -10) bad(`单人扣分复评应直接结案：${JSON.stringify(penaltyRereview)}`)
    else ok('基础/扣分申诉会回到最近生效提案的提出人，单人复评直接结案')
  }
	const editableDraft = await call('POST', '/review/objections', { token: groupToken, body: { kind: 'base', studentUserId: targetStudent.userId, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 70, basis: '冒烟测试：基础项草稿可编辑' } })
	await call('PUT', `/review/objections/${editableDraft.id}`, { token: groupToken, body: { kind: 'base', studentUserId: targetStudent.userId, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 65, basis: '冒烟测试：基础项草稿已修改' } })
	await call('DELETE', `/review/objections/${editableDraft.id}`, { token: groupToken })
	const withdrawDraft = await call('POST', '/review/objections', { token: groupToken, body: { kind: 'penalty', studentUserId: targetStudent.userId, category: 'moral', itemKey: 'moral_penalty', targetId: appliedPenalty?.id ?? null, proposedScore: -5, quantity: 1, basis: '冒烟测试：待撤回的一次扣分' } })
	await call('POST', '/review/objections/submit', { token: groupToken, body: { ids: [withdrawDraft.id] } })
	const withdrawn = await call('POST', `/review/objections/${withdrawDraft.id}/withdraw`, { token: groupToken })
  if (withdrawn.status !== 'withdrawn') bad('已提交提案未能由提出人撤回')
	const batchOne = await call('POST', '/review/objections', { token: groupToken, body: { kind: 'base', studentUserId: targetStudent.userId, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 70, basis: '冒烟测试：批量终裁一' } })
	const batchTwo = await call('POST', '/review/objections', { token: groupToken, body: { kind: 'base', studentUserId: targetStudent.userId, category: 'health', itemKey: 'health_base', targetId: null, proposedScore: 55, basis: '冒烟测试：批量终裁二' } })
	await call('POST', '/review/objections/submit', { token: groupToken, body: { ids: [batchOne.id, batchTwo.id] } })
  const batchDecision = await call('POST', '/admin/objections/decide-batch', { token: A, body: { ids: [batchOne.id, batchTwo.id], action: 'dismiss', reason: '冒烟测试：验证整批驳回保持原子性' } })
  if (batchDecision.decided !== 2) bad(`批量终裁应处理 2 条，实际 ${batchDecision.decided}`)
  else ok('提案修改、删除、撤回与批量终裁接口均通过')
	const handover = await call('POST', '/admin/dispatch/handover', {
		token: A, body: { from: rev1ID, reason: '冒烟测试整体转出并重新均衡' },
	})
	if (handover.moved < 1 || !Array.isArray(handover.rows)) bad(`整体转出结果不正确：${JSON.stringify(handover)}`)
	else ok(`整体转出 ${handover.moved} 条，逐条按同一均衡顺位重新找接手人`)

  info('11. GPA 导入')
  const gpa = await call('POST', '/admin/gpa/paste', { token: A, body: { text: [1, 2, 3, 4].map((n, i) => `${sid(n)},${[87.5, 82, 79.25, 91][i]}`).join('\n') } })
  ok(`导入 ${gpa.imported}/${gpa.totalStudents}，齐全=${gpa.complete}`)
  const wrongName = await call('POST', '/admin/gpa/paste', { token: A, body: { text: `${sid(1)},张三三,90` }, raw: true })
  if (wrongName.status !== 422) bad(`姓名对不上应 422，实际 ${wrongName.status}`)
  else ok('姓名与名单不符时整批拒绝（422 gpa_roster_mismatch）')

  info('12. 封存两步确认')
  const wrong = await call('POST', '/me/seal', { token: stu2.access_token, body: { phrase: '全部提交完毕', sid: sid(4) }, raw: true })
  if (wrong.status !== 422) bad(`短语错应 422，实际 ${wrong.status}`)
  else ok('短语差一字即拒绝（422 confirmation_mismatch）')
  for (const [tok, s] of [[S, sid(1)], [stu2.access_token, sid(4)], [rev1.access_token, sid(2)], [rev2.access_token, sid(3)], [A, 'T' + stamp]]) {
    await call('POST', '/me/seal', { token: tok, body: { phrase: '全部提交完成', sid: s }, raw: true })
  }
  ok('全员封存')

  /* 解封是「班级成员」页上唯一能把封存状态改回去的入口，理由会写进审计。
     解完必须重新封存，后面的结算闸门要求全员封存。 */
  const sealedList = await call('GET', '/admin/users', { token: A })
  const target = sealedList.items.find((u) => u.sid === sid(1))
  if (!target?.sealed) bad('封存后 /admin/users 没有把该学生标为已封存')
  const shortReason = await call('POST', `/admin/seals/${target.id}/unseal`, { token: A, body: { reason: '短' }, raw: true })
  if (shortReason.status !== 400) bad(`解封理由过短应 400，实际 ${shortReason.status}`)
  const byStudent = await call('POST', `/admin/seals/${target.id}/unseal`, { token: S, body: { reason: '学生自己解封' }, raw: true })
  if (byStudent.status !== 403) bad(`学生解封应 403，实际 ${byStudent.status}`)
  await call('POST', `/admin/seals/${target.id}/unseal`, { token: A, body: { reason: '遗漏一份志愿服务证明，班会确认后允许补交' } })
  const afterUnseal = await call('GET', '/admin/users', { token: A })
  if (afterUnseal.items.find((u) => u.sid === sid(1))?.sealed) bad('解封后仍然显示为已封存')
  else ok('班管填理由可解封（短理由 400、学生 403），状态同步回成员表')
  await call('POST', '/me/seal', { token: S, body: { phrase: '全部提交完成', sid: sid(1) }, raw: true })

  info('12.5 实时成绩与轻量核对')
  for (const row of created.filter((entry) => entry.category !== 'moral')) {
    const [ta, tb] = await tokensForSubmission(row.id)
    for (const token of [ta, tb].filter(Boolean)) await call('POST', '/review/tasks/' + row.id + '/decision', { token, body: { decision: 'accepted', reason: '材料属实', spentSeconds: 15 } })
  }
  const my = await call('GET', '/me/scorecard', { token: S })
  if (!my.revision || !my.scorecard) bad('缺少实时成绩和版本')
  if (/reviewer(Id|Sid)|"reviewer"/.test(JSON.stringify(my.scorecard))) bad('成绩表泄露审核人身份')
  const confirmed = await call('POST', '/me/scorecard/confirm', { token: S, body: { revision: my.revision } })
  if (!confirmed.confirmed) bad('核对记录未保存')
  const duplicate = await call('POST', '/me/scorecard/confirm', { token: S, body: { revision: my.revision } })
  if (duplicate.confirmedAt !== confirmed.confirmedAt) bad('重复确认应保持原记录')
  const liveGate = await call('GET', '/admin/gate', { token: A })
  if (liveGate.conditions.some((c) => ['resultsConfirmed', 'blindAuditComplete'].includes(c.key))) bad('结算仍含旧终审或确认条件')
  const unsealed = await call('GET', '/me/scorecard', { token: stu2.access_token })
  await call('POST', '/me/scorecard/confirm', { token: stu2.access_token, body: { revision: unsealed.revision } })
  ok('核对无需终审或封存，重复请求幂等，结算不依赖核对')

  info('13. 结算闸门')
  const officerTarget = classAdminOnRoster ?? reviewStudents.items.find((u) => u.sid === sid(3))
  if (!officerTarget) throw new Error('闸门测试找不到班长或综测小组作为异议对象')
  const pendingOfficer = await call('POST', '/review/objections', {
    token: groupToken,
    body: { kind: 'base', studentUserId: officerTarget.userId, category: 'moral', itemKey: 'moral_base', targetId: null, proposedScore: 80, basis: '冒烟测试：未终裁异议必须挡住结算闸门' },
  })
  await call('POST', '/review/objections/submit', { token: groupToken, body: { ids: [pendingOfficer.id] } })
  const blockedGate = await call('GET', '/admin/gate', { token: A })
  const noConflict = blockedGate.conditions.find((c) => c.key === 'noConflict')
  if (blockedGate.pendingConflicts < 1 || noConflict?.ok) {
    bad(`未终裁异议应挡住闸门，实际 pendingConflicts=${blockedGate.pendingConflicts} noConflict=${JSON.stringify(noConflict)}`)
  } else {
    ok(`未终裁异议计入闸门（待终裁 ${blockedGate.pendingConflicts}）`)
  }
  await call('POST', `/review/objections/${pendingOfficer.id}/withdraw`, { token: groupToken })
  const gate = await call('GET', '/admin/gate', { token: A })
  console.log('     ' + gate.conditions.map((c) => `${c.label}=${c.ok ? '✓' : '✗'}(${c.detail})`).join('  '))
  if (gate.conditions.find((c) => c.key === 'noConflict')?.ok !== true) {
    bad('撤回未终裁异议后闸门仍显示有待终裁冲突')
  }
  if (!gate.open) {
    const forced = await call('POST', '/admin/gate/force', { token: A, body: { reason: '冒烟测试：允许在条件未全满足时结算' } })
    ok(`强制开闸 open=${forced.open}`)
  }

  info('14. 结算（同一批数据必须可复现）')
  const first = await call('POST', '/admin/settle', { token: A })
  ok(`run=${first.runId} 覆盖 ${first.items.length} 人，首名 ${first.items[0].name} ${first.items[0].totalScore}`)
  const again = await call('POST', '/admin/settle', { token: A })
  if (!again.reused) bad('未失效的快照应被复用，而不是重新算一份')
  else ok('重复结算复用同一份快照（reused=true）')

  info('15. 学生读快照')
  const score = await call('GET', '/me/score', { token: S })
  if (!score.settled) bad('结算后学生应能读到快照')
  else ok(`总分 ${score.totalScore}，班级第 ${score.classRank}/${score.classSize}，三好=${score.honor}，过期=${score.stale}`)

  info('16. 导出')
  const job = await call('POST', '/admin/export', { token: A, body: { kind: 'summary' } })
  ok(`导出任务 ${job.jobId.slice(0, 8)} status=${job.status}（worker:export 未起时会停在 queued）`)

  info('17. 越权与边界')
  const opsAgentBoundary = await call('GET', '/ops/agent', { token: ops.access_token, raw: true })
  const checks = [
    ['ops 打业务接口', await call('GET', '/submissions', { token: ops.access_token, raw: true }), 404],
    ['学生打管理接口', await call('GET', '/admin/users', { token: S, raw: true }), 403],
    ['AI 预审接口', await call('POST', `/review/tasks/${moral[0].id}/suggest`, { token: groupToken, raw: true }), 501],
    ['运维 Agent 配置接口', opsAgentBoundary, 200],
    ['无令牌读方案', await call('GET', '/scheme/current', { raw: true }), 401],
  ]
  for (const [label, res, want] of checks) {
    if (res.status !== want) bad(`${label} 期望 ${want}，实际 ${res.status}`)
    else ok(`${label} → ${res.status}`)
  }
  if (opsAgentBoundary.body?.config && !Object.hasOwn(opsAgentBoundary.body.config, 'apiKey')) ok('运维 Agent 查询不回显 API Key')
  else bad('运维 Agent 查询泄露或缺失安全配置视图')

  console.log('')
  if (failures.length) {
    console.log(`\x1b[31m${failures.length} 个断言未通过：\x1b[0m`)
    failures.forEach((f) => console.log('  • ' + f))
    process.exit(1)
  }
  console.log('\x1b[32m全流程通过\x1b[0m')
}

main().catch((e) => {
  console.error('\n\x1b[31m中断：\x1b[0m' + e.message)
  process.exit(1)
})
