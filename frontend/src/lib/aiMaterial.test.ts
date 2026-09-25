import assert from 'node:assert/strict'
import test from 'node:test'
import type { AICandidate } from '../api/types.ts'
import type { ScoreRule } from './types.ts'
import { applyProblem, chooseEnumClaim, resetClaim, type AIMaterialItem } from './aiMaterial.ts'

const competitionRule: Extract<ScoreRule, { type: 'enum' }> = {
  type: 'enum',
  options: [
    { label: '国家级 A2 类一等', score: 20 },
    { label: '国家级 A3 类一等', score: 15 },
  ],
}

function items(rule: ScoreRule = competitionRule): AIMaterialItem[] {
  return [{
    categoryKey: 'practice',
    categoryName: '实践创新',
    item: { key: 'competition', name: '学科竞赛', scoreRule: rule },
  }]
}

function candidate(change: Partial<AICandidate> = {}): AICandidate {
  return {
    id: 'candidate-example',
    categoryKey: 'practice',
    itemKey: 'competition',
    title: '示例竞赛全国总决赛一等奖',
    assets: [{ assetId: 'asset-example' }],
    claim: { option: '国家级 A2 类一等', score: 20 },
    note: '本人获全国总决赛一等奖。',
    alternatives: [],
    confidence: 0.6,
    needsReview: true,
    reviewReasons: ['学校认定类别待核对', '请核对是否属于本次评定学年'],
    expectedScore: 20,
    ...change,
  }
}

test('手动从 A2 改为 A3 时档位和建议分同步更新，不受旧待核对提醒阻塞', () => {
  const original = candidate()
  const edited = { ...original, claim: chooseEnumClaim(competitionRule, '国家级 A3 类一等'), expectedScore: null }

  assert.deepEqual(edited.claim, { option: '国家级 A3 类一等', score: 15 })
  assert.equal(applyProblem([edited], items()), null)
  assert.deepEqual(edited.reviewReasons, original.reviewReasons)
  assert.deepEqual(original.claim, { option: '国家级 A2 类一等', score: 20 })
})

test('AI 缺少档位时不自动选第一档，手动补充合法档位后可创建', () => {
  const original = candidate({ claim: {}, expectedScore: null })
  assert.deepEqual(chooseEnumClaim(competitionRule, ''), {})
  assert.equal(applyProblem([original], items()), null)
  const edited = { ...original, claim: chooseEnumClaim(competitionRule, '国家级 A3 类一等') }
  assert.deepEqual(edited.claim, { option: '国家级 A3 类一等', score: 15 })
  assert.equal(applyProblem([edited], items()), null)
})

test('AI 的未知档位仍需修正，但改为当前方案档位或主动留空后可创建', () => {
  const original = candidate({ claim: { option: '不存在的档位', score: 99 } })
  assert.match(applyProblem([original], items())!, /档次不在当前方案/)
  assert.deepEqual(chooseEnumClaim(competitionRule, '不存在的档位'), {})

  const corrected = { ...original, claim: chooseEnumClaim(competitionRule, '国家级 A3 类一等') }
  assert.equal(applyProblem([corrected], items()), null)
  const cleared = { ...original, claim: chooseEnumClaim(competitionRule, '') }
  assert.equal(applyProblem([cleared], items()), null)
})

test('本人可修改档位建议分，合法期望分不要求等于档位默认分', () => {
  const edited = candidate({ claim: { ...chooseEnumClaim(competitionRule, '国家级 A3 类一等'), score: 12 } })
  assert.equal(applyProblem([edited], items()), null)
  assert.deepEqual(resetClaim(competitionRule, edited.claim), { option: '国家级 A3 类一等', score: 12 })
})

test('待核对标记、低置信度和旧预估分不作为创建草稿的权威校验', () => {
  const edited = candidate({
    confidence: 0,
    needsReview: true,
    expectedScore: 999,
    claim: { option: '国家级 A3 类一等', score: 15 },
  })
  assert.equal(applyProblem([edited], items()), null)
})

test('真实非法档位分值仍然被拦截，不能靠修改待核对标记绕过', () => {
  for (const score of [-1, 21, Infinity, NaN]) {
    const edited = candidate({ needsReview: false, reviewReasons: [], claim: { option: '国家级 A3 类一等', score } })
    assert.match(applyProblem([edited], items())!, /期望加分必须在 0—20/)
  }
})

test('无效小项、标题、缺失材料与空批次仍然被拦截', () => {
  assert.match(applyProblem([], items())!, /至少保留/)
  assert.match(applyProblem([candidate({ itemKey: 'missing' })], items())!, /有效小项/)
  assert.match(applyProblem([candidate({ title: ' ' })], items())!, /事项名称/)
  assert.match(applyProblem([candidate({ assets: [] })], items())!, /没有佐证/)
})

test('数量和自报分规则继续保留真实非法值校验', () => {
  const quantityRule: ScoreRule = { type: 'per_unit', unit: '小时', per: 1 }
  for (const quantity of [-1, Infinity, NaN]) {
    assert.match(applyProblem([candidate({ claim: { quantity } })], items(quantityRule))!, /数量必须是非负数字/)
  }
  const freeRule: ScoreRule = { type: 'free', min: 0, max: 5 }
  for (const score of [-1, 6, Infinity, NaN]) {
    assert.match(applyProblem([candidate({ claim: { score } })], items(freeRule))!, /自报分必须在 0—5/)
  }
  assert.equal(applyProblem([candidate({ claim: {} })], items(quantityRule)), null)
  assert.equal(applyProblem([candidate({ claim: {} })], items(freeRule)), null)
})

test('切换小项清理不适用的旧字段，未知档位不被默认替换成第一档', () => {
  assert.deepEqual(resetClaim(competitionRule, { option: '旧方案档位', quantity: 3, score: 99 }), {})
  assert.deepEqual(resetClaim(competitionRule, { option: '国家级 A3 类一等', quantity: 3 }), { option: '国家级 A3 类一等', score: 15 })
  assert.deepEqual(resetClaim({ type: 'per_unit', unit: '次', per: 1 }, { option: '旧方案档位', quantity: 3, score: 99 }), { quantity: 3 })
})
