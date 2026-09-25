import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { ApiError, request, setAccessToken } from './client.ts'

const realFetch = globalThis.fetch
const realLocks = Object.getOwnPropertyDescriptor(navigator, 'locks')

afterEach(() => {
  globalThis.fetch = realFetch
  setAccessToken(null)
  if (realLocks) Object.defineProperty(navigator, 'locks', realLocks)
  else Reflect.deleteProperty(navigator, 'locks')
})

test('匿名登录的 401 保留账号密码错误，不触发 refresh', async () => {
  const calls: string[] = []
  globalThis.fetch = (async (input: string | URL | Request) => {
    calls.push(String(input))
    return new Response(JSON.stringify({ code: 'invalid_credentials', message: '账号或密码错误' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof fetch

  await assert.rejects(
    request('/auth/login', { method: 'POST', body: { account: 'wrong', password: 'wrong' } }),
    (error: unknown) => error instanceof ApiError && error.status === 401 && error.message === '账号或密码错误',
  )
  assert.deepEqual(calls, ['/api/v1/auth/login'])
})

test('业务接口的 401 在跨标签页锁内刷新一次并重试', async () => {
  const calls: string[] = []
  const locks: string[] = []
  Object.defineProperty(navigator, 'locks', {
    configurable: true,
    value: {
      request: async (name: string, _options: LockOptions, callback: () => Promise<boolean>) => {
        locks.push(name)
        return callback()
      },
    },
  })
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input)
    calls.push(url)
    if (url === '/api/v1/auth/refresh') {
      return new Response(JSON.stringify({ access_token: 'fresh-access' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (calls.filter((item) => item === '/api/v1/me').length === 1) {
      return new Response(JSON.stringify({ code: 'unauthenticated', message: '登录已过期，请重新登录' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response(JSON.stringify({ role: 'student' }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof fetch

  assert.deepEqual(await request<{ role: string }>('/me'), { role: 'student' })
  assert.deepEqual(calls, ['/api/v1/me', '/api/v1/auth/refresh', '/api/v1/me'])
  assert.deepEqual(locks, ['easygpa-refresh-session'])
})

test('网关的 HTML 错误页变成一句人话，而不是 JSON.parse 的原始报错', async () => {
  globalThis.fetch = (async () =>
    new Response('<html>\n<head><title>502 Bad Gateway</title></head>\n<body>...</body>\n</html>', {
      status: 502,
      headers: { 'Content-Type': 'text/html' },
    })) as typeof fetch

  await assert.rejects(
    request('/ai/batches/1/assets/1/complete', { method: 'POST' }),
    (error: unknown) =>
      error instanceof ApiError &&
      error.status === 502 &&
      error.code === 'gateway_error' &&
      error.message === '服务暂时不可用（502），请稍后重试' &&
      !error.message.includes('JSON'),
  )
})

test('200 但回了非 JSON 同样给人话，不把解析异常直接抛给界面', async () => {
  globalThis.fetch = (async () =>
    new Response('<html>nope</html>', { status: 200, headers: { 'Content-Type': 'text/html' } })) as typeof fetch

  await assert.rejects(
    request('/me'),
    (error: unknown) => error instanceof ApiError && error.code === 'invalid_response',
  )
})

test('断网和响应体读取中断统一返回带原因标识的中文错误', async () => {
  for (const bodyInterrupted of [false, true]) {
    globalThis.fetch = (async () => {
      if (!bodyInterrupted) throw new TypeError('Failed to fetch')
      return { status: 200, ok: true, text: async () => { throw new TypeError('terminated') } } as Response
    }) as typeof fetch
    await assert.rejects(request('/me'), (error: unknown) =>
      error instanceof ApiError && error.code === 'network_error' && error.message === '网络连接中断，请检查网络后重试')
  }
})

test('主动取消保留原异常，超时单独给出中文提示', async () => {
  const cancelled = new DOMException('The operation was aborted', 'AbortError')
  globalThis.fetch = (async () => { throw cancelled }) as typeof fetch
  await assert.rejects(request('/me'), (error: unknown) => error === cancelled)
  globalThis.fetch = (async () => { throw new DOMException('Timed out', 'TimeoutError') }) as typeof fetch
  await assert.rejects(request('/me'), (error: unknown) => error instanceof ApiError && error.code === 'request_timeout' && error.message.includes('超时'))
})

test('接口 message 不是字符串时不显示对象或原始错误', async () => {
  globalThis.fetch = (async () => new Response(JSON.stringify({ code: 123, message: { raw: 'invalid input' } }), { status: 422 })) as typeof fetch
  await assert.rejects(request('/submissions', { method: 'POST' }), (error: unknown) => error instanceof ApiError && error.code === 'unknown' && error.message.includes('检查填写的信息'))
})
