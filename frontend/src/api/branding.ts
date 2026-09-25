import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'

export interface Branding { svg: string; revision: string }
const key = ['branding'] as const
export const crestSource = (svg: string) => `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`
export function useBranding() {
  return useQuery({ queryKey: key, queryFn: () => api.get<Branding>('/branding'), staleTime: 0,
    refetchInterval: 30_000, refetchOnWindowFocus: true, retry: false })
}
export function useUpdateBranding() {
  const client = useQueryClient()
  return useMutation({ mutationFn: (svg: string) => api.put<Branding>('/ops/branding', { svg }), onSuccess: (data) => { client.setQueryData(key, data) } })
}
