// Real HTTP regression; synthetic loopback environment only.
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { writeFile } from 'node:fs/promises'
import { spawnSync } from 'node:child_process'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
await assertLocalDevelopmentAPI(origin)
const stamp = Date.now().toString(36)
const password = 'bonus-grant-synthetic-password'
async function call(method, path, body, token, status) {
  const response = await fetch(`${origin}/api/v1${path}`, { method,
    headers: e2eHeaders({ 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }),
    body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await response.json().catch(() => null)
  assert.ok(status ? response.status === status : response.ok, `${method} ${path}: ${response.status} ${data?.code ?? ''}`)
  return data
}
async function register(sid, name) {
  const { ticket } = await call('POST','/auth/register/check',{sid,name})
  return (await call('POST','/auth/register/complete',{ticket,password})).access_token
}
const ops = (await call('POST','/auth/login',{account:process.env.OPS_ACCOUNT ?? 'ops@e2e.local',password:process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password'})).access_token
async function makeClass(prefix) {
  const sid = `${prefix}${stamp}`
  await call('POST','/ops/tenants',{name:'直接加分回归班',slug:`bonus-${prefix.toLowerCase()}-${stamp}`,adminSid:sid,adminName:'回归班管'},ops)
  return { sid, token: await register(sid,'回归班管') }
}
const admin = await makeClass('BA')
const other = await makeClass('BO')
await call('POST','/admin/whitelist/import',{csv:`sid,name,role\nBS${stamp},回归学生,student\nBU${stamp},未注册成员,student\nBG1${stamp},回归审核甲,group\nBG2${stamp},回归审核乙,group`},admin.token)
const student = await register(`BS${stamp}`,'回归学生')
const group = await register(`BG1${stamp}`,'回归审核甲')
const group2 = await register(`BG2${stamp}`,'回归审核乙')
const roster = (await call('GET','/review/students',undefined,admin.token)).items
const id = (sid) => roster.find((m) => m.sid === sid).userId
const studentID = id(`BS${stamp}`), unregisteredID = id(`BU${stamp}`), adminID = id(admin.sid)
const foreignID = (await call('GET','/review/students',undefined,other.token)).items[0].userId
const evidence = {required:true,types:['pdf'],maxMb:10}
const config = {schemeName:'直接加分回归方案',weights:{moral:1},categories:[{key:'moral',name:'思想道德',maxTotal:100,
  baseItems:[{key:'ordinary',name:'普通基础分',full:10},{key:'condition',name:'条件基础项',full:5,studentClaim:{minimum:2,unit:'项',evidence}}],penaltyItems:[],
  items:[{key:'activity',name:'统一活动',scoreRule:{type:'free',min:0,max:10},evidence},
    {key:'quantity',name:'志愿时长',scoreRule:{type:'per_unit',unit:'小时',per:0.5,cap:5},evidence},
    {key:'tier',name:'活动档位',scoreRule:{type:'enum',options:[{label:'校级',score:3},{label:'省级',score:6}]},evidence}]}]}
const draft = await call('POST','/admin/scheme',{name:config.schemeName,config},admin.token)
await call('POST',`/admin/scheme/${draft.id}/publish`,undefined,admin.token)
const scheme = await call('GET','/scheme/current',undefined,admin.token)
const open = new Date(Date.now()-3600000).toISOString(), close = new Date(Date.now()+86400000).toISOString()
await call('PUT','/admin/window',{open,close},admin.token)
for (const key of ['studentReport','review','appeal','arbitrate']) await call('PUT','/admin/window/capabilities',{key,on:true},admin.token)
const request = (studentIds=[studentID], extra={}) => ({requestId:randomUUID(),schemeVersion:scheme.version,studentIds,category:'moral',itemKey:'activity',title:'统一无争议活动',note:'已核实本批成员参与统一活动',claim:{score:4.125},...extra})
const list = async () => (await call('GET','/admin/bonus-grants',undefined,admin.token)).items
const endpoint = '/admin/bonus-grants'
const batch = request([studentID,unregisteredID,adminID])
const results = await Promise.all([call('POST',endpoint,batch,admin.token),call('POST',endpoint,batch,admin.token)])
assert.equal(results[0].id,results[1].id)
assert.equal((await list()).length,1)
assert.equal((await list())[0].members.length,3)
await call('POST',endpoint,{...batch,title:'改变已提交批次'},admin.token,409)
for (const token of [student,group,other.token]) {
  const version = token === other.token ? (await call('GET','/scheme/current',undefined,token)).version : scheme.version
  await call('POST',endpoint,request([studentID], {schemeVersion:version}),token,token===other.token?422:403)
}
for (const body of [request([]),request([studentID,studentID]),request([studentID,foreignID]),request([studentID],{note:'短'}),
  request([studentID],{claim:{}}),request([studentID],{claim:{score:0}}),request([studentID],{claim:{score:-1}}),
  request([studentID],{claim:{score:11}}),request([studentID],{itemKey:'ordinary'}),request([studentID],{category:'major'})]) {
  await call('POST',endpoint,body,admin.token,422)
}
await call('POST',endpoint,request([studentID],{schemeVersion:'v0'}),admin.token,409)
await call('PUT',`/admin/users/${unregisteredID}/status`,{status:'disabled'},admin.token)
await call('POST',endpoint,request([studentID,unregisteredID]),admin.token,422)
assert.equal((await list()).length,1,'Failed batches must leave no grants')
await call('PUT',`/admin/users/${unregisteredID}/status`,{status:'active'},admin.token)

const row = (await list())[0].members.find((m) => m.studentId===studentID)
const detail = (await call('GET',`/submissions/${row.id}`,undefined,student)).submission
assert.equal(detail.source,'admin_grant'); assert.equal(detail.finalScore,4.125); assert.equal(detail.status,'scored'); assert.equal(detail.evidenceCount,0)
const publicEntries = (await call('GET','/class-penalties',undefined,group)).items.find((m) => m.userId===studentID).entries
assert.equal(publicEntries.find((e) => e.targetId===row.id).source,'admin_grant')
assert.deepEqual(publicEntries.find((e) => e.targetId===row.id).evidence,[])
const card = await call('GET',`/review/students/${studentID}/scorecard`,undefined,group)
assert.equal(card.submissions.find((s) => s.id===row.id).source,'admin_grant')
const trail = await call('GET',`/class-penalties/submission/${row.id}/history`,undefined,group)
assert.equal(trail.events[0].kind,'admin_grant'); assert.equal(trail.events[0].reason,batch.note)
assert.equal(trail.events[0].actorId,undefined)

// No review task is created, yet ordinary reports and objections accept the score.
const query = `SELECT jsonb_build_object('reviewers',(SELECT count(*) FROM submission_reviewer WHERE submission_id=${row.id}),'reviews',(SELECT count(*) FROM review WHERE submission_id=${row.id}))`
const dbCheck = spawnSync('docker',['exec','easygpa-plus-e2e-postgres-1','psql','-U','easygpa','-d','easygpa_e2e','-At','-c',query],{encoding:'utf8'})
assert.equal(dbCheck.status,0); assert.deepEqual(JSON.parse(dbCheck.stdout),{reviewers:0,reviews:0})
const target = {kind:'submission',studentUserId:studentID,category:'moral',itemKey:'activity',targetId:row.id,proposedScore:2,basis:'经核对实际活动参与范围与加分记录不符，请复核'}
const report = await call('POST','/reports',target,group)
assert.ok(report.id)
assert.ok((await call('GET','/reports/mine',undefined,group)).items.some((r)=>r.id===report.id))
assert.deepEqual((await call('GET','/reports/mine',undefined,student)).items,[])
const objection = await call('POST','/review/objections',target,group2)
assert.ok(objection.id)
await call('DELETE',`/review/objections/${objection.id}`,undefined,group2)
await call('POST',`/admin/submissions/${row.id}/force-score`,{score:3,previousScore:4.125,reason:'核对活动记录后纠正分数'},admin.token)
const corrected = (await call('GET',`/submissions/${row.id}`,undefined,student)).submission
assert.equal(corrected.finalScore,3); assert.equal(corrected.source,'admin_grant')
const own = (await list())[0].members.find((m) => m.studentId===adminID)
await call('POST',`/admin/submissions/${own.id}/force-score`,{score:3,previousScore:4.125,reason:'本人仍须回避终裁'},admin.token,403)

for (const [itemKey,claim,expected] of [['quantity',{quantity:20},5],['tier',{option:'省级',score:4},4],['condition',{score:2.5},2.5]]) {
  assert.equal((await call('POST',endpoint,request([unregisteredID],{itemKey,claim}),admin.token)).score,expected)
}
// Staged evidence is private; all copies enter the same atomic grant transaction.
const upload = await call('POST','/admin/bonus-grant-uploads',undefined,admin.token)
const uploadPath = `/admin/bonus-grant-uploads/${upload.id}/notes`
const pdf = Buffer.from('%PDF-1.4\nSynthetic bonus evidence\n%%EOF')
const fileInput = {filename:'统一活动佐证.pdf',mediaType:'application/pdf',sizeBytes:pdf.length}
for (const token of [student,group]) await call('POST','/admin/bonus-grant-uploads',undefined,token,403)
await call('POST',`${uploadPath}/presign`,fileInput,other.token,404)
const pre = await call('POST',`${uploadPath}/presign`,fileInput,admin.token)
await call('POST',endpoint,request([studentID],{uploadId:upload.id}),admin.token,409)
const form = new FormData()
for (const [key,value] of Object.entries(pre.uploadFields)) form.append(key,value)
form.append('file',new Blob([pdf],{type:'application/pdf'}),fileInput.filename)
assert.ok((await fetch(pre.uploadUrl,{method:'POST',body:form})).ok)
await call('POST',`${uploadPath}/${pre.evidenceId}/complete`,undefined,admin.token)
await call('POST',`${uploadPath}/${pre.evidenceId}/complete`,undefined,admin.token)
await call('GET',`/evidence/${pre.evidenceId}/url`,undefined,student,403)
await call('GET',`/evidence/${pre.evidenceId}/url`,undefined,group,403)
await call('POST',endpoint,request([studentID,foreignID],{uploadId:upload.id}),admin.token,422)
const attachmentBatch = request([studentID,unregisteredID,adminID],{uploadId:upload.id,title:'带佐证的统一活动'})
const savedAttachments = await call('POST',endpoint,attachmentBatch,admin.token)
await call('POST',endpoint,attachmentBatch,admin.token)
const withFiles = (await list()).find(row=>row.id===savedAttachments.id)
assert.equal(withFiles.evidence.length,1)
for (const member of withFiles.members) {
  const material = (await call('GET',`/admin/submissions?id=${member.id}`,undefined,admin.token)).items[0]
  assert.equal(material.evidence.length,1)
  const link = await call('GET',`/evidence/${material.evidence[0].id}/url`,undefined,admin.token)
  assert.deepEqual(Buffer.from(await (await fetch(link.url)).arrayBuffer()),pdf)
}
const studentGrant = withFiles.members.find(row=>row.studentId===studentID)
const studentMaterial = await call('GET',`/submissions/${studentGrant.id}`,undefined,student)
assert.equal(studentMaterial.evidence.length,1)
await call('GET',`/evidence/${studentMaterial.evidence[0].id}/url`,undefined,student)
await call('GET',`/evidence/${studentMaterial.evidence[0].id}/url`,undefined,group)
await call('GET',`/evidence/${studentMaterial.evidence[0].id}/url`,undefined,other.token,404)
assert.equal((await call('GET',`/review/score-history/submission/${studentGrant.id}`,undefined,group)).evidence.length,1)
await call('POST',endpoint,request([studentID],{uploadId:upload.id}),admin.token,403)
await call('DELETE',`${uploadPath}/${pre.evidenceId}`,undefined,admin.token,403)
const disposable = await call('POST','/admin/bonus-grant-uploads',undefined,admin.token)
const disposablePath = `/admin/bonus-grant-uploads/${disposable.id}/notes`
const removable = await call('POST',`${disposablePath}/presign`,fileInput,admin.token)
const removeForm = new FormData()
for (const [key,value] of Object.entries(removable.uploadFields)) removeForm.append(key,value)
removeForm.append('file',new Blob([pdf],{type:'application/pdf'}),fileInput.filename)
assert.ok((await fetch(removable.uploadUrl,{method:'POST',body:removeForm})).ok)
await call('POST',`${disposablePath}/${removable.evidenceId}/complete`,undefined,admin.token)
await call('DELETE',`${disposablePath}/${removable.evidenceId}`,undefined,admin.token)
await call('GET',`/evidence/${removable.evidenceId}/url`,undefined,admin.token,404)
console.log('bonus_evidence=ok: upload completion, private staging, batch copies, replay, ownership, removal and history')
// Direct grants remain available after material intake closes, but never after lockdown.
await call('PUT','/admin/window',{open,close:new Date(Date.now()-60000).toISOString()},admin.token)
await call('POST',endpoint,request([studentID],{title:'封存后统一加分'}),admin.token)

async function waitFor(check, label) {
  const until = Date.now()+60000
  do {
    const value = await check()
    if (value) return value
    await new Promise((resolve) => setTimeout(resolve,400))
  } while (Date.now()<until)
  throw new Error(`Timed out: ${label}`)
}
await call('POST','/admin/gpa/paste',{text:roster.map((m) => `${m.sid},85`).join('\n')},admin.token)
assert.equal(spawnSync('docker',['restart','easygpa-plus-e2e-workers-1'],{encoding:'utf8'}).status,0)
const live = await call('GET','/me/scorecard',undefined,student)
await call('POST','/me/scorecard/confirm',{revision:live.revision},student)
const settlement=await call('POST','/admin/settle',undefined,admin.token)
await call('POST',endpoint,request([studentID],{title:'结算后补录统一加分'}),admin.token)
const changed=await call('GET','/me/scorecard',undefined,student)
assert.equal(changed.confirmation.confirmed,false)
assert.equal(changed.confirmation.recheck,true)
assert.notEqual(changed.revision,live.revision)
assert.match(String(settlement.runId),/^\d+$/)
const invalidated=spawnSync('docker',['exec','easygpa-plus-e2e-postgres-1','psql','-U','easygpa','-d','easygpa_e2e','-At','-c',`SELECT count(*) FROM settlement_invalidation WHERE run_id=${settlement.runId}`],{encoding:'utf8'})
assert.equal(invalidated.status,0); assert.equal(Number(invalidated.stdout.trim()),1)
console.log('bonus_grants_invalidation=ok: settlement invalidated, affected confirmation revoked, live revision changed')

await call('PUT','/admin/window',{open,close:new Date(Date.now()-120000).toISOString(),lockdown:new Date(Date.now()-60000).toISOString()},admin.token)
await call('POST',endpoint,request(),admin.token,409)
await call('POST','/admin/bonus-grant-uploads',undefined,admin.token,409)
await call('PUT','/admin/window',{open,close,lockdown:null},admin.token)
console.log('bonus_grants=ok: atomic batches, replay protection, self/unregistered grants, tenant and role guards, rule bounds, source visibility, disputes, corrections, lockdown')
if (process.env.E2E_BROWSER_FIXTURE) await writeFile(process.env.E2E_BROWSER_FIXTURE,JSON.stringify({sid:admin.sid,password,studentSid:`BS${stamp}`,groupSid:`BG1${stamp}`}))
