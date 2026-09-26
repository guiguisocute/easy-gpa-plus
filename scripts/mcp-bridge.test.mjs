import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { once } from 'node:events'
import { sendMCP } from './mcp-http.mjs'

const script = fileURLToPath(new URL('./mcp-bridge.mjs', import.meta.url))
// Synthetic credentials are valid only inside these local test servers.
const token = `egm_${'a'.repeat(64)}`
async function runBridge(t, handler, lines, { finalNewline = true } = {}) {
  const requests = []
  const server = createServer(async (req, res) => {
    const parts = []
    for await (const part of req) parts.push(part)
    const body = JSON.parse(Buffer.concat(parts))
    requests.push({ body, authorized: req.headers.authorization === `Bearer ${token}` })
    handler(body, res)
  })
  server.listen(0, '127.0.0.1'); await once(server, 'listening')
  t.after(() => { server.closeAllConnections(); server.close() })
  const child = spawn(process.execPath, [script], { env: { ...process.env, EASYGPA_MCP_URL: `http://127.0.0.1:${server.address().port}/mcp`, EASYGPA_MCP_TOKEN: token }, stdio: ['pipe', 'pipe', 'pipe'] })
  t.after(() => child.kill())
  let out = '', err = ''
  child.stdout.on('data', b => { out += b })
  child.stderr.on('data', b => { err += b })
  const close = once(child, 'close')
  child.stdin.end(lines.join('\n') + (finalNewline ? '\n' : ''))
  const [code] = await close
  assert.equal(code, 0)
  assert.equal(out.includes(token) || err.includes(token), false, 'credentials must not reach stdout/stderr')
  return { requests, messages: out.trim().split('\n').filter(Boolean).map(s => JSON.parse(s)), err }
}

test('stdio bridge preserves JSON-RPC IDs, initialization, tool results and notifications', { timeout: 10000 }, async t => {
  const rpc = (id, method, params = {}) => JSON.stringify({ jsonrpc: '2.0', ...(id === undefined ? {} : { id }), method, params })
  const { requests, messages, err } = await runBridge(t, (body, res) => {
    if (body.id === undefined) { res.writeHead(202); res.end(); return }
    res.setHeader('Content-Type', 'application/json')
    res.end(JSON.stringify({ jsonrpc: '2.0', id: body.id, result: body.method === 'initialize' ? { protocolVersion: '2025-11-25', capabilities: { tools: {} }, serverInfo: { name: 'Synthetic', version: '1' } } : { structuredContent: { status: 200, data: { score: 83 } } } }))
  }, [rpc(1, 'initialize'), rpc(undefined, 'notifications/initialized'), rpc('tool-2', 'tools/call', { name: 'scores.mine', arguments: {} })])
  assert.equal(requests.length, 3)
  assert.ok(requests.every(r => r.authorized))
  assert.equal(messages.length, 2)
  assert.equal(messages.find(m => m.id === 1).result.protocolVersion, '2025-11-25')
  assert.equal(messages.find(m => m.id === 'tool-2').result.structuredContent.data.score, 83)
  assert.equal(err, '')
})

test('expired credentials produce a useful error without forwarding response bodies', { timeout: 10000 }, async t => {
  const { messages } = await runBridge(t, (_, res) => { res.writeHead(401); res.end('untrusted upstream details') }, [JSON.stringify({ jsonrpc: '2.0', id: 8, method: 'ping' })])
  assert.equal(messages[0].id, 8)
  assert.match(messages[0].error.message, /密钥已失效/)
  assert.equal(JSON.stringify(messages).includes('untrusted'), false)
})

test('malformed stdio requests never reach the service', { timeout: 10000 }, async t => {
  const { messages, requests } = await runBridge(t, () => assert.fail('must not forward invalid requests'), ['{broken', '[]', '{"jsonrpc":"2.0","id":3}'])
  assert.equal(requests.length, 0)
  assert.deepEqual(messages.map(m => m.error.code), [-32700, -32600, -32600])
})

test('oversized final requests are rejected with or without a trailing newline', { timeout: 10000 }, async t => {
  const request = JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { text: 'x'.repeat(256 * 1024) } })
  for (const finalNewline of [true, false]) {
    const { messages, requests } = await runBridge(t, () => assert.fail('must not forward oversized requests'), [request], { finalNewline })
    assert.equal(requests.length, 0)
    assert.equal(messages.length, 1)
    assert.equal(messages[0].error.message, 'Request too large')
  }
})

test('MCP response limit cancels an oversized stream before it finishes', async t => {
  let produced = 0
  let cancelled = false
  t.mock.method(globalThis, 'fetch', async () => new Response(new ReadableStream({
    pull(controller) {
      if (produced === 100) { controller.close(); return }
      produced++
      controller.enqueue(new Uint8Array(64 * 1024))
    },
    cancel() { cancelled = true },
  })))
  await assert.rejects(sendMCP({ endpoint: 'http://127.0.0.1/mcp', token }, { id: 1 }), /返回内容过大/)
  assert.equal(cancelled, true)
  assert.ok(produced <= 82, 'must stop reading near the 5 MiB limit')
})

test('MCP JSON decoding preserves UTF-8 characters split across response chunks', async t => {
  const message = { jsonrpc: '2.0', id: 1, result: { title: '材料与成绩' } }
  const bytes = Buffer.from(JSON.stringify(message))
  t.mock.method(globalThis, 'fetch', async () => new Response(new ReadableStream({
    start(controller) {
      for (const byte of bytes) controller.enqueue(Uint8Array.of(byte))
      controller.close()
    },
  })))
  assert.deepEqual(await sendMCP({ endpoint: 'http://127.0.0.1/mcp', token }, { id: 1 }), message)
})

test('malformed service responses do not leak upstream content into bridge errors', { timeout: 10000 }, async t => {
  const { messages } = await runBridge(t, (_, res) => res.end(`invalid response with ${token}`), [JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'ping' })])
  assert.equal(messages[0].error.message, 'MCP 返回了无效的 JSON 响应。')
})
