import assert from 'node:assert/strict'
import test from 'node:test'
import { buildClaim, expected, readClaim } from './claim.ts'
import type { ScoreRule } from './types.ts'

const enumRule: ScoreRule = {
  type: 'enum',
  options: [
    { label: '主要成员', score: 4 },
    { label: '其他成员', score: 2 },
  ],
}

test('留空的自报分和数量保持缺省，填零与待审核认定严格区分', () => {
  for (const rule of [{ type: 'free', min: 0, max: 12 }, { type: 'per_unit', per: 0.5, unit: '小时' }] satisfies ScoreRule[]) {
    assert.deepEqual(buildClaim(rule, ' ', 0, ' '), {})
    assert.deepEqual(expected(rule, ' ', 0, ' '), { raw: null, final: null, capped: false })
    assert.deepEqual(readClaim(rule, {}), { qty: '', level: 0, free: '' })
  }
  assert.deepEqual(buildClaim({ type: 'free', min: 0, max: 12 }, '', 0, '0'), { score: 0 })
})

test('按档规则以档位分为建议值，并允许学生调整期望分', () => {
  assert.deepEqual(expected(enumRule, '', 0, ''), { raw: 4, final: 4, capped: false })
  assert.deepEqual(expected(enumRule, '', 0, '2'), { raw: 2, final: 2, capped: false })
  assert.deepEqual(buildClaim(enumRule, '', 0, '2'), { option: '主要成员', score: 2 })
})

test('旧按档草稿没有期望分时，用所选档位建议分补齐', () => {
  assert.deepEqual(readClaim(enumRule, { option: '其他成员' }), { qty: '', level: 1, free: '2' })
})

test('按档期望分沿用审核认定范围并在预览中提示越界', () => {
  assert.deepEqual(expected(enumRule, '', 1, '9'), { raw: 9, final: 4, capped: true })
  assert.deepEqual(expected(enumRule, '', 1, '-1'), { raw: -1, final: 0, capped: true })
})
