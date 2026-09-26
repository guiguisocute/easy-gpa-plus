import { useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Sub, Table, THead, TRow } from '@/components/ui'
import { OpsActions, OpsField, OpsLoadError, OpsSection, OpsTabPanel, OpsTabs } from '@/components/OpsLayout'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useMailConfig, useMailLog, useOpsActions } from '@/api/queries'
import { mailChannelUpdate } from '@/lib/mailChannel'

const PROVIDERS = [
  { id: 'smtp', name: 'SMTP', desc: '连接已有邮箱或邮件服务' },
  { id: 'tencent_ses', name: '腾讯云 SES', desc: '云模板投递与送达反馈' },
  { id: 'aliyun_dm', name: '阿里云邮件推送', desc: 'DirectMail API 投递' },
  { id: 'resend', name: 'Resend', desc: 'API Key 连接邮件服务' },
] as const
const providerName = (value: string) => PROVIDERS.find((provider) => provider.id === value)?.name ?? value
const TABS = [{ id: 'channel', label: '发信通道' }, { id: 'policy', label: '通知策略' }, { id: 'templates', label: '邮件模板' }, { id: 'logs', label: '投递日志' }]
const TEMPLATES = [
  ['verification_code', '邮箱验证码', '绑定邮箱时核验收件地址'],
  ['password_reset', '找回密码', '发送密码重置入口'],
  ['mail_test', '通道自检', '检查保存的通道能否正常投递'],
  ['notification_alert', '进展提醒', '合并或逐条提醒新的业务进展'],
  ['notification_digest', '每日汇总', '按用户偏好汇总当天进展'],
] as const
const STATUS_META: Record<string, { label: string; tone: 'ok' | 'warn' | 'bad' | 'idle' }> = {
  sent: { label: '已受理', tone: 'ok' }, delivered: { label: '已投递', tone: 'ok' }, queued: { label: '结果待确认', tone: 'warn' },
  retrying: { label: '重试中', tone: 'warn' }, failed: { label: '失败', tone: 'bad' }, suppressed: { label: '发送受限', tone: 'warn' },
}
const FEEDBACK_LABEL: Record<string, string> = { delivered: '已送达', complaint: '已举报 · 停止业务邮件', unsubscribed: '已退订', rejected: '收件方拒收', hard_bounce: '无效地址 · 停止发送', invalid_address: '无效地址', dropped: '已丢弃', deferred: '延迟投递', accepted: '待送达', not_found: '暂无反馈', lookup_failed: '反馈查询失败' }

export default function OpsMail() {
  const say = useApp((state) => state.say)
  const config = useMailConfig()
  const { updateMail, testMail, clearMailChannel } = useOpsActions()
  const [tab, setTab] = useState('channel')
  const [channel, setChannel] = useState<Record<string, string>>({})
  const [clearSecrets, setClearSecrets] = useState<string[]>([])
  const [templates, setTemplates] = useState<Record<string, string>>({})
  const [policy, setPolicy] = useState<Record<string, string>>({})
  const [approved, setApproved] = useState(false)
  const [to, setTo] = useState('')
  if (config.isLoading) return <div className="load-bar"><span /></div>
  if (!config.data) return <OpsLoadError title="邮件设置暂时无法读取" onRetry={() => void config.refetch()} />
  const state = config.data
  const c = state.config
  const provider = channel.provider ?? c.provider ?? state.activeProvider
  const busy = updateMail.isPending || clearMailChannel.isPending
  const dirty = Object.keys(channel).length > 0 || clearSecrets.length > 0
  const templatesDirty = Object.keys(templates).length > 0
  const encrypted = state.secretKeyReady !== false
  const fromDatabase = state.secretSource === 'database'
  const field = (key: string, fallback = '') => channel[key] ?? String((c as Record<string, unknown>)[key] ?? fallback)
  const change = (key: string, value: string) => setChannel((current) => ({ ...current, [key]: value }))
  const fail = (error: unknown) => say(error instanceof ApiError ? error.message : '操作失败，请重试')
  const resetChannel = () => { setChannel({}); setClearSecrets([]) }
  const secretFields = provider === 'smtp' ? [
    { key: 'smtpUsername', label: 'SMTP 用户名', saved: !!state.smtpUsernameSet },
    { key: 'smtpPassword', label: 'SMTP 密码或授权码', saved: !!state.smtpPasswordSet },
  ] : provider === 'aliyun_dm' ? [
    { key: 'aliyunAccessKeyId', label: 'AccessKey ID', saved: !!state.aliyunAccessKeyIdSet },
    { key: 'aliyunAccessKeySecret', label: 'AccessKey Secret', saved: !!state.aliyunAccessKeySecretSet },
  ] : provider === 'resend' ? [
    { key: 'resendApiKey', label: 'Resend API Key', saved: !!state.resendApiKeySet },
  ] : [
    { key: 'sesSecretId', label: 'SecretId', saved: !!state.sesSecretIdSet },
    { key: 'sesSecretKey', label: 'SecretKey', saved: !!state.sesSecretKeySet },
  ]
  const saveChannel = () => {
    let payload: Record<string, unknown>
    try { payload = mailChannelUpdate(provider, channel, clearSecrets, c) }
    catch (error) { return say(error instanceof Error ? error.message : '请检查通道设置') }
    updateMail.mutate(payload, { onSuccess: () => { resetChannel(); say(`${providerName(provider)} 通道已保存`) }, onError: fail })
  }
  const saveTemplates = () => {
    const ids = { ...c.sesTemplateIds }
    for (const [key, name] of TEMPLATES) {
      const raw = (templates[key] ?? String(ids[key] ?? '')).trim()
      if (!raw && key.startsWith('notification_') && !c.notificationsEnabled) { delete ids[key]; continue }
      if (!/^\d+$/.test(raw) || !Number.isSafeInteger(Number(raw)) || Number(raw) <= 0) return say(`请填写「${name}」的正整数模板 ID`)
      ids[key] = Number(raw)
    }
    updateMail.mutate({ sesTemplateIds: ids }, { onSuccess: () => { setTemplates({}); say('腾讯云模板已保存') }, onError: fail })
  }
  const savePolicy = () => {
    const perMinute = Number(policy.perMinute ?? c.perMinute), perDay = Number(policy.perDay ?? c.perDay)
    if (![perMinute, perDay].every((value) => Number.isSafeInteger(value) && value > 0)) return say('发送额度必须填写正整数')
    updateMail.mutate({ perMinute, perDay, quietStart: policy.quietStart ?? c.quietStart, quietEnd: policy.quietEnd ?? c.quietEnd }, { onSuccess: () => { setPolicy({}); say('通知策略已保存') }, onError: fail })
  }
  return <div className="ops-page">
    <PageHead en="MAIL & NOTIFICATIONS" title="邮件与通知" desc="从发信连接到通知策略，在同一处检查每封邮件的去向。" side={<div className="ops-inline-status"><Pill tone={state.configured ? 'ok' : 'idle'}>{providerName(state.activeProvider)}</Pill><Pill tone={c.notificationsEnabled ? 'ok' : 'idle'}>业务通知{c.notificationsEnabled ? '已开启' : '已暂停'}</Pill></div>} />
    <OpsTabs id="mail" value={tab} onChange={setTab} items={TABS} />
    <OpsTabPanel id="mail" name="channel" active={tab}>
      <fieldset disabled={busy} className="ops-form">
        <OpsSection title="选择发信服务" desc="所有通道共用通知策略。切换通道时，其他服务已保存的凭据会保留。">
          <div className="ops-provider-choices">{PROVIDERS.map((item) => <button className="ops-provider-choice" key={item.id} type="button" aria-pressed={provider === item.id} onClick={() => change('provider', item.id)}><strong>{item.name}</strong><span>{item.desc}</span></button>)}</div>
          <div className="ops-inline-status" style={{ marginTop: 14 }}><span>当前生效：{providerName(state.activeProvider)}</span><span>来源：{fromDatabase ? '控制台配置' : '部署环境'}</span>{provider !== state.activeProvider && <Pill tone="warn">新通道尚未保存</Pill>}</div>
          {state.configured === false && state.configurationError && <div style={{ marginTop: 12 }}><Note tone="warn">{state.configurationError}</Note></div>}
        </OpsSection>
        <OpsSection title="连接与凭据" desc="凭据加密保存，不会回显。留空保持已有值；明确选择清除后，保存才会删除。">
          {!encrypted && <div style={{ marginBottom: 18 }}><Note tone="warn">部署尚未配置邮件凭据加密密钥，暂时无法保存新凭据。</Note></div>}
          <div className="ops-form-grid">
            {provider === 'smtp' ? <>
              <OpsField label="SMTP 服务器" hint="只填主机名，不包含协议或路径。"><input value={field('smtpHost')} onChange={(event) => change('smtpHost', event.target.value)} placeholder="smtp.example.org" style={fieldStyle} /></OpsField>
              <OpsField label="端口"><input type="number" min={1} max={65535} value={field('smtpPort', '587')} onChange={(event) => change('smtpPort', event.target.value)} style={fieldStyle} /></OpsField>
              <OpsField label="传输加密" hint="与服务商提供的端口及加密方式一致。"><select value={field('smtpSecurity', 'starttls')} onChange={(event) => change('smtpSecurity', event.target.value)} style={fieldStyle}><option value="starttls">STARTTLS · 通常使用 587</option><option value="tls">TLS · 通常使用 465</option></select></OpsField>
            </> : provider === 'aliyun_dm' ? <OpsField label="阿里云服务地域" hint="与已验证的发信地址所在地域一致。"><select value={field('aliyunRegion', 'cn-hangzhou')} onChange={(event) => change('aliyunRegion', event.target.value)} style={fieldStyle}><option value="cn-hangzhou">华东 1 · 杭州</option><option value="ap-southeast-1">新加坡</option><option value="us-east-1">美国 · 弗吉尼亚</option><option value="eu-central-1">德国 · 法兰克福</option></select></OpsField>
              : provider === 'tencent_ses' ? <OpsField label="腾讯云服务地域"><select value={field('sesRegion', 'ap-guangzhou')} onChange={(event) => change('sesRegion', event.target.value)} style={fieldStyle}><option value="ap-guangzhou">华南 · 广州</option><option value="ap-hongkong">中国香港</option></select></OpsField> : null}
            {secretFields.map((secret) => <div key={secret.key}>
              <OpsField label={secret.label} hint={clearSecrets.includes(secret.key) ? '保存时将清除这项凭据。' : secret.saved ? '已保存，留空表示不修改。' : provider === 'smtp' ? '不需要认证的 TLS 中继可同时留空用户名与密码。' : '尚未保存凭据。'}><input type="password" autoComplete="new-password" disabled={!encrypted || clearSecrets.includes(secret.key)} value={channel[secret.key] ?? ''} onChange={(event) => change(secret.key, event.target.value)} placeholder={secret.saved ? '输入新值以替换' : `填写${secret.label}`} style={fieldStyle} /></OpsField>
              {(secret.saved || clearSecrets.includes(secret.key)) && <div className="ops-secret-controls"><button type="button" onClick={() => setClearSecrets((current) => current.includes(secret.key) ? current.filter((key) => key !== secret.key) : [...current, secret.key])}>{clearSecrets.includes(secret.key) ? '撤销清除' : '清除已保存凭据'}</button></div>}
            </div>)}
          </div>
        </OpsSection>
        <OpsSection title="发件人" desc="认证邮件与业务通知使用不同发信地址。先在服务商侧完成地址和域名验证。"><div className="ops-form-grid">{[
          { key: 'sesFrom', label: '认证发信地址', hint: '验证码、找回密码与测试邮件使用。', placeholder: 'account@example.org' },
          { key: 'sesFromName', label: '认证发件人名称', hint: '可选，用于在收件箱中显示发件人名称。', placeholder: 'EasyGPA Plus' },
          { key: 'sesNotificationFrom', label: '通知发信地址', hint: `业务通知使用 ${c.sesNotificationDomain ?? '独立通知域名'} 下的地址。`, placeholder: 'updates@notify.example.org' },
          { key: 'sesNotificationFromName', label: '通知发件人名称', hint: '可选，用于在收件箱中显示发件人名称。', placeholder: 'EasyGPA Plus 进展提醒' },
          { key: 'sesReplyTo', label: '回复地址（可选）', hint: '留空时使用邮件服务商的默认回复行为。', placeholder: 'support@example.org' },
        ].map((item) => <OpsField key={item.key} label={item.label} hint={item.hint}><input value={field(item.key)} onChange={(event) => change(item.key, event.target.value)} placeholder={item.placeholder} style={fieldStyle} /></OpsField>)}</div></OpsSection>
        <OpsActions note={dirty ? '有尚未保存的通道改动' : '通道设置已与服务器同步'}><Btn primary disabled={!dirty || busy} onClick={saveChannel}>{updateMail.isPending ? '保存中…' : '保存发信通道'}</Btn><Btn disabled={!dirty || busy} onClick={resetChannel}>撤销改动</Btn></OpsActions>
      </fieldset>
      <OpsSection title="发送测试邮件" desc="测试使用已经保存的通道。保存改动后，给自己发送一封邮件检查结果。"><div className="ops-toolbar"><OpsField label="测试收件邮箱"><input type="email" value={to} onChange={(event) => setTo(event.target.value)} placeholder="you@example.org" style={fieldStyle} /></OpsField><Btn disabled={dirty || busy || !to.trim().includes('@') || testMail.isPending || state.configured === false} onClick={() => testMail.mutate(to.trim(), { onSuccess: (result) => say(`邮件服务已受理${result.messageId ? ` · ${result.messageId}` : ''}`), onError: fail })}>{testMail.isPending ? '发送中…' : '发送测试邮件'}</Btn></div><p className="ops-field-hint">“已受理”表示发信服务接受了请求，实际送达请检查收件箱或投递日志。</p></OpsSection>
      <details className="ops-reading" style={{ padding: '20px 0' }}><summary>恢复部署配置</summary><p>清除本页保存的所有通道配置、凭据与模板 ID，回到部署环境提供的设置。</p><Btn danger disabled={!fromDatabase || busy} onClick={() => { if (!window.confirm('清除全部邮件通道配置、凭据和模板 ID，并回到部署环境配置？')) return; clearMailChannel.mutate(undefined, { onSuccess: () => { resetChannel(); setTemplates({}); say('已恢复部署邮件配置') }, onError: fail }) }}>清除控制台通道配置</Btn></details>
    </OpsTabPanel>
    <OpsTabPanel id="mail" name="policy" active={tab}>
      <OpsSection title="业务通知开关" desc="只影响业务进展邮件。验证码、找回密码和通道自检始终独立运行。">
        <Pill tone={c.notificationsEnabled ? 'ok' : 'idle'}>{c.notificationsEnabled ? '正在发送新的业务进展' : '业务邮件已暂停'}</Pill><p className="ops-reading">暂停后会清理待发积压；再次开启只提醒新的进展。每位用户还可以选择通知分类、每日汇总与个人免打扰时段。</p>
        {!c.notificationsEnabled && <label className="ops-checkline"><input type="checkbox" checked={approved} onChange={(event) => setApproved(event.target.checked)} />{state.activeProvider === 'tencent_ses' ? '我已确认两个业务通知模板审核通过，模板 ID 和独立通知发信地址均已保存。' : '我已确认独立通知发信地址与服务商验证状态，并通过已保存通道发送了测试邮件。'}</label>}
        <OpsActions><Btn primary={!c.notificationsEnabled} danger={!!c.notificationsEnabled} disabled={busy || dirty || templatesDirty || (!c.notificationsEnabled && (!approved || state.configured === false))} onClick={() => updateMail.mutate({ notificationsEnabled: !c.notificationsEnabled }, { onSuccess: () => { setApproved(false); say(c.notificationsEnabled ? '业务邮件已暂停，不会补发积压' : '业务邮件已开启') }, onError: fail })}>{busy ? '保存中…' : c.notificationsEnabled ? '暂停业务邮件' : '开启业务邮件'}</Btn></OpsActions>
        {state.notificationStats && <div className="ops-inline-status"><span>受保护地址 {state.notificationStats.suppressedRecipients}</span><span>待发 {state.notificationStats.pendingBatches} 批</span><span>失败 {state.notificationStats.failedBatches} 批</span></div>}
        <p className="ops-field-hint">{state.feedbackSupported ? state.notificationStats?.lastFeedbackAt ? `送达反馈最近同步于 ${f.dateTime(state.notificationStats.lastFeedbackAt)}` : '支持送达反馈，等待首次同步。' : '当前通道的送达结果请结合收件箱与邮件服务商日志核对。'}</p>
      </OpsSection>
      <fieldset disabled={busy} className="ops-form">
        <OpsSection title="发送额度" desc="限制平台整体投递速度。超出额度的邮件进入队列顺延。"><div className="ops-form-grid">{[{ key: 'perMinute', label: '每分钟上限（封）' }, { key: 'perDay', label: '每日上限（封）' }].map((item) => <OpsField key={item.key} label={item.label}><input type="number" min={1} step={1} value={policy[item.key] ?? String((c as Record<string, unknown>)[item.key] ?? '')} onChange={(event) => setPolicy((current) => ({ ...current, [item.key]: event.target.value }))} style={fieldStyle} /></OpsField>)}</div></OpsSection>
        <OpsSection title="平台免打扰" desc="北京时间，可跨午夜。期间暂存业务通知，验证码与找回密码不受影响。"><div className="ops-form-grid">{[{ key: 'quietStart', label: '开始时间', fallback: '22:00' }, { key: 'quietEnd', label: '结束时间', fallback: '07:00' }].map((item) => <OpsField key={item.key} label={item.label}><input type="time" value={policy[item.key] ?? String((c as Record<string, unknown>)[item.key] ?? item.fallback)} onChange={(event) => setPolicy((current) => ({ ...current, [item.key]: event.target.value }))} style={fieldStyle} /></OpsField>)}</div></OpsSection>
        <OpsActions note="个人通知偏好会在平台策略内继续生效。"><Btn primary disabled={!Object.keys(policy).length || busy} onClick={savePolicy}>{updateMail.isPending ? '保存中…' : '保存通知策略'}</Btn><Btn disabled={!Object.keys(policy).length || busy} onClick={() => setPolicy({})}>撤销改动</Btn></OpsActions>
      </fieldset>
    </OpsTabPanel>
    <OpsTabPanel id="mail" name="templates" active={tab}>
      <OpsSection title="邮件内容与模板" desc={provider === 'tencent_ses' ? '在腾讯云创建并审核模板，再填写相应 ID。暂停业务通知时，两个通知模板可暂不配置。' : `${providerName(provider)} 使用系统内置邮件模板，无需填写云模板 ID。`}><fieldset className="ops-form" disabled={busy}>{TEMPLATES.map(([key, name, desc]) => <div key={key} className="ops-setting-row"><div className="ops-setting-copy"><strong>{name}</strong><p>{desc}</p><span className="ops-field-hint">{key}</span></div>{provider === 'tencent_ses' ? <OpsField label={`${name}模板 ID`}><input inputMode="numeric" value={templates[key] ?? String(c.sesTemplateIds?.[key] ?? '')} onChange={(event) => setTemplates((current) => ({ ...current, [key]: event.target.value }))} placeholder="模板 ID" style={{ ...fieldStyle, width: 130 }} /></OpsField> : <Pill tone="idle">内置模板</Pill>}</div>)}</fieldset>{provider === 'tencent_ses' && <OpsActions><Btn primary disabled={!templatesDirty || busy} onClick={saveTemplates}>{updateMail.isPending ? '保存中…' : '保存模板 ID'}</Btn><Btn disabled={!templatesDirty || busy} onClick={() => setTemplates({})}>撤销改动</Btn></OpsActions>}</OpsSection>
    </OpsTabPanel>
    <OpsTabPanel id="mail" name="logs" active={tab}><MailDeliveryLog /></OpsTabPanel>
  </div>
}

function MailDeliveryLog() {
  const say = useApp((state) => state.say)
  const [page, setPage] = useState(1)
  const [filter, setFilter] = useState({ status: '', tenantId: '', from: '', to: '' })
  const [selectedId, setSelectedId] = useState('')
  const [messageId, setMessageId] = useState('')
  const [checked, setChecked] = useState(false)
  const iso = (value: string) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toISOString() : undefined
  const log = useMailLog({ status: filter.status || undefined, tenantId: filter.tenantId || undefined, from: iso(filter.from), to: iso(filter.to), page, page_size: 50 })
  const { reconcileMail } = useOpsActions()
  const rows = log.data?.items ?? []
  const pending = (row: (typeof rows)[number]) => row.status === 'queued' && Date.now() - Date.parse(row.createdAt) > 120_000
  const selected = rows.find((row) => row.id === selectedId && pending(row))
  const unresolved = rows.filter(pending).length
  const fail = (error: unknown) => say(error instanceof ApiError ? error.message : '操作失败，请重试')
  const reconcile = (status: 'sent' | 'failed') => {
    if (!selected || !checked) return
    reconcileMail.mutate({ id: selected.id, status, messageId: status === 'sent' ? messageId.trim() : '' }, { onSuccess: (result) => { setSelectedId(''); setMessageId(''); setChecked(false); say(result.status === 'sent' ? '已记录受理结果，不再重复发送' : result.eventId ? '已确认未受理，原通知可继续重试；失败队列请在任务页重投' : '已确认未受理，认证邮件可由用户重新发起') }, onError: fail })
  }
  return <div style={{ paddingTop: 26 }}>
    <Sub title="投递记录" note={`共 ${log.data?.total ?? 0} 条 · 包含业务通知与认证邮件`} actions={<Btn disabled={log.isFetching} onClick={() => void log.refetch()}>{log.isFetching ? '刷新中…' : '刷新日志'}</Btn>} />
    {unresolved > 0 && <Note tone="warn">本页有 {unresolved} 条结果待确认的邮件，系统已暂停重复发送。请核对服务商记录后处理。</Note>}
    <div className="ops-toolbar"><OpsField label="投递状态"><select value={filter.status} onChange={(event) => { setFilter({ ...filter, status: event.target.value }); setPage(1) }} style={fieldStyle}><option value="">全部状态</option>{Object.entries(STATUS_META).map(([key, item]) => <option key={key} value={key}>{item.label}</option>)}</select></OpsField><OpsField label="班级 ID"><input value={filter.tenantId} onChange={(event) => { setFilter({ ...filter, tenantId: event.target.value }); setPage(1) }} placeholder="全部班级" inputMode="numeric" style={fieldStyle} /></OpsField><OpsField label="起始时间"><input type="datetime-local" value={filter.from} onChange={(event) => { setFilter({ ...filter, from: event.target.value }); setPage(1) }} style={fieldStyle} /></OpsField><OpsField label="结束时间"><input type="datetime-local" value={filter.to} onChange={(event) => { setFilter({ ...filter, to: event.target.value }); setPage(1) }} style={fieldStyle} /></OpsField></div>
    {selected && <div className="ops-detail-box"><Sub title={`核对投递 #${selected.id}`} note={`${f.dateTime(selected.createdAt)} · 收件人 ${selected.recipientHash.slice(0, 16)}…`} /><Note tone="warn">先核对邮件服务商日志。确认已受理会阻止重复发送；确认未受理才允许再次尝试。已进入失败队列的通知仍需在任务页重投。</Note><div style={{ marginTop: 18 }}><OpsField label="服务商 MessageId" hint="确认已受理时必填；确认未受理时应留空。"><input value={messageId} onChange={(event) => setMessageId(event.target.value)} maxLength={256} style={fieldStyle} /></OpsField></div><label className="ops-checkline" style={{ marginTop: 14 }}><input type="checkbox" checked={checked} onChange={(event) => setChecked(event.target.checked)} />我已核对服务商日志，确认本次请求结果</label><OpsActions><Btn primary disabled={!checked || !messageId.trim() || reconcileMail.isPending} onClick={() => reconcile('sent')}>确认已受理</Btn><Btn danger disabled={!checked || !!messageId.trim() || reconcileMail.isPending} onClick={() => reconcile('failed')}>确认未受理</Btn><Btn disabled={reconcileMail.isPending} onClick={() => setSelectedId('')}>取消</Btn></OpsActions></div>}
    {log.isError ? <OpsLoadError title="投递日志暂时无法读取" onRetry={() => void log.refetch()} /> : log.isLoading ? <div className="load-bar"><span /></div> : !rows.length ? <Empty title="没有匹配的投递记录" desc="调整筛选条件，或先发送一封测试邮件。" /> : <Table cols="130px 80px minmax(110px,1fr) 100px 54px minmax(120px,1fr) minmax(150px,1.5fr)"><THead cells={['时间', '班级', '邮件类型', '状态', '尝试', '收件人哈希', '错误 / 消息 ID']} />{rows.map((row) => <TRow key={row.id} cells={[
      <span key="time" style={mono('11.5px', '0')}>{f.dateTime(row.createdAt)}</span>,
      <span key="tenant" style={mono('11px', '0')}>{row.tenantId ? `#${row.tenantId}` : '平台'}</span>,
      <span key="template">{TEMPLATES.find(([key]) => key === row.template)?.[1] ?? (row.template === 'legacy_notification' ? '历史通知' : row.template ?? '业务通知')}<span className="ops-field-hint" style={{ display: 'block', marginTop: 4 }}>{providerName(row.provider)}</span></span>,
      <div key="status" style={{ display: 'grid', gap: 6 }}><Pill tone={STATUS_META[row.status]?.tone ?? 'idle'}>{STATUS_META[row.status]?.label ?? row.status}</Pill>{row.feedbackStatus && <span className="ops-field-hint">{FEEDBACK_LABEL[row.feedbackStatus] ?? row.feedbackStatus}</span>}</div>,
      <span key="attempt" style={num}>{row.attempt}</span>, <span key="recipient" style={mono('11px', '0')}>{row.recipientHash.slice(0, 16)}…</span>,
      <div key="detail" style={{ minWidth: 0 }}><span title={row.errorCode ?? row.error ?? row.messageId ?? undefined} style={{ display: 'block', fontSize: 12, color: row.error ? 'var(--red)' : 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.errorCode ?? row.error ?? row.messageId ?? '—'}</span>{pending(row) && <div style={{ marginTop: 8 }}><Btn onClick={() => { setSelectedId(row.id); setMessageId(''); setChecked(false) }}>核对结果</Btn></div>}</div>,
    ]} />)}</Table>}
    <OpsActions note={`第 ${page} / ${Math.max(1, Math.ceil((log.data?.total ?? 0) / 50))} 页`}><Btn disabled={page <= 1 || log.isFetching} onClick={() => setPage((value) => value - 1)}>上一页</Btn><Btn disabled={page * 50 >= (log.data?.total ?? 0) || log.isFetching} onClick={() => setPage((value) => value + 1)}>下一页</Btn></OpsActions>
  </div>
}
