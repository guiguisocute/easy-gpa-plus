/* 综测小组 · 扣分与异议。

   这一页把三件原本散在各处的事收进一个台面：
   ① 改某人的基础分（课堂纪律扣了 2 分这种）；
   ② 提交某人的扣分项（无故缺勤 3 次 = −3 分）；
   ③ 对某人**任意已定分条目**提出异议（我认为这条不该给 4 分）。

   三件事共用同一条通道，而且都**不是直接改分**：小组只提出提案，
   一律先落草稿，批量提交后进班级管理员的终裁队列，管理员签字才生效。

   为什么这么设计：扣分是全系统最容易起争议的地方（§4.2 末），
   如果小组能直接落笔，学生的第一反应必然是"谁扣的、凭什么"，而系统答不上来。
   走一遍终裁，每一笔扣分背后都有一个签字的人和一条审计记录——
   代价是多一步，换来的是这一步说得清。

   页面形状：左选人 → 右改分 → 下方草稿箱批量提交。
   一次点头交一整批，而不是每条弹一次确认——扣分录入天然是成批的。

   共治模式用的是同一页（collective）。区别只有两处：接口挂在 /governance 下、
   由「是否生效共治成员」放行；交上去之后不进班管队列，而是随机派给几名成员
   独立评审。判的东西、填的东西、能不能撤回都没变，所以不另起一页。 */

import { useState } from 'react'
import { Paperclip } from 'lucide-react'
import { Btn, Empty, Note, PageHead, Pill, Seg, Split, SplitCol, Stat, StatGrid, Sub, Table, TextBtn, THead, TRow } from '@/components/ui'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { ScoreHistory } from '@/components/ScoreHistory'
import { FilePicker } from '@/components/EvidenceUploader'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { RichText } from '@/components/Markdown'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { scoreBounds } from '@/lib/claim'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useClassificationActions, useInvalidateObjections, useObjectionActions, useObjections, useReviewStudents, useScheme, useScorecard, uploadObjectionEvidence, type NewObjection } from '@/api/queries'
import {
  OBJECTION_KIND_LABEL,
  OBJECTION_STATUS_LABEL,
  type Objection,
  type ObjectionStatus,
  type ScorecardBaseRow,
  type ScorecardSubRow,
  type StudentScorecard,
} from '@/api/types'
import { STATUS_LABEL } from '@/lib/types'
import type { CategoryKey } from '@/lib/types'
import { claimableItems } from '@/lib/schemeTree'
import '@/styles/mobile-objections.css'

const TABS = [
  { key: 'edit', label: '编辑台' },
  { key: 'mine', label: '我的提案' },
] as const

const STATUS_TONE: Record<ObjectionStatus, 'ok' | 'warn' | 'bad' | 'idle'> = {
  draft: 'idle',
  submitted: 'warn',
  applied: 'ok',
  adjusted: 'ok',
  dismissed: 'bad',
  withdrawn: 'idle',
}

/* ---- 一行提案编辑器 ----
   三种对象各有各的输入方式，但产出的都是同一个 ObjectionInput：
   基础项直接填实得分，扣分项填次数（分值 = 次数 × 单价），定分异议填建议分。

   佐证在这里只是**攒着**，不当场上传：后端的附件口子挂在 /objections/:id/notes，
   要先有提案 id 才有东西可挂，而这条提案要等「加入草稿箱」才存在。所以顺序是
   先建草稿、再把攒下的文件逐份传上去，对填表的人来说仍然是一次操作。 */

function Editor({
  title,
  hint,
  current,
  lo,
  hi,
  unitScore,
  onCancel,
  onSave,
  busy,
  handler,
}: {
  title: string
  hint: string
  current: number | null
  lo: number
  hi: number
  /** 扣分项的单价（负数）。给了它就按"次数"输入，否则按分值输入 */
  unitScore?: number | null
  onCancel: () => void
  onSave: (v: { score: number; quantity: number | null; basis: string; files: File[] }) => void
  busy: boolean
  /** 交上去之后谁来判：班级管理员，或共治的随机评审。 */
  handler: string
}) {
  const [raw, setRaw] = useState('')
  const [basis, setBasis] = useState('')
  const [files, setFiles] = useState<File[]>([])

  const n = Number(raw)
  const filled = raw.trim() !== '' && !Number.isNaN(n)
  const quantity = unitScore != null ? (filled ? n : null) : null
  /* Keep the same thousandth precision as scheme.NewPoints on the backend;
     rounding to tenths turns a valid 0.25-point penalty into the wrong score. */
  const score = unitScore != null ? (filled ? Math.round(n * unitScore * 1000) / 1000 : NaN) : n
  const scoreBad = !filled || Number.isNaN(score) || score < lo || score > hi || (unitScore != null && n < 0)
  const ok = !scoreBad && basis.trim().length >= 4

  return (
    <div style={{ borderTop: '1px solid var(--line2)', padding: '16px 0 20px', display: 'flex', flexDirection: 'column', gap: 14, animation: 'rise .2s ease both' }}>
      <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>{title}</div>
      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '160px 1fr', gap: 18, alignItems: 'start' }}>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>{unitScore != null ? '次数' : '分值'}</span>
          <input
            value={raw}
            onChange={(e) => setRaw(e.target.value)}
            inputMode="decimal"
            placeholder={unitScore != null ? '如 3' : `现为 ${f.score(current)}`}
            style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${raw.trim() && scoreBad ? 'var(--red)' : 'var(--warn)'}`, padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
          />
          <span style={{ fontSize: 11.5, color: raw.trim() && scoreBad ? 'var(--red)' : 'var(--fg3)', lineHeight: 1.6 }}>
            {unitScore != null
              ? filled && !scoreBad
                ? `${n} 次 × ${f.score(unitScore)} = ${f.delta(score)} 分`
                : `每次 ${f.score(unitScore)} 分 · 合计不低于 ${lo === -Infinity ? '无下限' : lo}`
              : `允许 ${lo === -Infinity ? '不设下限' : lo} — ${hi === Infinity ? '不封顶' : hi}`}
          </span>
        </label>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>依据 · 至少 4 个字，学生能看到</span>
          <MarkdownEditor value={basis} onChange={setBasis} minHeight={72} placeholder={hint} />
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>
          佐证文件 · 可以不传，传了会跟这条提案一起交给{handler}
        </span>
        <FilePicker title="把佐证拖到这里" busy={busy} actionLabel="保存中…" onFiles={(picked) => setFiles((prev) => [...prev, ...picked])} />
        {files.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {files.map((file, at) => (
              <div key={`${file.name}-${at}`} style={{ display: 'flex', alignItems: 'center', gap: 10, border: '1px solid var(--line)', padding: '8px 12px', minWidth: 0 }}>
                <Paperclip size={13} strokeWidth={1.7} style={{ flex: 'none', color: 'var(--fg3)' }} />
                <span title={file.name} style={{ fontSize: 12.5, color: 'var(--fg)', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{file.name}</span>
                <span style={{ ...mono('11px', '0'), flex: 'none' }}>{f.bytes(file.size)}</span>
                <span style={{ marginLeft: 'auto', flex: 'none' }}>
                  <TextBtn onClick={() => setFiles((prev) => prev.filter((_, i) => i !== at))}>移除</TextBtn>
                </span>
              </div>
            ))}
            {/* 说清它们还没上路：这一步只是攒着，真正落盘在「加入草稿箱」那一下。 */}
            <span style={{ fontSize: 11.5, color: 'var(--fg3)' }}>点「加入草稿箱」时随提案一起上传</span>
          </div>
        )}
      </div>

      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
        <Btn primary disabled={!ok || busy} onClick={() => onSave({ score, quantity, basis: basis.trim(), files })}>
          {busy ? '保存中…' : files.length ? `加入草稿箱 · 带 ${files.length} 份佐证` : '加入草稿箱'}
        </Btn>
        <Btn onClick={onCancel}>取消</Btn>
        <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>草稿只有你自己看得到，交上去才会进{handler}的队列</span>
      </div>
    </div>
  )
}

/* ---- 计分卡的三组行 ---- */

function BaseRow({ row, onCreate, busy, collective }: { row: ScorecardBaseRow; onCreate: (v: NewObjection, files: File[]) => void; busy: boolean; collective: boolean }) {
  const [open, setOpen] = useState(false)
  const penalty = row.kind === 'penalty'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', background: open ? 'var(--sub)' : 'transparent' }}>
      <button
        type="button"
        className="hv-sub"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '12px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
      >
        <span style={{ width: 52, flex: 'none', display: 'flex' }}>
          {penalty ? <span style={{ ...mono('11px', '.04em'), color: 'var(--red)' }}>扣分</span> : <Pill tone="ok">基础分</Pill>}
        </span>
        <span style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span style={{ fontSize: 13, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.itemName}</span>
          <span style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {row.recorded ? row.basis || '（无依据）' : penalty ? '尚无扣分记录' : '方案默认满分，无需录入'}
          </span>
        </span>
        <span style={{ width: 66, flex: 'none', textAlign: 'right', fontSize: 13.5, fontWeight: 600, color: penalty ? 'var(--red)' : 'var(--fg)', ...num }}>
          {penalty ? f.delta(row.score) : f.score(row.score)}
        </span>
        <span data-r="hidesm" style={{ ...mono('11.5px', '0'), width: 60, flex: 'none', textAlign: 'right' }}>
          {penalty ? `每次 ${f.score(row.perScore)}` : `满分 ${f.score(row.fullScore)}`}
        </span>
        <span style={{ width: 74, flex: 'none', display: 'flex', justifyContent: 'flex-end' }}>
          {row.locked ? <Pill tone="warn">申诉中</Pill> : penalty ? <Pill tone={row.recorded ? 'ok' : 'idle'}>{row.recorded ? '已录入' : '未录入'}</Pill> : null}
        </span>
        <span style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
      </button>

      {open && <ScoreHistory kind={row.kind} targetId={row.id} audience="reviewer" collective={collective} />}
      {open && row.locked && <Note tone="warn">申诉处理中，可以查看轨迹，暂不能提交新提案。</Note>}
      {open && !row.locked && (
        <Editor
          title={penalty ? `提交扣分 · ${row.itemName}` : `调整基础分 · ${row.itemName}`}
          hint={penalty ? '写清依据：出勤册第几页、通报文号或者学院文件编号' : '写清扣分或者恢复的依据。学生申诉的时候看的就是这段话'}
          current={row.score}
          lo={penalty ? -Infinity : 0}
          hi={penalty ? 0 : (row.fullScore ?? 0)}
          unitScore={penalty ? row.perScore : undefined}
          busy={busy}
          handler={collective ? '随机评审' : '班级管理员'}
          onCancel={() => setOpen(false)}
          onSave={(v) => {
            onCreate({
              kind: penalty ? 'penalty' : 'base',
              category: row.category,
              itemKey: row.itemKey,
              targetId: row.id,
              proposedScore: v.score,
              quantity: v.quantity,
              basis: v.basis,
            }, v.files)
            setOpen(false)
          }}
        />
      )}
    </div>
  )
}

function SubRow({ row, onCreate, busy, collective }: { row: ScorecardSubRow; onCreate: (v: NewObjection, files: File[]) => void; busy: boolean; collective: boolean }) {
  const [open, setOpen] = useState(false)
  const say = useApp((state) => state.say)
  /* 共治模式下旧班管身份不带任何额外权限（governanceGuard 会把角色降成学生），
     归类建议那条路走的是 /admin，后端直接 403，所以这里一并收起来。 */
  const isAdmin = useApp((state) => state.user?.role === 'class_admin') && !collective
  const scheme = useScheme()
  const classification = useClassificationActions()
  const [targetCategory, setTargetCategory] = useState<CategoryKey>(row.category)
  const [targetItem, setTargetItem] = useState(row.itemKey)
  const [classificationReason, setClassificationReason] = useState('')
  const { lo, hi } = scoreBounds(row.ruleSnapshot.item.scoreRule)
  const category = scheme.data?.categories.find((item) => item.key === targetCategory)
  const targetItems = category ? claimableItems(category) : []

  const changeCategory = (value: CategoryKey) => {
    setTargetCategory(value)
    const next = scheme.data?.categories.find((item) => item.key === value)
    setTargetItem(next ? (claimableItems(next)[0]?.key ?? '') : '')
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', background: open ? 'var(--sub)' : 'transparent' }}>
      <button
        type="button"
        className="hv-sub"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '12px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
      >
        <span style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.title}</span>
          {row.source === 'admin_grant' && <Pill tone="warn">班管直接加分</Pill>}
          {row.source === 'collective_grant' && <Pill>共同加分决议</Pill>}
          <span style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {row.categoryName} · {row.itemName} · {f.ruleText(row.ruleSnapshot.item.scoreRule)}
          </span>
        </span>
        <span style={{ width: 66, flex: 'none', textAlign: 'right', fontSize: 13.5, fontWeight: 600, ...num }}>{f.score(row.finalScore)}</span>
        <span style={{ width: 92, flex: 'none', display: 'flex', justifyContent: 'flex-end' }}>
          {row.locked ? <Pill tone="warn">申诉中</Pill> : <Pill tone="ok">{STATUS_LABEL[row.status]}</Pill>}
        </span>
        <span style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
      </button>

      {open && (
        <>
          {row.reviewedByMe && (
            <div style={{ padding: '12px 0 0' }}>
              <Note tone="warn">这条是你自己审的。对自己审过的条目提异议没问题，但班级管理员那边会看到这一点。</Note>
            </div>
          )}
          <ScoreHistory kind="submission" targetId={row.id} audience="reviewer" collective={collective} />
          {row.locked ? <Note tone="warn">申诉处理中，可以查看轨迹，暂不能提交新提案。</Note> : <Editor
            title={`对已定分条目提出异议 · 现为 ${f.score(row.finalScore)} 分`}
            hint="写清你觉得判得偏在哪，依据的是细则里哪一条"
            current={row.finalScore}
            lo={lo}
            hi={hi}
            busy={busy}
            handler={collective ? '随机评审' : '班级管理员'}
            onCancel={() => setOpen(false)}
            onSave={(v) => {
              onCreate({
                kind: 'submission',
                category: row.category,
                itemKey: row.itemKey,
                targetId: row.id,
                proposedScore: v.score,
                quantity: null,
                basis: v.basis,
              }, v.files)
              setOpen(false)
            }}
          />}
          {isAdmin && <div style={{ borderTop: '1px solid var(--line2)', marginTop: 14, padding: '14px 0' }}>
            <Sub title="班管归类建议" />
            <div style={{ display: 'grid', gridTemplateColumns: 'minmax(130px,1fr) minmax(150px,1fr)', gap: 10 }}>
              <select value={targetCategory} onChange={(event) => changeCategory(event.target.value as CategoryKey)} style={fieldStyle}>
                {scheme.data?.categories.filter((item) => claimableItems(item).length).map((item) => <option key={item.key} value={item.key}>{item.name}</option>)}
              </select>
              <select value={targetItem} onChange={(event) => setTargetItem(event.target.value)} style={fieldStyle}>
                {targetItems.map((item) => <option key={item.key} value={item.key}>{item.name}</option>)}
              </select>
            </div>
            <textarea value={classificationReason} onChange={(event) => setClassificationReason(event.target.value)} placeholder="归类建议理由至少 4 字" style={{ ...fieldStyle, minHeight: 72, resize: 'vertical', marginTop: 10 }} />
            <div style={{ marginTop: 10 }}><Btn disabled={!targetItem || (targetCategory === row.category && targetItem === row.itemKey) || classificationReason.trim().length < 4 || classification.suggest.isPending} onClick={() => classification.suggest.mutate({ submissionId: row.id, category: targetCategory, itemKey: targetItem, reason: classificationReason.trim() }, { onSuccess: () => { say('归类建议已记下，结算前必须处理掉'); setClassificationReason('') }, onError: (error) => say(error instanceof ApiError ? error.message : '归类建议保存失败') })}>登记归类建议</Btn></div>
          </div>}
        </>
      )}
    </div>
  )
}

/* ---- 计分卡 ---- */

function Scorecard({ card, onCreate, busy, collective }: { card: StudentScorecard; onCreate: (v: NewObjection, files: File[]) => void; busy: boolean; collective: boolean }) {
  const [group, setGroup] = useState<'penalty' | 'scored'>('scored')
  const base = card.baseItems.filter((r) => r.kind === 'base')
  const penalty = card.baseItems.filter((r) => r.kind === 'penalty')

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, flexWrap: 'wrap', paddingBottom: 14 }}>
        <span style={{ fontSize: 16, fontWeight: 600, letterSpacing: '-.025em' }}>{card.student.name}</span>
        <span style={mono('11.5px', '.02em')}>{card.student.sid}</span>
        {card.student.self && <Pill tone="idle">本人</Pill>}
        {card.student.sealed && <Pill tone="ok">已封存</Pill>}
        <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>
          {card.student.openObjections > 0 ? `${card.student.openObjections} 条未结提案` : '暂无未结提案'}
        </span>
      </div>

      <Seg
        items={[
          { key: 'scored', label: `已定分条目（${base.length + card.submissions.length}）` },
          { key: 'penalty', label: `扣分项（${penalty.length}）` },
        ]}
        value={group}
        onChange={setGroup}
      />

      {group === 'penalty' ? (
        <div style={{ paddingTop: 16 }}>
          <Sub title="扣分项" note="填次数就行，分数按单价乘出来，不用手填" />
          {penalty.map((r) => (
            <BaseRow key={`${r.category}-${r.itemKey}`} row={r} onCreate={onCreate} busy={busy} collective={collective} />
          ))}
          {penalty.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>方案里没有扣分项。</div>}
        </div>
      ) : (
        <div style={{ paddingTop: 16 }}>
          <Sub title="已定分条目" note="基础分默认满分。哪条有异议，在这里提" />
          {base.map((r) => (
            <BaseRow key={`${r.category}-${r.itemKey}`} row={r} onCreate={onCreate} busy={busy} collective={collective} />
          ))}
          {card.submissions.map((r) => (
            <SubRow key={r.id} row={r} onCreate={onCreate} busy={busy} collective={collective} />
          ))}
          {base.length === 0 && card.submissions.length === 0 && (
            <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>这个人还没有已定分的条目。</div>
          )}
        </div>
      )}
    </div>
  )
}

/* ---- 草稿箱的一行 ----
   点开就看得到这条提案带了哪些佐证——缩略图能点开预览，也能下载，和班管终裁台上
   看到的是同一份。这里不提供上传入口：文件是在上面那个编辑器里跟着提案一起交的，
   草稿箱只负责"确认交对了没有"。 */
function DraftRow({ row, checked, onToggle, onRemove }: { row: Objection; checked: boolean; onToggle: () => void; onRemove: () => void }) {
  const [open, setOpen] = useState(false)
  const files = row.noteEvidence ?? []

  return (
    <div style={{ borderTop: '1px solid var(--line2)', background: open ? 'var(--sub)' : 'transparent' }}>
      <div className="objection-draft-row" style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 0', flexWrap: 'wrap' }}>
        {/* 勾选框在可点区域之外单独放着：勾选和展开是两件事，套在一起谁都会点错。 */}
        <input
          type="checkbox"
          checked={checked}
          onChange={onToggle}
          aria-label={`选中 ${row.student} 的 ${row.itemName}`}
          style={{ width: 15, height: 15, flex: 'none', accentColor: 'var(--red)', cursor: 'pointer' }}
        />
        <button
          type="button"
          className="hv-fg objection-draft-toggle"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          style={{ display: 'flex', alignItems: 'center', gap: 12, flex: 1, minWidth: 0, background: 'none', border: 0, margin: 0, padding: 0, font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
        >
          <span className="objection-draft-identity" style={{ display: 'contents' }}>
            <span style={{ fontSize: 12.5, color: 'var(--fg)', width: 76, flex: 'none' }}>{row.student}</span>
            <span style={{ ...mono('11px', '.02em'), width: 64, flex: 'none' }}>{OBJECTION_KIND_LABEL[row.kind]}</span>
          </span>
          <span className="objection-draft-summary" style={{ fontSize: 12.5, color: 'var(--fg2)', minWidth: 0, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {row.itemName} · {row.basis}
          </span>
          {files.length > 0 && (
            <span className="objection-draft-files" style={{ display: 'flex', alignItems: 'center', gap: 3, flex: 'none', fontSize: 11.5, color: 'var(--fg3)' }}>
              <Paperclip size={11} strokeWidth={1.8} />{files.length}
            </span>
          )}
          <span className="objection-draft-score" style={{ fontSize: 12.5, color: 'var(--fg3)', flex: 'none', ...num }}>
            {f.score(row.currentScore)} → <span style={{ color: 'var(--fg)', fontWeight: 600 }}>{f.score(row.proposedScore)}</span>
            {row.quantity != null && `（${row.quantity} 次）`}
          </span>
          <span className="objection-draft-expand" style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
        </button>
        <TextBtn onClick={onRemove}>删除</TextBtn>
      </div>

      {open && (
        <div style={{ padding: '2px 0 18px' }}>
          {files.length > 0 ? (
            <EvidenceFiles items={files} />
          ) : (
            <div style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: 'var(--fg3)' }}>
              <Paperclip size={13} strokeWidth={1.7} />
              这条提案没带佐证。删掉重提一条，就能在编辑器里附文件了。
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/* ---- 页面 ---- */

export default function ReviewObjections({ collective = false }: { collective?: boolean } = {}) {
  const say = useApp((s) => s.say)
  const [tab, setTab] = useState<(typeof TABS)[number]['key']>('edit')
  const [query, setQuery] = useState('')
  const [picked, setPicked] = useState<string | null>(null)
  const [checked, setChecked] = useState<Set<string>>(new Set())

  const students = useReviewStudents(collective)
  const card = useScorecard(picked, collective)
  const objections = useObjections({}, collective)
  const actions = useObjectionActions(collective)
  const refreshObjections = useInvalidateObjections(collective)
  /* 交出去之后谁来判：普通模式是班管终裁，共治是随机抽到的几名成员独立评审。
     页面其余部分一个字都不用改。 */
  const handler = collective ? '随机评审' : '班级管理员'

  /* 一个班几十号人，搜索直接过滤就够了；套 useMemo 反而要为
     "每次渲染都新建的数组"再想一层依赖，得不偿失。 */
  const roster = students.data?.items ?? []
  const q = query.trim().toLowerCase()
  const visible = q ? roster.filter((s) => s.name.toLowerCase().includes(q) || s.sid.includes(q)) : roster

  const all = objections.data?.items ?? []
  const drafts = all.filter((o) => o.status === 'draft')
  const submitted = all.filter((o) => o.status === 'submitted')
  const decided = all.filter((o) => o.status === 'applied' || o.status === 'adjusted')
  const dismissed = all.filter((o) => o.status === 'dismissed')

  const toggle = (id: string) =>
    setChecked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  /* 佐证要等提案有了 id 才传得上去（/objections/:id/notes 认的是提案本人 + 草稿状态）。
     所以先建草稿，再把编辑器里攒下的文件逐份送上去；哪一份失败就说是哪一份，
     提案本身已经落进草稿箱了，不会因为附件挂了而白填一遍。 */
  const create = (v: NewObjection, files: File[]) => {
    if (!picked) return
    actions.create.mutate({ ...v, studentUserId: picked }, {
      onSuccess: async (created) => {
        if (files.length === 0) {
          say(`已加入草稿箱 · 在下面勾上，就能一起交给${handler}`)
          return
        }
        try {
          for (const file of files) await uploadObjectionEvidence(created.id, file, undefined, collective)
          say(`已加入草稿箱 · 带 ${files.length} 份佐证，在下方勾选后一起提交`)
        } catch (e) {
          say(`${e instanceof ApiError ? e.message : '佐证上传失败'} · 提案存进草稿箱了，可以在那一行重试`)
        } finally {
          refreshObjections()
        }
      },
      onError: (e) => say(e instanceof ApiError ? e.message : '保存失败'),
    })
  }

  const submitChecked = () => {
    const ids = drafts.filter((d) => checked.has(d.id)).map((d) => d.id)
    if (ids.length === 0) return
    actions.submit.mutate(ids, {
      onSuccess: (r) => {
        setChecked(new Set())
        say(
          collective
            ? `已提交 ${r.submitted} 条 · 交给随机评审，提案人和当事人回避。生效之前学生看不到`
            : `已提交 ${r.submitted} 条 · 等班级管理员裁。生效之前学生看不到`,
        )
      },
      onError: (e) => say(e instanceof ApiError ? e.message : '提交失败'),
    })
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en={collective ? 'SCORE OBJECTIONS' : 'DEDUCTIONS & OBJECTIONS'}
        title={collective ? '扣分与成绩异议' : '扣分与异议'}
        desc={collective ? '基础分、扣分和定分异议 · 事实与依据 · 随机评审' : '基础分、扣分和定分异议'}
        side={
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
            <span style={{ fontSize: 26, fontWeight: 600, letterSpacing: '-.04em', ...num }}>{drafts.length}</span>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>条草稿待提交</span>
          </div>
        }
      />

      <StatGrid cols={4}>
        <Stat en="草稿" value={String(drafts.length)} unit="条" note="只有你看得到，随时可改可删" />
        <Stat en={collective ? '评审中' : '待终裁'} value={String(submitted.length)} unit="条" note={`已交给${handler}`} tone={submitted.length ? 'var(--warn)' : undefined} />
        <Stat en="已生效" value={String(decided.length)} unit="条" note={collective ? '多数评审给出同一裁决后记分' : '班级管理员照原提案确认，或者改了分再确认'} />
        <Stat en="被驳回" value={String(dismissed.length)} unit="条" note={collective ? '多数评审认为不成立' : '管理员认为不成立'} tone={dismissed.length ? 'var(--red)' : undefined} />
      </StatGrid>

      <div style={{ padding: '22px 0 14px' }}>
        <Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={setTab} />
      </div>

      {tab === 'edit' ? (
        <>
          <Split cols="280px 1fr">
            <SplitCol first>
              <div style={{ padding: '4px 0 30px' }}>
                <Sub title="班级成员" note={`${roster.length} 人 · 含本人`} />
                <input
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜姓名或学号"
                  style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--line)', padding: '9px 12px', color: 'var(--fg)', font: 'inherit', fontSize: 13, marginBottom: 8 }}
                />
                {students.isLoading ? (
                  <div className="load-bar"><span /></div>
                ) : visible.length === 0 ? (
                  <div style={{ fontSize: 12.5, color: 'var(--fg3)', padding: '10px 0' }}>没有匹配的成员。</div>
                ) : (
                  <div style={{ maxHeight: 520, overflowY: 'auto' }}>
                    {visible.map((s) => {
                      const on = s.userId === picked
                      return (
                        <button
                          key={s.userId}
                          type="button"
                          className="hv-sub"
                          onClick={() => setPicked(s.userId)}
                          style={{ display: 'flex', alignItems: 'center', gap: 10, width: '100%', background: on ? 'var(--sub)' : 'none', border: 0, borderTop: '1px solid var(--line2)', borderLeft: `2px solid ${on ? 'var(--red)' : 'transparent'}`, margin: 0, padding: '11px 8px', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
                        >
                          <span style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0, flex: 1 }}>
                            <span style={{ fontSize: 13, fontWeight: on ? 600 : 400, color: 'var(--fg)' }}>{s.name}</span>
                            <span style={mono('11px', '.02em')}>{s.sid}</span>
                          </span>
                          {s.self && <Pill tone="idle">本人</Pill>}
                          {s.openObjections > 0 && <Pill tone="warn">{s.openObjections}</Pill>}
                        </button>
                      )
                    })}
                  </div>
                )}
              </div>
            </SplitCol>

            <SplitCol>
              <div style={{ padding: '4px 0 30px' }}>
                {!picked ? (
                  <Empty title="请选择成员" />
                ) : card.isLoading ? (
                  <div className="load-bar"><span /></div>
                ) : !card.data ? (
                  <Empty title="读不到这个人的计分卡" desc="他可能被停用了，也可能方案还没发布。" />
                ) : (
                  <Scorecard key={card.data.student.userId} card={card.data} onCreate={create} busy={actions.create.isPending} collective={collective} />
                )}
              </div>
            </SplitCol>
          </Split>

          <div style={{ border: '1px solid var(--line)', marginTop: 10 }}>
            <div style={{ background: 'var(--sub)', borderBottom: '1px solid var(--line)', padding: '16px 20px', display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 0 }}>
                <span style={{ fontSize: 14.5, fontWeight: 600, letterSpacing: '-.02em' }}>草稿箱</span>
                <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>勾上一起提交，{handler}那边会当成一批来处理</span>
              </div>
              <div style={{ marginLeft: 'auto', display: 'flex', gap: 10, flexWrap: 'wrap' }}>
                <Btn onClick={() => setChecked(new Set(drafts.map((d) => d.id)))} disabled={drafts.length === 0}>全选</Btn>
                <Btn primary disabled={checked.size === 0 || actions.submit.isPending} onClick={submitChecked}>
                  {actions.submit.isPending ? '提交中…' : `提交给${handler}（${checked.size}）`}
                </Btn>
              </div>
            </div>

            {drafts.length === 0 ? (
              <div style={{ padding: '18px 20px', fontSize: 12.5, color: 'var(--fg3)' }}>草稿箱是空的。在上面的计分卡里改一行，它就会出现在这里。</div>
            ) : (
              <div style={{ padding: '4px 20px 16px' }}>
                {drafts.map((o) => (
                  <DraftRow
                    key={o.id}
                    row={o}
                    checked={checked.has(o.id)}
                    onToggle={() => toggle(o.id)}
                    onRemove={() =>
                      actions.remove.mutate(o.id, {
                        onSuccess: () => say('草稿已删除'),
                        onError: (e) => say(e instanceof ApiError ? e.message : '删除失败'),
                      })
                    }
                  />
                ))}
              </div>
            )}
          </div>
        </>
      ) : (
        <MyObjections
          rows={all}
          collective={collective}
          onWithdraw={(id) =>
            actions.withdraw.mutate(id, {
              onSuccess: () => say(`已撤回 · 这条不在草稿里了，${handler}那边也不再显示`),
              onError: (e) => say(e instanceof ApiError ? e.message : '撤回失败'),
            })
          }
        />
      )}

      <div style={{ paddingTop: 26 }}>
        <Note>
          {collective
            ? '提案本身不改分。交上去之后随机抽成员独立认定，多数给出同一裁决才生效，结果和依据给学生看。'
            : '提案要班级管理员确认之后才生效，然后把结果和依据给学生看。'}
        </Note>
      </div>
    </div>
  )
}

/* ---- 我的提案 ---- */

function MyObjections({ rows, onWithdraw, collective }: { rows: Objection[]; onWithdraw: (id: string) => void; collective: boolean }) {
  const [status, setStatus] = useState<'all' | ObjectionStatus>('all')
  const filtered = status === 'all' ? rows : rows.filter((r) => r.status === status)

  return (
    <div>
      <div style={{ paddingBottom: 14 }}>
        <Seg
          items={[
            { key: 'all', label: '全部' },
            { key: 'submitted', label: collective ? '评审中' : '待终裁' },
            { key: 'applied', label: '已生效' },
            { key: 'dismissed', label: '被驳回' },
          ]}
          value={status}
          onChange={(k) => setStatus(k as 'all' | ObjectionStatus)}
        />
      </div>

      {filtered.length === 0 ? (
        <Empty title="这一类下没有提案" desc="在「编辑台」里改一行分数，会先存成草稿。" />
      ) : (
        <Table cols="84px 88px minmax(150px,1.4fr) 92px 92px 100px 84px">
          <THead cells={['学生', '类型', '对象与依据', '当前分', '建议分', '状态', '操作']} />
          {filtered.map((o) => (
            <TRow
              key={o.id}
              cells={[
                <span key="a" style={{ color: 'var(--fg)', fontWeight: 500 }}>{o.student}</span>,
                <span key="b" style={{ fontSize: 12, color: 'var(--fg3)' }}>{OBJECTION_KIND_LABEL[o.kind]}</span>,
                <span key="c" style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 7, minWidth: 0 }}>
                    <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{o.itemName}</span>
                    {!!o.noteEvidence?.length && (
                      <span title={`${o.noteEvidence.length} 份佐证`} style={{ display: 'flex', alignItems: 'center', gap: 2, flex: 'none', fontSize: 11, color: 'var(--fg3)' }}>
                        <Paperclip size={11} strokeWidth={1.8} />{o.noteEvidence.length}
                      </span>
                    )}
                  </span>
                  <details style={{ fontSize: 12, color: 'var(--fg3)' }}>
                    <summary style={{ cursor: 'pointer' }}>{o.decisionReason ? `查看${collective ? '裁决' : '终裁'}说明与附件` : '查看提案依据与附件'}</summary>
                    <RichText value={o.decisionReason || o.basis} evidence={o.noteEvidence} />
                  </details>
                </span>,
                <span key="d" style={num}>{f.score(o.currentScore)}</span>,
                <span key="e" style={{ ...num, fontWeight: 600 }}>{f.score(o.proposedScore)}</span>,
                <span key="f" style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-start' }}>
                  <Pill tone={STATUS_TONE[o.status]}>
                    {collective && o.status === 'submitted' ? '随机评审中' : OBJECTION_STATUS_LABEL[o.status]}
                  </Pill>
                  {o.decidedScore !== null && (
                    <span style={{ ...mono('11px', '0') }}>{collective ? '裁决' : '终裁'} {f.score(o.decidedScore)}</span>
                  )}
                </span>,
                <span key="g">
                  {/* 共治里提交那一刻就抽好了评审席位、记下了该回避的人。收回提案会
                      留下一个没东西可判的案子，本版不做撤签，所以这里也不给撤回。 */}
                  {o.status === 'submitted' && !collective ? (
                    <TextBtn onClick={() => onWithdraw(o.id)}>撤回</TextBtn>
                  ) : (
                    <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
                      {o.status === 'draft' ? '在编辑台' : o.status === 'submitted' ? '等评审' : f.dayMonth(o.decidedAt)}
                    </span>
                  )}
                </span>,
              ]}
            />
          ))}
        </Table>
      )}
    </div>
  )
}
