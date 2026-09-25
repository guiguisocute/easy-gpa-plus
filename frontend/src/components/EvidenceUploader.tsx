/* 业务附件的投放区。

   拖放、选文件夹、粘贴截图这一套每个上传入口都要重写一遍，而它们真正的差别
   只有"文件到手之后往哪送"。所以这里分两层：

   · FilePicker      —— 只负责把文件收上来，交给调用方。表单还没保存、拿不到
                        对象 id 的地方（比如提案编辑器）用它先把文件攒在本地；
   · EvidenceUploader —— FilePicker 加一条上传通道加一张已传列表，给已经有
                        对象 id 的地方（申诉补传）直接用。

   一次传一份、串行发：并发上传在弱网下只会互相抢带宽，而且错误报出来分不清是哪一份。
   传完不提供"移除"——后端只允许删掉尚未 ready 的对象，已经落定的附件要靠业务动作撤销。 */

import { useRef, useState } from 'react'
import { Paperclip } from 'lucide-react'
import { Btn } from '@/components/ui'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { FileDropzone } from '@/components/FileDropzone'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { filesFromDrop } from '@/lib/dropFiles'
import { validateEvidenceFile } from '@/lib/evidence'
import type { Evidence } from '@/api/types'

export function FilePicker({
  title,
  actionLabel = '选择文件',
  busy = false,
  onFiles,
}: {
  title: string
  /** 忙的时候顶掉「选择文件」，用来显示进度。 */
  actionLabel?: string
  busy?: boolean
  onFiles: (files: File[]) => void
}) {
  const say = useApp((s) => s.say)
  const picker = useRef<HTMLInputElement>(null)
  const folderPicker = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)

  /* 类型与大小在后端还会再验一次；前端先拦是为了不让人白等一次上传。 */
  const take = (files: File[]) => {
    if (busy || !files.length) return
    for (const file of files) {
      const bad = validateEvidenceFile(file, undefined)
      if (bad) {
        say(bad)
        return
      }
    }
    onFiles(files)
  }

  return (
    <>
      <FileDropzone
        /* 截图直接 Ctrl+V 就能传——补材料最常见的动作就是贴一张截图。 */
        active={dragging}
        disabled={busy}
        icon={<Paperclip size={20} strokeWidth={1.5} />}
        title={dragging ? '松手就开始上传' : title}
        hint="可以选文件、选文件夹、直接拖进来，也可以在这儿粘截图"
        actions={
          <>
            <Btn disabled={busy} onClick={() => picker.current?.click()}>{busy ? actionLabel : '选择文件'}</Btn>
            <Btn disabled={busy} onClick={() => folderPicker.current?.click()}>选择文件夹</Btn>
          </>
        }
        onDragEnter={(event) => { event.preventDefault(); if (!busy) setDragging(true) }}
        onDragOver={(event) => { event.preventDefault(); event.dataTransfer.dropEffect = busy ? 'none' : 'copy' }}
        onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false) }}
        onDrop={(event) => {
          event.preventDefault()
          setDragging(false)
          if (busy) return
          void filesFromDrop(event.dataTransfer).then((files) => {
            if (!files.length) return say('这次拖进来没读到文件，换「选择文件」或「选择文件夹」试试')
            take(files)
          })
        }}
        onPaste={(event) => {
          const files = [...event.clipboardData.files]
          if (!files.length) return
          event.preventDefault()
          take(files)
        }}
      />

      <input
        ref={picker}
        type="file"
        multiple
        hidden
        disabled={busy}
        onChange={(event) => {
          take([...(event.target.files ?? [])])
          event.target.value = ''
        }}
      />
      <input
        ref={(node) => { folderPicker.current = node; node?.setAttribute('webkitdirectory', '') }}
        type="file"
        multiple
        hidden
        disabled={busy}
        onChange={(event) => {
          take([...(event.target.files ?? [])])
          event.target.value = ''
        }}
      />
    </>
  )
}

export function EvidenceUploader({
  items,
  loading = false,
  title,
  emptyHint,
  upload,
  onUploaded,
}: {
  items: Evidence[]
  loading?: boolean
  title: string
  emptyHint: string
  /** 传一份。onProgress 可以不调——不是每条通道都报得出进度。 */
  upload: (file: File, onProgress: (pct: number) => void) => Promise<unknown>
  onUploaded: (count: number) => void
}) {
  const say = useApp((s) => s.say)
  const [pct, setPct] = useState<number | null>(null)

  const send = async (files: File[]) => {
    setPct(0)
    try {
      for (const file of files) await upload(file, setPct)
      onUploaded(files.length)
    } catch (e) {
      say(e instanceof ApiError ? e.message : '上传失败')
    } finally {
      setPct(null)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 11 }}>
      <FilePicker
        title={title}
        busy={pct !== null}
        actionLabel={pct ? `上传中 ${pct}%` : '上传中…'}
        onFiles={(files) => void send(files)}
      />

      {loading ? (
        <div className="load-bar"><span /></div>
      ) : items.length > 0 ? (
        <EvidenceFiles items={items} />
      ) : (
        <div style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: 'var(--fg3)' }}>
          <Paperclip size={13} strokeWidth={1.7} />
          {emptyHint}
        </div>
      )}
    </div>
  )
}
