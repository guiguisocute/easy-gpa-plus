// Shared by the stdio bridge and the local-file upload helper. No credentials
// are accepted in command arguments or URLs, persisted, or printed.
export function connectionOptions(url = process.env.EASYGPA_MCP_URL) {
  if (!url) throw new Error('设置 EASYGPA_MCP_URL，或传入 --url <MCP 服务地址>。')
  const endpoint = new URL(url)
  const local = ['localhost', '127.0.0.1', '[::1]'].includes(endpoint.hostname)
  if (endpoint.protocol !== 'https:' && !(local && endpoint.protocol === 'http:')) throw new Error('远程 MCP 连接必须使用 HTTPS；HTTP 仅用于本机开发。')
  if (endpoint.username || endpoint.password || endpoint.search || endpoint.hash) throw new Error('服务地址不能包含账号、密钥、查询参数或片段。')
  const token = process.env.EASYGPA_MCP_TOKEN ?? ''
  if (!/^egm_[a-f0-9]{64}$/.test(token)) throw new Error('请将账号设置签发的临时密钥放入 EASYGPA_MCP_TOKEN 环境变量；演示密钥不可连接真实服务。')
  return { endpoint: endpoint.href.replace(/\/$/, ''), token }
}

export async function sendMCP(connection, message, protocolVersion) {
  const headers = { Authorization: `Bearer ${connection.token}`, 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' }
  if (protocolVersion) headers['MCP-Protocol-Version'] = protocolVersion
  const response = await fetch(connection.endpoint, { method: 'POST', headers, body: JSON.stringify(message), redirect: 'error', signal: AbortSignal.timeout(60_000) })
  if (response.status === 202 || response.status === 204 || !response.ok) {
    await response.body?.cancel()
    if (response.ok) return null
    throw new Error(response.status === 401 ? 'MCP 密钥已失效，请在账号设置重新签发。' : `MCP 请求失败，HTTP ${response.status}。`)
  }
  // Enforce the limit while reading, including chunked/decompressed responses.
  // Checking after response.text() would already have buffered an unlimited body.
  const reader = response.body?.getReader()
  const chunks = []
  let bytes = 0
  if (reader) {
    try {
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        bytes += value.byteLength
        if (bytes > 5 * 1024 * 1024) {
          await reader.cancel()
          throw new Error('MCP 返回内容过大，请缩小查询范围。')
        }
        chunks.push(value)
      }
    } finally { reader.releaseLock() }
  }
  try { return JSON.parse(Buffer.concat(chunks, bytes).toString('utf8')) }
  catch { throw new Error('MCP 返回了无效的 JSON 响应。') }
}

export async function callMCP(connection, name, args) {
  const result = await sendMCP(connection, { jsonrpc: '2.0', id: crypto.randomUUID(), method: 'tools/call', params: { name, arguments: args } }, '2025-11-25')
  if (result?.error) throw new Error(result.error.message || 'MCP 协议错误')
  const envelope = result?.result?.structuredContent
  if (!envelope || result.result.isError) throw new Error(envelope?.data?.message || envelope?.data?.error?.message || `工具 ${name} 未完成`)
  return envelope.data
}
