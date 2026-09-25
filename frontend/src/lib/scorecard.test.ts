import assert from 'node:assert/strict'
import test from 'node:test'
import { isScorecardReviewItem } from './scorecard.ts'

test('成绩明细过滤强制驳回与本人主动驳回，但保留普通零分、负分及强制改分', () => {
  const rows = [
    { id: 'rejected', score: 0, forceRejection: { reason: '材料无效' } },
    { id: 'self', score: 0, forceRejection: { selfRejected: true } },
    { id: 'zero', score: 0, forceRejection: null },
    { id: 'negative', score: -2 },
    { id: 'adjusted', score: 0, forcedScore: { previousScore: 5 } },
  ]
  assert.deepEqual(rows.filter(isScorecardReviewItem).map((row) => row.id), ['zero', 'negative', 'adjusted'])
  assert.equal(rows.length, 5, '历史快照原文保持不变')
})
