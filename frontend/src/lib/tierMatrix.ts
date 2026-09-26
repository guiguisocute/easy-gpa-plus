import { enumOptionPath } from './schemeTree'
import type { ScoreRule } from './types'

export type EnumRule = Extract<ScoreRule, { type: 'enum' }>

/** 矩阵只在每一档都是「级别·等次」两段、且等次不止两种时才划得来。 */
export function wantsMatrix(rule: EnumRule) {
  const paths = rule.options.map(enumOptionPath)
  if (paths.length < 6 || !paths.every((path) => path.length === 2)) return false
  return new Set(paths.map((path) => path[1])).size >= 3
}
