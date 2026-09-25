import assert from 'node:assert/strict'
import test from 'node:test'
import { canResetMemberPassword, countResettableMemberPasswords, mergeMembers } from './roster.ts'
import type { AdminUser, WhitelistRow } from '../api/types.ts'

test('未注册的名单行没有账号，也没有可重置的密码', () => {
  const members = mergeMembers([listed('2023211001', '王一')], [])
  assert.equal(members.length, 1)
  assert.equal(members[0].account, null)
  assert.equal(members[0].whitelistId, 'w1')
  assert.equal(members[0].role, 'student')
  assert.equal(canResetMemberPassword(members[0]), false)
})

/* 升降身份走的是账号那一侧，名单上留的是注册时的旧值。照着旧值显示会让人以为
   降级没生效，所以已注册的行必须以账号为准。 */
test('已注册的行以账号的姓名和身份为准，而不是名单上的旧值', () => {
  const members = mergeMembers(
    [{ ...listed('2023211001', '王一'), role: 'student' }],
    [{ ...account('2023211001', '王一改'), role: 'group' }],
  )
  assert.equal(members.length, 1)
  assert.equal(members[0].name, '王一改')
  assert.equal(members[0].role, 'group')
  assert.equal(members[0].account?.sealed, false)
  assert.equal(canResetMemberPassword(members[0]), true)
})

// 后端不允许移出已注册的白名单行，所以这种账号正常不存在；真出现了也不能让人从表上消失。
test('名单里查不到的账号仍然补在表尾，且不给移出入口', () => {
  const members = mergeMembers([listed('2023211001', '王一')], [account('2023211099', '孤儿账号')])
  assert.deepEqual(members.map((m) => m.sid), ['2023211001', '2023211099'])
  assert.equal(members[1].whitelistId, null)
  assert.ok(members[1].account)
})

test('每个学号只出现一行', () => {
  const members = mergeMembers(
    [listed('2023211001', '王一'), listed('2023211002', '李二', 'w2')],
    [account('2023211001', '王一')],
  )
  assert.equal(members.length, 2)
  assert.equal(new Set(members.map((m) => m.sid)).size, 2)
})

test('已启用的学生和综测小组可重置密码，批量计数排除待注册、停用和班管账号', () => {
  const members = mergeMembers(
    [
      listed('2023211001', '学生'),
      listed('2023211002', '未注册', 'w2'),
      { ...listed('2023211003', '小组', 'w3'), role: 'group' },
      { ...listed('2023211004', '班管', 'w4'), role: 'class_admin' },
      listed('2023211005', '停用学生', 'w5'),
      { ...listed('2023211006', '停用小组', 'w6'), role: 'group' },
      { ...listed('2023211007', '待注册小组', 'w7'), role: 'group' },
    ],
    [
      account('2023211001', '学生'),
      { ...account('2023211003', '小组'), role: 'group' },
      { ...account('2023211004', '班管'), role: 'class_admin' },
      { ...account('2023211005', '停用学生'), status: 'disabled' },
      { ...account('2023211006', '停用小组'), role: 'group', status: 'disabled' },
    ],
  )
  assert.equal(canResetMemberPassword(members[0]), true)
  assert.equal(canResetMemberPassword(members[1]), false)
  assert.equal(canResetMemberPassword(members[2]), true)
  assert.equal(canResetMemberPassword(members[3]), false)
  assert.equal(canResetMemberPassword(members[4]), false)
  assert.equal(canResetMemberPassword(members[5]), false)
  assert.equal(canResetMemberPassword(members[6]), false)
  assert.equal(countResettableMemberPasswords(members), 2)
})

function listed(sid: string, name: string, id = 'w1'): WhitelistRow {
  return { id, sid, name, role: 'student', active: true, registered: false, registeredAt: null, createdAt: '2026-08-01T00:00:00Z' }
}

function account(sid: string, name: string): AdminUser {
  return {
    id: 'u' + sid, sid, name, role: 'student', status: 'active', primaryEmail: `${sid}@example.edu`,
    sealed: false, lastLoginAt: null, createdAt: '2026-08-02T00:00:00Z',
  }
}
