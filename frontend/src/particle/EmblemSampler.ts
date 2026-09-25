import type { EmblemParticleConfig } from './config'

export interface EmblemMask { cols: number; rows: number; data: Float32Array }
const SS = 4

/** Sample the complete vector, preserving its aspect ratio. Subtract a uniform
 * opaque background so both a light favicon and dark crest retain their shape. */
export async function sampleEmblem(cols: number, rows: number, cfg: EmblemParticleConfig): Promise<EmblemMask> {
  const w = cols * SS, h = rows * SS
  // Uploaded SVG is already available locally; decoding avoids a network request
  // blocked by the production connect-src policy for data URLs.
  const prefix = 'data:image/svg+xml;charset=utf-8,'
  let source: string
  if (cfg.src.startsWith(prefix)) source = decodeURIComponent(cfg.src.slice(prefix.length))
  else {
    const response = await fetch(cfg.src)
    if (!response.ok) throw new Error('图案加载失败')
    source = await response.text()
  }
  const doc = new DOMParser().parseFromString(source, 'image/svg+xml')
  const svg = doc.documentElement
  if (svg.localName !== 'svg' || doc.querySelector('parsererror')) throw new Error('SVG 格式错误')
  // Bare SVG exported without xmlns is valid input but otherwise fails when
  // decoded as an image. Preserve the author's coordinate system on resize.
  if (!svg.getAttribute('xmlns')) svg.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
  const viewBox = svg.getAttribute('viewBox')?.trim().split(/[\s,]+/).map(Number)
  const sourceW = parseFloat(svg.getAttribute('width') ?? '')
  const sourceH = parseFloat(svg.getAttribute('height') ?? '')
  if (!viewBox && sourceW > 0 && sourceH > 0) svg.setAttribute('viewBox', `0 0 ${sourceW} ${sourceH}`)
  const aspect = viewBox?.length === 4 && viewBox[2] > 0 && viewBox[3] > 0 ? viewBox[2] / viewBox[3] :
    (parseFloat(svg.getAttribute('width') ?? '') / parseFloat(svg.getAttribute('height') ?? '') || 1)
  const drawW = Math.min(w, h * aspect), drawH = drawW / aspect
  svg.setAttribute('width', String(Math.ceil(drawW)))
  svg.setAttribute('height', String(Math.ceil(drawH)))
  const url = URL.createObjectURL(new Blob([new XMLSerializer().serializeToString(svg)], { type: 'image/svg+xml' }))
  try {
    const img = await new Promise<HTMLImageElement>((resolve, reject) => {
      const image = new Image()
      image.onload = () => resolve(image); image.onerror = () => reject(new Error('SVG 解码失败')); image.src = url
    })
    const canvas = document.createElement('canvas')
    canvas.width = w; canvas.height = h
    const ctx = canvas.getContext('2d', { willReadFrequently: true })
    if (!ctx) throw new Error('无法创建画布')
    const left = (w - drawW) / 2, top = (h - drawH) / 2
    ctx.drawImage(img, left, top, drawW, drawH)
    const px = ctx.getImageData(0, 0, w, h).data
    const at = (x: number, y: number) => (Math.min(h - 1, Math.max(0, Math.round(y))) * w + Math.min(w - 1, Math.max(0, Math.round(x)))) * 4
    const corner = at(left, top)
    const corners = [[left, top], [left + drawW - 1, top], [left, top + drawH - 1], [left + drawW - 1, top + drawH - 1]]
    const opaqueBackground = corners.every(([x, y]) => {
      const i = at(x, y)
      return px[i + 3] > 245 && Math.abs(px[i] - px[corner]) + Math.abs(px[i + 1] - px[corner + 1]) + Math.abs(px[i + 2] - px[corner + 2]) < 24
    })
    const data = new Float32Array(cols * rows)
    let peak = 0
    for (let gy = 0; gy < rows; gy++) for (let gx = 0; gx < cols; gx++) {
      let sum = 0
      for (let sy = 0; sy < SS; sy++) for (let sx = 0; sx < SS; sx++) {
        const i = ((gy * SS + sy) * w + gx * SS + sx) * 4
        const contrast = opaqueBackground ? Math.max(Math.abs(px[i] - px[corner]), Math.abs(px[i + 1] - px[corner + 1]), Math.abs(px[i + 2] - px[corner + 2])) / 255 : 1
        sum += px[i + 3] / 255 * contrast
      }
      const ink = sum / (SS * SS)
      data[gy * cols + gx] = ink; peak = Math.max(peak, ink)
    }
    for (let i = 0; i < data.length; i++) {
      const ink = peak ? data[i] / peak : 0
      data[i] = ink < cfg.inkCutoff ? 0 : Math.pow(ink, cfg.alphaGamma)
    }
    return { cols, rows, data }
  } finally { URL.revokeObjectURL(url) }
}
