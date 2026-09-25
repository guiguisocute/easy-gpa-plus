import { spawnSync } from 'node:child_process'
const project = process.env.MOCK_PAGES_PROJECT
if (!project || !/^[a-z0-9-]+$/.test(project)) throw new Error('Set MOCK_PAGES_PROJECT to your Cloudflare Pages project name.')
const result = spawnSync(process.platform === 'win32' ? 'npx.cmd' : 'npx', ['--yes','wrangler@4','pages','deploy','../mock/dist','--project-name',project,'--branch','main','--commit-dirty=true'], {stdio:'inherit',env:process.env,shell:process.platform==='win32'})
process.exit(result.status ?? 1)
