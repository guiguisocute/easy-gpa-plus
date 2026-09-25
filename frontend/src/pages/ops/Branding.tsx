import { useRef, useState } from 'react'
import { Btn, Note, Sub } from '@/components/ui'
import { FileDropzone } from '@/components/FileDropzone'
import ParticleEmblem, { type EmblemStatus } from '@/components/ParticleEmblem'
import { crestSource, useBranding, useUpdateBranding } from '@/api/branding'
import { sampleEmblem } from '@/particle/EmblemSampler'
import { DEFAULT_CONFIG, type EmblemParticleConfig } from '@/particle/config'

const PREVIEW: Partial<EmblemParticleConfig> = { centerX: 0.5, centerY: 0.5, sizeW: 0.7, sizeH: 0.8, textGuardWidth: 0, spacing: 4, minEmblemCells: 16 }

export default function BrandingSettings() {
  const branding = useBranding()
  const update = useUpdateBranding()
  const input = useRef<HTMLInputElement>(null)
  const selecting = useRef(false)
  const [reading, setReading] = useState(false)
  const [status, setStatus] = useState<EmblemStatus>('loading')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const svg = branding.data?.svg ?? ''
  const busy = reading || update.isPending
  async function select(file?: File) {
    if (!file || selecting.current) return
    selecting.current = true; setReading(true); setError(''); setMessage('正在检查并保存校徽…')
    try {
      if (!file.name.toLowerCase().endsWith('.svg') || file.size > 128 * 1024) throw new Error('请选择不超过 128 KB 的 SVG 文件')
      const text = await file.text()
      const mask = await sampleEmblem(80, 80, { ...DEFAULT_CONFIG, src: crestSource(text) })
      if (!mask.data.some((ink) => ink > 0)) throw new Error('没有检测到可显示的图案，请检查 SVG 的画布范围与可见内容')
      await update.mutateAsync(text)
      setMessage('校徽已保存到服务器。首页每 8 秒轮换图案，已打开的页面会在 30 秒内更新。')
    } catch (e) {
      setMessage(''); setError(e instanceof Error ? e.message : '校徽保存失败，请重试')
    } finally { selecting.current = false; setReading(false) }
  }
  async function remove() {
    setError(''); setMessage('')
    try { await update.mutateAsync(''); setMessage('校徽已移除，首页仅展示网页图标。') }
    catch (e) { setError(e instanceof Error ? e.message : '移除失败，请重试') }
  }
  return <section style={{ padding: '26px 0' }}>
    <Sub title="登录页校徽" note="粒子每 8 秒在网页图标与校徽间重组；开启减少动态效果时静态显示校徽" />
    <div style={{ position: 'relative', height: 260, overflow: 'hidden', background: 'var(--sub)', marginBottom: 14 }}>
      <ParticleEmblem src={svg ? crestSource(svg) : undefined} config={PREVIEW} onStatus={setStatus} />
    </div>
    <p style={{ color: 'var(--fg2)', fontSize: 12 }}>{branding.isPending ? '正在读取已保存的校徽…' : svg ? '当前已保存校徽，选择新文件即可替换。' : '当前未保存校徽，仅展示网页图标。'}</p>
    <FileDropzone title="上传并保存 SVG 校徽" hint="128 KB 以内，选中后自动保存。支持矢量形状和内嵌 PNG/JPEG；文字请转轮廓，不支持脚本、样式表或外部资源。"
      disabled={busy} onDragOver={(e) => e.preventDefault()} onDrop={(e) => { e.preventDefault(); if (!busy) void select(e.dataTransfer.files[0]) }}
      actions={<Btn disabled={busy} onClick={() => input.current?.click()}>{busy ? '正在保存…' : '选择 SVG 文件'}</Btn>} />
    <input ref={input} type="file" accept=".svg,image/svg+xml" aria-label="选择 SVG 校徽" hidden onChange={(e) => { void select(e.target.files?.[0]); e.target.value = '' }} />
    <div style={{ display: 'flex', gap: 10, padding: '14px 0' }}>
      <Btn disabled={!svg || busy} onClick={() => void remove()}>移除校徽</Btn>
      <Btn disabled={busy || branding.isFetching} onClick={() => void branding.refetch()}>刷新保存状态</Btn>
    </div>
    {message && <div role="status"><Note>{message}</Note></div>}
    {(error || branding.error) && <div role="alert"><Note>{error || branding.error?.message}</Note></div>}
    {!busy && svg && (status === 'error' || status === 'empty') && <div role="alert"><Note>已保存的校徽未能生成图案，请检查 SVG 并重新上传。</Note></div>}
  </section>
}
