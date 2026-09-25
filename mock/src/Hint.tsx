import { useState, type CSSProperties } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useApp } from '@/stores/app'
import { authApi } from '@/api/queries'
import { ApiError, api, setAccessToken } from '@/api/client'
import type { User } from '@/lib/types'
import { db, DEMO_PASSWORD, TOUR_CASTS, TOUR_SID, isTourUser, userBySid, type TourCast } from './db'

const ACCOUNTS = [
  ['20240001', '陈屿', '学生'],
  ['20240002', '李审核', '综测小组'],
  ['20240003', '王管理', '班级管理员'],
  ['20240006', '周复评', '综测小组'],
  ['ops@demo.local', '运维', '运维超管'],
] as const

const CHROME_KEY = 'easygpa.mock.chrome'

const panel: CSSProperties = {
  position: 'fixed',
  right: 24,
  bottom: 24,
  zIndex: 80,
  width: 280,
  padding: '16px 18px',
  background: 'var(--bg)',
  border: '1px solid var(--line)',
  color: 'var(--fg)',
}

const chip: CSSProperties = {
  position: 'fixed',
  right: 24,
  bottom: 24,
  zIndex: 80,
  border: '1px solid var(--line)',
  background: 'var(--bg)',
  color: 'var(--fg3)',
  font: "500 11px/1 'JetBrains Mono',monospace",
  letterSpacing: '.18em',
  padding: '8px 10px',
  cursor: 'pointer',
}

const resetBtn: CSSProperties = {
  border: '1px solid var(--line)',
  background: 'var(--bg)',
  color: 'var(--fg3)',
  font: "500 11px/1 'JetBrains Mono',monospace",
  letterSpacing: '.08em',
  padding: '8px 10px',
  cursor: 'pointer',
}

function currentCast(user: User): TourCast {
  if (user.role === 'class_admin') return 'class_admin'
  if (user.role === 'group' && user.isDeputy) return 'deputy'
  if (user.role === 'group') return 'group'
  return 'student'
}

function resetDemo() {
  sessionStorage.removeItem('easygpa.mock.sid')
  sessionStorage.removeItem('easygpa.mock.store')
  location.href = location.pathname
}

function readOpen(): boolean {
  try {
    return sessionStorage.getItem(CHROME_KEY) !== 'hide'
  } catch {
    return true
  }
}

function writeOpen(open: boolean) {
  try {
    sessionStorage.setItem(CHROME_KEY, open ? 'show' : 'hide')
  } catch {
    /* 隐私模式写不进去，本次会话内的显隐仍然有效 */
  }
}

function ChromeHead({ label, onHide }: { label: string; onHide: () => void }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <div style={{ font: "500 10px/1 'JetBrains Mono',monospace", letterSpacing: '.18em', color: 'var(--fg3)' }}>{label}</div>
      <button
        type="button"
        onClick={onHide}
        title="收起演示浮层"
        style={{
          marginLeft: 'auto',
          border: 0,
          background: 'none',
          color: 'var(--fg3)',
          font: "500 11px/1 'JetBrains Mono',monospace",
          letterSpacing: '.08em',
          padding: 0,
          cursor: 'pointer',
        }}
      >
        隐藏
      </button>
    </div>
  )
}

function CollectiveHint({onHide}:{onHide:()=>void}) {
  const user=useApp(s=>s.user), say=useApp(s=>s.say), queryClient=useQueryClient()
  const [pending,setPending]=useState(false)
  return <aside style={{...panel,width:210,padding:'12px 14px'}}>
    <ChromeHead label="共治演示" onHide={onHide}/>
    <p style={{fontSize:11,color:'var(--fg3)',lineHeight:1.7,marginBottom:0}}>虚构成员与预置提案，共用一个成员工作台。</p>
    <button type="button" disabled={pending} style={{...resetBtn,marginTop:12,width:'100%'}} onClick={async()=>{
      setPending(true)
      try {
        if(user) {await api.post('/governance/demo/advance');await queryClient.invalidateQueries();say('已模拟经过四天，未代投任何票')}
        else {const session=await authApi.login('20240003',DEMO_PASSWORD);setAccessToken(session.access_token);useApp.getState().setUser(session.user as unknown as User);useApp.getState().go('govHome')}
      } catch(e) {say(e instanceof Error?e.message:'操作失败')} finally {setPending(false)}
    }}>{user?'推进演示时间':'返回共治工作台'}</button>
    <button type="button" onClick={resetDemo} style={{...resetBtn,marginTop:8,width:'100%'}}>重置演示 · 重新选择</button>
    <a href="/source.tar.gz" download style={{display:'block',marginTop:8,fontSize:11,color:'var(--fg3)'}}>AGPL-3.0 · 演示站源码</a>
  </aside>
}

export default function DemoHint() {
  const user = useApp((s) => s.user)
  const ready = useApp((s) => s.sessionReady)
  const setUser = useApp((s) => s.setUser)
  const say = useApp((s) => s.say)
  const queryClient = useQueryClient()
  /* 记住正在登的是哪个账号，而不是只记一个 busy 布尔：几行同时变灰看不出
     点的是哪一个，而演示站上最常见的操作就是连着换身份。 */
  const [pending, setPending] = useState('')
  const [open, setOpen] = useState(readOpen)

  const hide = () => {
    writeOpen(false)
    setOpen(false)
  }
  const show = () => {
    writeOpen(true)
    setOpen(true)
  }

  if (!ready) return null

  if (!open) {
    return (
      <button type="button" onClick={show} title="显示演示浮层" style={chip}>
        DEMO
      </button>
    )
  }

  if (db().demoMode === 'collective' || db().governance?.mode === 'collective') return <CollectiveHint onHide={hide}/>

  if (user && isTourUser(user)) {
    const on = currentCast(user)
    const switchCast = async (identity: TourCast) => {
      if (pending || identity === on) return
      setPending(identity)
      try {
        const next = await api.post<User>('/me/demo-cast', { identity })
        queryClient.clear()
        setUser(next)
        say(`已切到${TOUR_CASTS.find((item) => item.id === identity)?.label ?? identity}`)
      } catch (e) {
        say(e instanceof ApiError ? e.message : '切换身份失败')
      } finally {
        setPending('')
      }
    }
    return (
      <aside style={panel}>
        <ChromeHead label="TOUR" onHide={hide} />
        <div style={{ marginTop: 8, fontSize: 13, fontWeight: 600 }}>体验账号 · 切身份即可，不必登出</div>
        <p style={{ margin: '8px 0 0', fontSize: 12, color: 'var(--fg3)', lineHeight: 1.65 }}>
          退出登录会清空本页全部演示数据。切到综测小组可按真实流程审核刚提交的材料。
        </p>
        <ul style={{ margin: '12px 0 0', padding: 0, listStyle: 'none' }}>
          {TOUR_CASTS.map((item) => (
            <li key={item.id}>
              <button
                type="button"
                disabled={!!pending}
                onClick={() => void switchCast(item.id)}
                style={{
                  display: 'flex',
                  width: '100%',
                  margin: 0,
                  padding: '6px 6px',
                  border: 0,
                  background: on === item.id ? 'var(--sub)' : 'transparent',
                  color: 'var(--fg2)',
                  font: 'inherit',
                  fontSize: 12.5,
                  lineHeight: 1.85,
                  textAlign: 'left',
                  cursor: pending ? 'progress' : 'pointer',
                  opacity: pending && pending !== item.id ? 0.45 : 1,
                }}
              >
                <span style={{ width: 5, height: 5, margin: '8px 8px 0 0', flex: 'none', background: on === item.id ? 'var(--red)' : 'var(--line)' }} />
                {item.label}
                <span style={{ marginLeft: 'auto', color: 'var(--fg3)' }}>
                  {pending === item.id ? '切换中…' : on === item.id ? '当前' : ''}
                </span>
              </button>
            </li>
          ))}
        </ul>
        <button
          type="button"
          onClick={resetDemo}
          title="清掉本机点过的演示数据并回到登录"
          style={{ ...resetBtn, marginTop: 12, width: '100%' }}
        >
          演示 · 重置
        </button>
        <a href="/source.tar.gz" download style={{fontSize:11,color:"var(--fg3)"}}>AGPL-3.0 · 演示站源码</a>
    </aside>
    )
  }

  if (user) {
    return (
      <aside style={{ ...panel, width: 200, padding: '12px 14px' }}>
        <ChromeHead label="DEMO" onHide={hide} />
        <button
          type="button"
          onClick={resetDemo}
          title="清掉本机点过的演示数据并回到登录"
          style={{ ...resetBtn, marginTop: 12, width: '100%' }}
        >
          演示 · 重置
        </button>
        <a href="/source.tar.gz" download style={{fontSize:11,color:"var(--fg3)"}}>AGPL-3.0 · 演示站源码</a>
    </aside>
    )
  }

  /* 点一行就登进去。走的是和登录表单同一条路（authApi.login + setAccessToken
     + setUser），不是绕过鉴权直接塞一个 user——演示站要演示的就是真实流程，
     这里抄近路的话，登录本身坏了都看不出来。 */
  const enter = async (account: string) => {
    if (pending) return
    setPending(account)
    try {
      const session = await authApi.login(account, DEMO_PASSWORD)
      setAccessToken(session.access_token)
      if (session.user) {
        setUser(session.user as unknown as User)
        say('已登录')
      }
    } catch (e) {
      say(e instanceof ApiError ? e.message : '登录失败，请手动输入试试')
    } finally {
      setPending('')
    }
  }
  const enterTour = async () => {
    if (pending) return
    setPending('tour')
    try {
      const session = await api.post<{ access_token: string; user?: User }>('/auth/demo-tour')
      setAccessToken(session.access_token)
      if (session.user) {
        setUser(session.user)
        say('已进入体验账号')
      }
    } catch (e) {
      say(e instanceof ApiError ? e.message : '进入体验账号失败')
    } finally {
      setPending('')
    }
  }
  const accounts = ACCOUNTS.map(([acct, name, role]) => ({
    acct, name, identity: userBySid(acct)?.isDeputy ? '小组 / 副班管' : role,
  }))

  return (
    <aside style={panel}>
      <ChromeHead label="DEMO" onHide={hide} />
      <div style={{ marginTop: 8, fontSize: 13, fontWeight: 600 }}>点一行直接登录 · 密码均为 demodemo12</div>
      <button
        type="button"
        disabled={!!pending}
        onClick={() => void enterTour()}
        title="进入周未注册体验账号，登录后可切学生 / 小组 / 副班管 / 班管"
        style={{
          display: 'flex',
          gap: 8,
          alignItems: 'baseline',
          width: '100%',
          margin: '12px 0 0',
          padding: '7px 6px',
          border: '1px solid var(--line)',
          background: pending === 'tour' ? 'var(--sub)' : 'transparent',
          color: 'var(--fg2)',
          font: 'inherit',
          fontSize: 12.5,
          lineHeight: 1.85,
          textAlign: 'left',
          fontVariantNumeric: 'tabular-nums',
          cursor: pending ? 'progress' : 'pointer',
        }}
      >
        <span style={{ flex: 1, minWidth: 0 }}>{TOUR_SID}</span>
        <span>周</span>
        <span style={{ color: 'var(--fg3)' }}>{pending === 'tour' ? '进入中…' : '体验账号'}</span>
      </button>
      <ul style={{ margin: '8px 0 0', padding: 0, listStyle: 'none' }}>
        {accounts.map(({ acct, name, identity }) => (
          <li key={acct}>
            <button
              type="button"
              disabled={!!pending}
              onClick={() => enter(acct)}
              title={`以 ${name}（${identity}）登录`}
              style={{
                display: 'flex',
                gap: 8,
                alignItems: 'baseline',
                width: '100%',
                margin: 0,
                padding: '5px 6px',
                border: 0,
                background: pending === acct ? 'var(--sub)' : 'transparent',
                color: 'var(--fg2)',
                font: 'inherit',
                fontSize: 12.5,
                lineHeight: 1.85,
                textAlign: 'left',
                fontVariantNumeric: 'tabular-nums',
                cursor: pending ? 'progress' : 'pointer',
                opacity: pending && pending !== acct ? 0.45 : 1,
              }}
              onMouseEnter={(e) => {
                if (!pending) e.currentTarget.style.background = 'var(--sub)'
              }}
              onMouseLeave={(e) => {
                if (pending !== acct) e.currentTarget.style.background = 'transparent'
              }}
            >
              <span style={{ flex: 1, minWidth: 0 }}>{acct}</span>
              <span>{name}</span>
              <span style={{ color: 'var(--fg3)' }}>{pending === acct ? '登录中…' : identity}</span>
            </button>
          </li>
        ))}
      </ul>
      <div style={{ marginTop: 12, fontSize: 12, color: 'var(--fg3)', lineHeight: 1.65 }}>
        体验账号不含运维。也可走注册：学号 {TOUR_SID}、姓名 周未注册，验证码 123456。该号退出即清空本页数据。<br />
        副班管从综测小组任命，可在班管「班级成员」中更换。
      </div>
      <a href="/source.tar.gz" download style={{fontSize:11,color:"var(--fg3)"}}>AGPL-3.0 · 演示站源码</a>
    </aside>
  )
}
