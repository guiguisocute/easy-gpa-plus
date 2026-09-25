import { useEffect, useRef, useState } from 'react'
import { api } from '@/api/client'
import type { ExportJob } from '@/api/types'
import { Btn, TextBtn } from '@/components/ui'
import { bytes } from '@/lib/format'
import { createDownloadTarget, ExportDownload as Download, type DownloadLink, type DownloadProgress } from '@/lib/exportDownload'

export function ExportDownload({ job, onBusyChange }: { job: ExportJob; onBusyChange: (busy: boolean) => void }) {
  const session = useRef<Download | null>(null)
  const controller = useRef<AbortController | null>(null)
  const pending = useRef<Promise<void> | null>(null)
  const [state, setState] = useState<'idle' | 'running' | 'paused' | 'complete'>('idle')
  const [progress, setProgress] = useState<DownloadProgress>({ received: 0, total: job.sizeBytes ?? 0, retrying: false })
  const [error, setError] = useState('')

  useEffect(() => {
    onBusyChange(state === 'running' || state === 'paused')
    return () => onBusyChange(false)
  }, [state, onBusyChange])

  useEffect(() => () => {
    controller.current?.abort()
    void pending.current?.finally(() => session.current?.abort().catch(() => {}))
  }, [])

  const getLink = (signal: AbortSignal) => api.get<DownloadLink>(`/admin/export/${job.jobId}`, signal)
  const start = () => {
    if (controller.current) return
    const active = new AbortController()
    controller.current = active
    setState('running')
    setError('')
    if (!session.current) setProgress({ received: 0, total: job.sizeBytes ?? 0, retrying: false })
    // Selecting the destination must happen in this user activation.
    const target = session.current ? null : createDownloadTarget(job.filename ?? `easygpa-${job.kind}.${job.kind === 'archive' || job.kind === 'college' ? 'zip' : 'xlsx'}`, job.sizeBytes ?? 0)
    pending.current = (async () => {
      try {
        if (target) session.current = new Download(await target, getLink)
        active.signal.throwIfAborted()
        await session.current!.run(active.signal, setProgress)
        setState('complete')
        session.current = null
      } catch (failure) {
        setState(session.current ? 'paused' : 'idle')
        if (!active.signal.aborted && !(failure instanceof Error && failure.name === 'AbortError')) {
          setError(failure instanceof TypeError ? '网络连接中断，请继续下载' : failure instanceof Error ? failure.message : '下载中断，请继续下载')
        }
        setProgress((previous) => ({ ...previous, received: session.current?.received ?? 0, retrying: false }))
      } finally {
        controller.current = null
      }
    })()
  }
  const cancel = async () => {
    controller.current?.abort()
    await pending.current
    await session.current?.abort().catch(() => {})
    session.current = null
    setState('idle')
    setError('')
    setProgress({ received: 0, total: job.sizeBytes ?? 0, retrying: false })
  }
  const direct = async () => {
    setError('')
    try {
      const link = await getLink(new AbortController().signal)
      const anchor = document.createElement('a')
      anchor.href = link.downloadUrl
      anchor.download = job.filename ?? ''
      anchor.click()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : '获取下载链接失败，请重试')
    }
  }
  const percent = progress.total ? Math.min(100, Math.floor(progress.received / progress.total * 100)) : 0
  return <div style={{ display: 'grid', gap: 10 }}>
    <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
      <Btn primary disabled={state === 'running'} onClick={start}>{state === 'running' ? '下载中…' : state === 'paused' ? '继续下载' : state === 'complete' ? '再次下载' : '下载文件'}</Btn>
      {state === 'running' && <TextBtn onClick={() => controller.current?.abort()}>暂停</TextBtn>}
      {state === 'paused' && <TextBtn onClick={() => { void cancel() }}>取消下载</TextBtn>}
      <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{job.sizeBytes ? bytes(job.sizeBytes) : ''}</span>
    </div>
    {(state !== 'idle') && <>
      <div role="progressbar" aria-label="文件下载进度" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} style={{ height: 6, background: 'var(--line2)', overflow: 'hidden' }}>
        <div style={{ height: '100%', width: `${percent}%`, background: state === 'complete' ? 'var(--ok)' : 'var(--red)', transition: 'width .15s' }} />
      </div>
      <div role="status" style={{ display: 'flex', justifyContent: 'space-between', gap: 12, fontSize: 12.5, color: 'var(--fg3)' }}>
        <span>{state === 'complete' ? '下载完成，文件已保存或交给浏览器保存' : state === 'paused' ? '已暂停 · 已下载部分已保留' : progress.retrying ? '连接中断或繁忙，正在自动重试…' : percent === 100 ? '正在保存文件…' : '正在下载'}</span>
        <span>{bytes(progress.received)} / {bytes(progress.total)} · {percent}%</span>
      </div>
    </>}
    {error && <div role="alert" style={{ fontSize: 12.5, color: 'var(--red)' }}>{error}</div>}
    <div style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.7 }}>
      分段下载，断线后自动重试；暂停后可从已完成的分段继续。下载期间请保留此页面。
      <div><TextBtn disabled={state === 'running' || state === 'paused'} onClick={() => { void direct() }}>浏览器直接下载</TextBtn> · 进度可在浏览器下载列表查看</div>
    </div>
  </div>
}
