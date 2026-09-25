/** 登录页粒子校徽的视觉与物理参数。 */
export interface EmblemParticleConfig {
  /** 校徽图源。放在 public/ 下即可，也可传绝对 URL。 */
  src: string
  /** 从图源中取出的归一化裁剪框。 */
  crop: { x: number; y: number; w: number; h: number }
  /** 按原图中心半径切掉外圈装饰；<= 0 表示不裁。 */
  ringCutRadius: number

  /** 粒子采样间距与单点边长，单位均为 CSS px。 */
  spacing: number
  dotSize: number
  /** 校徽粒子和游离光点的最大不透明度。 */
  emblemAlpha: number
  ambientAlpha: number
  /** 每平方像素生成的游离光点数量。 */
  ambientDensity: number
  /** 游离光点的基础移动速度，单位 px/s。 */
  ambientSpeed: number

  /** 校徽中心与最大占用范围，均为画布宽高的比例。 */
  centerX: number
  centerY: number
  sizeW: number
  sizeH: number

  /** 采样后的遮罩对比度。 */
  alphaGamma: number
  lumaWeight: number
  inkCutoff: number

  /** 鼠标斥力场：半径、加速度、回位弹簧、阻尼与速度上限。 */
  pointerRadius: number
  pointerForce: number
  springStrength: number
  damping: number
  maxSpeed: number
  /** 不同深度的粒子随指针产生的轻微视差。 */
  pointerParallax: number

  /** 静止时的亚像素呼吸，让点云保持生命感但不破坏轮廓。 */
  idleAmplitude: number
  idleSpeed: number
  /** 粒子首次成像时从目标点外散开的最大距离。 */
  entrySpread: number

  /** 正文区的渐隐保护：左内边距、最大正文宽度、最低亮度、保护带高度与羽化宽度。 */
  textGuardInset: number
  textGuardWidth: number
  textGuardFloor: number
  textGuardBandHeight: number
  textGuardFeather: number

  /** 窄屏采样间距倍率，以及成像所需的最少横向格数。 */
  compactSpacingScale: number
  minEmblemCells: number
  /** 主题切换时粒子颜色的过渡时长。 */
  themeFadeMs: number
}

export const DEFAULT_CONFIG: EmblemParticleConfig = {
  src: '/favicon.svg',
  crop: { x: 0, y: 0, w: 1, h: 1 },
  ringCutRadius: 0,

  /* 参考页使用高密度小方点，而不是铺满背景的大点阵。 */
  spacing: 6.8,
  dotSize: 1.9,
  emblemAlpha: 0.72,
  ambientAlpha: 0.5,
  ambientDensity: 0.000052,
  ambientSpeed: 7,

  /* 放大后让主体横跨右侧与上下留白；正文覆盖范围由二维渐隐蒙版保护。 */
  centerX: 0.67,
  centerY: 0.5,
  sizeW: 0.62,
  sizeH: 0.66,

  alphaGamma: 0.82,
  lumaWeight: 0.35,
  inkCutoff: 0.11,

  /* 圆形斥力会在光标附近挖出空洞；弹簧与阻尼负责有惯性的复原。 */
  pointerRadius: 112,
  pointerForce: 9000,
  springStrength: 31,
  damping: 7.4,
  maxSpeed: 820,
  pointerParallax: 0.014,

  idleAmplitude: 0.75,
  idleSpeed: 0.62,
  entrySpread: 95,

  textGuardInset: 56,
  textGuardWidth: 320,
  textGuardFloor: 0.035,
  textGuardBandHeight: 360,
  textGuardFeather: 50,

  compactSpacingScale: 1.08,
  minEmblemCells: 28,
  themeFadeMs: 420,
}

/* 粒子颜色跟 --fg 走，两档各自等于对应主题的正文色（#0a0a0a / #e5e5e5）。
   深色这一档原先是纯白：登录页满屏几千个点，纯白铺在暗底上是整个界面最灼眼的一处。 */
export const PARTICLE_RGB = {
  light: [10, 10, 10] as const,
  dark: [229, 229, 229] as const,
}
