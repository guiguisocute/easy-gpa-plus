import { readableErrorMessage, userErrorMessage } from '@/api/errorMessages'
import { useRef, useState } from 'react'
import { Download, FileArchive, FileSearch, FolderOpen, RefreshCw, UploadCloud, X } from 'lucide-react'
import type { KnowledgeDocument, KnowledgeEvidence } from '@/api/types'
import {
  evidenceUrl,
  uploadKnowledgeDocument,
  useKnowledge,
  useKnowledgeActions,
  useKnowledgeDocument,
  useKnowledgeEntry,
} from '@/api/queries'
import { AgentWorkspace } from '@/components/AgentPanel'
import { FileDropzone } from '@/components/FileDropzone'
import { Btn, Empty, Note, Overlay, PageHead, Pill, Seg, Stat, StatGrid, Sub, TextBtn, Toggle } from '@/components/ui'
import {
  KNOWLEDGE_STATUS_LABEL,
  fileLogicalPath,
  formatBytes,
  formatLocator,
  runUploadQueue,
} from '@/lib/knowledgeAgent'
import { filesFromDrop } from '@/lib/dropFiles'
import { mono } from '@/lib/style'
import { useApp } from '@/stores/app'

type Tab = 'documents' | 'agent'
type UploadState = 'waiting' | 'uploading' | 'complete' | 'failed' | 'canceled'

interface UploadRow {
  id: string
  file: File
  logicalPath: string
  state: UploadState
  progress: number
  error: string | null
}

const STATUS_TONE = (status: KnowledgeDocument['status']): 'ok' | 'warn' | 'bad' | 'idle' => {
  if (status === 'ready') return 'ok'
  if (status === 'partial' || status === 'queued' || status === 'processing' || status === 'uploading') return 'warn'
  if (status === 'failed') return 'bad'
  return 'idle'
}

export default function AdminKnowledge() {
  const say = useApp((state) => state.say)
  const knowledge = useKnowledge()
  const actions = useKnowledgeActions()
  const [tab, setTab] = useState<Tab>('documents')
  const [uploads, setUploads] = useState<UploadRow[]>([])
  const [dragging, setDragging] = useState(false)
  const [previewId, setPreviewId] = useState<string | null>(null)
  const controllers = useRef(new Map<string, AbortController>())
  const fileInput = useRef<HTMLInputElement>(null)
  const folderInput = useRef<HTMLInputElement>(null)

  const fail = (error: unknown) => say(userErrorMessage(error))
  const state = knowledge.data

  const updateUpload = (id: string, patch: Partial<UploadRow>) =>
    setUploads((rows) => rows.map((row) => row.id === id ? { ...row, ...patch } : row))

  /* 传一批已经在 uploads 里的行。controllers 里有没有这一行的 AbortController，
     就是"这一行在不在飞"的唯一判据：进队列时登记，跑完删掉。 */
  const runUploads = async (rows: UploadRow[]) => {
    for (const row of rows) controllers.current.set(row.id, new AbortController())

    await runUploadQueue(rows, async (row) => {
      const controller = controllers.current.get(row.id)!
      updateUpload(row.id, { state: 'uploading', progress: 0, error: null })
      const documentId = await uploadKnowledgeDocument(
        row.file,
        row.logicalPath,
        (progress) => updateUpload(row.id, { progress }),
        controller.signal,
      )
      updateUpload(row.id, { state: 'complete', progress: 100 })
      return documentId
    }, {
      concurrency: 3,
      onProgress: ({ item, state: next, error }) => {
        if (next === 'failed') updateUpload(item.id, { state: 'failed', error: userErrorMessage(error, '上传失败，请稍后重试') })
        if (next === 'canceled') updateUpload(item.id, { state: 'canceled', error: '已取消' })
      },
    })
    for (const row of rows) controllers.current.delete(row.id)
    await knowledge.refetch()
  }

  const startUploads = async (files: File[]) => {
    if (!files.length) return
    const rows = files.map<UploadRow>((file) => ({
      id: crypto.randomUUID(),
      file,
      logicalPath: fileLogicalPath(file),
      state: 'waiting',
      progress: 0,
      error: null,
    }))
    setUploads((current) => [...rows, ...current])
    await runUploads(rows)
  }

  /* 重试就地复用原来那一行。以前这里走的是 startUploads([row.file])，那条路径
     每次都新建一行压进列表顶部：知识库没开时重试必定再失败，于是点一次多一条
     同名死行，越点越长、请求也越堆越多。 */
  const retryUpload = (id: string) => {
    if (controllers.current.has(id)) return
    const row = uploads.find((item) => item.id === id)
    if (!row) return
    const reset: UploadRow = { ...row, state: 'waiting', progress: 0, error: null }
    setUploads((current) => current.map((item) => item.id === id ? reset : item))
    void runUploads([reset])
  }

  if (knowledge.isLoading) return <div className="load-bar"><span /></div>
  if (knowledge.isError || !state) return <Empty title="班级资料暂时无法读取" desc="请稍后刷新，或联系平台运维。" />

  const consent = state.policy.externalProcessingApproved
  const enableConsent = () => {
    if (!consent) {
      const confirmed = window.confirm(
        '确认你有权处置这些资料，并同意第三方模型读取其中必要的片段。',
      )
      if (!confirmed) return
    }
    actions.setPolicy.mutate(!consent, {
      onSuccess: () => say(consent ? '已撤销本班第三方处理授权' : '本班第三方处理授权已确认'),
      onError: fail,
    })
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="CLASS FILES"
        title="班级资料"
        desc="评定文件 · 全班发布 · 可选内容检索"
        side={<Seg items={[{ key: 'documents', label: '文件' }, { key: 'agent', label: 'Agent 调试' }]} value={tab} onChange={setTab} />}
      />

      {tab === 'agent' ? (
        <div style={{ paddingTop: 2 }}>
          <div style={{ marginBottom: 16 }}>
            <Note>Agent 只搜当前账号能看的班级资料。</Note>
          </div>
          <AgentWorkspace embedded />
        </div>
      ) : (
        <>
          <StatGrid cols={5}>
            <Stat en="全部文件" value={String(state.stats.totalFiles)} note={state.stats.updatedAt ? `最近 ${new Date(state.stats.updatedAt).toLocaleDateString('zh-CN')}` : '暂无文件'} />
            <Stat en="班级资料" value={String(state.stats.classFiles)} note={`上限 ${state.limits.filesPerClass}`} />
            <Stat en="佐证材料" value={String(state.stats.evidenceFiles)} note="含全班成员" />
            <Stat en="可检索资料" value={String(state.stats.readyFiles)} tone="var(--ok)" note={state.stats.processingFiles ? `${state.stats.processingFiles} 份处理中` : '当前无处理任务'} />
            <Stat en="占用空间" value={formatBytes(state.stats.storageBytes)} note="资料与已完成佐证" />
          </StatGrid>

          <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 0, borderTop: '1px solid var(--line)', marginTop: 26 }}>
            <section style={{ padding: '24px 34px 24px 0' }}>
              <Sub title="内容处理状态" />
              <StatusLine label="AI 总开关" on={state.platform.aiEnabled} />
              <StatusLine label="文件转换、检索与问答" on={state.platform.knowledgeEnabled} />
              <StatusLine label="允许知识内容发送到模型" on={state.platform.knowledgeEgressEnabled} />
              {!state.platform.knowledgeEnabled && <Note tone="warn">内容转换和检索没开，上传下载照常。</Note>}
            </section>
            <section style={{ padding: '24px 0 24px 34px', borderLeft: '1px solid var(--line)' }} data-r="splitcol">
              <Sub title="本班第三方处理确认" />
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
                <Toggle on={consent} locked={actions.setPolicy.isPending} onClick={enableConsent} />
                <strong style={{ fontSize: 13 }}>{consent ? '本班已确认' : '默认关闭'}</strong>
              </div>
              <p style={{ margin: 0, color: 'var(--fg3)', fontSize: 12.5, lineHeight: 1.8 }}>
                文字问答只发片段，图片识别会整页发出去。撤销后不再发起新任务。
              </p>
            </section>
          </div>

          <section style={{ padding: '28px 0 0' }}>
            <Sub title="上传评定文件" note={`原件始终可保存 · 单文件 ${state.limits.fileMb} MB · 同时上传 3 个`} />
            <FileDropzone
              active={dragging}
              icon={<UploadCloud size={26} strokeWidth={1.3} />}
              title={dragging ? '松手就开始上传' : '把班级规则、表格或者证明材料拖进来'}
              hint="上传完成后，全班都能查看和下载"
              actions={<><Btn onClick={() => fileInput.current?.click()}>选择文件</Btn><Btn onClick={() => folderInput.current?.click()}><FolderOpen size={13} /> 选择文件夹</Btn></>}
              onDragEnter={(event) => { event.preventDefault(); setDragging(true) }}
              onDragOver={(event) => { event.preventDefault(); event.dataTransfer.dropEffect = 'copy' }}
              onDragLeave={(event) => { if (event.target === event.currentTarget) setDragging(false) }}
              onDrop={(event) => {
                event.preventDefault()
                setDragging(false)
                void filesFromDrop(event.dataTransfer).then(startUploads)
              }}
            />
            <input ref={fileInput} type="file" multiple hidden onChange={(event) => { void startUploads(Array.from(event.target.files ?? [])); event.currentTarget.value = '' }} />
            <input
              ref={(node) => { folderInput.current = node; node?.setAttribute('webkitdirectory', '') }}
              type="file"
              multiple
              hidden
              onChange={(event) => { void startUploads(Array.from(event.target.files ?? [])); event.currentTarget.value = '' }}
            />
            {uploads.length > 0 && (
              <div className="knowledge-upload-list">
                {uploads.map((row) => (
                  <div key={row.id}>
                    <span><strong>{row.file.name}</strong><small>{row.logicalPath}</small></span>
                    <progress value={row.progress} max={100} />
                    <span style={mono('10.5px', '.02em')}>{uploadLabel(row)}</span>
                    {row.state === 'uploading' || row.state === 'waiting' ? (
                      <button type="button" onClick={() => controllers.current.get(row.id)?.abort()} aria-label={`取消上传 ${row.file.name}`}><X size={13} /></button>
                    ) : row.state === 'failed' || row.state === 'canceled' ? (
                      <button type="button" onClick={() => retryUpload(row.id)} aria-label={`重试上传 ${row.file.name}`}><RefreshCw size={13} /></button>
                    ) : <span />}
                  </div>
                ))}
              </div>
            )}
          </section>

          <section style={{ padding: '30px 0 0' }}>
            <Sub title="班级公开资料" note="传完之后，学生能在「班级操行分评定细则」里下载" />
            {!state.documents.length ? <Empty title="还没有班级资料" desc="传学院细则、班级速查表或者补充说明。" /> : (
              <div data-r="scroll">
                <table className="knowledge-table">
                  <thead><tr><th>文件与路径</th><th>状态</th><th>内容</th><th>更新时间</th><th>操作</th></tr></thead>
                  <tbody>{state.documents.map((document) => (
                    <KnowledgeRow key={document.id} document={document} onPreview={setPreviewId} onError={fail} />
                  ))}</tbody>
                </table>
              </div>
            )}
          </section>

          <EvidenceInventory items={state.evidence} onError={fail} />
        </>
      )}

      {previewId && <KnowledgePreview documentId={previewId} onClose={() => setPreviewId(null)} />}
    </div>
  )
}

function StatusLine({ label, on }: { label: string; on: boolean }) {
  return <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderTop: '1px solid var(--line2)', padding: '10px 0' }}><span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>{label}</span><Pill tone={on ? 'ok' : 'bad'}>{on ? '已开启' : '未开启'}</Pill></div>
}

function KnowledgeRow({ document, onPreview, onError }: { document: KnowledgeDocument; onPreview: (id: string) => void; onError: (error: unknown) => void }) {
  const actions = useKnowledgeActions()
  const rename = () => {
    const displayName = window.prompt('修改显示名称', document.displayName)?.trim()
    if (!displayName || displayName === document.displayName) return
    actions.updateDocument.mutate({ id: document.id, displayName }, { onError })
  }
  const content = document.pageCount ? `${document.pageCount} 页` : document.sheetCount ? `${document.sheetCount} 个工作表` : `${document.entries} 个条目`
  return (
    <tr>
      <td><strong>{document.displayName}</strong><small>{document.logicalPath}</small><small>{document.mediaType} · {formatBytes(document.sizeBytes)}</small></td>
      <td><Pill tone={STATUS_TONE(document.status)}>{KNOWLEDGE_STATUS_LABEL[document.status]}</Pill>{document.warning && <small>{document.warning}</small>}{document.error && <small className="is-error">{readableErrorMessage(document.error, '文件内容转换失败，请检查原件后重试')}</small>}</td>
      <td><span>{document.searchable ? content : '原件可下载'}</span><small>{document.extractor || 'original-only'}</small></td>
      <td><span>{new Date(document.updatedAt).toLocaleDateString('zh-CN')}</span><small>{new Date(document.updatedAt).toLocaleTimeString('zh-CN')}</small></td>
      <td><div className="knowledge-row-actions"><TextBtn onClick={() => onPreview(document.id)}>预览</TextBtn><TextBtn onClick={rename}>改名</TextBtn><TextBtn onClick={() => actions.reprocess.mutate(document.id, { onError })}>重处理</TextBtn><TextBtn tone="var(--red)" onClick={() => { if (window.confirm(`删除“${document.displayName}”？`)) actions.remove.mutate(document.id, { onError }) }}>删除</TextBtn></div></td>
    </tr>
  )
}

const EVIDENCE_SOURCE_LABEL: Record<KnowledgeEvidence['source'], string> = {
  submission: '学生提交',
  appeal: '申诉材料',
  objection: '异议说明',
}

function EvidenceInventory({ items, onError }: { items: KnowledgeEvidence[]; onError: (error: unknown) => void }) {
  const [busy, setBusy] = useState<string | null>(null)
  const download = async (item: KnowledgeEvidence) => {
    setBusy(item.id)
    try {
      const link = await evidenceUrl(item.id)
      window.open(link.url, '_blank', 'noopener')
    } catch (error) {
      onError(error)
    } finally {
      setBusy(null)
    }
  }

  return (
    <section style={{ padding: '30px 0 0' }}>
      <Sub title="全班佐证材料" note={`${items.length} 份 · 班管汇总`} />
      {!items.length ? <Empty title="还没有佐证材料" desc="学生上传材料后会出现在这里。" /> : (
        <div data-r="scroll">
          <table className="knowledge-table">
            <thead><tr><th>文件</th><th>所属同学</th><th>来源</th><th>上传人</th><th>状态</th><th>上传时间</th><th>操作</th></tr></thead>
            <tbody>{items.map((item) => (
              <tr key={item.id}>
                <td><strong>{item.filename}</strong><small>{item.mediaType} · {formatBytes(item.sizeBytes)}</small></td>
                <td><span>{item.subjectName}</span><small>{item.subjectSid}</small></td>
                <td><span>{item.kind === 'note' ? '说明附件' : EVIDENCE_SOURCE_LABEL[item.source]}</span>{item.title && <small>{item.title}</small>}</td>
                <td><span>{item.uploaderName}</span><small>{item.uploaderSid}</small></td>
                <td><Pill tone={item.status === 'ready' ? 'ok' : 'warn'}>{item.status === 'ready' ? '已上传' : '上传中'}</Pill></td>
                <td><span>{new Date(item.createdAt).toLocaleDateString('zh-CN')}</span><small>{new Date(item.createdAt).toLocaleTimeString('zh-CN')}</small></td>
                <td><TextBtn disabled={item.status !== 'ready' || busy === item.id} onClick={() => void download(item)}>{busy === item.id ? '生成中…' : '下载'}</TextBtn></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function KnowledgePreview({ documentId, onClose }: { documentId: string; onClose: () => void }) {
  const detail = useKnowledgeDocument(documentId)
  const [entryId, setEntryId] = useState<string | null>(null)
	const [endLine, setEndLine] = useState(200)
  const document = detail.data
  const selected = document?.entriesPreview.find((entry) => entry.id === entryId) ?? document?.entriesPreview[0]
	const fullEntry = useKnowledgeEntry(selected?.id ?? null, 1, endLine)
	const shown = fullEntry.data ?? selected
  return (
    <Overlay>
    <div className="knowledge-preview-overlay" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <aside className="knowledge-preview" role="dialog" aria-modal="true" aria-label="知识文件预览">
        <header><div><span style={mono('10px', '.12em')}>SOURCE PREVIEW</span><strong>{document?.displayName ?? '读取中…'}</strong><small>{document?.logicalPath}</small></div><button type="button" onClick={onClose} aria-label="关闭预览"><X size={15} /></button></header>
        {detail.isLoading ? <div className="agent-muted">正在重新校验权限并读取文件…</div> : detail.isError || !document ? <Note tone="warn">文件不存在或权限已变化。</Note> : (
          <>
            <section className="knowledge-attachment">
              <FileArchive size={21} strokeWidth={1.35} />
              <div><strong>{document.filename}</strong><span>{document.mediaType} · {formatBytes(document.sizeBytes)}</span></div>
              <a href={document.downloadUrl} target="_blank" rel="noopener noreferrer" download><Download size={13} /> 下载原件</a>
            </section>
            <div className="knowledge-preview-meta"><Pill tone={STATUS_TONE(document.status)}>{KNOWLEDGE_STATUS_LABEL[document.status]}</Pill><span>{document.searchable ? `${document.entries} 个规范化条目` : '内容暂不可检索'}</span></div>
            {document.entriesPreview.length > 0 ? (
              <div className="knowledge-entry-view">
                <nav>{document.entriesPreview.map((entry, index) => <button key={entry.id} type="button" className={selected?.id === entry.id ? 'is-active' : ''} onClick={() => { setEntryId(entry.id); setEndLine(200) }}><span>{entry.kind || `条目 ${index + 1}`}</span><small>{formatLocator(entry.locator)}</small></button>)}</nav>
                <article>
                  <div><FileSearch size={14} /><span>{selected ? formatLocator(selected.locator) : '资料片段'}</span></div>
                  <pre>{shown?.text}</pre>
                  {!!selected?.lineCount && (shown?.endLine ?? 0) < selected.lineCount && (
                    <Btn disabled={fullEntry.isFetching} onClick={() => setEndLine((value) => Math.min(value + 200, selected.lineCount ?? value + 200))}>
                      {fullEntry.isFetching ? '读取中…' : `继续加载（已显示 ${shown?.endLine ?? 0}/${selected.lineCount} 行）`}
                    </Btn>
                  )}
                </article>
              </div>
            ) : <Note>这份文件里现在没有能搜的文字，但原件可以正常下载。</Note>}
          </>
        )}
      </aside>
    </div>
    </Overlay>
  )
}

function uploadLabel(row: UploadRow) {
  if (row.state === 'waiting') return '等待上传'
  if (row.state === 'uploading') return `${row.progress}%`
  if (row.state === 'complete') return '上传完成'
  return row.error || (row.state === 'canceled' ? '已取消' : '失败')
}
