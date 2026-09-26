import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { ApiError, getAccessToken, refreshSession, request, setAccessToken, setSessionLostHandler } from './client.ts'

const realFetch = globalThis.fetch
const realLocks = Object.getOwnPropertyDescriptor(navigator, 'locks')

afterEach(() => {
  globalThis.fetch = realFetch
  setAccessToken(null)
  setSessionLostHandler(null)
  if (realLocks) Object.defineProperty(navigator, 'locks', realLocks)
  else Reflect.deleteProperty(navigator, 'locks')
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
const aborted = (error: unknown) => error instanceof DOMException && error.name === 'AbortError'

test('迟到的旧令牌 401 复用已刷新的令牌，不再轮换刷新 Cookie', async () => {
  const late = deferred<Response>()
  let refreshes = 0
  const tokens: Array<string | undefined> = []
  setAccessToken('expired')
  globalThis.fetch = (async (input, init) => {
    if (String(input).endsWith('/auth/refresh')) {
      refreshes++
      return json({ access_token: 'fresh' })
    }
    const token = (init?.headers as Record<string, string> | undefined)?.Authorization
    tokens.push(token)
    if (token === 'Bearer fresh') return json({ ok: true })
    if (String(input).endsWith('/slow')) return late.promise
    return json({}, 401)
  }) as typeof fetch

  const slow = request('/slow')
  await request('/fast')
  late.resolve(json({}, 401))
  assert.deepEqual(await slow, { ok: true })
  assert.equal(refreshes, 1)
  assert.deepEqual(tokens, ['Bearer expired', 'Bearer expired', 'Bearer fresh', 'Bearer fresh'])
})

test('退出或重新登录后，旧会话的刷新结果不能覆盖当前令牌或重放旧请求', async () => {
  for (const nextToken of [null, 'another-user']) {
    const response = deferred<Response>()
    const started = deferred<boolean>()
    let businessCalls = 0
    let lost = 0
    setAccessToken('old-user')
    setSessionLostHandler(() => { lost++ })
    globalThis.fetch = (async (input) => {
      if (String(input).endsWith('/auth/refresh')) {
        started.resolve(true)
        return response.promise
      }
      businessCalls++
      return json({}, 401)
    }) as typeof fetch
    const pending = request('/me/password', { method: 'PUT', body: { value: 'old-operation' } })
    const rejected = assert.rejects(pending, aborted)
    await started.promise
    setAccessToken(nextToken)
    response.resolve(json({ access_token: 'old-user-refreshed' }))
    await rejected
    assert.equal(getAccessToken(), nextToken)
    assert.equal(businessCalls, 1)
    assert.equal(lost, 0)
  }
})

test('会话变化后迟到的成功响应不会传给新账号页面', async () => {
  for (const duringBodyRead of [false, true]) {
    const response = deferred<Response>()
    const body = deferred<string>()
    const reading = deferred<boolean>()
    setAccessToken('first-user')
    globalThis.fetch = (async () => duringBodyRead ? {
      ok: true, status: 200, text: () => { reading.resolve(true); return body.promise },
    } as Response : response.promise) as typeof fetch
    const rejected = assert.rejects(request('/me/scorecard'), aborted)
    if (duringBodyRead) await reading.promise
    setAccessToken('second-user')
    response.resolve(json({ private: 'first-user-score' }))
    body.resolve(JSON.stringify({ private: 'first-user-score' }))
    await rejected
    assert.equal(getAccessToken(), 'second-user')
  }
})

test('新的登录会话不等待旧会话仍在飞的刷新', async () => {
  // Test the per-tab promise independently of the cross-tab lock (which must
  // serialize actual cookie rotation, including between different sessions).
  Object.defineProperty(navigator, 'locks', { configurable: true, value: undefined })
  const oldResponse = deferred<Response>()
  let calls = 0
  setAccessToken('old-user')
  globalThis.fetch = (async () => ++calls === 1 ? oldResponse.promise : json({ access_token: 'new-user-refreshed' })) as typeof fetch
  const oldRefresh = refreshSession()
  setAccessToken('new-user')
  assert.equal(await refreshSession(), true)
  oldResponse.resolve(json({ access_token: 'old-user-refreshed' }))
  assert.equal(await oldRefresh, false)
  assert.equal(getAccessToken(), 'new-user-refreshed')
})

test('已取消的 401 请求不触发刷新，也不注销当前会话', async () => {
  const controller = new AbortController()
  let calls = 0
  let lost = 0
  setAccessToken('current')
  setSessionLostHandler(() => { lost++ })
  globalThis.fetch = (async () => {
    calls++
    controller.abort()
    return json({}, 401)
  }) as typeof fetch
  await assert.rejects(request('/me', { signal: controller.signal }), aborted)
  assert.equal(calls, 1)
  assert.equal(lost, 0)
  assert.equal(getAccessToken(), 'current')
})

test('等待共享刷新时取消的请求不会因刷新失败而清除会话', async () => {
  const controller = new AbortController()
  const started = deferred<boolean>()
  const response = deferred<Response>()
  let lost = 0
  setAccessToken('current')
  setSessionLostHandler(() => { lost++ })
  globalThis.fetch = (async (input) => {
    if (!String(input).endsWith('/auth/refresh')) return json({}, 401)
    started.resolve(true)
    return response.promise
  }) as typeof fetch
  const rejected = assert.rejects(request('/me', { signal: controller.signal }), aborted)
  await started.promise
  controller.abort()
  response.resolve(json({}, 401))
  await rejected
  assert.equal(lost, 0)
  assert.equal(getAccessToken(), 'current')
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
  const controller = new AbortController()
  controller.abort(new DOMException('Timed out', 'TimeoutError'))
  await assert.rejects(request('/me', { signal: controller.signal }), (error: unknown) => error instanceof ApiError && error.code === 'request_timeout')
})

test('接口 message 不是字符串时不显示对象或原始错误', async () => {
  globalThis.fetch = (async () => new Response(JSON.stringify({ code: 123, message: { raw: 'invalid input' } }), { status: 422 })) as typeof fetch
  await assert.rejects(request('/submissions', { method: 'POST' }), (error: unknown) => error instanceof ApiError && error.code === 'unknown' && error.message.includes('检查填写的信息'))
})
