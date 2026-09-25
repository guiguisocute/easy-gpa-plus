import { readableErrorMessage, userErrorMessage } from '@/api/errorMessages'
import { useEffect, useMemo, useRef, useState, type DragEvent as ReactDragEvent } from 'react'
import { FileText, RefreshCw, Sparkles, Upload, X } from 'lucide-react'
import { Btn, Bar, Field, Note, Overlay, Pill, TextBtn } from '@/components/ui'
import { FileDropzone } from '@/components/FileDropzone'
import { EvidenceLightbox, type Preview } from '@/components/EvidenceLightbox'
import { ApiError } from '@/api/client'
import { aiAssetMediaType, uploadAIAsset, useAIBatch, useAIBatchActions } from '@/api/queries'
import type { AIApplyResult, AIBatchLimits, AICandidate, AIAsset, AIItem, AIMaterialFormat, Claim } from '@/api/types'
import type { SchemeConfig, ScoreRule } from '@/lib/types'
import { claimableItems } from '@/lib/schemeTree'
import { applyProblem, candidateItem, chooseEnumClaim, resetClaim, type AIMaterialItem } from '@/lib/aiMaterial'
import { filesFromDrop } from '@/lib/dropFiles'
import { fileExtension } from '@/lib/evidence'
import { fieldStyle, mono, num } from '@/lib/style'
import { useApp } from '@/stores/app'

const SESSION_BATCH_KEY = 'easygpa.ai-material-batch'
const FORMAT_ORDER: AIMaterialFormat[] = ['jpeg', 'png', 'webp', 'pdf']
const FORMAT_META: Record<AIMaterialFormat, { label: string; extensions: string[] }> = {
  jpeg: { label: 'JPEG', extensions: ['.jpg', '.jpeg'] },
  png: { label: 'PNG', extensions: ['.png'] },
  webp: { label: 'WebP', extensions: ['.webp'] },
  pdf: { label: 'PDF', extensions: ['.pdf'] },
}
const DEFAULT_LIMITS: AIBatchLimits = { items: 100, dailyBatches: 5, activeBatches: 3, fileMb: 50, batchMb: 256, pdfPages: 64, allowedFormats: FORMAT_ORDER }

type LocalUploadStatus = 'queued' | 'uploading' | 'ready' | 'error'

interface LocalUpload {
  key: string
  file: File
  assetId?: string
  status: LocalUploadStatus
  progress: number
  error?: string
}

function savedBatchId() {
  try {
    return sessionStorage.getItem(SESSION_BATCH_KEY)
  } catch {
    return null
  }
}

function rememberBatch(id: string | null) {
  try {
    if (id) sessionStorage.setItem(SESSION_BATCH_KEY, id)
    else sessionStorage.removeItem(SESSION_BATCH_KEY)
  } catch {
    // 隐私模式下退化为当前页面内有效。
  }
}

function copyCandidates(value: AICandidate[]) {
  return value.map((candidate) => ({
    ...candidate,
    claim: { ...candidate.claim },
    assets: candidate.assets.map((asset) => ({ ...asset })),
    alternatives: [...candidate.alternatives],
    reviewReasons: [...candidate.reviewReasons],
  }))
}

/* 只归到大项时把大项名字找出来。candidateItem 要求大项小项都对得上，
   所以它在这种半成品上一律返回 undefined。 */
function categoryOnlyName(candidate: AICandidate, scheme: SchemeConfig) {
  if (!candidate.categoryKey || candidate.itemKey) return ''
  return scheme.categories.find((category) => category.key === candidate.categoryKey)?.name ?? ''
}

function selectedValue(candidate: AICandidate) {
  return candidate.categoryKey && candidate.itemKey ? `${candidate.categoryKey}\u0000${candidate.itemKey}` : ''
}

/* 浮层里的拖拽不能冒到页面上去。React 的 portal 事件走 React 树而不是 DOM 树，
   而提交页根节点的 onDragOver 在没选中小项时会把 dropEffect 设成 'none'——那等于替
   整页否决了这次拖放，浮层里的 drop 事件根本不会触发，表现就是"拖进去毫无反应"。 */
function keepDrag(event: ReactDragEvent<HTMLElement>) {
  event.preventDefault()
  event.stopPropagation()
}

function materialFormat(file: Pick<File, 'name'>): AIMaterialFormat | null {
  switch (fileExtension(file.name)) {
    case 'jpg': case 'jpeg': return 'jpeg'
    case 'png': return 'png'
    case 'webp': return 'webp'
    case 'pdf': return 'pdf'
    default: return null
  }
}

function allowedFormatText(formats: AIMaterialFormat[]) {
  return FORMAT_ORDER.filter((format) => formats.includes(format)).map((format) => FORMAT_META[format].label).join('、')
}

function formatAccept(formats: AIMaterialFormat[]) {
  return FORMAT_ORDER.filter((format) => formats.includes(format)).flatMap((format) => FORMAT_META[format].extensions).join(',')
}

function validateFile(file: File, maxMB: number, allowedFormats: AIMaterialFormat[]) {
  if (file.size <= 0) return `${file.name} 是空文件`
  const format = materialFormat(file)
  if (!format || !allowedFormats.includes(format)) {
    return `${file.name} 当前不允许上传；请选择 ${allowedFormatText(allowedFormats)}`
  }
  if (file.size > maxMB * 1024 * 1024) return `${file.name} 超过 ${maxMB} MB`
  const type = aiAssetMediaType(file)
  if (!['image/jpeg', 'image/png', 'image/webp', 'application/pdf'].includes(type)) {
    return `${file.name} 的格式与文件类型不一致`
  }
  return null
}

/* 点缩略图开大图，用的是佐证卡片和正文内嵌图那一套 EvidenceLightbox——按 Esc 关、
   点空白关的行为得处处一致。原来这里是 target="_blank" 跳新标签页：核对候选时被
   甩出当前上下文，看完还得切回来，而且和站内别处看图的手感完全不同。

   PDF 没法用 <img> 显示，仍旧走新标签页打开。 */
function AssetThumb({ asset, page, size = 76, onPreview }: { asset?: AIAsset; page?: number; size?: number; onPreview?: (preview: Preview) => void }) {
  if (!asset) return null
  const image = asset.mediaType.startsWith('image/')
  const inner = (
    <>
      {image && asset.previewUrl ? (
        <img src={asset.previewUrl} alt={asset.filename} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
      ) : (
        <FileText size={Math.round(size / 3)} strokeWidth={1.4} />
      )}
      {page != null && page > 0 && (
        <span style={{ position: 'absolute', right: 4, bottom: 4, padding: '2px 5px', background: 'rgba(0,0,0,.72)', color: '#fff', fontSize: 10 }}>P{page}</span>
      )}
    </>
  )
  const frame = { width: size, height: size, flex: 'none' as const, border: '1px solid var(--line)', background: 'var(--sub)', color: 'var(--fg2)', textDecoration: 'none', display: 'flex', alignItems: 'center', justifyContent: 'center', position: 'relative' as const, overflow: 'hidden' }
  if (image && asset.previewUrl && onPreview) {
    return (
      <button
        type="button"
        title={`查看 ${asset.filename}`}
        onClick={() => onPreview({ url: asset.previewUrl!, name: asset.filename })}
        style={{ ...frame, padding: 0, cursor: 'zoom-in' }}
      >
        {inner}
      </button>
    )
  }
  return (
    <a href={asset.previewUrl} target="_blank" rel="noreferrer" title={`查看 ${asset.filename}`} style={frame}>
      {inner}
    </a>
  )
}

function fileSize(bytes: number) {
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`
}

function UploadCardShell({
  name, size, image, preview, status, tone, progress, error, removing, canRemove, onRemove, onRetry,
}: {
  name: string
  size: number
  image: boolean
  preview?: string
  status: string
  tone: 'bad' | 'ok' | 'idle'
  progress?: number
  error?: string
  removing: boolean
  canRemove: boolean
  onRemove: () => void
  /** 只有失败的本地上传会传：重传复用原来那张卡片，不新增一行。 */
  onRetry?: () => void
}) {
  const visual = image && preview
    ? <img src={preview} alt={name} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
    : <FileText size={24} strokeWidth={1.35} />
  return (
    <div style={{ minWidth: 0, border: '1px solid var(--line)', background: 'var(--bg)', display: 'grid', gridTemplateColumns: '72px minmax(0,1fr) auto', gap: 12, alignItems: 'center', padding: 10 }}>
      {preview ? (
        <a href={preview} target="_blank" rel="noreferrer" title={`查看 ${name}`} style={{ width: 72, height: 72, background: 'var(--sub)', color: 'var(--fg3)', display: 'flex', alignItems: 'center', justifyContent: 'center', overflow: 'hidden', textDecoration: 'none' }}>{visual}</a>
      ) : (
        <span style={{ width: 72, height: 72, background: 'var(--sub)', color: 'var(--fg3)', display: 'flex', alignItems: 'center', justifyContent: 'center', overflow: 'hidden' }}>{visual}</span>
      )}
      <span style={{ minWidth: 0, display: 'flex', flexDirection: 'column', gap: 5 }}>
        <strong title={name} style={{ fontSize: 12.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{name}</strong>
        <small style={{ fontSize: 11.5, color: 'var(--fg3)' }}>{fileSize(size)}</small>
        {progress != null && progress < 100 && <Bar pct={`${progress}%`} tone={tone === 'bad' ? 'var(--red)' : 'var(--fg)'} />}
        {error && <small style={{ fontSize: 11.5, color: 'var(--red)', lineHeight: 1.45 }}>{readableErrorMessage(error, '材料处理失败，请重试或手动提交')}</small>}
      </span>
      <span style={{ alignSelf: 'stretch', display: 'flex', flexDirection: 'column', alignItems: 'flex-end', justifyContent: 'space-between', gap: 8 }}>
        <Pill tone={tone}>{status}</Pill>
        <span style={{ display: 'flex', gap: 6 }}>
        {onRetry && (
          <button
            type="button"
            aria-label={`重新上传 ${name}`}
            title="重新上传"
            disabled={removing}
            onClick={onRetry}
            className="hv-line-fg"
            style={{ width: 28, height: 28, borderRadius: 999, border: '1px solid var(--line)', background: 'var(--bg)', color: 'var(--fg2)', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: removing ? 'not-allowed' : 'pointer', opacity: removing ? .55 : 1 }}
          >
            <RefreshCw size={13} />
          </button>
        )}
        <button
          type="button"
          aria-label={`删除 ${name}`}
          title={canRemove ? '删除这份材料' : '上传完成后可以删除'}
          disabled={!canRemove || removing}
          onClick={onRemove}
          className="hv-red"
          style={{ width: 28, height: 28, borderRadius: 999, border: '1px solid var(--line)', background: 'var(--bg)', color: canRemove ? 'var(--red)' : 'var(--fg3)', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: canRemove ? 'pointer' : 'not-allowed', opacity: removing ? .55 : 1 }}
        >
          <X size={13} />
        </button>
        </span>
      </span>
    </div>
  )
}

function LocalUploadCard({ upload, removing, onRemove, onRetry }: { upload: LocalUpload; removing: boolean; onRemove: () => void; onRetry: () => void }) {
  const image = aiAssetMediaType(upload.file).startsWith('image/')
  const [preview, setPreview] = useState('')
  useEffect(() => {
    if (!image) {
      setPreview('')
      return
    }
    const url = URL.createObjectURL(upload.file)
    setPreview(url)
    return () => URL.revokeObjectURL(url)
  }, [image, upload.file])
  const label = upload.status === 'error' ? '失败' : upload.status === 'ready' ? '已上传' : `${upload.progress}%`
  return <UploadCardShell name={upload.file.name} size={upload.file.size} image={image} preview={preview} status={label} tone={upload.status === 'error' ? 'bad' : upload.status === 'ready' ? 'ok' : 'idle'} progress={upload.progress} error={upload.error} removing={removing} canRemove={upload.status === 'ready' || upload.status === 'error'} onRemove={onRemove} onRetry={upload.status === 'error' ? onRetry : undefined} />
}

function ServerAssetCard({ asset, removing, onRemove }: { asset: AIAsset; removing: boolean; onRemove: () => void }) {
  const status = asset.status === 'rejected' || asset.status === 'failed' ? '失败' : asset.status === 'ready' ? '已上传' : '处理中'
  const tone = asset.status === 'rejected' || asset.status === 'failed' ? 'bad' : asset.status === 'ready' ? 'ok' : 'idle'
  return <UploadCardShell name={asset.filename} size={asset.sizeBytes} image={asset.mediaType.startsWith('image/')} preview={asset.previewUrl} status={status} tone={tone} error={asset.error ?? undefined} removing={removing} canRemove={asset.status === 'pending' || asset.status === 'ready' || asset.status === 'rejected'} onRemove={onRemove} />
}

function ClaimEditor({ rule, claim, onChange }: { rule: ScoreRule; claim: Claim; onChange: (claim: Claim) => void }) {
  if (rule.type === 'enum') {
    const max = Math.max(0, ...rule.options.map((option) => option.score))
    const unknownOption = !!claim.option && !rule.options.some((option) => option.label === claim.option)
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <Field label="申报档次" hint="AI 建议值，记得核对。拿不准先空着">
          <select
            value={claim.option ?? ''}
            onChange={(event) => onChange(chooseEnumClaim(rule, event.target.value))}
            style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '10px 12px' }}
          >
            <option value="">请选择档位（可暂时留空）</option>
            {unknownOption && <option value={claim.option} disabled>选一个档位（AI 原来建议的那个不在当前方案里）</option>}
            {rule.options.map((option) => <option key={option.label} value={option.label}>{option.label} · 建议 {option.score} 分</option>)}
          </select>
        </Field>
        <Field label="期望加分 · 可调整" hint={`选了档位会填一个建议分，可以按实际情况在 0—${max} 分内修改`}>
          <input
            type="number"
            min={0}
            max={max}
            step="0.01"
            value={claim.score ?? ''}
            onChange={(event) => onChange({ option: claim.option, ...(event.target.value === '' ? {} : { score: Number(event.target.value) }) })}
            style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '10px 12px', ...num }}
          />
        </Field>
      </div>
    )
  }
  if (rule.type === 'free') {
    return (
      <Field label="自报分" hint={`AI 会先填一个（有可能是 0 分），核对一下。允许范围 ${rule.min}—${rule.max} 分`}>
        <input
          type="number"
          min={rule.min}
          max={rule.max}
          step="any"
          value={claim.score ?? ''}
          onChange={(event) => onChange(event.target.value === '' ? {} : { score: Number(event.target.value) })}
          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '10px 12px', ...num }}
        />
      </Field>
    )
  }
  return (
    <Field label={`数量（${rule.unit}）`} hint={rule.type === 'per_unit' ? '可以空着，回头审核人照佐证来定' : `达到 ${rule.minimum} ${rule.unit}后按规则计分`}>
      <input
        type="number"
        min={0}
        step="any"
        value={claim.quantity ?? ''}
        onChange={(event) => onChange(event.target.value === '' ? {} : { quantity: Number(event.target.value) })}
        style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '10px 12px', ...num }}
      />
    </Field>
  )
}

export function AIMaterialPanel({ open, onClose, scheme }: { open: boolean; onClose: () => void; scheme: SchemeConfig }) {
  const say = useApp((state) => state.say)
  const go = useApp((state) => state.go)
  const actions = useAIBatchActions()
  const [batchId, setBatchId] = useState<string | null>(() => savedBatchId())
  const [uploads, setUploads] = useState<LocalUpload[]>([])
  const [candidates, setCandidates] = useState<AICandidate[]>([])
  const [applyError, setApplyError] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  // 名字不用 preview：那个已经是归组过程中的候选预览，两件事。
  const [lightbox, setLightbox] = useState<Preview | null>(null)
  const [applied, setApplied] = useState<AIApplyResult | null>(null)
  const [createdLimits, setCreatedLimits] = useState<AIBatchLimits | null>(null)
  const createPromise = useRef<Promise<string> | null>(null)
  const inFlight = useRef(new Set<string>())
  const loadedReview = useRef<string | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const folderInput = useRef<HTMLInputElement>(null)
  const [dropping, setDropping] = useState(false)
  const [deleting, setDeleting] = useState<Set<string>>(() => new Set())
  const [removedAssetIds, setRemovedAssetIds] = useState<Set<string>>(() => new Set())
  const batch = useAIBatch(batchId)

  const items = useMemo<AIMaterialItem[]>(() => scheme.categories.flatMap((category) =>
    claimableItems(category).map((item) => ({ categoryKey: category.key, categoryName: category.name, item })),
  ), [scheme])
  const assetById = useMemo(() => new Map((batch.data?.assets ?? []).map((asset) => [asset.id, asset])), [batch.data?.assets])
  /* 识图结果一直在接口里（items[].perception），但处理中的列表从来没用过它，于是
     几十张图的识别阶段界面上只有一个百分比在走。按资产归一下就能边识边看。 */
  const perceptionByAsset = useMemo(() => {
    const map = new Map<string, AIItem[]>()
    for (const item of batch.data?.items ?? []) {
      const bucket = map.get(item.assetId)
      if (bucket) bucket.push(item)
      else map.set(item.assetId, [item])
    }
    return map
  }, [batch.data?.items])
  const limits = batch.data?.limits ?? createdLimits ?? DEFAULT_LIMITS
  const allowedFormats = limits.allowedFormats?.length ? limits.allowedFormats : DEFAULT_LIMITS.allowedFormats
  const acceptedFiles = formatAccept(allowedFormats)

  useEffect(() => {
    if (!open) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKey = (event: KeyboardEvent) => {
      /* 大图开着时 Esc 归它。两个监听器都挂在 window 上，谁也拦不住谁，不让位的话
         一次 Esc 会把图和整个面板一起关掉——核对到一半的候选就这么没了。 */
      if (event.key === 'Escape' && !lightbox) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      document.body.style.overflow = previous
      window.removeEventListener('keydown', onKey)
    }
  }, [open, onClose, lightbox])

  useEffect(() => {
    const data = batch.data
    if (data?.status !== 'review') return
    const version = `${data.id}:${data.completedAt ?? ''}`
    if (loadedReview.current === version) return
    loadedReview.current = version
    setCandidates(copyCandidates(data.result.candidates ?? []))
    setApplyError(null)
    setSelected(new Set())
  }, [batch.data])

  useEffect(() => {
    if (!batchId || !(batch.error instanceof ApiError) || batch.error.status !== 404) return
    // sessionStorage 只保存恢复提示。服务端已经清理或过期时，静默回到新批次，
    // 避免一个陈旧 ID 让整个上传入口永久卡在 404。
    setBatchId(null)
    rememberBatch(null)
    setUploads([])
    setCandidates([])
    setApplyError(null)
    setCreatedLimits(null)
    setRemovedAssetIds(new Set())
    loadedReview.current = null
  }, [batch.error, batchId])

  async function ensureBatch() {
    if (batchId) return batchId
    if (createPromise.current) return createPromise.current
    const pending = actions.create.mutateAsync().then((created) => {
      setBatchId(created.id)
      setCreatedLimits(created.limits)
      setRemovedAssetIds(new Set())
      rememberBatch(created.id)
      return created.id
    }).finally(() => {
      createPromise.current = null
    })
    createPromise.current = pending
    return pending
  }

  function updateUpload(key: string, change: Partial<LocalUpload>) {
    setUploads((current) => current.map((entry) => entry.key === key ? { ...entry, ...change } : entry))
  }

  async function addFiles(list: File[]) {
    if (!list.length) return
    const maxMB = limits.fileMb
    const additions: LocalUpload[] = []
    const skipped: string[] = []
    for (const file of list) {
      const problem = validateFile(file, maxMB, allowedFormats)
      if (problem) skipped.push(problem)
      else additions.push({ key: crypto.randomUUID(), file, status: 'queued', progress: 0 })
    }
    /* 拖一整个目录进来会顺带扫到一堆无关文件，一个文件弹一条提示会把屏幕刷满：
       只说第一条，其余折成一句总数。 */
    if (skipped.length === 1) say(skipped[0])
    else if (skipped.length > 1) say(`${skipped[0]}；另有 ${skipped.length - 1} 个文件被跳过`)
    if (!additions.length) return
    const serverCount = (batch.data?.assets ?? []).filter((asset) => asset.status !== 'rejected').length
    const localCount = uploads.filter((upload) => upload.status !== 'error').length
    const currentCount = Math.max(serverCount, localCount) + additions.length
    if (currentCount > limits.items) {
      say(`一批最多 ${limits.items} 张图片或 PDF 页，分几批传`)
      return
    }
    setUploads((current) => [...current, ...additions])
    let id: string
    try {
      id = await ensureBatch()
    } catch (error) {
      const message = error instanceof ApiError ? error.message : '无法建立 AI 批次'
      for (const upload of additions) updateUpload(upload.key, { status: 'error', error: message })
      return
    }
    await runUploads(id, additions)
    if (batchId === id) await batch.refetch()
  }

  /* 传一批已经在 uploads 里的卡片。inFlight 记着哪些 key 还在飞，重传按钮据此
     去重，连点不会把同一份材料排两次队。 */
  async function runUploads(id: string, rows: LocalUpload[]) {
    for (const row of rows) inFlight.current.add(row.key)
    let cursor = 0
    const worker = async () => {
      while (cursor < rows.length) {
        const upload = rows[cursor++]
        updateUpload(upload.key, { status: 'uploading', progress: 1, error: undefined })
        try {
          const completed = await uploadAIAsset(id, upload.file, (progress) => updateUpload(upload.key, { progress: Math.min(progress, 99) }))
          updateUpload(upload.key, { assetId: completed.assetId, status: 'ready', progress: 100 })
        } catch (error) {
          updateUpload(upload.key, { status: 'error', error: userErrorMessage(error, '上传失败，请稍后重试') })
        }
      }
    }
    // 并发 3，和知识库上传一致（runUploadQueue 的上限也是 3）。
    await Promise.all(Array.from({ length: Math.min(3, rows.length) }, worker))
    for (const row of rows) inFlight.current.delete(row.key)
  }

  /* 重传复用原来那张卡片，不新建一行：失败的材料越点越多是上一版知识库上传踩过
     的坑，这里不重犯。批次已经不是 uploading 了就没得重传，提示一句让用户重建。 */
  async function retryUpload(key: string) {
    if (inFlight.current.has(key)) return
    const row = uploads.find((upload) => upload.key === key)
    if (!row || row.status !== 'error') return
    let id: string
    try {
      id = await ensureBatch()
    } catch (error) {
      updateUpload(key, { status: 'error', error: error instanceof ApiError ? error.message : '无法建立 AI 批次' })
      return
    }
    await runUploads(id, [{ ...row, status: 'queued', progress: 0, error: undefined }])
    if (batchId === id) await batch.refetch()
  }

  async function removeUploaded(assetId: string, localKey?: string) {
    if (!batchId) return
    setDeleting((current) => new Set(current).add(assetId))
    try {
      await actions.removeAsset.mutateAsync({ batchId, assetId })
      setRemovedAssetIds((current) => new Set(current).add(assetId))
      setUploads((current) => current.filter((upload) => upload.key !== localKey && upload.assetId !== assetId))
      await batch.refetch()
    } catch (error) {
      say(error instanceof ApiError ? error.message : '无法删除这份材料')
    } finally {
      setDeleting((current) => {
        const next = new Set(current)
        next.delete(assetId)
        return next
      })
    }
  }

  function removeLocal(upload: LocalUpload) {
    if (upload.status === 'error') {
      setUploads((current) => current.filter((entry) => entry.key !== upload.key))
      return
    }
    if (upload.assetId) void removeUploaded(upload.assetId, upload.key)
  }

  async function start() {
    if (!batchId) return
    try {
      await actions.start.mutateAsync(batchId)
      await batch.refetch()
    } catch (error) {
      say(error instanceof ApiError ? error.message : '无法开始整理')
    }
  }

  function changeCandidate(id: string, change: Partial<AICandidate>) {
    setApplyError(null)
    setCandidates((current) => current.map((candidate) => candidate.id === id ? { ...candidate, ...change } : candidate))
  }

  function chooseItem(candidate: AICandidate, value: string) {
    const [categoryKey, itemKey] = value.split('\u0000')
    const entry = items.find((item) => item.categoryKey === categoryKey && item.item.key === itemKey)
    if (!entry) {
      changeCandidate(candidate.id, { categoryKey: '', itemKey: '', claim: {}, needsReview: true })
      return
    }
    changeCandidate(candidate.id, {
      categoryKey,
      itemKey,
      claim: resetClaim(entry.item.scoreRule, candidate.claim),
      needsReview: true,
      reviewReasons: [...new Set([...candidate.reviewReasons, '小项是你自己改过的，再核对一遍下面的字段'])],
      expectedScore: null,
    })
  }

  function splitCandidate(candidate: AICandidate) {
    if (candidate.assets.length < 2) return
    setApplyError(null)
    const pieces = candidate.assets.map((asset, index) => ({
      ...candidate,
      id: `${candidate.id}-split-${index + 1}-${Date.now()}`,
      assets: [{ ...asset }],
      claim: { ...candidate.claim },
      needsReview: true,
      reviewReasons: [...new Set([...candidate.reviewReasons, '这条是你自己拆开的，两边的标题和字段都核对一下'])],
      expectedScore: null,
    }))
    setCandidates((current) => current.flatMap((entry) => entry.id === candidate.id ? pieces : [entry]))
    setSelected((current) => {
      const next = new Set(current)
      next.delete(candidate.id)
      return next
    })
  }

  function mergeSelected() {
    const picked = candidates.filter((candidate) => selected.has(candidate.id))
    if (picked.length < 2) return
    setApplyError(null)
    const first = picked[0]
    const seen = new Set<string>()
    const assets = picked.flatMap((candidate) => candidate.assets).filter((asset) => {
      const key = `${asset.assetId}:${asset.page ?? 0}`
      if (seen.has(key)) return false
      seen.add(key)
      return true
    })
    const merged: AICandidate = {
      ...first,
      id: `candidate-merged-${Date.now()}`,
      assets,
      claim: { ...first.claim },
      note: [...new Set(picked.map((candidate) => candidate.note.trim()).filter(Boolean))].join('\n'),
      needsReview: true,
      reviewReasons: [...new Set([...picked.flatMap((candidate) => candidate.reviewReasons), '这条是你自己合并的，核对一下小项和字段'])],
      expectedScore: null,
    }
    setCandidates((current) => [merged, ...current.filter((candidate) => !selected.has(candidate.id))])
    setSelected(new Set())
  }

  async function apply() {
    if (!batchId) return
    const problem = applyProblem(candidates, items)
    if (problem) {
      setApplyError(problem)
      say(problem)
      return
    }
    setApplyError(null)
    try {
      const result = await actions.apply.mutateAsync({ id: batchId, candidates })
      setApplied(result)
      rememberBatch(null)
      say(`已创建 ${result.drafts.length} 条 AI 草稿，没有自动提交`)
    } catch (error) {
      const message = error instanceof ApiError ? error.message : '应用候选失败'
      setApplyError(message)
      say(message)
    }
  }

  async function discardBatch() {
    if (batchId && batch.data?.status !== 'complete') {
      try {
        await actions.remove.mutateAsync(batchId)
      } catch (error) {
        say(error instanceof ApiError ? error.message : '无法放弃本批')
        return
      }
    }
    setBatchId(null)
    rememberBatch(null)
    setUploads([])
    setCandidates([])
    setApplyError(null)
    setSelected(new Set())
    setApplied(null)
    setCreatedLimits(null)
    setRemovedAssetIds(new Set())
    loadedReview.current = null
  }

  /* 只置标记，剩下的交给 worker：正在跑的那一块要让它自己收尾，然后批次会照常
     进 review，界面靠轮询看到。不清本地状态——已经归好的候选正是要留下的东西。 */
  async function stopCompose() {
    if (!batchId) return
    try {
      await actions.stopCompose.mutateAsync(batchId)
      say('已经叫停了，当前这一批归完就停')
    } catch (error) {
      say(error instanceof ApiError ? error.message : '无法停止归组')
    }
  }

  if (!open) return null
  const data = batch.data
  const status = data?.status ?? (batchId ? 'uploading' : 'uploading')
  const localPending = uploads.some((upload) => upload.status === 'queued' || upload.status === 'uploading')
  const serverAssets = data?.assets ?? []
  // presign 一落库，批次查询就可能先看到 pending 服务端记录；本地卡片此时仍在显示
  // 预览和进度。让本地卡片在本次页面会话内负责展示，服务端卡片按 ID 优先、文件指纹
  // 兜底做一对一让位，既不闪两张，也不会误吞确实上传的两份同名同大小文件。
  const unmatchedUploads = [...uploads]
  const visibleServerAssets = serverAssets.filter((asset) => {
    if (removedAssetIds.has(asset.id)) return false
    const index = unmatchedUploads.findIndex((upload) => upload.assetId
      ? asset.id === upload.assetId
      : asset.filename === upload.file.name && asset.sizeBytes === upload.file.size)
    if (index < 0) return true
    unmatchedUploads.splice(index, 1)
    return false
  })
  const visibleLocalUploads = uploads
  const canStart = !!batchId && !localPending && (uploads.some((upload) => upload.status === 'ready') || serverAssets.some((asset) => asset.status === 'ready'))
  const progress = data?.total ? Math.min(100, Math.round((data.processed / data.total) * 100)) : 0
  // 逐张识别跑完、批次还在 processing，就说明已经进到归组阶段了。
  const grouping = status === 'processing' && !!data?.total && data.processed >= data.total
  const preview = data?.composePreview ?? []
  const thinking = data?.composeThinking ?? ''
  const composeChunks = data?.composeChunks
  const failedAssets = (data?.assets ?? []).filter((asset) => asset.status === 'failed')

  return (
    <Overlay>
      {/* 浮层里的拖拽一律就地吃掉：没命中投放区时不让浏览器直接打开那个文件，命中了
          也不能让事件冒到页面上去。 */}
      <div role="dialog" aria-modal="true" aria-label="AI 批量整理材料" onDragEnter={keepDrag} onDragOver={keepDrag} onDragLeave={keepDrag} onDrop={keepDrag} style={{ position: 'fixed', inset: 0, zIndex: 90, display: 'flex', justifyContent: 'flex-end', background: 'rgba(0,0,0,.38)', backdropFilter: 'blur(2px)' }} onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
      <section data-r="ai-panel" style={{ width: 'min(760px,100vw)', height: '100dvh', background: 'var(--bg)', borderLeft: '1px solid var(--line)', boxShadow: '-22px 0 70px rgba(0,0,0,.18)', display: 'flex', flexDirection: 'column', animation: 'rise .2s ease both' }}>
        <header style={{ padding: '21px 24px 18px', borderBottom: '1px solid var(--line)', display: 'flex', alignItems: 'flex-start', gap: 16 }}>
          <span style={{ width: 34, height: 34, borderRadius: 999, background: 'var(--redBg)', color: 'var(--red)', display: 'flex', alignItems: 'center', justifyContent: 'center', flex: 'none' }}><Sparkles size={17} strokeWidth={1.6} /></span>
          <div style={{ minWidth: 0, flex: 1 }}>
            <div style={{ fontSize: 18, fontWeight: 600, letterSpacing: '-.025em' }}>用 AI 批量整理材料</div>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭" className="hv-line-fg" style={{ width: 32, height: 32, borderRadius: 999, background: 'none', border: '1px solid var(--line)', color: 'var(--fg2)', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer' }}><X size={15} /></button>
        </header>

        <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', padding: '24px' }}>
          {applied ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 22 }}>
              <div>
                <div style={mono('11px', '.08em')}>DRAFTS CREATED</div>
                <div style={{ fontSize: 23, fontWeight: 600, letterSpacing: '-.035em', marginTop: 10 }}>已创建 {applied.drafts.length} 条草稿</div>
              </div>
              <Note tone="ok">材料整理成草稿了，检查过再自己提交。</Note>
              <div style={{ borderTop: '1px solid var(--line)' }}>
                {applied.drafts.map((draft, index) => (
                  <div key={draft.id} style={{ padding: '15px 0', borderBottom: '1px solid var(--line2)', display: 'flex', alignItems: 'center', gap: 12 }}>
                    <span style={mono('11px', '.02em')}>#{draft.id}</span>
                    <span style={{ fontSize: 13, color: 'var(--fg2)' }}>草稿 {index + 1}</span>
                    <span style={{ marginLeft: 'auto' }}><Btn onClick={() => { onClose(); go('stuSubmit', draft.id) }}>打开检查</Btn></span>
                  </div>
                ))}
              </div>
              <div style={{ display: 'flex', gap: 10 }}><Btn primary onClick={() => { onClose(); go('stuList') }}>查看我的提交</Btn><Btn onClick={() => void discardBatch()}>整理新一批</Btn></div>
            </div>
          ) : status === 'queued' || status === 'processing' ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
              <div>
                <div style={mono('11px', '.08em')}>PROCESSING</div>
                <div style={{ fontSize: 23, fontWeight: 600, letterSpacing: '-.035em', marginTop: 10 }}>{status === 'queued' ? '正在等待处理' : grouping ? '正在按方案归组' : '正在逐张识别'}</div>
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', marginTop: 8 }}>可以把面板关掉去忙别的，后台还在跑。</div>
              </div>
              {/* 归组按块跑、每块落库，所以这里能报真进度：几块里归完了几块、已经出了
                  多少条候选。以前只有一个不动的"归组中"，看着像卡死。 */}
              <div style={{ borderTop: '1px solid var(--line)', borderBottom: '1px solid var(--line)', padding: '20px 0' }}>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 12 }}>
                  {/* 这里报的是"第几批"，不是"第几张"。写成 0 / 4 会被读成材料数——
                      24 张图明明传上去了，界面上却是 0，看着像什么都没做。 */}
                  <span style={{ fontSize: 28, fontWeight: 600, ...num }}>
                    {grouping
                      ? composeChunks?.total
                        ? `第 ${Math.min(composeChunks.complete + 1, composeChunks.total)} 批`
                        : '归组中'
                      : `${progress}%`}
                  </span>
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
                    {grouping
                      ? composeChunks?.total
                        ? `共 ${composeChunks.total} 批${composeChunks.candidates ? ` · 已写出 ${composeChunks.candidates} 条候选` : ''}`
                        : `${data?.total ?? 0} 张或页已识别完，正在合并成候选`
                      : `${data?.processed ?? 0} / ${data?.total ?? 0} 张或页`}
                  </span>
                </div>
                <Bar pct={grouping && composeChunks?.total ? `${Math.round((composeChunks.complete / composeChunks.total) * 100)}%` : `${progress}%`} />
                {grouping && (
                  <div style={{ marginTop: 12 }}>
                    <div style={{ fontSize: 11.5, color: 'var(--fg3)' }}>
                      {data?.total ?? 0} 张或页按活动分成 {composeChunks?.total ?? 0} 批交给文本模型。每归完一批就存下来。
                    </div>
                    {/* 模型边写边回显。定高 + 顶部对齐，免得每多一条整个面板就往下抖一格。 */}
                    <ul style={{ margin: '10px 0 0', padding: 0, listStyle: 'none', maxHeight: 168, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 6 }}>
                      {preview.map((line, index) => {
                        /* 后端把标题和那句申报说明用换行拼在一行里送来，这里拆开显示：
                           标题一眼扫过，说明是模型真正写出来的正文，等的时候能读。 */
                        const [title, ...rest] = line.split('\n')
                        const note = rest.join(' ').trim()
                        return (
                          <li key={`${index}-${title}`} style={{ display: 'flex', gap: 8, fontSize: 12, color: 'var(--fg2)' }}>
                            <span style={{ ...mono('11px', '.02em'), color: 'var(--fg3)', flex: 'none' }}>{String(index + 1).padStart(2, '0')}</span>
                            <span style={{ minWidth: 0, display: 'flex', flexDirection: 'column', gap: 3 }}>
                              <span style={{ overflowWrap: 'anywhere', fontWeight: 500 }}>{title}</span>
                              {note && <span style={{ color: 'var(--fg3)', lineHeight: 1.6, overflowWrap: 'anywhere' }}>{note}</span>}
                            </span>
                          </li>
                        )
                      })}
                      {!preview.length && <li style={{ fontSize: 12, color: 'var(--fg3)' }}>模型还没写出第一条…</li>}
                    </ul>
                    {/* 推理比正文早得多（实测 11 秒对 64 秒）。候选还一条都没有的那一分钟，
                        全靠这段让人看出模型确实在干活，而不是又卡住了。 */}
                    {thinking && !preview.length && (
                      <div style={{ marginTop: 12, borderTop: '1px dashed var(--line2)', paddingTop: 10 }}>
                        <div style={{ ...mono('10.5px', '.06em'), color: 'var(--fg3)' }}>模型正在思考</div>
                        <p style={{ margin: '7px 0 0', fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.7, maxHeight: 96, overflowY: 'auto', overflowWrap: 'anywhere', whiteSpace: 'pre-wrap' }}>{thinking}</p>
                      </div>
                    )}
                  </div>
                )}
              </div>
              {/* 这一列原来只有文件名和一个状态点。手机相册和微信存下来的图，文件名就是
                  一串哈希，二十几行排下来根本认不出哪张是哪张——失败了也不知道该重传哪个。
                  沿用上传那一步的缩略图，再把识图读出来的内容跟在后面：哪张成了、读成了
                  什么，一眼就能对上。 */}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {(data?.assets ?? []).map((asset) => {
                  const read = (perceptionByAsset.get(asset.id) ?? []).filter((item) => item.perception)
                  return (
                    <div key={asset.id} style={{ display: 'flex', gap: 11, alignItems: 'flex-start', padding: '10px 0', borderBottom: '1px solid var(--line2)' }}>
                      <AssetThumb asset={asset} size={46} onPreview={setLightbox} />
                      <span style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
                        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 12.5 }}>{asset.filename}</span>
                        {read.map((item) => {
                          const fields = item.perception?.fields
                          const detail = [fields?.date, fields?.level, fields?.issuer].map((part) => part?.trim()).filter(Boolean).join(' · ')
                          return (
                            <span key={`${item.assetId}:${item.page}`} style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: 6, fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6 }}>
                              {item.perception?.materialType && <Pill>{item.perception.materialType}</Pill>}
                              <span style={{ color: 'var(--fg2)', overflowWrap: 'anywhere' }}>{fields?.title?.trim() || '（没读到名称）'}</span>
                              {detail && <span style={{ overflowWrap: 'anywhere' }}>{detail}</span>}
                            </span>
                          )
                        })}
                        {asset.status === 'failed' && asset.error && (
                          <span style={{ fontSize: 11.5, color: 'var(--red)', lineHeight: 1.5 }}>{readableErrorMessage(asset.error, '材料识别失败，请重试或手动提交')}</span>
                        )}
                      </span>
                      <Pill tone={asset.status === 'failed' ? 'bad' : asset.status === 'complete' ? 'ok' : 'idle'}>{asset.status === 'failed' ? '失败' : asset.status === 'complete' ? '完成' : '处理中'}</Pill>
                    </div>
                  )
                })}
              </div>
            </div>
          ) : status === 'review' ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
              <div style={{ display: 'flex', alignItems: 'flex-end', gap: 12, flexWrap: 'wrap' }}>
                <div style={{ flex: 1 }}><div style={mono('11px', '.08em')}>REVIEW CANDIDATES</div><div style={{ fontSize: 23, fontWeight: 600, letterSpacing: '-.035em', marginTop: 9 }}>核对 {candidates.length} 条候选</div></div>
                {/* 没勾中时按钮是灰的、写着「合并所选（0）」，看不出该先做什么。
                    改成先说怎么用，勾够了再变成可点的动作。 */}
                {selected.size < 2 ? (
                  <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.6, maxWidth: 250 }}>
                    同一件事拆成两条了？勾上它们，这里会出现合并按钮。
                  </span>
                ) : (
                  <Btn onClick={mergeSelected}>把选中的 {selected.size} 条合并成一条</Btn>
                )}
              </div>
              {(data?.result.warnings ?? []).map((warning) => <Note key={warning} tone="warn">{warning}</Note>)}
              {((data?.result.warnings?.length ?? 0) > 0 || candidates.some((candidate) => candidate.needsReview || candidate.reviewReasons.length > 0)) && (
                <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.6 }}>黄色的要你核对，不影响创建草稿。拿不准的字段先空着。</span>
              )}
              {/* 有材料没读出来时给一条补救路径。重跑只补失败的那几张（识别成功的
                  都在库里复用），但归组是整批重来，所以现有候选会被覆盖。 */}
              {failedAssets.length > 0 && (
                <Note tone="warn">
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                    <span style={{ flex: 1, minWidth: 200 }}>
                      有 {failedAssets.length} 份材料没认出来，现在这批候选里没有它们。
                    </span>
                    <Btn
                      disabled={actions.recompose.isPending}
                      onClick={() => {
                        if (!window.confirm(
                          `重跑这 ${failedAssets.length} 份材料，重新归一次组？归组是整批重来，你在这一页改过的会被覆盖；再失败的话，现在这些候选就没了。`,
                        )) return
                        actions.recompose.mutate(batchId!, {
                          onSuccess: (result) => say(`正在重跑 ${result.retryingItems} 份材料`),
                          onError: (error) => say(error instanceof ApiError ? error.message : '无法重跑'),
                        })
                      }}
                    >{actions.recompose.isPending ? '正在重跑…' : `重跑这 ${failedAssets.length} 份`}</Btn>
                  </div>
                </Note>
              )}
              {candidates.length === 0 && <Note tone="warn">AI 没归出能用的候选。放弃这一批，回去手动填吧。</Note>}
              {candidates.map((candidate, index) => {
                const entry = candidateItem(candidate, items)
                return (
                  <article key={candidate.id} style={{ border: '1px solid var(--line)', background: 'var(--bg)' }}>
                    <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--line)', background: 'var(--sub)', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                      {/* 光一个裸复选框看不出是干什么用的——它是顶部「合并所选」的唯一
                          入口，勾上才知道。把用途写在旁边。 */}
                      <label title="勾上两条或更多，点上面的「合并所选」把它们并成一条" style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 13, fontWeight: 600 }}>
                        <input type="checkbox" checked={selected.has(candidate.id)} onChange={(event) => setSelected((current) => { const next = new Set(current); if (event.target.checked) next.add(candidate.id); else next.delete(candidate.id); return next })} aria-label={`勾选第 ${index + 1} 条候选来合并`} />
                        候选 {index + 1}
                      </label>
                      <Pill tone={candidate.needsReview ? 'warn' : 'ok'}>{candidate.needsReview ? '需要核对' : '字段完整'}</Pill>
                      {/* 置信度删了：一个"AI 85%"既不影响能不能应用，也没告诉人该做什么，
                          只会让人对着一个不知道怎么用的数字发愣。拿不准的地方写在
                          reviewReasons 里，那才是具体、可执行的。 */}
                      <span style={{ marginLeft: 'auto' }} />
                      <TextBtn disabled={candidate.assets.length < 2} onClick={() => splitCandidate(candidate)}>按材料拆分</TextBtn>
                      <TextBtn tone="var(--red)" onClick={() => { setApplyError(null); setCandidates((current) => current.filter((entry) => entry.id !== candidate.id)); setSelected((current) => { const next = new Set(current); next.delete(candidate.id); return next }) }}>丢弃</TextBtn>
                    </div>
                    <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 17 }}>
                      <div style={{ display: 'flex', gap: 8, overflowX: 'auto', paddingBottom: 2 }}>{candidate.assets.map((ref) => <AssetThumb key={`${ref.assetId}:${ref.page ?? 0}`} asset={assetById.get(ref.assetId)} page={ref.page} onPreview={setLightbox} />)}</div>
                      {/* 选项文字带上大项：下拉框收起时只显示选中项的文字，optgroup 的分组名
                          是看不见的，光写小项名根本认不出这条归到了哪个大项。 */}
                      <Field label="归入小项" hint="下拉框里就是已发布方案里的那些小项，加不了新的">
                        {/* AI 认出了大项、没认准小项时，select 的值是空串，只显示「请选择小项」——
                            它已经判断出来的那一半就这么没了，人得自己重新猜是哪个大项。 */}
                        {!entry && categoryOnlyName(candidate, scheme) && (
                          <span style={{ fontSize: 12, color: 'var(--fg2)', display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                            <Pill tone="warn">已归入大项</Pill>
                            <strong>{categoryOnlyName(candidate, scheme)}</strong>
                            <span style={{ color: 'var(--fg3)' }}>· 小项没认准，请在下面挑一个</span>
                          </span>
                        )}
                        <select value={selectedValue(candidate)} onChange={(event) => chooseItem(candidate, event.target.value)} style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '10px 12px' }}>
                          <option value="">请选择小项</option>
                          {scheme.categories.map((category) => <optgroup key={category.key} label={category.name}>{claimableItems(category).map((item) => <option key={`${category.key}:${item.key}`} value={`${category.key}\u0000${item.key}`}>{category.name} · {item.name}</option>)}</optgroup>)}
                        </select>
                      </Field>
                      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1.45fr 1fr', gap: 14 }}>
                        <Field label="事项名称"><input value={candidate.title} onChange={(event) => changeCandidate(candidate.id, { title: event.target.value, expectedScore: null })} style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '10px 12px' }} /></Field>
                        {entry ? <ClaimEditor rule={entry.item.scoreRule} claim={candidate.claim} onChange={(claim) => changeCandidate(candidate.id, { claim, expectedScore: null })} /> : <div />}
                      </div>
                      <Field label="参与说明" hint="用你自己的话写。这段是以你的名义交上去的"><textarea rows={3} value={candidate.note} onChange={(event) => changeCandidate(candidate.id, { note: event.target.value })} style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '10px 12px', resize: 'vertical', lineHeight: 1.65 }} /></Field>
                      {candidate.reviewReasons.length > 0 && <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>{candidate.reviewReasons.map((reason) => <Pill key={reason} tone="warn">{reason}</Pill>)}</div>}
                      {/* 预估分是这张卡片上最该被看见的数字，原来和脚注一样是 11.5px 的灰字。 */}
                      <div style={{ display: 'flex', alignItems: 'baseline', gap: 9, borderTop: '1px solid var(--line2)', paddingTop: 11 }}>
                        <span style={{ fontSize: 12, color: 'var(--fg3)' }}>当前预估</span>
                        {candidate.expectedScore == null ? (
                          <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>创建草稿时计算</span>
                        ) : (
                          <>
                            <span style={{ fontSize: 22, fontWeight: 600, color: 'var(--fg)', ...num }}>{candidate.expectedScore}</span>
                            <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>分</span>
                          </>
                        )}
                      </div>
                    </div>
                  </article>
                )
              })}
            </div>
          ) : status === 'failed' || status === 'expired' || status === 'canceled' ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
              <div>
                <div style={mono('11px', '.08em')}>NEEDS MANUAL ACTION</div>
                <div style={{ fontSize: 23, fontWeight: 600, marginTop: 10 }}>这一批没能完成</div>
              </div>
              <Note tone="warn">{readableErrorMessage(data?.error, status === 'expired' ? '批次已过期' : status === 'canceled' ? '批次已取消' : '材料整理失败，请重试')}。手动提交功能不受影响。</Note>
              {/* 只有 failed 能重试，过期和取消不行。重试不会重跑已经识别成功的图：
                  逐张结果都在库里，worker 只补没成的那部分，归组失败时就只重跑归组
                  那一次调用——几十张图等十几分钟的活儿不必再来一遍。 */}
              {status === 'failed' ? (
                <>
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7 }}>
                    已经认出来的会留着，重试只补没做完的。
                  </span>
                  <div style={{ display: 'flex', gap: 10 }}>
                    <Btn primary disabled={actions.start.isPending} onClick={() => void start()}>
                      {actions.start.isPending ? '正在重试…' : '重试这一批'}
                    </Btn>
                    <Btn onClick={() => void discardBatch()}>整理新一批</Btn>
                  </div>
                </>
              ) : (
                <div><Btn primary onClick={() => void discardBatch()}>整理新一批</Btn></div>
              )}
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 21 }}>
              <div><div style={mono('11px', '.08em')}>UPLOAD</div><div style={{ fontSize: 23, fontWeight: 600, letterSpacing: '-.035em', marginTop: 9 }}>把同一人的材料放进来</div><div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 8 }}>当前允许 {allowedFormatText(allowedFormats)}；单份最多 {limits.fileMb} MB，每批总量最多 {limits.batchMb} MB，PDF 最多 {limits.pdfPages} 页，全批图片和 PDF 页合计最多 {limits.items}，每日最多启动 {limits.dailyBatches} 批，同时保留 {limits.activeBatches} 个活动批次。每批只处理你一个人的材料。</div></div>
              {/* 拖进来的可以是文件，也可以是整个目录：一学年的材料通常就摊在一个
                  文件夹里，逼着人先把图片挑出来没有道理。目录里无关的文件在
                  addFiles 里按扩展名跳过。 */}
              <FileDropzone
                data-r="ai-drop"
                active={dropping}
                icon={<Upload size={24} strokeWidth={1.4} />}
                          title={dropping ? '松手开始上传' : '将文件或文件夹拖至此区域'}
                hint="或点击下方按钮上传"
                actions={<><Btn onClick={() => fileInput.current?.click()}>选择文件</Btn><Btn onClick={() => folderInput.current?.click()}>选择文件夹</Btn></>}
                onDragEnter={(event) => { keepDrag(event); setDropping(true) }}
                // dropEffect 必须自己写死 copy：留空会沿用上一次 dragover 的值。
                onDragOver={(event) => { keepDrag(event); event.dataTransfer.dropEffect = 'copy'; setDropping(true) }}
                onDragLeave={(event) => { keepDrag(event); if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropping(false) }}
                onDrop={(event) => {
                  keepDrag(event)
                  setDropping(false)
                  void filesFromDrop(event.dataTransfer).then((files) => {
                    // 一个文件都没取到时必须出声，否则用户看到的只是"拖进去没反应"。
                    if (!files.length) return say('这次拖进来没读到文件，换下面的「选择文件」或「选择文件夹」试试')
                    return addFiles(files)
                  })
                }}
              />
              <input ref={fileInput} type="file" multiple accept={acceptedFiles} onChange={(event) => { void addFiles(Array.from(event.target.files ?? [])); event.target.value = '' }} style={{ display: 'none' }} />
              {/* webkitdirectory 不在 React 的属性表里，只能挂 ref 上手写（同
                  admin/Knowledge 的选文件夹）；选目录时不带 accept，否则整个目录都
                  会被筛掉。 */}
              <input
                ref={(node) => { folderInput.current = node; node?.setAttribute('webkitdirectory', '') }}
                type="file"
                multiple
                onChange={(event) => { void addFiles(Array.from(event.target.files ?? [])); event.target.value = '' }}
                style={{ display: 'none' }}
              />
              {(visibleLocalUploads.length > 0 || visibleServerAssets.length > 0) && (
                <div>
                  <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 12, marginBottom: 10 }}>
                    <strong style={{ fontSize: 12.5 }}>已添加材料</strong>
                    <span style={{ fontSize: 11.5, color: 'var(--fg3)' }}>{visibleServerAssets.length + visibleLocalUploads.filter((upload) => upload.status !== 'error').length} 份</span>
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(290px,1fr))', gap: 10 }}>
                    {visibleServerAssets.map((asset) => <ServerAssetCard key={asset.id} asset={asset} removing={deleting.has(asset.id)} onRemove={() => void removeUploaded(asset.id)} />)}
                    {visibleLocalUploads.map((upload) => <LocalUploadCard key={upload.key} upload={upload} removing={!!upload.assetId && deleting.has(upload.assetId)} onRemove={() => removeLocal(upload)} onRetry={() => void retryUpload(upload.key)} />)}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>

        <footer style={{ padding: '15px 24px', borderTop: '1px solid var(--line)', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', background: 'var(--bg)' }}>
          {status === 'review' && applyError && <div role="alert" style={{ flexBasis: '100%' }}><Note tone="bad">{applyError}。候选和你改过的内容都还在。</Note></div>}
          {status === 'uploading' && <><Btn primary disabled={!canStart || actions.start.isPending} onClick={() => void start()}>{actions.start.isPending ? '启动中…' : '开始整理'}</Btn>{batchId && <Btn danger disabled={actions.remove.isPending} onClick={() => void discardBatch()}>放弃本批</Btn>}</>}
          {status === 'review' && <><Btn primary disabled={!candidates.length || actions.apply.isPending} onClick={() => void apply()}>{actions.apply.isPending ? '创建中…' : `确认并创建 ${candidates.length} 条草稿`}</Btn><Btn danger onClick={() => void discardBatch()}>放弃本批</Btn></>}
          {(status === 'queued' || status === 'processing') && (
            <>
              <Btn onClick={onClose}>后台继续处理</Btn>
              {/* 归组阶段才给"停止归组"：识图还没跑完时一条候选都还没有，没什么可保留的，
                  三个按钮挤在一起反而让人分不清哪个是哪个。 */}
              {grouping && (
                <Btn
                  disabled={actions.stopCompose.isPending}
                  onClick={() => {
                    if (!window.confirm('停止归组？已经归好的会留着，剩下的回头再跑。')) return
                    void stopCompose()
                  }}
                >{actions.stopCompose.isPending ? '正在停止…' : '停止归组（保留已出候选）'}</Btn>
              )}
              {/* 放弃整批会连已上传的材料一起丢掉，所以问一句——这一批的识图结果
                  没法转给下一批。 */}
              <Btn
                danger
                onClick={() => {
                  if (!window.confirm('放弃整批？传上去的材料和识别结果都会删掉，找不回来。')) return
                  void discardBatch()
                }}
              >放弃整批</Btn>
            </>
          )}
          <span style={{ marginLeft: 'auto', fontSize: 11.5, color: 'var(--fg3)' }}>AI 不会自动提交，也不写最终分</span>
        </footer>
        </section>
      </div>
      {/* 挂在面板外层：大图要盖住整个抽屉，挂在 section 里会被它的滚动容器裁掉。 */}
      <EvidenceLightbox preview={lightbox} onClose={() => setLightbox(null)} />
    </Overlay>
  )
}
