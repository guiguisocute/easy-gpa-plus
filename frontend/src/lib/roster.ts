import type { AdminUser, WhitelistRow } from '@/api/types'
import type { Role } from '@/lib/types'

/* 一行班级成员。account 为 null 就是"在名单里但还没注册"；whitelistId 为 null 是
   名单里查不到的账号——正常不会出现（后端不允许移出已注册的白名单行），留着是为了
   这种人也不会从成员表上凭空消失。 */
export type Member = {
  sid: string
  name: string
  role: Exclude<Role, 'ops'>
  registeredAt: string | null
  whitelistId: string | null
  account: AdminUser | null
}

/* 按学号把白名单和已注册账号并成一张表。

   白名单是主表，账号挂在对应行上。姓名与身份以账号为准：注册之后这两项可能已经
   被改过（升降身份走的是账号那一侧并写审计），名单上留的是注册时的旧值，照着旧值
   显示会让人以为降级没生效。 */
export function mergeMembers(whitelist: WhitelistRow[], accounts: AdminUser[]): Member[] {
  const unmatched = new Map(accounts.map((account) => [account.sid, account]))
  const members = whitelist.map<Member>((row) => {
    const account = unmatched.get(row.sid) ?? null
    unmatched.delete(row.sid)
    return {
      sid: row.sid,
      name: account?.name ?? row.name,
      role: account?.role ?? row.role,
      registeredAt: row.registeredAt,
      whitelistId: row.id,
      account,
    }
  })
  for (const account of unmatched.values()) {
    members.push({
      sid: account.sid,
      name: account.name,
      role: account.role,
      registeredAt: account.createdAt,
      whitelistId: null,
      account,
    })
  }
  return members
}

/** 已启用的学生与综测小组可重置密码；待注册成员没有密码，停用账号需先启用。 */
export function canResetMemberPassword(member: Member): boolean {
  const account = member.account
  return account?.status === 'active' && (account.role === 'student' || account.role === 'group')
}

export function countResettableMemberPasswords(members: Member[]): number {
  return members.filter(canResetMemberPassword).length
}
