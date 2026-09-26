import type { Role, User } from './types.ts'

type SessionState = {
  user: Pick<User, 'sid' | 'classId' | 'role' | 'isDeputy'> | null
  viewAs: Role | null
}

/** Cache keys are shared across accounts, so invalidate them synchronously at
 * every identity/permission boundary, before another account can render. */
export function clearPreviousSession(cache: { clear: () => void }, state: SessionState, previous: SessionState) {
  const user = state.user
  const before = previous.user
  if (user?.sid !== before?.sid || user?.classId !== before?.classId || user?.role !== before?.role ||
      Boolean(user?.isDeputy) !== Boolean(before?.isDeputy) || state.viewAs !== previous.viewAs) {
    cache.clear()
  }
}
