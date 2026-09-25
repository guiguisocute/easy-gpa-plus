import assert from 'node:assert/strict'
import test from 'node:test'
import { requireLoopbackOrigin, requireSyntheticProviderURL } from './local-environment.mjs'

test('E2E API 和前端只接受纯 loopback HTTP origin', () => {
  assert.equal(requireLoopbackOrigin('API', 'http://127.0.0.1:48080'), 'http://127.0.0.1:48080')
  assert.equal(requireLoopbackOrigin('API', 'http://localhost:48080'), 'http://localhost:48080')
  assert.equal(requireLoopbackOrigin('API', 'http://[::1]:48080'), 'http://[::1]:48080')

  for (const unsafe of [
    'https://gpa.example.org',
    'http://gpa.example.org',
    'http://10.145.0.1:38080',
    'http://127.0.0.1:48080/api/v1',
    'http://user:password@127.0.0.1:48080',
  ]) {
    assert.throws(() => requireLoopbackOrigin('API', unsafe), /只允许|不允许|必须/)
  }
})

test('模型端点只接受 loopback 或 Compose 内的合成服务', () => {
  assert.equal(
    requireSyntheticProviderURL('model', 'http://127.0.0.1:18082/v1'),
    'http://127.0.0.1:18082/v1',
  )
  assert.equal(
    requireSyntheticProviderURL('model', 'http://fake-openai:18082/v1'),
    'http://fake-openai:18082/v1',
  )
  assert.throws(
    () => requireSyntheticProviderURL('model', 'https://api.openai.com/v1'),
    /只能指向本机或隔离 Compose/,
  )
})
