/* 运维超管 · 开关与阈值。AI 开关也会在 Agent 配置页展示；后端只有在
   URL、模型和密钥均可安全解析时才允许开启。 */

import BrandingSettings from './Branding'
import { useState } from 'react'
import { Btn, Note, PageHead, Pill, Stat, StatGrid, Sub, Toggle } from '@/components/ui'
import { fieldStyle, mono, num } from '@/lib/style'
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

export default function OpsFlags() {
  const say = useApp((s) => s.say)
  const flags = useFlags()
  const { setFlag } = useOpsActions()
  const [edit, setEdit] = useState<Record<string, string>>({})
	const [formatEdit, setFormatEdit] = useState<string[] | null>(null)

  if (flags.isLoading) return <div className="load-bar"><span /></div>

  const values = flags.data?.flags ?? {}
  const locked = new Set(flags.data?.locked ?? [])
  const deployment = flags.data?.deploymentLimits
  const keys = Object.keys(META).filter((k) => k in values || locked.has(k))
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '修改失败')

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="FLAGS"
        title="开关与阈值"
        desc="全局开关、限流和维护"
        side={<Pill tone={values.maintenance ? 'warn' : 'ok'}>{values.maintenance ? '维护模式中' : '正常服务'}</Pill>}
      />

      <BrandingSettings />
      <StatGrid cols={4}>
        <Stat en="维护模式" value={values.maintenance ? '开启' : '关闭'} note="开启后业务只读" tone={values.maintenance ? 'var(--red)' : undefined} />
        <Stat en="开放注册" value={values.registration ? '开启' : '关闭'} note="关闭后新账号无法建立" />
        <Stat en="佐证上限" value={String(values.uploadMaxMb ?? '—')} unit="MB" note="单份文件的硬上限" />
        <Stat en="API 限流" value={String(values.apiRateLimitPerMinute ?? '—')} unit="次/分" note="按 IP 或登录会话计数" />
      </StatGrid>

      <div style={{ padding: '26px 0 0' }}>
        <Sub title="开关" note="开关点一下就生效，数字要点保存" />
        {keys.map((key) => {
          const meta = META[key]
          const isLocked = locked.has(key)
          const current = values[key]
          const minimum = meta.min ?? 1
          let maximum = meta.max ?? 10000
          if (key === 'apiRateLimitPerMinute' && deployment) maximum = Math.min(maximum, deployment.apiRateLimitPerMinute)
          if (key === 'apiRateLimitBurst' && deployment) maximum = Math.min(maximum, deployment.apiRateLimitBurst)
          if (key === 'passwordHashConcurrency' && deployment) maximum = Math.min(maximum, deployment.passwordHashConcurrency)
          return (
            <div key={key} style={{ display: 'flex', alignItems: 'center', gap: 16, borderTop: '1px solid var(--line2)', padding: '15px 0', flexWrap: 'wrap' }}>
              <span style={{ ...mono('11px', '.06em'), width: 160, flex: 'none' }}>{key}</span>
              <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 200, flex: 1 }}>
                <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>{meta.name}</span>
                <span style={{ fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>{meta.desc}</span>
              </span>

              {meta.kind === 'bool' ? (
                <Toggle
                  on={!!current}
                  locked={isLocked}
                  onClick={() => {
                    if (isLocked) return say('此项由部署配置锁定')
                    setFlag.mutate(
                      { key, value: !current },
                      { onSuccess: () => say(`${meta.name} 已${current ? '关闭' : '开启'}`), onError: fail },
                    )
                  }}
                />
              ) : (
                <span style={{ display: 'flex', alignItems: 'center', gap: 10, flex: 'none' }}>
                  <input
                    value={edit[key] ?? String(current ?? '')}
                    onChange={(e) => setEdit((s) => ({ ...s, [key]: e.target.value }))}
                    inputMode="numeric"
                    min={minimum}
                    max={maximum}
                    style={{ ...fieldStyle, width: 90, textAlign: 'right', border: '1px solid var(--line)', padding: '7px 10px', ...num }}
                  />
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)', width: 40 }}>{meta.unit}</span>
                  <Btn
                    disabled={
                      edit[key] === undefined ||
                      edit[key] === String(current) ||
                      !/^\d+$/.test(edit[key]) ||
                      Number(edit[key]) < minimum ||
                      Number(edit[key]) > maximum
                    }
                    onClick={() =>
                      setFlag.mutate(
                        { key, value: Number(edit[key]) },
                        {
                          onSuccess: () => {
                            setEdit((s) => {
                              const next = { ...s }
                              delete next[key]
                              return next
                            })
                            say(`${meta.name} 已改为 ${edit[key]} ${meta.unit ?? ''}`)
                          },
                          onError: fail,
                        },
                      )
                    }
                  >
                    保存
                  </Btn>
                </span>
              )}
            </div>
          )
        })}
      </div>

	  <div style={{ paddingTop: 26 }}>
		<Sub title="平台允许的佐证格式" note="方案只能再收窄。上传时会核对文件头" />
		<div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', borderTop: '1px solid var(--line2)', padding: '14px 0' }}>
		  {EVIDENCE_FORMATS.map((format) => {
			const selected = formatEdit ?? (Array.isArray(values.evidenceAllowedFormats) ? values.evidenceAllowedFormats : EVIDENCE_FORMATS)
			const on = selected.includes(format)
			return <label key={format} style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '6px 9px', border: '1px solid var(--line)', fontSize: 12, cursor: 'pointer' }}>
			  <input type="checkbox" checked={on} onChange={() => setFormatEdit(on ? selected.filter((item) => item !== format) : [...selected, format])} /> .{format}
			</label>
		  })}
		</div>
		<Btn primary disabled={formatEdit === null || formatEdit.length === 0 || setFlag.isPending} onClick={() => setFlag.mutate({ key: 'evidenceAllowedFormats', value: formatEdit ?? [] }, { onSuccess: () => { setFormatEdit(null); say('平台佐证格式策略已更新') }, onError: fail })}>保存格式策略</Btn>
	  </div>

      <div style={{ paddingTop: 26 }}>
        <Note>
          能填多大受部署时的上限卡着：API 总速率 {deployment?.apiRateLimitPerMinute ?? '—'} 次/分，
          突发额度 {deployment?.apiRateLimitBurst ?? '—'}，密码校验并发上限 {deployment?.passwordHashConcurrency ?? '—'}。
        </Note>
      </div>
    </div>
  )
}
