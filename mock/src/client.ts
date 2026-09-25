/* 替换 frontend/src/api/client.ts。页面与 hooks 原样复用，所有 /api/v1 在这里走内存数据。 */

import { handle } from './handle'
import { ApiError } from './errors'
export { ApiError }

let accessToken: string | null = null

export const setAccessToken = (t: string | null) => {
  accessToken = t
}
export const getAccessToken = () => accessToken

let onSessionLost: (() => void) | null = null
export const setSessionLostHandler = (fn: (() => void) | null) => {
  onSessionLost = fn
}

// 沿用存储键，但保存含版本的令牌，重置密码后旧会话不能重新刷新。
const SESSION_KEY = 'easygpa.mock.sid'

export async function refreshSession(): Promise<boolean> {
  try {
    const token = sessionStorage.getItem(SESSION_KEY)
    if (!token) return false
    const body = (await handle('POST', '/auth/refresh', undefined, token)) as { access_token?: string }
    if (!body.access_token) return false
    accessToken = body.access_token
    return true
  } catch {
    return false
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  noRetry?: boolean
  signal?: AbortSignal
}

export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  void opts.signal
  try {
    const result = await handle(opts.method ?? 'GET', path, opts.body, accessToken)
    if (path === '/auth/login' || path === '/auth/register/complete' || path === '/auth/refresh' || path === '/auth/demo-tour') {
      const session = result as { access_token?: string }
      if (session.access_token) sessionStorage.setItem(SESSION_KEY, session.access_token)
    }
    if (path === '/auth/logout') sessionStorage.removeItem(SESSION_KEY)
    return result as T
  } catch (error) {
    if (error instanceof ApiError) {
      const mayRefresh = !path.startsWith('/auth/')
      if (error.status === 401 && !opts.noRetry && mayRefresh) {
        if (await refreshSession()) return request<T>(path, { ...opts, noRetry: true })
        accessToken = null
        sessionStorage.removeItem(SESSION_KEY)
        onSessionLost?.()
        throw new ApiError(401, 'unauthenticated', '登录已过期，请重新登录')
      }
      throw error
    }
    throw error
  }
}

export const api = {
  get: <T>(p: string, signal?: AbortSignal) => request<T>(p, { signal }),
  post: <T>(p: string, body?: unknown) => request<T>(p, { method: 'POST', body }),
  put: <T>(p: string, body?: unknown) => request<T>(p, { method: 'PUT', body }),
  del: <T>(p: string) => request<T>(p, { method: 'DELETE' }),
  form: <T>(p: string, body: FormData) => request<T>(p, { method: 'POST', body }),
}

/* 直传对象存储那一步不经过 api.request。拦截 /mock-upload，静态托管也能点上传。 */
;(function installUploadStub() {
  if (typeof XMLHttpRequest === 'undefined') return
  const proto = XMLHttpRequest.prototype
  const origOpen = proto.open
  const origSend = proto.send
  proto.open = function (this: XMLHttpRequest, method: string, url: string | URL, async?: boolean, username?: string | null, password?: string | null) {
    ;(this as XMLHttpRequest & { __mockUpload?: boolean }).__mockUpload = String(url).includes('/mock-upload')
    return origOpen.call(this, method, url, async ?? true, username, password)
  }
  proto.send = function (this: XMLHttpRequest, body?: Document | XMLHttpRequestBodyInit | null) {
    if ((this as XMLHttpRequest & { __mockUpload?: boolean }).__mockUpload) {
      Object.defineProperty(this, 'status', { configurable: true, value: 204 })
      Object.defineProperty(this, 'statusText', { configurable: true, value: 'No Content' })
      this.upload?.dispatchEvent(new ProgressEvent('progress', { lengthComputable: true, loaded: 1, total: 1 }))
      queueMicrotask(() => {
        this.dispatchEvent(new ProgressEvent('load'))
        this.onload?.(new ProgressEvent('load'))
      })
      return
    }
    return origSend.call(this, body)
  }
})()
