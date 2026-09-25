import { useState } from 'react'
import { ApiError } from '@/api/client'
import { useAIProviders, useModelRoutes, useOpsActions } from '@/api/queries'
import type { AIProvider, AIProviderCapabilities, ModelPurpose, ModelRoute } from '@/api/types'
import { Btn, Note, Pill, Sub, Table, THead, TRow } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import { useApp } from '@/stores/app'
import '@/styles/mobile-ops.css'

const PURPOSES: {
  purpose: ModelPurpose
  name: string
  use: string
  vision: boolean
}[] = [
  { purpose: 'material.vision', name: '材料逐份识别', use: '读图片和 PDF 页，把能归组的事实抠出来', vision: true },
  { purpose: 'material.compose', name: '材料整批归组', use: '把识别结果整理为候选申报草稿', vision: false },
  { purpose: 'knowledge.ocr', name: '知识文件 OCR', use: '转换扫描件和图片型知识文件', vision: true },
  { purpose: 'agent.text', name: 'Agent 文字问答', use: '搜索班级知识并生成文字回答', vision: false },
  { purpose: 'agent.vision', name: 'Agent 图片问答', use: '处理用户附件、截图及含图对话', vision: true },
]

const EMPTY_CAPABILITIES: AIProviderCapabilities = { json: true, stream: true, vision: false, models: false }

export function ModelRouting({ secretKeyReady }: { secretKeyReady: boolean }) {
  const say = useApp((state) => state.say)
  const providers = useAIProviders()
  const routes = useModelRoutes()
  const actions = useOpsActions()
  const [editing, setEditing] = useState<AIProvider | 'new' | null>(null)
  const fail = (error: unknown) => say(error instanceof ApiError ? error.message : '操作失败')

  const providerItems = providers.data?.items ?? []
  const routeItems = routes.data?.items ?? []

  return (
    <section className="ops-model-routing" style={{ paddingBottom: 32 }}>
      <Sub title="模型供应商" note="管连接地址、API Key 和传输策略" />
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 14, marginBottom: 14, flexWrap: 'wrap' }}>
        <Note>为每项功能选择供应商与模型。</Note>
        <Btn primary disabled={editing !== null} onClick={() => setEditing('new')}>添加供应商</Btn>
      </div>

      {editing !== null && (
        <ProviderEditor
          key={editing === 'new' ? 'new' : `${editing.id}:${editing.revision}`}
          provider={editing === 'new' ? undefined : editing}
          secretKeyReady={secretKeyReady}
          onCancel={() => setEditing(null)}
          onSaved={() => { setEditing(null); say(editing === 'new' ? '模型供应商已添加' : '模型供应商已更新') }}
          onError={fail}
        />
      )}

      {providers.isLoading ? <div className="load-bar"><span /></div> : (
        <Table cols="minmax(130px,1fr) minmax(210px,1.8fr) 150px 105px 210px">
          <THead cells={['供应商', 'OpenAI 兼容端点', '能力', '状态', '操作']} />
          {providerItems.map((provider) => (
            <TRow key={provider.id} cells={[
              <span key="name" style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                <strong>{provider.name}</strong>
                <small style={{ color: 'var(--fg3)' }}>超时 {provider.timeoutSeconds}s · 重试 {provider.maxRetries}</small>
              </span>,
              <span key="url" style={{ ...mono('10.5px', '.01em'), overflowWrap: 'anywhere' }}>{provider.baseUrl}</span>,
              <span key="caps" style={{ display: 'flex', gap: 5, flexWrap: 'wrap' }}>
                {provider.capabilities.vision && <Pill tone="ok">视觉</Pill>}
                {provider.capabilities.stream && <Pill>流式</Pill>}
                {provider.capabilities.json && <Pill>JSON</Pill>}
              </span>,
              <span key="status" style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                <Pill tone={provider.enabled && provider.apiKeySet ? 'ok' : 'bad'}>{provider.enabled ? '已启用' : '已停用'}</Pill>
                <small style={{ color: 'var(--fg3)' }}>{provider.apiKeySet ? 'Key 已保存' : '无 Key'}</small>
              </span>,
              <span key="actions" style={{ display: 'flex', gap: 7, flexWrap: 'wrap' }}>
                <Btn onClick={() => setEditing(provider)}>编辑</Btn>
                <Btn
                  disabled={!provider.apiKeySet || actions.clearAIProviderKey.isPending}
                  onClick={() => {
                    if (!window.confirm(`清除“${provider.name}”的 API Key 并停用它？`)) return
                    actions.clearAIProviderKey.mutate(provider.id, { onSuccess: () => say('供应商 Key 已清除并停用'), onError: fail })
                  }}
                >清除 Key</Btn>
                <Btn
                  danger
                  disabled={actions.deleteAIProvider.isPending}
                  onClick={() => {
                    if (!window.confirm(`删除供应商“${provider.name}”？使用它的路由必须先改绑或删除。`)) return
                    actions.deleteAIProvider.mutate(provider.id, { onSuccess: () => say('模型供应商已删除'), onError: fail })
                  }}
                >删除</Btn>
              </span>,
            ]} />
          ))}
        </Table>
      )}
      {!providers.isLoading && providerItems.length === 0 && (
        <div style={{ borderTop: '1px solid var(--line)', padding: '18px 0', color: 'var(--fg3)', fontSize: 12.5 }}>尚未添加供应商。</div>
      )}

      <div style={{ marginTop: 34 }}>
        <Sub title="业务模型路由" note="为各项功能分配模型" />
        {routes.isLoading ? <div className="load-bar"><span /></div> : (
          <div style={{ borderTop: '1px solid var(--line)' }}>
            {PURPOSES.map((definition) => {
              const route = routeItems.find((item) => item.purpose === definition.purpose)
              return (
                <RouteEditor
                  key={`${definition.purpose}:${route?.revision ?? 0}`}
                  definition={definition}
                  route={route}
                  providers={providerItems}
                  onSaved={() => say(`${definition.name}路由已保存`)}
                  onDeleted={() => say(`${definition.name}路由已删除`)}
                  onTested={(duration) => say(`${definition.name}连接与能力正常 · ${duration} ms`)}
                  onError={fail}
                />
              )
            })}
          </div>
        )}
      </div>
    </section>
  )
}

function ProviderEditor({
  provider, secretKeyReady, onCancel, onSaved, onError,
}: {
  provider?: AIProvider
  secretKeyReady: boolean
  onCancel: () => void
  onSaved: () => void
  onError: (error: unknown) => void
}) {
  const actions = useOpsActions()
  const [name, setName] = useState(provider?.name ?? '')
  const [baseUrl, setBaseUrl] = useState(provider?.baseUrl ?? '')
  const [apiKey, setApiKey] = useState('')
  const [timeout, setTimeoutValue] = useState(String(provider?.timeoutSeconds ?? 90))
  const [retries, setRetries] = useState(String(provider?.maxRetries ?? 0))
  const [enabled, setEnabled] = useState(provider?.enabled ?? true)
  const [capabilities, setCapabilities] = useState<AIProviderCapabilities>(provider?.capabilities ?? EMPTY_CAPABILITIES)
  const pending = actions.createAIProvider.isPending || actions.updateAIProvider.isPending

  const save = () => {
    const timeoutSeconds = Number(timeout)
    const maxRetries = Number(retries)
    if (!name.trim() || !baseUrl.trim() || !Number.isInteger(timeoutSeconds) || !Number.isInteger(maxRetries)) {
      onError(new Error('供应商名称、URL、超时和重试次数都得填上'))
      return
    }
    if (!provider && !apiKey) {
      onError(new Error('新建供应商必须填 API Key'))
      return
    }
    const shared = { name: name.trim(), baseUrl: baseUrl.trim(), timeoutSeconds, maxRetries, capabilities, enabled }
    if (provider) {
      actions.updateAIProvider.mutate({ id: provider.id, ...shared, ...(apiKey ? { apiKey } : {}) }, { onSuccess: onSaved, onError })
    } else {
      actions.createAIProvider.mutate({ ...shared, apiKey }, { onSuccess: onSaved, onError })
    }
  }

  return (
    <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: 18, marginBottom: 18 }}>
      <div className="ops-provider-grid">
        <Field label="供应商名称"><input value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 MiMo / DeepSeek / 校内网关" style={fieldStyle} /></Field>
        <Field label="OpenAI 兼容 /v1 URL"><input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="https://provider.example/v1" spellCheck={false} style={fieldStyle} /></Field>
        <Field label="API Key"><input type="password" autoComplete="new-password" value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder={provider?.apiKeySet ? '已配置 · 留空表示不修改' : '粘贴 API Key'} style={fieldStyle} /></Field>
        <div className="ops-parameter-grid" style={{ gap: 12 }}>
          <Field label="请求超时（秒）"><input type="number" min={5} max={600} value={timeout} onChange={(event) => setTimeoutValue(event.target.value)} style={fieldStyle} /></Field>
          <Field label="自动重试（次）"><input type="number" min={0} max={5} value={retries} onChange={(event) => setRetries(event.target.value)} style={fieldStyle} /></Field>
        </div>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 18, flexWrap: 'wrap', borderTop: '1px solid var(--line2)', marginTop: 15, paddingTop: 14 }}>
        {([
          ['json', 'JSON 响应'], ['stream', '流式输出'], ['vision', '图片输入'], ['models', '模型列表接口'],
        ] as const).map(([key, label]) => (
          <label key={key} style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 12, cursor: 'pointer' }}>
            <input type="checkbox" checked={capabilities[key]} onChange={() => setCapabilities((current) => ({ ...current, [key]: !current[key] }))} /> {label}
          </label>
        ))}
        <label style={{ display: 'flex', alignItems: 'center', gap: 7, marginLeft: 'auto', fontSize: 12, cursor: 'pointer' }}>
          <input type="checkbox" checked={enabled} onChange={() => setEnabled((value) => !value)} /> 启用供应商
        </label>
      </div>
      <div style={{ display: 'flex', gap: 9, marginTop: 16, flexWrap: 'wrap' }}>
        <Btn primary disabled={pending || (!secretKeyReady && (!provider || !!apiKey))} onClick={save}>{pending ? '保存中…' : '保存供应商'}</Btn>
        <Btn disabled={pending} onClick={onCancel}>取消</Btn>
      </div>
      {!secretKeyReady && <div style={{ marginTop: 12 }}><Note tone="warn">服务端未配置 AI_CONFIG_SECRET_KEY，现在加不了新供应商，也换不了 Key。</Note></div>}
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <label style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0 }}><span style={{ fontSize: 11.5, color: 'var(--fg2)' }}>{label}</span>{children}</label>
}

function RouteEditor({
  definition, route, providers, onSaved, onDeleted, onTested, onError,
}: {
  definition: (typeof PURPOSES)[number]
  route?: ModelRoute
  providers: AIProvider[]
  onSaved: () => void
  onDeleted: () => void
  onTested: (duration: number) => void
  onError: (error: unknown) => void
}) {
  const actions = useOpsActions()
  /* 测试结果留在行里，不只发一条 2.6 秒的 toast：失败原因是一整句英文诊断
     （DNS、被拦的地址、供应商回包），一闪而过等于没给。 */
  const [testError, setTestError] = useState('')
  const [providerId, setProviderId] = useState(route?.providerId ?? '')
  const [model, setModel] = useState(route?.model ?? '')
	const [temperature, setTemperature] = useState(route?.parameters.temperature === undefined ? '' : String(route.parameters.temperature))
	const [maxTokens, setMaxTokens] = useState(route?.parameters.maxTokens === undefined ? '' : String(route.parameters.maxTokens))
  const candidates = providers.filter((provider) => !definition.vision || provider.capabilities.vision)
  const selected = providers.find((provider) => provider.id === providerId)
	const originalTemperature = route?.parameters.temperature === undefined ? '' : String(route.parameters.temperature)
	const originalMaxTokens = route?.parameters.maxTokens === undefined ? '' : String(route.parameters.maxTokens)
	const dirty = providerId !== (route?.providerId ?? '') || model !== (route?.model ?? '') || temperature !== originalTemperature || maxTokens !== originalMaxTokens
	const parametersValid = (temperature === '' || (!Number.isNaN(Number(temperature)) && Number(temperature) >= 0 && Number(temperature) <= 2)) &&
	  (maxTokens === '' || (/^\d+$/.test(maxTokens) && Number(maxTokens) >= 1 && Number(maxTokens) <= 131072))
	const save = () => {
		const parameters: Record<string, unknown> = {}
		if (temperature !== '') parameters.temperature = Number(temperature)
		if (maxTokens !== '') parameters.maxTokens = Number(maxTokens)
		actions.updateModelRoute.mutate({ purpose: definition.purpose, providerId, model: model.trim(), parameters }, { onSuccess: onSaved, onError })
	}

  return (
    <div className="ops-route-editor">
      <span className="ops-route-title" style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <strong style={{ fontSize: 12.5 }}>{definition.name}</strong>
        <small style={{ ...mono('10px', '.03em'), color: 'var(--fg3)' }}>{definition.purpose}</small>
        <small style={{ color: 'var(--fg3)', lineHeight: 1.5 }}>{definition.use}</small>
      </span>
      <Field label="供应商">
        <select value={providerId} onChange={(event) => setProviderId(event.target.value)} style={fieldStyle}>
          <option value="">选择供应商</option>
          {candidates.map((provider) => <option key={provider.id} value={provider.id} disabled={!provider.enabled || !provider.apiKeySet}>{provider.name}{provider.enabled ? '' : '（已停用）'}</option>)}
        </select>
      </Field>
      <Field label="模型 ID">
        <input value={model} onChange={(event) => setModel(event.target.value)} placeholder="厂商模型 ID" spellCheck={false} style={fieldStyle} />
        <small style={{ color: selected?.enabled && selected.apiKeySet ? 'var(--fg3)' : 'var(--red)', overflowWrap: 'anywhere' }}>
          {selected ? `${selected.name} · ${selected.baseUrl}` : route ? route.providerName : '尚未绑定'}
        </small>
      </Field>
	  <span className="ops-parameter-grid">
		<Field label="温度 0—2"><input type="number" min={0} max={2} step={0.1} value={temperature} onChange={(event) => setTemperature(event.target.value)} placeholder="业务默认" style={fieldStyle} /></Field>
		<Field label="最大 Tokens"><input type="number" min={1} max={131072} step={1} value={maxTokens} onChange={(event) => setMaxTokens(event.target.value)} placeholder="业务默认" style={fieldStyle} /></Field>
	  </span>
      <span className="ops-route-actions">
		<Btn primary disabled={!dirty || !providerId || !model.trim() || !parametersValid || actions.updateModelRoute.isPending} onClick={save}>保存</Btn>
        <Btn
          disabled={!route || actions.testModelRoute.isPending}
          onClick={() => {
            setTestError('')
            actions.testModelRoute.mutate(definition.purpose, {
              onSuccess: (result) => onTested(result.durationMs),
              onError: (error) => { setTestError(testReason(error)); onError(error) },
            })
          }}
        >测试</Btn>
        <Btn danger disabled={!route || actions.deleteModelRoute.isPending} onClick={() => {
          if (!window.confirm(`删除“${definition.name}”路由？对应功能将不可用。`)) return
          actions.deleteModelRoute.mutate(definition.purpose, { onSuccess: onDeleted, onError })
        }}>删除</Btn>
      </span>
      {testError && (
        <span style={{ gridColumn: '1 / -1' }}>
          <Note tone="bad">
            <strong style={{ display: 'block', marginBottom: 5 }}>测试失败</strong>
            <span style={{ ...mono('11px', '.01em'), color: 'var(--fg2)', overflowWrap: 'anywhere', userSelect: 'text' }}>{testError}</span>
          </Note>
        </span>
      )}
    </div>
  )
}

/* 后端把擦过密钥的原始错误放在 detail.reason 里。拿不到就退回 message —— 那是
   一句"模型路由连接或能力测试失败"，不解释原因，但总比空着强。 */
function testReason(error: unknown) {
  if (!(error instanceof ApiError)) return '操作失败'
  const detail = error.detail && typeof error.detail === 'object' ? error.detail as Record<string, unknown> : {}
  const reason = typeof detail.reason === 'string' ? detail.reason.trim() : ''
  return reason || error.message
}
