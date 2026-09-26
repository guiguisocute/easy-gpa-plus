import assert from 'node:assert/strict'
import { test } from 'node:test'
import { QueryClient } from '@tanstack/react-query'
import { clearPreviousSession } from './sessionCache.ts'
import type { User } from './types.ts'

const student: User = { sid: 'student-one', name: '示例学生', classId: 1, className: '示例班', role: 'student', initial: '示', sub: '学生' }

test('退出、换账号、换班或权限变化会清除查询与变更缓存', () => {
  const previous = { user: student, viewAs: null }
  for (const state of [
    { user: null, viewAs: null },
    { user: { ...student, sid: 'student-two' }, viewAs: null },
    { user: { ...student, classId: 2 }, viewAs: null },
    { user: { ...student, role: 'group' as const }, viewAs: null },
    { user: { ...student, isDeputy: true }, viewAs: null },
    { user: student, viewAs: 'student' as const },
  ]) {
    const client = new QueryClient()
    client.setQueryData(['submissions'], { private: 'previous account' })
    client.getMutationCache().build(client, { mutationKey: ['saved-submission'] })
    clearPreviousSession(client, state, previous)
    assert.equal(client.getQueryData(['submissions']), undefined)
    assert.equal(client.getMutationCache().getAll().length, 0)
  }
})

test('清理会话会取消在途查询，旧响应不能重新填充缓存', async () => {
  const client = new QueryClient()
  let signal: AbortSignal | undefined
  let resolve!: (value: string) => void
  const response = new Promise<string>((done) => { resolve = done })
  const pending = client.fetchQuery({ queryKey: ['submissions'], queryFn: (context) => {
    signal = context.signal
    return response
  } })
  const rejected = assert.rejects(pending)
  clearPreviousSession(client, { user: null, viewAs: null }, { user: student, viewAs: null })
  assert.equal(signal?.aborted, true)
  resolve('previous account records')
  await rejected
  assert.equal(client.getQueryData(['submissions']), undefined)
})

test('主题、页面与同一身份信息刷新不会丢弃业务缓存', () => {
  const client = new QueryClient()
  client.setQueryData(['submissions'], 'current data')
  clearPreviousSession(client, { user: { ...student, name: '改名', isDeputy: false }, viewAs: null }, { user: student, viewAs: null })
  assert.equal(client.getQueryData(['submissions']), 'current data')
  client.clear()
})
