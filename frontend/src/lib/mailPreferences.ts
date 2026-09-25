import type { MailCategory, MailPreferences } from '@/api/types'

export const MAIL_CATEGORIES: { key: MailCategory; label: string; detail: string }[] = [
  { key: 'decisions', label: '重要结果', detail: '申诉处理、裁定、强制驳回与班级结算。' },
  { key: 'deadlines', label: '截止提醒', detail: '尚未封存材料的截止提醒，以及逾期待办。' },
  { key: 'results', label: '材料认定结果', detail: '单项材料形成初审结论后的更新。' },
  { key: 'tasks', label: '待办任务', detail: '分配给你的材料审核与申诉处理任务。' },
  { key: 'progress', label: '材料审核进展', detail: '审核进行中、等待另一位审核人或出现分歧等进展。' },
  { key: 'receipts', label: '操作回执', detail: '封存成功与提交申诉的回执。' },
  { key: 'class_activity', label: '班级处理进展', detail: '班级管理员关注的班级材料与成绩处理进展。' },
]
export function mailPreset(preset: 'balanced' | 'important' | 'digest' | 'frequent' | 'off', current?: MailPreferences): MailPreferences {
  const result: MailPreferences = {
    enabled: preset !== 'off', digestTime: current?.digestTime ?? '18:30', quietStart: current?.quietStart ?? '22:00', quietEnd: current?.quietEnd ?? '08:00', dailyLimit: preset === 'frequent' ? 20 : preset === 'off' ? current?.dailyLimit ?? 3 : 3,
    categories: { decisions: 'immediate', deadlines: 'immediate', results: 'digest', tasks: 'digest', progress: 'off', receipts: 'off', class_activity: 'digest' },
  }
  if (preset === 'important') for (const { key } of MAIL_CATEGORIES) result.categories[key] = key === 'decisions' || key === 'deadlines' ? 'immediate' : 'off'
  if (preset === 'digest') for (const { key } of MAIL_CATEGORIES) if (result.categories[key] !== 'off') result.categories[key] = 'digest'
  if (preset === 'frequent') for (const { key } of MAIL_CATEGORIES) result.categories[key] = 'frequent'
  if (preset === 'off' && current) result.categories = { ...current.categories }
  return result
}
