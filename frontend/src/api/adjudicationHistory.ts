import { useQuery } from '@tanstack/react-query'
import { api } from './client'
import type { Evidence, Page } from './types'
import { useApp } from '@/stores/app'

export const HISTORY_KINDS = {
  arbitration: '仲裁定分',
  appeal: '申诉终裁',
  objection: '小组提案',
  report: '学生举报',
  scorecard: '整表问题',
  classification: '分类确认',
  force_reject: '强制驳回',
  force_score: '强制改分',
} as const

export type HistoryKind = keyof typeof HISTORY_KINDS
export interface AdjudicationHistoryRow {
  id: string
  kind: HistoryKind
  studentId: string
  student: string
  title: string
  category: string
  itemKey: string
  beforeCategory: string
  beforeItemKey: string
  beforeScore: number | null
  score: number | null
  decision: string
  reason: string
  createdAt: string
  submissionId: string | null
  appealId: string | null
  objectionId: string | null
  reportId: string | null
  currentScore: number | null
  currentStatus: string | null
}

export interface AdjudicationHistoryDetail extends AdjudicationHistoryRow {
  note: string
  sourceReason: string
  evidence: Evidence[]
  reviews: { reviewer: string; stage: string; decision: string; score: number; reason: string; at: string; superseded: boolean }[]
}

export function useAdjudicationHistory(filter: { kind: string; student: string; q: string; from: string; until: string; page: number }) {
  const user = useApp((state) => state.user)
  const params = new URLSearchParams({ page_size: '25' })
  for (const [key, value] of Object.entries(filter)) if (value !== '') params.set(key, String(value))
  const query = params.toString()
  return useQuery({
    queryKey: ['admin', 'adjudication-history', user?.classId, user?.sid, query],
    queryFn: ({ signal }) => api.get<Page<AdjudicationHistoryRow>>(`/admin/adjudication-history?${query}`, signal),
  })
}

export function useAdjudicationHistoryDetail(id: string) {
  const user = useApp((state) => state.user)
  return useQuery({
    queryKey: ['admin', 'adjudication-history', user?.classId, user?.sid, 'detail', id],
    queryFn: ({ signal }) => api.get<AdjudicationHistoryDetail>(`/admin/adjudication-history/${id}`, signal),
  })
}
