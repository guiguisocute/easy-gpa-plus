import assert from 'node:assert/strict'
import test from 'node:test'
import { diffRule, findCurrentItem, hasRuleDrift } from './ruleDiff.ts'
import type { SchemeConfig, SchemeItem } from './types.ts'

const enumItem = (options: { label: string; score: number }[], extra: Partial<SchemeItem> = {}): SchemeItem => ({
  key: 'practice_contest',
  name: '与专业学习相关的学科竞赛获奖',
  scoreRule: { type: 'enum', levels: ['级别'], options },
  ...extra,
})

const perUnit = (per: number, cap?: number): SchemeItem => ({
  key: 'moral_volunteer',
  name: '青年志愿者服务',
  scoreRule: { type: 'per_unit', unit: '小时', per, ...(cap == null ? {} : { cap }) },
})

test('规则没动时不报变更', () => {
  const item = enumItem([{ label: '省级·一等', score: 15 }])
  assert.equal(hasRuleDrift(diffRule(item, item)), false)
})

test('档位分值变化逐档列出，方向不能反', () => {
  const frozen = enumItem([{ label: '省级·一等', score: 15 }, { label: '省级·二等', score: 12 }])
  const current = enumItem([{ label: '省级·一等', score: 8 }, { label: '省级·二等', score: 12 }])
  const diff = diffRule(frozen, current)
  assert.equal(diff.changes.length, 1)
  assert.equal(diff.changes[0].label, '档位「省级·一等」')
  // was 必须是冻结值（据此定分），now 是现行值
  assert.equal(diff.changes[0].was, '15 分')
  assert.equal(diff.changes[0].now, '8 分')
})

test('档位被删和新增都要报出来', () => {
  const frozen = enumItem([{ label: '省级·鼓励', score: 6 }])
  const current = enumItem([{ label: '省级·参与', score: 1 }])
  const diff = diffRule(frozen, current)
  const labels = diff.changes.map((c) => `${c.label}:${c.was}→${c.now}`)
  assert.deepEqual(labels, ['档位「省级·鼓励」:6 分→已删除', '档位「省级·参与」:（当时没有）→1 分'])
})

test('按量小项的单位分值与封顶变化', () => {
  const diff = diffRule(perUnit(0.5), perUnit(0.25, 10))
  assert.deepEqual(diff.changes.map((c) => c.label), ['单位分值', '小项封顶'])
  assert.equal(diff.changes[0].was, '每 小时 0.5 分')
  assert.equal(diff.changes[1].now, '10 分')
})

test('计分方式整个换掉时只报一条，不再逐档比', () => {
  const frozen = perUnit(0.5)
  const current = enumItem([{ label: 'x', score: 1 }], { key: 'moral_volunteer', name: '青年志愿者服务' })
  const diff = diffRule(frozen, current)
  assert.equal(diff.changes.length, 1)
  assert.equal(diff.changes[0].label, '计分方式')
})

test('小项已从现行方案移除', () => {
  const diff = diffRule(enumItem([]), null)
  assert.equal(diff.removed, true)
  assert.equal(diff.changes.length, 0)
  assert.equal(hasRuleDrift(diff), true)
})

test('说明文字改动不算变更——否则每条都跳警告，久了没人看', () => {
  const frozen = enumItem([{ label: 'a', score: 1 }], { note: '旧说明' })
  const current = enumItem([{ label: 'a', score: 1 }], { note: '新说明，长得多的一段文字' })
  assert.equal(hasRuleDrift(diffRule(frozen, current)), false)
})

test('互斥组与共享封顶的变化要报', () => {
  const frozen = enumItem([{ label: 'a', score: 1 }], { capGroup: { key: 'g', cap: 5 } })
  const current = enumItem([{ label: 'a', score: 1 }], { capGroup: { key: 'g', cap: 10 }, exclusiveGroup: 'x' })
  const diff = diffRule(frozen, current)
  assert.deepEqual(diff.changes.map((c) => c.label).sort(), ['共享封顶', '互斥组'].sort())
})

test('findCurrentItem 跨大项按 key 找，找不到返回 null', () => {
  const config = {
    schemeName: 's', version: '1', weights: {},
    categories: [
      { key: 'moral', name: '思想道德', maxTotal: 100, baseItems: [], penaltyItems: [], items: [perUnit(0.25, 10)] },
    ],
  } as unknown as SchemeConfig
  assert.equal(findCurrentItem(config, 'moral_volunteer')?.key, 'moral_volunteer')
  assert.equal(findCurrentItem(config, 'nope'), null)
  assert.equal(findCurrentItem(undefined, 'moral_volunteer'), null)
})
