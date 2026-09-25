import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import App from '@/App'
import { ModeChoice, type ClassMode } from '@/pages/ModeChoice'
import { api, setAccessToken } from '@/api/client'
import { authApi } from '@/api/queries'
import { useApp } from '@/stores/app'
import type { User } from '@/lib/types'
import { db, DEMO_PASSWORD, resetStore } from './db'
import DemoHint from './Hint'
import './workspace.css'

export default function DemoWorkspace() {
  const [mode, setMode] = useState<ClassMode | null>(
    () =>
      db().demoMode ??
      (db().governance?.mode === 'collective'
        ? 'collective'
        : db().governance?.version
          ? 'centralized'
          : null),
  )
  const [pending, setPending] = useState(false),
    [error, setError] = useState('')
  const [choosing, setChoosing] = useState(false)
  const queryClient = useQueryClient()
  async function choose(value: ClassMode) {
    if (pending) return
    if (mode === value) {
      setChoosing(false)
      return
    }
    setPending(true)
    setError('')
    try {
      // 演示切换重新载入独立示例，不修改真实班级的模式选择规则。
      if (mode) {
        queryClient.clear()
        useApp.getState().signOut()
        resetStore()
      }
      const session = await authApi.login('20240003', DEMO_PASSWORD)
      setAccessToken(session.access_token)
      await api.post('/governance/demo', { mode: value })
      queryClient.clear()
      useApp.getState().setUser(session.user as unknown as User)
      useApp.getState().go(value === 'collective' ? 'govHome' : 'admBoard')
      setMode(value)
      setChoosing(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : '初始化失败，请重试')
    } finally {
      setPending(false)
    }
  }
  if (!mode || choosing)
    return (
      <ModeChoice
        demo
        demoCurrentMode={mode ?? undefined}
        onCancel={mode ? () => { setError(''); setChoosing(false) } : undefined}
        onChoose={(value) => void choose(value)}
        pending={pending}
        error={error}
      />
    )
  return (
    <div className="demo-workspace">
      <nav className="demo-mode-bar" aria-label="演示模式切换">
        <span>演示站 <strong>{mode === 'collective' ? '共治模式（测试版）' : '普通模式'}</strong></span>
        <button type="button" onClick={() => { setChoosing(true); window.scrollTo(0, 0) }}>
          {mode === 'collective' ? '体验普通模式' : '体验共治模式（测试版）'} <span aria-hidden="true">↗</span>
        </button>
      </nav>
      <App />
      <DemoHint />
    </div>
  )
}
