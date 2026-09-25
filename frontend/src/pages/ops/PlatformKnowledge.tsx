import { readableErrorMessage, userErrorMessage } from '@/api/errorMessages'
import { useEffect, useRef, useState } from 'react'
import { Download, Eye, FileUp, RefreshCw, Trash2 } from 'lucide-react'
import {
  uploadPlatformKnowledgeDocument,
  usePlatformKnowledge,
  usePlatformKnowledgeActions,
  usePlatformKnowledgeDocument,
} from '@/api/queries'
import type { PlatformKnowledgeDocument, PlatformKnowledgeRole } from '@/api/types'
import { Btn, Empty, Note, Pill, Row, Stat, StatGrid, Sub, Table, THead, TRow } from '@/components/ui'
import { FileDropzone } from '@/components/FileDropzone'
import * as f from '@/lib/format'
import { mono } from '@/lib/style'
import { useApp } from '@/stores/app'

const ROLE_LABEL: Record<PlatformKnowledgeRole, string> = {
  student: '学生',
  group: '综测小组',
  class_admin: '班级管理员',
}

const STATUS_LABEL: Record<string, string> = {
  uploading: '上传中', queued: '排队中', processing: '转换中', ready: '可检索', partial: '部分可用',
  unsupported: '格式不支持', failed: '转换失败', superseded: '已替代', retired: '已退役', deleted: '已删除',
}

function statusTone(status: string): 'ok' | 'warn' | 'bad' | 'idle' {
  if (status === 'ready' || status === 'partial') return 'ok'
  if (status === 'failed' || status === 'unsupported') return 'bad'
  if (status === 'processing' || status === 'queued' || status === 'uploading') return 'warn'
  return 'idle'
}

function sourceLabel(doc: PlatformKnowledgeDocument) {
  return doc.sourceKind === 'builtin' ? '内置' : '自定义'
}

export default function PlatformKnowledge() {
  const say = useApp((s) => s.say)
  const state = usePlatformKnowledge()
  const actions = usePlatformKnowledgeActions()
  const inputRef = useRef<HTMLInputElement>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState(0)
  const [dragging, setDragging] = useState(false)
  const [name, setName] = useState('')
  const [roles, setRoles] = useState<PlatformKnowledgeRole[]>([])
  const [enabled, setEnabled] = useState(false)

  const selected = state.data?.documents.find((doc) => doc.id === selectedId) ?? null
  const detail = usePlatformKnowledgeDocument(selectedId)

  useEffect(() => {
    if (!selectedId && state.data?.documents[0]) setSelectedId(state.data.documents[0].id)
  }, [selectedId, state.data?.documents])

  useEffect(() => {
    const doc = detail.data
    if (!doc) return
    setName(doc.displayName)
    setRoles(doc.allowedRoles)
    setEnabled(doc.enabled)
  }, [detail.data])

  const fail = (error: unknown) => say(userErrorMessage(error, '平台知识操作失败，请稍后重试'))

  const choose = (doc: PlatformKnowledgeDocument) => {
    setSelectedId(doc.id)
    setName(doc.displayName)
    setRoles(doc.allowedRoles)
    setEnabled(doc.enabled)
  }

  const upload = async (file: File) => {
    setUploading(true)
    setUploadProgress(0)
    try {
      await uploadPlatformKnowledgeDocument(file, setUploadProgress)
      say('已上传，正在排队转换。默认停用，预览过再手动发布')
      await state.refetch()
    } catch (error) {
      fail(error)
    } finally {
      setUploading(false)
      setUploadProgress(0)
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  const save = () => {
    if (!selected) return
    if (!roles.length) {
      say('至少选择一个业务角色')
      return
    }
    actions.update.mutate(
      { id: selected.id, displayName: selected.sourceKind === 'custom' ? name : undefined, allowedRoles: roles, enabled },
      { onSuccess: () => say(enabled ? '平台知识已发布' : '平台知识已停用'), onError: fail },
    )
  }

  const download = () => {
    const doc = detail.data
    if (!doc) return
    if (doc.downloadUrl) {
      window.open(doc.downloadUrl, '_blank', 'noopener,noreferrer')
      return
    }
    if (doc.content !== undefined) {
      const url = URL.createObjectURL(new Blob([doc.content], { type: 'text/markdown;charset=utf-8' }))
      const link = document.createElement('a')
      link.href = url
      link.download = doc.filename || `${doc.displayName}.md`
      document.body.append(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
    }
  }

  if (state.isLoading) return <div className="load-bar"><span /></div>
  if (state.isError) return <Note tone="warn">平台知识加载失败：{userErrorMessage(state.error, '请稍后重试')}</Note>
  const data = state.data
  if (!data) return null

  return (
    <section style={{ paddingTop: 30 }}>
      <Sub title="平台知识" note="全站产品说明，不含班级文件和学生数据" />
      <FileDropzone
        active={dragging}
        disabled={uploading}
        icon={<FileUp size={26} strokeWidth={1.3} aria-hidden="true" />}
        title={uploading ? `上传中 ${uploadProgress}%` : dragging ? '松手就开始上传' : '将文件拖至此区域'}
        hint="或点击下方按钮上传 · Markdown / 文本 / PDF / Office / 图片 · 一次一份"
        actions={<Btn primary disabled={uploading} onClick={() => inputRef.current?.click()}>选择文件</Btn>}
        onDragEnter={(event) => { event.preventDefault(); if (!uploading) setDragging(true) }}
        onDragOver={(event) => { event.preventDefault(); event.dataTransfer.dropEffect = 'copy' }}
        onDragLeave={(event) => { if (event.target === event.currentTarget) setDragging(false) }}
        onDrop={(event) => {
          event.preventDefault()
          setDragging(false)
          if (uploading) return
          const file = event.dataTransfer.files?.[0]
          if (file) void upload(file)
        }}
      />
      <input ref={inputRef} type="file" hidden accept=".md,.txt,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.png,.jpg,.jpeg,.webp" onChange={(event) => { const file = event.target.files?.[0]; if (file) void upload(file) }} />

      <StatGrid cols={4}>
        <Stat en="文档" value={String(data.stats.totalFiles)} unit="份" note={`上限 ${data.limits.files} 份`} />
        <Stat en="可检索" value={String(data.stats.readyFiles)} unit="份" note={`${data.stats.processingFiles} 份正在转换`} tone={data.stats.processingFiles ? 'var(--warn)' : undefined} />
        <Stat en="存储" value={f.bytes(data.stats.storageBytes)} note={`上限 ${data.limits.storageMb} MB`} />
        <Stat en="默认发布" value="关闭" note="自定义文档必须预览并手动启用" />
      </StatGrid>

      <div style={{ paddingTop: 22 }}>
        <Table cols="minmax(220px,1.8fr) 80px 110px minmax(150px,1fr) 120px 90px">
          <THead cells={['文档', '来源', '状态', '受众', '更新时间', '操作']} />
          {data.documents.map((doc) => (
            <TRow key={doc.id} onClick={() => choose(doc)} label={`查看 ${doc.displayName}`} bg={selectedId === doc.id ? 'var(--sub)' : undefined} cells={[
              <span key="doc" style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}><strong style={{ color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{doc.displayName}</strong><small style={{ ...mono('10.5px'), color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{doc.filename}</small></span>,
              <Pill key="source" tone={doc.sourceKind === 'builtin' ? 'idle' : 'warn'}>{sourceLabel(doc)}</Pill>,
              <Pill key="status" tone={statusTone(doc.status)}>{STATUS_LABEL[doc.status] ?? doc.status}{doc.enabled ? ' · 已启用' : ''}</Pill>,
              <span key="roles" style={{ color: 'var(--fg3)' }}>{doc.allowedRoles.map((role) => ROLE_LABEL[role]).join('、')}</span>,
              <span key="updated">{f.dateTime(doc.updatedAt)}</span>,
              <span key="action" style={{ display: 'inline-flex', gap: 9 }}><Eye size={15} aria-hidden="true" /><span className="sr-only">查看</span></span>,
            ]} />
          ))}
        </Table>
        {!data.documents.length && <Empty title="还没有平台知识文档" desc="内置文档启动时自动同步。也可以自己传 Markdown、Office 或 PDF。" />}
      </div>

      {selected && (
        <div style={{ borderTop: '1px solid var(--line)', marginTop: 28, paddingTop: 24 }}>
          <Sub title="文档详情" note={selected.sourceKind === 'builtin' ? '内置文档的正文改不了，但能开关它、也能改给哪些角色看' : '自己传的文档转换完之后，还得手动发布一下'} actions={<><Btn disabled={!detail.data || !detail.data.downloadUrl && detail.data?.content === undefined} onClick={download}><Download size={14} aria-hidden="true" /> 下载</Btn>{selected.sourceKind === 'custom' && <Btn disabled={actions.reprocess.isPending || !['ready', 'partial', 'failed', 'unsupported'].includes(selected.status)} onClick={() => actions.reprocess.mutate(selected.id, { onSuccess: () => say('已重新进入转换队列'), onError: fail })}><RefreshCw size={14} aria-hidden="true" /> 重处理</Btn>}{selected.sourceKind === 'custom' && <Btn danger disabled={actions.remove.isPending} onClick={() => { if (window.confirm('删掉这份自定义平台知识？之前引用过它的地方都会失效。')) actions.remove.mutate(selected.id, { onSuccess: () => { setSelectedId(null); say('平台知识已删除') }, onError: fail }) }}><Trash2 size={14} aria-hidden="true" /> 删除</Btn>}</>} />
          {detail.isLoading ? <div className="load-bar"><span /></div> : detail.data ? (
            <>
              <div data-r="platform-knowledge-meta" style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: '0 24px' }}>
                <Row label="文件大小" value={f.bytes(detail.data.sizeBytes)} />
                <Row label="转换器" value={detail.data.extractor || '—'} />
                <Row label="条目数" value={String(detail.data.entries)} />
              </div>
              <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', alignItems: 'flex-end', padding: '18px 0', borderBottom: '1px solid var(--line2)' }}>
                {selected.sourceKind === 'custom' && <label style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 260, flex: 1 }}><span style={{ fontSize: 12, color: 'var(--fg2)' }}>显示名称</span><input value={name} onChange={(event) => setName(event.target.value)} style={{ padding: '9px 10px', border: '1px solid var(--line)', background: 'var(--bg)', color: 'var(--fg)', font: 'inherit' }} /></label>}
                <fieldset style={{ border: 0, padding: 0, margin: 0, display: 'flex', flexDirection: 'column', gap: 7 }}><legend style={{ fontSize: 12, color: 'var(--fg2)', marginBottom: 2 }}>允许回答的角色</legend><div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>{(Object.keys(ROLE_LABEL) as PlatformKnowledgeRole[]).map((role) => <label key={role} style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 12.5, color: 'var(--fg2)' }}><input type="checkbox" checked={roles.includes(role)} onChange={() => setRoles((current) => current.includes(role) ? current.filter((item) => item !== role) : [...current, role])} />{ROLE_LABEL[role]}</label>)}</div></fieldset>
                <label style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: 'var(--fg2)' }}><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />启用检索</label>
                <Btn primary disabled={actions.update.isPending || !roles.length || (selected.sourceKind === 'custom' && !name.trim())} onClick={save}>保存发布设置</Btn>
              </div>
              {(detail.data.warning || detail.data.error) && <Note tone={detail.data.error ? 'warn' : 'idle'}>{detail.data.error ? readableErrorMessage(detail.data.error, '文件内容转换失败，请检查原件后重试') : detail.data.warning}</Note>}
              <div style={{ marginTop: 18, maxHeight: 420, overflow: 'auto', background: 'var(--sub)', padding: 16, whiteSpace: 'pre-wrap', fontFamily: 'ui-monospace, SFMono-Regular, Consolas, monospace', fontSize: 11.5, lineHeight: 1.7 }}>{detail.data.entriesPreview.map((entry) => entry.text).join('\n\n') || '转换结果暂不可预览'}</div>
            </>
          ) : <Empty title="文档详情不可用" desc="请刷新列表后重试。" />}
        </div>
      )}
    </section>
  )
}
