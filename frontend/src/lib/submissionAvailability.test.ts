import assert from 'node:assert/strict'
import test from 'node:test'
import type { WindowState } from '../api/types'
import { submissionBlockReason } from './submissionAvailability.ts'

const now = Date.parse('2026-09-09T10:00:00Z')
const state: WindowState = {
  window: { open: '2026-09-08T10:00:00Z', close: '2026-09-10T10:00:00Z', lockdown: null },
  capabilities: { submit: true } as WindowState['capabilities'],
  honorRoll: {} as WindowState['honorRoll'],
  collegeName: '', enrollmentClass: '', academicYear: '', schemeVersion: '1',
  serverNow: new Date(now).toISOString(), sealed: true,
}

test('an active personal seal disables uploads; unsealing in the open window restores them', () => {
  assert.match(submissionBlockReason(state, now)!, /已封存/)
  assert.equal(submissionBlockReason({ ...state, sealed: false }, now), null)
})

test('unsealing does not bypass the intake window, submission switch or lockdown', () => {
  const unsealed = { ...state, sealed: false }
  assert.match(submissionBlockReason(unsealed, Date.parse(state.window.open) - 1)!, /尚未开放/)
  assert.equal(submissionBlockReason(unsealed, Date.parse(state.window.open)), null)
  assert.match(submissionBlockReason(unsealed, Date.parse(state.window.close))!, /已截止/)
  assert.match(submissionBlockReason({ ...unsealed, capabilities: { ...state.capabilities, submit: false } }, now)!, /尚未开启/)
  assert.match(submissionBlockReason({ ...unsealed, window: { ...state.window, lockdown: state.serverNow } }, now)!, /全系统封锁/)
})

test('missing submission state cannot enable uploads', () => {
  assert.match(submissionBlockReason(undefined, now)!, /核对提交状态/)
  assert.match(submissionBlockReason(state, NaN)!, /核对提交状态/)
})
