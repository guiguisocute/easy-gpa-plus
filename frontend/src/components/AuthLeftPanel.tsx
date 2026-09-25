/* 开屏左栏：品牌介绍 + 点阵标志背景。

   桌面端是左右分栏里的左半边；窄屏下它变成独立的第一屏(compact)，
   底部多一个进入按钮，点了才切到表单屏——两栏在手机上并排会把两边都挤成竖排文字。

   层级是 页面背景 → 点阵画布(absolute, inset 0) → 原有文案(position:relative, z-index 1)。
   三个直接子元素各自提层而不是整体包一层 div——外层是 flex，
   中间那块靠 margin:'auto 0' 撑开上下留白，多包一层就把这个布局破坏了。 */

import { crestSource, useBranding } from '@/api/branding'
import { BrandMark } from './ui'
import ParticleEmblem from './ParticleEmblem'
import type { EmblemParticleConfig } from '@/particle/config'

const LAYER = { position: 'relative', zIndex: 1 } as const

/* 窄屏第一屏的品牌标志参数。必须是模块级常量:ParticleEmblem 把 config 放进了 useMemo 依赖，
   传字面量对象会每次渲染换一个引用，于是每帧重建栅格并重采样。

   减淡区在这里要关掉——它是为桌面端"左边一列文案、右边留白"设计的，
   而窄屏文案是满宽的，左偏的减淡只会白白把品牌标志压暗。 */
const COMPACT_EMBLEM: Partial<EmblemParticleConfig> = {
  textGuardWidth: 0,
  textGuardFloor: 1,
  emblemAlpha: 0.64,
  centerX: 0.5,
  /* 独立的尺寸与位置：窄屏文案是满宽的，品牌标志只能塞进品牌行与标题之间那段留白。
     桌面端的 sizeW 直接拿过来会小到格数不够、被 minEmblemCells 挡掉而整个消失。 */
  centerY: 0.22,
  sizeW: 0.68,
  sizeH: 0.4,
}

interface Props {
  /** 窄屏第一屏模式：占满视口、去掉右边框、显示进入按钮。 */
  compact?: boolean
  onEnter?: () => void
}

export default function AuthLeftPanel({ compact, onEnter }: Props) {
  const branding = useBranding()
  return (
    <div
      className={compact ? 'emblem-fade auth-brand-mobile' : 'emblem-fade'}
      style={{
        padding: compact ? '44px 24px' : '52px 56px',
        display: 'flex',
        flexDirection: 'column',
        borderRight: compact ? 'none' : '1px solid var(--line)',
        background: 'var(--sub)',
        position: 'relative',
        overflow: 'hidden',
        ...(compact ? { minHeight: '100dvh' } : null),
      }}
    >
      <ParticleEmblem src={branding.data?.svg ? crestSource(branding.data.svg) : undefined} config={compact ? COMPACT_EMBLEM : undefined} />

      <div style={{ ...LAYER, display: 'flex', alignItems: 'center', gap: 11 }}>
        <BrandMark />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <div style={{ fontWeight: 600, letterSpacing: '-.025em', fontSize: 13 }}>EasyGPA Plus</div>
          <div style={{ font: "500 10px/1 'JetBrains Mono',monospace", letterSpacing: '.18em', color: 'var(--fg3)' }}>
            OPEN SOURCE · SELF HOSTED
          </div>
        </div>
      </div>

      <div style={{ ...LAYER, margin: 'auto 0', display: 'flex', flexDirection: 'column', gap: 26, maxWidth: 420 }}>
        <div style={{ fontSize: compact ? 28 : 34, fontWeight: 600, letterSpacing: '-.045em', lineHeight: 1.2 }}>
          EasyGPA Plus
        </div>
        <div style={{ fontSize: 13.5, color: 'var(--fg2)', lineHeight: 1.85, textWrap: 'pretty' }}>
          从材料提交到审核、申诉与结果确认，在一处完成。
        </div>
        {/* 三行要点在窄屏下会被压成一列竖排字，第一屏本来就该只留主信息，直接不渲染。 */}
        {!compact && (
          <div style={{ display: 'flex', flexDirection: 'column', borderTop: '1px solid var(--line)' }}>
            {[
              ['学生', '提交材料 · 查看进度与结果'],
              ['综测小组', '审核材料 · 处理复评'],
              ['班级管理员', '管理方案 · 分发与结算'],
            ].map(([k, v]) => (
              <div key={k} style={{ display: 'flex', alignItems: 'baseline', gap: 14, padding: '11px 0', borderBottom: '1px solid var(--line2)' }}>
                <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', flex: 'none', width: 96 }}>{k}</span>
                <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>{v}</span>
              </div>
            ))}
          </div>
        )}
        {compact && (
          <button
            type="button"
            className="hv-op82"
            onClick={onEnter}
            style={{
              margin: 0,
              padding: '13px 18px',
              borderRadius: 999,
              border: '1px solid var(--fg)',
              background: 'var(--fg)',
              color: 'var(--bg)',
              font: 'inherit',
              fontSize: 13,
              fontWeight: 600,
              cursor: 'pointer',
              transition: 'opacity .16s',
            }}
          >
            进入登录
          </button>
        )}
      </div>

      <div style={{ ...LAYER, fontSize: 12.5, color: 'var(--fg3)' }}>登录后将进入与你身份对应的工作台。 <a href="/source.tar.gz" download style={{ color: 'inherit' }}>开源代码 · AGPL-3.0</a></div>
    </div>
  )
}
