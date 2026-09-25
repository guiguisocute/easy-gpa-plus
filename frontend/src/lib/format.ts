/* 展示层格式化。

   分数一律不在这里算，只负责把后端给的数字变成人看的字符串。
   分数最多保留两位小数，整数仍补一位零以保持原有界面口径。
   后端用定点数（scheme.Points），前端展示时也不能把 0.25 误显示成 0.3。 */

function scoreText(v: number): string {
  /* Keep the familiar 3.0/3.5 style while preserving values such as 0.25. */
  return v.toFixed(2).replace(/(\.\d)0$/, '$1')
}

export function score(v: number | null | undefined, dash = '—'): string {
  if (v === null || v === undefined || Number.isNaN(v)) return dash
  return scoreText(v)
}

/** 带符号，用于扣分项与差值 */
export function delta(v: number | null | undefined): string {
  if (v === null || v === undefined) return '—'
  return (v > 0 ? '+' : '') + scoreText(v)
}

/** 库里偶发的坏编码（U+FFFD 替换符）不要原样摊到界面上。 */
export function readableText(value: string | null | undefined, fallback = '（原文编码已损坏）'): string {
  if (value == null) return ''
  const text = value.trim()
  if (!text) return ''
  const chars = [...text]
  const broken = chars.filter((c) => c === '\uFFFD').length
  if (broken === 0) return text
  if (broken / chars.length >= 0.15) return fallback
  return chars.filter((c) => c !== '\uFFFD').join('')
}

export function pct(part: number, whole: number): string {
  if (!whole) return '0%'
  return Math.round((part / whole) * 100) + '%'
}

const pad = (n: number) => String(n).padStart(2, '0')

export function date(iso: string | null | undefined, dash = '—'): string {
  if (!iso) return dash
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return dash
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/* 学生端的方案标记。

   规则改一次版本号就加一，到 v7 时那个数字对学生什么也没说明——他关心的只有
   一件事：我上次读的规则，之后变过没有。所以印时间不印版本。

   版本号本身没有消失：每条申报都冻结了它，结算和审计仍按它对账，只是不摆在
   学生面前。没有发布时间（老数据）时才退回版本号，不显示一个空壳。 */
export function schemeStamp(config: { version?: string; publishedAt?: string } | null | undefined): string {
  if (!config) return ''
  if (config.publishedAt) return `最后更新 ${date(config.publishedAt)}`
  return config.version ? `方案 ${config.version}` : ''
}

export function dateTime(iso: string | null | undefined, dash = '—'): string {
  if (!iso) return dash
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return dash
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function dayMonth(iso: string | null | undefined, dash = '—'): string {
  if (!iso) return dash
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return dash
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** 距离某个时刻还剩几天，已过则为 0 */
export function daysUntil(iso: string | null | undefined): number {
  if (!iso) return 0
  const ms = new Date(iso).getTime() - Date.now()
  return ms <= 0 ? 0 : Math.ceil(ms / 86400000)
}

export function duration(seconds: number | null | undefined): string {
  if (!seconds) return '—'
  if (seconds < 60) return `${seconds} 秒`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m} 分`
  return `${Math.floor(m / 60)} 时 ${m % 60} 分`
}

export function bytes(n: number | null | undefined): string {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

/** 学生申报值的人话版。按档规则会同时带档位和可调整的期望分。 */
export function claimText(claim: { quantity?: number; option?: string; score?: number } | null | undefined, unit?: string): string {
  if (!claim) return '—'
  if (claim.quantity !== undefined && claim.quantity !== null) return `${claim.quantity} ${unit ?? ''}`.trim()
  if (claim.option && claim.score !== undefined && claim.score !== null) return `${claim.option} · 期望 ${scoreText(claim.score)} 分`
  if (claim.option) return claim.option
  if (claim.score !== undefined && claim.score !== null) return `自报 ${scoreText(claim.score)} 分`
  return '—'
}

/** 把规则快照说成一句话，工作台与学生列表都用它，保证两边口径一致。 */
export function ruleText(rule: {
  type: string
  unit?: string
  per?: number
  cap?: number
  min?: number
  max?: number
  minimum?: number
  award?: number
  options?: { label: string; score: number }[]
}): string {
  if (rule.type === 'per_unit') {
    const capText = rule.cap === undefined ? '不封顶' : `封顶 ${rule.cap} 分`
    return `每 ${rule.unit} ${rule.per} 分 · ${capText}`
  }
  if (rule.type === 'enum') {
    return (rule.options ?? []).map((o) => `${o.label} · 建议 ${o.score} 分`).join(' / ')
  }
  if (rule.type === 'free') return `审核人在 ${rule.min}—${rule.max} 分间认定`
  if (rule.type === 'threshold') return `至少 ${rule.minimum} ${rule.unit} · 达标得 ${rule.award} 分，未达标 0 分`
  return '未知规则'
}

/** 草稿允许没有标题（先传佐证、后补名称），列表里给它一个占位而不是留一片空白。 */
export const submissionTitle = (title: string) => title.trim() || '未命名草稿'
