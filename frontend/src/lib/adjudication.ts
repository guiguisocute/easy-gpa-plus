export interface AdjudicationPermission {
  canAdjudicate?: boolean
  recusalReason?: string
}

/** 服务端结果优先，学号比对兼容较旧的列表数据。最终权限始终由服务端核验。 */
export function isAdjudicationRecused(item: AdjudicationPermission, studentSid: string | undefined, mySid: string | undefined): boolean {
  return item.canAdjudicate === false || (!!mySid && mySid === studentSid)
}

export const DEPUTY_RECUSAL_NOTE = '本人事项由副班管处理。'
