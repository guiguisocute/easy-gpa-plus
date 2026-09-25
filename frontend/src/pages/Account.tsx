/* 账号设置。学生、综测小组、班级管理员共用一页——三种人要改的都是同一对东西，
   没必要按角色各做一份。三边都从左下角账号菜单进来（见 lib/nav.ts 的 ACCOUNT_ENTRY）。
   运维不在其列：/me/* 对 ops 是 404。

   学号与姓名来自白名单，这里改不了；可修改收件邮箱、邮件偏好与密码。

   多邮箱的语义要写清楚（§3.1 user_email）：主邮箱按偏好接收通知，备用邮箱只用于找回密码。
   否则学生会以为加一个备用邮箱就能多收一份提醒。

   加邮箱走的是验证码而不是验证链接（后端 /me/emails → /me/emails/verify），
   所以这里是"填地址 → 收码 → 填码"两步，不是发一封链接就完事。 */

import { useState } from 'react'
import { z } from 'zod'
import { Btn, PageHead, Pill, RequiredMark, Split, SplitCol, TextBtn } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { authApi, useAccountActions, useMyEmails } from '@/api/queries'
import { ROLE_LABEL, type Role } from '@/lib/types'
import EmailPreferences from '@/components/EmailPreferences'
import AgentConnections from '@/components/AgentConnections'
import { useLocalAgentPreference } from '@/stores/agentPreference'

/* 身份是怎么来的，三种人不一样：学生名单里就有，另外两种是被人任命的。
   一律写「由班级名单认定」会让小组成员以为改一改名单就能改身份。
   ops 那条只是把表填全，它进不来这一页（导航里没有，/me/* 对它也是 404）。 */
const ROLE_ORIGIN: Record<Role, string> = {
  student: '由班级名单认定',
  group: '由班级管理员任命',
  class_admin: '由运维任命',
  ops: '由平台配置',
}

const emailSchema = z.email('邮箱格式不正确')
/* 与后端 auth.ValidatePassword 保持一致：只卡长度，不强制大小写与符号——
   强制复杂度会把用户推向"Password1!"这类可预测口令，长度才是真正有效的那一维。 */
const pwSchema = z.string().min(10, '至少 10 位')

export default function Account() {
  const say = useApp((s) => s.say)
  const user = useApp((s) => s.user)
  const signOut = useApp((s) => s.signOut)

  const localAgent = useLocalAgentPreference()
  const emails = useMyEmails()
  const a = useAccountActions()

  const [newMail, setNewMail] = useState('')
  const [challengeId, setChallengeId] = useState('')
  const [code, setCode] = useState('')
  const [cur, setCur] = useState('')
  const [next, setNext] = useState('')
  const [again, setAgain] = useState('')

  /* 一个已验证主邮箱都没有时，这次添加就是在绑主邮箱：后端会把它直接置为
     主邮箱，界面上的措辞也要跟着改，不能再叫「备用」。 */
  const hasPrimary = (emails.data?.items ?? []).some((m) => m.primary)
  const bindingPrimary = !emails.isLoading && !hasPrimary

  const mailOk = emailSchema.safeParse(newMail.trim()).success
  const pwOk = pwSchema.safeParse(next).success && next === again && cur.length > 0

  const facts = [
    { k: '学号', v: user?.sid ?? '—' },
    { k: '姓名', v: user?.name ?? '—' },
    { k: '班级', v: user?.className ?? '—' },
    { k: '身份', v: user ? `${ROLE_LABEL[user.role]} · ${ROLE_ORIGIN[user.role]}` : '—' },
  ]

  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead en="ACCOUNT" title="账号设置" />

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '10px 30px', borderTop: '1px solid var(--line)', borderBottom: '1px solid var(--line)', padding: '13px 0' }}>
        {facts.map((x) => (
          <span key={x.k} style={{ display: 'flex', alignItems: 'baseline', gap: 9, fontSize: 12.5, minWidth: 0 }}>
            <span style={{ color: 'var(--fg3)', whiteSpace: 'nowrap' }}>{x.k}</span>
            <span style={{ color: 'var(--fg2)', fontWeight: 500 }}>{x.v}</span>
          </span>
        ))}
      </div>

      <section style={{ padding: '24px 0', borderBottom: '1px solid var(--line)' }} aria-label="Agent 偏好">
        <label style={{ display: 'flex', alignItems: 'center', gap: 12, cursor: 'pointer', fontWeight: 600, fontSize: 14 }}>
          <input type="checkbox" checked={localAgent.enabled} onChange={(event) => localAgent.setEnabled(event.target.checked)} />
          启用网页「问 Agent」
        </label>
        <p style={{ margin: '10px 0 0', fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8 }}>关闭后隐藏按钮并关闭浮窗。此选择按账号保存在当前浏览器，刷新或重新登录后仍然有效；可随时在这里开启。</p>
      </section>
      <AgentConnections />
      <EmailPreferences />
      <Split cols="1.15fr 1fr">
        <SplitCol first>
          <div style={{ padding: '26px 0' }}>
            <div style={{ paddingBottom: 16 }}>
              <span style={{ fontSize: 15, fontWeight: 600, letterSpacing: '-.025em' }}>邮箱</span>
            </div>

            {emails.isLoading ? (
              <div className="load-bar"><span /></div>
            ) : (
              (emails.data?.items ?? []).map((m) => (
                <div key={m.id} style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', borderTop: '1px solid var(--line2)', padding: '14px 0' }}>
                  <span style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 0, flex: 1 }}>
                    <span style={{ display: 'flex', alignItems: 'center', gap: 9, flexWrap: 'wrap' }}>
                      <span style={{ fontSize: 13, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.email}</span>
                      <Pill tone={m.primary ? 'ok' : m.verified ? 'idle' : 'warn'}>
                        {m.primary ? '主邮箱 · 按偏好收信' : m.verified ? '备用 · 仅找回密码' : '待验证'}
                      </Pill>
                    </span>
                    <span style={mono('11px', '0')}>添加于 {f.date(m.createdAt)}</span>
                  </span>
                  <span style={{ display: 'flex', gap: 8, flex: 'none' }}>
                    {!m.primary && m.verified && (
                      <Btn onClick={() => a.setPrimary.mutate(m.id, { onSuccess: () => say('主邮箱已切换'), onError: fail })}>设为主邮箱</Btn>
                    )}
                    {!m.primary && (
                      <Btn danger onClick={() => a.removeEmail.mutate(m.id, { onSuccess: () => say('已移除'), onError: fail })}>
                        移除
                      </Btn>
                    )}
                  </span>
                </div>
              ))
            )}

            {challengeId ? (
              <div style={{ border: '1px solid var(--line)', padding: '16px', display: 'flex', flexDirection: 'column', gap: 11, marginTop: 18 }}>
                <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>验证码已发往 {newMail || '新邮箱'} · 10 分钟内有效</span>
                <div style={{ display: 'flex', alignItems: 'flex-end', gap: 12, flexWrap: 'wrap' }}>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: 7, flex: 1, minWidth: 180 }}>
                    <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>6 位验证码</span>
                    <input
                      className="hv-bfg"
                      value={code}
                      onChange={(e) => setCode(e.target.value)}
                      inputMode="numeric"
                      placeholder="000000"
                      style={{ ...fieldStyle, letterSpacing: '.3em' }}
                    />
                  </label>
                  <Btn
                    primary
                    disabled={!/^\d{6}$/.test(code.trim()) || a.verifyEmail.isPending}
                    onClick={() =>
                      a.verifyEmail.mutate(
                        { challengeId, code: code.trim() },
                        {
                          onSuccess: (added) => {
                            setChallengeId('')
                            setCode('')
                            setNewMail('')
                            say(added.primary ? '主邮箱已绑定 · 按邮件偏好接收提醒' : '备用邮箱已添加')
                          },
                          onError: fail,
                        },
                      )
                    }
                  >
                    确认添加
                  </Btn>
                  <Btn onClick={() => { setChallengeId(''); setCode('') }}>取消</Btn>
                </div>
              </div>
            ) : (
              <div style={{ display: 'flex', alignItems: 'flex-end', gap: 12, flexWrap: 'wrap', borderTop: '1px solid var(--line)', paddingTop: 18 }}>
                <label style={{ display: 'flex', flexDirection: 'column', gap: 7, flex: 1, minWidth: 220 }}>
                  <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>
                    {bindingPrimary ? '绑定主邮箱' : '添加备用邮箱'}
                    {bindingPrimary && <RequiredMark />}
                  </span>
                  <input className="hv-bfg" value={newMail} onChange={(e) => setNewMail(e.target.value)} placeholder="name@example.com" style={fieldStyle} />
                </label>
                <Btn
                  primary
                  disabled={!mailOk || a.addEmail.isPending}
                  onClick={() =>
                    a.addEmail.mutate(newMail.trim(), {
                      onSuccess: (r) => {
                        setChallengeId(r.challengeId)
                        say('验证码已发送')
                      },
                      onError: fail,
                    })
                  }
                >
                  发验证码
                </Btn>
              </div>
            )}
            <span style={{ display: 'block', fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, paddingTop: 10, textWrap: 'pretty' }}>
              {bindingPrimary
                ? '验证通过之后它直接变成主邮箱，不用再点一次「设为主邮箱」。'
                : '没验证的邮箱收不到任何系统邮件，也不能设成主邮箱。'}
            </span>
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '26px 0' }}>
            <div style={{ paddingBottom: 18 }}>
              <span style={{ fontSize: 15, fontWeight: 600, letterSpacing: '-.025em' }}>修改密码</span>
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: 18, maxWidth: 380 }}>
              {[
                { label: '当前密码', value: cur, set: setCur, ph: '••••••••••', err: '' },
                {
                  label: '新密码',
                  value: next,
                  set: setNext,
                  ph: '至少 10 位',
                  err: next && !pwSchema.safeParse(next).success ? '至少 10 位' : '',
                },
                {
                  label: '再输一次',
                  value: again,
                  set: setAgain,
                  ph: '重复新密码',
                  err: again && again !== next ? '两次输入不一致' : '',
                },
              ].map((p) => (
                <label key={p.label} style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
                  <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>{p.label}</span>
                  <input
                    className="hv-bfg"
                    type="password"
                    autoComplete={p.label === '当前密码' ? 'current-password' : 'new-password'}
                    value={p.value}
                    onChange={(e) => p.set(e.target.value)}
                    placeholder={p.ph}
                    style={{ ...fieldStyle, letterSpacing: '.14em' }}
                  />
                  {p.err && <span style={{ fontSize: 12, color: 'var(--red)', lineHeight: 1.6 }}>{p.err}</span>}
                </label>
              ))}
              <div>
                <Btn
                  primary
                  disabled={!pwOk || a.changePassword.isPending}
                  onClick={() =>
                    a.changePassword.mutate(
                      { currentPassword: cur, newPassword: next },
                      {
                        onSuccess: () => {
                          setCur('')
                          setNext('')
                          setAgain('')
                          say('密码改好了 · 其他设备都已经被踢下线')
                        },
                        onError: fail,
                      },
                    )
                  }
                >
                  {a.changePassword.isPending ? '保存中…' : '保存新密码'}
                </Btn>
              </div>
            </div>

            <div style={{ marginTop: 18 }}>
              <TextBtn
                onClick={() => {
                  void authApi.logout().finally(() => {
                    signOut()
                    say('已退出登录')
                  })
                }}
              >
                退出登录
              </TextBtn>
            </div>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}
