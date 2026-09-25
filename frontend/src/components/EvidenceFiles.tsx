/* 已上传佐证的可视列表：图片出缩略图，其余按类型出图标，点开可预览或下载。

   预览链接是 10 分钟的预签名 URL，由 useEvidenceLink 缓存 9 分钟。不缓存的话，
   每渲染一次就重新签发一次，审计日志会被 evidence.url_issued 刷满。

   能不能"就地渲染"由后端按存下来的 media_type 判定（只放行图片与 PDF），
   前端只照着回包里的 inline 决定是开预览还是直接下载——这一道不能放到前端来判。 */

import { useState } from 'react'
import { Download, File, FileArchive, FileImage, FileSpreadsheet, FileText, Lock, Presentation } from 'lucide-react'
import { Pill } from '@/components/ui'
import { EvidenceLightbox, type Preview } from '@/components/EvidenceLightbox'
import { mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { evidenceUrl, useEvidenceLink } from '@/api/queries'
import { canThumbnail, evidenceKind, type EvidenceKind } from '@/lib/evidence'
import type { Evidence } from '@/api/types'
import { currentAgentPage, useAgentPage } from '@/stores/agentPage'

const kindIcon: Record<EvidenceKind, typeof File> = {
  image: FileImage,
  pdf: FileText,
  doc: FileText,
  sheet: FileSpreadsheet,
  slide: Presentation,
  text: FileText,
  archive: FileArchive,
  file: File,
}

const kindLabel: Record<EvidenceKind, string> = {
  image: '图片', pdf: 'PDF', doc: '文档', sheet: '表格',
  slide: '演示', text: '文本', archive: '压缩包', file: '文件',
}

const statusPill = (status: Evidence['status']) =>
  status === 'ready' ? { tone: 'ok' as const, text: '已附上' }
    : status === 'rejected' ? { tone: 'bad' as const, text: '已拒绝' }
      : { tone: 'warn' as const, text: '待确认' }

function Thumb({ item, kind, readOnly }: { item: Evidence; kind: EvidenceKind; readOnly: boolean }) {
  const [broken, setBroken] = useState(false)
  /* 只读时连缩略图都不取：缩略图走的就是下载那一个接口，学生在那一侧是 403，
     取一次失败一次，还会把审计刷满。只读的那一列一律出图标。 */
  const wantsThumb = !readOnly && item.status === 'ready' && canThumbnail(kind, item.name)
  const link = useEvidenceLink(item.id, wantsThumb && !broken)
  const Icon = kindIcon[kind]

  if (wantsThumb && !broken && link.data?.inline) {
    return (
      <img
        src={link.data.url}
        alt=""
        loading="lazy"
        onError={() => setBroken(true)}
        style={{ width: '100%', height: '100%', objectFit: 'cover', display: 'block' }}
      />
    )
  }
  return (
    <div style={{ width: '100%', height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 6, color: 'var(--fg3)' }}>
      <Icon size={22} strokeWidth={1.5} />
      <span style={{ ...mono('10px', '.02em') }}>{kindLabel[kind]}</span>
    </div>
  )
}

/* readOnly：只摆出有哪些材料，不给任何取文件的入口。
   学生的「举报台」用它——那一页要和小组的「扣分与异议」摆同一份东西，
   但佐证原件只对审核人和班级管理员开放，前端不给入口，后端也照样拦。 */
export function EvidenceFiles({ items, onRemove, readOnly = false }: { items: Evidence[]; onRemove?: (eid: string) => void; readOnly?: boolean }) {
  const say = useApp((s) => s.say)
  const [preview, setPreview] = useState<Preview | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  /* 预览和下载走同一个接口，只差 disposition：图片留在页内灯箱，
     PDF 交给浏览器自带阅读器，其余类型没有"在线看"这回事，直接下载。 */
  const act = async (item: Evidence, mode: 'open' | 'download') => {
    setBusy(item.id)
    try {
      const link = await evidenceUrl(item.id, mode === 'open' ? 'inline' : undefined)
      if (mode === 'open' && currentAgentPage()) useAgentPage.setState({ selectedEvidence: item.id })
      if (mode === 'download' || !link.inline) {
        window.open(link.url, '_blank', 'noopener')
        return
      }
      if (evidenceKind(link.mediaType, item.name) === 'image') setPreview({ url: link.url, name: item.name })
      else window.open(link.url, '_blank', 'noopener')
    } catch (e) {
      say(e instanceof ApiError ? e.message : '无法生成查看链接')
    } finally {
      setBusy(null)
    }
  }

  if (!items.length) return null

  return (
    <>
      <div data-r="evidence-grid" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(148px,1fr))', gap: 12 }}>
        {items.map((item) => {
          const kind = evidenceKind(item.mediaType, item.name)
          const ready = item.status === 'ready'
          const pill = statusPill(item.status)
          return (
            <div key={item.id} style={{ border: '1px solid var(--line)', display: 'flex', flexDirection: 'column', minWidth: 0 }}>
              <button
                type="button"
                disabled={readOnly || !ready || busy === item.id}
                onClick={() => void act(item, 'open')}
                title={readOnly ? `${item.name} · 佐证原件只对审核人和班级管理员开放` : ready ? `预览 ${item.name}` : '这份还没上传完成'}
                style={{
                  border: 0, borderBottom: '1px solid var(--line)', margin: 0, padding: 0,
                  aspectRatio: '4/3', background: 'var(--sub)', overflow: 'hidden',
                  cursor: readOnly ? 'default' : ready ? 'pointer' : 'not-allowed', opacity: ready || readOnly ? 1 : 0.55,
                }}
              >
                <Thumb item={item} kind={kind} readOnly={readOnly} />
              </button>

              <div style={{ padding: '9px 10px', display: 'flex', flexDirection: 'column', gap: 7, minWidth: 0 }}>
                <span title={item.name} style={{ fontSize: 12.5, lineHeight: 1.45, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.name}</span>
                <div style={{ display: 'flex', alignItems: 'center', gap: 7, minWidth: 0 }}>
                  <Pill tone={pill.tone}>{pill.text}</Pill>
                  <span style={{ ...mono('10.5px', '0'), color: 'var(--fg3)' }}>{f.bytes(item.sizeBytes)}</span>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', paddingTop: 7 }}>
                  {readOnly ? (
                    <span style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 12, color: 'var(--fg3)' }}>
                      <Lock size={12} strokeWidth={1.7} />不可下载
                    </span>
                  ) : (
                    <button
                      type="button"
                      className="hv-fg"
                      disabled={!ready}
                      onClick={() => void act(item, 'download')}
                      style={{ display: 'flex', alignItems: 'center', gap: 4, background: 'none', border: 0, padding: 0, font: 'inherit', fontSize: 12, color: 'var(--fg3)', cursor: ready ? 'pointer' : 'not-allowed' }}
                    >
                      <Download size={12} strokeWidth={1.7} />下载
                    </button>
                  )}
                  {onRemove && (
                    <button
                      type="button"
                      className="hv-red"
                      onClick={() => onRemove(item.id)}
                      style={{ marginLeft: 'auto', background: 'none', border: 0, padding: 0, font: 'inherit', fontSize: 12, color: 'var(--fg3)', cursor: 'pointer' }}
                    >
                      移除
                    </button>
                  )}
                </div>
              </div>
            </div>
          )
        })}
      </div>

      <EvidenceLightbox preview={preview} onClose={() => { setPreview(null); useAgentPage.setState({ selectedEvidence: '' }) }} />
    </>
  )
}
