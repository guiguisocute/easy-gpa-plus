import assert from 'node:assert/strict'
import test from 'node:test'
import { checkGoTestReport } from './go-test-report.mjs'

const pkg = 'easygpa/backend/internal/maintenance'
const report = (...events) => [
  { Action: 'pass', Package: pkg, Test: 'TestPureLogic' },
  ...events,
  { Action: 'pass', Package: pkg },
].map(JSON.stringify).join('\n')

test('skip detection does not depend on the reason mentioning an env variable', () => {
  const result = checkGoTestReport(report(
    { Action: 'output', Package: pkg, Test: 'TestScheduledConfirmationLifecycle', Output: 'requires the disposable loopback database' },
    { Action: 'skip', Package: pkg, Test: 'TestScheduledConfirmationLifecycle' },
  ))
  assert.deepEqual(result.requiredSkipped, [`${pkg}/TestScheduledConfirmationLifecycle`])
})

test('normal output mentioning configuration is not a skipped test', () => {
  const result = checkGoTestReport(report({ Action: 'output', Package: pkg, Output: 'EASYGPA_TEST_REDIS_ADDR is configured' }))
  assert.deepEqual(result.requiredSkipped, [])
})

test('only explicitly listed external opt-ins may skip', () => {
  const result = checkGoTestReport(report(
    { Action: 'skip', Package: 'easygpa/backend/internal/aiassist', Test: 'TestLiveComposeBatchEndToEnd' },
    { Action: 'skip', Package: 'easygpa/backend/internal/aiassist', Test: 'TestLiveButRequired' },
    { Action: 'skip', Package: 'easygpa/backend/internal/notify', Test: 'TestMailPolicy' },
  ))
  assert.equal(result.skipped.length, 3)
  assert.equal(result.requiredSkipped.length, 2)
})

test('an empty, truncated or failed report cannot masquerade as success', () => {
  assert.throws(() => checkGoTestReport(''), /未完整结束/)
  assert.throws(() => checkGoTestReport('{"Action":'), SyntaxError)
  assert.throws(() => checkGoTestReport(report(
    { Action: 'start', Package: 'easygpa/backend/internal/events' },
    { Action: 'run', Package: 'easygpa/backend/internal/events', Test: 'TestPending' },
  )), /未完整结束/)
  const result = checkGoTestReport(report({ Action: 'fail', Package: pkg, Test: 'TestBroken' }))
  assert.deepEqual(result.failed, [`${pkg}/TestBroken`])
})
