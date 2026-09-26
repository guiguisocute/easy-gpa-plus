/* HTTP 客户端。短 access + 长 refresh（DESIGN §10 鉴权）。

   access token 只放内存，不落 localStorage —— 落盘等于把 XSS 从"能操作"升级成"能带走"。
   代价是刷新页面后要靠 refresh 换一次，这一次往返换来的安全性是划算的。
   refresh token 由后端下发 HttpOnly Cookie，前端碰不到，所以这里只发 credentials。 */

import { apiErrorMessage } from './errorMessages.ts'

const BASE = '/api/v1'

/* 字段显式声明而不是构造函数参数属性：tsconfig 开了 erasableSyntaxOnly，
   参数属性需要 TS 生成运行时赋值代码，与"类型可直接擦除"的前提冲突。 */
export class ApiError extends Error {
  status: number
  code: string
  /** 后端在校验失败时附带的结构化细节，例如 GPA 导入的名单差异 */
  detail: unknown

  constructor(status: number, code: string, message: string, detail?: unknown) {
    super(apiErrorMessage(status, code, message, detail))
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.detail = detail
  }
}

let accessToken: string | null = null
// Only explicit login/logout changes the session. A token refresh belongs to
// the same session, so concurrent responses may safely reuse its newer token.
let sessionVersion = 0

export const setAccessToken = (t: string | null) => {
  sessionVersion++
  accessToken = t
}
export const getAccessToken = () => accessToken

/** 刷新也救不回来时通知外层清会话。由 stores/app 注册，避免这里反向依赖 store。 */
let onSessionLost: (() => void) | null = null
export const setSessionLostHandler = (fn: (() => void) | null) => {
  onSessionLost = fn
}

/* 并发请求同时撞上 401 时，只允许一次刷新在飞；其余请求等这一次的结果。
   单页内用 Promise 合并，多个标签页之间再用 Web Lock 串行化。refresh cookie
   是全站共享的，如果只做单页互斥，两个标签页仍会拿同一枚旧 cookie 同时轮换，
   后到的请求会触发重放保护，把前一个标签页刚拿到的新会话一起作废。 */
let refreshing: { version: number; promise: Promise<boolean> } | null = null

async function refreshOnce(version: number): Promise<boolean> {
  if (version !== sessionVersion) return false
  try {
    const res = await fetch(`${BASE}/auth/refresh`, { method: 'POST', credentials: 'same-origin' })
    if (!res.ok) return false
    const body = (await res.json()) as { access_token?: string }
    if (typeof body.access_token !== 'string' || !body.access_token || version !== sessionVersion) return false
    accessToken = body.access_token
    return true
  } catch {
    return false
  }
}

async function refreshAcrossTabs(version: number): Promise<boolean> {
  if (navigator.locks) {
    return navigator.locks.request('easygpa-refresh-session', { mode: 'exclusive' }, () => refreshOnce(version))
  }
  return refreshOnce(version)
}

export async function refreshSession(): Promise<boolean> {
  const version = sessionVersion
  if (!refreshing || refreshing.version !== version) {
    const promise = refreshAcrossTabs(version).finally(() => {
      if (refreshing?.promise === promise) refreshing = null
    })
    refreshing = { version, promise }
  }
  return refreshing.promise
}

interface RequestOptions {
  method?: string
  body?: unknown
  /** 刷新失败后不再重试，避免 refresh 接口自己 401 时无限递归 */
  noRetry?: boolean
  signal?: AbortSignal
}

export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const version = sessionVersion
  const token = accessToken
  const checkSession = () => {
    if (version !== sessionVersion) throw new DOMException('会话已切换，请重新操作', 'AbortError')
    if (opts.signal?.aborted) throw connectionError(opts.signal.reason, opts.signal)
  }
  checkSession()
  const headers: Record<string, string> = { Accept: 'application/json' }
  const isForm = opts.body instanceof FormData
  /* FormData 要让浏览器自己带 boundary，手写 Content-Type 会让后端分片解析失败。 */
  if (opts.body !== undefined && !isForm) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`

  const requestBody = opts.body === undefined ? undefined : isForm ? (opts.body as FormData) : JSON.stringify(opts.body)
  let res: Response
  try {
    res = await fetch(BASE + path, {
      method: opts.method ?? 'GET',
      headers,
      credentials: 'same-origin',
      signal: opts.signal,
      body: requestBody,
    })
  } catch (error) {
    checkSession()
    throw connectionError(error, opts.signal)
  }
  checkSession()

  /* 匿名鉴权接口的 401 是业务结果（最常见是账号或密码错误），不能拿它去
     触发 refresh，更不能把后端的准确文案覆盖成“登录已过期”。 */
  const mayRefresh = !path.startsWith('/auth/')
  if (res.status === 401 && !opts.noRetry && mayRefresh) {
    // A late 401 may belong to the token already replaced by another request.
    // Reuse that token instead of rotating the shared refresh cookie again.
    const refreshed = (token !== accessToken && !!accessToken) || await refreshSession()
    checkSession()
    if (refreshed) return request<T>(path, { ...opts, noRetry: true })
    setAccessToken(null)
    onSessionLost?.()
    throw new ApiError(401, 'unauthenticated', '登录已过期，请重新登录')
  }

  if (res.status === 204) return undefined as T

  let text: string
  try {
    text = await res.text()
  } catch (error) {
    checkSession()
    throw connectionError(error, opts.signal)
  }
  checkSession()
  /* 网关自己出错时回的是 HTML 错误页，不是我们的 JSON。以前这里直接 JSON.parse，
     于是界面上显示的是 `Unexpected token '<', "<html> <h"... is not valid JSON`
     —— 对着这句话没人能看出是网关挂了。 */
  let body: unknown = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      throw new ApiError(res.status, res.ok ? 'invalid_response' : 'gateway_error', gatewayMessage(res.status))
    }
  }

  if (!res.ok) {
    const e = body as { code?: string; message?: string; detail?: unknown } | null
    throw new ApiError(res.status, typeof e?.code === 'string' ? e.code : 'unknown', typeof e?.message === 'string' ? e.message : '', e?.detail)
  }
  return body as T
}

function connectionError(error: unknown, signal?: AbortSignal): unknown {
  // Query cancellation is control flow: keep the original exception so callers
  // can stop silently. Timeout is a failure and gets a separate translation key.
  if (error instanceof Error && error.name === 'TimeoutError') return new ApiError(0, 'request_timeout', '')
  if (signal?.aborted || error instanceof Error && error.name === 'AbortError') return error
  return new ApiError(0, 'network_error', '')
}

/** 网关回了非 JSON 时给一句人话。502/504 基本就是后端没起来或链路断了。 */
function gatewayMessage(status: number) {
  if (status === 502 || status === 503 || status === 504) return `服务暂时不可用（${status}），请稍后重试`
  if (status === 413) return '文件超出服务器允许的大小'
  if (status >= 500) return `服务器出错（${status}），请稍后重试`
  if (status >= 400) return `请求被网关拒绝（${status}）`
  return '服务器返回了无法解析的内容'
}

export const api = {
  get: <T>(p: string, signal?: AbortSignal) => request<T>(p, { signal }),
  post: <T>(p: string, body?: unknown) => request<T>(p, { method: 'POST', body }),
  put: <T>(p: string, body?: unknown) => request<T>(p, { method: 'PUT', body }),
  del: <T>(p: string) => request<T>(p, { method: 'DELETE' }),
  form: <T>(p: string, body: FormData) => request<T>(p, { method: 'POST', body }),
}
