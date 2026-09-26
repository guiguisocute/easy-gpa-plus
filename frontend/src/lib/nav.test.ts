import assert from 'node:assert/strict'
import test from 'node:test'
import {
  COLLECTIVE_NAV,
  NAV,
  navGroups,
  workspaceNav,
  workspaceView,
} from './nav.ts'

test('ordinary workspaces contain no collective pages; collective navigation is identical for every business role', () => {
  for (const role of ['student', 'group', 'class_admin'] as const) {
    assert.equal(
      NAV[role].some(
        (n) => n.view === 'classGovernance' || n.view.startsWith('gov'),
      ),
      false,
    )
    assert.deepEqual(workspaceNav(role, true), COLLECTIVE_NAV)
    assert.equal(
      COLLECTIVE_NAV.some(
        (n) => n.view.startsWith('adm') || n.view.startsWith('rev'),
      ),
      false,
    )
    assert.equal(workspaceView(workspaceNav(role, true), 'admBoard'), 'govHome')
    assert.equal(
      workspaceView(workspaceNav(role, false), 'govProposals'),
      NAV[role][0].view,
    )
  }
  assert.deepEqual(workspaceNav('ops', true), NAV.ops)
})

test('repeated group labels have distinct stable keys across role and mode changes', () => {
  const repeated = [NAV.class_admin[0], NAV.class_admin[4], NAV.class_admin[1]]
  const groups = navGroups(repeated, '默认')
  assert.deepEqual(
    groups.map((g) => g.title),
    ['班级', '评定设置', '班级'],
  )
  assert.equal(new Set(groups.map((g) => g.id)).size, 3)
  for (const entries of [...Object.values(NAV), COLLECTIVE_NAV]) {
    const rendered = navGroups(entries, '默认').flatMap((g) =>
      g.entries.map((n) => n.view),
    )
    assert.equal(new Set(rendered).size, rendered.length)
  }
})

test('ops groups use stable identities and contain every control once', () => {
  const groups = navGroups(NAV.ops, '运维控制台')
  assert.deepEqual(groups.map(({ id, title }) => ({ id, title })), [
    { id: 'ops-operations', title: '日常运营' },
    { id: 'ops-capabilities', title: '平台能力' },
    { id: 'ops-maintenance', title: '系统维护' },
  ])
  assert.deepEqual(groups.flatMap((group) => group.entries.map((entry) => entry.view)), NAV.ops.map((entry) => entry.view))
  const relabeled = NAV.ops.map((entry) => entry.groupId === 'ops-capabilities' ? { ...entry, group: '能力设置' } : entry)
  assert.deepEqual(navGroups(relabeled, '运维').map((group) => group.id), groups.map((group) => group.id))
})
