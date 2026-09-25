import { execFileSync } from 'node:child_process'
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { changedFiles, classify } from './changes.mjs'

export function planComponents(head, previous, paths, { force = false, stale = false } = {}) {
  const scope = classify(paths ?? [])
  if (paths === null || force) scope.publish_frontend = scope.publish_backend = true
  if (stale) scope.publish_frontend = scope.publish_backend = false
  const manifest = {
    release: stale ? previous.release : head,
    frontend: scope.publish_frontend ? head : previous.frontend,
    backend: scope.publish_backend ? head : previous.backend,
  }
  return { scope, manifest }
}

function main() {
  const head = process.env.RELEASE_SHA
  if (!/^[a-f0-9]{40}$/.test(head ?? '')) throw Error('Invalid release SHA')
  const workflow = process.env.RELEASE_WORKFLOW ?? 'cd.yml'
  const temp = join(process.env.RUNNER_TEMP, 'release-state')
  mkdirSync(temp, { recursive: true })
  const gh = args => execFileSync('gh', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
  // Only successful runs count. A failed or partially deployed run cannot hide
  // unpublished commits from the next release.
  const run = gh(['run', 'list', '--workflow', workflow, '--branch', 'main', '--status', 'success', '--limit', '1', '--json', 'databaseId', '--jq', '.[0].databaseId // empty'])
  let previous
  if (run) {
    try {
      gh(['run', 'download', run, '--name', 'production-state', '--dir', temp])
      previous = JSON.parse(readFileSync(join(temp, 'release-manifest.json'), 'utf8'))
      if (![previous.release, previous.frontend, previous.backend].every(v => /^[a-f0-9]{40}$/.test(v ?? ''))) previous = undefined
    } catch { console.log('Previous release manifest unavailable; checking and publishing both components.') }
  }
  const paths = previous ? changedFiles(previous.release, head) : null
  let stale = false
  if (previous && previous.release !== head && paths === null) {
    try { execFileSync('git', ['merge-base', '--is-ancestor', head, previous.release], { stdio: 'pipe' }); stale = true } catch { /* rewritten history requires a complete release */ }
  }
  const { scope, manifest } = planComponents(head, previous, paths, { force: process.env.FORCE_FULL === 'true', stale })
  writeFileSync(join(process.env.RUNNER_TEMP, 'release-manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
  for (const [key, value] of Object.entries({ backend: scope.publish_backend, frontend: scope.publish_frontend, backend_sha: manifest.backend, frontend_sha: manifest.frontend })) {
    appendFileSync(process.env.GITHUB_OUTPUT, key + '=' + value + '\n')
    console.log(key + '=' + value)
  }
  if (stale) console.log('A newer release is already deployed; this older CI completion will not roll it back.')

}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main()
