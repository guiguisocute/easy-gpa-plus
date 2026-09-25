/* 申诉轨迹的人话。

   后端把这条申诉在 audit_log 里的全部记录原样下发（appeal_handlers.go appealTrail），
   其中大半是「谁看了一眼」——appeal.read、appeal.list、预签发链接。它们对读轨迹的人
   没有意义，却会把真正发生过的那四五件事淹掉，于是整块「轨迹」看着像调试输出。

   这里做两件事：把只读的动作滤掉，把剩下的动作名翻译成中文。认不出来的动作原样保留，
   不假装认识它——轨迹是留档，宁可显示一个陌生的键，也不能悄悄吞掉一步。 */

export interface TrailStep {
  action: string
  at: string
}

/** 只是"看了一下"的动作。轨迹要回答的是"这一分怎么变成现在这样的"。 */
const READ_ONLY = /\.(read|list|admin_list|presigned)$/

const LABEL: Record<string, string> = {
  'appeal.draft_created': '学生开始写申诉',
  'appeal.draft_deleted': '学生删掉了申诉草稿',
  'appeal.withdrawn': '学生撤回申诉并继续修改',
  'appeal.filed': '学生提交申诉',
  'appeal.evidence_completed': '学生补传佐证',
  'appeal.evidence_deleted': '学生撤回一份佐证',
  'appeal.assigned': '派回原审核人复评',
  'appeal.rereviewed': '原审核人提交复评',
  'appeal.resolved': '两人复评一致，本轮结案',
  'appeal.escalated': '复评不一致，升给班级管理员',
  'appeal.escalated_round2': '交班级管理员终裁',
  'appeal.final': '班级管理员终裁',
  'note_evidence.completed': '补充理由附件',
  'note_evidence.deleted': '移除理由附件',
  'objection.applied': '提案照准生效',
  'objection.dismissed': '提案被驳回',
}

export function trailLabel(action: string): string {
  return LABEL[action] ?? action
}

/** 只保留改变了状态的那几步，按后端给的顺序。 */
export function readableTrail(items: TrailStep[] | undefined | null): { title: string; at: string; action: string }[] {
  return (items ?? [])
    .filter((step) => !READ_ONLY.test(step.action))
    .map((step) => ({ title: trailLabel(step.action), at: step.at, action: step.action }))
}
