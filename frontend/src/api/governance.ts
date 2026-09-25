import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'
import { useApp } from '@/stores/app'
import type { Claim, Evidence } from './types'
import type { SchemeItem } from '@/lib/types'

export interface GovernanceState {
  config: {
    mode: 'centralized' | 'enrolling' | 'collective'
    version: number
    profile: 'compact' | 'standard' | null
    enrollmentCloseAt: string | null
  }
  counts: {
    roster: number
    registered: number
    submitted: number
    enrolled: number
    electorate: number
    reviewers: number
  }
  mine: {
    id: string
    joinedAt: string | null
    leftAt: string | null
    reviewer: boolean
  }
  canConfigure: boolean
  ordinaryRequired: number
  protectedRequired: number
  capacityReason: string
  suggestedProfile: { name: string; maximum: number; appeal: number }
}
export interface GovernanceProposal {
  id: string
  kind: string
  action: string
  title: string
  body: string
  payload: Record<string, unknown>
  status: string
  electorateCount: number
  rosterCount: number
  requiredYes: number
  opensAt: string
  closesAt: string
  result?: { jobId?: string }
}
export interface ProposalView {
  proposal: GovernanceProposal
  participated: number
  eligible: boolean
  myChoice: string | null
  mySubmitted?: boolean
  summary?: string
  evidence?: Evidence[]
  tally?: { yes: number; no: number; abstain: number }
}
export interface CaseDetail {
  version: string
  changed: boolean
  proposal: GovernanceProposal
  title: string
  body: string
  category: string
  itemKey: string
  currentScore: number | null
  ruleSnapshot: { item: SchemeItem; categoryName?: string; capturedAt?: string }
  evidence: Evidence[]
  canReview: boolean
  myOpinion: Record<string, unknown>
  /** 当事人。评审与当事人本人可读；评审和举报人的身份任何接口都不下发。 */
  student: string
  studentId: string
  /** 本轮已交独立意见的份数。只有份数，没有谁、也没有内容。 */
  submitted: number
  /** 以下三项只有挂着提交条目的案件才有。 */
  claim?: Claim | null
  requestedScore?: number | null
  submittedAt?: string | null
  opinions?: {
    opinion: {
      score: number
      category: string
      itemKey: string
      decision?: string
    }
    reason: string
    hash: string
  }[]
}
export function useGovernance() {
  const user = useApp((s) => s.user)
  return useQuery({
    queryKey: ['governance', user?.classId, user?.sid, 'state'],
    queryFn: () => api.get<GovernanceState>('/governance'),
    enabled: !!user && user.role !== 'ops',
    staleTime: 15_000,
  })
}
export function useGovernanceProposals(enabled: boolean) {
  const user = useApp((s) => s.user)
  return useQuery({
    queryKey: ['governance', user?.classId, user?.sid, 'proposals'],
    queryFn: () => api.get<{ items: ProposalView[] }>('/governance/proposals'),
    enabled: !!user && enabled,
    refetchInterval: 15_000,
  })
}
export function useGovernanceCase(id: string | null) {
  const user = useApp((s) => s.user)
  return useQuery({
    queryKey: ['governance', user?.classId, user?.sid, 'case', id],
    queryFn: () => api.get<CaseDetail>(`/governance/cases/${id}`),
    enabled: !!id,
    refetchInterval: 15_000,
  })
}
export function useGovernanceAction() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({
      path,
      body,
      method = 'post',
    }: {
      path: string
      body?: unknown
      method?: 'put' | 'post' | 'delete'
    }) =>
      method === 'delete'
        ? api.del<{ status?: string; notice?: string }>(`/governance${path}`)
        : api[method]<{ status?: string; notice?: string }>(
            `/governance${path}`,
            body,
          ),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['governance'] })
    },
  })
}
export function useGovernanceComments(id: string) {
  const user = useApp((s) => s.user)
  return useQuery({
    queryKey: ['governance', user?.classId, user?.sid, 'comments', id],
    queryFn: () =>
      api.get<{
        items: {
          id: string
          body: string
          kind: string
          createdAt: string
          mine: boolean
        }[]
      }>(`/governance/proposals/${id}/comments`),
  })
}
