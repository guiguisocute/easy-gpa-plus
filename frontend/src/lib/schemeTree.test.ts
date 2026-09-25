import assert from 'node:assert/strict'
import test from 'node:test'
import { attachClaimableBase, automaticBaseScore, claimableBaseProgress, studentClaimBaseItem } from './schemeTree.ts'
import type { SchemeCategory, SchemeConfig } from './types.ts'

test('自动基础分只统计无需学生申报的基础项', () => {
  const category: SchemeCategory = {
    key: 'moral',
    name: '思想道德素质',
    maxTotal: 100,
    items: [],
    penaltyItems: [],
    baseItems: [
      { key: 'a', name: '日常表现', full: 15 },
      { key: 'b', name: '集体活动', full: 10 },
      { key: 'c', name: '条件基础项', full: 40, studentClaim: { minimum: 2, unit: '项' } },
    ],
  }
  assert.equal(automaticBaseScore(category), 25)
})

test('需申报的基础项区分未申报、草稿、审核和已认定', () => {
  assert.deepEqual(claimableBaseProgress([], 40), { state: 'unsubmitted', score: 0, hasDecision: false })
  assert.deepEqual(claimableBaseProgress([{ status: 'draft', finalScore: null }], 40), { state: 'draft', score: 0, hasDecision: false })
  assert.deepEqual(claimableBaseProgress([{ status: 'pending', finalScore: null }], 40), { state: 'reviewing', score: 0, hasDecision: false })
  assert.deepEqual(claimableBaseProgress([{ status: 'appealing', finalScore: 32 }], 40), { state: 'appealing', score: 32, hasDecision: true })
  assert.deepEqual(
    claimableBaseProgress([{ status: 'scored', finalScore: 30 }, { status: 'locked', finalScore: 20 }], 40),
    { state: 'scored', score: 40, hasDecision: true },
  )
})

test('条件基础项转成申报项时带上醒目标记和学生可见说明', () => {
  const item = studentClaimBaseItem({
    key: 'practice_base',
    name: '参加课外学术科技活动',
    full: 40,
    studentClaim: { minimum: 2, unit: '项' },
    note: '竞赛、论文、专利均可计入。',
  })
  assert.ok(item)
  assert.deepEqual(item.claimableBase, { full: 40, minimum: 2, unit: '项' })
  assert.equal(item.scoreRule.type, 'free')
  assert.match(item.note ?? '', /完整达标：至少 2 项可得 40 分/)
  assert.match(item.note ?? '', /竞赛、论文、专利均可计入/)
  assert.equal(studentClaimBaseItem({ key: 'plain', name: '日常', full: 15 }), null)
})

test('规则快照对照当前方案后，条件基础项会重新带上醒目标记', () => {
  const config = {
    schemeName: 't',
    version: 'v1',
    window: { open: '', close: '' },
    capabilities: {
      submit: true, edit: true, appeal: true, review: true, arbitrate: true,
      export: { on: false, gate: 'settlement' },
    },
    weights: { major: 0.6, moral: 0.15, practice: 0.15, health: 0.1 },
    honorRoll: { topPercent: 30 },
    categories: [{
      key: 'practice',
      name: '实践创新素质',
      maxTotal: 100,
      items: [],
      penaltyItems: [],
      baseItems: [{ key: 'practice_base', name: '参加课外学术科技活动', full: 40, studentClaim: { minimum: 2, unit: '项' } }],
    }],
  } as SchemeConfig
  const frozen = { key: 'practice_base', name: '参加课外学术科技活动', scoreRule: { type: 'free' as const, min: 0, max: 40 } }
  assert.deepEqual(attachClaimableBase(frozen, config).claimableBase, { full: 40, minimum: 2, unit: '项' })
  assert.equal(attachClaimableBase(frozen, undefined).claimableBase, undefined)
})
