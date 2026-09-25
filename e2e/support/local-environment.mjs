const LOOPBACK_HOSTS = new Set(['127.0.0.1', 'localhost', '[::1]'])

export function requireLoopbackOrigin(label, value) {
  let parsed
  try {
    parsed = new URL(value)
  } catch {
    throw new Error(`${label} 不是合法 URL：${value}`)
  }
  if (parsed.protocol !== 'http:') throw new Error(`${label} 只允许本机 http 地址，实际为 ${parsed.protocol}`)
  if (!LOOPBACK_HOSTS.has(parsed.hostname)) throw new Error(`${label} 只允许 127.0.0.1、localhost 或 ::1，实际为 ${parsed.hostname}`)
  if (parsed.username || parsed.password) throw new Error(`${label} 不允许在 URL 中携带凭据`)
  if (parsed.pathname !== '/' || parsed.search || parsed.hash) throw new Error(`${label} 必须是纯 origin，不能包含路径、查询或片段`)
  return parsed.origin
}

export function requireSyntheticProviderURL(label, value) {
  let parsed
  try {
    parsed = new URL(value)
  } catch {
    throw new Error(`${label} 不是合法 URL：${value}`)
  }
  const hostAllowed = LOOPBACK_HOSTS.has(parsed.hostname) || parsed.hostname === 'fake-openai'
  if (parsed.protocol !== 'http:' || !hostAllowed || parsed.username || parsed.password) {
    throw new Error(`${label} 只能指向本机或隔离 Compose 内的 fake-openai`)
  }
  return parsed.toString().replace(/\/$/, '')
}

export function e2eHeaders(extra = {}) {
  const token = process.env.E2E_TEST_TOKEN?.trim()
  return token ? { ...extra, 'X-EasyGPA-E2E-Token': token } : { ...extra }
}

export async function assertLocalDevelopmentAPI(apiOrigin) {
  const origin = requireLoopbackOrigin('E2E API', apiOrigin)
  let response
  try {
    response = await fetch(`${origin}/healthz`, { headers: { Accept: 'application/json' } })
  } catch (error) {
    throw new Error(`无法连接本地 E2E API ${origin}：${error.message}`)
  }
  const body = await response.json().catch(() => null)
  if (!response.ok || body?.status !== 'ok') {
    throw new Error(`本地 E2E API 健康检查失败：HTTP ${response.status}`)
  }
  if (body.environment !== 'dev') {
    throw new Error(`拒绝运行 E2E：${origin}/healthz 报告 environment=${JSON.stringify(body?.environment)}，必须为 dev`)
  }
  return origin
}

export async function assertLocalFrontend(frontendOrigin) {
  const origin = requireLoopbackOrigin('E2E 前端', frontendOrigin)
  const page = await fetch(`${origin}/`)
  if (!page.ok) throw new Error(`本地 E2E 前端不可用：HTTP ${page.status}`)
  const health = await fetch(`${origin}/api/healthz`, { headers: e2eHeaders({ Accept: 'application/json' }) })
  const healthBody = await health.json().catch(() => null)
  if (!health.ok || healthBody?.status !== 'ok' || healthBody?.environment !== 'dev') {
    throw new Error(`拒绝运行 E2E：本地前端的 /api 代理未连接 dev API（HTTP ${health.status}，environment=${JSON.stringify(healthBody?.environment)}）`)
  }
  const boundary = await fetch(`${origin}/api/v1/me`, { headers: e2eHeaders({ Accept: 'application/json' }) })
  if (boundary.status !== 401) {
    throw new Error(`本地 Vite → API 边界异常：未登录 /api/v1/me 返回 ${boundary.status}，预期 401`)
  }
  return origin
}
