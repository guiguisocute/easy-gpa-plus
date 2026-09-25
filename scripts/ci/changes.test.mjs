import assert from 'node:assert/strict'
import test from 'node:test'
import { classify, changedFiles } from './changes.mjs'

test('frontend button changes never publish a backend image', () => {
  const scope = classify(['frontend/src/components/Button.tsx'])
  assert.equal(scope.publish_frontend, true)
  assert.equal(scope.publish_backend, false)
  assert.equal(scope.backend, false)
})
test('docs and mock do not deploy production; embedded backend content does', () => {
  assert.equal(classify(['README.md', 'docs/AGENT-CONNECT.md']).publish_frontend, false)
  assert.equal(classify(['mock/src/handle.ts']).publish_frontend, false)
  assert.equal(classify(['mock/src/handle.ts']).frontend, true)
  assert.equal(classify(['backend/internal/platformknowledge/content/rules.md']).publish_backend, true)
  assert.equal(classify(['asset/mailtemplate/notice.html']).publish_backend, true)
})
test('migrations publish backend only; nginx publishes frontend only', () => {
  assert.equal(classify(['backend/db/migrations/000064_example.up.sql']).publish_frontend, false)
  const scope = classify(['deploy/production/gpa.example.org.backup.nginx.conf'])
  assert.equal(scope.publish_backend, false)
  assert.equal(scope.publish_frontend, true)
})
test('missing, rewritten or invalid base fails closed to a full release', () => {
  assert.equal(changedFiles('', 'a'.repeat(40)), null)
  assert.equal(changedFiles('0'.repeat(40), 'a'.repeat(40)), null)
  assert.equal(changedFiles('b'.repeat(40), 'a'.repeat(40), () => { throw Error('not an ancestor') }), null)
})
test('deleted files and all unpublished commits participate in the diff', () => {
  const calls = []
  const paths = changedFiles('b'.repeat(40), 'a'.repeat(40), (_cmd, args) => {
    calls.push(args)
    return args[0] === 'diff' ? 'backend/internal/api/server.go\0frontend/src/App.tsx\0' : ''
  })
  assert.ok(calls[1].includes('--no-renames'))
  assert.equal(calls[1].at(-2), 'b'.repeat(40))
  assert.equal(classify(paths).publish_backend, true)
  assert.equal(classify(paths).publish_frontend, true)
})

import { planComponents } from './release-plan.mjs'
const previous = { release: 'a'.repeat(40), frontend: 'b'.repeat(40), backend: 'c'.repeat(40) }
const head = 'd'.repeat(40)
test('frontend release preserves backend identity and documentation preserves both', () => {
  const { manifest, scope } = planComponents(head, previous, ['frontend/src/App.tsx'])
  assert.equal(manifest.backend, previous.backend)
  assert.equal(manifest.frontend, head)
  assert.equal(scope.publish_backend, false)
  const doc = planComponents(head, previous, ['README.md'])
  assert.deepEqual(doc.manifest, { ...previous, release: head })
})
test('first release is full; stale completion never rolls back even with force', () => {
  assert.deepEqual(planComponents(head, undefined, null).manifest, { release: head, frontend: head, backend: head })
  assert.deepEqual(planComponents(head, previous, null, { force: true, stale: true }).manifest, previous)
  assert.equal(planComponents(head, previous, [], { force: true }).scope.publish_backend, true)
})
