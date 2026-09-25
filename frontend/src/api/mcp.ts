import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'

export interface AgentConnection {
  id: string; name: string; prefix: string; scopes: string[]; expiresAt: string
  revokedAt: string | null; createdAt: string; lastUsedAt: string | null
}
export interface AgentConnectionsResponse {
  items: AgentConnection[]; url: string; enabled: boolean; maxTTLMinutes: number; demo?: boolean
  scopes: { key: string; label: string; description: string }[]
}
export interface AgentOperation {
  id: string; connectionId: string; connectionName: string; tool: string
  status: 'pending' | 'approved' | 'rejected' | 'applied'
  preview: { title?: string; input?: unknown; before?: unknown; targets?: unknown; notice?: string }
  createdAt: string; expiresAt: string; approvedAt: string | null; connectionActive: boolean
}
export interface CreatedConnection { id: string; name: string; token: string; url: string; expiresAt: string; scopes: string[] }
const connectionsKey = ['agent-connections'] as const
const operationsKey = ['agent-operations'] as const
export function useAgentConnections() {
  return useQuery({ queryKey: connectionsKey, queryFn: () => api.get<AgentConnectionsResponse>('/me/agent-connections'), staleTime: 0 })
}
export function useAgentOperations() {
  return useQuery({ queryKey: operationsKey, queryFn: () => api.get<{ items: AgentOperation[] }>('/me/agent-operations'), staleTime: 0 })
}
// Keep the one-time secret out of React Query's mutation cache and all storage.
export const createAgentConnection = (input: { name: string; password: string; ttlMinutes: number; scopes: string[] }) => api.post<CreatedConnection>('/me/agent-connections', input)
export function useAgentConnectionActions() {
  const client = useQueryClient()
  const refresh = () => { void client.invalidateQueries({ queryKey: connectionsKey }); void client.invalidateQueries({ queryKey: operationsKey }) }
  const revoke = useMutation({ mutationFn: (id: string) => api.del(`/me/agent-connections/${id}`), onSuccess: refresh })
  const decide = useMutation({ mutationFn: ({ id, approve }: { id: string; approve: boolean }) => api.post(`/me/agent-operations/${id}/decision`, { approve }), onSuccess: refresh })
  return { revoke, decide, refresh }
}
