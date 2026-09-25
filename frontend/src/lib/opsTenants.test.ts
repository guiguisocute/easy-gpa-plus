import assert from 'node:assert/strict'
import test from 'node:test'

import type { List, Tenant } from '../api/types.ts'
import { patchTenantAdmin, patchTenantList } from './opsTenants.ts'

const tenants: List<Tenant> = {
  items: [{
    id: '529',
    name: '23级表演班',
    slug: 'class-529',
    state: 'archived',
    archived: true,
    storageBytes: 0,
    storageCalibratedAt: null,
    admins: [],
    createdAt: '2026-08-24T00:00:00Z',
    updatedAt: '2026-08-24T00:00:00Z',
  }],
}

test('启用班级时缓存立即切换为运行状态', () => {
  const next = patchTenantList(tenants, { id: '529', archived: false })
  assert.equal(next?.items[0].archived, false)
  assert.equal(next?.items[0].state, 'running')
  assert.equal(tenants.items[0].archived, true)
})

test('任命班管后列表立即显示新管理员且同一学号不重复', () => {
  const first = patchTenantAdmin(tenants, '529', { sid: '20230001', name: '甲', registered: false })
  const second = patchTenantAdmin(first, '529', { sid: '20230001', name: '乙', registered: true })
  assert.deepEqual(second?.items[0].admins, [{ sid: '20230001', name: '乙', registered: true }])
})
