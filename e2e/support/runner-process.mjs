import { spawn, spawnSync } from 'node:child_process'
import { createServer } from 'node:net'

// One fixed Compose project needs one host-wide owner, including `clean`.
// The OS releases this loopback socket even if Node is forcibly terminated.
export async function acquireProjectLock(port = 29471) {
  const server = createServer((socket) => socket.destroy())
  await new Promise((resolve, reject) => {
    server.once('error', (error) => reject(new Error(`无法取得 E2E 运行锁（127.0.0.1:${port}）；另一个测试或清理可能正在运行`, { cause: error })))
    server.listen({ host: '127.0.0.1', port, exclusive: true }, resolve)
  })
  server.unref()
  return () => new Promise((resolve) => server.close(resolve))
}

export function runProcess(command, args, { cwd, env, signal, capture = false, ignoreStdin = false, maxBytes = 16 * 1024 * 1024 } = {}) {
  signal?.throwIfAborted()
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd, env, shell: false, windowsHide: true,
      stdio: capture ? ['ignore', 'pipe', 'pipe'] : [ignoreStdin ? 'ignore' : 'inherit', 'inherit', 'inherit'],
    })
    let stdout = ''
    let stderr = ''
    let bytes = 0
    let failure
    let killTimer
    let stopping = false
    const stop = () => {
      if (stopping) return
      stopping = true
      // Windows SIGTERM terminates only the immediate process. Its Docker or
      // browser subprocess could otherwise outlive the runner and its lock.
      if (process.platform === 'win32' && child.pid && child.exitCode === null) {
        spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore', timeout: 5000 })
      }
      child.kill('SIGTERM')
      killTimer ??= setTimeout(() => child.kill('SIGKILL'), 5000)
      killTimer.unref()
    }
    const abort = () => { failure = signal.reason; stop() }
    signal?.addEventListener('abort', abort, { once: true })
    const collect = (name, chunk) => {
      bytes += Buffer.byteLength(chunk)
      if (bytes > maxBytes) {
        failure ??= new Error('测试输出超过 16 MiB 上限，已终止任务')
        stop()
      } else if (name === 'stdout') stdout += chunk
      else stderr += chunk
    }
    if (capture) {
      child.stdout.setEncoding('utf8').on('data', (chunk) => collect('stdout', chunk))
      child.stderr.setEncoding('utf8').on('data', (chunk) => collect('stderr', chunk))
    }
    child.once('error', (error) => { failure = error })
    child.once('close', (status, exitSignal) => {
      clearTimeout(killTimer)
      signal?.removeEventListener('abort', abort)
      if (failure) reject(failure)
      else resolve({ status, signal: exitSignal, stdout, stderr })
    })
  })
}
