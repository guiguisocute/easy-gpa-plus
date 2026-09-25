import { userErrorMessage } from '@/api/errorMessages'
/* 理由类正文的编辑器。审核意见、申诉理由、复评理由、扣分依据、终裁理由都用它。

   没有引第三方所见即所得编辑器：MDXEditor / TipTap 之流各自带一整套 CSS 主题，
   要跟这个站"全内联样式 + --fg/--line/--sub 变量"的写法对齐，花在覆盖样式上的代码
   会比这个文件还多，打包体积还要翻一倍。这里就是原来那个 textarea，
   上面加一排按钮、旁边加一个预览页签——写的是 markdown，所见即所得靠切页签。

   「插入佐证」插的是 `![名字](evidence:<id>)`，只列得出本条记录自己的佐证。
   正文里不放外部图床链接的理由写在 Markdown.tsx 顶部。 */

import { useEffect, useRef, useState, type ClipboardEvent } from 'react'
import { Bold, Italic, Link2, List, ListOrdered, Paperclip, Quote, Upload } from 'lucide-react'
import { Seg } from '@/components/ui'
import { RichText } from '@/components/Markdown'
import { uploadMarkdownAttachment, type MarkdownUploadTarget } from '@/api/queries'
import { evidenceAccept, validateEvidenceFile } from '@/lib/evidence'
import type { Evidence } from '@/api/types'

const TABS = [
  { key: 'write' as const, label: '写' },
  { key: 'preview' as const, label: '预览' },
]

// 沿用站内 evidence 引用：图片预览，其他格式显示受权限保护的附件链接。
function evidenceSnippet(item: Evidence) {
  const name = item.name.replaceAll('\\', '\\\\').replaceAll('[', '\\[').replaceAll(']', '\\]').replace(/[\r\n]/g, ' ')
  return `![${name}](evidence:${item.id})`
}

/** 把选区包起来（粗体、斜体这类）。没选中时插入占位词并选中它，让人接着打字就能覆盖。 */
function wrap(value: string, start: number, end: number, mark: string, placeholder: string) {
  const chosen = value.slice(start, end) || placeholder
  const text = `${value.slice(0, start)}${mark}${chosen}${mark}${value.slice(end)}`
  return { text, from: start + mark.length, to: start + mark.length + chosen.length }
}

/** 给选区涉及的每一行加前缀（引用、列表这类）。有序列表要逐行递增编号。 */
function prefixLines(value: string, start: number, end: number, mark: string | ((i: number) => string)) {
  const lineStart = value.lastIndexOf('\n', start - 1) + 1
  const lineEnd = value.indexOf('\n', end) === -1 ? value.length : value.indexOf('\n', end)
  const body = value
    .slice(lineStart, lineEnd)
    .split('\n')
    .map((line, i) => (typeof mark === 'string' ? mark : mark(i)) + line)
    .join('\n')
  const text = value.slice(0, lineStart) + body + value.slice(lineEnd)
  return { text, from: lineStart, to: lineStart + body.length }
}

export function MarkdownEditor({
  value,
  onChange,
  placeholder,
  minHeight = 96,
  invalid,
  evidence = [],
  uploadTarget,
  onUploadingChange,
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  minHeight?: number
  invalid?: boolean
  /** 可以在正文里引用的佐证。为空时不显示「插入佐证」。 */
  evidence?: Evidence[]
  /** 提供归属后，可选择文件或粘贴截图，上传为本条正文的附件。 */
  uploadTarget?: MarkdownUploadTarget
  onUploadingChange?: (uploading: boolean) => void
}) {
  const area = useRef<HTMLTextAreaElement>(null)
  const picker = useRef<HTMLInputElement>(null)
  const session = useRef(0)
  const busy = useRef(false)
  const latest = useRef({ value, onChange })
  useEffect(() => { latest.current = { value, onChange } }, [value, onChange])
  const [tab, setTab] = useState<'write' | 'preview'>('write')
  const [picking, setPicking] = useState(false)
  const [localEvidence, setLocalEvidence] = useState<Evidence[]>([])
  const [uploading, setUploading] = useState(0)
  const [uploadError, setUploadError] = useState('')
  const uploadTargetKey = uploadTarget ? `${uploadTarget.kind}:${'owner' in uploadTarget ? `${uploadTarget.owner}:` : ''}${uploadTarget.id}` : ''

  useEffect(() => {
    const activeSession = ++session.current
    busy.current = false
    setUploading(0)
    setPicking(false)
    setLocalEvidence([])
    setUploadError('')
    return () => { session.current = activeSession + 1 }
  }, [uploadTargetKey])

  useEffect(() => {
    onUploadingChange?.(uploading > 0)
    return () => onUploadingChange?.(false)
  }, [uploading, onUploadingChange])

  const visibleEvidence = [...evidence, ...localEvidence.filter((local) => !evidence.some((item) => item.id === local.id))]

  /* 没有内容就没什么可预览的，一律显示编辑区。这一条同时兜住了一个真实的坑：
     停在预览页签时切到下一条待审，value 被清空、组件却还留在预览态，
     于是整排工具条是灰的、textarea 也不见了，看着像页面坏了。 */
  const previewing = tab === 'preview' && value.trim() !== ''

  /* 改完文本要把光标放回去，否则连点两次工具条按钮，第二次会作用在文首。 */
  const apply = (next: { text: string; from: number; to: number }) => {
    onChange(next.text)
    requestAnimationFrame(() => {
      const el = area.current
      if (!el) return
      el.focus()
      el.setSelectionRange(next.from, next.to)
    })
  }

  const act = (fn: (v: string, s: number, e: number) => { text: string; from: number; to: number }) => () => {
    const el = area.current
    if (!el) return
    apply(fn(value, el.selectionStart, el.selectionEnd))
  }

  const insert = (snippet: string) => {
    const el = area.current
    const at = el ? el.selectionStart : value.length
    /* 图片自成一段，塞在句子中间会跟前后文字挤在一行。 */
    const before = value.slice(0, at)
    const pad = before && !before.endsWith('\n') ? '\n\n' : ''
    const text = before + pad + snippet + '\n' + value.slice(at)
    apply({ text, from: at + pad.length + snippet.length + 1, to: at + pad.length + snippet.length + 1 })
  }

  const uploadFiles = async (files: File[]) => {
    if (!uploadTarget || !files.length || busy.current) return
    const activeSession = session.current
    const original = latest.current.value
    const at = area.current?.selectionStart ?? original.length
    busy.current = true
    setUploading(files.length)
    setUploadError('')
    const uploaded: Evidence[] = []
    const errors: string[] = []
    for (const file of files) {
      if (session.current !== activeSession) return
      try {
        const invalidFile = validateEvidenceFile(file, undefined)
        if (invalidFile) throw new Error(invalidFile)
        uploaded.push(await uploadMarkdownAttachment(uploadTarget, file))
      } catch (error) {
        errors.push(validateEvidenceFile(file, undefined) || `${file.name}：${userErrorMessage(error, '上传失败，请重试')}`)
      } finally {
        if (session.current === activeSession) setUploading((count) => Math.max(0, count - 1))
      }
    }
    if (session.current !== activeSession) return
    busy.current = false
    setUploadError(errors.join('；'))
    if (uploaded.length === 0) return
    setLocalEvidence((current) => [...current, ...uploaded])
    const snippets = uploaded.map(evidenceSnippet).join('\n\n')
    // 上传期间仍可打字；正文变过就追加到最新文本，绝不写回上传前的旧值。
    const current = latest.current.value
    const position = current === original ? at : current.length
    const before = current.slice(0, position)
    const pad = before && !before.endsWith('\n') ? '\n\n' : ''
    const inserted = pad + snippets + '\n'
    latest.current.onChange(before + inserted + current.slice(position))
    requestAnimationFrame(() => {
      if (session.current !== activeSession) return
      area.current?.focus()
      area.current?.setSelectionRange(position + inserted.length, position + inserted.length)
    })
  }

  const pasteImages = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    if (!uploadTarget) return
    const files = Array.from(event.clipboardData.items)
      .filter((item) => item.kind === 'file' && item.type.startsWith('image/'))
      .map((item) => item.getAsFile())
      .filter((file): file is File => file !== null)
    if (files.length === 0) return
    event.preventDefault()
    if (busy.current) {
      setUploadError('请等当前文件上传完成后再粘贴截图')
      return
    }
    void uploadFiles(files)
  }

  /* 链接不能用 wrap：光标要停在 https:// 后面，好让人直接粘地址。 */
  const insertLink = act((v, s, e) => {
    const snippet = `[${v.slice(s, e) || '链接文字'}](https://)`
    const at = s + snippet.length - 1
    return { text: v.slice(0, s) + snippet + v.slice(e), from: at, to: at }
  })

  const tools = [
    { icon: Bold, title: '粗体', run: act((v, s, e) => wrap(v, s, e, '**', '粗体')) },
    { icon: Italic, title: '斜体', run: act((v, s, e) => wrap(v, s, e, '*', '斜体')) },
    { icon: Quote, title: '引用', run: act((v, s, e) => prefixLines(v, s, e, '> ')) },
    { icon: List, title: '无序列表', run: act((v, s, e) => prefixLines(v, s, e, '- ')) },
    { icon: ListOrdered, title: '有序列表', run: act((v, s, e) => prefixLines(v, s, e, (i) => `${i + 1}. `)) },
    { icon: Link2, title: '链接', run: insertLink },
  ]

  return (
    <div style={{ border: `1px solid ${invalid ? 'var(--red)' : 'var(--line)'}`, background: 'var(--bg)', minWidth: 0 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap', borderBottom: '1px solid var(--line2)', padding: '7px 8px' }}>
        {tools.map((t) => (
          <button
            key={t.title}
            type="button"
            className="hv-fg"
            title={t.title}
            aria-label={t.title}
            data-ui="icon-button"
            disabled={previewing}
            onClick={t.run}
            style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', width: 26, height: 26, border: 0, background: 'none', padding: 0, color: 'var(--fg3)', cursor: previewing ? 'not-allowed' : 'pointer', opacity: previewing ? 0.4 : 1 }}
          >
            <t.icon size={14} strokeWidth={1.7} />
          </button>
        ))}

        {uploadTarget && (
          <button
            type="button"
            className="hv-fg"
            title="上传文件/图片"
            aria-label="上传文件/图片"
            data-ui="icon-button"
            disabled={previewing || uploading > 0}
            onClick={() => picker.current?.click()}
            style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', width: 26, height: 26, border: 0, background: 'none', padding: 0, color: 'var(--fg3)', cursor: previewing || uploading > 0 ? 'not-allowed' : 'pointer', opacity: previewing || uploading > 0 ? 0.4 : 1 }}
          >
            <Upload size={14} strokeWidth={1.7} />
          </button>
        )}

        {visibleEvidence.length > 0 && (
          <button
            type="button"
            className="hv-fg"
            disabled={previewing}
            onClick={() => setPicking((v) => !v)}
            data-ui="text-button"
            style={{ display: 'flex', alignItems: 'center', gap: 5, border: 0, background: 'none', padding: '0 4px', font: 'inherit', fontSize: 12, color: 'var(--fg3)', cursor: previewing ? 'not-allowed' : 'pointer', opacity: previewing ? 0.4 : 1 }}
          >
            <Paperclip size={13} strokeWidth={1.7} />插入佐证
          </button>
        )}

        <div style={{ marginLeft: 'auto' }}>
          <Seg items={TABS} value={tab} onChange={setTab} pad="3px 11px" fs={12} />
        </div>
      </div>

      {uploadTarget && <input ref={picker} type="file" multiple hidden accept={evidenceAccept(undefined)} disabled={previewing || uploading > 0} onChange={(event) => {
        const files = Array.from(event.target.files ?? [])
        event.target.value = ''
        void uploadFiles(files)
      }} />}
      {uploading > 0 && <div role="status" style={{ padding: '8px 12px', borderBottom: '1px solid var(--line2)', fontSize: 12, color: 'var(--fg3)' }}>正在上传 {uploading} 份文件…</div>}
      {uploadError && <div role="alert" style={{ padding: '8px 12px', borderBottom: '1px solid var(--line2)', fontSize: 12, color: 'var(--red)', overflowWrap: 'anywhere' }}>{uploadError}</div>}

      {picking && !previewing && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 7, borderBottom: '1px solid var(--line2)', padding: '9px 10px', background: 'var(--sub)' }}>
          {visibleEvidence.map((item) => (
            <button
              key={item.id}
              type="button"
              className="hv-line-fg"
              disabled={item.status !== 'ready'}
              onClick={() => {
                insert(evidenceSnippet(item))
                setPicking(false)
              }}
              style={{ border: '1px solid var(--line)', background: 'var(--bg)', margin: 0, padding: '5px 10px', font: 'inherit', fontSize: 12, color: 'var(--fg2)', cursor: item.status === 'ready' ? 'pointer' : 'not-allowed', opacity: item.status === 'ready' ? 1 : 0.5, maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
            >
              {item.name}
            </button>
          ))}
        </div>
      )}

      {previewing ? (
        <div style={{ minHeight, padding: 12 }}>
          <RichText value={value} evidence={visibleEvidence} />
        </div>
      ) : (
        <textarea
          ref={area}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onPaste={pasteImages}
          placeholder={placeholder}
          style={{ display: 'block', width: '100%', minHeight, background: 'none', border: 0, padding: 12, color: 'var(--fg)', font: 'inherit', fontSize: 13, lineHeight: 1.7, resize: 'vertical' }}
        />
      )}
    </div>
  )
}
