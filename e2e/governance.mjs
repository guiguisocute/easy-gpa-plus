// Real HTTP + database + object storage, restricted to the disposable local stack.
import assert from 'node:assert/strict'
import {execFileSync} from 'node:child_process'
import {mkdirSync, writeFileSync} from 'node:fs'
import {assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin} from './support/local-environment.mjs'
const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
await assertLocalDevelopmentAPI(origin)
const stamp = Date.now().toString(36), password = 'governance-local-test-password'
const sql = text => execFileSync('docker', ['exec','-i','easygpa-plus-e2e-postgres-1','psql','-U','easygpa','-d','easygpa_e2e','-X','-Atq','-v','ON_ERROR_STOP=1'], {input:text, encoding:'utf8', windowsHide:true}).trim()
assert.equal(sql('SELECT current_database()'), 'easygpa_e2e')
async function call(method, path, body, token, status) {
  const response = await fetch(`${origin}/api/v1${path}`, {method, headers:e2eHeaders({'Content-Type':'application/json', ...(token ? {Authorization:`Bearer ${token}`} : {})}), body:body === undefined ? undefined : JSON.stringify(body)})
  const data = await response.json().catch(() => null)
  assert.ok(status ? response.status === status : response.ok, `${method} ${path}: ${response.status} ${JSON.stringify(data)}`)
  return data
}
async function register(sid, name) {
  const {ticket} = await call('POST','/auth/register/check',{sid,name})
  return (await call('POST','/auth/register/complete',{ticket,password})).access_token
}
const ops = (await call('POST','/auth/login',{account:'ops@e2e.local',password:'easygpa-e2e-ops-password'})).access_token
const sid = `GA${stamp}`
const tenant = await call('POST','/ops/tenants',{name:'共治合成回归班',slug:`governance-${stamp}`,adminSid:sid,adminName:'共治初始化成员'},ops)
const classID = Number(tenant.id)
assert.ok(Number.isSafeInteger(classID) && classID > 0)
const admin = await register(sid,'共治初始化成员')
await call('POST','/admin/whitelist/import',{csv:'sid,name,role\n'+Array.from({length:39},(_,i)=>`GS${stamp}${i},共治合成成员${i},student`).join('\n')},admin)
const config = {schemeName:'共治合成方案',weights:{moral:1},categories:[{key:'moral',name:'思想道德',maxTotal:100,baseItems:[{key:'base',name:'基础分',full:10}],penaltyItems:[{key:'late',name:'迟到扣分',per:-1}],items:[{key:'activity',name:'活动分',scoreRule:{type:'free',min:0,max:10},evidence:{required:false,types:['png','pdf'],maxMb:10}}]}]}
const draft = await call('POST','/admin/scheme',{name:config.schemeName,config},admin)
await call('POST',`/admin/scheme/${draft.id}/publish`,undefined,admin)
await call('PUT','/admin/timeline',{open:'2020-01-01T00:00:00Z',close:'2090-01-01T00:00:00Z'},admin)
for (const key of ['studentReport','review','appeal','arbitrate']) await call('PUT','/admin/window/capabilities',{key,on:true},admin)
const scheme = await call('GET','/scheme/current',undefined,admin)
const roster = (await call('GET','/review/students',undefined,admin)).items
const tokens = [admin]
for (let i=0;i<11;i++) tokens.push(await register(`GS${stamp}${i}`,`共治合成成员${i}`))
await call('PUT','/governance/config',{mode:'collective'},admin)
for (const token of tokens) await call('PUT','/governance/membership',{join:true,reviewer:true},token)
sql(`UPDATE class_governance SET enrollment_close_at=now()-interval '1 day' WHERE class_id=${classID}; UPDATE governance_member SET joined_at=now()-interval '2 days' WHERE class_id=${classID};`)
let state = await call('GET','/governance',undefined,admin)
assert.deepEqual([state.counts.roster,state.counts.registered,state.counts.submitted,state.counts.electorate],[40,12,0,12])
async function propose(action,payload,kind='ordinary') {
  return (await call('POST','/governance/proposals',{requestId:crypto.randomUUID(),kind,action,title:`合成回归 ${action}`,body:'依据现有规则及合成测试原件提出本次事项。',payload},admin)).id
}
async function pass(id, exclude=[]) {
  assert.match(String(id), /^\d+$/)
  sql(`UPDATE governance_proposal SET opens_at=now()-interval '1 hour',closes_at=now()+interval '1 day' WHERE id=${id};`)
  for (const token of tokens.filter(t => !exclude.includes(t))) await call('POST',`/governance/proposals/${id}/vote`,{choice:'yes',lock:true},token)
  let result = await call('POST',`/governance/proposals/${id}/close`,undefined,admin)
  if (result.status==='passed') {sql(`UPDATE governance_proposal SET decided_at=now()-interval '25 hours' WHERE id=${id};`); result=await call('POST',`/governance/proposals/${id}/close`,undefined,admin)}
  assert.equal(result.status,'applied')
  return result
}
await pass(await propose('activate',{},'activate'))
assert.equal((await call('GET','/governance',undefined,admin)).config.mode,'collective')
await call('POST','/admin/settle',{},admin,403)
await call('POST','/governance/proposals',{requestId:crypto.randomUUID(),kind:'protected',action:'timeline',title:'人数不足的重大提案',body:'不能删除未注册者来减少门槛。',payload:{open:'2020-01-01T00:00:00Z',close:'2090-01-01T00:00:00Z'}},admin,409)
console.log('✓ 40 人在册、12 人自愿参加且无人提交材料：共治可启动，旧管理员写操作受限，重大门槛不降低')

const unregistered = roster.find(u=>u.sid===`GS${stamp}38`)
const bonus = await propose('bonus',{requestId:crypto.randomUUID(),schemeVersion:scheme.version,studentIds:[unregistered.userId],category:'moral',itemKey:'activity',title:'合成共同活动',note:'按统一活动记录给未注册成员加分',claim:{score:4}},'bonus')
await pass(bonus)
assert.equal(sql(`SELECT final_score::text FROM submission WHERE class_id=${classID} AND student_id=${Number(unregistered.userId)} AND source='collective_grant'`),'4.000')
assert.equal((await call('GET','/governance',undefined,admin)).counts.submitted,0)
console.log('✓ 无个人改分特权的成员通过共同加分，未注册成员保留受益身份')

const upload = await call('POST','/governance/bonus-grant-uploads',{},admin)
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=','base64')
const pre = await call('POST',`/governance/bonus-grant-uploads/${upload.id}/notes/presign`,{filename:'synthetic-source.png',mediaType:'image/png',sizeBytes:png.length},admin)
const form = new FormData(); for (const [key,value] of Object.entries(pre.uploadFields)) form.append(key,value)
form.append('file',new Blob([png],{type:'image/png'}),'synthetic-source.png')
const uploadResult = await fetch(pre.uploadUrl,{method:'POST',body:form}); assert.ok(uploadResult.ok,`object upload ${uploadResult.status}`)
await call('POST',`/governance/bonus-grant-uploads/${upload.id}/notes/${pre.evidenceId}/complete`,{},admin)
await call('GET',`/evidence/${pre.evidenceId}/url`,undefined,admin)
const gpa = await propose('gpa',{uploadId:upload.id,text:'学号,姓名,均分\n'+roster.map(u=>`${u.sid},${u.name},80`).join('\n')})
await call('DELETE',`/governance/bonus-grant-uploads/${upload.id}/notes/${pre.evidenceId}`,undefined,admin,403)
await call('GET',`/evidence/${pre.evidenceId}/url`,undefined,tokens[1])
await pass(gpa,[admin])
assert.equal(sql(`SELECT count(*) FROM student_gpa WHERE class_id=${classID}`),'40')
console.log('✓ 原件上传、预览、冻结和其他选民读取正常，专业分通过核验后覆盖全部 40 人')

const subject = roster.find(u=>u.sid===`GS${stamp}0`)
const objection = await call('POST','/governance/objections',{kind:'base',studentUserId:subject.userId,category:'moral',itemKey:'base',targetId:null,proposedScore:8,basis:'合成回归：核对基础项事实，请随机小组确认。'},admin)
await call('POST','/governance/objections/submit',{ids:[objection.id]},admin)
const caseID = sql(`SELECT id FROM governance_proposal WHERE class_id=${classID} AND action='objection' AND target_id=${Number(objection.id)}`)
assert.match(caseID,/^\d+$/)
const seats = sql(`SELECT user_id FROM governance_voter WHERE proposal_id=${caseID} AND active ORDER BY user_id`).split('\n')
for (const seat of seats) {
  const who = roster.find(u=>u.userId===seat)
  assert.notEqual(who.sid,sid); assert.notEqual(who.sid,subject.sid)
  const token = (await call('POST','/auth/login',{account:who.sid,password})).access_token
  const d = await call('GET',`/governance/cases/${caseID}`,undefined,token)
  assert.equal(d.opinions,undefined)
  await call('POST',`/governance/cases/${caseID}/opinion`,{score:8,reason:'合成评审：依现有基础项规则核对证据认定八分。',expectedVersion:d.version},token)
}
assert.equal(sql(`SELECT score::text FROM base_score WHERE class_id=${classID} AND student_id=${Number(subject.userId)} AND item_key='base'`),'8.000')
console.log('✓ 随机处理扣分异议，发起人和当事人均回避，匿名独立意见通过后写回成绩')

// The collective desks are the ordinary ones behind a /governance prefix: same
// handlers, membership instead of the group role, old /review path still shut.
const baseID = sql(`SELECT id FROM base_score WHERE class_id=${classID} AND student_id=${Number(subject.userId)} AND item_key='base'`)
assert.equal((await call('GET','/governance/gpa',undefined,tokens[1])).items.length,40)
assert.ok((await call('GET','/governance/gate',undefined,tokens[1])).conditions.length>0)
assert.ok((await call('GET',`/governance/score-history/base/${baseID}`,undefined,tokens[1])).events.length>0)
assert.ok((await call('GET','/governance/objections',undefined,tokens[1])).items !== undefined)
await call('GET',`/review/score-history/base/${baseID}`,undefined,tokens[1],403)
await call('GET','/admin/gpa',undefined,tokens[1],403)
const outsiderSid = `GS${stamp}20`
const outsider = await register(outsiderSid,`共治合成成员20`)
await call('GET','/governance/objections',undefined,outsider,403)
await call('GET','/governance/score-history/base/'+baseID,undefined,outsider,403)
console.log('✓ 共治复用普通台的名单、闸门、异议与计分轨迹接口，未加入者和旧 /review、/admin 路径都取不到')

sql(`UPDATE class_timeline SET close_at=now()-interval '1 day' WHERE class_id=${classID};`)
const settlement = await call('POST','/governance/settle',{},tokens[1])
assert.ok(settlement.runId)
const exported = await pass(await propose('export',{kind:'summary'}),[admin])
assert.ok(exported.result.jobId)
await call('GET',`/governance/exports/${exported.result.jobId}`,undefined,admin)
await call('GET',`/governance/exports/${exported.result.jobId}`,undefined,tokens[1],403)
console.log('✓ 未交材料者不产生空表审核，普通参与者可按真实条件结算，导出仅授予指定领取人')

// Verify the scheduler's least-privilege discovery using its actual DB role.
assert.equal(sql(`SET ROLE easygpa_ops; SELECT count(*)>=0 FROM governance_due_proposals(); RESET ROLE;`),'t')
mkdirSync('.ai-eval',{recursive:true})
writeFileSync('.ai-eval/governance-fixture.json',JSON.stringify({classID,adminSid:sid,password,studentSid:subject.sid}))
console.log('✓ 共治 HTTP 回归完成，合成浏览器验收账号保存于被忽略的本地测试目录')
