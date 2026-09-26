import type { ReviewDecision } from '@/api/types'

export interface DeskChoice {
  key: ReviewDecision
  label: string
  icon: string
  tone: 'ok' | 'warn' | 'bad'
  needScore: boolean
  needWhy: boolean
}

export const DESK_CHOICES: DeskChoice[] = [
  { key: 'accepted', label: '通过 · 按期望分', icon: 'M5 13l4 4L19 7', tone: 'ok', needScore: false, needWhy: false },
  { key: 'adjusted', label: '调整认定分', icon: 'M4 12h16M12 4v16', tone: 'warn', needScore: true, needWhy: true },
  { key: 'rejected', label: '驳回 · 计 0 分', icon: 'M6 6l12 12M18 6L6 18', tone: 'bad', needScore: false, needWhy: true },
]

/** 换几句措辞用同一套档位。共治的「驳回」在异议案件里是「维持原分」，形状不变。 */
export function deskChoices(labels: Partial<Record<ReviewDecision, string>>): DeskChoice[] {
  return DESK_CHOICES.map((choice) => ({ ...choice, label: labels[choice.key] ?? choice.label }))
}

export const isDeskDecision = (value: unknown): value is ReviewDecision =>
  typeof value === 'string' && DESK_CHOICES.some((choice) => choice.key === value)
