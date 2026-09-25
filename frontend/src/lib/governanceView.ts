import type { ProposalView } from '../api/governance'

export const statusName: Record<string, string> = {
  discussion: '讨论公示中',
  voting: '进行中',
  passed: '已通过 · 待生效',
  applied: '已执行',
  rejected: '未通过',
  stale: '依据已变化',
  blocked: '等待补位或补证',
  deliberating: '共同评议中',
}
export const governanceDate = (value: string) =>
  new Date(value).toLocaleString('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
export const isReview = (item: ProposalView) => ['review', 'appeal'].includes(item.proposal.kind)
export const isFinished = (item: ProposalView) =>
  ['applied', 'rejected', 'stale'].includes(item.proposal.status)
export function governanceQueue(items: ProposalView[], reviews: boolean, filter: string) {
  return items.filter(
    (item) =>
      isReview(item) === reviews &&
      (filter === 'all' ||
        (filter === 'active' && !isFinished(item)) ||
        (filter === 'done' && isFinished(item)) ||
        (filter === 'mine' &&
          item.eligible &&
          !item.mySubmitted &&
          !item.myChoice &&
          !isFinished(item))),
  )
}

export const statusTone: Record<string, 'ok' | 'warn' | 'bad' | 'idle'> = {
  discussion: 'idle',
  voting: 'warn',
  passed: 'ok',
  applied: 'ok',
  rejected: 'bad',
  stale: 'warn',
  blocked: 'warn',
  deliberating: 'warn',
}
