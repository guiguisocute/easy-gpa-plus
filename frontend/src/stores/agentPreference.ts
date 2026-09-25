import { create } from 'zustand'
import { useApp } from './app'

const KEY = 'easygpa.agent-preferences.v1'
type Preferences = Record<string, boolean>

function read(): Preferences {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(KEY) ?? '{}')
    return value && typeof value === 'object' && !Array.isArray(value)
      ? Object.fromEntries(Object.entries(value).filter(([, enabled]) => typeof enabled === 'boolean')) : {}
  } catch { return {} }
}

const usePreferences = create<{ values: Preferences; setEnabled: (account: string, enabled: boolean) => void }>((set, get) => ({
  values: read(),
  setEnabled: (account, enabled) => {
    const values = { ...get().values, [account]: enabled }
    try { localStorage.setItem(KEY, JSON.stringify(values)) } catch { /* Still apply in this tab when storage is unavailable. */ }
    set({ values })
  },
}))

window.addEventListener('storage', (event) => {
  if (event.key === KEY || event.key === null) usePreferences.setState({ values: read() })
})

/** A preference belongs to one account in this browser, including across sign-outs. */
export function useLocalAgentPreference() {
  const user = useApp((state) => state.user)
  const account = user ? `${user.classId}:${user.sid}` : ''
  const enabled = usePreferences((state) => state.values[account] ?? true)
  const setEnabled = usePreferences((state) => state.setEnabled)
  return { enabled, setEnabled: (value: boolean) => { if (account) setEnabled(account, value) } }
}
