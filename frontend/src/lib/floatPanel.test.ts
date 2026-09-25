import assert from 'node:assert/strict'
import test from 'node:test'
import { clampFrame, moveFrame, resizeFrame } from './floatPanel.ts'

const start = { left: 100, top: 80, width: 400, height: 500 }

test('向右下拉大浮窗', () => {
  const next = resizeFrame(start, 'se', 40, 60, 1200, 900)
  assert.deepEqual(next, { left: 100, top: 80, width: 440, height: 560 })
})

test('向左上拉大时对边不动', () => {
  const next = resizeFrame(start, 'nw', -30, -20, 1200, 900)
  assert.equal(next.width, 430)
  assert.equal(next.height, 520)
  assert.equal(next.left + next.width, start.left + start.width)
  assert.equal(next.top + next.height, start.top + start.height)
})

test('缩到最小宽度时左缘不再跟着鼠标跑', () => {
  const next = resizeFrame(start, 'w', 300, 0, 1200, 900)
  assert.equal(next.width, 320)
  assert.equal(next.left + next.width, start.left + start.width)
})

test('拖出视口会被夹住', () => {
  const next = moveFrame(start, 2000, 2000, 800, 700)
  assert.equal(next.left, 400)
  assert.equal(next.top, 200)
})

test('视口变窄时整框仍落在屏幕里', () => {
  const next = clampFrame({ left: 500, top: 100, width: 430, height: 640 }, 360, 500)
  assert.equal(next.width, 360)
  assert.equal(next.height, 500)
  assert.equal(next.left, 0)
  assert.equal(next.top, 0)
})
