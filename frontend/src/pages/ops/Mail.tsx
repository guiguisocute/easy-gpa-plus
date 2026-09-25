/* 运维超管 · 邮件与通知。

   生产投递只走腾讯云 SES SendEmail API。SecretId/SecretKey 加密后存入 ops 库，
   任何接口都不回显；模板 ID 则显式覆盖仓库里的每一种事务邮件。 */

import { useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, TRow } from '@/components/ui'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useMailConfig, useMailLog, useOpsActions } from '@/api/queries'
import '@/styles/mobile-ops.css'

const STATUS_META: Record<string, { label: string; tone: 'ok' | 'warn' | 'bad' | 'idle' }> = {
  sent: { label: '已受理', tone: 'ok' },
  delivered: { label: '已投递', tone: 'ok' },
  queued: { label: '结果待确认', tone: 'warn' },
  retrying: { label: '重试中', tone: 'warn' },
  failed: { label: '失败', tone: 'bad' },
  suppressed: { label: '发送受限', tone: 'warn' },
}

const PROVIDER_LABEL: Record<string, string> = { tencent_ses: '腾讯云 SES' }
const SOURCE_LABEL: Record<string, string> = { database: '控制台配置', environment: '部署配置' }
const FEEDBACK_LABEL: Record<string, string> = { delivered: '已送达', complaint: '已举报 · 停止业务邮件', unsubscribed: '已退订', rejected: '收件方拒收', hard_bounce: '无效地址 · 停止发送', invalid_address: '无效地址', dropped: '已丢弃', deferred: '延迟投递', accepted: '待送达', not_found: '暂无反馈', lookup_failed: '反馈查询失败' }

const SES_TEMPLATES = [
  ['notification_alert', '新版 · 进展提醒（合并或逐条）'],
  ['notification_digest', '新版 · 每日汇总'],
  ['verification_code', '绑定邮箱验证码'],
  ['password_reset', '找回密码'],
  ['mail_test', '通道自检'],
] as const

export default function OpsMail() {
  const say = useApp((s) => s.say)
  const config = useMailConfig()
  const [logPage, setLogPage] = useState(1)
  const [logStatus, setLogStatus] = useState('')
  const [logTenant, setLogTenant] = useState('')
  const [logFrom, setLogFrom] = useState('')
  const [logTo, setLogTo] = useState('')
  const log = useMailLog({ status: logStatus || undefined, tenantId: logTenant || undefined, from: logFrom ? new Date(logFrom).toISOString() : undefined, to: logTo ? new Date(logTo).toISOString() : undefined, page: logPage, page_size: 200 })
  const { updateMail, testMail, clearMailChannel, reconcileMail } = useOpsActions()
  const [reconcileId, setReconcileId] = useState('')
  const [reconcileMessageId, setReconcileMessageId] = useState('')
  const [providerChecked, setProviderChecked] = useState(false)
  const [notificationTemplatesApproved, setNotificationTemplatesApproved] = useState(false)

  const [to, setTo] = useState('')
  const [limits, setLimits] = useState<Record<string, string>>({})
  const [quiet, setQuiet] = useState<Record<string, string>>({})
  const [channel, setChannel] = useState<Record<string, string>>({})
  const [templateEdits, setTemplateEdits] = useState<Record<string, string>>({})

  const c = config.data?.config ?? {}
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')
  const secretIDSet = !!config.data?.sesSecretIdSet
  const secretKeySet = !!config.data?.sesSecretKeySet
  const secretStorageReady = config.data?.secretKeyReady !== false
  const fromDatabase = config.data?.secretSource === 'database'
  const channelField = (key: string, fallback: unknown = '') => channel[key] ?? String((c as Record<string, unknown>)[key] ?? fallback)
  const templateField = (name: string) => templateEdits[name] ?? String(c.sesTemplateIds?.[name] ?? '')
  const dirty = Object.keys(channel).length > 0 || Object.keys(templateEdits).length > 0

  const saveChannel = () => {
    if (!dirty) return
    const payload: Record<string, unknown> = { provider: 'tencent_ses', ...channel }
    if (Object.keys(templateEdits).length > 0) {
      const ids: Record<string, number> = { ...c.sesTemplateIds }
      for (const [name] of SES_TEMPLATES) {
        const raw = templateField(name).trim()
        if (name.startsWith('notification_') && !raw) { delete ids[name]; continue }
        if (!/^\d+$/.test(raw) || Number(raw) <= 0 || !Number.isSafeInteger(Number(raw))) {
          say(`模板「${name}」必须填写正整数 ID`)
          return
        }
        ids[name] = Number(raw)
      }
      payload.sesTemplateIds = ids
    }
    updateMail.mutate(payload, {
      onSuccess: () => {
        setChannel({})
        setTemplateEdits({})
        say('腾讯云 SES 通道已保存 · 业务邮件开关保持原设置')
      },
      onError: fail,
    })
  }

  const rows = log.data?.items ?? []
  const failed = rows.filter((r) => r.status === 'failed').length
  const unresolved = rows.filter((r) => r.status === 'queued' && Date.now() - new Date(r.createdAt).getTime() > 120_000).length
  const reconcileRow = rows.find((r) => r.id === reconcileId && r.status === 'queued' && Date.now() - new Date(r.createdAt).getTime() > 120_000)
  const submitReconciliation = (status: 'sent' | 'failed') => {
    if (!reconcileRow || !providerChecked) return
    reconcileMail.mutate({ id: reconcileRow.id, status, messageId: status === 'sent' ? reconcileMessageId.trim() : '' }, {
      onSuccess: (result) => {
        setReconcileId('')
        setReconcileMessageId('')
        setProviderChecked(false)
        say(result.status === 'sent' ? '已记录供应商受理结果，不再重复发送' : result.eventId ? '已确认未受理，原通知可继续重试；死信请在队列页重投' : '已确认未受理，验证码或找回密码可由用户重新发起')
      },
      onError: fail,
    })
  }
  const limitFields = [
    { key: 'perMinute', name: '每分钟上限', desc: '突发保护，超出的入队顺延', unit: '封/分' },
    { key: 'perDay', name: '每日上限', desc: '当日总投递额度', unit: '封/天' },
  ]

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="MAIL"
        title="邮件与通知"
        desc="配置发信通道、模板与投递日志。"
        side={<><Pill tone="ok">{PROVIDER_LABEL[config.data?.activeProvider ?? 'tencent_ses'] ?? config.data?.activeProvider}</Pill><Pill tone="idle">凭据：{SOURCE_LABEL[config.data?.secretSource ?? 'environment'] ?? config.data?.secretSource}</Pill></>}
      />

      <section style={{ border: '1px solid var(--line)', borderRadius: 8, padding: 20, marginBottom: 22 }} aria-label="业务邮件总开关">
        <div style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap', marginBottom: 12 }}><strong style={{ fontSize: 15 }}>业务邮件</strong><Pill tone={c.notificationsEnabled ? 'ok' : 'warn'}>{c.notificationsEnabled ? '已启用' : '已暂停'}</Pill></div>
        <Note>暂停会跳过业务邮件并清理待发积压，恢复后只处理新的进展。验证码、找回密码与通道自检独立运行。新版默认每人每天最多 3 封，支持分类退订、免打扰和每日汇总。</Note>
        {config.data?.notificationStats && <p style={{ color: 'var(--fg3)', fontSize: 12, lineHeight: 1.9 }}>已保护 {config.data.notificationStats.suppressedRecipients} 个退订或拒收地址 · 待发 {config.data.notificationStats.pendingBatches} 批 · 失败 {config.data.notificationStats.failedBatches} 批 · 腾讯云反馈{config.data.notificationStats.lastFeedbackAt ? `最近同步于 ${f.dateTime(config.data.notificationStats.lastFeedbackAt)}` : '等待首次同步'}</p>}
        {!c.notificationsEnabled && <label style={{ display: 'flex', gap: 9, alignItems: 'flex-start', marginTop: 16, fontSize: 12.5, lineHeight: 1.8 }}><input type="checkbox" checked={notificationTemplatesApproved} onChange={(e) => setNotificationTemplatesApproved(e.target.checked)} />我已在腾讯云确认 notification_alert 和 notification_digest 审核通过，并在本页保存了正确的模板 ID 和独立通知发信地址。</label>}
        <div style={{ marginTop: 16 }}><Btn danger={!!c.notificationsEnabled} primary={!c.notificationsEnabled} disabled={!config.data || updateMail.isPending || (!c.notificationsEnabled && (!notificationTemplatesApproved || dirty))} onClick={() => updateMail.mutate({ notificationsEnabled: !c.notificationsEnabled }, { onSuccess: () => { setNotificationTemplatesApproved(false); say(c.notificationsEnabled ? '业务邮件已暂停，不会补发积压' : '业务邮件已启用，按用户偏好提醒新进展') }, onError: fail })}>{c.notificationsEnabled ? '立即暂停业务邮件' : '启用新版业务邮件'}</Btn></div>
      </section>

      <StatGrid cols={4}>
        <Stat en="最近投递" value={String(rows.length)} unit="封" note="默认取最近 200 条" />
        <Stat en="失败" value={String(failed)} unit="封" note="多次失败后暂停投递" tone={failed > 0 ? 'var(--red)' : undefined} />
        <Stat en="每分钟上限" value={String(c.perMinute ?? '—')} note="超出部分自动顺延" />
        <Stat en="静默时段" value={c.quietStart && c.quietEnd ? `${c.quietStart}—${c.quietEnd}` : '未设置'} note="只入队，时段结束后投递" />
      </StatGrid>

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '30px 0' }}>
            <Sub title="限额" note="改完马上生效" />
          <Note>业务邮件默认每天最多 3 封，可调到 1—50。逐条提醒要用户自己开。验证码和找回密码不受静默时段限制。</Note>
            {limitFields.map((lf) => (
              <div key={lf.key} style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0', flexWrap: 'wrap' }}>
                <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 160, flex: 1 }}><span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>{lf.name}</span><span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{lf.desc}</span></span>
                <input value={limits[lf.key] ?? String((c as Record<string, unknown>)[lf.key] ?? '')} onChange={(e) => setLimits((s) => ({ ...s, [lf.key]: e.target.value }))} inputMode="numeric" style={{ ...fieldStyle, width: 90, textAlign: 'right', border: '1px solid var(--line)', padding: '7px 10px', ...num }} />
                <span style={{ fontSize: 12.5, color: 'var(--fg3)', width: 52, flex: 'none' }}>{lf.unit}</span>
                <Btn disabled={limits[lf.key] === undefined || !/^\d+$/.test(limits[lf.key])} onClick={() => updateMail.mutate({ [lf.key]: Number(limits[lf.key]) }, { onSuccess: () => { setLimits((s) => { const next = { ...s }; delete next[lf.key]; return next }); say(`${lf.name} 已更新`) }, onError: fail })}>保存</Btn>
              </div>
            ))}

            <div style={{ borderTop: '1px solid var(--line)', marginTop: 18, paddingTop: 18 }}>
              <Sub title="静默时段" note="北京时间，可跨午夜" />
              <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
                <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}><span style={{ fontSize: 12 }}>开始</span><input type="time" value={quiet.quietStart ?? c.quietStart ?? '22:00'} onChange={(e) => setQuiet((v) => ({ ...v, quietStart: e.target.value }))} style={fieldStyle} /></label>
                <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}><span style={{ fontSize: 12 }}>结束</span><input type="time" value={quiet.quietEnd ?? c.quietEnd ?? '07:00'} onChange={(e) => setQuiet((v) => ({ ...v, quietEnd: e.target.value }))} style={fieldStyle} /></label>
                <Btn disabled={Object.keys(quiet).length === 0 || updateMail.isPending} onClick={() => updateMail.mutate(quiet, { onSuccess: () => { setQuiet({}); say('静默时段已更新') }, onError: fail })}>保存静默时段</Btn>
              </div>
            </div>

            <div style={{ borderTop: '1px solid var(--line)', marginTop: 18, paddingTop: 18 }}>
              <Sub title="通道自检" note="发送一封测试邮件" />
              <div style={{ display: 'flex', alignItems: 'flex-end', gap: 12, flexWrap: 'wrap' }}>
                <label style={{ display: 'flex', flexDirection: 'column', gap: 7, flex: 1, minWidth: 200 }}><span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>收件邮箱</span><input className="hv-bfg" value={to} onChange={(e) => setTo(e.target.value)} placeholder="you@example.com" style={fieldStyle} /></label>
                <Btn primary disabled={!to.includes('@') || testMail.isPending} onClick={() => testMail.mutate(to.trim(), { onSuccess: (r) => say(`腾讯云已受理 · ${r.messageId}`), onError: fail })}>{testMail.isPending ? '发送中…' : '发送测试邮件'}</Btn>
              </div>
            </div>
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '30px 0' }}>
            <Sub title="腾讯云 SES API" note={fromDatabase ? '当前使用控制台配置' : '现在用的是部署时的配置。填全保存就切到这一套'} />
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', padding: '4px 0 14px' }}>
              <Pill tone={fromDatabase ? 'ok' : 'idle'}>{fromDatabase ? '控制台配置' : '部署配置'}</Pill>
              <Pill tone={secretIDSet && secretKeySet ? 'ok' : 'idle'}>{secretIDSet && secretKeySet ? 'API 凭据已保存' : '未保存完整凭据'}</Pill>
            </div>

            {!secretStorageReady && <div style={{ paddingBottom: 14 }}><Note tone="warn">还没配加密密钥，现在存不了 SecretId 和 SecretKey。</Note></div>}

            <label style={{ display: 'flex', flexDirection: 'column', gap: 6, borderTop: '1px solid var(--line2)', padding: '12px 0' }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>服务地域</span>
              <select className="hv-bfg" value={channelField('sesRegion', 'ap-guangzhou')} onChange={(e) => setChannel((s) => ({ ...s, sesRegion: e.target.value }))} style={fieldStyle}><option value="ap-guangzhou">ap-guangzhou · 广州</option><option value="ap-hongkong">ap-hongkong · 中国香港</option></select>
            </label>

            {[
              { key: 'sesFrom', name: '认证发信地址', hint: '验证码、找回密码与通道自检使用现有地址', ph: '已验证的认证发信地址' },
              { key: 'sesFromName', name: '认证发件人显示名', hint: '留空时显示 EasyGPA Plus；不能包含冒号、尖括号或换行', ph: 'EasyGPA Plus' },
              { key: 'sesNotificationFrom', name: '通知发信地址', hint: `使用已验证的 ${c.sesNotificationDomain ?? 'notify.example.org'} 下的发信地址；不会回退到认证域名`, ph: '填写腾讯云控制台中的完整通知发信地址' },
              { key: 'sesNotificationFromName', name: '通知发件人显示名', hint: '留空时显示 EasyGPA Plus', ph: 'EasyGPA Plus 综测提醒' },
              { key: 'sesReplyTo', name: '回复地址', hint: '可以不填。不填的话，收件人直接回复可能发不出去', ph: 'support@example.org' },
            ].map((field) => (
              <label key={field.key} style={{ display: 'flex', flexDirection: 'column', gap: 6, borderTop: '1px solid var(--line2)', padding: '12px 0' }}>
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>{field.name}</span>
                <input className="hv-bfg" value={channelField(field.key)} placeholder={field.ph} onChange={(e) => setChannel((s) => ({ ...s, [field.key]: e.target.value }))} style={fieldStyle} />
                <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6 }}>{field.hint}</span>
              </label>
            ))}

            {[
              { key: 'sesSecretId', name: '腾讯云 SecretId', set: secretIDSet },
              { key: 'sesSecretKey', name: '腾讯云 SecretKey', set: secretKeySet },
            ].map((field) => (
              <label key={field.key} style={{ display: 'flex', flexDirection: 'column', gap: 6, borderTop: '1px solid var(--line2)', padding: '12px 0' }}>
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>{field.name}</span>
                <input className="hv-bfg" type="password" autoComplete="new-password" value={channel[field.key] ?? ''} placeholder={field.set ? '已保存 · 留空表示不改动' : `填写 ${field.name}`} onChange={(e) => setChannel((s) => ({ ...s, [field.key]: e.target.value }))} style={fieldStyle} />
                <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6 }}>加密保存，之后不会回显。</span>
              </label>
            ))}

            <div style={{ borderTop: '1px solid var(--line)', marginTop: 8, paddingTop: 18 }}>
              <Sub title="腾讯云模板 ID" note="两份新版业务模板可在暂停期间留空，审核通过后填写" />
              <Note>业务邮件仅使用两份新版模板。旧业务模板保留历史配置，已停用，无需重新申请；验证码、找回密码和通道自检继续使用已有模板。</Note>
              {SES_TEMPLATES.map(([name, label]) => (
                <label key={name} className="ops-mail-template">
                  <span><span style={{ display: 'block', fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>{label}</span><code style={{ fontSize: 10.5, color: 'var(--fg3)' }}>{name}</code></span>
                  <input className="hv-bfg" value={templateField(name)} inputMode="numeric" placeholder="模板 ID" onChange={(e) => setTemplateEdits((s) => ({ ...s, [name]: e.target.value }))} style={{ ...fieldStyle, textAlign: 'right', ...num }} />
                </label>
              ))}
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', borderTop: '1px solid var(--line)', paddingTop: 14, marginTop: 8 }}>
              <Btn primary disabled={!dirty || updateMail.isPending} onClick={saveChannel}>{updateMail.isPending ? '保存中…' : '保存 SES 通道'}</Btn>
              <Btn disabled={!dirty} onClick={() => { setChannel({}); setTemplateEdits({}) }}>撤销改动</Btn>
              <Btn danger disabled={!fromDatabase || clearMailChannel.isPending} onClick={() => { if (!window.confirm('清空会退回部署时的环境变量，已存的凭据和模板 ID 一起删掉。继续？')) return; clearMailChannel.mutate(undefined, { onSuccess: () => { setChannel({}); setTemplateEdits({}); say('已清空 · 回到部署环境变量') }, onError: fail }) }}>清空并回退</Btn>
            </div>

            <div style={{ marginTop: 16 }}><Note>先去腾讯云把对应模板建好、审核通过，再回来一个个填模板 ID。</Note></div>
          </div>
        </SplitCol>
      </Split>

      <div style={{ padding: '10px 0 0' }}>
        <Sub title="投递日志" note="业务通知、验证码和通道自检" />
        {unresolved > 0 && <Note tone="warn">有 {unresolved} 条邮件尚未确认投递结果，已暂停重复发送。请核对邮件服务商的发信记录后处理。</Note>}
        {reconcileRow && <div style={{ border: '1px solid var(--line)', padding: 16, margin: '12px 0', borderRadius: 8 }}>
          <Sub title={`核对投递 #${reconcileRow.id}`} note={`发起时间 ${f.dateTime(reconcileRow.createdAt)} · 收件人哈希 ${reconcileRow.recipientHash.slice(0, 16)}…`} />
          <Note tone="warn">请先在腾讯云 SES 发信日志中核对该次请求。仅在查明结果后操作：确认已受理会阻止重复发送；确认未受理会允许原通知再次尝试。若通知已经进入死信，仍需在队列页重投。验证码和找回密码由用户重新发起。</Note>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 12, maxWidth: 520 }}><span style={{ fontSize: 12 }}>供应商 MessageId（确认已受理时必填）</span><input value={reconcileMessageId} onChange={(e) => setReconcileMessageId(e.target.value)} placeholder="填写腾讯云 SES 日志中的 MessageId" maxLength={256} style={fieldStyle} /></label>
          <label style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 12, fontSize: 12 }}><input type="checkbox" checked={providerChecked} onChange={(e) => setProviderChecked(e.target.checked)} />我已核对供应商日志，确认本次请求的受理结果</label>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 12 }}>
            <Btn primary disabled={!providerChecked || !reconcileMessageId.trim() || reconcileMail.isPending} onClick={() => submitReconciliation('sent')}>确认已受理</Btn>
            <Btn danger disabled={!providerChecked || !!reconcileMessageId.trim() || reconcileMail.isPending} onClick={() => submitReconciliation('failed')}>确认未受理，允许重试</Btn>
            <Btn disabled={reconcileMail.isPending} onClick={() => setReconcileId('')}>取消</Btn>
          </div>
        </div>}
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', paddingBottom: 12 }}><select value={logStatus} onChange={(e) => { setLogStatus(e.target.value); setLogPage(1) }} style={{ ...fieldStyle, width: 130 }}><option value="">全部状态</option><option value="sent">已受理</option><option value="failed">失败</option><option value="retrying">重试中</option><option value="queued">排队中</option></select><input value={logTenant} onChange={(e) => { setLogTenant(e.target.value); setLogPage(1) }} placeholder="班级 ID" inputMode="numeric" style={{ ...fieldStyle, width: 120 }} /><input type="datetime-local" value={logFrom} onChange={(e) => { setLogFrom(e.target.value); setLogPage(1) }} style={{ ...fieldStyle, width: 190 }} /><input type="datetime-local" value={logTo} onChange={(e) => { setLogTo(e.target.value); setLogPage(1) }} style={{ ...fieldStyle, width: 190 }} /></div>
        {log.isLoading ? <div className="load-bar"><span /></div> : rows.length === 0 ? <Empty title="还没有投递记录" desc="发送通知后将在这里显示。" /> : (
          <Table cols="130px 80px minmax(130px,1fr) 96px 54px minmax(130px,1fr) minmax(160px,1.5fr)">
            <THead cells={['时间', '班级', '邮件类型', '状态', '尝试', '收件人哈希', '错误 / 消息 ID']} />
            {rows.map((r) => <TRow key={r.id} cells={[
              <span key="a" style={mono('11.5px', '.02em')}>{f.dateTime(r.createdAt)}</span>,
              <span key="b" style={mono('11px', '.02em')}>{r.tenantId ? `#${r.tenantId}` : '平台'}</span>,
              <span key="type" style={{ fontSize: 12 }}>{SES_TEMPLATES.find(([key]) => key === r.template)?.[1] ?? (r.template === 'legacy_notification' ? '历史通知' : r.template ?? '业务通知')}</span>,
              <div key="c" style={{ display: 'grid', gap: 6 }}><Pill tone={STATUS_META[r.status]?.tone ?? 'idle'}>{STATUS_META[r.status]?.label ?? r.status}</Pill>{r.feedbackStatus && <span style={{ fontSize: 11, color: 'var(--fg3)' }}>{FEEDBACK_LABEL[r.feedbackStatus] ?? r.feedbackStatus}</span>}</div>,
              <span key="d" style={num}>{r.attempt}</span>,
              <span key="e" style={{ ...mono('11px', '0'), overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.recipientHash.slice(0, 16)}…</span>,
              <div key="f" style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0, alignItems: 'flex-start' }}><span title={r.errorCode ?? r.error ?? r.messageId ?? undefined} style={{ fontSize: 12, color: r.error ? 'var(--red)' : 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '100%' }}>{r.errorCode ?? r.error ?? r.messageId ?? '—'}</span>{r.status === 'queued' && Date.now() - new Date(r.createdAt).getTime() > 120_000 && <Btn onClick={() => { setReconcileId(r.id); setReconcileMessageId(''); setProviderChecked(false) }}>核对结果</Btn>}</div>,
            ]} />)}
          </Table>
        )}
        <div style={{ display: 'flex', gap: 10, alignItems: 'center', paddingTop: 14 }}><Btn disabled={logPage <= 1} onClick={() => setLogPage((value) => value - 1)}>上一页</Btn><span style={mono('11px', '0')}>第 {logPage} / {Math.max(1, Math.ceil((log.data?.total ?? 0) / 200))} 页</span><Btn disabled={logPage * 200 >= (log.data?.total ?? 0)} onClick={() => setLogPage((value) => value + 1)}>下一页</Btn></div>
      </div>
    </div>
  )
}
