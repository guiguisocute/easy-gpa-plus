import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '@/styles/global.css'
import { useApp } from '@/stores/app'
import DemoWorkspace from './Workspace'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, refetchOnWindowFocus: false, retry: 1 },
  },
})

// 标准登录、快捷登录和身份刷新共用 store；换账号或任命变化时不沿用旧队列。
const unsubscribeIdentity = useApp.subscribe((state, previous) => {
  const user = state.user
  const before = previous.user
  if (user?.sid !== before?.sid || user?.role !== before?.role || Boolean(user?.isDeputy) !== Boolean(before?.isDeputy)) {
    queryClient.clear()
  }
})
import.meta.hot?.dispose(unsubscribeIdentity)

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <DemoWorkspace />
    </QueryClientProvider>
  </StrictMode>,
)
