import { defineConfig, devices } from '@playwright/test'
import { e2eHeaders, requireLoopbackOrigin } from '../e2e/support/local-environment.mjs'

const e2eBaseURL = requireLoopbackOrigin(
  'PLAYWRIGHT_BASE_URL',
  process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:45173',
)

export default defineConfig({
  testDir: './e2e',
  outputDir: '../.ai-eval/playwright/artifacts',
  fullyParallel: false,
  forbidOnly: true,
  workers: 1,
  timeout: 90_000,
  expect: { timeout: 10_000 },
  // These tests create real business state. Retrying a half-completed flow can
  // conceal non-idempotent bugs, so every run starts a fresh disposable stack.
  retries: 0,
  reporter: [['list'], ['html', { outputFolder: '../.ai-eval/playwright/report', open: 'never' }]],
  use: {
    baseURL: e2eBaseURL,
    extraHTTPHeaders: e2eHeaders(),
    // DOM/network traces can retain password-field values and request bodies.
    // Knowledge Agent tests therefore keep only screenshots, never traces/video.
    trace: 'off',
    video: 'off',
    screenshot: 'only-on-failure',
  },
  globalSetup: './e2e/global-setup.ts',
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
