import { createRequire } from 'node:module'
import { writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const mockRoot = path.dirname(fileURLToPath(import.meta.url))
const frontendRoot = path.resolve(mockRoot, '../frontend')
const frontendSrc = path.resolve(frontendRoot, 'src')
const mockClient = path.resolve(mockRoot, 'src/client.ts')
const mockMain = path.resolve(mockRoot, 'src/main.tsx')
const require = createRequire(path.join(frontendRoot, 'package.json'))

const { defineConfig, searchForWorkspaceRoot } = (await import(pathToFileURL(require.resolve('vite')).href)) as typeof import('vite')
const { default: react } = (await import(pathToFileURL(require.resolve('@vitejs/plugin-react')).href)) as {
  default: typeof import('@vitejs/plugin-react').default
}

function mockRewritePlugin() {
  const isClient = (id: string) => /[/\\]src[/\\]api[/\\]client\.ts$/.test(id)
  const isMain = (id: string) => /[/\\]src[/\\]main\.tsx$/.test(id) && !id.includes(`${path.sep}mock${path.sep}`)
  return {
    name: 'easygpa-mock-rewrite',
    enforce: 'pre' as const,
    async resolveId(source: string, importer: string | undefined, options: { skipSelf?: boolean }) {
      if (source === '@/api/client' || source === '@/api/client.ts') return mockClient
      if ((source === './client' || source === './client.ts') && importer && /[/\\]src[/\\]api[/\\]queries\.ts$/.test(importer)) {
        return mockClient
      }
      if (importer?.endsWith('index.html') && source.includes('main.tsx')) return mockMain
      if (!source.includes('client') && !source.includes('main.tsx')) return null
      const resolved = await this.resolve(source, importer, { ...options, skipSelf: true })
      if (!resolved) return null
      if (isClient(resolved.id) && !resolved.id.includes(`${path.sep}mock${path.sep}`)) return mockClient
      if (isMain(resolved.id)) return mockMain
      return null
    },
    transformIndexHtml(html: string) {
      return html.replace('<title>综测统计平台</title>', '<title>综测统计平台（演示）</title>')
    },
    closeBundle() {
      const dist = path.resolve(mockRoot, 'dist')
      writeFileSync(path.join(dist, '_redirects'), '/*    /index.html   200\n')
    },
  }
}

function mockUploadPlugin() {
  const reply = (
    req: { url?: string; on: (ev: string, fn: () => void) => void },
    res: { statusCode: number; end: () => void },
    next: () => void,
  ) => {
    const url = req.url?.split('?')[0] ?? ''
    if (url !== '/mock-upload') {
      next()
      return
    }
    req.on('end', () => {
      res.statusCode = 204
      res.end()
    })
    req.on('data', () => undefined)
  }
  return {
    name: 'easygpa-mock-upload',
    configureServer(server: { middlewares: { use: (fn: typeof reply) => void } }) {
      server.middlewares.use(reply)
    },
    configurePreviewServer(server: { middlewares: { use: (fn: typeof reply) => void } }) {
      server.middlewares.use(reply)
    },
  }
}

export default defineConfig({
  /* 根放在 frontend，这样 react 等依赖跟正式开发共用同一份 node_modules。 */
  root: frontendRoot,
  publicDir: path.resolve(frontendRoot, 'public'),
  cacheDir: path.resolve(mockRoot, '.vite'),
  plugins: [mockRewritePlugin(), react(), mockUploadPlugin()],
  resolve: {
    alias: {
      '@': frontendSrc,
      react: path.resolve(frontendRoot, 'node_modules/react'),
      'react-dom': path.resolve(frontendRoot, 'node_modules/react-dom'),
      '@tanstack/react-query': path.resolve(frontendRoot, 'node_modules/@tanstack/react-query'),
    },
    dedupe: ['react', 'react-dom'],
  },
  server: {
    port: 4173,
    strictPort: true,
    host: true,
    fs: {
      allow: [searchForWorkspaceRoot(frontendRoot), frontendRoot, mockRoot],
    },
  },
  preview: {
    port: 4173,
    host: true,
  },
  build: {
    outDir: path.resolve(mockRoot, 'dist'),
    emptyOutDir: true,
  },
})
