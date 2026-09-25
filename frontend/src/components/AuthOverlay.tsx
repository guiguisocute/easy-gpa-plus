/* 登录 / 注册三步 / 找回密码 / 重置密码。左右分栏，结构对齐原型 signedOut 区块。

   注册拆成三步：
     reg   核对（学号, 姓名）是否命中白名单——此时还没有账号
     pwd   设密码，账号在这一步建出来并直接进入登录态
     email 推荐绑定邮箱，可跳过；跳过的账号照常可用，只是收不到通知、也不能自助找回密码

   第三步已经是登录态（access token 在手），所以它调的是 /me/emails 那套现成接口。
   为了让这一步还留在浮层里，第二步成功后只 setAccessToken 而不 setUser——
   一旦 setUser，App 就会切去渲染 Shell，浮层直接卸载。

   身份不由用户选：白名单里的 role 决定初始身份。前端只做"填了没、格式对不对"这一层，
   真正的白名单判定必须在后端，否则任何人改一下前端就能自称管理员。

   左栏不写具体班名：一套部署供多个班级共用，登录前根本不知道来访者属于哪个租户。 */

import { useEffect, useState } from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { z } from 'zod'
import { ThemeToggle } from './ui'
import AuthLeftPanel from './AuthLeftPanel'
import { useMediaQuery } from '@/lib/useMediaQuery'
import { fieldStyle } from '../lib/style'
import { useApp } from '@/stores/app'
import type { User } from '@/lib/types'
import { authApi } from '@/api/queries'
import { ApiError, setAccessToken } from '@/api/client'

type AuthView = 'login' | 'reg' | 'pwd' | 'email' | 'code' | 'forgot' | 'reset'

const VIEW_META: Record<AuthView, { en: string; title: string; desc: string; cta: string; altLabel: string }> = {
  login: {
    en: 'SIGN IN',
    title: '登录',
    desc: '使用学号或已绑定邮箱登录。',
    cta: '登录',
    altLabel: '首次使用 · 注册',
  },
  reg: {
    en: 'REGISTER · 1 / 3',
    title: '核对身份',
    desc: '请填写学号与真实姓名，须与班级名单一致。',
    cta: '下一步',
    altLabel: '已有账号 · 登录',
  },
  pwd: {
    en: 'REGISTER · 2 / 3',
    title: '设置密码',
    desc: '设置至少 10 位的登录密码。',
    cta: '创建账号',
    altLabel: '返回上一步',
  },
  email: {
    en: 'REGISTER · 3 / 3',
    title: '绑定邮箱（推荐）',
    desc: '用来收提醒、找回密码。也可以以后再绑。',
    cta: '发送验证码',
    altLabel: '暂不绑定 · 直接进入',
  },
  code: {
    en: 'REGISTER · 3 / 3',
    title: '验证邮箱',
    desc: '',
    cta: '完成绑定',
    altLabel: '暂不绑定 · 直接进入',
  },
  forgot: {
    en: 'RESET PASSWORD',
    title: '找回密码',
    desc: '请填写学号或已绑定邮箱，系统将发送密码重置邮件。',
    cta: '发送重置邮件',
    altLabel: '返回登录',
  },
  reset: {
    en: 'SET NEW PASSWORD',
    title: '设置新密码',
    desc: '重置链接 30 分钟内有效。改完之后，其他设备上的登录都会失效。',
    cta: '设置并登录',
    altLabel: '返回登录',
  },
}

const acctSchema = z.string().trim().min(1, '请填写学号或邮箱')
const sidSchema = z.string().trim().min(1, '请填写学号')
const emailSchema = z.email('邮箱格式不正确')
/* 后端 auth.ValidatePassword 的下限是 10 位，这里对齐；长度比"必须含大小写数字符号"更有效，
   也不会逼用户把复杂度写在便签上。 */
const pwSchema = z.string().min(10, '密码至少 10 位')
const nameSchema = z.string().trim().min(1, '请填写姓名')
const codeSchema = z.string().trim().regex(/^\d{6}$/, '验证码为 6 位数字')

export default function AuthOverlay() {
  const setUser = useApp((s) => s.setUser)
  const say = useApp((s) => s.say)

  /* 断点取 860：再窄下去左右两栏各自不到 400px，表单的字段标签就开始折行了。 */
  const narrow = useMediaQuery('(max-width: 860px)')
  const [step, setStep] = useState<'brand' | 'form'>('brand')

  const [view, setView] = useState<AuthView>('login')
  const [acct, setAcct] = useState('')
  const [sid, setSid] = useState('')
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [pw, setPw] = useState('')
  const [code, setCode] = useState('')
  const [challenge, setChallenge] = useState('')
  const [ticket, setTicket] = useState('')
  /* 第二步拿到的 user 先存着：第三步结束或跳过时才 setUser，浮层才能活到第三步。 */
  const [pendingUser, setPendingUser] = useState<User | null>(null)
  const [resetToken, setResetToken] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  /* 密码明文开关。换一屏（登录 → 注册 → 重置）就收回去，不把上一屏的明文带过去。 */
  const [pwShown, setPwShown] = useState(false)

  /* 重置链接形如 /?reset_token=…；读完就从地址栏抹掉，
     免得令牌跟着分享链接或浏览器历史外泄。 */
  useEffect(() => {
    const url = new URL(location.href)
    const token = url.searchParams.get('reset_token')
    if (!token) return
    setResetToken(token)
    setView('reset')
    /* 带令牌进来的是"来改密码"，窄屏下别让他先看一屏品牌再自己点进去。 */
    setStep('form')
    url.searchParams.delete('reset_token')
    history.replaceState(null, '', url)
  }, [])

  const meta = VIEW_META[view]
  const goto = (v: AuthView) => {
    setView(v)
    setErr('')
    setPwShown(false)
  }

  const signIn = (session: { access_token: string; user?: { role: string } }) => {
    setAccessToken(session.access_token)
    if (session.user) {
      setUser(session.user as never)
      say('已登录')
    }
  }

  /** 注册收尾：把第二步暂存的 user 交给 store，浮层随之卸载。 */
  const enterApp = (msg: string) => {
    if (!pendingUser) return
    setUser(pendingUser)
    say(msg)
  }

  const fail = (e: unknown) => {
    setErr(e instanceof ApiError ? e.message : '网络异常，请稍后再试')
  }

  async function submit() {
    if (busy) return
    setErr('')

    const check = (...results: { success: boolean; error?: { issues: { message: string }[] } }[]) => {
      for (const r of results) {
        if (!r.success) {
          setErr(r.error!.issues[0].message)
          return false
        }
      }
      return true
    }

    setBusy(true)
    try {
      if (view === 'login') {
        if (!check(acctSchema.safeParse(acct), pwSchema.safeParse(pw))) return
        signIn(await authApi.login(acct.trim(), pw))
        return
      }

      /* 第一步只核对身份。后端此时不建号、不发信，失败也只回一句笼统的"未命中白名单"，
         不区分"查无此人"和"已被注册"之外的细节，免得拿来枚举名单。 */
      if (view === 'reg') {
        if (!check(sidSchema.safeParse(sid), nameSchema.safeParse(name))) return
        const t = await authApi.registerCheck({
          sid: sid.trim(),
          name: name.trim(),
        })
        setTicket(t.ticket)
        goto('pwd')
        say(`身份已核对 · ${Math.round(t.expires_in / 60)} 分钟内完成设置`)
        return
      }

      /* 第二步建号。成功后只放 access token，不 setUser——留在浮层里走第三步。 */
      if (view === 'pwd') {
        if (!check(pwSchema.safeParse(pw))) return
        const session = await authApi.registerComplete(ticket, pw)
        setAccessToken(session.access_token)
        setPw('')
        if (!session.user) {
          /* 理论上不会发生；真发生了也别把用户卡在浮层里，退回登录重来。 */
          goto('login')
          say('账号已创建，请登录')
          return
        }
        setPendingUser(session.user)
        goto('email')
        return
      }

      if (view === 'email') {
        if (!check(emailSchema.safeParse(email))) return
        const ch = await authApi.bindEmail(email.trim())
        setChallenge(ch.challengeId)
        goto('code')
        say(`验证码已发往 ${email.trim()} · ${Math.round(ch.expiresIn / 60)} 分钟内有效`)
        return
      }

      if (view === 'code') {
        if (!check(codeSchema.safeParse(code))) return
        await authApi.bindEmailVerify(challenge, code.trim())
        enterApp('邮箱已绑定')
        return
      }

      if (view === 'forgot') {
        if (!check(acctSchema.safeParse(acct))) return
        await authApi.forgotPassword(acct.trim())
        goto('login')
        say('若该账号存在，密码重置邮件已发出。')
        return
      }

      if (view === 'reset') {
        if (!check(pwSchema.safeParse(pw))) return
        await authApi.resetPassword(resetToken, pw)
        setPw('')
        goto('login')
        say('密码已重置，请用新密码登录')
      }
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }

  function alt() {
    if (view === 'login') return goto('reg')
    if (view === 'pwd') return goto('reg')
    /* 第三步的次要按钮就是"跳过"：账号在第二步已经建好，直接进应用即可。 */
    if (view === 'email' || view === 'code') return enterApp('已登录 · 邮箱什么时候在账号设置里绑都行')
    goto('login')
  }

  const field = (
    label: string,
    value: string,
    onChange: (v: string) => void,
    extra: React.InputHTMLAttributes<HTMLInputElement> = {},
  ) => (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
      <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>{label}</span>
      {/* 密码框右边挂一只眼睛。手机上打十位以上的密码打错一个字符只能全删重来，
          而这是登录页——用户还没进系统，没有任何别的办法确认自己打对了没有。 */}
      {extra.type === 'password' ? (
        <span style={{ position: 'relative', display: 'flex', alignItems: 'center' }}>
          <input
            className="hv-bfg"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            {...extra}
            type={pwShown ? 'text' : 'password'}
            style={{ ...fieldStyle, fontSize: 16, paddingRight: 48, ...(extra.style ?? {}) }}
          />
          <button
            type="button"
            className="hv-fg"
            onClick={() => setPwShown((v) => !v)}
            title={pwShown ? '隐藏密码' : '显示密码'}
            aria-label={pwShown ? '隐藏密码' : '显示密码'}
            aria-pressed={pwShown}
            data-ui="password-toggle"
            style={{ position: 'absolute', right: 0, bottom: 8, display: 'flex', alignItems: 'center', background: 'none', border: 0, margin: 0, padding: 4, color: 'var(--fg3)', cursor: 'pointer' }}
          >
            {pwShown ? <EyeOff size={16} strokeWidth={1.7} /> : <Eye size={16} strokeWidth={1.7} />}
          </button>
        </span>
      ) : (
        <input
          className="hv-bfg"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          {...extra}
          style={{ ...fieldStyle, fontSize: 16, ...(extra.style ?? {}) }}
        />
      )}
    </label>
  )

  const pad = narrow ? 24 : 56
  const formPane = (
    <div
      className={narrow ? 'auth-form-pane auth-form-mobile' : 'auth-form-pane'}
      style={{
        padding: `52px ${pad}px`,
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        position: 'relative',
        ...(narrow ? { minHeight: '100dvh' } : null),
      }}
    >
      {/* 绝对定位而不是塞进 flex 流:本栏靠 justifyContent 居中表单，多一个兄弟节点就会把表单顶偏。
          偏移量取自本栏 padding，于是它和左栏顶部的 BrandMark 落在同一条水平线上。 */}
      <div className="auth-theme" style={{ position: 'absolute', top: 52, right: pad }}>
        <ThemeToggle />
      </div>
      {narrow && (
        <button
          type="button"
          className="hv-fg"
          data-ui="auth-back"
          onClick={() => setStep('brand')}
          style={{ position: 'absolute', top: 52, left: pad, height: 32, display: 'flex', alignItems: 'center', background: 'none', border: 0, margin: 0, padding: 0, font: 'inherit', fontSize: 12.5, color: 'var(--fg2)', cursor: 'pointer' }}
        >
          ← 返回
        </button>
      )}
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
          style={{ maxWidth: 380, width: '100%', margin: '0 auto', display: 'flex', flexDirection: 'column' }}
        >
          <div style={{ font: "500 10.5px/1 'JetBrains Mono',monospace", letterSpacing: '.14em', color: 'var(--fg3)' }}>{meta.en}</div>
          <div style={{ fontSize: 26, fontWeight: 600, letterSpacing: '-.035em', marginTop: 10 }}>{meta.title}</div>
          {meta.desc ? <div style={{ fontSize: 12.5, color: 'var(--fg3)', marginTop: 9, lineHeight: 1.7, textWrap: 'pretty' }}>{meta.desc}</div> : null}

          <div style={{ display: 'flex', flexDirection: 'column', gap: 22, marginTop: 30 }}>
            {(view === 'login' || view === 'forgot') &&
              field('学号 或 邮箱', acct, setAcct, { placeholder: '请输入学号或邮箱', autoComplete: 'username' })}

            {view === 'reg' && (
              <>
                {field('学号', sid, setSid, { placeholder: '请输入本人学号', autoComplete: 'username', inputMode: 'numeric' })}
                {field('姓名', name, setName, { placeholder: '请输入真实姓名' })}
              </>
            )}

            {view === 'email' &&
              field('邮箱', email, setEmail, { placeholder: 'you@example.edu', type: 'email', autoComplete: 'email' })}

            {view === 'code' &&
              field('邮箱验证码 · 10 分钟内有效', code, setCode, {
                placeholder: '6 位数字',
                inputMode: 'numeric',
                autoComplete: 'one-time-code',
                style: { letterSpacing: '.3em' },
              })}

            {(view === 'login' || view === 'pwd' || view === 'reset') &&
              field(view === 'login' ? '密码' : '密码（至少 10 位）', pw, setPw, {
                type: 'password',
                placeholder: '••••••••',
                autoComplete: view === 'login' ? 'current-password' : 'new-password',
              })}
          </div>

          {err && (
            <div role="alert" style={{ display: 'flex', alignItems: 'flex-start', gap: 9, marginTop: 20 }}>
              <span style={{ width: 5, height: 5, background: 'var(--red)', flex: 'none', marginTop: 6 }} />
              <span style={{ fontSize: 12.5, color: 'var(--red)', lineHeight: 1.6 }}>{err}</span>
            </div>
          )}

          <button
            type="submit"
            className="hv-op82"
            disabled={busy}
            style={{
              margin: '26px 0 0',
              padding: '12px 18px',
              borderRadius: 999,
              border: '1px solid var(--fg)',
              background: 'var(--fg)',
              color: 'var(--bg)',
              font: 'inherit',
              fontSize: 13,
              fontWeight: 600,
              cursor: busy ? 'progress' : 'pointer',
              opacity: busy ? 0.6 : 1,
              transition: 'opacity .16s',
            }}
          >
            {busy ? '处理中…' : meta.cta}
          </button>

          {/* 注册入口仍是左下角那个文字链，只是登录页上描红加粗——
              全班每人一学年只点它一次，找不到就只能来问班长。 */}
          <div style={{ display: 'flex', gap: 18, marginTop: 18 }}>
            <button
              type="button"
              className={view === 'login' ? 'hv-op82' : 'hv-red'}
              onClick={alt}
              style={{ background: 'none', border: 0, margin: 0, padding: 0, font: 'inherit', fontSize: 12.5, fontWeight: view === 'login' ? 600 : 400, color: view === 'login' ? 'var(--red)' : 'var(--fg2)', cursor: 'pointer' }}
            >
              {meta.altLabel}
            </button>
            {view === 'login' && (
              <button type="button" className="hv-fg" onClick={() => goto('forgot')} style={{ background: 'none', border: 0, margin: '0 0 0 auto', padding: 0, font: 'inherit', fontSize: 12.5, color: 'var(--fg3)', cursor: 'pointer' }}>
                忘记密码
              </button>
            )}
          </div>
      </form>
    </div>
  )

  /* 窄屏拆成两屏：品牌屏 → 表单屏。并排的两栏在手机上只有 200px 出头，
     两边都会被挤成竖排单字，那不是"能用但难看"，是根本没法填。 */
  if (narrow) {
    return step === 'brand' ? <AuthLeftPanel compact onEnter={() => setStep('form')} /> : formPane
  }

  return (
    <div style={{ minHeight: '100vh', display: 'grid', gridTemplateColumns: 'minmax(0,1.05fr) minmax(0,.95fr)' }}>
      <AuthLeftPanel />
      {formPane}
    </div>
  )
}
