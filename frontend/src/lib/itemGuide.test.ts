import assert from 'node:assert/strict'
import test from 'node:test'
import { claimableBaseNote, itemGuideMarkdown, plainNoteToMarkdown } from './itemGuide.ts'

test('条件基础分说明按条列出达标与未达标口径', () => {
  assert.equal(
    claimableBaseNote({ full: 40, minimum: 2, unit: '项' }),
    '- 完整达标：至少 2 项可得 40 分。\n- 未完全达标时，仍可依据已有材料在 0—40 分内自报，最终由审核人认定。',
  )
})

test('旧方案用分号串起来的备注拆成 Markdown 列表', () => {
  assert.equal(
    plainNoteToMarkdown('各项职务加分不累加，取最高；只任职一学期者减半。以缴费形式参加的兴趣社团不加分。'),
    '- 各项职务加分不累加，取最高\n- 只任职一学期者减半\n- 以缴费形式参加的兴趣社团不加分',
  )
  assert.equal(plainNoteToMarkdown('须出示当年献血证，班级全体班委认证。'), '须出示当年献血证，班级全体班委认证。')
  assert.equal(
    plainNoteToMarkdown('- 已经是列表\n- 第二项'),
    '- 已经是列表\n- 第二项',
  )
})

test('提交页说明不带互斥组 key，共享上限只在备注没写时补一句', () => {
  assert.equal(
    itemGuideMarkdown({ note: '各项职务加分不累加，取最高；只任职一学期者减半。' }),
    '- 各项职务加分不累加，取最高\n- 只任职一学期者减半',
  )
  assert.equal(
    itemGuideMarkdown({ capGroup: { key: 'moral_news', cap: 5 } }),
    '- 与相关小项合计不超过 5 分。',
  )
  assert.equal(
    itemGuideMarkdown({ note: '可累加，上限 10 分。', capGroup: { key: 'health_performance', cap: 10 } }),
    '可累加，上限 10 分。',
  )
})
