// Regression for optional self-reported scores. Uses only the disposable local
// stack, synthetic accounts and real Garage uploads; never contacts production.
import assert from 'node:assert/strict'
import { assertLocalDevelopmentAPI, e2eHeaders, requireLoopbackOrigin } from './support/local-environment.mjs'

const origin = requireLoopbackOrigin('API_BASE', process.env.API_BASE ?? 'http://127.0.0.1:48080')
await assertLocalDevelopmentAPI(origin)
const stamp = Date.now().toString(36)
async function call(method, path, body, token, expectedStatus) {
  const res = await fetch(`${origin}/api/v1${path}`, {
    method,
    headers: e2eHeaders({ 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = res.status === 204 ? null : await res.json()
  assert.ok(expectedStatus ? res.status === expectedStatus : res.ok, `${method} ${path}: ${res.status} ${data?.code ?? ''} ${data?.message ?? ''}`)
  return data
}
async function register(sid, name) {
  const { ticket } = await call('POST', '/auth/register/check', { sid, name })
  const session = await call('POST', '/auth/register/complete', { ticket, password: 'local-claims-test-password' })
  return session.access_token
}

const ops = await call('POST', '/auth/login', {
  account: process.env.OPS_ACCOUNT ?? 'ops@e2e.local', password: process.env.OPS_PASSWORD ?? 'easygpa-e2e-ops-password',
})
const adminSid = `CA${stamp}`
await call('POST', '/ops/tenants', { name: '申报留空回归班', slug: `claims-${stamp}`, adminSid, adminName: '回归班管' }, ops.access_token)
const admin = await register(adminSid, '回归班管')
await call('POST', '/admin/whitelist/import', { csv: `sid,name,role\nCS${stamp},回归学生,student` }, admin)
const student = await register(`CS${stamp}`, '回归学生')
const evidence = { required: true, types: ['pdf'], maxMb: 10 }
const draft = await call('POST', '/admin/scheme', {
  name: '申报回归方案',
  config: {
    schemeName: '申报回归方案', weights: { moral: 1 },
    categories: [{ key: 'moral', name: '思想道德', maxTotal: 100, penaltyItems: [],
      baseItems: [{ key: 'base', name: '条件基础项', full: 5, studentClaim: { minimum: 2, unit: '项', evidence } }],
      items: [
        { key: 'free', name: '自报分测试', scoreRule: { type: 'free', min: 0, max: 10 }, evidence },
        { key: 'quantity', name: '数量测试', scoreRule: { type: 'per_unit', unit: '小时', per: 0.5 }, evidence },
        { key: 'enum', name: '档位测试', scoreRule: { type: 'enum', options: [{ label: '校级', score: 3 }] }, evidence },
        { key: 'threshold', name: '达标测试', scoreRule: { type: 'threshold', unit: '项', minimum: 2, award: 5 }, evidence },
      ],
    }],
  },
}, admin)
await call('POST', `/admin/scheme/${draft.id}/publish`, undefined, admin)
await call('PUT', '/admin/window', { open: new Date(Date.now() - 3600_000).toISOString(), close: new Date(Date.now() + 86400_000).toISOString() }, admin)
await call('PUT', '/admin/window/capabilities', { key: 'submit', on: true }, admin)
await call('PUT', '/admin/window/capabilities', { key: 'edit', on: true }, admin)

const pdf = new Blob(['%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n'], { type: 'application/pdf' })
async function upload(id) {
  const pre = await call('POST', `/submissions/${id}/evidence/presign`, { filename: '回归佐证.pdf', mediaType: pdf.type, sizeBytes: pdf.size }, student)
  const form = new FormData()
  for (const [key, value] of Object.entries(pre.uploadFields)) form.append(key, value)
  form.append('file', pdf, '回归佐证.pdf')
  const res = await fetch(pre.uploadUrl, { method: 'POST', body: form })
  assert.ok(res.ok, `Garage upload: ${res.status}`)
  await call('POST', `/submissions/${id}/evidence/${pre.evidenceId}/complete`, undefined, student)
}

for (const [itemKey, claim, expected] of [
  ['free', {}, null], ['free', { score: null }, null], ['quantity', {}, null], ['base', {}, null], ['free', { score: 0 }, 0],
]) {
  const body = { category: 'moral', itemKey, title: '留空待审核认定', claim, note: '' }
  const created = await call('POST', '/submissions', body, student)
  // Saving, editing and later submitting a draft must share the same policy.
  await call('PUT', `/submissions/${created.id}`, body, student)
  const missingEvidence = await call('POST', `/submissions/${created.id}/submit`, undefined, student, 422)
  assert.equal(missingEvidence.code, 'evidence_required')
  await upload(created.id)
  await call('POST', `/submissions/${created.id}/submit`, undefined, student)
  const detail = await call('GET', `/submissions/${created.id}`, undefined, student)
  assert.notEqual(detail.submission.status, 'draft')
  assert.equal(detail.submission.wantScore, expected)
  assert.equal(detail.submission.finalScore, null)
}
for (const [itemKey, claim, reason] of [
  ['free', { score: 11 }, 'claim_score_above_maximum'], ['free', { score: -1 }, 'claim_score_below_minimum'],
  ['quantity', { quantity: -1 }, 'claim_quantity_invalid'], ['enum', { option: '不存在的档位' }, 'claim_option_invalid'],
]) {
  const error = await call('POST', '/submissions', { category: 'moral', itemKey, title: '非法申报', claim }, student, 422)
  assert.equal(error.code, 'submission_invalid')
  assert.equal(error.detail.reason, reason)
  assert.match(error.message, /\p{Script=Han}/u)
}
for (const [itemKey, reason] of [['enum', 'claim_option_invalid'], ['threshold', 'claim_quantity_required']]) {
  const created = await call('POST', '/submissions', { category: 'moral', itemKey, title: '必填项待补充', claim: {} }, student)
  const error = await call('POST', `/submissions/${created.id}/submit`, undefined, student, 422)
  assert.equal(error.detail.reason, reason)
}
console.log('submission_claims=ok: blank/null stay pending, zero stays zero, evidence and invalid claims still enforced')

// Optional local browser handoff. The file is outside the repository, contains
// synthetic fixture data only, and is never printed or used by CI.
if (process.env.E2E_BROWSER_FIXTURE) {
  const { writeFile } = await import('node:fs/promises')
  await writeFile(process.env.E2E_BROWSER_FIXTURE, JSON.stringify({ sid: `CS${stamp}`, password: 'local-claims-test-password' }))
}
