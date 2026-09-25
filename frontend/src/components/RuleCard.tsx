/* 审核台的「适用规则」。审核工作台与复评工作台共用。

   学生写材料时看到的是 EnumOptionPicker：一档一个胶囊、选中的实心、建议分跟在档名后面。
   审核台原来把同一份规则交给 f.ruleText 压成一行——十几个档位首尾相连，分数混在档名里，
   审核人要在一行 12.5px 的小字里找出"学生选的是哪一档、这一档该给几分"。

   这里按规则本来的形状铺开：单级档位竖排、分数单独占一列；两级档位排成矩阵
   （级别 × 等次），学生选中的那一格高亮。档名与分值仍原样取自已发布方案，
   不做任何换算、合并或改写——任何"我们理解的规则"都会和文件漂移。

   方案在提交之后改过时，把现行那份**原样再渲染一遍**，而不是列一张「哪里变了」的
   差异表。差异表是我们对规则的解读，读者没法从它复原出现行规则本来长什么样；
   两份都原样摆出来，谁变了、变成什么样，审核人自己看得出来。 */

import type { ReactNode } from 'react'
import { RichText } from '@/components/Markdown'
import { TierMatrix, wantsMatrix, type EnumRule } from '@/components/TierMatrix'
import { ActivityTable } from '@/components/ActivityTable'
import { scoreBounds } from '@/lib/claim'
import { itemGuideMarkdown } from '@/lib/itemGuide'
import { enumOptionPath } from '@/lib/schemeTree'
import { diffRule, hasRuleDrift } from '@/lib/ruleDiff'
import { num } from '@/lib/style'
import * as f from '@/lib/format'
import type { Claim } from '@/api/types'
import type { ScoreRule, SchemeItem } from '@/lib/types'

function Label({ children }: { children: ReactNode }) {
  return <span style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>{children}</span>
}

/** 「名称 — 值」，值比 ui.Row 大一号：审核人读的就是这几个数。 */
function Fact({ label, value, tone }: { label: string; value: ReactNode; tone?: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'baseline', gap: 14, padding: '10px 0', borderTop: '1px solid var(--line2)' }}>
      <span style={{ fontSize: 12.5, color: 'var(--fg3)', flex: 'none' }}>{label}</span>
      <span style={{ marginLeft: 'auto', textAlign: 'right', fontSize: 14, fontWeight: 600, color: tone ?? 'var(--fg)', minWidth: 0, ...num }}>{value}</span>
    </div>
  )
}

/** 单级档位：一行一档，分数右对齐成一列。学生选中的那一行带红边。 */
function TierList({ rule, picked }: { rule: EnumRule; picked?: string }) {
  return (
    <div>
      {rule.options.map((option, index) => {
        const on = option.label === picked
        return (
          <div
            key={`${option.label}-${index}`}
            style={{
              display: 'flex', alignItems: 'baseline', gap: 14,
              borderTop: index === 0 ? 0 : '1px solid var(--line2)',
              borderLeft: `2px solid ${on ? 'var(--red)' : 'transparent'}`,
              background: on ? 'var(--redBg)' : 'transparent',
              padding: '10px 12px',
            }}
          >
            <span style={{ fontSize: 13.5, lineHeight: 1.6, fontWeight: on ? 600 : 400, color: on ? 'var(--fg)' : 'var(--fg2)', minWidth: 0, textWrap: 'pretty' }}>
              {option.label}
            </span>
            <span style={{ marginLeft: 'auto', flex: 'none', fontSize: 15, fontWeight: 600, color: on ? 'var(--red)' : 'var(--fg)', ...num }}>
              {f.score(option.score)}
            </span>
          </div>
        )
      })}
    </div>
  )
}

function ruleKind(rule: ScoreRule): string {
  if (rule.type === 'enum') return `按档位认定 · 共 ${rule.options.length} 档`
  if (rule.type === 'per_unit') return `按${rule.unit}计分`
  if (rule.type === 'threshold') return '达标即计分'
  return '由审核人在区间内认定'
}

/** 一份规则的完整呈现。冻结版与现行版走同一个组件，两块长得一模一样，
    读者不必怀疑「差别是不是渲染方式造成的」。 */
function RuleBlock({
  item,
  heading,
  headingNote,
  categoryName,
  claim,
  requestedScore,
  tone = 'primary',
}: {
  item: SchemeItem
  heading: string
  headingNote?: ReactNode
  categoryName?: string
  /* 不传表示这一块不对应任何一条申报（「现行方案」那一块就是这样）：
     学生没有在这份规则下选过档，也就没有「学生自报」可言。 */
  claim?: Claim | null
  requestedScore?: number | null
  /** secondary 给「现行方案」那一块：淡一档，不跟据以定分的那份抢注意力。 */
  tone?: 'primary' | 'secondary'
}) {
  const rule = item.scoreRule
  const { lo, hi } = scoreBounds(rule)
  const guide = itemGuideMarkdown(item)
  const picked = claim?.option
  const enumRule = rule.type === 'enum' ? rule : null
  const hit = enumRule?.options.find((option) => option.label === picked)
  /* 学生可以按实际情况改掉档位带入的建议分（见 CONTEXT.md「档位建议分」）。
     改过的那条必须显眼，否则审核人默认按档给分，就把学生的自我下调抹掉了。 */
  const adjusted = hit && requestedScore != null && requestedScore !== hit.score ? hit.score : null
  const secondary = tone === 'secondary'
  const showClaim = claim !== undefined

  return (
    <div style={{ border: `1px solid var(${secondary ? '--line2' : '--line'})` }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap', background: 'var(--sub)', borderBottom: '1px solid var(--line)', padding: '13px 16px' }}>
        <Label>{heading}</Label>
        {headingNote}
        {categoryName && <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>{categoryName}</span>}
      </div>

      <div style={{ padding: '16px 16px 4px', display: 'flex', alignItems: 'baseline', gap: 14, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 16.5, fontWeight: 600, letterSpacing: '-.025em', minWidth: 0, textWrap: 'pretty' }}>{item.name}</span>
        <span style={{ marginLeft: 'auto', flex: 'none', fontSize: 12.5, color: 'var(--fg3)' }}>
          {hi === Infinity
            ? <>认定分 <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)', ...num }}>{lo}</span> 分起 · 不封顶</>
            : <>认定区间 <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)', ...num }}>{lo} — {hi}</span> 分</>}
        </span>
      </div>
      <div style={{ padding: '0 16px 14px', fontSize: 12.5, color: 'var(--fg3)' }}>{ruleKind(rule)}</div>

      {/* 学生选了哪一档，先说清楚。这一条是审核人进这一页要回答的第一个问题。 */}
      {enumRule && picked && (
        <div style={{ borderTop: '1px solid var(--line)', background: hit ? 'var(--sub)' : 'var(--redBg)', padding: '13px 16px', display: 'flex', alignItems: 'baseline', gap: 12, flexWrap: 'wrap' }}>
          <Label>学生选档</Label>
          <span style={{ fontSize: 14.5, fontWeight: 600, minWidth: 0, textWrap: 'pretty' }}>{hit ? enumOptionPath(hit).join(' / ') : picked}</span>
          <span style={{ marginLeft: 'auto', flex: 'none', fontSize: 12.5, color: 'var(--fg3)' }}>
            {hit ? <>档位建议 <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--fg)', ...num }}>{f.score(hit.score)}</span> 分</> : '该档次不在这份规则里'}
          </span>
        </div>
      )}
      {adjusted !== null && (
        <div style={{ borderTop: '1px solid var(--line)', background: 'var(--warnBg)', padding: '11px 16px', fontSize: 13, color: 'var(--fg2)', lineHeight: 1.65 }}>
          学生把这一档的建议分 <b style={num}>{f.score(adjusted)}</b> 改成了 <b style={num}>{f.score(requestedScore)}</b> 分
        </div>
      )}

      <div style={{ borderTop: '1px solid var(--line)', padding: enumRule ? '6px 0 4px' : '0 16px 8px' }}>
        {rule.type === 'enum' ? (
          wantsMatrix(rule)
            ? <div style={{ padding: '8px 12px 4px' }}><TierMatrix rule={rule} picked={picked} /></div>
            : <TierList rule={rule} picked={picked} />
        ) : rule.type === 'per_unit' ? (
          <>
            <Fact label="计分方式" value={`每 ${rule.unit} ${f.score(rule.per)} 分`} />
            <Fact label="小项封顶" value={rule.cap == null ? '不封顶' : `${f.score(rule.cap)} 分`} />
            {showClaim && <Fact label="学生自报" value={claim?.quantity == null ? '未填，待你认定' : `${claim.quantity} ${rule.unit}`} tone={claim?.quantity == null ? 'var(--warn)' : undefined} />}
          </>
        ) : rule.type === 'threshold' ? (
          <>
            <Fact label="达标线" value={`至少 ${rule.minimum} ${rule.unit}`} />
            <Fact label="达标得分" value={`${f.score(rule.award)} 分`} />
            <Fact label="未达标" value="0 分" />
            {showClaim && <Fact label="学生自报" value={claim?.quantity == null ? '未填，待你认定' : `${claim.quantity} ${rule.unit}`} tone={claim?.quantity == null ? 'var(--warn)' : undefined} />}
          </>
        ) : (
          <>
            <Fact label="认定区间" value={`${f.score(rule.min)} — ${f.score(rule.max)} 分`} />
            <Fact label="定分方式" value="依据佐证由审核人认定" />
            {showClaim && <Fact label="学生自报" value={claim?.score == null ? '未填，待你认定' : `${f.score(claim.score)} 分`} tone={claim?.score == null ? 'var(--warn)' : undefined} />}
          </>
        )}
      </div>

      {guide && (
        <div style={{ borderTop: '1px solid var(--line)', padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 9 }}>
          <Label>认定要点</Label>
          <RichText value={guide} />
        </div>
      )}

      {/* 审核人也要这张名单：核对「他报的这场活动在不在本学年名单里、口径对不对」，
          比凭印象判断可靠。和学生看到的是同一份。 */}
      {item.activities?.length ? (
        <div style={{ borderTop: '1px solid var(--line)', padding: '14px 16px' }}>
          <ActivityTable activities={item.activities} />
        </div>
      ) : null}
    </div>
  )
}

export function RuleCard({
  item,
  categoryName,
  capturedAt,
  claim,
  requestedScore,
  currentItem,
}: {
  item: SchemeItem
  categoryName?: string
  /* 现行方案里同一个 key 的小项。传了就和冻结版比一次，不一致时把现行那份
     原样再渲染成第二块。没有现行方案（快照回放、离线查看）时不传，第二块
     整个不出现。 */
  currentItem?: SchemeItem | null
  /* 这一条申报冻结规则的时刻。原来这里印的是方案版本号（v7），但读这张卡的人
     ——学生看申诉、审核人看认定——要确认的是「这条按哪一版规则算」，
     一个时间点回答得了，一个序号回答不了。 */
  capturedAt?: string
  /** 学生这一条填了什么。用来在规则里标出他选的那一档。 */
  claim?: Claim | null
  /** 学生期望分。与档位建议分不一致时单独提醒——那正是审核人要核的地方。 */
  requestedScore?: number | null
}) {
  /* currentItem 为 undefined 表示调用方没有现行方案可比，不是「没有差异」——
     两者要分开，否则快照回放页会假装规则从没变过。 */
  const drift = currentItem === undefined ? null : diffRule(item, currentItem)
  const drifted = !!drift && hasRuleDrift(drift)

  const frozen = (
    <RuleBlock
      item={item}
      heading="适用规则"
      headingNote={capturedAt ? <span style={{ fontSize: 12, color: 'var(--fg3)' }}>提交时冻结 · {f.date(capturedAt)}</span> : undefined}
      categoryName={categoryName}
      claim={claim}
      requestedScore={requestedScore}
    />
  )

  if (!drifted) return frozen

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      {/* 只陈述事实，不给处置建议。哪一份算数、要不要升给班管，实际情况比这
          复杂得多，是审核人的判断——系统摆出两份内容就够了。 */}
      <div style={{ border: '1px solid var(--warn)', background: 'var(--warnBg)', padding: '12px 16px', fontSize: 13, color: 'var(--fg2)', lineHeight: 1.75 }}>
        <b style={{ color: 'var(--fg)' }}>这一条提交之后，方案改过。</b>
        {drift?.removed
          ? '这个小项已经不在现在的方案里了。下面这份是你提交时存下来的。'
          : '下面把你提交时存下的规则和现在的方案并排列出来。'}
      </div>
      {frozen}
      {currentItem && <RuleBlock item={currentItem} heading="现行方案" tone="secondary" />}
    </div>
  )
}
