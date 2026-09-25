import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import path from 'node:path'
import { before, after, beforeEach, test } from 'node:test'
import { fileURLToPath, pathToFileURL } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const frontend = path.join(root, 'frontend')
const require = createRequire(path.join(frontend, 'package.json'))
const { createServer } = await import(pathToFileURL(require.resolve('vite')).href)
let server, handle, database, token
before(async () => {
  server = await createServer({root: frontend, configFile: false, appType: 'custom', logLevel: 'error', resolve: {alias: {'@': path.join(frontend, 'src')}}, server: {middlewareMode: true, hmr: false, watch: null, fs: {allow: [root]}}})
  ;({handle} = await server.ssrLoadModule(path.join(root, 'mock/src/handle.ts')))
  database = await server.ssrLoadModule(path.join(root, 'mock/src/db.ts'))
})
after(async () => {await server?.close()})
const call = (method, path, body) => handle(method, path, body, token)
beforeEach(async () => {
  database.resetStore()
  token = (await handle('POST', '/auth/login', {account: '20240003', password: database.DEMO_PASSWORD})).access_token
})
test('centralized remains the default; opt-in demo includes unregistered and non-submitting members', async () => {
  assert.equal((await call('GET', '/governance')).config.mode, 'centralized')
  await call('POST', '/governance/demo', {mode:'collective'})
  const s = await call('GET', '/governance')
  assert.equal(s.config.mode, 'collective')
  assert.equal(s.counts.electorate, 12)
  assert.ok(s.counts.roster > s.counts.registered)
  const volunteer = database.db().users.find(u => u.id === 'u-gov-0')
  assert.ok(volunteer.registered)
  assert.equal(database.db().submissions.filter(s => [volunteer.id, volunteer.sid].includes(s.studentId)).length, 0)
  assert.ok(database.db().governance.members[volunteer.id])
  await assert.rejects(() => call('POST', '/admin/settle'), e => e.status === 403)
})
test('withdrawal does not erase a frozen vote; protected proposal cannot lower the roster floor', async () => {
  await call('POST', '/governance/demo', {mode:'collective'})
  const initial = await call('GET', '/governance')
  if (initial.protectedRequired > 12) await assert.rejects(() => call('POST', '/governance/proposals', {requestId: crypto.randomUUID(), kind: 'protected', action: 'timeline', title: '重大事项演示', body: '核对全班时间窗口', payload: {}}), e => e.code === 'quorum_unreachable')
  const p = (await call('GET', '/governance/proposals')).items.find(x => x.proposal.action === 'motion').proposal
  await call('PUT', '/governance/membership', {join: false})
  await call('POST', `/governance/proposals/${p.id}/vote`, {choice: 'yes'})
  assert.equal((await call('GET', '/governance/proposals')).items.find(x => x.proposal.id === p.id).proposal.electorateCount, 12)
  await assert.rejects(() => call('POST', `/governance/proposals/${p.id}/close`), e => e.status === 409)
  await call('POST', '/governance/demo/advance')
  assert.equal((await call('POST', `/governance/proposals/${p.id}/close`)).status, 'applied')
})
test('individual opinions remain hidden until independent review completes, and majority updates score', async () => {
  await call('POST', '/governance/demo', {mode:'collective'})
  const p = (await call('GET', '/governance/proposals')).items.find(x => x.proposal.kind === 'review').proposal
  const d = await call('GET', `/governance/cases/${p.id}`)
  assert.equal(d.opinions, undefined)
  const saved = database.db().governance.proposals.find(x => x.id === p.id)
  const opinion = Object.values(saved.voters).find(x => x.opinion).opinion
  const result = await call('POST', `/governance/cases/${p.id}/opinion`, {...opinion, expectedVersion: d.version, reason: '根据公开规则和材料完成独立认定'})
  assert.equal(result.status, 'applied')
  assert.equal((await call('GET','/governance/proposals')).items.find(x=>x.proposal.id===p.id).mySubmitted,true)
  assert.equal(database.db().submissions.find(x => x.id === saved.target).finalScore, opinion.score)
})

test('the review case carries what a reviewer needs to score it, and only the count of submitted opinions',async()=>{
  await call('POST','/governance/demo',{mode:'collective'})
  const p=(await call('GET','/governance/proposals')).items.find(x=>x.proposal.kind==='review').proposal
  const d=await call('GET',`/governance/cases/${p.id}`)
  assert.ok(d.student)
  assert.match(d.studentId,/^\d+$/)
  assert.ok('claim' in d)
  assert.ok('requestedScore' in d)
  assert.ok(d.ruleSnapshot.item)
  // One seat has a preset opinion; the count is reported, the content is not.
  assert.equal(d.submitted,1)
  assert.equal(d.opinions,undefined)
})

test('collective desks reuse the ordinary handlers and still require membership',async()=>{
  await call('POST','/governance/demo',{mode:'collective'})
  assert.ok((await call('GET','/governance/objections')).items.length>0)
  assert.ok((await call('GET','/governance/gpa')).items.length>0)
  assert.ok((await call('GET','/governance/gate')).conditions.length>0)
  // Membership, not the old role, is what opens these. Leaving closes them again,
  // and the ordinary /review path stays shut for everyone in collective mode.
  await call('PUT','/governance/membership',{join:false})
  await assert.rejects(()=>call('GET','/governance/objections'),e=>e.status===403)
  await assert.rejects(()=>call('GET','/review/objections'),e=>e.status===403)
  // The gate is not a member-only read: anyone in the class can check it.
  assert.ok((await call('GET','/governance/gate')).conditions.length>0)
})

test('demo mode is chosen once by the initializer and cannot contaminate an ordinary demo',async()=>{
  const before=database.db().submissions.length
  await call('POST','/governance/demo',{mode:'centralized'})
  assert.equal(database.db().demoMode,'centralized')
  assert.equal(database.db().submissions.length,before)
  await assert.rejects(()=>call('POST','/governance/demo',{mode:'collective'}),e=>e.status===409)
  assert.equal((await call('GET','/governance')).config.mode,'centralized')
})

test('a fresh demo opens in ordinary mode without the choice page, and stays ordinary until reset',async()=>{
  const {startCentralizedDemo}=await server.ssrLoadModule(path.join(root,'mock/src/governance.ts'))
  startCentralizedDemo()
  assert.equal(database.db().demoMode,'centralized')
  // version 0 would send the class admin to the first-time mode choice after login.
  assert.equal((await call('GET','/governance')).config.version,1)
  await assert.rejects(()=>call('POST','/governance/demo',{mode:'collective'}),e=>e.status===409)
  database.resetStore()
  await call('POST','/governance/demo',{mode:'collective'})
  assert.equal((await call('GET','/governance')).config.mode,'collective')
})

test('ordinary members cannot choose demo mode; collective mode has no role-switch endpoint',async()=>{
  const student=(await handle('POST','/auth/login',{account:'20240001',password:database.DEMO_PASSWORD})).access_token
  await assert.rejects(()=>handle('POST','/governance/demo',{mode:'collective'},student),e=>e.status===403)
  await call('POST','/governance/demo',{mode:'collective'})
  await assert.rejects(()=>call('POST','/me/demo-cast',{identity:'class_admin'}),e=>e.status===403)
})
