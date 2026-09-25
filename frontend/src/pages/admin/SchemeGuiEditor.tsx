import { useEffect, useRef, useState } from 'react'
import { Btn, Pill } from '@/components/ui'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { fieldStyle, mono } from '@/lib/style'
import type { SchemeActivity, SchemeCategory, SchemeConfig, SchemeItem, ScoreRule } from '@/lib/types'
import { COMMON_EVIDENCE_TYPES, DEFAULT_EVIDENCE_MAX_MB } from '@/lib/evidence'
import { parseActivitiesText } from '@/lib/activities'
import '@/styles/mobile-pages.css'

const defaultEvidence = () => ({ required: true, types: [...COMMON_EVIDENCE_TYPES], maxMb: DEFAULT_EVIDENCE_MAX_MB })

type ItemList = 'items' | 'baseItems' | 'penaltyItems'
const itemLists: readonly ItemList[] = ['items', 'baseItems', 'penaltyItems']
type Selection =
  | { kind: 'scheme' }
  | { kind: 'category'; categoryIndex: number }
  | { kind: 'item'; categoryIndex: number; list: ItemList; itemIndex: number }

const boxInput = { ...fieldStyle, background: 'var(--bg)', border: '1px solid var(--line)', padding: '9px 11px' }

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0 }}>
      <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>{label}</span>
      {children}
      {hint && <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.55 }}>{hint}</span>}
    </label>
  )
}

function NoteMarkdownField({ label, hint, value, onChange, placeholder }: {
  label: string
  hint: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0 }}>
      <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>{label}</span>
      <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.55 }}>{hint}</span>
      <MarkdownEditor value={value} onChange={onChange} minHeight={140} placeholder={placeholder} />
    </div>
  )
}

/** 把 "甲 / 乙" 这行文本切成模型里的数组。空段丢掉，两头空格也去掉。 */
const splitPath = (text: string) => text.split('/').map((part) => part.trim()).filter(Boolean)

/** 本大项里已有的共享封顶组。共享组只在大项内部汇总——ComputeCategoryScore 是按大项调的，
    同一个 key 写到两个大项里不会合并成一个池，而是各封各的，所以这里只在本大项内找。 */
function capGroupsIn(category: SchemeCategory) {
  const groups = new Map<string, { key: string; cap: number; members: string[] }>()
  for (const item of category.items) {
    if (!item.capGroup) continue
    const found = groups.get(item.capGroup.key)
    if (found) found.members.push(item.name)
    else groups.set(item.capGroup.key, { key: item.capGroup.key, cap: item.capGroup.cap, members: [item.name] })
  }
  return [...groups.values()]
}

/* 共享封顶组的选择器。

   原来这里是一个自由文本的「共享组 key」加一个「共享总上限」，两样都按小项各填一遍：
   界面上看不出这个组里还有谁，也不知道组内已经堆了多少分，
   上限还得在每个成员上填得一模一样，填岔了要等发布时才被后端拒绝。
   组是组、小项是小项——这里把组当成一等公民来选。 */
function CapGroupPicker({ category, itemKey, value, onPick, onCap }: {
  category: SchemeCategory
  itemKey: string
  value: { key: string; cap: number }
  onPick: (key: string, cap: number) => void
  onCap: (cap: number) => void
}) {
  const groups = capGroupsIn(category)
  const current = groups.find((g) => g.key === value.key)
  const isNew = !current || current.members.length === 0
  const others = (current?.members ?? []).filter((name) => name !== category.items.find((entry) => entry.key === itemKey)?.name)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, border: '1px solid var(--line2)', padding: '12px 14px', background: 'var(--sub)' }}>
      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        <Field label="共享组" hint="只在这个大项里共享。换组就跟那组共用上限">
          <select
            value={groups.some((g) => g.key === value.key) ? value.key : '__new__'}
            onChange={(event) => {
              if (event.target.value === '__new__') {
                onPick(nextGroupKey(category), value.cap)
                return
              }
              const picked = groups.find((g) => g.key === event.target.value)!
              onPick(picked.key, picked.cap)
            }}
            style={boxInput}
          >
            {groups.map((g) => (
              <option key={g.key} value={g.key}>{g.key}（{g.cap} 分 · {g.members.length} 个小项）</option>
            ))}
            <option value="__new__">＋ 新建一个组</option>
          </select>
        </Field>
        <NumberField label="共享总上限" value={value.cap} min={0} step={0.5} hint="会同步给同组全部小项" onChange={onCap} />
      </div>
      <div style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6 }}>
        {isNew || others.length === 0
          ? '组里只有这一个小项。把别的也设成同一组，它们会先加起来再一起卡上限。'
          : `组内还有：${others.join('、')} —— 这些小项与本项合计不超过 ${value.cap} 分。`}
      </div>
    </div>
  )
}

/** 新组 key 取一个本大项内没用过的。 */
function nextGroupKey(category: SchemeCategory) {
  const used = new Set(capGroupsIn(category).map((g) => g.key))
  for (let i = 1; ; i++) {
    const key = `${category.key}_cap${i > 1 ? i : ''}`
    if (!used.has(key)) return key
  }
}

/** 留空时的层级路径。与 lib/schemeTree.ts 的 enumOptionPath 同一口径：按「·」拆，拆不出就整名一级。 */
const autoPath = (label: string) => {
  const parts = splitPath(label.split('·').join('/'))
  return parts.length ? parts : [label]
}

function TinyBtn({ children, onClick, title }: { children: React.ReactNode; onClick: () => void; title?: string }) {
  return (
    <button
      type="button"
      className="hv-fg"
      onClick={onClick}
      title={title}
      style={{ flex: 'none', background: 'none', border: 0, padding: 0, margin: 0, font: 'inherit', fontSize: 11.5, color: 'var(--fg3)', cursor: 'pointer', whiteSpace: 'nowrap' }}
    >
      {children}
    </button>
  )
}

/* 「用 / 分隔」的输入框。

   模型里存的是字符串数组，框里是一行文本。原来直接拿 `array.join(' / ')` 当 value、
   每次 onChange 都 split + trim + filter(Boolean) 再写回——于是这个框永远打不出斜杠：
   敲下 "/" 的那一刻尾部多出一个空段，filter(Boolean) 把它丢掉，
   下一次渲染 join 回来的字符串里没有斜杠，刚敲的那一横当场消失。
   空格同理："甲 / 乙" 中间的空格会被 trim 掉再由 join 补回，光标跟着乱跳。

   受控值只要是「自身的有损解析结果」，就一定会吃掉处于中间态的输入。
   所以框里那行文本自己存一份，只把解析结果同步给模型。 */
function PathInput({ value, onChange, placeholder, ariaLabel }: {
  value: string[] | undefined
  onChange: (parts: string[]) => void
  placeholder?: string
  ariaLabel?: string
}) {
  const external = value?.join(' / ') ?? ''
  const [text, setText] = useState(external)

  /* 只在"外部值确实变了"的时候才覆盖框里的文本：换了小项、上面一行被删掉导致
     这一行换了内容、撤销，都算外部变。判据是"我这行文本解析出来的结果"跟外部对不上——
     自己正敲到一半的 "甲 /" 解析出来还是 ["甲"]，与外部一致，就别动它。
     不这样判而直接跟随 external，删掉上面一行之后这个框会继续显示上一行的旧文本。 */
  const textRef = useRef(text)
  textRef.current = text
  useEffect(() => {
    if (splitPath(textRef.current).join(' / ') !== external) setText(external)
  }, [external])

  return (
    <input
      value={text}
      aria-label={ariaLabel}
      placeholder={placeholder}
      onChange={(event) => {
        setText(event.target.value)
        onChange(splitPath(event.target.value))
      }}
      /* 离开时把 "甲 / " 这种尾巴收干净，显示的和存下去的保持一致。 */
      onBlur={() => setText(external)}
      style={boxInput}
    />
  )
}

/* step 允许传 'any'：HTML 的步进网格以 min 为基准，min=0.01 配 step=1 会把合法值变成
   0.01 / 1.01 / 2.01，箭头一点就跳出 1.01 这种数。数量类字段要的是"必须为正、但不限小数"，
   只能靠 step='any' 把网格摘掉——此时浏览器的上下箭头仍按 1 递增，min 也照常拦住 0。 */
function NumberField({ label, value, onChange, step = 1, min, max, hint }: { label: string; value: number; onChange: (value: number) => void; step?: number | 'any'; min?: number; max?: number; hint?: string }) {
  return (
    <Field label={label} hint={hint}>
      <input
        type="number"
        value={value}
        min={min}
        max={max}
        step={step}
        onChange={(event) => onChange(Number.isFinite(event.target.valueAsNumber) ? event.target.valueAsNumber : 0)}
        style={boxInput}
      />
    </Field>
  )
}

function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label style={{ display: 'flex', alignItems: 'center', gap: 9, fontSize: 12.5, color: 'var(--fg2)', cursor: 'pointer' }}>
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} />
      {label}
    </label>
  )
}

function listLabel(list: ItemList) {
  return list === 'items' ? '学生申报项' : list === 'baseItems' ? '基础项' : '扣分项'
}

function ruleTypeLabel(rule: ScoreRule) {
  return rule.type === 'enum' ? '按档' : rule.type === 'per_unit' ? '按量' : rule.type === 'threshold' ? '条件' : '自报'
}

function enumOptionCount(item: SchemeItem) {
  return item.scoreRule.type === 'enum' ? item.scoreRule.options.length : 0
}


function ActivitiesEditor({
  activities,
  onChange,
}: {
  activities?: SchemeActivity[]
  onChange: (activities?: SchemeActivity[]) => void
}) {
  const [batchOpen, setBatchOpen] = useState(false)
  const [batchText, setBatchText] = useState('')
  const [batchError, setBatchError] = useState<string | null>(null)

  const list = activities ?? []

  const updateItem = (index: number, patch: Partial<SchemeActivity>) => {
    const next = [...list]
    next[index] = { ...next[index], ...patch }
    for (const key of ['date', 'level', 'org', 'score'] as const) {
      if (next[index][key] !== undefined && !String(next[index][key]).trim()) {
        delete next[index][key]
      }
    }
    onChange(next)
  }

  const addItem = () => {
    onChange([...list, { name: '' }])
  }

  const removeItem = (index: number) => {
    const next = list.filter((_, i) => i !== index)
    onChange(next.length ? next : undefined)
  }

  const moveItem = (index: number, direction: -1 | 1) => {
    const target = index + direction
    if (target < 0 || target >= list.length) return
    const next = [...list]
    const [moved] = next.splice(index, 1)
    next.splice(target, 0, moved)
    onChange(next)
  }

  const handleBatchImport = (mode: 'replace' | 'append') => {
    setBatchError(null)
    const parsed = parseActivitiesText(batchText)
    if (!parsed.length) {
      setBatchError('没读出有效的活动，检查一下格式（每条至少要有活动名称）')
      return
    }
    const next = mode === 'replace' ? parsed : [...list, ...parsed]
    onChange(next.length ? next : undefined)
    setBatchText('')
    setBatchOpen(false)
  }

  return (
    <div style={{ borderTop: '1px solid var(--line)', paddingTop: 16, display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>归属活动清单（学生可见）</span>
        <Pill tone={list.length ? 'idle' : 'warn'}>
          {list.length ? `${list.length} 项活动` : '未配置活动'}
        </Pill>
        <Btn onClick={addItem}>＋添加活动</Btn>
        <Btn onClick={() => { setBatchOpen((v) => !v); setBatchError(null) }}>
          {batchOpen ? '收起批量导入' : '批量导入 / 粘贴'}
        </Btn>
        {list.length > 0 && (
          <Btn danger onClick={() => { if (window.confirm('确定要把这个小项下面的活动全部清空吗？')) onChange(undefined) }}>
            清空活动
          </Btn>
        )}
      </div>

      <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.55 }}>
        这学年算在这个小项下的活动。学生填事项名称时能看到，方便照抄。
      </span>

      {batchOpen && (
        <div style={{ background: 'var(--sub)', border: '1px solid var(--line2)', padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>批量导入活动清单</div>
          <div style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6 }}>
            支持直接粘贴 JSON 数组（如 <code>&quot;activities&quot;: [ ... ]</code>），或从 Excel 复制的制表符行（名称 \t 时间 \t 级别 \t 举办单位 \t 分值说明），也可以纯文本一行一个活动名称。
          </div>
          <textarea
            value={batchText}
            onChange={(e) => setBatchText(e.target.value)}
            placeholder="可以粘 JSON 数组（比如一份活动列表）、从表格复制的行，或者纯文本一行一个"
            style={{ ...fieldStyle, minHeight: 120, width: '100%', fontFamily: 'var(--mono)', fontSize: 11.5, lineHeight: 1.5, padding: '8px 10px', background: 'var(--bg)', border: '1px solid var(--line)' }}
          />
          {batchError && <div style={{ fontSize: 12, color: 'var(--red)' }}>{batchError}</div>}
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <Btn primary onClick={() => handleBatchImport('replace')}>覆盖导入</Btn>
            <Btn onClick={() => handleBatchImport('append')}>追加导入</Btn>
            <Btn onClick={() => { setBatchOpen(false); setBatchError(null) }}>取消</Btn>
          </div>
        </div>
      )}

      {list.length > 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, overflowX: 'auto' }}>
          <div data-r="activity-head" style={{ display: 'grid', gridTemplateColumns: 'minmax(140px, 1.4fr) minmax(80px, 0.7fr) minmax(70px, 0.6fr) minmax(110px, 1fr) minmax(90px, 0.8fr) auto', gap: 8, minWidth: 620 }}>
            <span style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--fg3)' }}>活动名称（必填）</span>
            <span style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--fg3)' }}>时间</span>
            <span style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--fg3)' }}>级别</span>
            <span style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--fg3)' }}>举办/组织单位</span>
            <span style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--fg3)' }}>分值说明</span>
            <span style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--fg3)' }}></span>
          </div>
          {list.map((act, index) => (
            <div key={index} data-r="activity-row" style={{ display: 'grid', gridTemplateColumns: 'minmax(140px, 1.4fr) minmax(80px, 0.7fr) minmax(70px, 0.6fr) minmax(110px, 1fr) minmax(90px, 0.8fr) auto', gap: 8, alignItems: 'center', minWidth: 620 }}>
              <label className="activity-field">
                <span className="activity-field-label">活动名称（必填）</span>
                <input
                  aria-label={`活动 ${index + 1} 名称`}
                  placeholder="活动名称（必填）"
                  value={act.name}
                  onChange={(e) => updateItem(index, { name: e.target.value })}
                  style={boxInput}
                />
              </label>
              <label className="activity-field">
                <span className="activity-field-label">时间</span>
                <input
                  aria-label={`活动 ${index + 1} 时间`}
                  placeholder="如 2025.11"
                  value={act.date ?? ''}
                  onChange={(e) => updateItem(index, { date: e.target.value })}
                  style={boxInput}
                />
              </label>
              <label className="activity-field">
                <span className="activity-field-label">级别</span>
                <input
                  aria-label={`活动 ${index + 1} 级别`}
                  placeholder="如 校级"
                  value={act.level ?? ''}
                  onChange={(e) => updateItem(index, { level: e.target.value })}
                  style={boxInput}
                />
              </label>
              <label className="activity-field">
                <span className="activity-field-label">举办 / 组织单位</span>
                <input
                  aria-label={`活动 ${index + 1} 举办单位`}
                  placeholder="如 院团委学生会"
                  value={act.org ?? ''}
                  onChange={(e) => updateItem(index, { org: e.target.value })}
                  style={boxInput}
                />
              </label>
              <label className="activity-field">
                <span className="activity-field-label">分值说明</span>
                <input
                  aria-label={`活动 ${index + 1} 分值说明`}
                  placeholder="如 组织者 2 分"
                  value={act.score ?? ''}
                  onChange={(e) => updateItem(index, { score: e.target.value })}
                  style={boxInput}
                />
              </label>
              <div className="activity-edit-actions" style={{ display: 'flex', alignItems: 'center', gap: 6, flex: 'none' }}>
                <TinyBtn title="上移" onClick={() => moveItem(index, -1)}>↑</TinyBtn>
                <TinyBtn title="下移" onClick={() => moveItem(index, 1)}>↓</TinyBtn>
                <Btn danger onClick={() => removeItem(index)}>删除</Btn>
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div style={{ fontSize: 12.5, color: 'var(--fg3)', padding: '12px 14px', background: 'var(--sub)', border: '1px dashed var(--line)' }}>
          还没有活动。用上面的「＋添加活动」或「批量导入 / 粘贴」录入。
        </div>
      )}
    </div>
  )
}

function itemMark(categoryKey: string, list: ItemList, itemKey: string) {
  return `${categoryKey}/${list}/${itemKey}`
}

/* 把跨页签共享的小项 key 还原成本编辑器用的下标选择。
   对外只传 key 是因为下标会被增删改顺序打乱，而 key 才是学生端预览那棵树认得的东西。 */
function itemSelection(config: SchemeConfig, itemKey: string | null): Selection | null {
  if (!itemKey) return null
  for (const [categoryIndex, category] of config.categories.entries()) {
    for (const list of itemLists) {
      const items: { key: string }[] = category[list]
      const itemIndex = items.findIndex((item) => item.key === itemKey)
      if (itemIndex >= 0) return { kind: 'item', categoryIndex, list, itemIndex }
    }
  }
  return null
}

/* 小项级"未保存"标记：把编辑中的草稿和服务端已保存的那一份逐项比对，新增或改过的小项进这个集合。
   按 key 配对而不是按下标——按下标的话删掉中间一项，后面每一项都会被标成改过。
   代价是纯粹的顺序调整落不到单项上，那部分由顶部整份草稿的"有未保存修改"覆盖。
   两边都由同一份 JSON 往返而来，字段顺序一致，可以直接比字符串。 */
function unsavedItems(config: SchemeConfig, saved: SchemeConfig | null) {
  const marks = new Set<string>()
  if (!saved) return marks
  const before = new Map<string, string>()
  for (const category of saved.categories) {
    for (const list of itemLists) {
      for (const item of category[list]) before.set(itemMark(category.key, list, item.key), JSON.stringify(item))
    }
  }
  for (const category of config.categories) {
    for (const list of itemLists) {
      for (const item of category[list]) {
        const mark = itemMark(category.key, list, item.key)
        if (before.get(mark) !== JSON.stringify(item)) marks.add(mark)
      }
    }
  }
  return marks
}

function nextKey(config: SchemeConfig, prefix: string) {
  const used = new Set(config.categories.flatMap((category) => [...category.items, ...category.baseItems, ...category.penaltyItems].map((item) => item.key)))
  let index = 1
  while (used.has(`${prefix}_${index}`)) index++
  return `${prefix}_${index}`
}

export default function SchemeGuiEditor({ config, savedConfig, focusKey, onFocusKey, onChange }: {
  config: SchemeConfig
  savedConfig: SchemeConfig | null
  focusKey: string | null
  onFocusKey: (itemKey: string) => void
  onChange: (config: SchemeConfig) => void
}) {
  /* 切页签会把这个编辑器整个卸载，所以初始选择从共享的 focusKey 还原；
     没有聚焦小项时直接打开第一个评分大项。班级运行参数由时间线统一管理。 */
  const [selection, setSelection] = useState<Selection>(() => itemSelection(config, focusKey) ?? (config.categories.length ? { kind: 'category', categoryIndex: 0 } : { kind: 'scheme' }))
  const unsaved = unsavedItems(config, savedConfig)

  const selectItem = (categoryIndex: number, list: ItemList, itemIndex: number, itemKey: string) => {
    setSelection({ kind: 'item', categoryIndex, list, itemIndex })
    onFocusKey(itemKey)
  }

  const commit = (change: (next: SchemeConfig) => void) => {
    const next = structuredClone(config)
    change(next)
    onChange(next)
  }

  const category = selection.kind === 'scheme' ? null : config.categories[selection.categoryIndex]
  const selectedItem = selection.kind === 'item' && category ? category[selection.list][selection.itemIndex] : null

  /* 三格公式共用一条写入路径：三格都空了就把整个 formula 删掉，
     免得方案里留一个 {lhs:'',numerator:'',denominator:''} 的空壳过不了校验。 */
  const commitFormula = (value: string, part: 'lhs' | 'numerator' | 'denominator') => {
    if (selection.kind === 'scheme') return
    commit((next) => {
      const target = next.categories[selection.categoryIndex]
      const merged = { ...(target.formula ?? { lhs: '' }), [part]: value }
      if (!merged.lhs?.trim() && !merged.numerator?.trim() && !merged.denominator?.trim()) delete target.formula
      else target.formula = merged
    })
  }

  const addItem = (categoryIndex: number, list: ItemList) => {
    let itemIndex = 0
    let itemKey = ''
    commit((next) => {
      const category = next.categories[categoryIndex]
      if (list === 'items') {
        itemIndex = category.items.length
        itemKey = nextKey(next, `${category.key}_item`)
        category.items.push({
          key: itemKey,
          name: '新申报小项',
          scoreRule: { type: 'enum', options: [{ label: '新档次', score: 0 }] },
          evidence: defaultEvidence(),
        })
      } else if (list === 'baseItems') {
        itemIndex = category.baseItems.length
        itemKey = nextKey(next, `${category.key}_base`)
        category.baseItems.push({ key: itemKey, name: '新基础项', full: 0 })
      } else {
        itemIndex = category.penaltyItems.length
        itemKey = nextKey(next, `${category.key}_penalty`)
        category.penaltyItems.push({ key: itemKey, name: '新扣分项', per: -1 })
      }
    })
    selectItem(categoryIndex, list, itemIndex, itemKey)
  }

  const removeSelected = () => {
    if (selection.kind !== 'item') return
    const { categoryIndex, list, itemIndex } = selection
    commit((next) => {
      next.categories[categoryIndex][list].splice(itemIndex, 1)
    })
    setSelection({ kind: 'category', categoryIndex })
  }

  const moveSelected = (offset: -1 | 1) => {
    if (selection.kind !== 'item') return
    const { categoryIndex, list, itemIndex } = selection
    const target = itemIndex + offset
    const source = config.categories[categoryIndex][list]
    if (target < 0 || target >= source.length) return
    commit((next) => {
      const values = next.categories[categoryIndex][list]
      const [item] = values.splice(itemIndex, 1)
      values.splice(target, 0, item as never)
    })
    setSelection({ ...selection, itemIndex: target })
  }

  const updateClaimable = (change: (item: SchemeItem) => void) => {
    if (selection.kind !== 'item' || selection.list !== 'items') return
    commit((next) => change(next.categories[selection.categoryIndex].items[selection.itemIndex]))
  }

  return (
    <div data-r="scheme-gui" style={{ display: 'grid', gridTemplateColumns: '248px minmax(0,1fr)', border: '1px solid var(--line)', minHeight: 560 }}>
      <nav aria-label="可编辑方案树" style={{ minWidth: 0, borderRight: '1px solid var(--line)', padding: '14px 12px', background: 'var(--sub)', overflowY: 'auto', maxHeight: 'calc(100dvh - 210px)' }}>
        {config.categories.map((cat, categoryIndex) => (
          <div key={`${cat.key}-${categoryIndex}`} style={{ paddingTop: 12 }}>
            <button
              type="button"
              className="hv-fg"
              onClick={() => setSelection({ kind: 'category', categoryIndex })}
              style={{ width: '100%', border: 0, background: selection.kind === 'category' && selection.categoryIndex === categoryIndex ? 'var(--bg)' : 'transparent', padding: '8px 8px', textAlign: 'left', font: 'inherit', cursor: 'pointer', display: 'flex', gap: 8, alignItems: 'baseline' }}
            >
              <span style={{ fontSize: 13, fontWeight: 600, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cat.name || '未命名大项'}</span>
              <span style={{ marginLeft: 'auto', ...mono('10px', '0') }}>{cat.key}</span>
            </button>

            {itemLists.map((list) => (
              <div key={list} style={{ paddingTop: 4 }}>
                <div style={{ padding: '5px 8px 3px 18px', fontSize: 10.5, color: 'var(--fg3)', letterSpacing: '.04em' }}>{listLabel(list)} · {cat[list].length}</div>
                {cat[list].map((item, itemIndex) => {
                  const active = selection.kind === 'item' && selection.categoryIndex === categoryIndex && selection.list === list && selection.itemIndex === itemIndex
                  const dirty = unsaved.has(itemMark(cat.key, list, item.key))
                  return (
                    <button
                      key={`${list}-${item.key}-${itemIndex}`}
                      type="button"
                      className="hv-fg"
                      onClick={() => selectItem(categoryIndex, list, itemIndex, item.key)}
                      style={{ width: '100%', border: 0, borderLeft: `3px solid ${active ? 'var(--red)' : 'transparent'}`, background: active ? 'var(--bg)' : 'transparent', padding: '7px 8px 7px 15px', textAlign: 'left', font: 'inherit', fontSize: 12.5, color: list === 'items' ? 'var(--fg2)' : 'var(--fg3)', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 7 }}
                    >
                      <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.name || '未命名小项'}</span>
                      {dirty && <span title="有未保存修改" style={{ flex: 'none', fontSize: 9, lineHeight: 1, color: 'var(--warn)' }}>●</span>}
                      {'scoreRule' in item && <span style={{ marginLeft: 'auto', fontSize: 10.5, flex: 'none' }}>{ruleTypeLabel(item.scoreRule)}</span>}
                    </button>
                  )
                })}
              </div>
            ))}
          </div>
        ))}
      </nav>

      <div style={{ minWidth: 0, padding: '22px 24px', display: 'flex', flexDirection: 'column', gap: 18 }}>
        {selection.kind === 'scheme' ? (
          <div style={{ color: 'var(--fg3)', fontSize: 13, lineHeight: 1.8 }}>这份方案还没有评分大项。先去高级 JSON 里把评分结构补上。</div>
        ) : selection.kind === 'category' && category ? (
          <>
            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12, flexWrap: 'wrap' }}>
              <div>
                <div style={{ fontSize: 18, fontWeight: 600, letterSpacing: '-.025em' }}>{category.name || '未命名大项'}</div>
                <div style={{ marginTop: 5, ...mono('11px', '.02em') }}>{category.key}</div>
              </div>
              <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                <Btn onClick={() => addItem(selection.categoryIndex, 'items')}>＋申报项</Btn>
                <Btn onClick={() => addItem(selection.categoryIndex, 'baseItems')}>＋基础项</Btn>
                <Btn onClick={() => addItem(selection.categoryIndex, 'penaltyItems')}>＋扣分项</Btn>
              </div>
            </div>
            <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
              <Field label="大项名称">
                <input value={category.name} onChange={(event) => commit((next) => { next.categories[selection.categoryIndex].name = event.target.value })} style={boxInput} />
              </Field>
              <Field label="固定 key" hint="这四个大项的 key 是结算时写死的，在这里改不了">
                <input value={category.key} readOnly style={{ ...boxInput, color: 'var(--fg3)' }} />
              </Field>
              <NumberField label="总分上限" value={category.maxTotal} min={0} step={1} onChange={(value) => commit((next) => { next.categories[selection.categoryIndex].maxTotal = value })} />
              <NumberField label="总评权重" value={config.weights[category.key] ?? 0} min={0} max={1} step={0.01} hint="所有大项权重合计必须为 1" onChange={(value) => commit((next) => { next.weights[category.key] = value })} />
            </div>
            <div style={{ borderTop: '1px solid var(--line)', paddingTop: 14, display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)' }}>
                计分公式（学生可见）
              </div>
              <div style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.7 }}>
                空着就不显示。专业素质这类导入分用。分子分母要么都填，要么都空。
              </div>
              <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12 }}>
                <Field label="等号左边">
                  <input value={category.formula?.lhs ?? ''} placeholder="总平均分值" onChange={(event) => commitFormula(event.target.value, 'lhs')} style={boxInput} />
                </Field>
                <Field label="分子">
                  <input value={category.formula?.numerator ?? ''} placeholder="∑（各门课程的分数 × 各门课程学分）" onChange={(event) => commitFormula(event.target.value, 'numerator')} style={boxInput} />
                </Field>
                <Field label="分母">
                  <input value={category.formula?.denominator ?? ''} placeholder="∑（各门课程学分）" onChange={(event) => commitFormula(event.target.value, 'denominator')} style={boxInput} />
                </Field>
              </div>
            </div>
            <NoteMarkdownField
              label="大项说明（学生可见）"
              hint="整个大项的规矩，显示在公式下面。"
              value={category.note ?? ''}
              placeholder={'- 算哪些课：上学年已修的全部课程\n- 不算哪些课：公共选修课（课程号00开头）除外，不含补考成绩'}
              onChange={(value) => commit((next) => {
                if (value.trim()) next.categories[selection.categoryIndex].note = value
                else delete next.categories[selection.categoryIndex].note
              })}
            />
            <div style={{ borderTop: '1px solid var(--line)', paddingTop: 14, display: 'flex', gap: 14, flexWrap: 'wrap' }}>
              <Pill tone="idle">申报项 {category.items.length}</Pill>
              <Pill tone="ok">基础项 {category.baseItems.length}</Pill>
              <Pill tone="bad">扣分项 {category.penaltyItems.length}</Pill>
            </div>
          </>
        ) : selection.kind === 'item' && category && selectedItem ? (
          <>
            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
                  <span style={{ fontSize: 18, fontWeight: 600, letterSpacing: '-.025em' }}>{selectedItem.name || '未命名小项'}</span>
                  <Pill tone={selection.list === 'items' ? 'idle' : selection.list === 'baseItems' ? 'ok' : 'bad'}>{listLabel(selection.list)}</Pill>
                </div>
                <div style={{ marginTop: 5, ...mono('11px', '.02em') }}>{category.name} / {selectedItem.key}</div>
              </div>
              <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
                <Btn disabled={selection.itemIndex === 0} onClick={() => moveSelected(-1)}>上移</Btn>
                <Btn disabled={selection.itemIndex === category[selection.list].length - 1} onClick={() => moveSelected(1)}>下移</Btn>
                <Btn danger onClick={removeSelected}>删除</Btn>
              </div>
            </div>

            <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
              <Field label="唯一 key" hint="同一份方案内不能重复">
                <input
                  value={selectedItem.key}
                  onChange={(event) => {
                    commit((next) => { next.categories[selection.categoryIndex][selection.list][selection.itemIndex].key = event.target.value })
                    onFocusKey(event.target.value)
                  }}
                  style={boxInput}
                />
              </Field>
              <Field label="显示名称">
                <input value={selectedItem.name} onChange={(event) => commit((next) => { next.categories[selection.categoryIndex][selection.list][selection.itemIndex].name = event.target.value })} style={boxInput} />
              </Field>
            </div>

            {selection.list === 'baseItems' && 'full' in selectedItem && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                <NumberField label="基础项满分" value={selectedItem.full} min={0} step={0.01} onChange={(value) => commit((next) => { next.categories[selection.categoryIndex].baseItems[selection.itemIndex].full = value })} />
                <div style={{ borderTop: '1px solid var(--line)', paddingTop: 16, display: 'flex', flexDirection: 'column', gap: 14 }}>
                  <Toggle
                    label="允许学生申报此基础项"
                    checked={!!selectedItem.studentClaim}
                    onChange={(checked) => commit((next) => {
                      const item = next.categories[selection.categoryIndex].baseItems[selection.itemIndex]
                      if (checked) item.studentClaim = { minimum: 1, unit: '项', evidence: defaultEvidence() }
                      else delete item.studentClaim
                    })}
                  />
                  {selectedItem.studentClaim && (
                    <>
                      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                        <NumberField label="最低申报数量" value={selectedItem.studentClaim.minimum} min={0.01} step="any" onChange={(value) => commit((next) => { const claim = next.categories[selection.categoryIndex].baseItems[selection.itemIndex].studentClaim; if (claim) claim.minimum = value })} />
                        <Field label="计量单位"><input value={selectedItem.studentClaim.unit} onChange={(event) => commit((next) => { const claim = next.categories[selection.categoryIndex].baseItems[selection.itemIndex].studentClaim; if (claim) claim.unit = event.target.value })} style={boxInput} /></Field>
                      </div>
                      <Toggle label="必须上传佐证" checked={!!selectedItem.studentClaim.evidence?.required} onChange={(checked) => commit((next) => {
                        const claim = next.categories[selection.categoryIndex].baseItems[selection.itemIndex].studentClaim
                        if (!claim) return
                        if (!claim.evidence) claim.evidence = { ...defaultEvidence(), required: checked }
                        else claim.evidence.required = checked
                      })} />
                      {selectedItem.studentClaim.evidence && (
                        <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                          <Field label="允许扩展名"><input value={selectedItem.studentClaim.evidence.types.join(',')} onChange={(event) => commit((next) => { const evidence = next.categories[selection.categoryIndex].baseItems[selection.itemIndex].studentClaim?.evidence; if (evidence) evidence.types = event.target.value.split(',').map((value) => value.trim()).filter(Boolean) })} style={boxInput} /></Field>
                          <NumberField label="单文件上限（MB）" value={selectedItem.studentClaim.evidence.maxMb} min={1} onChange={(value) => commit((next) => { const evidence = next.categories[selection.categoryIndex].baseItems[selection.itemIndex].studentClaim?.evidence; if (evidence) evidence.maxMb = value })} />
                        </div>
                      )}
                      <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.7, background: 'var(--sub)', border: '1px solid var(--line2)', padding: '10px 12px' }}>
                        提交页会自动写明：完整达标至少 {selectedItem.studentClaim.minimum} {selectedItem.studentClaim.unit}可得 {selectedItem.full} 分；未完全达标可在 0—{selectedItem.full} 分内自报。
                      </div>
                      <NoteMarkdownField
                        label="补充说明（学生可见）"
                        hint="学生提交时能看到。用 Markdown 分点写。"
                        value={selectedItem.note ?? ''}
                        placeholder={'- 竞赛、论文、专利均可计入\n- 须提供主办方或指导教师证明'}
                        onChange={(value) => commit((next) => {
                          const item = next.categories[selection.categoryIndex].baseItems[selection.itemIndex]
                          if (value.trim()) item.note = value
                          else delete item.note
                        })}
                      />
                    </>
                  )}
                </div>
              </div>
            )}

            {selection.list === 'penaltyItems' && 'per' in selectedItem && (
              <NumberField label="每次扣分" value={selectedItem.per} max={-0.01} step={0.01} hint="扣分必须填写负数" onChange={(value) => commit((next) => { next.categories[selection.categoryIndex].penaltyItems[selection.itemIndex].per = value })} />
            )}

            {selection.list === 'items' && 'scoreRule' in selectedItem && (
              <>
                <div style={{ borderTop: '1px solid var(--line)', paddingTop: 16, display: 'flex', flexDirection: 'column', gap: 14 }}>
                  <Field label="计分规则类型">
                    <select
                      value={selectedItem.scoreRule.type}
                      onChange={(event) => updateClaimable((item) => {
                        const type = event.target.value as ScoreRule['type']
                        item.scoreRule = type === 'enum'
                          ? { type: 'enum', options: [{ label: '新档次', score: 0 }] }
                          : type === 'per_unit'
                            ? { type: 'per_unit', unit: '次', per: 1 }
                            : type === 'threshold'
                              ? { type: 'threshold', unit: '项', minimum: 1, award: 5 }
                              : { type: 'free', min: 0, max: 10 }
                      })}
                      style={boxInput}
                    >
                      <option value="enum">按档：选择档次带入建议分</option>
                      <option value="per_unit">按量：数量 × 单位分</option>
                      <option value="free">自报：在区间内填写期望分</option>
                      <option value="threshold">条件：达到最低数量后得固定分</option>
                    </select>
                  </Field>

                  {selectedItem.scoreRule.type === 'per_unit' && (
                    <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
                      <Field label="计量单位"><input value={selectedItem.scoreRule.unit} onChange={(event) => updateClaimable((item) => { if (item.scoreRule.type === 'per_unit') item.scoreRule.unit = event.target.value })} style={boxInput} /></Field>
                      <NumberField label="每单位分值" value={selectedItem.scoreRule.per} step={0.01} onChange={(value) => updateClaimable((item) => { if (item.scoreRule.type === 'per_unit') item.scoreRule.per = value })} />
                      <NumberField label="小项上限（0 为不设）" value={selectedItem.scoreRule.cap ?? 0} min={0} step={0.01} onChange={(value) => updateClaimable((item) => { if (item.scoreRule.type === 'per_unit') { if (value > 0) item.scoreRule.cap = value; else delete item.scoreRule.cap } })} />
                    </div>
                  )}

                  {selectedItem.scoreRule.type === 'free' && (
                    <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                      <NumberField label="最小分" value={selectedItem.scoreRule.min} step={0.01} onChange={(value) => updateClaimable((item) => { if (item.scoreRule.type === 'free') item.scoreRule.min = value })} />
                      <NumberField label="最大分" value={selectedItem.scoreRule.max} step={0.01} onChange={(value) => updateClaimable((item) => { if (item.scoreRule.type === 'free') item.scoreRule.max = value })} />
                    </div>
                  )}

                  {selectedItem.scoreRule.type === 'threshold' && (
                    <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
                      <Field label="计量单位"><input value={selectedItem.scoreRule.unit} onChange={(event) => updateClaimable((item) => { if (item.scoreRule.type === 'threshold') item.scoreRule.unit = event.target.value })} style={boxInput} /></Field>
                      <NumberField label="最低数量" value={selectedItem.scoreRule.minimum} min={0.01} step="any" onChange={(value) => updateClaimable((item) => { if (item.scoreRule.type === 'threshold') item.scoreRule.minimum = value })} />
                      <NumberField label="达标分值" value={selectedItem.scoreRule.award} min={0} step={0.01} onChange={(value) => updateClaimable((item) => { if (item.scoreRule.type === 'threshold') item.scoreRule.award = value })} />
                    </div>
                  )}

                  {selectedItem.scoreRule.type === 'enum' && (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
                      <Field label="层级名称（可选）" hint="用 / 分隔，例如「赛事级别 / 获奖等级」。没配就按档名里的「·」拆。">
                        <PathInput
                          value={selectedItem.scoreRule.levels}
                          placeholder="赛事级别 / 获奖等级"
                          onChange={(levels) => updateClaimable((item) => {
                            if (item.scoreRule.type !== 'enum') return
                            if (levels.length) item.scoreRule.levels = levels
                            else delete item.scoreRule.levels
                          })}
                        />
                      </Field>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>档次与建议分</span>
                        <Btn onClick={() => updateClaimable((item) => { if (item.scoreRule.type === 'enum') item.scoreRule.options.push({ label: '新档次', score: 0 }) })}>＋档次</Btn>
                        <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.55, flex: 1, minWidth: 200 }}>
                          学生选档会带出建议分。路径默认按档名里的「·」拆，对不齐再点「改写」。
                        </span>
                      </div>
                      {/* 三列没有表头时，中间那列的灰字看着像"没填却有值"。 */}
                      <div data-r="enum-option" style={{ display: 'grid', gridTemplateColumns: 'minmax(140px,1fr) minmax(160px,1fr) 110px auto', gap: 8 }}>
                        {['档次名称', '学生端选择路径', '建议分', ''].map((head, i) => (
                          <span key={i} style={{ fontSize: 11.5, fontWeight: 600, color: 'var(--fg3)' }}>{head}</span>
                        ))}
                      </div>
                      {selectedItem.scoreRule.options.map((option, optionIndex) => (
                        <div key={optionIndex} data-r="enum-option" style={{ display: 'grid', gridTemplateColumns: 'minmax(140px,1fr) minmax(160px,1fr) 110px auto', gap: 8 }}>
                          <input aria-label={`档次 ${optionIndex + 1} 名称`} placeholder="稳定档次名称" value={option.label} onChange={(event) => updateClaimable((item) => { if (item.scoreRule.type === 'enum') item.scoreRule.options[optionIndex].label = event.target.value })} style={boxInput} />
                          {/* 绝大多数行不需要碰这一列，所以默认是只读文本而不是输入框——
                              一排空着的框会让人以为"这里漏填了"。真要改的是少数：
                              比如某个小项里几行的第一级是赛事级别、另几行却是成果类型，
                              自动拆出来的第一级对不齐，学生点下去就是乱的。
                              有没有 path 本身就是"自动还是手改"的开关，不另外存状态。
                              自动的口径与 lib/schemeTree.ts 的 enumOptionPath 一致。 */}
                          {option.path ? (
                            <div style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
                              <PathInput
                                value={option.path}
                                ariaLabel={`档次 ${optionIndex + 1} 层级路径`}
                                onChange={(path) => updateClaimable((item) => {
                                  if (item.scoreRule.type !== 'enum') return
                                  if (path.length) item.scoreRule.options[optionIndex].path = path
                                  else delete item.scoreRule.options[optionIndex].path
                                })}
                              />
                              <TinyBtn
                                title="改回按名称自动拆"
                                onClick={() => updateClaimable((item) => {
                                  if (item.scoreRule.type === 'enum') delete item.scoreRule.options[optionIndex].path
                                })}
                              >
                                恢复自动
                              </TinyBtn>
                            </div>
                          ) : (
                            <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                              <span style={{ flex: 1, minWidth: 0, fontSize: 12.5, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                {autoPath(option.label).join(' / ')}
                              </span>
                              <TinyBtn
                                title="改成手动指定这一档的层级"
                                onClick={() => updateClaimable((item) => {
                                  if (item.scoreRule.type === 'enum') item.scoreRule.options[optionIndex].path = autoPath(option.label)
                                })}
                              >
                                改写
                              </TinyBtn>
                            </div>
                          )}
                          <input type="number" value={option.score} step={0.01} onChange={(event) => updateClaimable((item) => { if (item.scoreRule.type === 'enum') item.scoreRule.options[optionIndex].score = Number.isFinite(event.target.valueAsNumber) ? event.target.valueAsNumber : 0 })} style={boxInput} />
                          <Btn danger disabled={enumOptionCount(category.items[selection.itemIndex]) === 1} onClick={() => updateClaimable((item) => { if (item.scoreRule.type === 'enum') item.scoreRule.options.splice(optionIndex, 1) })}>删除</Btn>
                        </div>
                      ))}
                    </div>
                  )}
                </div>

                <div style={{ borderTop: '1px solid var(--line)', paddingTop: 16, display: 'flex', flexDirection: 'column', gap: 14 }}>
                  <Toggle label="启用佐证材料规则" checked={!!selectedItem.evidence} onChange={(checked) => updateClaimable((item) => { if (checked) item.evidence = defaultEvidence(); else delete item.evidence })} />
                  {selectedItem.evidence && (
                    <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
                      <Toggle label="学生必须上传" checked={selectedItem.evidence.required} onChange={(checked) => updateClaimable((item) => { if (item.evidence) item.evidence.required = checked })} />
                      <Field label="允许扩展名" hint="用英文逗号分隔"><input value={selectedItem.evidence.types.join(',')} onChange={(event) => updateClaimable((item) => { if (item.evidence) item.evidence.types = event.target.value.split(',').map((value) => value.trim()) })} style={boxInput} /></Field>
                      <NumberField label="单文件上限（MB）" value={selectedItem.evidence.maxMb} min={1} step={1} onChange={(value) => updateClaimable((item) => { if (item.evidence) item.evidence.maxMb = value })} />
                    </div>
                  )}
                </div>

                <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                  <Field label="互斥组" hint="同一组里只算最高的那一条。留空就是不互斥"><input value={selectedItem.exclusiveGroup ?? ''} onChange={(event) => updateClaimable((item) => { if (event.target.value) item.exclusiveGroup = event.target.value; else delete item.exclusiveGroup })} style={boxInput} /></Field>
                  <Toggle
                    label="启用共享封顶组"
                    checked={!!selectedItem.capGroup}
                    onChange={(checked) => updateClaimable((item) => {
                      if (!checked) { delete item.capGroup; return }
                      /* 默认挂到本大项已有的第一个组上——绝大多数情况就是"我要和它们共用上限"，
                         而不是"我要开一个新组"。开新组在下面的下拉里选。 */
                      const first = capGroupsIn(category)[0]
                      item.capGroup = first ? { key: first.key, cap: first.cap } : { key: `${category.key}_cap`, cap: 10 }
                    })}
                  />
                </div>
                {selectedItem.capGroup && (
                  <CapGroupPicker
                    category={category}
                    itemKey={selectedItem.key}
                    value={selectedItem.capGroup}
                    onPick={(key, cap) => updateClaimable((item) => { item.capGroup = { key, cap } })}
                    onCap={(cap) => commit((next) => {
                      /* 上限是"这个组的"，不是"这个小项的"。改一处就同步给全组，
                         否则各成员上填的数一旦不一致，要等点发布时后端才报
                         cap group has conflicting caps（scheme/validate.go:98）。 */
                      const key = selectedItem.capGroup!.key
                      for (const item of next.categories[selection.categoryIndex].items) {
                        if (item.capGroup?.key === key) item.capGroup.cap = cap
                      }
                    })}
                  />
                )}

                <ActivitiesEditor
                  activities={selectedItem.activities}
                  onChange={(activities) => updateClaimable((item) => {
                    if (activities?.length) item.activities = activities
                    else delete item.activities
                  })}
                />

                <NoteMarkdownField
                  label="小项说明（学生可见）"
                  hint="学生提交时能看到。别写互斥组、共享组这些内部名字。"
                  value={selectedItem.note ?? ''}
                  placeholder={'- 各项职务加分不累加，取最高\n- 只任职一学期者减半'}
                  onChange={(value) => updateClaimable((item) => { if (value.trim()) item.note = value; else delete item.note })}
                />
              </>
            )}
          </>
        ) : (
          <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>这个节点已经没了，在左边重新选一个。</div>
        )}
      </div>
    </div>
  )
}
