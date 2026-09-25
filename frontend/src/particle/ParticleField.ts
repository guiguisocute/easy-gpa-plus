/**
 * 校徽粒子云。
 *
 * 粒子只出现在图像遮罩中，不再用整屏规则点阵铺底。每个粒子都有固定的 home
 * 坐标、速度与弹簧：指针靠近时施加径向斥力，离开后靠阻尼弹簧回到图案，形成
 * 参考页那种“挖开一个圆形空洞、随后带惯性复原”的手感。
 */

import type { EmblemParticleConfig } from './config'
import type { EmblemMask } from './EmblemSampler'
import type { PointerInteraction } from './PointerInteraction'

const ALPHA_BUCKETS = 18
const TAU = Math.PI * 2

export interface FieldGeometry {
  cols: number
  rows: number
  spacing: number
  emblemCol0: number
  emblemRow0: number
  emblemCols: number
  emblemRows: number
}

interface AmbientParticle {
  x: number
  y: number
  vx: number
  vy: number
  phase: number
  alpha: number
  size: number
}

function hash(x: number, y: number, salt: number) {
  const n = Math.sin(x * 127.1 + y * 311.7 + salt * 74.7) * 43758.5453123
  return n - Math.floor(n)
}

function smoothstep(t: number) {
  const v = Math.max(0, Math.min(1, t))
  return v * v * (3 - 2 * v)
}

export class ParticleField {
  geom: FieldGeometry = {
    cols: 0,
    rows: 0,
    spacing: 0,
    emblemCol0: 0,
    emblemRow0: 0,
    emblemCols: 0,
    emblemRows: 0,
  }

  private width = 0
  private height = 0
  private count = 0
  private elapsed = 0
  private transitionAt = -10
  private reducedMotion = false

  private homeX = new Float32Array(0)
  private homeY = new Float32Array(0)
  private x = new Float32Array(0)
  private y = new Float32Array(0)
  private vx = new Float32Array(0)
  private vy = new Float32Array(0)
  private ink = new Float32Array(0)
  private guard = new Float32Array(0)
  private phase = new Float32Array(0)
  private depth = new Float32Array(0)
  private response = new Float32Array(0)
  private ambient: AmbientParticle[] = []

  build(width: number, height: number, spacing: number, cfg: EmblemParticleConfig): FieldGeometry {
    this.width = width
    this.height = height
    this.elapsed = 0
    this.count = 0

    const cols = Math.max(1, Math.ceil(width / spacing))
    const rows = Math.max(1, Math.ceil(height / spacing))
    const aspect = cfg.crop.w / cfg.crop.h

    let boxW = width * cfg.sizeW
    let boxH = boxW / aspect
    if (boxH > height * cfg.sizeH) {
      boxH = height * cfg.sizeH
      boxW = boxH * aspect
    }

    const emblemCols = Math.max(1, Math.round(boxW / spacing))
    const emblemRows = Math.max(1, Math.round(boxH / spacing))
    const emblemCol0 = Math.round((width * cfg.centerX - boxW / 2) / spacing)
    const emblemRow0 = Math.round((height * cfg.centerY - boxH / 2) / spacing)

    this.geom = { cols, rows, spacing, emblemCol0, emblemRow0, emblemCols, emblemRows }
    this.buildAmbient(cfg)
    return this.geom
  }

  /** 把图像遮罩变成带速度状态的粒子集合。 */
  applyMask(mask: EmblemMask, cfg: EmblemParticleConfig) {
    let count = 0
    for (let i = 0; i < mask.data.length; i++) {
      if (mask.data[i] > 0) count++
    }

    const previousX = this.x, previousY = this.y, previousCount = this.count
    this.transitionAt = this.elapsed
    this.count = count
    this.homeX = new Float32Array(count)
    this.homeY = new Float32Array(count)
    this.x = new Float32Array(count)
    this.y = new Float32Array(count)
    this.vx = new Float32Array(count)
    this.vy = new Float32Array(count)
    this.ink = new Float32Array(count)
    this.guard = new Float32Array(count)
    this.phase = new Float32Array(count)
    this.depth = new Float32Array(count)
    this.response = new Float32Array(count)

    const { spacing, emblemCol0, emblemRow0 } = this.geom
    let p = 0

    for (let my = 0; my < mask.rows; my++) {
      for (let mx = 0; mx < mask.cols; mx++) {
        const ink = mask.data[my * mask.cols + mx]
        if (ink <= 0) continue

        const homeX = (emblemCol0 + mx + 0.5) * spacing
        const homeY = (emblemRow0 + my + 0.5) * spacing
        const h1 = hash(mx, my, 1)
        const h2 = hash(mx, my, 2)
        const h3 = hash(mx, my, 3)
        const angle = h1 * TAU
        const spread = cfg.entrySpread * (0.25 + h2 * 0.75)

        this.homeX[p] = homeX
        this.homeY[p] = homeY
        this.ink[p] = ink
        this.phase[p] = h3 * TAU
        this.depth[p] = 0.35 + h2 * 0.65
        this.response[p] = 0.78 + h1 * 0.44

        this.guard[p] = this.readabilityGuard(homeX, homeY, cfg)

        if (this.reducedMotion) {
          this.x[p] = homeX
          this.y[p] = homeY
        } else {
          const from = previousCount ? Math.min(previousCount - 1, Math.floor(p * previousCount / count)) : -1
          this.x[p] = from >= 0 ? previousX[from] : homeX + Math.cos(angle) * spread
          this.y[p] = from >= 0 ? previousY[from] : homeY + Math.sin(angle) * spread
          this.vx[p] = Math.cos(angle) * spread * 3
          this.vy[p] = Math.sin(angle) * spread * 3
        }
        p++
      }
    }
  }

  setReducedMotion(reduced: boolean) {
    this.reducedMotion = reduced
    if (!reduced) return

    for (let i = 0; i < this.count; i++) {
      this.x[i] = this.homeX[i]
      this.y[i] = this.homeY[i]
      this.vx[i] = 0
      this.vy[i] = 0
    }
  }

  /** 推进物理；返回大于 0 表示画布仍需重绘。 */
  update(dt: number, pointer: PointerInteraction, cfg: EmblemParticleConfig): number {
    if (this.reducedMotion) return 0

    this.elapsed += dt
    const damping = Math.exp(-cfg.damping * dt)
    const radius = cfg.pointerRadius
    const radius2 = radius * radius
    const maxSpeed2 = cfg.maxSpeed * cfg.maxSpeed
    const parallaxX = pointer.active ? (pointer.x - this.width / 2) * cfg.pointerParallax : 0
    const parallaxY = pointer.active ? (pointer.y - this.height / 2) * cfg.pointerParallax : 0

    for (let i = 0; i < this.count; i++) {
      const phase = this.phase[i]
      const depth = this.depth[i]
      const response = this.response[i]
      const idle = cfg.idleAmplitude * depth
      const scatter = (1 - smoothstep((this.elapsed - this.transitionAt) / 1.5)) * cfg.entrySpread
      const targetX =
        this.homeX[i] + Math.cos(phase) * scatter +
        Math.cos(this.elapsed * cfg.idleSpeed + phase) * idle +
        parallaxX * (depth - 0.62)
      const targetY =
        this.homeY[i] + Math.sin(phase) * scatter +
        Math.sin(this.elapsed * cfg.idleSpeed * 0.83 + phase * 1.37) * idle +
        parallaxY * (depth - 0.62)

      let ax = (targetX - this.x[i]) * cfg.springStrength * response
      let ay = (targetY - this.y[i]) * cfg.springStrength * response

      if (pointer.active) {
        const dx = this.x[i] - pointer.x
        const dy = this.y[i] - pointer.y
        const d2 = dx * dx + dy * dy
        if (d2 < radius2) {
          const d = Math.sqrt(d2)
          const nx = d > 0.01 ? dx / d : Math.cos(phase)
          const ny = d > 0.01 ? dy / d : Math.sin(phase)
          const falloff = 1 - d / radius
          const force = cfg.pointerForce * falloff * falloff * (0.82 + depth * 0.28)
          ax += nx * force
          ay += ny * force
        }
      }

      let vx = (this.vx[i] + ax * dt) * damping
      let vy = (this.vy[i] + ay * dt) * damping
      const speed2 = vx * vx + vy * vy
      if (speed2 > maxSpeed2) {
        const scale = cfg.maxSpeed / Math.sqrt(speed2)
        vx *= scale
        vy *= scale
      }

      this.vx[i] = vx
      this.vy[i] = vy
      this.x[i] += vx * dt
      this.y[i] += vy * dt
    }

    this.updateAmbient(dt, pointer, cfg)
    return 1
  }

  draw(ctx: CanvasRenderingContext2D, rgb: readonly [number, number, number], cfg: EmblemParticleConfig) {
    ctx.fillStyle = `rgb(${rgb[0]},${rgb[1]},${rgb[2]})`
    this.drawAmbient(ctx, cfg)

    const half = cfg.dotSize / 2
    let bucket = -1
    for (let i = 0; i < this.count; i++) {
      const twinkle = 0.91 + Math.sin(this.elapsed * 1.4 + this.phase[i]) * 0.09
      /* 当前位置也过一遍蒙版：入场和斥力把粒子推入正文时仍会及时压暗。 */
      const liveGuard = this.readabilityGuard(this.x[i], this.y[i], cfg)
      const alpha =
        cfg.emblemAlpha *
        (0.42 + this.ink[i] * 0.58) *
        Math.min(this.guard[i], liveGuard) *
        twinkle
      const nextBucket = Math.min(ALPHA_BUCKETS - 1, Math.max(0, Math.round(alpha * (ALPHA_BUCKETS - 1))))
      if (nextBucket === 0) continue
      if (nextBucket !== bucket) {
        bucket = nextBucket
        ctx.globalAlpha = bucket / (ALPHA_BUCKETS - 1)
      }
      ctx.fillRect(this.x[i] - half, this.y[i] - half, cfg.dotSize, cfg.dotSize)
    }
    ctx.globalAlpha = 1
  }

  private buildAmbient(cfg: EmblemParticleConfig) {
    const count = Math.max(8, Math.round(this.width * this.height * cfg.ambientDensity))
    this.ambient = Array.from({ length: count }, (_, i) => {
      const h1 = hash(i, count, 11)
      const h2 = hash(i, count, 12)
      const h3 = hash(i, count, 13)
      const angle = h3 * TAU
      const speed = cfg.ambientSpeed * (0.35 + h2 * 0.9)
      return {
        x: h1 * this.width,
        y: h2 * this.height,
        vx: Math.cos(angle) * speed,
        vy: Math.sin(angle) * speed,
        phase: h3 * TAU,
        alpha: 0.38 + h1 * 0.62,
        size: 0.8 + h2 * 0.9,
      }
    })
  }

  private updateAmbient(dt: number, pointer: PointerInteraction, cfg: EmblemParticleConfig) {
    const radius = cfg.pointerRadius * 1.1
    const radius2 = radius * radius

    for (const p of this.ambient) {
      p.x += (p.vx + Math.sin(this.elapsed * 0.7 + p.phase) * 1.5) * dt
      p.y += (p.vy + Math.cos(this.elapsed * 0.55 + p.phase) * 1.5) * dt

      if (pointer.active) {
        const dx = p.x - pointer.x
        const dy = p.y - pointer.y
        const d2 = dx * dx + dy * dy
        if (d2 < radius2 && d2 > 0.01) {
          const d = Math.sqrt(d2)
          const impulse = (1 - d / radius) * 46 * dt
          p.x += (dx / d) * impulse
          p.y += (dy / d) * impulse
        }
      }

      if (p.x < -8) p.x = this.width + 8
      else if (p.x > this.width + 8) p.x = -8
      if (p.y < -8) p.y = this.height + 8
      else if (p.y > this.height + 8) p.y = -8
    }
  }

  private drawAmbient(ctx: CanvasRenderingContext2D, cfg: EmblemParticleConfig) {
    for (const p of this.ambient) {
      const guard = this.readabilityGuard(p.x, p.y, cfg)
      const pulse = 0.72 + Math.sin(this.elapsed * 1.15 + p.phase) * 0.28
      const alpha = cfg.ambientAlpha * p.alpha * guard * pulse
      if (alpha < 0.018) continue

      /* 两层方点模拟参考页游离粒子的柔和辉光，避免逐点创建 radialGradient。 */
      ctx.globalAlpha = alpha * 0.1
      const glow = p.size * 5
      ctx.fillRect(p.x - glow / 2, p.y - glow / 2, glow, glow)
      ctx.globalAlpha = alpha
      ctx.fillRect(p.x - p.size / 2, p.y - p.size / 2, p.size, p.size)
    }
    ctx.globalAlpha = 1
  }

  /**
   * 只在正文实际占用的纵向范围内压暗左侧粒子；校徽伸到标题上方和信息区下方时
   * 恢复完整亮度。这样可以增大视觉体量，而不是把整个左半边一刀切暗。
   */
  private readabilityGuard(x: number, y: number, cfg: EmblemParticleConfig) {
    if (cfg.textGuardWidth <= 0) return 1

    const edge = Math.min(this.width - cfg.textGuardInset, cfg.textGuardInset + cfg.textGuardWidth)
    const transition = Math.min(52, Math.max(28, edge * 0.1))
    const start = edge - transition
    const horizontal = smoothstep((x - start) / transition)
    const faded = cfg.textGuardFloor + (1 - cfg.textGuardFloor) * horizontal

    const top = (this.height - cfg.textGuardBandHeight) / 2
    const bottom = (this.height + cfg.textGuardBandHeight) / 2
    const feather = Math.max(1, cfg.textGuardFeather)
    const enter = smoothstep((y - (top - feather)) / feather)
    const leave = 1 - smoothstep((y - bottom) / feather)
    const insideTextBand = enter * leave

    return 1 - insideTextBand * (1 - faded)
  }
}
