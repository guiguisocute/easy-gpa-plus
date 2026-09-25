import type { Appeal } from '../api/types.ts'

type Progress = Pick<Appeal, 'round' | 'status' | 'handlers'>

export function needsMyRereview(appeal: Progress) {
  return appeal.round === 1 && appeal.status === 'reviewing' && appeal.handlers.some((handler) => handler.mine && !handler.decided)
}

export function rereviewNotice(appeal: Progress) {
  if (appeal.status === 'final') return '这条申诉已终裁，复评已结束，无需再提交。可查看本轮终裁结论与理由。'
  if (appeal.status === 'resolved') return '这轮申诉已复评结案，无需再提交。可查看本轮处理结论与理由。'
  if (appeal.status === 'escalated' || appeal.round !== 1) return '这条申诉已交终裁人处理，不再接受复评。'
  if (appeal.status !== 'reviewing') return '这条申诉尚未进入复评，请等待分配。'
  if (!appeal.handlers.some((handler) => handler.mine)) return '这条申诉没派给你复评，你现在只能看。'
  return '你已提交复评，等待后续处理，无需重复提交。'
}

export function rereviewProgressLabel(appeal: Progress) {
  const count = `${appeal.handlers.filter((handler) => handler.decided).length}/${appeal.handlers.length || '—'}`
  if (appeal.status === 'final' || appeal.status === 'resolved') return `${count} · 已结束`
  if (appeal.status === 'escalated' || appeal.round !== 1) return `${count} · 待终裁`
  return `${count}${needsMyRereview(appeal) ? ' · 待我' : ''}`
}
