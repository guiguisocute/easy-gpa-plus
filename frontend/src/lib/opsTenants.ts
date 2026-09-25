import type { List, Tenant, TenantAdmin } from '@/api/types'

export type TenantUpdate = { id: string; name?: string; archived?: boolean }

export function patchTenantList(current: List<Tenant> | undefined, update: TenantUpdate): List<Tenant> | undefined {
  if (!current) return current
  return {
    ...current,
    items: current.items.map((tenant) => tenant.id !== update.id ? tenant : {
      ...tenant,
      ...(update.name === undefined ? {} : { name: update.name }),
      ...(update.archived === undefined ? {} : {
        archived: update.archived,
        state: update.archived ? 'archived' as const : 'running' as const,
      }),
    }),
  }
}

export function patchTenantAdmin(current: List<Tenant> | undefined, tenantId: string, admin: TenantAdmin): List<Tenant> | undefined {
  if (!current) return current
  return {
    ...current,
    items: current.items.map((tenant) => tenant.id !== tenantId ? tenant : {
      ...tenant,
      admins: [...tenant.admins.filter((item) => item.sid !== admin.sid), admin],
    }),
  }
}
