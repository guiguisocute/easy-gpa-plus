/* 应用级 UI 状态。业务数据一律走 TanStack Query，不进这里——
   两套状态源混在一起是这类后台最常见的腐化方式。 */

import { create } from 'zustand'
import type { Role, User } from '@/lib/types'
import { ROLE_RANK } from '@/lib/types'
import { ALL_VIEWS, NAV, type View } from '@/lib/nav'
import { api, refreshSession, setAccessToken, setSessionLostHandler } from '@/api/client'
import type { AppliedAgentAction, AgentPageContext } from '@/api/types'

export type Theme = 'light' | 'dark'

interface Toast {
  id: number
  msg: string
}

interface AppState {
  user: User | null
  /** 会话恢复完成前不渲染登录浮层，避免刷新闪一下"请登录" */
  sessionReady: boolean
  /** 视角切换（§14）：只降不升，只改渲染不改鉴权 */
  viewAs: Role | null
  view: View
  /** 从「我的提交」点继续编辑时带过来的草稿 id；提交页据此把那一条读回表单。 */
  openDraftId: string | null
  /** 从各入口跳到「发起申诉」时带过来的对象，形如 `submission:s-cadre`。
      发起页据此直接选中那一笔，而不是让学生自己再找一遍。 */
  openAppealTarget: string | null
  /** Agent 只把草稿/预填交给目标页面；页面消费后立即清除，不作为鉴权依据。 */
  agentHandoff: AppliedAgentAction | null
  agentFocus: AgentPageContext | null
  setAgentFocus: (value: AgentPageContext | null) => void
  theme: Theme
  drawer: boolean
  menuOpen: boolean
  toast: Toast | null

  setUser: (u: User | null) => void
  /** 页面加载时用 HttpOnly refresh cookie 换一次 access token，换不到就当未登录 */
  restore: () => Promise<void>
  signOut: () => void
  go: (v: View, draftId?: string) => void
  /** 跳到「发起申诉」并选中某个对象。 */
  goAppeal: (targetType: string, targetId: string) => void
  setAgentHandoff: (value: AppliedAgentAction | null) => void
  setViewAs: (r: Role | null) => void
  setTheme: (t: Theme) => void
  toggleTheme: () => void
  setDrawer: (v: boolean) => void
  toggleMenu: () => void
  say: (msg: string) => void
  clearToast: () => void
}

const THEME_KEY = 'zc.theme'

function readTheme(): Theme {
  const attr = document.documentElement.getAttribute('data-theme')
  return attr === 'dark' ? 'dark' : 'light'
}

/** 视图写进 query string，刷新与分享链接都能落回同一页。 */
function readView(): View | null {
  const v = new URLSearchParams(location.search).get('v')
  if (!v) return null
  const known = ALL_VIEWS.has(v as View)
  return known ? (v as View) : null
}

/** 「继续编辑」要带上目标草稿 id。和 view 一样写进 query string，
    刷新或把链接发给自己都能落回同一条草稿，而不是又开一张空表单。 */
function readDraft(): string | null {
  return new URLSearchParams(location.search).get('draft')
}

/** `?appeal=submission:s-cadre`。进 URL 而不是留在内存里：这条链接学生会截图发同学。 */
function readAppealTarget(): string | null {
  const raw = new URLSearchParams(location.search).get('appeal')
  return raw && /^(submission|base_score|penalty_score):.+$/.test(raw) ? raw : null
}

function writeView(v: View, draft?: string | null, appeal?: string | null, preserveDetail = false) {
  const url = new URL(location.href)
  url.searchParams.set('v', v)
  if (!preserveDetail) url.searchParams.delete('proposal')
  if (draft) url.searchParams.set('draft', draft)
  else url.searchParams.delete('draft')
  if (appeal) url.searchParams.set('appeal', appeal)
  else url.searchParams.delete('appeal')
  history.replaceState(null, '', url)
}

let toastSeq = 0

export const useApp = create<AppState>((set, get) => ({
  user: null,
  sessionReady: false,
  viewAs: null,
  view: readView() ?? 'stuHome',
  openDraftId: readDraft(),
  openAppealTarget: readAppealTarget(),
  agentHandoff: null,
  agentFocus: null,
  setAgentFocus: (agentFocus) => set({ agentFocus }),
  theme: readTheme(),
  drawer: false,
  menuOpen: false,
  toast: null,

  setUser: (user) => {
    if (!user) {
      set({ user: null, viewAs: null, menuOpen: false, drawer: false, agentHandoff: null, agentFocus: null })
      return
    }
    /* 登录后落到该角色的首页；除非 URL 里带的 view 本来就属于这个角色。 */
    const urlView = readView()
    // 班级模式尚未读取，先保留合法深链接；App 在模式就绪后统一校验。
    const view = urlView ?? NAV[user.role][0].view
    /* 只有停在 URL 原本那个视图上才留着 ?draft=：被弹回角色首页还带着草稿 id 没有意义。 */
    const draftId = view === urlView ? get().openDraftId : null
    const appeal = view === urlView ? get().openAppealTarget : null
    writeView(view, draftId, appeal, view === urlView)
    set({ user, view, viewAs: null, openDraftId: draftId, openAppealTarget: appeal })
  },

  restore: async () => {
    /* refresh 只轮换 token、不带 user，所以拿到 token 后还要读一次 /me。
       这样账号被停用或被降级时，刷新页面立刻反映出来，而不是等下一次写操作被拒。 */
    try {
      if (await refreshSession()) {
        get().setUser(await api.get<User>('/me'))
      }
    } catch {
      /* 网络不通或会话已作废，按未登录处理；登录页自己会再报错 */
      setAccessToken(null)
    } finally {
      set({ sessionReady: true })
    }
  },

  signOut: () => {
    setAccessToken(null)
    get().setUser(null)
  },

  go: (view, draftId) => {
    writeView(view, draftId)
    set({ view, openDraftId: draftId ?? null, openAppealTarget: null, menuOpen: false, drawer: false })
  },

  goAppeal: (targetType, targetId) => {
    const target = `${targetType}:${targetId}`
    writeView('stuFileAppeal', null, target)
    set({ view: 'stuFileAppeal', openDraftId: null, openAppealTarget: target, menuOpen: false, drawer: false })
  },

  setAgentHandoff: (agentHandoff) => set({ agentHandoff }),

  setViewAs: (role) => {
    const { user } = get()
    if (!user) return
    /* 只降不升：ops 不参与降级；目标角色等级必须低于真实身份。 */
    if (role !== null) {
      if (user.role === 'ops' || role === 'ops') return
      const self = ROLE_RANK[user.role as Exclude<Role, 'ops'>]
      const want = ROLE_RANK[role as Exclude<Role, 'ops'>]
      if (want >= self) return
    }
    const effective = role ?? user.role
    const view = NAV[effective][0].view
    writeView(view, null)
    set({ viewAs: role, view, openDraftId: null, openAppealTarget: null, menuOpen: false, agentHandoff: null })
  },

  setTheme: (theme) => {
    document.documentElement.setAttribute('data-theme', theme)
    try {
      localStorage.setItem(THEME_KEY, theme)
    } catch {
      /* 隐私模式下 localStorage 会抛，主题降级为本次会话有效即可 */
    }
    set({ theme })
  },

  toggleTheme: () => get().setTheme(get().theme === 'dark' ? 'light' : 'dark'),

  setDrawer: (drawer) => set({ drawer }),
  toggleMenu: () => set((s) => ({ menuOpen: !s.menuOpen })),

  say: (msg) => {
    const id = ++toastSeq
    set({ toast: { id, msg } })
    window.setTimeout(() => {
      /* 只清自己那条：连续两次 say 时，先来的定时器不该把后来的顶掉。 */
      if (get().toast?.id === id) set({ toast: null })
    }, 2600)
  },

  clearToast: () => set({ toast: null }),
}))

/* client 的 401 兜底：刷新也救不回来时清掉会话，浮层会自己回到登录态。
   用回调注册而不是让 client 直接 import store，避免两个模块互相依赖。 */
setSessionLostHandler(() => useApp.getState().signOut())

/** 当前生效角色。视角切换只改它，鉴权仍看 user.role。 */
export function useEffectiveRole(): Role {
  const user = useApp((s) => s.user)
  const viewAs = useApp((s) => s.viewAs)
  return viewAs ?? user?.role ?? 'student'
}
