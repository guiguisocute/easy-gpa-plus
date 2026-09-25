import { execFileSync } from 'node:child_process'
import { appendFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

export function classify(paths) {
  const out = { backend: false, frontend: false, deployment: false, publish_backend: false, publish_frontend: false }
  const back = () => { out.backend = out.publish_backend = true }
  const front = () => { out.frontend = out.publish_frontend = true }
  for (const p of paths) {
    if (p.startsWith('backend/') || p.startsWith('asset/mailtemplate/')) back()
    else if (p.startsWith('frontend/')) front()
    else if (p.startsWith('mock/')) out.frontend = true
    else if (p.startsWith('asset/')) front()
    else if (p.startsWith('e2e/')) out.frontend = out.backend = true
    else if (p.startsWith('scripts/mcp-')) out.frontend = true
    else if (p.startsWith('deploy/production/')) {
      out.deployment = true
      if (/nginx|frontend/.test(p)) front()
      else if (!p.endsWith('.md')) back()
    } else if (p.startsWith('deploy/nginx/')) { out.deployment = true; front() }
    else if (p.startsWith('deploy/') || p.startsWith('compose') || p.startsWith('.env') || p === '.dockerignore') {
      out.deployment = true; back(); front()
    } else if (p.startsWith('.github/workflows/') || p.startsWith('scripts/ci/')) {
      out.deployment = true; back(); front()
    } else if (/^(docs|examples)\/.*\.json$/.test(p)) { out.backend = out.frontend = true }
    else if (p.startsWith('examples/')) out.frontend = true
    else if (p.startsWith('docs/') || /\.md$/.test(p) || /^(LICENSE|NOTICE|COPYING|\.gitignore|\.gitattributes)/.test(p)) continue
    else { out.deployment = true; back(); front() }
  }
  return out
}

export function changedFiles(base, head, git = execFileSync) {
  if (!/^[a-f0-9]{40}$/.test(base ?? '') || /^0+$/.test(base) || !/^[a-f0-9]{40}$/.test(head ?? '')) return null
  try {
    git('git', ['merge-base', '--is-ancestor', base, head], { stdio: 'pipe' })
    return git('git', ['diff', '--no-renames', '--name-only', '-z', base, head], { encoding: 'utf8' }).split('\0').filter(Boolean)
  } catch { return null }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const paths = changedFiles(process.env.BASE_SHA, process.env.HEAD_SHA)
  const result = paths === null ? Object.fromEntries(Object.keys(classify([])).map(k => [k, true])) : classify(paths)
  for (const [key, value] of Object.entries(result)) {
    const line = key + '=' + value + '\n'
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, line)
    process.stdout.write(line)
  }
}
