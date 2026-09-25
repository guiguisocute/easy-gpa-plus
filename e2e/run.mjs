#!/usr/bin/env node
import { spawnSync } from 'node:child_process'
import { dirname, join } from 'node:path'
import { mkdirSync, readdirSync, statSync, unlinkSync, writeFileSync } from 'node:fs'
import { acquireProjectLock, runProcess } from './support/runner-process.mjs'
import { checkGoTestReport } from './support/go-test-report.mjs'
import { fileURLToPath } from 'node:url'
import {
  assertLocalDevelopmentAPI,
  assertLocalFrontend,
  requireLoopbackOrigin,
} from './support/local-environment.mjs'

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..')
const frontendRoot = join(repoRoot, 'frontend')
const target = process.argv[2] ?? 'all'
const forwarded = process.argv.slice(3)
const supported = new Set(['governance', 'mcp', 'backend', 'workflow', 'smoke', 'registration', 'knowledge', 'republish', 'deputy', 'force-rejection', 'bonus-grants', 'unseal', 'current-score', 'gpa', 'review-revision', 'submission-claims', 'adjudication-history', 'markdown-attachments', 'score-history', 'college-export', 'playwright', 'all', 'clean'])
if (!supported.has(target)) {
  console.error(`未知 E2E 目标 ${target}；可用值：${[...supported].join(', ')}`)
  process.exit(2)
}

function port(name, fallback) {
  const value = process.env[name] ?? fallback
  if (!/^\d+$/.test(value) || Number(value) < 1 || Number(value) > 65535) {
    throw new Error(`${name} 必须是 1—65535 的端口号`)
  }
  return value
}

const apiOrigin = requireLoopbackOrigin('E2E API', `http://127.0.0.1:${port('E2E_API_PORT', '48080')}`)
const frontendOrigin = requireLoopbackOrigin('E2E 前端', `http://127.0.0.1:${port('E2E_FRONTEND_PORT', '45173')}`)
const fakeHealthOrigin = requireLoopbackOrigin('E2E 合成模型', `http://127.0.0.1:${port('E2E_FAKE_OPENAI_PORT', '48082')}`)
const compose = [
  'compose', '--env-file', '.env.example',
  '-f', 'compose.dev.yaml', '-f', 'compose.e2e.yaml',
  '--project-name', 'easygpa-plus-e2e',
]

const cancellation = new AbortController()
async function run(command, args, options = {}) {
  const result = await runProcess(command, args, {
    cwd: options.cwd ?? repoRoot,
    env: options.env ?? process.env,
    signal: options.cleanup ? undefined : cancellation.signal,
    ignoreStdin: options.ignoreStdin,
  })
  if (result.status !== 0) throw new Error(`${command} 退出码 ${result.status}`)
}

async function docker(args, options) {
  await run('docker', [...compose, ...args], options)
}

function e2eContainerIDs() {
  const result = spawnSync('docker', ['ps', '-aq', '--filter', 'label=com.docker.compose.project=easygpa-plus-e2e'], {
    cwd: repoRoot,
    encoding: 'utf8',
    shell: false,
  })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`docker ps 退出码 ${result.status}`)
  return result.stdout.split(/\s+/).map((id) => id.trim()).filter(Boolean)
}

function assertNoE2EContainers() {
  const leftover = e2eContainerIDs()
  if (leftover.length > 0) {
    throw new Error(`E2E 项目仍有 ${leftover.length} 个容器未删除`)
  }
}

async function downE2E() {
  await docker(['down', '--remove-orphans'], { cleanup: true })
  assertNoE2EContainers()
}

const childEnv = {
  ...process.env,
  API_BASE: apiOrigin,
  PLAYWRIGHT_API_BASE: apiOrigin,
  PLAYWRIGHT_BASE_URL: frontendOrigin,
  PLAYWRIGHT_FAKE_OPENAI_BASE_URL: 'http://127.0.0.1:18082/v1',
  PLAYWRIGHT_FAKE_OPENAI_HEALTH_URL: `${fakeHealthOrigin}/health`,
  FAKE_OPENAI_BASE_URL: 'http://127.0.0.1:18082/v1',
  FAKE_OPENAI_HEALTH_URL: `${fakeHealthOrigin}/health`,
  E2E_TEST_TOKEN: 'easygpa-local-e2e-rate-limit-bypass-v1',
  OPS_ACCOUNT: 'ops@e2e.local',
  OPS_PASSWORD: 'easygpa-e2e-ops-password',
}

async function nodeScript(relativePath) {
  await run(process.execPath, [join(repoRoot, relativePath)], { env: childEnv })
}

async function playwright() {
  const cli = join(frontendRoot, 'node_modules', '@playwright', 'test', 'cli.js')
  await run(process.execPath, [cli, 'test', ...forwarded], { cwd: frontendRoot, env: childEnv })
}

async function goTests() {
  const reportDir = join(repoRoot, '.ai-eval', 'go-test')
  mkdirSync(reportDir, { recursive: true })
  await docker(['build', 'go-test'])
  const suites = [
    ['unit', ['test', '-json', '-count=1', '-timeout', '12m', './...']],
    ['race', ['test', '-json', '-race', '-count=1', '-timeout', '12m', './internal/events', './internal/aijob', './internal/agentjob', './internal/worker', './internal/notify', './internal/opsconfig']],
  ]
  for (const [kind, args] of suites) {
    console.log(`▸ Go ${kind}: 独立数据库、Redis、Garage，无后台消费者`)
    const result = await runProcess('docker', [...compose, 'run', '--rm', '--no-deps', '-T', '-e', `CGO_ENABLED=${kind === 'race' ? '1' : '0'}`, 'go-test', ...args], {
      cwd: repoRoot, capture: true, signal: cancellation.signal,
    })
    writeFileSync(join(reportDir, `go-${kind}-${Date.now()}.json`), result.stdout)
    const reports = readdirSync(reportDir, { withFileTypes: true })
      .filter((entry) => entry.isFile() && /^go-(unit|race)-[0-9]+\.json$/.test(entry.name))
      .map((entry) => ({ path: join(reportDir, entry.name), time: statSync(join(reportDir, entry.name)).mtimeMs }))
      .sort((a, b) => b.time - a.time)
    for (const [index, report] of reports.entries()) {
      if (index >= 6 || report.time < Date.now() - 7 * 86400000) unlinkSync(report.path)
    }
    if (result.stderr) console.error(result.stderr.slice(-4000))
    const report = checkGoTestReport(result.stdout)
    console.log(`Go ${kind}: passed=${report.passed}, optional skips=${report.skipped.length - report.requiredSkipped.length}`)
    if (result.status !== 0 || report.failed.length || report.requiredSkipped.length) {
      console.error([...report.failed, ...report.requiredSkipped].join('\n'))
      throw new Error(`Go ${kind} 验证失败；报告位于 .ai-eval/go-test`)
    }
  }
  for (const check of ['vet', 'build']) {
    await docker(['run', '--rm', '--no-deps', '-T', '--entrypoint', 'go', 'go-test', check, './...'])
  }
}

async function safetyContract() {
  await run(process.execPath, ['--test', ...['local-environment', 'runner-process', 'go-test-report'].map((name) => join(repoRoot, `e2e/support/${name}.test.mjs`))])
}

process.once('SIGINT', () => {
  process.exitCode = 130
  cancellation.abort(new Error('E2E interrupted'))
})
process.once('SIGTERM', () => {
  process.exitCode = 143
  cancellation.abort(new Error('E2E terminated'))
})

async function main() {
  const releaseLock = await acquireProjectLock()
  try {
    if (target === 'clean') {
      await docker(['down', '--volumes', '--remove-orphans'])
      assertNoE2EContainers()
      return
    }

    const leftover = e2eContainerIDs()
    if (leftover.length > 0) {
      console.log(`▸ 发现 ${leftover.length} 个上次中断留下的 E2E 容器，先清理`)
      await docker(['down', '--remove-orphans'], { cleanup: true })
      assertNoE2EContainers()
    }

    let started = false
    let cleaned = false
    async function cleanupE2E(reason) {
      if (!started || cleaned) return
      cleaned = true
      console.log(`\n▸ 删除 E2E 容器与 tmpfs 测试数据（${reason}；依赖缓存卷保留）`)
      try {
        await downE2E()
      } catch (error) {
        console.error(`E2E 容器清理失败：${error.message}`)
        process.exitCode = 1
      }
    }

    try {
      console.log('▸ 校验 E2E 本地地址安全契约')
      await safetyContract()
      console.log('▸ 启动隔离的 easygpa-plus-e2e 本地开发容器')
      // `compose up` can create only part of the stack before a health check fails;
      // cleanup must also cover that partial state.
      started = true
      // Run the one-shot migration explicitly. Docker Desktop can leave an exited
      // init container reported as "running" for a while, which makes Compose's
      // service_completed_successfully dependency wait until its full timeout.
      // An explicit run also surfaces compiler/migration errors before app startup.
      await docker(['up', '-d', '--wait', '--wait-timeout', '180', 'postgres', 'redis', 'garage'])
      await docker(['run', '--rm', '--no-TTY', 'migrate'], { ignoreStdin: true })
      if (target === 'backend') {
        await goTests()
      } else {
        if (target === 'governance') {
          await docker(['run', '--rm', '--no-TTY', 'go-test', 'test', '-run', 'TestGovernance', '-count=1', './internal/api'], { ignoreStdin: true })
        }
        await docker(['up', '-d', '--wait', '--wait-timeout', '180', '--remove-orphans', 'api', 'fake-openai', 'workers', 'frontend'])
        await assertLocalDevelopmentAPI(apiOrigin)
        await assertLocalFrontend(frontendOrigin)

        const targets = target === 'workflow' ? ['smoke', 'republish', 'deputy', 'force-rejection', 'bonus-grants', 'unseal', 'markdown-attachments'] : target === 'all' ? ['mcp', 'smoke', 'registration', 'knowledge', 'republish', 'deputy', 'force-rejection', 'bonus-grants', 'unseal', 'current-score', 'gpa', 'review-revision', 'submission-claims', 'adjudication-history', 'markdown-attachments', 'score-history', 'college-export', 'playwright'] : [target]
        for (const current of targets) {
          console.log(`\n▸ 运行 E2E：${current}`)
          if (current === 'smoke') await nodeScript('e2e/smoke.mjs')
          else if (current === 'governance') await nodeScript('e2e/governance.mjs')
          else if (current === 'mcp') await nodeScript('e2e/mcp.mjs')
          else if (current === 'college-export') await nodeScript('e2e/college-export.mjs')
          else if (current === 'registration') await nodeScript('e2e/register-noemail.mjs')
          else if (current === 'knowledge') await nodeScript('e2e/knowledge-agent.mjs')
          else if (current === 'republish') await nodeScript('e2e/scheme-republish.mjs')
          else if (current === 'deputy') await nodeScript('e2e/deputy-adjudication.mjs')
          else if (current === 'force-rejection') await nodeScript('e2e/force-rejection.mjs')
          else if (current === 'bonus-grants') await nodeScript('e2e/bonus-grants.mjs')
          else if (current === 'unseal') await nodeScript('e2e/unseal.mjs')
          else if (current === 'current-score') await nodeScript('e2e/current-score.mjs')
          else if (current === 'gpa') await nodeScript('e2e/gpa-always-open.mjs')
          else if (current === 'review-revision') await nodeScript('e2e/review-revision.mjs')
          else if (current === 'submission-claims') await nodeScript('e2e/submission-claims.mjs')
          else if (current === 'adjudication-history') await nodeScript('e2e/adjudication-history.mjs')
          else if (current === 'markdown-attachments') await nodeScript('e2e/markdown-attachments.mjs')
          else if (current === 'score-history') await nodeScript('e2e/score-history.mjs')
          else await playwright()
        }
      }
    } catch (error) {
      console.error(`\nE2E 失败：${error.message}`)
      process.exitCode = 1
    } finally {
      await cleanupE2E('finished')
    }

  } finally {
    await releaseLock()
  }
}

try {
  await main()
} catch (error) {
  console.error(`E2E 启动失败：${error.message}`)
  process.exitCode = 1
}
