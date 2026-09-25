#!/usr/bin/env node
import { connectionOptions, sendMCP } from './mcp-http.mjs'

const args = process.argv.slice(2)
if (args.length && (args.length !== 2 || args[0] !== '--url')) {
  console.error('用法：node scripts/mcp-bridge.mjs [--url <服务地址>]；密钥使用 EASYGPA_MCP_TOKEN 环境变量。')
  process.exit(1)
}
let connection
try { connection = connectionOptions(args[1]) } catch (error) { console.error(error.message); process.exit(1) }
let protocolVersion
let buffer = ''
let active = 0
let ended = false
const output = (message) => process.stdout.write(`${JSON.stringify(message)}\n`)
async function forward(line) {
  let request
  try { request = JSON.parse(line) } catch { output({ jsonrpc: '2.0', id: null, error: { code: -32700, message: 'Invalid JSON' } }); return }
  const id = request?.id
  if (!request || Array.isArray(request) || request.jsonrpc !== '2.0' || typeof request.method !== 'string') {
    output({ jsonrpc: '2.0', id: id ?? null, error: { code: -32600, message: 'Invalid MCP request' } }); return
  }
  if (active >= 8) { if (id !== undefined) output({ jsonrpc: '2.0', id, error: { code: -32000, message: 'Too many concurrent requests; retry shortly' } }); return }
  active++
  try {
    const response = await sendMCP(connection, request, protocolVersion)
    if (request.method === 'initialize' && response?.result?.protocolVersion) protocolVersion = response.result.protocolVersion
    if (id !== undefined && response) output(response)
  } catch (error) {
    if (id !== undefined) output({ jsonrpc: '2.0', id, error: { code: -32000, message: error instanceof Error ? error.message : 'MCP connection failed' } })
  } finally { active--; if (ended && active === 0) process.stdin.pause() }
}
process.stdin.setEncoding('utf8')
process.stdin.on('data', (chunk) => {
  buffer += chunk
  if (Buffer.byteLength(buffer) > 2 * 1024 * 1024) { console.error('MCP 输入过大。'); process.exit(1) }
  let boundary
  while ((boundary = buffer.indexOf('\n')) >= 0) {
    const line = buffer.slice(0, boundary).trim(); buffer = buffer.slice(boundary + 1)
    if (!line) continue
    if (Buffer.byteLength(line) > 256 * 1024) { output({ jsonrpc: '2.0', id: null, error: { code: -32600, message: 'Request too large' } }); continue }
    void forward(line)
  }
})
process.stdin.on('end', () => { ended = true; if (buffer.trim()) void forward(buffer.trim()) })
