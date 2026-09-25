/* 冻结规则 vs 现行方案。

   每条申报都冻结了提交当时的那一份规则，定分必须按冻结的那份走——学生是照着
   它准备材料的，事后改了方案再回头按新规矩扣他分，等于规则溯及既往。

   但审核人有权知道方案后来动过：一条按旧档位算出 15 分的申报，如果现行方案
   已经把那一档降到 8 分，他至少要意识到自己签的这个数和班里刚提交的同类材料
   不是一个口径，必要时可以升给班管统一处理。

   所以这里只回答「有没有变、变了哪些」，不替审核人做决定，也不改分。 */

import type { SchemeConfig, SchemeItem, ScoreRule } from './types.ts'

export interface RuleChange {
  /** 给人读的一句话，如「二等：20 → 12 分」。 */
  label: string
  was: string
  now: string
}

export interface RuleDiff {
  /** 现行方案里还有没有这个小项。被删掉时 changes 为空，但仍要提示。 */
  removed: boolean
  changes: RuleChange[]
}

const num = (v: number | null | undefined) => (v == null ? '—' : String(v))

/** 在现行方案里按 key 找同一个小项。找不到返回 null（可能已被删除或改键）。 */
export function findCurrentItem(config: SchemeConfig | undefined, itemKey: string): SchemeItem | null {
  if (!config) return null
  for (const category of config.categories) {
    const found = category.items.find((item) => item.key === itemKey)
    if (found) return found
  }
  return null
}

function ruleKindText(rule: ScoreRule): string {
  if (rule.type === 'enum') return `按档位认定 · ${rule.options.length} 档`
  if (rule.type === 'per_unit') return `每 ${rule.unit} ${rule.per} 分`
  if (rule.type === 'threshold') return `达标即计 ${rule.award} 分`
  return `区间 ${rule.min} — ${rule.max} 分`
}

function capText(rule: ScoreRule): string | null {
  if (rule.type === 'per_unit') return rule.cap == null ? '不封顶' : `${rule.cap} 分`
  return null
}

/**
 * 比对同一个小项的冻结版与现行版。
 *
 * 只比会影响分数或影响审核人判断的字段：名称、计分方式、每档分值、封顶、
 * 互斥组、共享封顶。说明文字（note）与佐证类型不进来——那些改动不改变分数，
 * 摆进来只会让每一条申报都跳一个「规则已变更」，久了就没人看了。
 */
export function diffRule(frozen: SchemeItem, current: SchemeItem | null): RuleDiff {
  if (!current) return { removed: true, changes: [] }
  const changes: RuleChange[] = []

  if (frozen.name !== current.name) {
    changes.push({ label: '小项名称', was: frozen.name, now: current.name })
  }

  const a = frozen.scoreRule
  const b = current.scoreRule
  if (a.type !== b.type) {
    changes.push({ label: '计分方式', was: ruleKindText(a), now: ruleKindText(b) })
    return { removed: false, changes }
  }

  if (a.type === 'per_unit' && b.type === 'per_unit') {
    if (a.per !== b.per || a.unit !== b.unit) {
      changes.push({ label: '单位分值', was: `每 ${a.unit} ${a.per} 分`, now: `每 ${b.unit} ${b.per} 分` })
    }
    if (a.cap !== b.cap) {
      changes.push({ label: '小项封顶', was: capText(a) ?? '—', now: capText(b) ?? '—' })
    }
  }

  if (a.type === 'free' && b.type === 'free') {
    if (a.min !== b.min || a.max !== b.max) {
      changes.push({ label: '认定区间', was: `${a.min} — ${a.max} 分`, now: `${b.min} — ${b.max} 分` })
    }
  }

  if (a.type === 'threshold' && b.type === 'threshold') {
    if (a.minimum !== b.minimum || a.award !== b.award) {
      changes.push({ label: '达标线与分值', was: `≥${a.minimum} ${a.unit} 计 ${a.award} 分`, now: `≥${b.minimum} ${b.unit} 计 ${b.award} 分` })
    }
  }

  if (a.type === 'enum' && b.type === 'enum') {
    const now = new Map(b.options.map((option) => [option.label, option.score]))
    const was = new Map(a.options.map((option) => [option.label, option.score]))
    for (const option of a.options) {
      if (!now.has(option.label)) {
        changes.push({ label: `档位「${option.label}」`, was: `${option.score} 分`, now: '已删除' })
      } else if (now.get(option.label) !== option.score) {
        changes.push({ label: `档位「${option.label}」`, was: `${option.score} 分`, now: `${now.get(option.label)} 分` })
      }
    }
    for (const option of b.options) {
      if (!was.has(option.label)) {
        changes.push({ label: `档位「${option.label}」`, was: '（当时没有）', now: `${option.score} 分` })
      }
    }
  }

  if ((frozen.exclusiveGroup ?? '') !== (current.exclusiveGroup ?? '')) {
    changes.push({ label: '互斥组', was: frozen.exclusiveGroup || '无', now: current.exclusiveGroup || '无' })
  }

  const frozenCap = frozen.capGroup
  const currentCap = current.capGroup
  if ((frozenCap?.key ?? '') !== (currentCap?.key ?? '') || (frozenCap?.cap ?? null) !== (currentCap?.cap ?? null)) {
    changes.push({
      label: '共享封顶',
      was: frozenCap ? `${frozenCap.key} · ${num(frozenCap.cap)} 分` : '无',
      now: currentCap ? `${currentCap.key} · ${num(currentCap.cap)} 分` : '无',
    })
  }

  return { removed: false, changes }
}

export const hasRuleDrift = (diff: RuleDiff) => diff.removed || diff.changes.length > 0
