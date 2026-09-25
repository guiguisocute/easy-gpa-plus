/* 设计原语。取自 EDU-POWER-PUSH 的 components/ui.tsx 与 admin/ui.tsx，
   按本项目原型补齐 Pill / PageHead / Table / Timeline / Bar 几个反复出现的形状。

   一律内联样式而不是 CSS Module：原型本身就是内联样式稿，一一对照时不需要在两套命名之间翻译；
   悬停与焦点这类伪类内联写不了，才落到 global.css 的 .hv-* 工具类。 */

import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ArrowLeft, Moon, Sun } from 'lucide-react'
import { mono, num } from '@/lib/style'
import { useApp } from '@/stores/app'

/** 品牌标记：直角红底方块 + 白色柱状图，与 favicon 同形。
    它同时要当浏览器图标用，所以只有实心块、没有描边：这个标记最小会被缩到 16px，
    描边到那个尺寸不足 1px，会被抗锯齿抹成一团粉色（上一版的白色天秤就是这么糊掉的）。
    同理坐标全取偶数，32 的 viewBox 缩到 16px 是 2:1，偶数才落在整像素边界上。
    与校徽的联系交给红底方块和这套直角几何，校徽本身的印章外圈和 [N] 缺口在 16px 下留不住。
    改这里必须同步 public/favicon.svg 与原型 dc.html 里的三处内联副本。 */
export function BrandMark({ size = 20 }: { size?: number }) {
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden="true" style={{ display: 'block', flex: 'none' }}>
      <rect width="32" height="32" fill="var(--red)" />
      <g fill="#fff">
        <rect x="2" y="16" width="6" height="8" />
        <rect x="12" y="10" width="6" height="14" />
        <rect x="22" y="4" width="6" height="20" />
        <rect x="2" y="26" width="28" height="4" />
      </g>
    </svg>
  )
}

/** 深浅色切换。登录前的开屏与登录后的顶栏都要有，所以放在这里给两处共用。 */
export function ThemeToggle() {
  const theme = useApp((s) => s.theme)
  const toggleTheme = useApp((s) => s.toggleTheme)
  return (
    <button
      type="button"
      className="hv-line-fg"
      onClick={toggleTheme}
      title="切换深浅色"
      aria-label="切换深浅色"
      data-ui="icon-button"
      style={{ background: 'none', border: '1px solid var(--line)', margin: 0, padding: 0, width: 32, height: 32, borderRadius: 999, cursor: 'pointer', color: 'var(--fg2)', display: 'flex', alignItems: 'center', justifyContent: 'center', flex: 'none' }}
    >
      {theme === 'dark' ? <Sun size={14} strokeWidth={1.6} /> : <Moon size={14} strokeWidth={1.6} />}
    </button>
  )
}

/** 描边图标。原型全站 stroke-width 1.6，比 lucide 默认的 2 更适合 13px 正文。 */
export function Icon({ d1, d2, size = 17 }: { d1: string; d2?: string; size?: number }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      style={{ display: 'block' }}
    >
      <path d={d1} />
      {d2 ? <path d={d2} /> : null}
    </svg>
  )
}

/* ---- 页头 ---- */

export function PageHead({
  en,
  title,
  desc,
  side,
}: {
  en: string
  title: string
  desc?: string
  side?: ReactNode
}) {
  return (
    <div
      data-r="hdr"
      style={{ display: 'flex', alignItems: 'flex-end', gap: 20, flexWrap: 'wrap', padding: '38px 0 26px' }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 9, minWidth: 0 }}>
        <div style={mono()}>{en}</div>
        <div style={{ fontSize: 26, fontWeight: 600, letterSpacing: '-.035em' }}>{title}</div>
        {desc && (
          <div style={{ fontSize: 12.5, color: 'var(--fg3)', maxWidth: 640, textWrap: 'pretty' }}>{desc}</div>
        )}
      </div>
      {side && (
        <div data-r="hdrside" style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
          {side}
        </div>
      )}
    </div>
  )
}

/* 详情页的返回链接。它是这一页的第一件东西，所以自己负责和顶栏之间的留白——
   PageHead 的 38px 上边距只有在页头是第一件东西时才起作用，返回链接一旦排在它前面，
   整页就贴着顶栏长出来了。仲裁台和小组申诉台共用同一个形状，两边的位置才对得齐。 */
export function BackLink({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className="hv-fg"
      data-ui="text-button"
      onClick={onClick}
      style={{ display: 'flex', alignItems: 'center', gap: 8, background: 'none', border: 0, margin: '30px 0 0', padding: 0, font: 'inherit', fontSize: 12.5, color: 'var(--fg2)', cursor: 'pointer' }}
    >
      <ArrowLeft size={14} strokeWidth={1.7} />
      {label}
    </button>
  )
}

/** 区块小标题。页面内的二级分组几乎都长这样。 */
export function Sub({ title, note, actions }: { title: string; note?: string; actions?: ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12, paddingBottom: 14, flexWrap: 'wrap' }}>
      <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>{title}</span>
      {note && <span style={{ fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>{note}</span>}
      {actions && <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, flexWrap: 'wrap' }}>{actions}</div>}
    </div>
  )
}

/* ---- 按钮 ---- */

export function Btn({
  children,
  onClick,
  primary,
  danger,
  tone,
  disabled,
  title,
  type = 'button',
}: {
  children: ReactNode
  onClick?: () => void
  primary?: boolean
  danger?: boolean
  tone?: 'ok' | 'warn' | 'bad'
  disabled?: boolean
  title?: string
  type?: 'button' | 'submit'
}) {
  const fill = tone === 'ok' ? 'var(--ok)' : tone === 'warn' ? 'var(--warn)' : tone === 'bad' || danger ? 'var(--red)' : 'var(--fg)'
  const ink = tone === 'ok' ? 'var(--ok)' : tone === 'warn' ? 'var(--warn)' : tone === 'bad' || danger ? 'var(--red)' : 'var(--fg2)'
  return (
    <button
      type={type}
      data-ui="button"
      className={disabled ? undefined : primary ? 'hv-op82' : 'hv-line-fg'}
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        margin: 0,
        padding: '9px 18px',
        borderRadius: 999,
        font: 'inherit',
        fontSize: 12.5,
        fontWeight: primary ? 600 : 500,
        cursor: disabled ? 'not-allowed' : 'pointer',
        background: primary ? fill : 'none',
        color: primary ? 'var(--onAccent)' : ink,
        border: `1px solid ${primary || tone || danger ? fill : 'var(--line)'}`,
        opacity: disabled ? 0.45 : 1,
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </button>
  )
}

/** 审核结论选项。选中后通过绿、调整黄、驳回红；未选中与原先一样。 */
export function ChoiceChip({
  on,
  tone,
  children,
  onClick,
}: {
  on: boolean
  tone: 'ok' | 'warn' | 'bad'
  children: ReactNode
  onClick: () => void
}) {
  const fill = tone === 'ok' ? 'var(--ok)' : tone === 'warn' ? 'var(--warn)' : 'var(--red)'
  return (
    <button
      type="button"
      className="hv-op82"
      data-ui="button"
      onClick={onClick}
      aria-pressed={on}
      style={{
        margin: 0,
        padding: '10px 18px',
        borderRadius: 999,
        font: 'inherit',
        fontSize: 13.5,
        fontWeight: on ? 600 : 400,
        cursor: 'pointer',
        background: on ? fill : 'var(--bg)',
        color: on ? 'var(--onAccent)' : 'var(--fg2)',
        border: `1px solid ${on ? fill : 'var(--line)'}`,
        display: 'inline-flex',
        alignItems: 'center',
        gap: 8,
      }}
    >
      {children}
    </button>
  )
}

/** 纯文字按钮。表格行尾的「查看 / 改派 / 撤回」用它，不抢主按钮的注意力。 */
export function TextBtn({
  children,
  onClick,
  tone = 'var(--fg2)',
  disabled,
  title,
}: {
  children: ReactNode
  onClick?: () => void
  tone?: string
  disabled?: boolean
  title?: string
}) {
  return (
    <button
      type="button"
      className={disabled ? undefined : 'hv-red'}
      data-ui="text-button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        background: 'none',
        border: 0,
        margin: 0,
        padding: 0,
        font: 'inherit',
        fontSize: 12.5,
        color: disabled ? 'var(--fg3)' : tone,
        cursor: disabled ? 'not-allowed' : 'pointer',
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </button>
  )
}

/* ---- 分段控件 ---- */

export interface SegItem<T extends string> {
  key: T
  label: string
}

export function Seg<T extends string>({
  items,
  value,
  onChange,
  pad = '5px 13px',
  fs = 12.5,
}: {
  items: SegItem<T>[]
  value: T
  onChange: (k: T) => void
  pad?: string
  fs?: number
}) {
  return (
    /* inline-flex 而不是 flex：分段控件要按内容宽度收缩，
       放进块级容器时用 flex 会被拉成整行宽。 */
    <div data-ui="seg" style={{ display: 'inline-flex', padding: 3, border: '1px solid var(--line)', borderRadius: 999, gap: 2, flexWrap: 'wrap', alignSelf: 'flex-start' }}>
      {items.map((it) => {
        const on = it.key === value
        return (
          <button
            key={it.key}
            type="button"
            onClick={() => onChange(it.key)}
            aria-pressed={on}
            style={{
              border: 0,
              margin: 0,
              padding: pad,
              borderRadius: 999,
              font: 'inherit',
              fontSize: fs,
              cursor: 'pointer',
              fontWeight: on ? 600 : 400,
              background: on ? 'var(--fg)' : 'transparent',
              color: on ? 'var(--bg)' : 'var(--fg2)',
              transition: 'background .16s, color .16s',
              whiteSpace: 'nowrap',
            }}
          >
            {it.label}
          </button>
        )
      })}
    </div>
  )
}

/** 下划线页签。班级看板「你的任务」和仲裁台共用：选中的那一项底下一条红线，旁边挂条数。 */
export function LineTabs<T extends string>({
  items,
  value,
  onChange,
  trailing,
}: {
  items: { key: T; label: string; count?: number }[]
  value: T
  onChange: (k: T) => void
  trailing?: ReactNode
}) {
  return (
    <div data-ui="line-tabs" style={{ display: 'flex', gap: 2, borderBottom: '1px solid var(--line)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
      {items.map((it) => {
        const on = it.key === value
        return (
          <button
            key={it.key}
            type="button"
            onClick={() => onChange(it.key)}
            aria-pressed={on}
            style={{
              background: 'none',
              border: 0,
              borderBottom: `2px solid ${on ? 'var(--red)' : 'transparent'}`,
              margin: '0 22px 0 0',
              padding: '0 4px 10px',
              font: 'inherit',
              fontSize: 13,
              cursor: 'pointer',
              color: on ? 'var(--fg)' : 'var(--fg3)',
              fontWeight: on ? 600 : 400,
            }}
          >
            {it.label}
            {it.count != null && (
              <span style={{ ...mono('11.5px', '0'), color: 'var(--fg3)', marginLeft: 6, lineHeight: 1.3 }}>{it.count}</span>
            )}
          </button>
        )
      })}
      {trailing && <div style={{ marginLeft: 'auto', paddingBottom: 9 }}>{trailing}</div>}
    </div>
  )
}

/** 开关。锁定项（护栏里的"不可关"）用 locked，视觉上仍是开，但点不动。 */
export function Toggle({
  on,
  onClick,
  locked,
  size = 'lg',
}: {
  on: boolean
  onClick?: () => void
  locked?: boolean
  size?: 'lg' | 'sm'
}) {
  const lg = size === 'lg'
  const w = lg ? 46 : 42
  const h = lg ? 27 : 25
  const k = lg ? 21 : 19
  const x = on ? (lg ? 19 : 17) : 0
  return (
    <button
      type="button"
      onClick={locked ? undefined : onClick}
      disabled={locked}
      data-ui="toggle"
      aria-pressed={on}
      title={locked ? '该护栏被锁定，不可关闭' : undefined}
      style={{
        border: 0,
        margin: 0,
        padding: 3,
        flex: 'none',
        width: w,
        height: h,
        borderRadius: 999,
        cursor: locked ? 'not-allowed' : 'pointer',
        transition: 'background .16s',
        background: on ? 'var(--red)' : 'var(--line)',
        opacity: locked ? 0.55 : 1,
      }}
    >
      <span
        style={{
          display: 'block',
          width: k,
          height: k,
          borderRadius: 99,
          background: '#fff',
          transition: 'transform .16s',
          transform: `translateX(${x}px)`,
        }}
      />
    </button>
  )
}

/* ---- 状态显示 ---- */

export type Tone = 'ok' | 'warn' | 'bad' | 'idle'

const TONE_FG: Record<Tone, string> = {
  ok: 'var(--ok)',
  warn: 'var(--warn)',
  bad: 'var(--red)',
  idle: 'var(--fg2)',
}
const TONE_BG: Record<Tone, string> = {
  ok: 'var(--okBg)',
  warn: 'var(--warnBg)',
  bad: 'var(--redBg)',
  idle: 'var(--sub)',
}

/* 铺满视口的浮层必须挂到 body 上。页面根节点都带 `animation: rise … both`，
   动画跑完停在 `transform: none`，但 Chrome 仍把它当成 fixed 定位的包含块——
   于是 `inset: 0` 的遮罩只铺满正文那一块，左边的侧栏和顶栏露在外面，抽屉也跟着
   缩水。这不是某一个浮层的毛病，凡是渲染在页面里的 fixed 浮层都要走这一层。 */
export function Overlay({ children }: { children: ReactNode }) {
  return createPortal(children, document.body)
}

/* 把页面自己的控件送进顶栏。顶栏归 Shell 渲染，页面在自己的 render 里还拿不到它的
   DOM 节点（整棵树是一次性提交的），所以先挂载一次拿到节点，再补一次渲染画进去。
   页面卸载时 portal 一起消失，顶栏自动回到原样。 */
export function TopbarSlot({ children }: { children: ReactNode }) {
  const [host, setHost] = useState<HTMLElement | null>(null)
  useEffect(() => {
    setHost(document.querySelector<HTMLElement>('[data-r="topslot"]'))
  }, [])
  return host ? createPortal(children, host) : null
}

export function Pill({ tone = 'idle', children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        padding: '3px 9px',
        borderRadius: 999,
        fontSize: 11.5,
        fontWeight: 500,
        whiteSpace: 'nowrap',
        background: TONE_BG[tone],
        color: TONE_FG[tone],
      }}
    >
      {children}
    </span>
  )
}

export function Dot({ tone = 'idle' }: { tone?: Tone }) {
  return <span style={{ width: 7, height: 7, flex: 'none', background: TONE_FG[tone], display: 'inline-block' }} />
}

/** 一行「名称 — 值」。面板里到处在用。 */
export function Row({ label, value, tone }: { label: ReactNode; value: ReactNode; tone?: string }) {
  return (
    <div data-ui="value-row" style={{ display: 'flex', alignItems: 'baseline', gap: 14, padding: '10px 0 12px', borderBottom: '1px solid var(--line2)' }}>
      <span style={{ fontSize: 12.5, color: 'var(--fg3)', flex: 'none' }}>{label}</span>
      <span style={{ marginLeft: 'auto', textAlign: 'right', fontSize: 12.5, color: tone || 'var(--fg)', minWidth: 0, wordBreak: 'break-word', ...num }}>
        {value}
      </span>
    </div>
  )
}

/** KPI 方块。四联排时外面套 [data-r="stats"] 的网格。 */
export function Stat({
  en,
  value,
  unit,
  note,
  tone,
  pct,
}: {
  en: string
  value: string
  unit?: string
  note?: string
  tone?: string
  pct?: string
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, paddingRight: 24, minWidth: 0 }}>
      <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>{en}</div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
        <span data-ui="stat-value" data-long={value.length > 8 || undefined} style={{ minWidth: 0, overflowWrap: 'anywhere', fontSize: 30, fontWeight: 600, letterSpacing: '-.04em', color: tone || 'var(--fg)', ...num }}>{value}</span>
        {unit && <span style={{ fontSize: 13, color: 'var(--fg3)' }}>{unit}</span>}
      </div>
      {note && <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.6, textWrap: 'pretty' }}>{note}</div>}
      {pct && (
        <div style={{ height: 2, background: 'var(--line2)' }}>
          <div style={{ height: 2, background: 'var(--red)', width: pct }} />
        </div>
      )}
    </div>
  )
}

export function StatGrid({ cols = 4, children }: { cols?: number; children: ReactNode }) {
  return (
    <div
      data-r="stats"
      style={{
        display: 'grid',
        gridTemplateColumns: `repeat(${cols},1fr)`,
        gap: 0,
        padding: '26px 0',
        borderTop: '1px solid var(--line)',
        borderBottom: '1px solid var(--line)',
      }}
    >
      {children}
    </div>
  )
}

/** 说明性空态：说清楚为什么没有内容，而不是留一片白。 */
export function Empty({ title, desc }: { title: string; desc?: string }) {
  return (
    <div style={{ padding: '22px 0' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <span style={{ width: 5, height: 5, background: 'var(--fg3)', flex: 'none' }} />
        <span style={{ fontSize: 13, color: 'var(--fg2)' }}>{title}</span>
      </div>
      {desc ? <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 10, textWrap: 'pretty' }}>{desc}</div> : null}
    </div>
  )
}

/* ---- 表格 ----
   发丝分割线 + 无边框。窄屏靠外层 [data-r="scroll"] 横向滚动，不做列折叠：
   综测的表几乎都是"一行一人一条目"，折叠后反而读不出对照关系。 */

export function Table({ cols, children }: { cols: string; children: ReactNode }) {
  const scroll = useRef<HTMLDivElement>(null)
  const [overflowing, setOverflowing] = useState(false)
  useEffect(() => {
    const el = scroll.current
    if (!el) return
    const update = () => setOverflowing(el.scrollWidth > el.clientWidth + 1)
    const observer = new ResizeObserver(update)
    observer.observe(el)
    if (el.firstElementChild) observer.observe(el.firstElementChild)
    update()
    return () => observer.disconnect()
  }, [])
  return (
    <div style={{ minWidth: 0, maxWidth: '100%' }}>
      {overflowing && <div className="table-scroll-hint">左右滑动查看完整表格 →</div>}
      <div ref={scroll} data-r="scroll" tabIndex={overflowing ? 0 : undefined} role={overflowing ? 'region' : undefined} aria-label={overflowing ? '可横向滚动的表格' : undefined}>
        <div style={{ minWidth: 720, display: 'grid', gridTemplateColumns: cols }} role="table">
          {children}
        </div>
      </div>
    </div>
  )
}

export function THead({ cells }: { cells: ReactNode[] }) {
  return (
    <div style={{ display: 'contents' }} role="row">
      {cells.map((c, i) => (
        <div
          key={i}
          role="columnheader"
          style={{
            fontSize: 12,
            fontWeight: 600,
            letterSpacing: '.01em',
            color: 'var(--fg2)',
            padding: '0 12px 10px 0',
            borderBottom: '1px solid var(--line)',
          }}
        >
          {c}
        </div>
      ))}
    </div>
  )
}

/* 整行可点时，只把第一格做成可聚焦的 role="button"，其余格子仍然响应鼠标。
   若每格都给 tabIndex，一行就会产生 N 个 Tab 停靠点，键盘用户翻一张表要按上百次。
   label 用来给这个停靠点一句话说明，否则读屏只会念出第一格里的编号。 */
export function TRow({
  cells,
  onClick,
  bg,
  label,
  expanded,
  controls,
}: {
  cells: ReactNode[]
  onClick?: () => void
  bg?: string
  label?: string
  expanded?: boolean
  controls?: string
}) {
  return (
    <div style={{ display: 'contents' }} role="row">
      {cells.map((c, i) => (
        <div
          key={i}
          role={onClick && i === 0 ? 'button' : 'cell'}
          tabIndex={onClick && i === 0 ? 0 : undefined}
          aria-label={onClick && i === 0 ? label : undefined}
          aria-expanded={onClick && i === 0 ? expanded : undefined}
          aria-controls={onClick && i === 0 ? controls : undefined}
          onClick={onClick}
          onKeyDown={
            onClick && i === 0
              ? (e) => {
                  if (e.key !== 'Enter' && e.key !== ' ') return
                  e.preventDefault()
                  onClick()
                }
              : undefined
          }
          className={onClick ? 'hv-sub' : undefined}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            fontSize: 12.5,
            color: 'var(--fg2)',
            padding: '13px 12px 13px 0',
            borderBottom: '1px solid var(--line2)',
            background: bg || 'transparent',
            cursor: onClick ? 'pointer' : undefined,
            minWidth: 0,
          }}
        >
          {c}
        </div>
      ))}
    </div>
  )
}

/* ---- 输入 ---- */

export function Field({ label, hint, required, children }: { label: string; hint?: ReactNode; required?: boolean; children: ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 7, minWidth: 0 }}>
      <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>
        {label}
        {required && <RequiredMark />}
      </span>
      {children}
      {hint && <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6, textWrap: 'pretty' }}>{hint}</span>}
    </label>
  )
}

/** 必填星号。用 abbr 而不是裸 * ——光一个星号，读屏和鼠标悬停都问不出它是什么意思。 */
export function RequiredMark() {
  return <abbr title="必填" style={{ color: 'var(--red)', marginLeft: 3, textDecoration: 'none', cursor: 'help' }}>*</abbr>
}

/* ---- 结构块 ---- */

/** 左右两栏。窄屏由 global.css 的 [data-r="splitcol"] 规则收成一栏。 */
export function Split({ cols = '1.35fr 1fr', children }: { cols?: string; children: ReactNode }) {
  return (
    <div data-r="split" style={{ display: 'grid', gridTemplateColumns: cols, gap: 0 }}>
      {children}
    </div>
  )
}

export function SplitCol({ children, first }: { children: ReactNode; first?: boolean }) {
  return (
    <div
      data-r={first ? 'splitfirst' : 'splitcol'}
      style={first ? { minWidth: 0, paddingRight: 34 } : { minWidth: 0, borderLeft: '1px solid var(--line)', paddingLeft: 34 }}
    >
      {children}
    </div>
  )
}

/* ---- 时间轴 ----

   横着排。竖着排时每一条独占一整行，六个事件就吃掉半屏，而每行真正有内容的
   只有左边一个标题和右边一个时间戳，中间是一大片空白。

   末尾那一格表示"这条现在到哪儿了"：还在走的转圈，正常走完打勾，出岔子打感叹号。
   前面那些都是已经发生过的事实，一律打勾。 */

export type StepState = 'done' | 'active' | 'error'

export interface Step {
  title: string
  desc?: string
  at: string
  tone?: string
  state?: StepState
}

function StepMark({ state, tone }: { state: StepState; tone?: string }) {
  const color = state === 'error' ? 'var(--red)' : state === 'active' ? 'var(--warn)' : tone || 'var(--ok)'
  if (state === 'active') {
    return (
      <span
        role="img"
        aria-label="进行中"
        style={{
          width: 17, height: 17, flex: 'none', borderRadius: 999,
          border: `2px solid ${color}`, borderTopColor: 'transparent',
          animation: 'spin .8s linear infinite',
        }}
      />
    )
  }
  return (
    <span
      role="img"
      aria-label={state === 'error' ? '异常' : '已完成'}
      style={{ width: 17, height: 17, flex: 'none', borderRadius: 999, background: color, color: '#fff', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
    >
      {state === 'error' ? (
        <span style={{ fontSize: 11, fontWeight: 700, lineHeight: 1 }}>!</span>
      ) : (
        <svg viewBox="0 0 24 24" width="11" height="11" fill="none" stroke="currentColor" strokeWidth="3.2" strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block' }}>
          <path d="M5 13l4 4L19 7" />
        </svg>
      )}
    </span>
  )
}

/* 连着好几条一样的（比如「审核人查看」×4）合成一格，标上次数。
   横排时那种重复会把真正的节点挤出屏幕，而它们说的本来就是同一件事发生了几次。 */
function collapse(items: Step[]): (Step & { times: number })[] {
  const out: (Step & { times: number })[] = []
  for (const it of items) {
    const last = out[out.length - 1]
    if (last && last.title === it.title && (last.state ?? 'done') === (it.state ?? 'done')) {
      last.times += 1
      last.at = it.at
      continue
    }
    out.push({ ...it, times: 1 })
  }
  return out
}

export function Timeline({ items, layout = 'horizontal' }: { items: Step[]; layout?: 'horizontal' | 'vertical' }) {
  const steps = collapse(items)
  if (steps.length === 0) return null
  if (layout === 'vertical') return (
    <ol aria-label="处理轨迹" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
      {steps.map((it, i) => (
        <li key={i} style={{ display: 'flex', gap: 12, minWidth: 0 }}>
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', paddingTop: 3 }}>
            <StepMark state={it.state ?? 'done'} tone={it.tone} />
            {i < steps.length - 1 && <span style={{ width: 1, flex: 1, minHeight: 16, background: 'var(--line)' }} />}
          </div>
          <div style={{ flex: 1, minWidth: 0, paddingBottom: 18 }}>
            <div style={{ display: 'flex', alignItems: 'baseline', flexWrap: 'wrap', gap: '4px 12px' }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, overflowWrap: 'anywhere' }}>{it.title}{it.times > 1 && ` ×${it.times}`}</span>
              <span style={{ ...mono('11px', '.02em'), color: 'var(--fg3)' }}>{it.at}</span>
            </div>
            {it.desc && <div style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.6, marginTop: 4, overflowWrap: 'anywhere' }}>{it.desc}</div>}
          </div>
        </li>
      ))}
    </ol>
  )
  return (
    <div data-r="scroll">
      <div style={{ display: 'flex', alignItems: 'flex-start', minWidth: 'min-content', paddingBottom: 2 }}>
        {steps.map((it, i) => {
          const state = it.state ?? 'done'
          return (
            <div key={i} style={{ flex: '1 1 0', minWidth: 96, display: 'flex', flexDirection: 'column', gap: 7 }}>
              <div style={{ display: 'flex', alignItems: 'center' }}>
                <span style={{ height: 1, flex: 1, background: i === 0 ? 'transparent' : 'var(--line)' }} />
                <StepMark state={state} tone={it.tone} />
                <span style={{ height: 1, flex: 1, background: i === steps.length - 1 ? 'transparent' : 'var(--line)' }} />
              </div>
              <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'center', gap: 5, padding: '0 6px' }}>
                <span style={{ minWidth: 0, overflowWrap: 'anywhere', fontSize: 12.5, fontWeight: 600, color: 'var(--fg)', textAlign: 'center' }}>{it.title}</span>
                {it.times > 1 && <span style={mono('10.5px', '0')}>×{it.times}</span>}
              </div>
              <span style={{ ...mono('11px', '.02em'), textAlign: 'center' }}>{it.at}</span>
              {it.desc && (
                <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.6, textAlign: 'center', padding: '0 6px', textWrap: 'pretty' }}>{it.desc}</span>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

/** 细横条。进度、占比、封顶余量都用同一个形状。 */
export function Bar({ pct, tone = 'var(--red)', h = 2 }: { pct: string; tone?: string; h?: number }) {
  return (
    <div style={{ height: h, background: 'var(--line2)', width: '100%' }}>
      <div style={{ height: h, background: tone, width: pct }} />
    </div>
  )
}

/** 卡片式说明块。用于"为什么这样算"这类需要读进去的长文本。 */
export function Note({ children, tone }: { children: ReactNode; tone?: Tone }) {
  return (
    <div
      style={{
        padding: '14px 16px',
        background: tone ? TONE_BG[tone] : 'var(--sub)',
        borderLeft: `2px solid ${tone ? TONE_FG[tone] : 'var(--line)'}`,
        fontSize: 12.5,
        color: 'var(--fg2)',
        lineHeight: 1.8,
        textWrap: 'pretty',
      }}
    >
      {children}
    </div>
  )
}
