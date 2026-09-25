/* 指针位置。斥力是一个跟着光标走的圆形力场，不需要把移动路径切成取样点；
   惯性与拖尾感由 ParticleField 里的速度和阻尼弹簧自然产生。

   触屏(粗指针)整体关掉：没有 hover 语义，移动端也该省电。 */

export class PointerInteraction {
  x = 0
  y = 0
  active = false
  private enabled = true

  setEnabled(v: boolean) {
    this.enabled = v
    if (!v) this.active = false
  }

  move(x: number, y: number) {
    if (!this.enabled) return
    this.x = x
    this.y = y
    this.active = true
  }

  leave() {
    this.active = false
  }

  reset() {
    this.active = false
  }
}
