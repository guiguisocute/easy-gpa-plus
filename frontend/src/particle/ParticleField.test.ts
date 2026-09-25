import test from 'node:test'
import assert from 'node:assert/strict'
import { ParticleField } from './ParticleField.ts'
import { PointerInteraction } from './PointerInteraction.ts'
import { DEFAULT_CONFIG } from './config.ts'

const cfg = { ...DEFAULT_CONFIG, centerX: 0.5, centerY: 0.5, sizeW: 1, sizeH: 1, textGuardWidth: 0, ambientAlpha: 0, idleAmplitude: 0 }
function position(field: ParticleField) {
  const points: number[][] = []
  const ctx = { fillRect: (x: number, y: number) => points.push([x, y]) } as unknown as CanvasRenderingContext2D
  field.draw(ctx, [255, 255, 255], cfg)
  assert.equal(points.length, 1)
  return points[0]
}
test('reassembly starts at the previous particle position and settles at the new shape', () => {
  const field = new ParticleField()
  field.build(100, 100, 10, cfg)
  const first = new Float32Array(100); first[11] = 1
  const next = new Float32Array(100); next[88] = 1
  field.setReducedMotion(true)
  field.applyMask({ cols: 10, rows: 10, data: first }, cfg)
  const before = position(field)
  field.setReducedMotion(false)
  field.applyMask({ cols: 10, rows: 10, data: next }, cfg)
  assert.deepEqual(position(field), before)
  const pointer = new PointerInteraction()
  for (let frame = 0; frame < 600; frame++) field.update(1 / 60, pointer, cfg)
  const after = position(field)
  assert.ok(Math.abs(after[0] - before[0] - 70) < 0.1)
  assert.ok(Math.abs(after[1] - before[1] - 70) < 0.1)
})
test('reduced motion shows the complete pattern immediately and stays still', () => {
  const field = new ParticleField()
  field.build(100, 100, 10, cfg)
  field.setReducedMotion(true)
  const data = new Float32Array(100); data[44] = 1
  field.applyMask({ cols: 10, rows: 10, data }, cfg)
  const before = position(field)
  const pointer = new PointerInteraction(); pointer.move(45, 45)
  assert.equal(field.update(0.05, pointer, cfg), 0)
  assert.deepEqual(position(field), before)
})
