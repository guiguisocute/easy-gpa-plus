import assert from 'node:assert/strict'
import test from 'node:test'
import { governanceQueue } from './governanceView.ts'
import type { ProposalView } from '../api/governance.ts'

test('proposal and review queues stay separate and submitted opinions are not pending work', () => {
  const row = (
    id: string,
    kind: string,
    status: string,
    eligible = true,
    mySubmitted = false,
  ) =>
    ({
      proposal: { id, kind, status },
      eligible,
      mySubmitted,
      myChoice: null,
    }) as ProposalView
  const items = [
    row('vote', 'ordinary', 'voting'),
    row('review', 'review', 'voting'),
    row('opinion', 'review', 'voting', true, true),
    row('subject', 'appeal', 'voting', false),
    row('finished', 'review', 'applied'),
  ]
  assert.deepEqual(
    governanceQueue(items, false, 'all').map((i) => i.proposal.id),
    ['vote'],
  )
  assert.deepEqual(
    governanceQueue(items, true, 'mine').map((i) => i.proposal.id),
    ['review'],
  )
  assert.deepEqual(
    governanceQueue(items, true, 'done').map((i) => i.proposal.id),
    ['finished'],
  )
})
