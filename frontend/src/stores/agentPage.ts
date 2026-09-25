import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react'
import { create } from 'zustand'
import type { AgentPageContext } from '@/api/types'
import { useApp } from './app'

type Page = AgentPageContext & {
  resourceKind: 'submission' | 'appeal'
  resourceId: string
  resourceLabel: string
  evidenceCount: number
  observedVersion: string
  userKey: string
  owner: symbol
  applyReason: (reason: string, expected: AgentPageContext['draft']) => boolean
}
interface State { current: Page | null; pinned: Page | null; selectedEvidence: string }
export const useAgentPage = create<State>(() => ({ current: null, pinned: null, selectedEvidence: '' }))

// Unsaved text stays in memory, scoped to the actor, page, item and business
// version. It is never localStorage data or a backend business draft.
const formFields = new Map<string, string>()
export const agentComposerDrafts = new Map<string, string>()
const unsubscribe = useApp.subscribe((next, previous) => {
  if (next.user?.sid !== previous.user?.sid || next.user?.classId !== previous.user?.classId || next.user?.role !== previous.user?.role || next.viewAs !== previous.viewAs) {
    formFields.clear(); agentComposerDrafts.clear()
    useAgentPage.setState({ current: null, pinned: null, selectedEvidence: '' })
  }
})
if (import.meta.hot) import.meta.hot.dispose(unsubscribe)

export function useAgentFormField<T extends string>(kind: 'submission' | 'appeal', id: string, version: string | undefined, field: string, initial: T): [T, Dispatch<SetStateAction<T>>] {
  const view = useApp((s) => s.view)
  const key = `${userKey()}:${view}:${kind}:${id}:${version ?? ''}:${field}`
  const [state, setState] = useState(() => ({ key, value: (formFields.get(key) ?? initial) as T }))
  const value = state.key === key ? state.value : (formFields.get(key) ?? initial) as T
  return [value, (next) => {
    const updated = typeof next === 'function' ? next(value) : next
    formFields.delete(key); formFields.set(key, updated)
    if (formFields.size > 200) formFields.delete(formFields.keys().next().value!)
    setState({ key, value: updated })
  }]
}

export function pageKey(page: AgentPageContext | null | undefined) {
  return `${page?.view ?? ''}:${page?.resourceKind ?? ''}:${page?.resourceId ?? ''}`
}
export function sameAgentDraft(a: AgentPageContext['draft'], b: AgentPageContext['draft']) {
  return a?.reason === b?.reason && a?.score === b?.score && a?.category === b?.category && a?.itemKey === b?.itemKey
}
function userKey() { const user = useApp.getState().user; return user ? `${user.classId}:${user.sid}:${user.role}:${useApp.getState().viewAs ?? ''}` : '' }

export function currentAgentPage() {
  const page = useAgentPage.getState().current
  return page?.userKey === userKey() && page.view === useApp.getState().view ? page : null
}

export function effectiveAgentPage() {
  const state = useAgentPage.getState()
  const current = currentAgentPage()
  const pinned = state.pinned?.userKey === userKey() ? state.pinned : null
  const target = pinned && pageKey(pinned) !== pageKey(current) ? pinned : current
  return target ? { ...target, evidenceId: target === current ? state.selectedEvidence || undefined : target.evidenceId } : null
}

export function toggleAgentPagePin() {
  useAgentPage.setState((state) => ({ pinned: state.pinned ? null : effectiveAgentPage() }))
}

export function selectAgentEvidence(id: string) {
  const effective = effectiveAgentPage()
  if (effective && pageKey(effective) !== pageKey(currentAgentPage())) {
    useAgentPage.setState((state) => ({ pinned: state.pinned ? { ...state.pinned, evidenceId: id || undefined } : null }))
  } else useAgentPage.setState({ selectedEvidence: id })
}

// Pages register only their typed locator and unsaved form fields. Neither DOM
// text nor the query cache is copied into model messages.
export function useRegisterAgentPage(input: {
  kind: 'submission' | 'appeal'; id: string; label: string; evidenceCount: number; version?: string
  draft: NonNullable<AgentPageContext['draft']>; onReason: (value: string) => void; editable: boolean
} | null) {
  const view = useApp((s) => s.view)
  const user = useApp((s) => s.user)
  const viewAs = useApp((s) => s.viewAs)
  const owner = useRef(Symbol('agent page'))
  const latest = useRef(input)
  latest.current = input
  const draftJSON = JSON.stringify(input?.draft)
  const kind = input?.kind, id = input?.id, label = input?.label, evidenceCount = input?.evidenceCount, version = input?.version
  const page = useMemo<Page | null>(() => kind && id && label && user ? {
    view, resourceKind: kind, resourceId: id, resourceLabel: label,
    evidenceCount: evidenceCount ?? 0, observedVersion: version ?? '',
    draft: JSON.parse(draftJSON) as AgentPageContext['draft'], userKey: `${user.classId}:${user.sid}:${user.role}:${viewAs ?? ''}`, owner: owner.current,
    applyReason: (reason, expected) => {
      const now = latest.current
      if (!now?.editable || !sameAgentDraft(now.draft, expected)) return false
      now.onReason(reason)
      return true
    },
  } : null, [view, user, viewAs, kind, id, label, evidenceCount, version, draftJSON])
  useEffect(() => {
    if (!page) return
    useAgentPage.setState({ current: page })
  }, [page])
  useEffect(() => {
    const token = owner.current
    return () => useAgentPage.setState((state) => state.current?.owner === token ? { current: null, selectedEvidence: '' } : {})
  }, [id, view])
}
