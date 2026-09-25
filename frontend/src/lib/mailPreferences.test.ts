import assert from 'node:assert/strict'
import test from 'node:test'
import { mailPreset } from './mailPreferences.ts'

test('light defaults and returning from frequent both use a three-email cap', () => {
  const light = mailPreset('balanced')
  assert.equal(light.enabled, true)
  assert.equal(light.dailyLimit, 3)
  assert.equal(light.categories.progress, 'off')
  assert.equal(light.categories.receipts, 'off')
  assert.equal(light.categories.results, 'digest')
  assert.equal(light.categories.decisions, 'immediate')
  assert.equal(Object.values(light.categories).includes('frequent'), false)
  const frequent = mailPreset('frequent', light)
  assert.equal(frequent.dailyLimit, 20)
  assert.ok(Object.values(frequent.categories).every((mode) => mode === 'frequent'))
  assert.equal(mailPreset('balanced', frequent).dailyLimit, 3)
})

test('closing mail preserves personal choices without enabling any category', () => {
  const chosen = mailPreset('balanced')
  chosen.categories.decisions = 'off'
  chosen.digestTime = '20:15'
  chosen.dailyLimit = 2
  const off = mailPreset('off', chosen)
  assert.equal(off.enabled, false)
  assert.deepEqual(off.categories, chosen.categories)
  assert.equal(off.digestTime, '20:15')
  assert.equal(off.dailyLimit, 2)
})
