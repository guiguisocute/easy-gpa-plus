/* 理由类正文的渲染器：审核意见、申诉理由、复评理由、扣分依据、终裁理由都走它。

   三条不能松的规矩：

   1. 不解析原生 HTML。react-markdown 默认就不解析（要另装 rehype-raw 才会），
      我们不装。学生和审核人写的东西直接进别人的浏览器，这道口子开了就收不回来。

   2. 图片只认 `evidence:<id>`，且那个 id 必须出现在本条记录自己的佐证清单里。
      不校验归属的话，谁都能写 `![](evidence:999)` 去蹭一张自己无权看、
      但读者恰好有权看的别人的附件——渲染出来还像是本条的材料。

   3. 外部 http(s) 图片不加载，只渲染成一个可点的链接。理由有三个：
      审核人的 IP 与 UA 不该因为读一条申诉就交给第三方图床；这些正文会进归档包，
      链接烂掉之后归档就复现不出来；而这套系统连"看过一次佐证"都要写审计日志，
      悄悄发一个不进日志的外部请求跟那个标准对不上。 */

import { useState } from 'react'
import Markdown, { defaultUrlTransform } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ExternalLink } from 'lucide-react'
import { mono } from '@/lib/style'
import { useEvidenceLink } from '@/api/queries'
import { EvidenceLightbox, type Preview } from '@/components/EvidenceLightbox'
import type { Evidence } from '@/api/types'
import { canThumbnail, evidenceKind } from '@/lib/evidence'

const EVIDENCE_PREFIX = 'evidence:'

/** 元素白名单。列表之外的一律丢弃——加标签要在这里明写，不靠"默认放行"。 */
const ALLOWED = [
  'p', 'br', 'strong', 'em', 'del', 'blockquote', 'hr',
  'ul', 'ol', 'li', 'input',
  'code', 'pre',
  'a', 'img',
  'h1', 'h2', 'h3', 'h4', 'h5',
  'table', 'thead', 'tbody', 'tr', 'th', 'td',
]

/* defaultUrlTransform 会把 `evidence:123` 当成一个不认识的协议直接抹成空串，
   所以先把它放行，其余（含 javascript:）仍旧交给默认那道。 */
const urlTransform = (url: string) =>
  url.startsWith(EVIDENCE_PREFIX) ? url : defaultUrlTransform(url)

/* 默认那一档给的是引用性正文（审核意见、依据）。lg 留给"这一页要读的主角"——
   学生自己写的申诉理由：字要大一号、颜色要正，否则和周围的元数据混成一片。 */
const SIZES = {
  md: { fontSize: 13, lineHeight: 1.8, color: 'var(--fg2)' },
  lg: { fontSize: 14.5, lineHeight: 1.9, color: 'var(--fg)' },
} as const

/** 正文里内嵌的佐证图。链接走 useEvidenceLink（缓存 9 分钟），不然滚动一次就刷一批审计。 */
function EvidenceImage({ item, onOpen }: { item: Evidence; onOpen: (p: Preview) => void }) {
  const [broken, setBroken] = useState(false)
  const link = useEvidenceLink(item.id, item.status === 'ready' && !broken)

  if (item.status !== 'ready' || broken || link.isPending || link.isError) {
    return (
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, border: '1px solid var(--line)', background: 'var(--sub)', padding: '5px 10px', fontSize: 12.5, color: 'var(--fg3)' }}>
        {item.name}
        <span style={mono('10.5px', '.02em')}>
          {item.status !== 'ready' ? '处理中' : broken ? '无法显示' : link.isError ? '读取失败' : '加载中'}
        </span>
      </span>
    )
  }
  if (!link.data.inline || !canThumbnail(evidenceKind(item.mediaType, item.name), item.name)) {
    return (
      <a
        href={link.data.url}
        target="_blank"
        rel="noopener noreferrer"
        style={{ display: 'inline-flex', alignItems: 'center', gap: 6, border: '1px solid var(--line)', background: 'var(--sub)', padding: '5px 10px', fontSize: 12.5, color: 'var(--fg2)', textDecoration: 'underline', textUnderlineOffset: 2 }}
      >
        {item.name}
        <span style={mono('10.5px', '.02em')}>打开附件</span>
      </a>
    )
  }
  return (
    <button
      type="button"
      onClick={() => onOpen({ url: link.data.url, name: item.name })}
      title={`放大 ${item.name}`}
      style={{ display: 'block', border: '1px solid var(--line)', background: 'var(--sub)', margin: '4px 0 10px', padding: 0, maxWidth: 420, cursor: 'zoom-in' }}
    >
      <img
        src={link.data.url}
        alt={item.name}
        loading="lazy"
        onError={() => setBroken(true)}
        style={{ display: 'block', width: '100%', height: 'auto' }}
      />
    </button>
  )
}

/** 引用了不属于本条的附件时给出的占位。不静默吞掉——读者有权知道正文里原本想放一张图。 */
function ForeignRef({ label }: { label: string }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, border: '1px dashed var(--line)', padding: '4px 9px', fontSize: 12, color: 'var(--fg3)' }}>
      {label || '附件'} · 不属于本条记录，未显示
    </span>
  )
}

export function RichText({ value, evidence = [], size = 'md' }: { value: string; evidence?: Evidence[]; size?: keyof typeof SIZES }) {
  const [preview, setPreview] = useState<Preview | null>(null)
  const byId = new Map(evidence.map((e) => [e.id, e]))
  const trimmed = value?.trim() ?? ''
  const text = SIZES[size]
  if (!trimmed) return null

  return (
    <div style={{ minWidth: 0, textWrap: 'pretty' }}>
      <Markdown
        remarkPlugins={[remarkGfm]}
        allowedElements={ALLOWED}
        urlTransform={urlTransform}
        components={{
          p: ({ children }) => <p style={{ ...text, margin: '0 0 9px' }}>{children}</p>,
          strong: ({ children }) => <strong style={{ fontWeight: 600, color: 'var(--fg)' }}>{children}</strong>,
          del: ({ children }) => <del style={{ color: 'var(--fg3)' }}>{children}</del>,
          h1: ({ children }) => <h1 style={{ fontSize: 15, fontWeight: 600, letterSpacing: '-.02em', margin: '14px 0 8px' }}>{children}</h1>,
          h2: ({ children }) => <h2 style={{ fontSize: 14, fontWeight: 600, letterSpacing: '-.015em', margin: '14px 0 7px' }}>{children}</h2>,
          h3: ({ children }) => <h3 style={{ fontSize: 13.5, fontWeight: 600, letterSpacing: '-.015em', margin: '14px 0 7px' }}>{children}</h3>,
          h4: ({ children }) => <h4 style={{ fontSize: 13, fontWeight: 600, margin: '12px 0 6px' }}>{children}</h4>,
          h5: ({ children }) => <h5 style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', margin: '12px 0 6px' }}>{children}</h5>,
          ul: ({ children }) => <ul style={{ ...text, margin: '0 0 9px', paddingLeft: 20 }}>{children}</ul>,
          ol: ({ children }) => <ol style={{ ...text, margin: '0 0 9px', paddingLeft: 20 }}>{children}</ol>,
          li: ({ children }) => <li style={{ margin: '0 0 3px' }}>{children}</li>,
          blockquote: ({ children }) => (
            <blockquote style={{ borderLeft: '2px solid var(--line)', margin: '0 0 9px', padding: '2px 0 2px 12px', color: 'var(--fg3)' }}>{children}</blockquote>
          ),
          hr: () => <hr style={{ border: 0, borderTop: '1px solid var(--line2)', margin: '14px 0' }} />,
          code: ({ children }) => (
            <code style={{ ...mono('12px', '0'), color: 'var(--fg2)', background: 'var(--sub)', padding: '1px 5px' }}>{children}</code>
          ),
          pre: ({ children }) => (
            <pre style={{ background: 'var(--sub)', border: '1px solid var(--line2)', padding: 12, margin: '0 0 9px', overflowX: 'auto', ...mono('12px', '0'), lineHeight: 1.65 }}>{children}</pre>
          ),
          table: ({ children }) => (
            <div data-r="scroll" style={{ margin: '0 0 9px' }}>
              <table style={{ borderCollapse: 'collapse', fontSize: 12.5, minWidth: 320 }}>{children}</table>
            </div>
          ),
          th: ({ children }) => (
            <th style={{ textAlign: 'left', fontWeight: 600, color: 'var(--fg2)', borderBottom: '1px solid var(--line)', padding: '7px 14px 7px 0' }}>{children}</th>
          ),
          td: ({ children }) => (
            <td style={{ borderBottom: '1px solid var(--line2)', padding: '7px 14px 7px 0', color: 'var(--fg2)' }}>{children}</td>
          ),
          /* 内部锚点在这套单页应用里没有意义，一律当外链开新标签。 */
          a: ({ href, children }) => (
            <a
              href={href}
              target="_blank"
              rel="noopener noreferrer"
              style={{ color: 'var(--fg)', textDecoration: 'underline', textUnderlineOffset: 2 }}
            >
              {children}
            </a>
          ),
          img: ({ src, alt }) => {
            const url = typeof src === 'string' ? src : ''
            if (!url.startsWith(EVIDENCE_PREFIX)) {
              /* 外链图片不发请求。渲染成链接，点不点由读者自己决定。 */
              return (
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 12.5, color: 'var(--fg2)', textDecoration: 'underline', textUnderlineOffset: 2 }}
                >
                  <ExternalLink size={12} strokeWidth={1.7} />
                  {alt || '外部图片'} · 站外链接，不自动加载
                </a>
              )
            }
            const item = byId.get(url.slice(EVIDENCE_PREFIX.length))
            if (!item) return <ForeignRef label={alt ?? ''} />
            return <EvidenceImage item={item} onOpen={setPreview} />
          },
        }}
      >
        {trimmed}
      </Markdown>
      <EvidenceLightbox preview={preview} onClose={() => setPreview(null)} />
    </div>
  )
}
