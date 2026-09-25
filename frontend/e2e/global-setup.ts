import {
  assertLocalDevelopmentAPI,
  assertLocalFrontend,
  requireLoopbackOrigin,
} from '../../e2e/support/local-environment.mjs'

export default async function globalSetup() {
  const api = requireLoopbackOrigin(
    'PLAYWRIGHT_API_BASE',
    process.env.PLAYWRIGHT_API_BASE ?? 'http://127.0.0.1:48080',
  )
  const frontend = requireLoopbackOrigin(
    'PLAYWRIGHT_BASE_URL',
    process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:45173',
  )
  await assertLocalDevelopmentAPI(api)
  await assertLocalFrontend(frontend)
}
