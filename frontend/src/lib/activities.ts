import type { SchemeActivity } from './types'

/** 批量解析活动清单文本：支持 JSON 数组片段、带 "activities": [...] 的 JSON、或表格制表符/多行文本。 */
export function parseActivitiesText(input: string): SchemeActivity[] {
  const trimmed = input.trim()
  if (!trimmed) return []

  // 1. 优先尝试 JSON 格式
  let jsonCandidate = trimmed
  const match = trimmed.match(/"activities"\s*:\s*(\[[\s\S]*\])/)
  if (match) {
    jsonCandidate = match[1]
  }

  try {
    const parsed = JSON.parse(jsonCandidate)
    const list = Array.isArray(parsed)
      ? parsed
      : (parsed && typeof parsed === 'object' && Array.isArray((parsed as { activities?: unknown[] }).activities))
        ? (parsed as { activities: unknown[] }).activities
        : null

    if (list && Array.isArray(list)) {
      const results: SchemeActivity[] = []
      for (const item of list) {
        if (!item || typeof item !== 'object') continue
        const raw = item as Record<string, unknown>
        const name = String(raw.name ?? '').trim()
        if (!name) continue
        const date = raw.date ? String(raw.date).trim() : undefined
        const level = raw.level ? String(raw.level).trim() : undefined
        const org = raw.org ? String(raw.org).trim() : undefined
        const score = raw.score ? String(raw.score).trim() : undefined
        results.push({ name, date, level, org, score })
      }
      if (results.length > 0) return results
    }
  } catch {
    // 非合法 JSON 则继续尝试表格文本解析
  }

  // 2. 制表符/多行文本解析
  const lines = trimmed.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
  const results: SchemeActivity[] = []
  for (const line of lines) {
    if (line.includes('\t')) {
      const parts = line.split('\t').map((p) => p.trim())
      if (parts[0]) {
        results.push({
          name: parts[0],
          date: parts[1] || undefined,
          level: parts[2] || undefined,
          org: parts[3] || undefined,
          score: parts[4] || undefined,
        })
      }
    } else if (line.startsWith('{') && line.endsWith('}')) {
      try {
        const obj = JSON.parse(line) as Record<string, unknown>
        const name = String(obj.name ?? '').trim()
        if (name) {
          results.push({
            name,
            date: obj.date ? String(obj.date).trim() : undefined,
            level: obj.level ? String(obj.level).trim() : undefined,
            org: obj.org ? String(obj.org).trim() : undefined,
            score: obj.score ? String(obj.score).trim() : undefined,
          })
        }
      } catch {
        results.push({ name: line })
      }
    } else {
      results.push({ name: line })
    }
  }
  return results
}
