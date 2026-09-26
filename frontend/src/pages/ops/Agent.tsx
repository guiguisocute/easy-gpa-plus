import { userErrorMessage } from '@/api/errorMessages'
/* 运维超管 · Agent 配置。 */

import { useState } from 'react'
import { Btn, Note, PageHead, Pill, Row, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, Toggle, TRow } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useAgentConfig, useOpsActions } from '@/api/queries'
import { useApp } from '@/stores/app'
import type { AIMaterialFormat } from '@/api/types'
import { ModelRouting } from './ModelRouting'
import PlatformKnowledge from './PlatformKnowledge'
import { OpsLoadError, OpsTabPanel, OpsTabs } from '@/components/OpsLayout'
import '@/styles/mobile-ops.css'

const GUARDS = [
  { name: '不参与结算', desc: '结算那四步是写死的，一步都不过模型。AI 出的东西永远只是草稿' },
  { name: '不代替审核', desc: '预审建议只是帮你把表单填上，结论还得审核人自己交' },
  { name: '不出分', desc: '模型只负责把事实和数量理出来，分还是照方案的规则算' },
  { name: '密钥不回传', desc: 'API Key 加密保存，页面只显示配置状态' },
]

const AGENT_LIMIT_FIELDS = [
  { key: 'agentMaxSteps' as const, name: '最大模型/工具步骤', unit: '步', min: 1, max: 64 },
  { key: 'agentTimeoutSeconds' as const, name: '单条总超时', unit: '秒', min: 5, max: 600 },
  { key: 'agentMaxAnswerKb' as const, name: '单条回答落库上限', unit: 'KB', min: 16, max: 512 },
  { key: 'agentToolResultKb' as const, name: '单次工具结果', unit: 'KB', min: 1, max: 256 },
  { key: 'agentToolScanMb' as const, name: '单条工具扫描总量', unit: 'MB', min: 1, max: 256 },
  { key: 'agentDailyMessages' as const, name: '每用户每日消息', unit: '条', min: 1, max: 10000 },
	{ key: 'agentAttachmentMaxFileMb' as const, name: '单张附件上限', unit: 'MB', min: 1, max: 50 },
	{ key: 'agentAttachmentMaxMessageMb' as const, name: '单条附件合计', unit: 'MB', min: 1, max: 100 },
	{ key: 'agentAttachmentDailyMb' as const, name: '每用户每日附件', unit: 'MB', min: 1, max: 10000 },
	{ key: 'agentAttachmentMaxCount' as const, name: '每条最多图片', unit: '张', min: 1, max: 12 },
  { key: 'knowledgeMaxFilesPerClass' as const, name: '每班文件别名', unit: '个', min: 1, max: 10000 },
  { key: 'knowledgeMaxStorageMbPerClass' as const, name: '每班原始存储', unit: 'MB', min: 1, max: 102400 },
]

const MATERIAL_LIMIT_FIELDS = [
  { key: 'materialDailyBatches' as const, name: '每用户每日批次', unit: '批', min: 1, max: 100 },
  { key: 'materialActiveBatches' as const, name: '每用户活动批次', unit: '批', min: 1, max: 20 },
  { key: 'materialMaxItems' as const, name: '每批图片与 PDF 页', unit: '张/页', min: 1, max: 100 },
  { key: 'materialMaxFileMb' as const, name: '单个材料大小', unit: 'MB', min: 1, max: 50 },
  { key: 'materialMaxBatchMb' as const, name: '每批材料总量', unit: 'MB', min: 1, max: 2048 },
  { key: 'materialMaxPdfPages' as const, name: '单份 PDF 页数', unit: '页', min: 1, max: 64 },
  { key: 'materialConcurrency' as const, name: '并行识别材料', unit: '份', min: 1, max: 8 },
  { key: 'materialRetentionDays' as const, name: '批次与原件保留', unit: '天', min: 1, max: 30 },
]

const MATERIAL_FORMATS: { key: AIMaterialFormat; label: string; note: string }[] = [
  { key: 'jpeg', label: 'JPEG', note: '.jpg / .jpeg' },
  { key: 'png', label: 'PNG', note: '.png' },
  { key: 'webp', label: 'WebP', note: '.webp' },
  { key: 'pdf', label: 'PDF', note: '.pdf' },
]

const DEFAULT_LIMITS = {
  agentMaxSteps: 24,
  agentTimeoutSeconds: 180,
  agentMaxAnswerKb: 128,
  agentToolResultKb: 32,
  agentToolScanMb: 64,
  agentDailyMessages: 50,
  materialDailyBatches: 5,
	materialActiveBatches: 3,
	agentAttachmentMaxFileMb: 5,
	agentAttachmentMaxMessageMb: 12,
	agentAttachmentDailyMb: 100,
	agentAttachmentMaxCount: 4,
  knowledgeMaxFilesPerClass: 1000,
  knowledgeMaxStorageMbPerClass: 1024,
  materialMaxItems: 100,
  materialMaxFileMb: 50,
  materialMaxBatchMb: 256,
  materialMaxPdfPages: 64,
  materialConcurrency: 2,
  materialRetentionDays: 7,
  materialAllowedFormats: ['jpeg', 'png', 'webp', 'pdf'] as AIMaterialFormat[],
}

export default function OpsAgent() {
  const say = useApp((s) => s.say)
  const agent = useAgentConfig()
  const { updateAgent, setFlag } = useOpsActions()
  const [limitEdit, setLimitEdit] = useState<Record<string, string>>({})
  const [formatEdit, setFormatEdit] = useState<AIMaterialFormat[] | null>(null)
  const [tab, setTab] = useState('models')

  if (agent.isLoading) return <div className="load-bar"><span /></div>
  if (!agent.data) return <OpsLoadError title="Agent 配置暂时无法读取" onRetry={() => void agent.refetch()} />

  const data = agent.data
  const limits = data?.limits ?? DEFAULT_LIMITS
  const enabled = !!data?.enabled
  const ready = !!data?.ready
  const secretKeyReady = data?.secretKeyReady !== false
  const materialFormats = formatEdit ?? limits.materialAllowedFormats
  const dirty = Object.keys(limitEdit).length > 0 || formatEdit !== null
  const fail = (e: unknown) => say(userErrorMessage(e))

  const save = () => {
    const payload: {
      agentMaxSteps?: number
      agentTimeoutSeconds?: number
      agentMaxAnswerKb?: number
      agentToolResultKb?: number
      agentToolScanMb?: number
      agentDailyMessages?: number
      materialDailyBatches?: number
      materialActiveBatches?: number
      knowledgeMaxFilesPerClass?: number
      knowledgeMaxStorageMbPerClass?: number
      materialMaxItems?: number
      materialMaxFileMb?: number
      materialMaxBatchMb?: number
      materialMaxPdfPages?: number
      materialConcurrency?: number
      materialRetentionDays?: number
      materialAllowedFormats?: AIMaterialFormat[]
	  agentAttachmentMaxFileMb?: number
	  agentAttachmentMaxMessageMb?: number
	  agentAttachmentDailyMb?: number
	  agentAttachmentMaxCount?: number
    } = {}
    for (const [key, value] of Object.entries(limitEdit)) {
      const parsed = Number(value)
      const range = [...AGENT_LIMIT_FIELDS, ...MATERIAL_LIMIT_FIELDS].find((item) => item.key === key)
      if (!/^\d+$/.test(value) || !Number.isInteger(parsed) || !range || parsed < range.min || parsed > range.max) {
        say(range ? `${range.name}必须填写 ${range.min}—${range.max} 的整数` : '使用限制必须填写有效整数')
        return
      }
      ;(payload as Record<string, string | number | undefined>)[key] = parsed
    }
    if (formatEdit !== null) {
      if (!formatEdit.length) {
        say('允许的材料格式至少保留一种')
        return
      }
      payload.materialAllowedFormats = formatEdit
    }
    updateAgent.mutate(payload, {
      onSuccess: () => {
        setLimitEdit({})
        setFormatEdit(null)
        say('使用限制已保存')
        updateAgent.reset()
      },
      onError: fail,
    })
  }

  const toggle = () => {
    if (!enabled && !ready) {
      say(data?.statusReason || '请先配置模型供应商与业务路由')
      return
    }
    setFlag.mutate(
      { key: 'aiEnabled', value: !enabled },
      { onSuccess: () => say(`AI 材料整理已${enabled ? '关闭' : '开启'}`), onError: fail },
    )
  }

  const toggleFlag = (key: 'knowledgeEnabled' | 'agentActionsEnabled' | 'knowledgeEgressEnabled', value: boolean, label: string) => {
    setFlag.mutate({ key, value: !value }, { onSuccess: () => say(`${label}已${value ? '关闭' : '开启'}`), onError: fail })
  }

  const routed = data?.routeStatus ?? {}
  const slots = [
    { slot: 'material.vision', name: '材料逐份识别', use: '读图片和 PDF 页，把文字、日期、等级、数量抠出来' },
    { slot: 'material.compose', name: '材料整批归组', use: '归组识别结果并生成候选申报草稿' },
    { slot: 'knowledge.ocr', name: '知识文件 OCR', use: '转换扫描件和图片型知识文件' },
    { slot: 'agent.text', name: 'Agent 文字问答', use: '搜索班级知识并生成文字回答' },
    { slot: 'agent.vision', name: 'Agent 图片问答', use: '处理上传图片、截图和含图对话' },
  ] as const

  return (
    <div className="ops-page">
      <PageHead
        en="AGENT"
        title="Agent 与知识库"
        desc="连接模型，设置材料处理与问答范围，管理平台公共知识。"
        side={
          <>
            <Pill tone={ready ? 'ok' : 'bad'}>{ready ? '配置就绪' : '配置未完成'}</Pill>
            <span style={{ display: 'flex', alignItems: 'center', gap: 9, fontSize: 12.5, color: 'var(--fg2)' }}>
              {enabled ? '已开启' : '已关闭'}
              <Toggle label="AI 材料整理" on={enabled} locked={setFlag.isPending} onClick={toggle} />
            </span>
          </>
        }
      />

      {!secretKeyReady && (
        <div style={{ marginBottom: 22 }}>
          <Note tone="warn">
            还没配加密密钥，现在存不了 API Key。
          </Note>
        </div>
      )}

      {!ready && data?.statusReason && (
        <div style={{ border: '1px solid var(--red)', background: 'var(--redBg)', padding: '13px 16px', marginBottom: 22, fontSize: 12.5, color: 'var(--fg2)' }}>
          当前不能启用：{data.statusReason}
        </div>
      )}

      <StatGrid cols={4}>
        <Stat en="运行状态" value={enabled ? '开启' : '关闭'} note={enabled ? '学生端入口已开放' : '不会接受新的 AI 批次'} tone={enabled ? 'var(--ok)' : undefined} />
        <Stat en="供应商" value={String(data?.providerCount ?? 0)} unit="个" note={(data?.providerCount ?? 0) > 0 ? '通过供应商列表管理连接' : '请先添加模型供应商'} />
        <Stat en="业务路由" value={String(data?.routeCount ?? 0)} unit="/ 5" note="每个槽位可使用不同厂商" />
		<Stat en="今日 Agent 用量" value={String(data?.usage?.dailyMessages ?? 0)} unit="条问答" note={`${data?.usage?.dailyAttachments ?? 0} 张附件 · ${f.bytes(data?.usage?.dailyAttachmentBytes ?? 0)}`} />
      </StatGrid>

      <OpsTabs id="agent-config" value={tab} onChange={setTab} items={[{ id: 'models', label: '模型连接' }, { id: 'materials', label: '材料整理' }, { id: 'knowledge', label: '知识问答' }, { id: 'library', label: '平台知识' }]} />
      <OpsTabPanel id="agent-config" name="models" active={tab}><div style={{ paddingTop: 26 }}><ModelRouting secretKeyReady={secretKeyReady} /></div></OpsTabPanel>

      <OpsTabPanel id="agent-config" name="materials" active={tab}>
      <Split cols="1fr 1fr">
        <SplitCol first>
          <div className="ops-inset-panel" style={{ padding: '4px 32px 30px 0' }}>
            <Sub title="AI 批量整理材料" note="管学生上传、逐张识别，以及暂存多久" />
            <div className="ops-limit-grid">
              {MATERIAL_LIMIT_FIELDS.map((item) => (
                <label key={item.key} style={{ display: 'flex', flexDirection: 'column', gap: 5, borderTop: '1px solid var(--line2)', padding: '11px 0' }}>
                  <span style={{ display: 'flex', justifyContent: 'space-between', gap: 8, fontSize: 11.5, color: 'var(--fg2)' }}><span>{item.name}</span><small>{item.unit}</small></span>
                  <input
                    type="number"
                    disabled={updateAgent.isPending}
                    min={item.min}
                    max={item.max}
                    value={limitEdit[item.key] ?? String(limits[item.key])}
                    onChange={(event) => setLimitEdit((current) => ({ ...current, [item.key]: event.target.value }))}
                    style={{ ...fieldStyle, fontVariantNumeric: 'tabular-nums' }}
                  />
                </label>
              ))}
            </div>
            <fieldset style={{ margin: '14px 0 0', padding: 0, border: 0 }}>
              <legend style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)', marginBottom: 8 }}>允许上传的格式</legend>
              <div className="ops-limit-grid" style={{ borderTop: '1px solid var(--line2)' }}>
                {MATERIAL_FORMATS.map((format) => (
                  <label key={format.key} style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '11px 0', cursor: 'pointer' }}>
                    <input
                      type="checkbox"
                      disabled={updateAgent.isPending}
                      aria-label={`允许 ${format.label}`}
                      checked={materialFormats.includes(format.key)}
                      onChange={() => setFormatEdit((current) => {
                        const selected = current ?? [...limits.materialAllowedFormats]
                        return selected.includes(format.key) ? selected.filter((value) => value !== format.key) : [...selected, format.key]
                      })}
                    />
                    <span style={{ display: 'flex', flexDirection: 'column', gap: 2 }}><strong style={{ fontSize: 12 }}>{format.label}</strong><small style={{ color: 'var(--fg3)', fontSize: 10.5 }}>{format.note}</small></span>
                  </label>
                ))}
              </div>
            </fieldset>
            <div style={{ marginTop: 14 }}>
              <Note>改完马上对新的上传生效。至少得留一种格式。</Note>
            </div>
          </div>
        </SplitCol>
        <SplitCol>
          <div className="ops-inset-panel" style={{ padding: '4px 0 30px 32px' }}>
            <Sub title="材料整理流水线" note="识图和归组各用各的模型" />
            <Row label="允许格式" value={MATERIAL_FORMATS.filter((format) => materialFormats.includes(format.key)).map((format) => format.label).join(' / ') || '尚未选择'} />
            <Row label="逐份识别" value={`${routed['material.vision']?.provider ?? '未配置'} / ${routed['material.vision']?.model ?? '—'} · 并行 ${limits.materialConcurrency}`} />
            <Row label="整批归组" value={`${routed['material.compose']?.provider ?? '未配置'} / ${routed['material.compose']?.model ?? '—'}`} />
            <Row label="产出范围" value="只创建待确认草稿" tone="var(--ok)" />
            <Row label="到期清理" value={`${limits.materialRetentionDays} 天后清理未采用原件`} />
          </div>
        </SplitCol>
      </Split>
      </OpsTabPanel>

      <OpsTabPanel id="agent-config" name="knowledge" active={tab}>
      <Split cols="1fr 1fr">
        <SplitCol first>
          <div className="ops-inset-panel" style={{ padding: '4px 32px 30px 0' }}>
            <Sub title="知识问答" note="三个开关彼此独立" />
            {[
              { key: 'knowledgeEnabled' as const, label: '知识内容处理与问答', value: !!data?.knowledgeEnabled, desc: '管转换、检索和问答。原件上传下载不受影响' },
              { key: 'agentActionsEnabled' as const, label: 'Agent 草稿操作', value: !!data?.agentActionsEnabled, desc: '可提草稿，两次确认后才生效。它自己不定案' },
              { key: 'knowledgeEgressEnabled' as const, label: '允许知识内容发送到模型端点', value: !!data?.knowledgeEgressEnabled, desc: '还得各班自己确认，两边都开才行' },
            ].map((item) => (
              <div key={item.key} style={{ display: 'flex', alignItems: 'center', gap: 13, borderTop: '1px solid var(--line2)', padding: '13px 0' }}>
                <Toggle label={item.label} on={item.value} locked={setFlag.isPending} size="sm" onClick={() => toggleFlag(item.key, item.value, item.label)} />
                <span style={{ display: 'flex', minWidth: 0, flex: 1, flexDirection: 'column', gap: 3 }}>
                  <strong style={{ fontSize: 12.5 }}>{item.label}</strong>
                  <small style={{ color: 'var(--fg3)', fontSize: 11.5, lineHeight: 1.6 }}>{item.desc}</small>
                </span>
              </div>
            ))}
            <div style={{ marginTop: 14 }}>
              <Note tone={data?.knowledgeReady ? 'idle' : 'warn'}>
                {data?.knowledgeReady ? '平台知识 Agent 的闸门已经就绪。每个班还得自己确认第三方处理授权。' : `当前未就绪：${data?.knowledgeStatusReason || '请补全模型配置和开关。'}`}
              </Note>
            </div>
          </div>
        </SplitCol>
        <SplitCol>
          <div className="ops-inset-panel" style={{ padding: '4px 0 30px 32px' }}>
            <Sub title="运行预算与班级配额" note="检索条数上限" />
            <div className="ops-limit-grid">
              {AGENT_LIMIT_FIELDS.map((item) => (
                <label key={item.key} style={{ display: 'flex', flexDirection: 'column', gap: 5, borderTop: '1px solid var(--line2)', padding: '11px 0' }}>
                  <span style={{ display: 'flex', justifyContent: 'space-between', gap: 8, fontSize: 11.5, color: 'var(--fg2)' }}><span>{item.name}</span><small>{item.unit}</small></span>
                  <input
                    type="number"
                    disabled={updateAgent.isPending}
                    min={item.min}
                    max={item.max}
                    value={limitEdit[item.key] ?? String(limits[item.key])}
                    onChange={(event) => setLimitEdit((current) => ({ ...current, [item.key]: event.target.value }))}
                    style={{ ...fieldStyle, fontVariantNumeric: 'tabular-nums' }}
                  />
                </label>
              ))}
            </div>
          </div>
        </SplitCol>
      </Split>
      </OpsTabPanel>

      {(tab === 'materials' || tab === 'knowledge') && <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', borderTop: '1px solid var(--line)', padding: '18px 0 30px' }}>
        <Btn primary disabled={!dirty || updateAgent.isPending} onClick={save}>
          {updateAgent.isPending ? '保存中…' : '保存使用限制'}
        </Btn>
        <Btn disabled={!dirty || updateAgent.isPending} onClick={() => { setLimitEdit({}); setFormatEdit(null) }}>撤销改动</Btn>
      </div>}

      {tab === 'models' && <>
      <div style={{ padding: '4px 0 0' }}>
        <Sub title="生效中的业务路由" note="当前实际在用的" />
        <Table cols="90px minmax(110px,1fr) minmax(150px,1.3fr) 110px minmax(190px,1.8fr)">
          <THead cells={['槽位', '用途', '模型', '来源', '说明']} />
          {slots.map((slot) => (
            <TRow key={slot.slot} cells={[
              <span key="slot" style={mono('10.5px', '.02em')}>{slot.slot}</span>,
              <span key="name" style={{ color: 'var(--fg)', fontWeight: 500 }}>{slot.name}</span>,
              <span key="model" style={mono('11px', '.02em')}>{routed[slot.slot]?.model ?? '—'}</span>,
              <span key="source">{routed[slot.slot]?.provider ?? '未配置'}</span>,
              <span key="use" style={{ fontSize: 12, color: 'var(--fg3)', textWrap: 'pretty' }}>{slot.use}</span>,
            ]} />
          ))}
        </Table>
      </div>

      <div style={{ padding: '30px 0' }}>
        <div style={{ marginBottom: 22 }}>
          <Note>运维台看不到班级资料、对话内容和学生材料。</Note>
        </div>
        <Sub title="不可修改的安全约束" />
        {GUARDS.map((guard) => (
          <div key={guard.name} style={{ display: 'flex', alignItems: 'flex-start', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0' }}>
            <span style={{ width: 7, height: 7, flex: 'none', marginTop: 5, background: 'var(--fg)' }} />
            <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0, flex: 1 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg)' }}>{guard.name}</span>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, textWrap: 'pretty' }}>{guard.desc}</span>
            </span>
          </div>
        ))}
      </div>

      </>}
      <OpsTabPanel id="agent-config" name="library" active={tab}><PlatformKnowledge /></OpsTabPanel>
    </div>
  )
}
