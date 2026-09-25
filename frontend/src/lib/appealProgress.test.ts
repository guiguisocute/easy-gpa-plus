import assert from 'node:assert/strict'
import test from 'node:test'
import { needsMyRereview, rereviewNotice, rereviewProgressLabel } from './appealProgress.ts'
import type { Appeal } from '../api/types.ts'

const appeal: Pick<Appeal, 'round' | 'status' | 'handlers'> = {
  round: 1, status: 'reviewing',
  handlers: [{ id: 'me', mine: true, decided: false, rereview: null }, { id: 'peer', mine: false, decided: true, rereview: null }],
}

test('同一条 1/2 申诉在复评中是待办，提前终裁后不再显示待我或允许提交', () => {
  assert.equal(needsMyRereview(appeal), true)
  assert.equal(rereviewProgressLabel(appeal), '1/2 · 待我')
  const finalized = { ...appeal, status: 'final' as const }
  assert.equal(needsMyRereview(finalized), false)
  assert.equal(rereviewProgressLabel(finalized), '1/2 · 已结束')
  assert.match(rereviewNotice(finalized), /已终裁.*无需再提交/)
})

test('已提交、结案、升级终裁、第二次申诉及未分配事项均不能复评', () => {
  for (const status of ['filed', 'resolved', 'escalated'] as const) assert.equal(needsMyRereview({ ...appeal, status }), false)
  assert.equal(needsMyRereview({ ...appeal, round: 2 }), false)
  assert.equal(needsMyRereview({ ...appeal, handlers: [] }), false)
  assert.equal(needsMyRereview({ ...appeal, handlers: appeal.handlers.map((handler) => ({ ...handler, decided: true })) }), false)
})
