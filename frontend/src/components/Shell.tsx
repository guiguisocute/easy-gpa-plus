import { userErrorMessage } from '@/api/errorMessages'
/* 应用外壳：238px 侧栏 + 57px 顶栏 + 窄屏抽屉。结构对齐原型 shell。 */

import { Fragment, useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Menu, X } from 'lucide-react'
import { BrandMark, Icon, ThemeToggle } from './ui'
import { mono } from '../lib/style'
import { NAV_LABEL, workspaceNav, navGroups, type NavEntry, type View } from '@/lib/nav'
import { ROLE_LABEL, ROLE_RANK, type Role } from '@/lib/types'
import { useApp, useEffectiveRole } from '@/stores/app'
import { authApi, useAccountActions, useDeploy, useTenants, useWindow } from '@/api/queries'
import * as fmt from '@/lib/format'
import { AgentLauncher } from './AgentPanel'
import { useLocalAgentPreference } from '@/stores/agentPreference'
import '../styles/mobile-shell.css'

function NavList({ entries, current, onGo }: { entries: NavEntry[]; current: View; onGo: (v: View) => void }) {
  return (
    <>
      {entries.map((n) => {
        const on = n.view === current
        return (
          <button
            key={n.view}
            type="button"
            onClick={() => onGo(n.view)}
            aria-current={on ? 'page' : undefined}
            className="hv-fg"
            style={{
              background: 'none',
              border: 0,
              borderTop: '1px solid var(--line2)',
              margin: 0,
              padding: '14px 0',
              font: 'inherit',
              textAlign: 'left',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              gap: 12,
              whiteSpace: 'nowrap',
              color: on ? 'var(--fg)' : 'var(--fg2)',
            }}
          >
            <span
              style={{
                width: 17,
                height: 17,
                flex: 'none',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: on ? 'var(--red)' : 'var(--fg3)',
              }}
            >
              <Icon d1={n.d1} d2={n.d2 || n.d1} />
            </span>
            <span style={{ fontSize: 13, fontWeight: on ? 600 : 400, letterSpacing: '-.02em' }}>{n.label}</span>
            <span style={{ marginLeft: 'auto', width: 14, height: 1.5, background: on ? 'var(--red)' : 'transparent' }} />
          </button>
        )
      })}
    </>
  )
}

/** 侧栏顶部那个红按钮。整份导航里只会有一条 sidebar:'primary'（学生的「提交材料」）：
    只有学生绝大多数时间都在做同一件事，小组和班管手上做什么随阶段换，挑一条描红反而误导。
    直角实心，和品牌标记同一套几何；不做 disabled 态——它只是跳页，能不能交由那一页自己说。 */
function PrimaryNavButton({ entry, current, onGo }: { entry: NavEntry; current: View; onGo: (v: View) => void }) {
  const on = entry.view === current
  return (
    <button
      type="button"
      data-r="navprimary"
      className="hv-op82"
      onClick={() => onGo(entry.view)}
      aria-current={on ? 'page' : undefined}
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 9,
        width: '100%',
        minHeight: 42,
        margin: '0 0 22px',
        padding: '0 14px',
        border: '1px solid var(--red)',
        background: 'var(--red)',
        color: 'var(--onAccent)',
        /* 停在提交页时列表里一条都不描红——它本来就不在列表里。红底再红一次没有意义，
           改成往里收一圈底色，和列表行尾那道红杠一样只是「你在这儿」的记号。 */
        boxShadow: on ? 'inset 0 0 0 2px var(--bg)' : undefined,
        font: 'inherit',
        fontSize: 13.5,
        fontWeight: 600,
        letterSpacing: '-.015em',
        cursor: 'pointer',
        flex: 'none',
      }}
    >
      <Icon d1={entry.d1} d2={entry.d2 || entry.d1} size={16} />
      {entry.label}
    </button>
  )
}

/** 账号菜单。视角切换只降不升，审计记真实身份（§14）。
    settings 是收进这里的那一页（学生的「账号设置」）：它一学期点不了两次，
    没必要在侧栏常年占一行，而点自己的名字去改自己的邮箱密码本来就是最顺的路。 */
function AccountMenu({ mobile = false, settings, collective = false }: { mobile?: boolean; settings?: NavEntry; collective?: boolean }) {
  const { user, view, viewAs, menuOpen, toggleMenu, setViewAs, setDrawer, go, signOut, say } = useApp()
  const eff = useEffectiveRole()
  const queryClient = useQueryClient()
  const accountActions = useAccountActions()
  const actionsRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const actionsId = mobile ? 'mobile-account-actions' : 'sidebar-account-actions'

  useEffect(() => {
    const actions = actionsRef.current
    if (menuOpen && actions?.getClientRects().length) {
      actions.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus({ preventScroll: true })
    }
  }, [menuOpen])

  if (!user) return null

  const switchView = async (role: Role, self: boolean) => {
    try {
      // 后端记录真实身份与目标视角；清掉上一视角的查询缓存，避免首页先显示
      // 旧角色缓存或停在空结果上，新的页面会按自己的查询重新取数。
      await accountActions.viewAs.mutateAsync(role)
      queryClient.removeQueries()
      setViewAs(self ? null : role)
      if (mobile) setDrawer(false)
      say(self ? '已回到本身身份' : `已切到 ${ROLE_LABEL[role]} 视角`)
    } catch (error) {
      say(userErrorMessage(error, '切换视角失败，请重试'))
    }
  }

  /* 先让后端作废 refresh cookie，再清本地状态。
     顺序反过来的话，cookie 会在浏览器里留到过期，换个标签页刷新就"自动登录"回来了。 */
  const logout = () => {
    void authApi.logout().finally(() => {
      signOut()
      say('已退出登录')
    })
  }

  const lower: Role[] =
    user.role === 'ops'
      ? []
      : (Object.keys(ROLE_RANK) as Exclude<Role, 'ops'>[])
          .filter((r) => ROLE_RANK[r] <= ROLE_RANK[user.role as Exclude<Role, 'ops'>])
          .sort((a, b) => ROLE_RANK[b] - ROLE_RANK[a])
  /* 学生只降不到别的身份，lower 里就剩他自己——那一行写着「学生 · 本身身份」，
     点了什么也不会发生。只有真能切的时候才把视角那一段放出来。 */
  const canSwitch = !collective && lower.length > 1

  return (
    <div className="shell-account" style={{ marginTop: 'auto', position: 'relative', display: 'flex', flexDirection: 'column' }}>
      {menuOpen && (
        <div
          ref={actionsRef}
          id={actionsId}
          className="shell-account-actions"
          role="group"
          aria-label="账号操作"
          onKeyDown={(event) => {
            if (event.key !== 'Escape') return
            event.stopPropagation()
            toggleMenu()
            triggerRef.current?.focus({ preventScroll: true })
          }}
          style={{
            position: 'absolute',
            left: 0,
            right: 0,
            bottom: 'calc(100% + 8px)',
            border: '1px solid var(--line)',
            background: 'var(--bg)',
            zIndex: 56,
            boxShadow: '0 -10px 34px rgba(0,0,0,.14)',
            animation: 'menupop .18s ease both',
            display: 'flex',
            flexDirection: 'column',
            padding: '6px 0',
          }}
        >
          {settings && (
            <button
              type="button"
              className="hv-sub"
              onClick={() => { toggleMenu(); go(settings.view) }}
              aria-current={view === settings.view ? 'page' : undefined}
              style={{
                background: view === settings.view ? 'var(--sub)' : 'transparent',
                border: 0,
                /* 下面接着视角那一段时才要这条线；只剩「退出登录」的话它自己带了一条。 */
                borderBottom: canSwitch ? '1px solid var(--line2)' : 0,
                margin: canSwitch ? '0 0 6px' : 0,
                padding: '11px 14px',
                font: 'inherit',
                fontSize: 12.5,
                textAlign: 'left',
                cursor: 'pointer',
                color: 'var(--fg)',
                display: 'flex',
                alignItems: 'center',
                gap: 9,
              }}
            >
              <span style={{ width: 15, height: 15, flex: 'none', color: 'var(--fg3)' }}>
                <Icon d1={settings.d1} d2={settings.d2 || settings.d1} size={15} />
              </span>
              {settings.label}
            </button>
          )}
          {canSwitch && (
            <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', padding: '10px 14px 8px' }}>
              当前身份 · {ROLE_LABEL[user.role]}
            </div>
          )}
          {canSwitch && lower.map((r) => {
            const on = r === eff
            const self = r === user.role
            return (
              <button
                key={r}
                type="button"
                disabled={accountActions.viewAs.isPending}
                className="hv-sub"
                onClick={() => void switchView(r, self)}
                style={{
                  background: on ? 'var(--sub)' : 'transparent',
                  border: 0,
                  margin: 0,
                  padding: '10px 14px',
                  font: 'inherit',
                  fontSize: 12.5,
                  textAlign: 'left',
                  cursor: 'pointer',
                  color: on ? 'var(--fg)' : 'var(--fg2)',
                  display: 'flex',
                  alignItems: 'center',
                  gap: 9,
                }}
              >
                <span style={{ width: 5, height: 5, flex: 'none', background: on ? 'var(--red)' : 'var(--line)' }} />
                {self ? `${ROLE_LABEL[r]} · 本身身份` : `以 ${ROLE_LABEL[r]} 视角查看`}
              </button>
            )
          })}
          <button
            type="button"
            className="hv-sub"
            onClick={logout}
            style={{
              background: 'none',
              border: 0,
              borderTop: '1px solid var(--line2)',
              margin: '6px 0 0',
              padding: '11px 14px',
              font: 'inherit',
              fontSize: 12.5,
              textAlign: 'left',
              cursor: 'pointer',
              color: 'var(--red)',
            }}
          >
            退出登录
          </button>
        </div>
      )}
      <button
        ref={triggerRef}
        type="button"
        className="hv-op72"
        data-shell-account-toggle=""
        onClick={toggleMenu}
        aria-expanded={menuOpen}
        aria-controls={menuOpen ? actionsId : undefined}
        style={{
          background: 'none',
          border: 0,
          borderTop: '1px solid var(--line)',
          margin: 0,
          padding: '14px 0 4px',
          font: 'inherit',
          textAlign: 'left',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          width: '100%',
        }}
      >
        <span
          style={{
            width: 26,
            height: 26,
            flex: 'none',
            background: 'var(--fg)',
            border: '1px solid var(--fg)',
            color: 'var(--bg)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            font: "600 12px/1 'Instrument Sans','Noto Sans SC',sans-serif",
          }}
        >
          {user.initial}
        </span>
        <span style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0, flex: 1 }}>
          <span style={{ fontSize: 12.5, fontWeight: 600, letterSpacing: '-.015em', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {user.name}
          </span>
          <span style={{ font: "400 11.5px/1.5 'JetBrains Mono',monospace", color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {collective ? '班级成员 · 共治测试版' : viewAs ? `${ROLE_LABEL[viewAs]} 视角` : user.sub}
          </span>
        </span>
        <span style={{ fontSize: 8, color: 'var(--fg3)', flex: 'none' }}>▲</span>
      </button>
    </div>
  )
}

export default function Shell({ children, collective = false }: { children: React.ReactNode; collective?: boolean }) {
  const localAgent = useLocalAgentPreference()
  const { user, view, drawer, setDrawer, go, menuOpen, toggleMenu } = useApp()
  const role = useEffectiveRole()
  const eff = collective ? 'student' : role
  const entries = workspaceNav(role, collective).filter((entry) => !entry.deputyOnly || (user?.role === 'group' && user.isDeputy))
  const primary = entries.find((entry) => entry.sidebar === 'primary')
  const settings = entries.find((entry) => entry.sidebar === 'account')
  const groups = navGroups(entries, NAV_LABEL[eff])
  const head = entries.find(entry => entry.view === view) ?? entries[0]
  const current = head.under ?? view
  const navLabel = collective ? '班级共治导航' : NAV_LABEL[eff]
  const shellRef = useRef<HTMLDivElement>(null)
  const drawerRef = useRef<HTMLElement>(null)
  const drawerTriggerRef = useRef<HTMLButtonElement>(null)

  /* Esc 关抽屉与账号菜单。浮层不给键盘退路是最常见的可达性缺陷。 */
  useEffect(() => {
    if (!drawer && !menuOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      if (menuOpen) {
        toggleMenu()
        const scope = drawer ? drawerRef.current : shellRef.current?.querySelector('[data-r="side"]')
        scope?.querySelector<HTMLButtonElement>('[data-shell-account-toggle]')?.focus({ preventScroll: true })
      } else if (drawer) setDrawer(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [drawer, menuOpen, setDrawer, toggleMenu])

  /* 手机抽屉是模态导航：固定背景以兼容 iOS，关闭后恢复原滚动位置。
     导航内部仍可滚动，键盘焦点不能落到被遮住的页面里。 */
  useEffect(() => {
    const panel = drawerRef.current
    if (!drawer || !panel) return
    const trigger = drawerTriggerRef.current
    const scrollY = window.scrollY
    const scrollbarWidth = window.innerWidth - document.documentElement.clientWidth
    const openedView = useApp.getState().view
    const body = document.body
    const previous = {
      position: body.style.position,
      top: body.style.top,
      left: body.style.left,
      right: body.style.right,
      overflow: body.style.overflow,
    }
    Object.assign(body.style, { position: 'fixed', top: `-${scrollY}px`, left: '0', right: `${scrollbarWidth}px`, overflow: 'hidden' })
    panel.querySelector<HTMLButtonElement>('[aria-label="关闭导航"]')?.focus({ preventScroll: true })

    const onTab = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') return
      const focusable = [...panel.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])')]
        .filter((element) => element.getClientRects().length > 0)
      const first = focusable[0]
      const last = focusable.at(-1)
      if (!first || !last) return
      if (!panel.contains(document.activeElement) || (event.shiftKey && document.activeElement === first)) {
        event.preventDefault()
        const next = event.shiftKey ? last : first
        next.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onTab)

    // 横屏或窗口拉宽后桌面侧栏已出现，不能留下不可见的模态状态。
    const observer = new ResizeObserver(([entry]) => {
      if (entry.contentRect.width > 900) setDrawer(false)
    })
    if (shellRef.current) observer.observe(shellRef.current)
    return () => {
      observer.disconnect()
      window.removeEventListener('keydown', onTab)
      Object.assign(body.style, previous)
      window.scrollTo(0, useApp.getState().view === openedView ? scrollY : 0)
      trigger?.focus({ preventScroll: true })
    }
  }, [drawer, setDrawer])

  if (!user) return null

  const sidebarInner = (mobile: boolean) => (
    <>
      <div className="shell-brand" style={{ display: 'flex', alignItems: 'center', gap: 11, paddingBottom: mobile ? 26 : 30 }}>
        <BrandMark />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
          <div style={{ fontWeight: 600, letterSpacing: '-.025em', fontSize: 13, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {eff === 'ops' ? '平台运维' : user.className}
          </div>
          <div style={{ fontSize: 12, letterSpacing: '.02em', color: 'var(--fg3)' }}>{collective ? '共治工作台' : '综测统计平台'}</div>
        </div>
        {mobile && (
          <button
            type="button"
            className="hv-line-fg"
            onClick={() => { setDrawer(false); if (menuOpen) toggleMenu() }}
            aria-label="关闭导航"
            style={{ background: 'none', border: '1px solid var(--line)', margin: '0 0 0 auto', padding: 0, width: 30, height: 30, borderRadius: 999, cursor: 'pointer', color: 'var(--fg2)', display: 'flex', alignItems: 'center', justifyContent: 'center', flex: 'none' }}
          >
            <X size={15} strokeWidth={1.6} />
          </button>
        )}
      </div>
      {primary && <PrimaryNavButton entry={primary} current={view} onGo={go} />}
      <nav aria-label={navLabel} style={{ display: 'flex', flexDirection: 'column', flex: '0 1 auto', minHeight: 0, overflowY: 'auto' }}>
        {groups.map((group, i) => (
          <Fragment key={group.id}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', padding: i === 0 ? '0 0 10px' : '24px 0 10px' }}>
              {group.title}
            </div>
            <NavList entries={group.entries} current={current} onGo={go} />
          </Fragment>
        ))}
      </nav>
    </>
  )

  return (
    <div ref={shellRef} data-r="shell" style={{ display: 'flex', minHeight: '100vh' }}>
      <aside
        data-r="side"
        inert={drawer}
        style={{
          width: 238,
          flex: 'none',
          position: 'sticky',
          top: 0,
          height: '100vh',
          zIndex: 30,
          borderRight: '1px solid var(--line)',
          display: 'flex',
          flexDirection: 'column',
          padding: '26px 22px 20px',
          background: 'var(--bg)',
        }}
      >
        {sidebarInner(false)}
        <AccountMenu settings={settings} collective={collective} />
      </aside>

      {drawer && (
        <>
          <div aria-hidden="true" onClick={() => { setDrawer(false); if (menuOpen) toggleMenu() }} style={{ position: 'fixed', inset: 0, zIndex: 68, background: 'rgba(0,0,0,.32)' }} />
          <aside
            ref={drawerRef}
            id="mobile-navigation"
            className="shell-drawer"
            role="dialog"
            aria-modal="true"
            aria-label="页面导航"
            style={{
              position: 'fixed',
              left: 0,
              top: 0,
              bottom: 0,
              width: 238,
              zIndex: 70,
              overflowY: 'auto',
              animation: 'drawin .22s ease both',
              boxShadow: '14px 0 44px rgba(0,0,0,.18)',
              borderRight: '1px solid var(--line)',
              display: 'flex',
              flexDirection: 'column',
              padding: '26px 22px 20px',
              background: 'var(--bg)',
            }}
          >
            {sidebarInner(true)}
            {/* 窄屏下侧栏整个被隐藏，账号菜单必须在抽屉里再出现一次，
                否则手机上既不能退出登录，也不能切视角。 */}
            <AccountMenu mobile settings={settings} collective={collective} />
          </aside>
        </>
      )}

      <main inert={drawer} style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        <header
          data-r="top"
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 16,
            padding: '0 44px',
            height: 57,
            borderBottom: '1px solid var(--line)',
            position: 'sticky',
            top: 0,
            background: 'var(--bg)',
            zIndex: 20,
          }}
        >
          <button
            ref={drawerTriggerRef}
            type="button"
            data-r="burger"
            className="hv-line-fg"
            onClick={() => { if (menuOpen) toggleMenu(); setDrawer(true) }}
            aria-label="打开导航"
            aria-expanded={drawer}
            aria-controls={drawer ? 'mobile-navigation' : undefined}
            aria-haspopup="dialog"
            style={{ display: 'none', background: 'none', border: '1px solid var(--line)', margin: 0, padding: 0, width: 32, height: 32, borderRadius: 999, cursor: 'pointer', color: 'var(--fg2)', alignItems: 'center', justifyContent: 'center', flex: 'none' }}
          >
            <Menu size={15} strokeWidth={1.6} />
          </button>
          <div className="shell-page-title">
            <span className="shell-page-en" style={mono()}>{head.en}</span>
            <span className="shell-page-label">{head.label}</span>
          </div>
          <div data-r="hidesm" style={{ width: 1, height: 14, background: 'var(--line)' }} />
          <div data-r="hidesm" style={{ fontSize: 13, color: 'var(--fg2)' }}>
            {head.desc}
          </div>
          {/* 页面自己的顶栏控件挂在这里（见 ui.tsx 的 TopbarSlot）。空着时宽度为 0，
              右侧那组仍旧靠 margin-left:auto 顶到最右，其他页面看不出区别。 */}
          <div data-r="topslot" style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }} />
          <div className="shell-top-actions" style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 12 }}>
            <TopbarStatus ops={eff === 'ops'} />
            <ThemeToggle />
          </div>
        </header>

        <div data-r="pad" style={{ padding: '0 44px 110px', flex: 1, position: 'relative' }}>
          {children}
          <footer style={{ marginTop: 32, fontSize: 11, color: 'var(--fg3)' }}><a href="/source.tar.gz" download style={{ color: 'inherit' }}>EasyGPA Plus · AGPL-3.0 · 源码</a></footer>
        </div>
      </main>
      {eff !== 'ops' && localAgent.enabled && <div inert={drawer} style={{ display: 'contents' }}><AgentLauncher /></div>}
    </div>
  )
}

/* 顶栏右侧那行状态。抽成独立组件是为了让取数只发生在真正需要它的分支上：
   学生不该因为渲染一个外壳就去打运维台的 /ops/tenants。 */
function TopbarStatus({ ops }: { ops: boolean }) {
  return ops ? <OpsStatus /> : <WindowStatus />
}

function OpsStatus() {
  const tenants = useTenants()
  const deploy = useDeploy()
  const running = (tenants.data?.items ?? []).filter((t) => !t.archived).length
  return (
    <div data-r="hidesm" style={{ fontSize: 12.5, color: 'var(--fg3)', whiteSpace: 'nowrap' }}>
      {running} 个已启用班级 · 数据库版本 {deploy.data?.migrationVersion ?? '—'} · {deploy.data?.imageTag ?? 'dev'}
    </div>
  )
}

function WindowStatus() {
  const win = useWindow()
  if (!win.data) return null
  const left = fmt.daysUntil(win.data.window.close)
  return (
    <div data-r="hidesm" style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
      <span style={{ fontSize: 12.5, color: 'var(--fg3)', whiteSpace: 'nowrap' }}>开放窗口至 {fmt.date(win.data.window.close)}</span>
      <span style={{ font: "400 11.5px/1 'JetBrains Mono',monospace", color: left <= 3 ? 'var(--red)' : 'var(--fg3)', whiteSpace: 'nowrap' }}>
        {left > 0 ? `剩 ${left} 天` : '已截止'}
      </span>
    </div>
  )
}
