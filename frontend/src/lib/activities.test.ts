import assert from 'node:assert/strict'
import test from 'node:test'
import { parseActivitiesText } from './activities.ts'

test('解析带 activities 前缀的 JSON 代码片段（如微信截图场景）', () => {
  const input = [
    '"activities": [',
    '  { "name": "2025 年秋季运动会", "date": "2025.11", "level": "校级", "org": "院团委学生会", "score": "组织者 2 分" },',
    '  { "name": "2026 年春季运动会", "date": "2026.5", "level": "校级", "org": "院团委学生会", "score": "组织者 2 分" },',
    '  { "name": "学代会", "date": "2025.11.15", "level": "院级", "org": "生活权益部", "score": "组织者 +0.25" }',
    ']',
  ].join('\n')
  const result = parseActivitiesText(input)
  assert.equal(result.length, 3)
  assert.equal(result[0].name, '2025 年秋季运动会')
  assert.equal(result[0].date, '2025.11')
  assert.equal(result[0].level, '校级')
  assert.equal(result[0].org, '院团委学生会')
  assert.equal(result[0].score, '组织者 2 分')
  assert.equal(result[2].name, '学代会')
  assert.equal(result[2].score, '组织者 +0.25')
})

test('解析标准 JSON 数组', () => {
  const input = JSON.stringify([
    { name: '志愿者活动', score: '加 1 分' },
    { name: '程序设计竞赛', level: '省级', date: '2026.04' },
  ])
  const result = parseActivitiesText(input)
  assert.equal(result.length, 2)
  assert.equal(result[0].name, '志愿者活动')
  assert.equal(result[0].score, '加 1 分')
  assert.equal(result[0].date, undefined)
  assert.equal(result[1].name, '程序设计竞赛')
  assert.equal(result[1].level, '省级')
})

test('解析 Excel 制表符行', () => {
  const input = [
    '2025 年秋季运动会\t2025.11\t校级\t院团委学生会\t组织者 2 分',
    '公文培训大会\t2025.10.18\t院级\t综合服务部\t组织者 1 分',
  ].join('\n')
  const result = parseActivitiesText(input)
  assert.equal(result.length, 2)
  assert.equal(result[0].name, '2025 年秋季运动会')
  assert.equal(result[0].org, '院团委学生会')
  assert.equal(result[1].name, '公文培训大会')
  assert.equal(result[1].date, '2025.10.18')
})

test('解析纯文本每行一个活动', () => {
  const input = '活动 A\n活动 B\n活动 C'
  const result = parseActivitiesText(input)
  assert.equal(result.length, 3)
  assert.deepEqual(result.map((a) => a.name), ['活动 A', '活动 B', '活动 C'])
})

test('空输入返回空数组', () => {
  assert.deepEqual(parseActivitiesText('   \n  '), [])
})
