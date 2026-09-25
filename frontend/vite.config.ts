import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'

/* 用 proxy 而不是 CORS：前后端同源后 refresh token 才能走 HttpOnly Cookie，
   省掉一整套跨域配置。

   后端默认在 8080；被别的项目占用时在 frontend/.env.local 里写
   VITE_API_PROXY=http://127.0.0.1:8090 覆盖。用 loadEnv 而不是 process.env——
   后者依赖 shell 把变量传进来，换个终端或换个 npm script 就会悄悄失效。 */
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  /* 普通开发仍由 .env.local 决定；隔离 E2E 可以在启动的单个 Vite
     子进程上显式覆盖，避免测试误连本机另一个 8080 实例。 */
  const apiProxy = process.env.VITE_API_PROXY?.trim() || env.VITE_API_PROXY || 'http://127.0.0.1:8080'
  const usePolling = (process.env.CHOKIDAR_USEPOLLING || env.CHOKIDAR_USEPOLLING) === 'true'
  const rawPollingInterval = process.env.CHOKIDAR_INTERVAL || env.CHOKIDAR_INTERVAL || '500'
  const parsedPollingInterval = Number.parseInt(rawPollingInterval, 10)
  const pollingInterval = Number.isFinite(parsedPollingInterval) && parsedPollingInterval > 0
    ? parsedPollingInterval
    : 500
  return {
    plugins: [react()],
    resolve: {
      alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
    },
    server: {
      port: 5173,
      /* Windows 编辑器修改 Docker Desktop bind mount 时，原生文件事件并不可靠。
         容器开发显式启用轮询；宿主机直接运行 Vite 时不改变默认 watcher。 */
      watch: usePolling ? { usePolling: true, interval: pollingInterval } : undefined,
      /* 显式监听全部地址。Vite 默认的 'localhost' 在 Windows + Node 上只解析到 ::1，
         于是 http://127.0.0.1:5173 直接被拒——浏览器和 curl 谁先试 IPv4 谁就打不开。
         顺带也能用局域网 IP 在手机上验窄屏布局。只想留在本机就改成 '127.0.0.1'。 */
      host: true,
      proxy: {
        '/api': { target: apiProxy, changeOrigin: true },
        '/mcp': { target: apiProxy, changeOrigin: true },
      },
    },
  }
})
