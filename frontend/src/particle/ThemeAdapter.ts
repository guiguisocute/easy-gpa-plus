/* 粒子颜色跟随主题，并且是"渐变过去"而不是瞬间换色。

   项目已有主题系统(zustand store 写 <html data-theme>，见 stores/app.ts)，
   所以这里只接收一个 'light' | 'dark'，不自己去监听 DOM 或媒体查询——
   两处各判一次主题迟早会不一致。没有 store 时调用方传 prefers-color-scheme 的结果即可。 */

import { PARTICLE_RGB, type EmblemParticleConfig } from './config'
import type { Theme } from '@/stores/app'

export class ThemeAdapter {
  private cur: [number, number, number]
  private target: [number, number, number]
  private cfg: EmblemParticleConfig

  constructor(theme: Theme, cfg: EmblemParticleConfig) {
    this.cfg = cfg
    const rgb = PARTICLE_RGB[theme]
    this.cur = [rgb[0], rgb[1], rgb[2]]
    this.target = [rgb[0], rgb[1], rgb[2]]
  }

  setTheme(theme: Theme) {
    const rgb = PARTICLE_RGB[theme]
    this.target = [rgb[0], rgb[1], rgb[2]]
  }

  /** 颜色已收敛到目标色。校徽是静态的，收敛后配合零能量就可以整帧跳过重绘。 */
  get settled(): boolean {
    return (
      Math.abs(this.cur[0] - this.target[0]) < 0.5 &&
      Math.abs(this.cur[1] - this.target[1]) < 0.5 &&
      Math.abs(this.cur[2] - this.target[2]) < 0.5
    )
  }

  /** 指数趋近目标色。themeFadeMs 是大致的收敛时长，不是精确时间。 */
  update(dt: number): readonly [number, number, number] {
    const k = 1 - Math.exp(-(1000 / Math.max(1, this.cfg.themeFadeMs)) * 4 * dt)
    for (let i = 0; i < 3; i++) this.cur[i] += (this.target[i] - this.cur[i]) * k
    return this.cur as unknown as readonly [number, number, number]
  }
}
