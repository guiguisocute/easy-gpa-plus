import assert from 'node:assert/strict'
import test from 'node:test'
import { effectiveEvidenceTypes, evidenceMediaType, validateEvidenceFile } from './evidence.ts'

test('显式 PDF/JPG/PNG 白名单不再静默扩展', () => {
  const types = effectiveEvidenceTypes(['pdf', 'jpg', 'png'])
  assert.deepEqual(types, ['pdf', 'jpg', 'png'])
})

test('管理员显式配置的窄白名单保持严格', () => {
  assert.deepEqual(effectiveEvidenceTypes(['pdf']), ['pdf'])
  assert.match(
    validateEvidenceFile({ name: 'proof.docx', size: 1024 }, { required: true, types: ['pdf'], maxMb: 10 }) ?? '',
    /不受支持/,
  )
})

test('未配置白名单时平台默认包含常用办公格式与 MP4', () => {
  const types = effectiveEvidenceTypes(undefined)
  assert.ok(types.includes('docx'))
  assert.ok(types.includes('zip'))
  assert.ok(types.includes('mp4'))
})

test('浏览器没有提供 MIME 时按扩展名补齐', () => {
  assert.equal(
    evidenceMediaType({ name: 'proof.docx', type: '' }),
    'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  )
  assert.equal(evidenceMediaType({ name: 'archive.unknown', type: '' }), 'application/octet-stream')
})
