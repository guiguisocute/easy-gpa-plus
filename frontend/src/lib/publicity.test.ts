import assert from 'node:assert/strict'
import test from 'node:test'
import { publicityIsOpen } from './publicity.ts'

test('公示默认关闭，开始时开放，结束时关闭，无效时间不能开放原件', () => {
  const window = { open: '2026-09-10T08:00:00Z', close: '2026-09-10T09:00:00Z' }
  const start = Date.parse(window.open), end = Date.parse(window.close)
  assert.equal(publicityIsOpen(undefined, start), false)
  assert.equal(publicityIsOpen(null, start), false)
  assert.equal(publicityIsOpen(window, start - 1), false)
  assert.equal(publicityIsOpen(window, start), true)
  assert.equal(publicityIsOpen(window, end - 1), true)
  assert.equal(publicityIsOpen(window, end), false)
  assert.equal(publicityIsOpen({ open: 'invalid', close: window.close }, start), false)
})
