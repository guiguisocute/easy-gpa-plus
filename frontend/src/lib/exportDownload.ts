/** Bounded Range requests: only a complete, validated chunk advances the offset.
 * A lost connection never requires keeping the whole archive in JS memory. */
export interface DownloadLink {
  downloadUrl: string
  sizeBytes: number
  etag: string
  downloadExpiresIn?: number
}

export interface DownloadTarget {
  write: (data: Uint8Array<ArrayBuffer>, position: number) => Promise<void>
  close: () => Promise<void>
  abort: () => Promise<void>
}

export interface DownloadProgress {
  received: number
  total: number
  retrying: boolean
}

export const DOWNLOAD_CHUNK_BYTES = 4 * 1024 * 1024

class DownloadError extends Error {
  retryable: boolean
  delay: number
  constructor(message: string, retryable = false, delay = 0) {
    super(message)
    this.retryable = retryable
    this.delay = delay
  }
}

const etagValue = (value: string) => value.replace(/^"|"$/g, '')

export function retryDelay(value: string | null, now = Date.now()): number {
  if (!value) return 0
  const seconds = Number(value)
  const milliseconds = Number.isFinite(seconds) ? seconds * 1000 : Date.parse(value) - now
  return Number.isFinite(milliseconds) ? Math.max(0, milliseconds) : 0
}

function wait(ms: number, signal: AbortSignal): Promise<void> {
  signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(signal.reason) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, ms)
    signal.addEventListener('abort', abort, { once: true })
  })
}

export class ExportDownload {
  received = 0
  private link: DownloadLink | null = null
  private refreshAt = 0
  private closed = false
  private readonly target: DownloadTarget
  private readonly getLink: (signal: AbortSignal) => Promise<DownloadLink>

  constructor(target: DownloadTarget, getLink: (signal: AbortSignal) => Promise<DownloadLink>) {
    this.target = target
    this.getLink = getLink
  }

  private async renew(signal: AbortSignal) {
    const next = await this.getLink(signal)
    if (!next.downloadUrl || !Number.isSafeInteger(next.sizeBytes) || next.sizeBytes <= 0 || !next.etag) {
      throw new DownloadError('文件信息不完整，请重新生成后下载')
    }
    if (this.link && (next.sizeBytes !== this.link.sizeBytes || next.etag !== this.link.etag)) {
      throw new DownloadError('文件已变化，请取消当前下载后重新下载')
    }
    this.link = next
    this.refreshAt = Date.now() + Math.max(1, (next.downloadExpiresIn ?? 600) - 30) * 1000
  }

  async run(signal: AbortSignal, progress: (value: DownloadProgress) => void) {
    if (this.closed) throw new DownloadError('下载已结束，请重新下载')
    await this.renew(signal)
    const total = this.link!.sizeBytes
    progress({ received: this.received, total, retrying: false })
    while (this.received < total) {
      const start = this.received
      const end = Math.min(start + DOWNLOAD_CHUNK_BYTES, total) - 1
      let bytes: Uint8Array<ArrayBuffer> | undefined
      for (let attempt = 0; ; attempt++) {
        signal.throwIfAborted()
        try {
          if (Date.now() >= this.refreshAt) await this.renew(signal)
          bytes = await this.chunk(start, end, signal, (count) => progress({ received: start + count, total, retrying: false }))
          break
        } catch (error) {
          signal.throwIfAborted()
          if (error instanceof DownloadError && !error.retryable || attempt >= 4) throw error
          progress({ received: start, total, retrying: true })
          await wait(Math.max(1000 * 2 ** attempt, error instanceof DownloadError ? error.delay : 0), signal)
        }
      }
      signal.throwIfAborted()
      // Disk errors are not network errors. Retrying must never append twice.
      await this.target.write(bytes, start)
      this.received = end + 1
      progress({ received: this.received, total, retrying: false })
    }
    signal.throwIfAborted()
    await this.target.close()
    this.closed = true
  }

  private async chunk(start: number, end: number, signal: AbortSignal, progress: (bytes: number) => void) {
    const controller = new AbortController()
    const abort = () => controller.abort(signal.reason)
    signal.addEventListener('abort', abort, { once: true })
    // Idle timeout, renewed as bytes arrive; a slow but healthy stream survives.
    let timer = setTimeout(() => controller.abort(), 45_000)
    let response: Response | undefined
    try {
      response = await fetch(this.link!.downloadUrl, {
        headers: { Range: `bytes=${start}-${end}`, 'If-Match': `"${etagValue(this.link!.etag)}"` },
        signal: controller.signal, credentials: 'omit', cache: 'no-store',
      })
      if (response.status === 403) {
        this.refreshAt = 0
        throw new DownloadError('下载链接需要更新，正在重新连接', true)
      }
      if (response.status === 429 || response.status >= 500) {
        throw new DownloadError('下载暂时中断，请继续下载', true, retryDelay(response.headers.get('Retry-After')))
      }
      if (response.status === 412) throw new DownloadError('文件已变化，请取消当前下载后重新下载')
      const fullResponse = response.status === 200 && start === 0 && end + 1 === this.link!.sizeBytes
      if (!fullResponse && (response.status !== 206 || response.headers.get('Content-Range') !== `bytes ${start}-${end}/${this.link!.sizeBytes}`)) {
        throw new DownloadError('服务器未返回正确的文件分段，请使用浏览器直接下载')
      }
      if (etagValue(response.headers.get('ETag') ?? '') !== etagValue(this.link!.etag)) {
        throw new DownloadError('文件校验信息不一致，请重新下载')
      }
      const reader = response.body?.getReader()
      if (!reader) throw new DownloadError('未收到文件内容', true)
      const bytes = new Uint8Array(end - start + 1)
      let received = 0
      try {
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          clearTimeout(timer)
          timer = setTimeout(() => controller.abort(), 45_000)
          if (received + value.length > bytes.length) throw new DownloadError('文件分段长度不正确，请重新下载')
          bytes.set(value, received)
          received += value.length
          progress(received)
        }
      } finally {
        await reader.cancel().catch(() => {})
        reader.releaseLock()
      }
      if (received !== bytes.length) throw new DownloadError('网络中断，正在重试当前分段', true)
      return bytes
    } finally {
      clearTimeout(timer)
      signal.removeEventListener('abort', abort)
      if (response?.body && !response.body.locked) await response.body.cancel().catch(() => {})
    }
  }

  async abort() {
    if (!this.closed) { this.closed = true; await this.target.abort() }
  }
}

function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
}

/** Call directly in the click handler, before any network await. */
export async function createDownloadTarget(filename: string, size: number): Promise<DownloadTarget> {
  const picker = (window as Window & { showSaveFilePicker?: (options: { suggestedName: string }) => Promise<FileSystemFileHandle> }).showSaveFilePicker
  if (picker) {
    const handle = await picker.call(window, { suggestedName: filename })
    const stream = await handle.createWritable()
    return { write: (bytes, position) => stream.write({ type: 'write', position, data: bytes }), close: () => stream.close(), abort: () => stream.abort() }
  }
  // Firefox/Safari can stage large files in browser-private disk storage.
  // Only one 4 MB chunk occupies the JS heap, regardless of archive size.
  if (navigator.storage?.getDirectory) {
    const root = await navigator.storage.getDirectory()
    const key = `easygpa-download-${crypto.randomUUID()}`
    const handle = await root.getFileHandle(key, { create: true })
    const stream = await handle.createWritable()
    const remove = () => root.removeEntry(key).catch(() => {})
    return {
      write: (bytes, position) => stream.write({ type: 'write', position, data: bytes }),
      close: async () => {
        await stream.close()
        saveBlob(await handle.getFile(), filename)
        setTimeout(() => { void remove() }, 60_000)
      },
      abort: async () => { await stream.abort().catch(() => {}); await remove() },
    }
  }
  if (size > 128 * 1024 * 1024) throw new Error('当前浏览器无法暂存大文件，请使用浏览器直接下载')
  const parts: Uint8Array<ArrayBuffer>[] = []
  return {
    write: async (bytes) => { parts.push(bytes) },
    close: async () => { saveBlob(new Blob(parts), filename); parts.length = 0 },
    abort: async () => { parts.length = 0 },
  }
}
