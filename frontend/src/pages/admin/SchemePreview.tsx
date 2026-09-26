/* 方案编辑器 · 学生端预览（原型 admScheme 的第三态）。

   最左边与学生提交页共用同一棵方案树；右侧是学生表单真实渲染和"结算器怎么算这一条"。
   在这里选择和填写的内容不会落库，只用于验证配置。

   表单的算分走 lib/claim 的 expected()，与学生提交页是同一份实现——
   预览的意义就是"学生会看到什么、算出多少"，两边各写一份就成了骗自己。

   推导顺序照抄后端 internal/scheme/score.go 的 ComputeCategoryScore：
   小项封顶 → 互斥组取最高 → 共享封顶组 → 基础分/扣分 → 大项封顶。
   互斥组与共享封顶组是跨条目的，单条算不出来，所以只列出规则而不给数。 */

import { useState } from 'react'
import { Upload } from 'lucide-react'
import { Btn, Empty, Field, Note, Pill, RequiredMark, Sub } from '@/components/ui'
import { EnumOptionPicker } from '@/components/EnumOptionPicker'
import { TierMatrix } from '@/components/TierMatrix'
import { wantsMatrix } from '@/lib/tierMatrix'
import { ActivityTable } from '@/components/ActivityTable'
import { CategoryBrief } from '@/components/CategoryBrief'
import { FileDropzone } from '@/components/FileDropzone'
import { ItemGuide } from '@/components/ItemGuide'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { RuleSheet } from '@/components/RuleSheet'
import { SchemeTree } from '@/components/SchemeTree'
import { categoryBriefKey, claimableItems, findCategoryBrief, findReadOnlySchemeItem } from '@/lib/schemeTree'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { evidenceHint, expected, scoreBounds } from '@/lib/claim'
import type { SchemeCategory, SchemeConfig, SchemeItem } from '@/lib/types'

interface Step {
  text: string
  val: string
  tone?: string
}

function steps(cat: SchemeCategory, item: SchemeItem, qty: string, level: number, free: string): Step[] {
  const rule = item.scoreRule
  const e = expected(rule, qty, level, free)
  const out: Step[] = []

  if (rule.type === 'per_unit') {
    out.push({
      text: e.raw == null ? `学生没填数量 → 由审核人照佐证来定` : `${qty.trim()} ${rule.unit} × ${rule.per} 分/${rule.unit}`,
      val: e.raw == null ? '待定' : e.raw.toFixed(2),
      tone: e.raw == null ? 'var(--fg3)' : undefined,
    })
    out.push({
      text: rule.cap == null ? '本小项不设封顶' : `小项封顶 ${rule.cap} 分 · ${e.capped ? '已截断' : '未触及'}`,
      val: e.final == null ? '待定' : e.final.toFixed(2),
      tone: e.capped ? 'var(--warn)' : undefined,
    })
  } else if (rule.type === 'enum') {
    const o = rule.options[level]
    out.push({ text: `选择档位「${o?.label ?? '—'}」并带入建议分`, val: (o?.score ?? 0).toFixed(2) })
    out.push({
      text: e.capped
        ? `填的 ${e.raw} 超出 0 – ${scoreBounds(rule).hi} 的允许范围，已夹取`
        : '学生可以按实际情况改期望分，最后的分还是审核人定',
      val: e.final == null ? '待填写' : e.final.toFixed(2),
      tone: e.capped ? 'var(--warn)' : 'var(--fg3)',
    })
  } else if (rule.type === 'threshold') {
    const quantity = Number(qty)
    out.push({
      text: !qty.trim() ? `学生尚未填写${rule.unit}数` : `${qty} ${rule.unit} ${quantity >= rule.minimum ? '已达标' : '未达标'}（最低 ${rule.minimum} ${rule.unit}）`,
      val: e.final == null ? '待填写' : e.final.toFixed(2),
      tone: e.final == null || quantity < rule.minimum ? 'var(--fg3)' : undefined,
    })
    out.push({ text: `达标获得基础分 ${rule.award} 分，未达标为 0 分`, val: e.final == null ? '待填写' : e.final.toFixed(2) })
  } else {
    out.push({
      text: e.raw == null ? '学生未自报 → 审核人完全自定' : `学生自报 ${free.trim()} 分，审核人可改`,
      val: e.raw == null ? '待定' : e.raw.toFixed(2),
      tone: e.raw == null ? 'var(--fg3)' : undefined,
    })
    out.push({
      text: `区间 ${rule.min} – ${rule.max}${e.capped ? ' · 已夹取' : ''}`,
      val: e.final == null ? '待定' : e.final.toFixed(2),
      tone: e.capped ? 'var(--warn)' : undefined,
    })
  }

  if (item.exclusiveGroup) {
    out.push({ text: `互斥组「${item.exclusiveGroup}」· 同组多条只算最高的一条`, val: '跨条目', tone: 'var(--fg3)' })
  }
  if (item.capGroup) {
    out.push({ text: `共享封顶组「${item.capGroup.key}」· 组内合计再封 ${item.capGroup.cap} 分`, val: '跨条目', tone: 'var(--fg3)' })
  }
  out.push({ text: `最后与基础分、扣分合并，整个「${cat.name}」封顶 ${cat.maxTotal} 分`, val: '跨条目', tone: 'var(--fg3)' })
  return out
}

/** 预览里试填的那一条。挂在小项 key 上，页面级持有，切页签回来不会被清空。 */
export interface PreviewTrial {
  key: string
  title?: string
  qty: string
  level: number
  free: string
  md?: string
}

const emptyTrial: PreviewTrial = { key: '', title: '', qty: '', level: 0, free: '', md: '' }

export default function SchemePreview({ config, focusKey, onFocus, trial, onTrial }: {
  config: SchemeConfig
  focusKey: string | null
  onFocus: (itemKey: string) => void
  trial: PreviewTrial | null
  onTrial: (trial: PreviewTrial) => void
}) {
  const [rulesOpen, setRulesOpen] = useState(false)

  /* 可申报项、教务导入大项说明与只读项必须和学生提交页使用同一棵树 */
  const options = config.categories.flatMap((c) => claimableItems(c).map((item) => ({ cat: c, item })))
  const treeKeys = config.categories.flatMap((c) => [
    categoryBriefKey(c.key),
    ...claimableItems(c).map((item) => item.key),
    ...c.baseItems.map((item) => item.key),
    ...c.penaltyItems.map((item) => item.key),
  ])

  /* 选中的小项与 GUI 编辑器共享：两个页签是同一个小项的两种视角，切过来不该从头开始。 */
  const currentKey = focusKey && treeKeys.includes(focusKey) ? focusKey : treeKeys[0] ?? ''
  const sel = options.find((o) => o.item.key === currentKey) ?? null
  const readOnlyItem = !sel ? findReadOnlySchemeItem(config, currentKey) : null
  const briefCategory = !sel && !readOnlyItem ? findCategoryBrief(config, currentKey) : null

  /* 试填值只在 key 对得上时才认：换了小项自然回到空白，不会拿着上一条的数量看下一条的分。 */
  const { title = '', qty, level, free, md = '' } = trial?.key === currentKey ? trial : emptyTrial
  const fill = (change: Partial<PreviewTrial>) => onTrial({ title, qty, level, free, md, ...change, key: currentKey })

  const rule = sel?.item.scoreRule
  const e = rule ? expected(rule, qty, level, free) : null
  const undecided = e?.final == null
  const enumMax = rule?.type === 'enum' ? scoreBounds(rule).hi : null

  return (
    <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '252px minmax(0,1fr)', gap: 0, borderTop: '1px solid var(--line)', marginTop: 16 }}>
      <SchemeTree config={config} selectedKey={currentKey || null} onSelect={onFocus} />

      <div style={{ minWidth: 0, padding: '22px 0 22px 30px' }}>
        {!currentKey ? (
          <Empty title="这份方案没有小项" desc="学生那边不会出现可以选的方案树节点。" />
        ) : briefCategory ? (
          <CategoryBrief category={briefCategory} weight={config.weights[briefCategory.key]} />
        ) : readOnlyItem ? (
          <div style={{ border: '1px solid var(--line)', display: 'flex', flexDirection: 'column' }}>
            <ItemGuide
              name={readOnlyItem.name}
              item={null}
              readOnlyHint="这个小项由审核人照班级记录录，学生只能看。"
            />
            <div style={{ padding: '24px 22px', display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', color: 'var(--fg2)' }}>
                  <path d="M6 11V8a6 6 0 0 1 12 0v3" />
                  <path d="M5 11h14v9H5z" />
                </svg>
                <span style={{ fontSize: 14.5, fontWeight: 600, letterSpacing: '-.02em' }}>这一项没有提交入口</span>
              </div>
              <div style={{ fontSize: 13, color: 'var(--fg2)', lineHeight: 1.85, maxWidth: 600, textWrap: 'pretty' }}>
                {readOnlyItem.kind === 'penalty'
                  ? '扣分是负分，由审核人照通报和学院文件录，学生不用报。'
                  : '基础项默认满分、只扣不加，由审核人照班级出勤册和记录录，学生不用报。'}
                每一笔的依据和得分都能在「综测总览」里看到，哪一笔有意见都可以申诉
              </div>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 14, borderTop: '1px solid var(--line2)', paddingTop: 16 }}>
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>录入方</span>
                <span style={{ marginLeft: 'auto', textAlign: 'right', fontSize: 13, color: 'var(--fg2)' }}>审核人（两名，他们知道是你，你不知道是他们）</span>
              </div>
            </div>
          </div>
        ) : sel && rule && e ? (
          <>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, paddingBottom: 12, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>学生端 · 提交表单</span>
              <Pill tone={sel.item.claimableBase ? 'bad' : 'idle'}>
                {sel.item.claimableBase ? '条件基础分' : rule.type === 'per_unit' ? '按量' : rule.type === 'enum' ? '按档' : rule.type === 'threshold' ? '条件申报' : '自报'}
              </Pill>
              <span style={{ marginLeft: 'auto', ...mono('11px', '.02em') }}>{sel.cat.key} / {sel.item.key}</span>
            </div>

            <div data-r="split" style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) 320px', gap: 30, alignItems: 'start' }}>
              <div style={{ border: '1px solid var(--line)', background: 'var(--bg)', display: 'flex', flexDirection: 'column', minWidth: 0 }}>
                <ItemGuide
                  name={sel.item.name}
                  item={sel.item}
                  extra={
                    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 5, flex: 'none' }}>
                      <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>本小项已交</span>
                      <span style={{ fontSize: 17, fontWeight: 600, ...num }}>0 条（预览）</span>
                    </div>
                  }
                />
                <div style={{ padding: '24px 22px', display: 'flex', flexDirection: 'column', gap: 24 }}>
                  {/* 本学年归到这一项下的活动：真实还原学生端渲染 */}
                  {sel.item.activities?.length ? <ActivityTable activities={sel.item.activities} defaultOpen={true} /> : null}

                  {/* 事项名称与主输入区 */}
                  <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr', gap: 24 }}>
                    <Field label="事项名称" required hint="主办方和时间，方便核对。">
                      <input
                        value={title}
                        onChange={(e) => fill({ title: e.target.value })}
                        placeholder={rule.type === 'enum' ? '如实填写' : '如实填写'}
                        style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px' }}
                      />
                    </Field>

                    {rule.type === 'per_unit' && (
                      <Field label={`${rule.unit}数 · 可留空`} hint={`每${rule.unit} ${rule.per} 分${rule.cap != null ? `，小项上限 ${rule.cap} 分` : '，不封顶'}；留空则由审核人根据佐证定分`}>
                        <input
                          type="number"
                          min={0}
                          step="any"
                          value={qty}
                          onChange={(e) => fill({ qty: e.target.value })}
                          inputMode="decimal"
                          placeholder="留空则由审核人根据佐证定分"
                          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px', fontWeight: 600, ...num }}
                        />
                      </Field>
                    )}

                    {rule.type === 'threshold' && (
                      <Field label={`已完成${rule.unit}数`} hint={`至少 ${rule.minimum} ${rule.unit}；达到后获得 ${rule.award} 分，未达到则本项为 0 分`}>
                        <input
                          type="number"
                          min={0}
                          step="any"
                          value={qty}
                          onChange={(e) => fill({ qty: e.target.value })}
                          inputMode="decimal"
                          placeholder={`请输入已完成的${rule.unit}数`}
                          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px', fontWeight: 600, ...num }}
                        />
                      </Field>
                    )}

                    {rule.type === 'free' && (
                      <Field label="期望加分 · 可留空" hint={`允许范围 ${rule.min} — ${rule.max} 分；留空则由审核人根据佐证定分`}>
                        <input
                          type="number"
                          min={rule.min}
                          max={rule.max}
                          step="0.01"
                          value={free}
                          onChange={(e) => fill({ free: e.target.value })}
                          inputMode="decimal"
                          placeholder="留空则由审核人根据佐证定分"
                          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px', fontWeight: 600, ...num }}
                        />
                      </Field>
                    )}
                  </div>

                  {rule.type === 'enum' && (
                    <>
                      {/* 档位总表：真实对齐学生端 */}
                      {wantsMatrix(rule) && (
                        <div>
                          <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 10 }}>
                            档位总表
                          </div>
                          <TierMatrix rule={rule} picked={rule.options[level]?.label} />
                        </div>
                      )}
                      <EnumOptionPicker
                        rule={rule}
                        value={level}
                        onChange={(next) => fill({ level: next, free: String(rule.options[next]?.score ?? '') })}
                      />
                      <Field
                        label="期望加分 · 可调整"
                        hint={`你可以按实际情况在 0 — ${enumMax} 分内修改，最终由审核人认定`}
                      >
                        <input
                          type="number"
                          min={0}
                          max={enumMax ?? undefined}
                          step="0.01"
                          value={free}
                          onChange={(e) => fill({ free: e.target.value })}
                          inputMode="decimal"
                          placeholder={`建议 ${rule.options[level]?.score ?? 0} 分`}
                          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px', fontWeight: 600, ...num }}
                        />
                      </Field>
                    </>
                  )}

                  {/* 佐证材料投放区：真实对齐 FileDropzone 渲染 */}
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>
                      佐证材料
                      {sel.item.evidence?.required && <RequiredMark />}
                    </span>
                    <FileDropzone
                      active={false}
                      disabled
                      icon={<Upload size={24} strokeWidth={1.4} />}
                      title="将文件或文件夹拖至此区域"
                      hint="或者点下面的按钮上传（预览模式下不会真的传）"
                      actions={<><Btn disabled>选择文件</Btn><Btn disabled>选择文件夹</Btn></>}
                    />
                    <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6, textWrap: 'pretty' }}>
                      {evidenceHint(sel.item)} · 预览模式下佐证上传已禁用
                    </span>
                  </div>

                  {/* 补充说明 Markdown 编辑器 */}
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    <Sub title="补充说明" />
                    <MarkdownEditor
                      value={md}
                      onChange={(val) => fill({ md: val })}
                      minHeight={140}
                      placeholder="补充一下怎么参与的、持续多久、谁能证明（预览里可以随便填）"
                    />
                  </div>

                  {/* 期望加分预览栏 */}
                  <Note tone={e.capped ? 'warn' : undefined}>
                    <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
                      <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>期望加分预览</span>
                      <span style={{ fontSize: 20, fontWeight: 600, color: 'var(--fg)', ...num }}>
                        {undecided ? '待审核人定分' : f.score(e.final!)}
                      </span>
                      {e.capped && <span style={{ fontSize: 12.5 }}>已按允许范围计入</span>}
                      <span style={{ ...mono('11px', '.02em'), marginLeft: 'auto' }}>{f.schemeStamp(config)}</span>
                    </div>
                  </Note>

                  {/* 底部按钮栏模拟 */}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                    <Btn primary disabled title="预览模式不可提交">提交并进入审核</Btn>
                    <Btn disabled title="预览模式不可存草稿">存为草稿</Btn>
                    <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>
                      学生端预览 · 随便填的数据只在本地算一算、对对规则，不会存
                    </span>
                  </div>
                </div>
              </div>

              {/* 计分推导步骤面板 */}
              <div style={{ minWidth: 0, background: 'var(--sub)', border: '1px solid var(--line)', padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 10 }}>
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>计分推导过程</span>
                {steps(sel.cat, sel.item, qty, level, free).map((st, i) => (
                  <div key={i} style={{ display: 'flex', alignItems: 'baseline', gap: 12, borderTop: '1px solid var(--line2)', paddingTop: 10 }}>
                    <span style={{ ...mono('11px', '0'), flex: 'none' }}>{i + 1}</span>
                    <span style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.65, minWidth: 0, textWrap: 'pretty' }}>{st.text}</span>
                    <span style={{ marginLeft: 'auto', ...mono('11.5px', '0'), color: st.tone ?? 'var(--fg2)', flex: 'none' }}>{st.val}</span>
                  </div>
                ))}
                <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.7, borderTop: '1px solid var(--line2)', paddingTop: 10, textWrap: 'pretty' }}>
                  跨条目的规则要到汇总时才算，这里只列出哪些规则会生效。
                </span>
              </div>
            </div>

            {/* 规则速查 */}
            <div style={{ borderTop: '1px solid var(--line)', marginTop: 26, paddingTop: 4 }}>
              <button
                type="button"
                className="hv-fg"
                aria-expanded={rulesOpen}
                onClick={() => setRulesOpen((v) => !v)}
                style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', border: 0, background: 'none', padding: '16px 0 6px', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
              >
                <span style={{ width: 12, color: 'var(--fg3)' }}>{rulesOpen ? '▾' : '▸'}</span>
                <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em' }}>规则速查</span>
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>照当前方案的原文列出来，加分以它为准 · {f.schemeStamp(config)}</span>
              </button>
              {rulesOpen && (
                <div style={{ paddingBottom: 24 }}>
                  <RuleSheet config={config} heading={false} />
                </div>
              )}
            </div>
          </>
        ) : null}
      </div>
    </div>
  )
}
