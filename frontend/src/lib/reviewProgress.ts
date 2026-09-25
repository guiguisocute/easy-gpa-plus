import type { AdminReviewProgressRow } from '@/api/types'
import type { View } from '@/lib/nav'

export type ProgressKind = 'all' | 'item' | 'scorecard'
export const PROGRESS_STATUSES = new Set(['all', 'unfinished', 'overdue', 'unassigned', '0/2', '1/2', 'pending_admin', 'blocked', 'complete'])

export const PROGRESS_LIFECYCLE = [
  { key: 'unassigned', label: '未分配', bg: 'color-mix(in srgb, var(--fg) 10%, transparent)', hint: '还没有分发到审核人手上' },
  { key: '0/2', label: '0/2', bg: 'color-mix(in srgb, var(--fg) 22%, transparent)', hint: '已分发，两名审核人都还没提交结论' },
  { key: '1/2', label: '1/2', bg: 'color-mix(in srgb, var(--fg) 42%, transparent)', hint: '一个人交了，等另一个。这期间两人互相看不到对方写了什么' },
  { key: 'pending_admin', label: '待你终裁', bg: 'var(--red)', hint: '两人都交了，或者两人有分歧，等你仲裁、终裁' },
  { key: 'blocked', label: '阻塞', bg: 'color-mix(in srgb, var(--red) 40%, var(--fg3))', hint: '前置事项没完成，这一轮被挡住了' },
  { key: 'complete', label: '已定分', bg: 'var(--ok)', hint: '已经定分，或者问题已经处理完' },
] as const

export function progressStatusLabel(row: AdminReviewProgressRow) {
  if (row.forceRejection) return '已强制驳回 · 0 分'
  if (row.kind === 'scorecard' && row.status === 'complete') return '复核完成'
  return PROGRESS_LIFECYCLE.find((part) => part.key === row.status)?.label ?? row.status
}

export function openReviewProgress(go: (view: View) => void, status = 'all', kind: ProgressKind = 'all') {
  const params = new URLSearchParams({ v: 'admReviewProgress', status })
  if (kind !== 'all') params.set('kind', kind)
  window.history.replaceState(null, '', `?${params}`)
  go('admReviewProgress')
}
