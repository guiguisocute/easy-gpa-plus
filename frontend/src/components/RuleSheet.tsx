/* 规则速查表。

   原来只长在综测小组的「我的审核量」页上，因为那时的前提是"每人固定负责一个大项"，
   速查表是给审核人补细则用的。分发改成按条均衡之后那个前提没了，规则本身也不是秘密：
   它就是已发布方案的人话版本，学生按它准备材料、审核人按它认定，看的必须是同一份。

   所以这里只做一件事：把已发布方案原样念一遍。不加解释、不做换算——
   任何"我们理解的规则"都会和文件漂移，漂移之后没人知道以哪个为准。 */

import { useState } from 'react'
import { Row, Seg, Sub } from '@/components/ui'
import { RichText } from '@/components/Markdown'
import * as f from '@/lib/format'
import { itemGuideMarkdown } from '@/lib/itemGuide'
import { claimableItems } from '@/lib/schemeTree'
import type { SchemeConfig, SchemeItem } from '@/lib/types'

function sharedCapLines(items: SchemeItem[]) {
  const groups = new Map<string, { cap: number; names: string[] }>()
  for (const item of items) {
    if (!item.capGroup) continue
    const found = groups.get(item.capGroup.key)
    if (found) found.names.push(item.name)
    else groups.set(item.capGroup.key, { cap: item.capGroup.cap, names: [item.name] })
  }
  return [...groups.values()].map((group) =>
    group.names.length === 1
      ? `${group.names[0]} 合计不超过 ${group.cap} 分`
      : `${group.names.join('、')} 合计不超过 ${group.cap} 分`,
  )
}

function exclusiveLines(items: SchemeItem[]) {
  const groups = new Map<string, string[]>()
  for (const item of items) {
    if (!item.exclusiveGroup) continue
    const found = groups.get(item.exclusiveGroup)
    if (found) found.push(item.name)
    else groups.set(item.exclusiveGroup, [item.name])
  }
  return [...groups.values()].map((names) =>
    names.length === 1 ? `${names[0]} 多条只取最高` : `${names.join('、')} 只取最高一条`,
  )
}

export function RuleSheet({ config, heading = true }: { config: SchemeConfig; heading?: boolean }) {
  const categories = config.categories ?? []
  const [cat, setCat] = useState<string | null>(null)
  const active = categories.find((c) => c.key === (cat ?? categories[0]?.key)) ?? categories[0]

  if (!active) return <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>这份方案还没有大项。</div>

  const claimable = claimableItems(active)
  const capLines = sharedCapLines(claimable)
  const exclusive = exclusiveLines(claimable)
  const noted = claimable.filter((item) => itemGuideMarkdown(item))

  return (
    <>
      {/* 折叠区自己已经有标题时不必再来一遍。 */}
      {heading && <Sub title="规则速查" note={['取自已发布方案', f.schemeStamp(config)].filter(Boolean).join(' · ')} />}
      {categories.length > 1 && (
        <div style={{ paddingBottom: 14 }}>
          <Seg items={categories.map((c) => ({ key: c.key as string, label: c.name }))} value={active.key as string} onChange={setCat} />
        </div>
      )}

      {claimable.map((item) => (
        <Row key={item.key} label={item.name} value={item.claimableBase ? `条件基础分 · 满分 ${item.claimableBase.full}` : f.ruleText(item.scoreRule)} />
      ))}
      {claimable.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>本大项没有可申报的小项。</div>}

      <div style={{ borderTop: '1px solid var(--line)', marginTop: 16, paddingTop: 6 }}>
        <Row label="大项上限" value={`${active.maxTotal} 分`} />
        <Row
          label="基础项"
          value={active.baseItems.map((b) => (b.studentClaim ? `${b.name} ${b.full} 分（须申报，至少 ${b.studentClaim.minimum} ${b.studentClaim.unit}）` : `${b.name} ${b.full} 分`)).join(' · ') || '无'}
        />
        <Row label="扣分项" value={active.penaltyItems.map((p) => `${p.name} 每次 ${p.per} 分`).join(' · ') || '无'} />
        {capLines.length > 0 && <Row label="共享上限" value={capLines.join('；')} />}
        {exclusive.length > 0 && <Row label="多条只取最高" value={exclusive.join('；')} />}
      </div>

      {noted.length > 0 && (
        <div style={{ borderTop: '1px solid var(--line)', marginTop: 16, paddingTop: 14, display: 'flex', flexDirection: 'column', gap: 14 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>小项说明</span>
          {noted.map((item) => (
            <div key={item.key} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg)' }}>{item.name}</span>
              <RichText value={itemGuideMarkdown(item)} />
            </div>
          ))}
        </div>
      )}
    </>
  )
}
