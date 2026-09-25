import assert from 'node:assert/strict'
import test from 'node:test'
import { randomUUID } from 'node:crypto'
import { existsSync, readFileSync, unlinkSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { acquireProjectLock, runProcess } from './runner-process.mjs'

test('a second runner cannot acquire ownership until the first releases it', async () => {
  const release = await acquireProjectLock(39471)
  try {
    await assert.rejects(acquireProjectLock(39471), /另一个测试或清理/)
  } finally {
    await release()
  }
  const releaseAgain = await acquireProjectLock(39471)
  await releaseAgain()
})

test('aborting a running child waits for its exit instead of blocking cleanup', async () => {
  const abort = new AbortController()
  const running = runProcess(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { signal: abort.signal, capture: true })
  const timer = setTimeout(() => abort.abort(new Error('test interrupted')), 100)
  try {
    await assert.rejects(running, /test interrupted/)
  } finally {
    clearTimeout(timer)
  }
})

test('an output flood fails within the capture limit', async () => {
  await assert.rejects(runProcess(process.execPath, ['-e', 'process.stdout.write("x".repeat(10000))'], {
    capture: true, maxBytes: 100,
  }), /输出超过/)
})

test('nonzero exit codes and UTF-8 output survive child execution', async () => {
  const result = await runProcess(process.execPath, ['-e', 'console.log("验证失败"); process.exitCode = 7'], { capture: true })
  assert.equal(result.status, 7)
  assert.equal(result.stdout.trim(), '验证失败')
})

test('Windows cancellation removes descendants and releases project ownership', { skip: process.platform !== 'win32' }, async () => {
  const marker = join(tmpdir(), `easygpa-runner-${randomUUID()}.json`)
  const abort = new AbortController()
  const code = `
    const { acquireProjectLock } = await import(${JSON.stringify(new URL('./runner-process.mjs', import.meta.url).href)});
    await acquireProjectLock(39472);
    const { spawn } = await import('node:child_process');
    const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore', windowsHide: true });
    const { writeFileSync } = await import('node:fs');
    writeFileSync(${JSON.stringify(marker)}, String(child.pid));
    setInterval(() => {}, 1000);
  `
  const running = runProcess(process.execPath, ['--input-type=module', '-e', code], { capture: true, signal: abort.signal })
  // Attach the rejection handler before aborting so no rejection is unhandled.
  const stopped = assert.rejects(running, /tree interrupted/)
  try {
    for (let i = 0; i < 100 && !existsSync(marker); i++) await delay(25)
    assert.ok(existsSync(marker), 'child did not become ready')
    const pid = Number(readFileSync(marker, 'utf8'))
    abort.abort(new Error('tree interrupted'))
    await stopped
    assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' })
    const release = await acquireProjectLock(39472)
    await release()
  } finally {
    if (!abort.signal.aborted) abort.abort(new Error('tree interrupted'))
    await stopped
    if (existsSync(marker)) unlinkSync(marker)
  }
})
