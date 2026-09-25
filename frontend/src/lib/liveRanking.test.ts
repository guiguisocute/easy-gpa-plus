import assert from 'node:assert/strict'
import test from 'node:test'
import { orderedRanking, rankingPlace, rankingSummary, type RankingRow } from './liveRanking.ts'

const row = (sid: string, value: number | null, rank?: number): RankingRow => ({ sid, name: `成员${sid}`, total: value, scored: 1, pending: 0, conflicts: 0, categoryScores: { practice: value }, categoryRanks: rank ? { practice: rank } : {} })

test('ranking preserves server ties and orders missing scores after zero and negative scores', () => {
  const rows = [row('5', null), row('3', 0, 3), row('2', 8, 1), row('1', 8, 1), row('4', -2, 4)]
  const ordered = orderedRanking(rows, 'practice')
  assert.deepEqual(ordered.map((entry) => entry.sid), ['1', '2', '3', '4', '5'])
  assert.deepEqual(ordered.map((entry) => rankingPlace(entry, 'practice')), [1, 1, 3, 4, null])
  assert.equal(rankingPlace(ordered[0], 'total'), null, 'never invent a total rank while GPA is incomplete')
  assert.equal(rows[0].sid, '5', 'does not mutate the query cache')
  assert.deepEqual(orderedRanking(rows, 'practice', ' 成员3 ').map((entry) => entry.sid), ['3'])
})

test('summary excludes missing grades and retains real zeros and negative scores', () => {
  assert.deepEqual(rankingSummary([row('1', null), row('2', 0), row('3', -4)], 'practice'), { count: 2, average: -2, max: 0, min: -4 })
  assert.deepEqual(rankingSummary([], 'total'), { count: 0, average: null, max: null, min: null })
})
