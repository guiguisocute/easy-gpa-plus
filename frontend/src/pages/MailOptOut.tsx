import { userErrorMessage } from '@/api/errorMessages'
import { useState } from 'react'
import { Btn } from '@/components/ui'
import { MAIL_CATEGORIES } from '@/lib/mailPreferences'
import { fieldStyle } from '@/lib/style'

export default function MailOptOut() {
  // The capability is in the URL fragment: it never appears in server access
  // logs or Referer headers. Opening a link or preview never changes consent.
  const [token] = useState(() => window.location.hash.slice(1))
  const [category, setCategory] = useState('all')
  const [pending, setPending] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState('')
  const valid = /^[A-Za-z0-9_-]{43}$/.test(token)
  const submit = async () => {
    setPending(true); setError('')
    try {
      const res = await fetch('/api/v1/mail/opt-out', { method: 'POST', credentials: 'omit', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token, category }) })
      if (!res.ok) throw new Error(res.status === 404 ? '退订链接无效。请从原邮件重新打开，或登录账号设置关闭邮件。' : '暂时无法保存退订，请稍后重试。')
      setDone(true)
      window.history.replaceState(null, '', '/?mail_opt_out=1')
    } catch (e) { setError(userErrorMessage(e, '连接失败，请稍后重试。')) }
    finally { setPending(false) }
  }
  return (
    <main style={{ maxWidth: 520, margin: '8vh auto', padding: 24 }}>
      <a href="/" style={{ color: 'var(--fg2)', textDecoration: 'none', fontSize: 18, fontWeight: 600 }}>EasyGPA Plus</a>
      <h1 style={{ fontSize: 26, marginTop: 36 }}>{done ? '已保存退订' : '邮件接收设置'}</h1>
      {done ? <p role="status" style={{ lineHeight: 1.9 }}>{category === 'all' ? '你已关闭全部业务邮件。' : '你已关闭所选类别的邮件。'}退订持续有效，业务记录仍可在平台查看。你主动申请的验证码和找回密码不受影响。</p> : <>
        <p style={{ color: 'var(--fg3)', lineHeight: 1.9 }}>无需登录。选择要关闭的邮件并确认，之后系统会跳过相应提醒。</p>
        {valid ? <><label style={{ display: 'grid', gap: 10, margin: '24px 0' }}><span>退订范围</span><select value={category} onChange={(e) => setCategory(e.target.value)} style={fieldStyle}><option value="all">全部业务邮件</option>{MAIL_CATEGORIES.map(({ key, label }) => <option key={key} value={key}>{label}</option>)}</select></label><Btn primary disabled={pending} onClick={() => void submit()}>{pending ? '正在保存…' : '确认退订'}</Btn></> : <p role="alert">退订链接不完整。请从原邮件重新打开，或登录账号设置关闭邮件。</p>}
        {error && <p role="alert" style={{ color: 'var(--red)', lineHeight: 1.8 }}>{error}</p>}
      </>}
      <p style={{ marginTop: 30, fontSize: 13 }}><a href="/?v=stuAccount" style={{ color: 'var(--fg2)' }}>登录后调整分类、频率与免打扰时间</a></p>
    </main>
  )
}
