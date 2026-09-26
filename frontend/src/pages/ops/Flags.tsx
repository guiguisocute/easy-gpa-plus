/* 运维超管 · 开关与阈值。AI 开关也会在 Agent 配置页展示；后端只有在
   URL、模型和密钥均可安全解析时才允许开启。 */

import BrandingSettings from './Branding'
import { useState } from 'react'
import { Btn, Note, PageHead, Pill, Toggle } from '@/components/ui'
import { fieldStyle, num } from '@/lib/style'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useFlags, useOpsActions } from '@/api/queries'

const META: Record<string, { name: string; desc: string; kind: 'bool' | 'number'; unit?: string; min?: number; max?: number }> = {
  maintenance: { name: '维护模式', desc: '打开之后业务接口只能读不能写，迁移或者紧急排障时用', kind: 'bool' },
  registration: { name: '开放注册', desc: '关掉之后，连白名单里的人也注册不了新账号', kind: 'bool' },
  nativeToolsEnabled: { name: '本地原生转换工具', desc: '紧急关掉 PDF 和旧版 Office 解析。图片不受影响', kind: 'bool' },
  uploadMaxMb: { name: '单份佐证上限', desc: '方案里的 maxMb 不能超过这个值', kind: 'number', unit: 'MB', min: 1, max: 50 },
  requestConcurrency: { name: '请求并发上限', desc: '单个 API 进程的并发处理上限', kind: 'number', unit: '并发', min: 1, max: 512 },
  exportConcurrency: { name: '导出并发上限', desc: '同时能跑几个导出任务。归档包很吃内存，别开太多', kind: 'number', unit: '并发', min: 1, max: 8 },
  passwordHashConcurrency: { name: '密码哈希并发', desc: '登录、注册和重置密码同时执行 Argon2 的数量', kind: 'number', unit: '并发', min: 1, max: 16 },
  apiRateLimitPerMinute: { name: 'API 客户端速率', desc: '每个 IP 或已登录会话每分钟最多请求数', kind: 'number', unit: '次/分', min: 10, max: 100000 },
  apiRateLimitBurst: { name: 'API 突发额度', desc: '允许突发多少个请求，不能超过每分钟的限额', kind: 'number', unit: '次', min: 1, max: 100000 },
  authLoginPerMinute: { name: '登录频率', desc: '每个来源 IP 每分钟登录尝试数', kind: 'number', unit: '次/分', min: 1, max: 120 },
  authRefreshPerMinute: { name: '刷新频率', desc: '每个来源 IP 每分钟刷新会话数', kind: 'number', unit: '次/分', min: 1, max: 600 },
  authRegisterPerHour: { name: '注册频率', desc: '每个来源 IP 每小时注册检查或完成数', kind: 'number', unit: '次/时', min: 1, max: 1000 },
  authForgotPerHour: { name: '找回密码频率', desc: '每个来源 IP 每小时发起找回数', kind: 'number', unit: '次/时', min: 1, max: 120 },
  authResetPerHour: { name: '重置密码频率', desc: '每个来源 IP 每小时提交重置数', kind: 'number', unit: '次/时', min: 1, max: 120 },
  evidencePresignPerHour: { name: '佐证上传授权', desc: '每用户每小时创建佐证或备注上传授权数', kind: 'number', unit: '次/时', min: 1, max: 5000 },
  evidenceDailyMb: { name: '佐证每日字节额度', desc: '每用户每天创建佐证、备注和申诉附件的声明总量', kind: 'number', unit: 'MB/日', min: 1, max: 10000 },
  aiPresignPerHour: { name: 'AI 材料上传授权', desc: '每用户每小时创建 AI 材料上传授权数', kind: 'number', unit: '次/时', min: 1, max: 5000 },
  agentPresignPerHour: { name: 'Agent 附件授权', desc: '每用户每小时创建对话附件上传授权数', kind: 'number', unit: '次/时', min: 1, max: 5000 },
  knowledgePresignPerHour: { name: '知识库上传授权', desc: '每管理员每小时创建知识文件上传授权数', kind: 'number', unit: '次/时', min: 1, max: 5000 },
  aiBatchActionsPerHour: { name: 'AI 批次操作频率', desc: '每用户每小时创建或启动 AI 批次的上限', kind: 'number', unit: '次/时', min: 1, max: 500 },
  agentMessagesPerMinute: { name: 'Agent 消息频率', desc: '每用户每分钟创建 Agent 消息数', kind: 'number', unit: '次/分', min: 1, max: 600 },
  exportRequestsPerHour: { name: '导出请求频率', desc: '每管理员每小时请求导出的上限', kind: 'number', unit: '次/时', min: 1, max: 500 },
  knowledgeReprocessPerHour: { name: '知识库重处理频率', desc: '每管理员每小时重处理文件数', kind: 'number', unit: '次/时', min: 1, max: 500 },
  aiEnabled: { name: 'AI 辅助总开关', desc: '配置填全了就能开。建议先去 Agent 配置页把两个模型都自检一遍', kind: 'bool' },
}

const EVIDENCE_FORMATS = ['pdf', 'jpg', 'jpeg', 'png', 'gif', 'webp', 'heic', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'txt', 'csv', 'wps', 'et', 'dps', 'zip', 'rar', '7z', 'mp4']

import { OpsActions, OpsLoadError, OpsSection, OpsTabPanel, OpsTabs } from '@/components/OpsLayout'

const TABS = [{ id: 'access', label: '访问与容量' }, { id: 'limits', label: '频率限制' }, { id: 'formats', label: '文件格式' }, { id: 'branding', label: '登录页品牌' }]

export default function OpsFlags() {
  const say = useApp((state) => state.say)
  const flags = useFlags()
  const { setFlag } = useOpsActions()
  const [tab, setTab] = useState('access')
  const [edit, setEdit] = useState<Record<string, string>>({})
  const [formatEdit, setFormatEdit] = useState<string[] | null>(null)
  if (flags.isLoading) return <div className="load-bar"><span /></div>
  if (!flags.data) return <OpsLoadError title="平台设置暂时无法读取" onRetry={() => void flags.refetch()} />

  const values = flags.data.flags
  const locked = new Set(flags.data.locked ?? [])
  const deployment = flags.data.deploymentLimits
  const fail = (error: unknown) => say(error instanceof ApiError ? error.message : '修改失败，请重试')
  const selectedFormats = formatEdit ?? (Array.isArray(values.evidenceAllowedFormats) ? values.evidenceAllowedFormats : EVIDENCE_FORMATS)
  const settings = (keys: string[]) => keys.filter((key) => key in values || locked.has(key)).map((key) => {
    const meta = META[key]
    const current = values[key]
    const isLocked = locked.has(key)
    const minimum = meta.min ?? 1
    let maximum = meta.max ?? 10000
    if (key === 'apiRateLimitPerMinute' && deployment) maximum = Math.min(maximum, deployment.apiRateLimitPerMinute)
    if (key === 'apiRateLimitBurst' && deployment) maximum = Math.min(maximum, deployment.apiRateLimitBurst)
    if (key === 'passwordHashConcurrency' && deployment) maximum = Math.min(maximum, deployment.passwordHashConcurrency)
    const changed = edit[key] !== undefined && edit[key] !== String(current)
    const invalid = !/^\d+$/.test(edit[key] ?? '') || Number(edit[key]) < minimum || Number(edit[key]) > maximum
    return <div key={key} className="ops-setting-row">
      <div className="ops-setting-copy"><strong>{meta.name}</strong><p>{meta.desc}</p>{isLocked ? <span className="ops-field-hint">由部署配置锁定</span> : meta.kind === 'number' && <span className="ops-field-hint">可设范围 {minimum}—{maximum} {meta.unit}</span>}</div>
      {meta.kind === 'bool' ? <Toggle label={meta.name} on={!!current} locked={isLocked || setFlag.isPending} onClick={() => setFlag.mutate({ key, value: !current }, { onSuccess: () => say(meta.name + '已' + (current ? '关闭' : '开启')), onError: fail })} /> : <div className="ops-setting-control">
        <input aria-label={meta.name} value={edit[key] ?? String(current ?? '')} disabled={isLocked || setFlag.isPending} onChange={(event) => setEdit((state) => ({ ...state, [key]: event.target.value }))} inputMode="numeric" min={minimum} max={maximum} style={{ ...fieldStyle, width: 90, textAlign: 'right', ...num }} />
        <small>{meta.unit}</small>
        <Btn disabled={isLocked || setFlag.isPending || !changed || invalid} onClick={() => setFlag.mutate({ key, value: Number(edit[key]) }, {
          onSuccess: () => { setEdit((state) => { const next = { ...state }; delete next[key]; return next }); say(meta.name + '已更新') }, onError: fail,
        })}>{setFlag.isPending && setFlag.variables?.key === key ? '保存中…' : '保存'}</Btn>
      </div>}
    </div>
  })

  return <div className="ops-page">
    <PageHead en="PLATFORM SETTINGS" title="开关与阈值" desc="按访问、容量和请求类型调整平台运行策略。" side={<Pill tone={values.maintenance ? 'warn' : 'ok'}>{values.maintenance ? '维护模式 · 业务只读' : '正常服务'}</Pill>} />
    <OpsTabs id="flags" value={tab} onChange={setTab} items={TABS} />
    <OpsTabPanel id="flags" name="access" active={tab}>
      <OpsSection title="访问与功能" desc="开关修改立即生效。模型连接与知识授权请在 Agent 页面配置。">{settings(['maintenance', 'registration', 'nativeToolsEnabled', 'aiEnabled'])}</OpsSection>
      <OpsSection title="容量与并发" desc="控制单次上传大小与同时执行的任务数量。较大的任务并发会增加内存占用。">{settings(['uploadMaxMb', 'requestConcurrency', 'exportConcurrency', 'passwordHashConcurrency'])}</OpsSection>
    </OpsTabPanel>
    <OpsTabPanel id="flags" name="limits" active={tab}>
      <OpsSection title="API 请求" desc="按来源 IP 或已登录会话限流。突发额度不能大于每分钟上限。">{settings(['apiRateLimitPerMinute', 'apiRateLimitBurst'])}</OpsSection>
      <OpsSection title="登录与账号" desc="分别限制登录、刷新、注册与密码操作。">{settings(['authLoginPerMinute', 'authRefreshPerMinute', 'authRegisterPerHour', 'authForgotPerHour', 'authResetPerHour'])}</OpsSection>
      <OpsSection title="材料、问答与导出" desc="按用户限制资源密集操作，避免个别账户持续占用处理资源。">{settings(['evidencePresignPerHour', 'evidenceDailyMb', 'aiPresignPerHour', 'agentPresignPerHour', 'knowledgePresignPerHour', 'aiBatchActionsPerHour', 'agentMessagesPerMinute', 'exportRequestsPerHour', 'knowledgeReprocessPerHour'])}</OpsSection>
      <div style={{ padding: '22px 0' }}><Note>部署上限：API {deployment?.apiRateLimitPerMinute ?? '—'} 次/分，突发 {deployment?.apiRateLimitBurst ?? '—'} 次，密码校验 {deployment?.passwordHashConcurrency ?? '—'} 并发。此页的配置不能超过这些上限。</Note></div>
    </OpsTabPanel>
    <OpsTabPanel id="flags" name="formats" active={tab}>
      <OpsSection title="允许的佐证格式" desc="班级方案可进一步收窄。平台还会检查文件内容与实际格式，至少保留一种格式。">
        <fieldset className="ops-form" disabled={setFlag.isPending || locked.has('evidenceAllowedFormats')}>
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>{EVIDENCE_FORMATS.map((format) => {
            const selected = selectedFormats.includes(format)
            return <label key={format} style={{ display: 'flex', alignItems: 'center', gap: 7, padding: '10px 12px', border: '1px solid var(--line)', minWidth: 80, fontSize: 12.5, cursor: 'pointer' }}><input type="checkbox" checked={selected} onChange={() => setFormatEdit(selected ? selectedFormats.filter((item) => item !== format) : [...selectedFormats, format])} />.{format}</label>
          })}</div>
          <OpsActions note={String(selectedFormats.length) + ' 种格式已选'}><Btn primary disabled={!formatEdit?.length || setFlag.isPending} onClick={() => setFlag.mutate({ key: 'evidenceAllowedFormats', value: formatEdit ?? [] }, { onSuccess: () => { setFormatEdit(null); say('平台佐证格式已更新') }, onError: fail })}>{setFlag.isPending ? '保存中…' : '保存格式策略'}</Btn><Btn disabled={formatEdit === null || setFlag.isPending} onClick={() => setFormatEdit(null)}>撤销改动</Btn></OpsActions>
        </fieldset>
      </OpsSection>
    </OpsTabPanel>
    <OpsTabPanel id="flags" name="branding" active={tab}><BrandingSettings /></OpsTabPanel>
  </div>
}
