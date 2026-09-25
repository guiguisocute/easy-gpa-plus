import assert from 'node:assert/strict'
import test from 'node:test'
import { claimText, delta, readableText, ruleText, score } from './format.ts'

test('坏编码文本不把替换符摊到界面上', () => {
  assert.equal(readableText('冒烟测试：允许在条件未全满足时结算'), '冒烟测试：允许在条件未全满足时结算')
  assert.equal(readableText('����为验证'), '（原文编码已损坏）')
  assert.equal(readableText('正常理由'), '正常理由')
  assert.equal(readableText(''), '')
})

test('分数展示保留两位小数，避免把 0.25 显示成 0.3', () => {
  assert.equal(score(0.25), '0.25')
  assert.equal(score(3), '3.0')
  assert.equal(score(3.5), '3.5')
  assert.equal(delta(-0.25), '-0.25')
  assert.equal(claimText({ score: 0.25 }), '自报 0.25 分')
  assert.equal(claimText({ option: '主要成员', score: 2 }), '主要成员 · 期望 2.0 分')
  assert.equal(ruleText({ type: 'enum', options: [{ label: '主要成员', score: 4 }] }), '主要成员 · 建议 4 分')
})
