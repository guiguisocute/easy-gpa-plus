/* 粒子校徽画布。填充其定位父元素，作为背景层铺在文案下面。

   渲染用 Canvas 2D 而不是 WebGL/Three：几千个带弹簧状态的方点没有光照和真正的
   三维几何，2D 足够流畅，也省掉 shader 与 three 的体积。DOM 堆点则会制造几千个
   节点的布局与合成开销。

   校徽采样失败时降级为少量游离光点：登录页不能因为一张装饰图挂掉。 */

import { useEffect, useMemo, useRef } from 'react'
import { DEFAULT_CONFIG, type EmblemParticleConfig } from '@/particle/config'
import { sampleEmblem } from '@/particle/EmblemSampler'
import { ParticleField } from '@/particle/ParticleField'
import { PointerInteraction } from '@/particle/PointerInteraction'
import { ThemeAdapter } from '@/particle/ThemeAdapter'
import { useApp } from '@/stores/app'

export type EmblemStatus = 'loading' | 'ready' | 'empty' | 'error'
interface Props {
  /** 上传的校徽图源；与默认网页图标定时轮换，不传时只显示网页图标。 */
  src?: string
  /** 覆盖任意参数，与 DEFAULT_CONFIG 浅合并。 */
  config?: Partial<EmblemParticleConfig>
  onStatus?: (status: EmblemStatus) => void
}

/** 窄屏轻微放稀点阵：省电，也避免小尺寸下点挤成一片糊。 */
const COMPACT_WIDTH = 640

export default function ParticleEmblem({ src, config, onStatus }: Props) {
  const theme = useApp((s) => s.theme)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  /* 主题变化不该重启渲染循环，用 ref 把新值递进去。 */
  const adapterRef = useRef<ThemeAdapter | null>(null)

  const cfg = useMemo<EmblemParticleConfig>(
    () => ({ ...DEFAULT_CONFIG, ...config }),
    [config],
  )

  useEffect(() => {
    const canvas = canvasRef.current
    const host = canvas?.parentElement
    if (!canvas || !host) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const field = new ParticleField()
    const pointer = new PointerInteraction()
    const adapter = new ThemeAdapter(useApp.getState().theme, cfg)
    adapterRef.current = adapter
    let dirty = true
    let masks: Awaited<ReturnType<typeof sampleEmblem>>[] = []
    let pattern = 0

    /* 减弱动效偏好下关掉入场、呼吸与斥力，直接把粒子固定在图案上。
       粗指针(触屏)没有 hover 语义，也不启用指针斥力。 */
    const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)')
    const finePointer = matchMedia('(pointer: fine)')
    const syncPointer = () => {
      const reduced = reduceMotion.matches
      pointer.setEnabled(finePointer.matches && !reduced)
      field.setReducedMotion(reduced)
      if (reduced && masks.length > 1) {
        pattern = 1
        field.applyMask(masks[pattern], cfg)
        canvas.dataset.pattern = 'crest'
      }
      /* 媒体查询可能在粒子已经静止后变化；强制补画一帧，避免画布
         停留在切换前的动态位置。 */
      dirty = true
    }
    syncPointer()
    finePointer.addEventListener('change', syncPointer)
    reduceMotion.addEventListener('change', syncPointer)

    let w = 0
    let h = 0
    let raf = 0
    let last = performance.now()
    let hadEnergy = false
    /* 采样是异步的，尺寸连续变化时用它作废掉过期的结果。 */
    let generation = 0

    async function rebuild() {
      const rect = host!.getBoundingClientRect()
      w = Math.max(1, Math.round(rect.width))
      h = Math.max(1, Math.round(rect.height))

      const dpr = Math.min(2, window.devicePixelRatio || 1)
      canvas!.width = Math.round(w * dpr)
      canvas!.height = Math.round(h * dpr)
      canvas!.style.width = `${w}px`
      canvas!.style.height = `${h}px`
      ctx!.setTransform(dpr, 0, 0, dpr, 0, 0)

      const spacing = cfg.spacing * (w < COMPACT_WIDTH ? cfg.compactSpacingScale : 1)
      const geom = field.build(w, h, spacing, cfg)
      pointer.reset()
      dirty = true

      const mine = ++generation
      masks = []
      canvas!.dataset.pattern = 'loading'
      onStatus?.('loading')
      /* 格数不够就别显像了：校徽只剩十来格时只会是一团糊块。 */
      if (geom.emblemCols < cfg.minEmblemCells) { onStatus?.('empty'); return }
      try {
        const icon = await sampleEmblem(geom.emblemCols, geom.emblemRows, cfg)
        if (mine !== generation) return
        masks = [icon]; pattern = 0
        field.applyMask(icon, cfg)
        canvas!.dataset.pattern = 'icon'
        onStatus?.('ready')
        if (src && src !== cfg.src) {
          try {
            const crest = await sampleEmblem(geom.emblemCols, geom.emblemRows, { ...cfg, src })
            if (mine !== generation) return
            if (crest.data.some((ink) => ink > 0)) {
              masks.push(crest)
              if (reduceMotion.matches) {
                pattern = 1
                field.applyMask(crest, cfg)
                canvas!.dataset.pattern = 'crest'
              }
              onStatus?.('ready')
            } else onStatus?.('empty')
          } catch { if (mine === generation) onStatus?.('error') }
        }
        dirty = true
      } catch {
        if (mine === generation) onStatus?.('error')
        /* 降级：保留少量游离光点，不显像校徽。 */
      }
    }

    const onMove = (e: PointerEvent) => {
      const rect = host.getBoundingClientRect()
      pointer.move(e.clientX - rect.left, e.clientY - rect.top)
    }
    const onLeave = () => pointer.leave()
    host.addEventListener('pointermove', onMove)
    host.addEventListener('pointerleave', onLeave)

    const loop = (now: number) => {
      /* dt 封顶：切回后台标签页再回来时，累积的时间差会让衰减一次跳过头。 */
      const dt = Math.min(0.05, (now - last) / 1000)
      last = now

      const disturb = field.update(dt, pointer, cfg)
      const rgb = adapter.update(dt)
      const active = disturb > 0.01

      /* 完全静止就不重绘。hadEnergy 让扰动归零后还能补画最后一帧，不留残影。 */
      if (active || hadEnergy || dirty || !adapter.settled) {
        ctx.clearRect(0, 0, w, h)
        field.draw(ctx, rgb, cfg)
        dirty = false
      }
      hadEnergy = active
      raf = requestAnimationFrame(loop)
    }

    let resizeTimer = 0
    const ro = new ResizeObserver(() => {
      window.clearTimeout(resizeTimer)
      resizeTimer = window.setTimeout(() => void rebuild(), 150)
    })
    ro.observe(host)

    const morphTimer = window.setInterval(() => {
      if (document.hidden || reduceMotion.matches || masks.length < 2) return
      pattern = (pattern + 1) % masks.length
      field.applyMask(masks[pattern], cfg)
      canvas.dataset.pattern = pattern === 0 ? 'icon' : 'crest'
      dirty = true
    }, 8000)
    void rebuild()
    raf = requestAnimationFrame(loop)

    return () => {
      generation++
      window.clearInterval(morphTimer)
      cancelAnimationFrame(raf)
      window.clearTimeout(resizeTimer)
      ro.disconnect()
      finePointer.removeEventListener('change', syncPointer)
      reduceMotion.removeEventListener('change', syncPointer)
      host.removeEventListener('pointermove', onMove)
      host.removeEventListener('pointerleave', onLeave)
      adapterRef.current = null
    }
  }, [cfg, src, onStatus])

  useEffect(() => {
    adapterRef.current?.setTheme(theme)
  }, [theme])

  return (
    <canvas
      ref={canvasRef}
      aria-hidden="true"
      style={{ position: 'absolute', inset: 0, pointerEvents: 'none', display: 'block' }}
    />
  )
}
