import assert from 'node:assert/strict'
import test from 'node:test'
import { DOWNLOAD_CHUNK_BYTES as chunkSize, ExportDownload, retryDelay, type DownloadTarget } from './exportDownload.ts'

function fixture(size = chunkSize + 13) {
  const source = new Uint8Array(size).map((_, i) => i % 251)
  const stored = new Uint8Array(size)
  const writes: number[] = []
  let closed = false
  const target: DownloadTarget = {
    write: async (data, offset) => { stored.set(data, offset); writes.push(offset) },
    close: async () => { closed = true }, abort: async () => {},
  }
  const link = { downloadUrl: 'https://storage.example.test/file', sizeBytes: size, etag: 'immutable' }
  function response(start: number, end: number) {
    return new Response(source.slice(start, end + 1), { status: 206, headers: { 'Content-Range': `bytes ${start}-${end}/${size}`, ETag: '"immutable"' } })
  }
  return { source, stored, writes, target, link, response, closed: () => closed }
}

test('断线只重试未完成分段，恢复文件逐字节一致且不携带登录凭据', async (t) => {
  const f = fixture()
  const ranges: string[] = []
  let failed = false
  t.mock.method(globalThis, 'fetch', async (_url, init: RequestInit) => {
    assert.equal(init.credentials, 'omit')
    const headers = new Headers(init.headers)
    assert.equal(headers.get('Authorization'), null)
    assert.equal(headers.get('If-Match'), '"immutable"')
    const range = headers.get('Range')!
    ranges.push(range)
    const [start, end] = range.slice(6).split('-').map(Number)
    if (start === chunkSize && !failed) { failed = true; throw new TypeError('connection reset') }
    return f.response(start, end)
  })
  const download = new ExportDownload(f.target, async () => f.link)
  await download.run(new AbortController().signal, () => {})
  assert.deepEqual(ranges, [`bytes=0-${chunkSize - 1}`, `bytes=${chunkSize}-${chunkSize + 12}`, `bytes=${chunkSize}-${chunkSize + 12}`])
  assert.deepEqual(f.writes, [0, chunkSize])
  assert.deepEqual(f.stored, f.source)
  assert.ok(f.closed())
})

test('暂停后从已完成分段继续，续签后不能混用另一版本的文件', async (t) => {
  const f = fixture()
  const controller = new AbortController()
  t.mock.method(globalThis, 'fetch', async (_url, init: RequestInit) => {
    const [start, end] = new Headers(init.headers).get('Range')!.slice(6).split('-').map(Number)
    return f.response(start, end)
  })
  let link = f.link
  const download = new ExportDownload(f.target, async () => link)
  await assert.rejects(download.run(controller.signal, () => { if (download.received === chunkSize) controller.abort() }), { name: 'AbortError' })
  assert.deepEqual(f.writes, [0])
  link = { ...f.link, etag: 'changed' }
  await assert.rejects(download.run(new AbortController().signal, () => {}), /文件已变化/)
  link = { ...f.link, downloadUrl: 'https://storage.example.test/renewed' }
  await download.run(new AbortController().signal, () => {})
  assert.deepEqual(f.writes, [0, chunkSize])
  assert.deepEqual(f.stored, f.source)
})

test('签名过期自动刷新，限流遵守 Retry-After', async (t) => {
  const f = fixture(7)
  let renewals = 0
  let calls = 0
  t.mock.method(globalThis, 'fetch', async () => {
    calls++
    if (calls === 1) return new Response('', { status: 403 })
    if (calls === 2) return new Response('', { status: 429, headers: { 'Retry-After': '0' } })
    return f.response(0, 6)
  })
  await new ExportDownload(f.target, async () => { renewals++; return f.link }).run(new AbortController().signal, () => {})
  assert.equal(renewals, 2)
  assert.deepEqual(f.writes, [0])
  assert.equal(retryDelay('17'), 17000)
  assert.equal(retryDelay('Wed, 09 Sep 2026 00:00:10 GMT', Date.parse('2026-09-09T00:00:00Z')), 10000)
  assert.equal(retryDelay('invalid'), 0)
})

test('拒绝完整响应冒充分段、错误区间和 ETag，失败不写入文件', async (t) => {
  for (const kind of ['full', 'range', 'etag']) {
    const f = fixture()
    t.mock.method(globalThis, 'fetch', async () => {
      if (kind === 'full') return new Response(f.source)
      return new Response(f.source.slice(0, chunkSize), { status: 206, headers: { 'Content-Range': kind === 'range' ? 'bytes 1-3/4' : `bytes 0-${chunkSize - 1}/${f.source.length}`, ETag: kind === 'etag' ? '"wrong"' : '"immutable"' } })
    })
    await assert.rejects(new ExportDownload(f.target, async () => f.link).run(new AbortController().signal, () => {}))
    assert.deepEqual(f.writes, [])
    assert.equal(f.closed(), false)
  }
})

test('收到不完整分段时重试完整分段，不把半截内容写入目标', async (t) => {
  const f = fixture(7)
  let calls = 0
  t.mock.method(globalThis, 'fetch', async () => {
    calls++
    if (calls === 1) return new Response(f.source.slice(0, 3), { status: 206, headers: { 'Content-Range': 'bytes 0-6/7', ETag: '"immutable"' } })
    return f.response(0, 6)
  })
  await new ExportDownload(f.target, async () => f.link).run(new AbortController().signal, () => {})
  assert.equal(calls, 2)
  assert.deepEqual(f.writes, [0])
  assert.deepEqual(f.stored, f.source)
})
