/* 小项说明给学生看的那一份：标题下的 Markdown，不含互斥组 key、共享组 key 这类内部字段。 */

import type { SchemeItem } from './types.ts'

export function claimableBaseNote(claim: { full: number; minimum: number; unit: string }): string {
  return [
    `- 完整达标：至少 ${claim.minimum} ${claim.unit}可得 ${claim.full} 分。`,
    `- 未完全达标时，仍可依据已有材料在 0—${claim.full} 分内自报，最终由审核人认定。`,
  ].join('\n')
}

/** 旧方案把几条规则用中文分号串成一段。已经是 Markdown 的原文原样用。 */
export function plainNoteToMarkdown(note: string): string {
  const trimmed = note.trim()
  if (!trimmed) return ''
  if (looksLikeMarkdown(trimmed)) return trimmed
  const clauses = splitNoteClauses(trimmed)
  if (clauses.length >= 2) return clauses.map((clause) => `- ${clause}`).join('\n')
  return trimmed
}

export function itemGuideMarkdown(item: Pick<SchemeItem, 'note' | 'claimableBase' | 'capGroup'>): string {
  const parts: string[] = []
  if (item.note?.trim()) parts.push(plainNoteToMarkdown(item.note.trim()))
  else if (item.claimableBase) parts.push(claimableBaseNote(item.claimableBase))
  if (item.capGroup && !/上限|不超过|封顶/.test(item.note ?? '')) {
    parts.push(`- 与相关小项合计不超过 ${item.capGroup.cap} 分。`)
  }
  return parts.join('\n\n')
}

function looksLikeMarkdown(note: string): boolean {
  return /(?:^|\n)\s*(?:[-*] |\d+\.[ \t]|#{1,4}[ \t]|> )/.test(note)
}

function splitNoteClauses(note: string): string[] {
  return note
    .split(/[；;]/)
    .flatMap((part) => part.split(/(?<=[。．])(?=\S)/))
    .map((part) => part.trim().replace(/[。．;；]+$/u, ''))
    .filter(Boolean)
}
