import { useState } from 'react'
import { Btn, Note, Pill } from '@/components/ui'
import { useAccountActions, useMailPreferences } from '@/api/queries'
import type { MailMode, MailPreferences, MailPreferenceState } from '@/api/types'
import { ApiError } from '@/api/client'
import { MAIL_CATEGORIES, mailPreset } from '@/lib/mailPreferences'
import { fieldStyle } from '@/lib/style'
import { useApp } from '@/stores/app'
import '@/styles/mail-preferences.css'

export default function EmailPreferences() {
  const state = useMailPreferences()
  if (state.isLoading) return <div className="load-bar" aria-label="正在读取邮件偏好"><span /></div>
  if (!state.data) return <div style={{ padding: '24px 0' }}><Note tone="warn">邮件偏好暂时无法读取。</Note><Btn onClick={() => void state.refetch()}>重新读取</Btn></div>
  return <PreferenceForm key={JSON.stringify(state.data.preferences)} state={state.data} />
}

function PreferenceForm({ state }: { state: MailPreferenceState }) {
  const [draft, setDraft] = useState<MailPreferences>(state.preferences)
  const [dirty, setDirty] = useState(false)
  const { updateMailPreferences } = useAccountActions()
  const say = useApp((s) => s.say)
  const role = useApp((s) => s.user?.role)
  const change = (patch: Partial<MailPreferences>) => { setDraft((old) => ({ ...old, ...patch })); setDirty(true) }
  const save = (value: MailPreferences) => updateMailPreferences.mutate(value, {
    onSuccess: () => { setDraft(value); setDirty(false); say(value.enabled ? '邮件偏好已保存' : '已关闭全部业务邮件') },
    onError: (e) => say(e instanceof ApiError ? e.message : '保存失败，请重试'),
  })
  const restricted = state.deliveryState !== 'ready' && state.deliveryState !== 'no_primary_email'
  return (
    <section className="mail-preferences" aria-labelledby="mail-preferences-title">
      <div className="mail-preferences-heading"><div><h2 id="mail-preferences-title">邮件提醒</h2><p>只收你需要的进展。邮件关闭后，业务记录仍可在平台查看。</p></div><Pill tone={dirty ? 'warn' : state.preferences.enabled ? 'ok' : 'idle'}>{dirty ? '更改待保存' : state.preferences.enabled ? '按偏好接收' : '业务邮件已关闭'}</Pill></div>
      {state.notificationsPaused && <Note>平台目前已暂停所有业务邮件。你可以先保存偏好，平台恢复后仅提醒新的进展，不补发旧通知。</Note>}
      {state.deliveryState === 'no_primary_email' && <Note>绑定并验证主邮箱后，邮件提醒才会生效。</Note>}
      {restricted && <Note tone="warn">当前主邮箱已有退订、拒收或无效地址记录，系统已停止相应邮件。保存偏好不会自动移除邮件服务商的记录；误操作时请联系平台管理员核实。</Note>}
      <div className="mail-presets" role="group" aria-label="邮件接收方案">
        {([['balanced', '轻量提醒（推荐）'], ['important', '仅重要提醒'], ['digest', '每日汇总'], ['frequent', '逐条提醒（高频）'], ['off', '全部关闭并保存']] as const).map(([key, label]) => <Btn key={key} disabled={updateMailPreferences.isPending} onClick={() => { const next = mailPreset(key, draft); if (key === 'off') save(next); else { setDraft(next); setDirty(true) } }}>{label}</Btn>)}
      </div>
      <label className="mail-enabled"><input type="checkbox" checked={draft.enabled} disabled={updateMailPreferences.isPending} onChange={(e) => change({ enabled: e.target.checked })} />接收业务邮件</label>
      <fieldset disabled={!draft.enabled || updateMailPreferences.isPending} className="mail-category-fields">
        <legend className="sr-only">按类别选择接收方式</legend>
        {MAIL_CATEGORIES.filter(({ key }) => key !== 'class_activity' || role === 'class_admin').map(({ key, label, detail }) => (
          <label className="mail-category" key={key}><span><strong>{label}</strong><span>{detail}</span></span><select aria-label={label} value={draft.categories[key]} onChange={(e) => change({ categories: { ...draft.categories, [key]: e.target.value as MailMode } })} style={fieldStyle}><option value="off">不发邮件</option><option value="digest">每日汇总</option><option value="immediate">及时提醒（合并）</option><option value="frequent">逐条提醒（高频）</option></select></label>
        ))}
        <div className="mail-timing">
          <label><span>汇总时间 · 北京时间</span><input type="time" required value={draft.digestTime} onChange={(e) => change({ digestTime: e.target.value })} style={fieldStyle} /></label>
          <label><span>免打扰开始</span><input type="time" required value={draft.quietStart} onChange={(e) => change({ quietStart: e.target.value })} style={fieldStyle} /></label>
          <label><span>免打扰结束</span><input type="time" required value={draft.quietEnd} onChange={(e) => change({ quietEnd: e.target.value })} style={fieldStyle} /></label>
          <label><span>每日业务邮件上限 · 1—50 封</span><input type="number" min={1} max={50} required value={draft.dailyLimit} onChange={(e) => change({ dailyLimit: Number(e.target.value) })} style={fieldStyle} /></label>
        </div>
      </fieldset>
      {draft.enabled && Object.values(draft.categories).includes('frequent') && <Note tone="warn">你选择了高频提醒：所选类别的每项有效更新会单独发信，邮件可能明显增多。仍遵守免打扰、每日上限与平台保护（至少间隔 1 分钟、每小时最多 8 封）。点击保存后生效，可随时改回轻量提醒。</Note>}
      <p className="mail-preference-help">默认轻量提醒：重要消息合并约 10 分钟，普通结果和待办每日汇总，审核进度和操作回执关闭，每天最多 3 封。每日汇总没有新内容就不发。</p>
      <p className="mail-preference-help">达到上限或处于免打扰时段会顺延，已处理、过期或被更新替代的提醒会跳过。提高频率不补发此前关闭或暂停期间的消息。免打扰起止时间相同表示关闭个人免打扰。</p>
      <p className="mail-preference-help">验证码和找回密码仅在你主动申请时发送，不受以上开关影响。截止日期仍以平台为准。</p>
      <div className="mail-preference-actions"><Btn primary disabled={!dirty || updateMailPreferences.isPending} onClick={() => save(draft)}>{updateMailPreferences.isPending ? '保存中…' : '保存邮件偏好'}</Btn>{dirty && <span>有未保存的更改</span>}</div>
    </section>
  )
}
