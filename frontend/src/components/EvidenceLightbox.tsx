/* 图片放大预览。佐证卡片网格与正文里内嵌的佐证图共用同一个——
   两处各写一遍，迟早会出现"卡片能按 Esc 关、正文里的不能"这种说不清的差别。 */

import { useEffect } from 'react'
import { X } from 'lucide-react'
import { Overlay } from '@/components/ui'

export interface Preview {
  url: string
  name: string
}

export function EvidenceLightbox({ preview, onClose }: { preview: Preview | null; onClose: () => void }) {
  useEffect(() => {
    if (!preview) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [preview, onClose])

  if (!preview) return null

  return (
    <Overlay>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={`预览 ${preview.name}`}
        onClick={onClose}
        /* 全屏看图必须压在所有内容之上：60 时它会被侧边抽屉（70）和 AI 整理面板（90）
           盖住——图打开了，人却只看得见半截。留在 Toast（120）之下，出错提示仍要可见。 */
        style={{ position: 'fixed', inset: 0, zIndex: 100, background: 'rgba(0,0,0,.82)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, padding: 28 }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 14, maxWidth: '100%' }}>
          <span style={{ fontSize: 13, color: '#fff', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{preview.name}</span>
          <button
            type="button"
            aria-label="关闭预览"
            data-ui="icon-button"
            onClick={onClose}
            style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', width: 28, height: 28, flex: 'none', border: '1px solid rgba(255,255,255,.35)', borderRadius: 999, background: 'none', color: '#fff', cursor: 'pointer' }}
          >
            <X size={14} strokeWidth={1.7} />
          </button>
        </div>
        <img
          src={preview.url}
          alt={preview.name}
          onClick={(event) => event.stopPropagation()}
          style={{ maxWidth: '100%', maxHeight: 'calc(100dvh - 140px)', objectFit: 'contain', background: 'var(--bg)' }}
        />
        <span style={{ fontSize: 12, color: 'rgba(255,255,255,.6)' }}>点击空白处关闭</span>
      </div>
    </Overlay>
  )
}
