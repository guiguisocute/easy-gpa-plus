import assert from 'node:assert/strict'
import test from 'node:test'
import { isAdjudicationRecused } from './adjudication.ts'

test('服务端禁止裁定时，即使对象不是本人也禁用单条与批量操作', () => {
  assert.equal(isAdjudicationRecused({ canAdjudicate: false }, 'admin-sid', 'deputy-sid'), true)
})

test('本人事项在旧响应或过时权限数据中仍回避，副班管可处理班管事项', () => {
  assert.equal(isAdjudicationRecused({}, 'admin-sid', 'admin-sid'), true)
  assert.equal(isAdjudicationRecused({ canAdjudicate: true }, 'admin-sid', 'admin-sid'), true)
  assert.equal(isAdjudicationRecused({ canAdjudicate: true }, 'admin-sid', 'deputy-sid'), false)
  assert.equal(isAdjudicationRecused({}, 'student-sid', 'admin-sid'), false)
})
