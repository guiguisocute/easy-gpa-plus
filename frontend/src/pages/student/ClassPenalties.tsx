/* 学生 · 举报同学。

   形状照搬综测小组的「扣分与异议」：左边选人，右边摊开这个人的计分卡，逐行展开填写。
   两边做的是同一件事——对某人的某一条计分提出异议——所以没有理由让学生学两套操作。

   三处不同，都是有意的：

   1. 匿名。举报人不写进举报本身，审计也不记 actor 和 IP（后端 appendAnonymousAudit）。
      班长、审核员、同学都查不到是谁提的。
   2. 审核与申诉轨迹匿名公开。公示窗口内可读取已公开的佐证原件；
      窗口外隐藏附件，处理人身份与未公开备注始终隐藏。
   3. 只能往低里报。举报不是送分的通道，主张把分改高会被后端直接拒掉。

   举报不会直接改分：它先派给两名审核人背靠背复核，两人一致才落定，不一致升给班级
   管理员。这一段必须在页面上说清楚——否则举报会被当成"我点一下他就掉分"。 */

import { useState } from 'react'
import { Paperclip, ShieldAlert } from 'lucide-react'
import { Btn, Empty, Note, PageHead, Pill, Seg, Split, SplitCol, Stat, StatGrid, Sub, TextBtn } from '@/components/ui'
import { ScoreHistory } from '@/components/ScoreHistory'
import { usePublicityWindow } from '@/components/usePublicityWindow'
import { FilePicker } from '@/components/EvidenceUploader'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useClassPenalties, useInvalidateReports, useMyReports, useReportActions, useWindow, uploadReportEvidence } from '@/api/queries'
import {
  REPORT_STATUS_LABEL,
  type ClassPenaltyEntry,
  type ClassPenaltyMember,
  type MyReport,
  type ReportStatus,
  type ReportablePenaltyItem,
} from '@/api/types'
import type { CategoryKey } from '@/lib/types'

const TABS = [
  { key: 'edit', label: '举报台' },
  { key: 'mine', label: '我提交的举报' },
] as const

const STATUS_TONE: Record<ReportStatus, 'ok' | 'warn' | 'bad' | 'idle'> = {
  reviewing: 'warn',
  applied: 'ok',
  dismissed: 'idle',
  escalated: 'bad',
  final: 'ok',
}

interface Draft {
  kind: 'base' | 'penalty' | 'submission'
  category: CategoryKey
  itemKey: string
  targetId?: string
  quantity?: number
  proposedScore?: number
  basis: string
}

/* 举报要带的材料。和小组提案那边一样只是先攒着：后端的附件口子挂在
   /reports/:id/notes，得先有举报 id 才有东西可挂，而这条举报要等提交那一下才存在。
   所以顺序是先建举报、再把攒下的文件逐份送上去，对填表的人仍然是一次操作。 */
type Staged = { draft: Draft; files: File[] }

/* ---- 一行举报编辑器 ----
   三种对象的输入方式和「扣分与异议」完全一致：扣分项填次数（分值 = 次数 × 单价），
   基础项和已定分条目填建议分。差别只在于这里多一条硬约束：只能往低里报。 */

function Editor({ title, hint, current, lo, hi, unitScore, busy, onCancel, onSave }: {
  title: string
  hint: string
  current: number | null
  lo: number
  hi: number
  /** 扣分项的单价（负数）。给了它就按"次数"输入，否则按分值输入 */
  unitScore?: number | null
  busy: boolean
  onCancel: () => void
  onSave: (v: { score: number; quantity: number | null; basis: string; files: File[] }) => void
}) {
  const [raw, setRaw] = useState('')
  const [basis, setBasis] = useState('')
  const [files, setFiles] = useState<File[]>([])

  const n = Number(raw)
  const filled = raw.trim() !== '' && !Number.isNaN(n)
  const quantity = unitScore != null ? (filled ? n : null) : null
  const score = unitScore != null ? (filled ? Math.round(n * unitScore * 1000) / 1000 : NaN) : n
  const scoreBad = !filled || Number.isNaN(score) || score < lo || score > hi || (unitScore != null && n <= 0)
  /* 只能往低里报。扣分项没有这个问题（它只会往负里加），前两类要挡。 */
  const notWorse = unitScore == null && current != null && filled && score >= current
  const basisLength = [...basis.trim()].length
  const ok = !scoreBad && !notWorse && basisLength >= 10

  return (
    <div style={{ borderTop: '1px solid var(--line2)', padding: '16px 0 20px', display: 'flex', flexDirection: 'column', gap: 14, animation: 'rise .2s ease both' }}>
      <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>{title}</div>
      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '160px 1fr', gap: 18, alignItems: 'start' }}>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>{unitScore != null ? '次数' : '你认为应得'}</span>
          <input
            value={raw}
            onChange={(e) => setRaw(e.target.value)}
            inputMode="decimal"
            placeholder={unitScore != null ? '如 3' : `现为 ${f.score(current)}`}
            style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${raw.trim() && (scoreBad || notWorse) ? 'var(--red)' : 'var(--warn)'}`, padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
          />
          <span style={{ fontSize: 11.5, color: raw.trim() && (scoreBad || notWorse) ? 'var(--red)' : 'var(--fg3)', lineHeight: 1.6 }}>
            {notWorse
              ? '举报只能要求把分往低了改。觉得自己分少了，那是本人去走申诉'
              : unitScore != null
                ? filled && !scoreBad
                  ? `${n} 次 × ${f.score(unitScore)} = ${f.delta(score)} 分`
                  : `每次 ${f.score(unitScore)} 分`
                : `允许 ${lo} — ${hi}，且必须低于现在的 ${f.score(current)}`}
          </span>
        </label>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: basis.trim() && basisLength < 10 ? 'var(--red)' : 'var(--fg2)' }}>
            说明 · 至少 10 个字。审核人能看到这段话，但不知道是谁写的
          </span>
          <MarkdownEditor value={basis} onChange={setBasis} minHeight={82} invalid={basis.trim() !== '' && basisLength < 10} placeholder={hint} />
        </div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>
          佐证文件 · 可以不传，传了会跟这条举报一起交给审核人
        </span>
        <FilePicker title="把佐证拖到这里" busy={busy} actionLabel="提交中…" onFiles={(picked) => setFiles((prev) => [...prev, ...picked])} />
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
            {/* 文件不会替你隐身：截图里的水印、文档属性里的作者名都跟着走。 */}
            <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.7 }}>
              系统不记是谁传的，但文件本身可能暴露你——
              截图里的头像昵称、文档属性里的作者名，传之前自己先看一眼。
            </span>
          </div>
        )}
      </div>

      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
        <Btn primary danger disabled={!ok || busy} onClick={() => onSave({ score, quantity, basis: basis.trim(), files })}>
          {busy ? '提交中…' : files.length ? `匿名提交举报 · 带 ${files.length} 份佐证` : '匿名提交举报'}
        </Btn>
        <Btn onClick={onCancel}>取消</Btn>
        <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>匿名提交，由两名审核人分别复核。</span>
      </div>
    </div>
  )
}

/* ---- 计分卡的行 ---- */

function EntryRow({ entry, busy, canReport, onReport }: { entry: ClassPenaltyEntry; busy: boolean; canReport: boolean; onReport: (staged: Staged) => void }) {
  const [open, setOpen] = useState(false)
  const penalty = entry.kind === 'penalty'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', background: open ? 'var(--sub)' : 'transparent' }}>
      <button
        type="button"
        className="hv-sub"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '12px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
      >
        <span style={{ width: 58, flex: 'none', display: 'flex' }}>
          {penalty ? <span style={{ ...mono('11px', '.04em'), color: 'var(--red)' }}>扣分</span>
            : entry.kind === 'base' ? <Pill tone="ok">基础分</Pill> : <Pill tone="idle">已定分</Pill>}
        </span>
        <span style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span style={{ fontSize: 13, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{entry.itemName}</span>
          {entry.source === 'admin_grant' && <Pill tone="warn">班管直接加分</Pill>}
          {entry.source === 'collective_grant' && <Pill>共同加分决议</Pill>}
          <span style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{entry.categoryName}</span>
        </span>
        <span style={{ width: 70, flex: 'none', textAlign: 'right', fontSize: 13.5, fontWeight: 600, color: penalty ? 'var(--red)' : 'var(--fg)', ...num }}>
          {penalty ? f.delta(entry.score) : f.score(entry.score)}
        </span>
        <span style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
      </button>

      {open && <ScoreHistory kind={entry.kind} targetId={entry.targetId} audience="student" />}

      {open && canReport && (
        <Editor
          title={penalty ? `举报 · ${entry.itemName}` : `对这一条提出举报 · 现为 ${f.score(entry.score)} 分`}
          hint={penalty ? '写清什么时间、什么事、你是怎么知道的' : '写清你觉得这一条高在哪，依据的是细则里哪一条'}
          current={entry.score}
          lo={0}
          hi={Math.max(entry.score, 0)}
          busy={busy}
          onCancel={() => setOpen(false)}
          onSave={(v) => {
            onReport({
              draft: {
                kind: entry.kind,
                category: entry.category,
                itemKey: entry.itemKey,
                targetId: entry.targetId ?? undefined,
                proposedScore: v.score,
                basis: v.basis,
              },
              files: v.files,
            })
            setOpen(false)
          }}
        />
      )}
    </div>
  )
}

/* 扣分项要单独列：它可以在"还没有任何记录"时被举报，前两类都是对着已有的一行提意见。 */
function PenaltyRow({ item, targetId, busy, canReport, onReport }: { item: ReportablePenaltyItem; targetId: string | null; busy: boolean; canReport: boolean; onReport: (staged: Staged) => void }) {
  const [open, setOpen] = useState(false)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', background: open ? 'var(--sub)' : 'transparent' }}>
      <button
        type="button"
        className="hv-sub"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '12px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
      >
        <span style={{ ...mono('11px', '.04em'), color: 'var(--red)', width: 58, flex: 'none' }}>扣分</span>
        <span style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span style={{ fontSize: 13, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.itemName}</span>
          <span style={{ fontSize: 12, color: 'var(--fg3)' }}>{item.categoryName}</span>
        </span>
        <span data-r="hidesm" style={{ ...mono('11.5px', '0'), width: 78, flex: 'none', textAlign: 'right' }}>每次 {f.score(item.perScore)}</span>
        <span style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
      </button>

      {open && <ScoreHistory kind="penalty" targetId={targetId} audience="student" />}
      {open && canReport && (
        <Editor
          title={`举报 · ${item.itemName}`}
          hint="写清什么时间、什么事、你怎么知道的。审核人手上就只有这段话"
          current={null}
          lo={-Infinity}
          hi={0}
          unitScore={item.perScore}
          busy={busy}
          onCancel={() => setOpen(false)}
          onSave={(v) => {
            onReport({
              draft: { kind: 'penalty', category: item.category, itemKey: item.itemKey, quantity: v.quantity ?? 1, basis: v.basis },
              files: v.files,
            })
            setOpen(false)
          }}
        />
      )}
    </div>
  )
}

/* ---- 一个人的计分卡 ---- */

function Scorecard({ member, penaltyItems, busy, canReport, publicityOpen, onReport }: {
  member: ClassPenaltyMember
  penaltyItems: ReportablePenaltyItem[]
  canReport: boolean
  publicityOpen: boolean
  busy: boolean
  onReport: (staged: Staged) => void
}) {
  const [group, setGroup] = useState<'scored' | 'penalty'>('scored')
  const scored = member.entries.filter((row) => row.kind !== 'penalty')
  const penalties = member.entries.filter((row) => row.kind === 'penalty')

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, flexWrap: 'wrap', paddingBottom: 14 }}>
        <span style={{ fontSize: 16, fontWeight: 600, letterSpacing: '-.025em' }}>{member.name}</span>
        <span style={mono('11.5px', '.02em')}>{member.sid}</span>
        {member.self && <Pill tone="idle">本人</Pill>}
        <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>
          {member.openReports > 0 ? `${member.openReports} 条举报复核中` : '暂无未结举报'}
        </span>
      </div>

      <Seg
        items={[
          { key: 'scored', label: `基础分与已定分（${scored.length}）` },
          { key: 'penalty', label: `扣分项（${penaltyItems.length}）` },
        ]}
        value={group}
        onChange={setGroup}
      />

      {member.self ? (
        <div style={{ paddingTop: 16 }}>
          <Note tone="warn">这是你自己。对自己的分有意见走「我的申诉」，举报是用来提别人的。</Note>
        </div>
      ) : group === 'scored' ? (
        <div style={{ paddingTop: 16 }}>
          <Sub title="基础分与已定分条目" note={publicityOpen ? '展开可看轨迹和原件' : '展开可看轨迹，原件仅公示期可看'} />
          {scored.map((entry) => (
            <EntryRow key={`${entry.kind}-${entry.category}-${entry.itemKey}-${entry.targetId ?? ''}`} entry={entry} busy={busy} canReport={canReport} onReport={onReport} />
          ))}
          {scored.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>这个人还没有已定分的条目。</div>}
        </div>
      ) : (
        <div style={{ paddingTop: 16 }}>
          <Sub title="扣分项" note={canReport ? '填次数就行，分数按单价乘出来' : '展开可看轨迹和公示佐证'} />
          {penalties.length > 0 && (
            <div style={{ fontSize: 12, color: 'var(--fg3)', paddingBottom: 6, textWrap: 'pretty' }}>
              已记在册：{penalties.map((row) => `${row.itemName} ${f.delta(row.score)}`).join('、')}
            </div>
          )}
          {penaltyItems.map((item) => (
            <PenaltyRow key={`${item.category}-${item.itemKey}`} item={item} targetId={penalties.find((row) => row.category === item.category && row.itemKey === item.itemKey)?.targetId ?? null} busy={busy} canReport={canReport} onReport={onReport} />
          ))}
          {penaltyItems.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>方案里没有扣分项。</div>}
        </div>
      )}
    </div>
  )
}

/* ---- 我提交的举报 ---- */

function MyReportRow({ row }: { row: MyReport }) {
  return (
    <div style={{ borderTop: '1px solid var(--line2)', padding: '13px 0', display: 'flex', alignItems: 'baseline', gap: 12, flexWrap: 'wrap' }}>
      <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', minWidth: 0 }}>{row.itemName}</span>
      <span style={{ fontSize: 12, color: 'var(--fg3)' }}>
        {row.categoryName}{row.quantity != null && ` · ${row.quantity} 次`}
      </span>
      <span style={{ fontSize: 12.5, color: 'var(--fg3)', ...num }}>
        {row.currentScore === null ? '' : `${f.score(row.currentScore)} → `}
        <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--red)' }}>{f.score(row.proposedScore)}</span>
      </span>
      <span style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        {row.status === 'reviewing' && (
          <span style={{ fontSize: 12, color: 'var(--fg3)', ...num }}>复核 {row.decidedReviews}/{row.expectedReviews}</span>
        )}
        {row.finalScore !== null && row.status !== 'reviewing' && (
          <span style={{ fontSize: 12.5, color: 'var(--fg2)', ...num }}>认定 {f.score(row.finalScore)}</span>
        )}
        <Pill tone={STATUS_TONE[row.status]}>{REPORT_STATUS_LABEL[row.status]}</Pill>
        <span style={{ ...mono('11px', '0'), width: 96, textAlign: 'right' }}>{f.dayMonth(row.createdAt)}</span>
      </span>
      {/* 被举报人姓名不回显：你知道自己报了谁，接口没必要再确认一次。 */}
      <div style={{ width: '100%', fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7 }}>{row.basis}</div>
      {row.evidence.length > 0 && (
        /* 只列文件名，不给下载入口。发一次下载地址就要在审计里记一笔「谁取了这份文件」，
           而这份文件挂在你报的那条举报上，班管翻一下审计就把人对出来了。
           要确认传没传上，这一行就够。 */
        <div style={{ width: '100%', display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', fontSize: 12, color: 'var(--fg3)' }}>
          <Paperclip size={12} strokeWidth={1.8} style={{ flex: 'none' }} />
          <span>已附 {row.evidence.length} 份：</span>
          {row.evidence.map((one) => <span key={one.id} style={mono('11px', '0')}>{one.name}</span>)}
        </div>
      )}
      {row.decisionReason && (
        <div style={{ width: '100%', fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75, borderLeft: '2px solid var(--line)', paddingLeft: 11 }}>
          处理结论：{row.decisionReason}
        </div>
      )}
    </div>
  )
}

/* ---- 页面 ---- */

export default function StudentClassPenalties() {
  const say = useApp((s) => s.say)
  const [tab, setTab] = useState<(typeof TABS)[number]['key']>('edit')
  const [query, setQuery] = useState('')
  const [picked, setPicked] = useState<string | null>(null)

  const window = useWindow({ live: true })
  const open = window.data?.capabilities.studentReport ?? false
  const publicityOpen = usePublicityWindow(window.data?.window.publicity, window.data?.serverNow, window.dataUpdatedAt)
  const readable = open || publicityOpen
  const locked = !!window.data?.window.lockdown && Date.parse(window.data.window.lockdown) <= Date.parse(window.data.serverNow)
  const canReport = open && !locked
  const penalties = useClassPenalties(readable)
  const mine = useMyReports(open)
  const actions = useReportActions()
  const refreshReports = useInvalidateReports()

  const members = penalties.data?.items ?? []
  const q = query.trim().toLowerCase()
  const visible = q ? members.filter(row => row.name.toLowerCase().includes(q) || row.sid.includes(q)) : members
  const current = members.find((row) => row.userId === picked) ?? null
  const myRows = mine.data?.items ?? []
  const openReports = myRows.filter((row) => row.status === 'reviewing' || row.status === 'escalated').length

  /* 佐证要等举报有了 id 才传得上去（/reports/:id/notes 认的是举报人本人，
     而且只在还没有人下过结论之前放行）。所以先建举报、再逐份送文件；
     哪一份失败就说是哪一份——举报本身已经进复核队列了，不会因为附件挂了白填一遍。 */
  const file = ({ draft, files }: Staged) => {
    if (!picked || !canReport) return
    actions.file.mutate(
      { studentUserId: picked, ...draft },
      {
        onSuccess: async (created) => {
          if (files.length === 0) {
            say('举报已提交。')
            return
          }
          try {
            for (const one of files) await uploadReportEvidence(created.id, one)
            say(`举报已提交 · ${files.length} 份佐证`)
          } catch (e) {
            say(`${e instanceof ApiError ? e.message : '佐证上传失败'} · 举报本身已经提交，佐证没跟上`)
          } finally {
            refreshReports()
          }
        },
        onError: (e) => say(e instanceof ApiError ? e.message : '提交失败'),
      },
    )
  }

  if (!window.isLoading && !readable) {
    return (
      <div style={{ animation: 'rise .28s ease both' }}>
        <PageHead en="REPORT A PEER" title="举报台" desc="看全班的计分情况，也可以匿名举报。" />
        <Empty
          title="举报台暂未开放"
          desc="本班未开放匿名举报，且当前不在佐证公示期间。"
        />
      </div>
    )
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="REPORT A PEER"
        title="举报台"
        desc="看全班计分，公示期内可看原件。"
        side={
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--fg3)' }}>
            <ShieldAlert size={15} strokeWidth={1.7} />
            <span style={{ fontSize: 12.5 }}>举报匿名 · 每天最多 {penalties.data?.dailyLimit ?? 10} 条</span>
          </div>
        }
      />

      <div style={{ paddingBottom: 16 }}><Note>
        {publicityOpen ? `佐证公示中 · 至 ${f.dateTime(window.data?.window.publicity?.close)}。展开计分条目可预览、下载原件。` : window.data?.window.publicity ? `佐证公示时间：${f.dateTime(window.data.window.publicity.open)} 至 ${f.dateTime(window.data.window.publicity.close)}。当前不开放原件。` : '本班尚未设置佐证公示时间。'}
        {!canReport && ' 当前仅供查看，不能提交举报。'}
      </Note></div>
      <StatGrid cols={3}>
        <Stat en="班级成员" value={String(members.length)} unit="人" note={publicityOpen ? '公示期间可查看佐证，处理人身份隐藏' : '计分与轨迹公开，处理人身份及附件隐藏'} />
        <Stat en="我提交的举报" value={String(myRows.length)} unit="条" note="只有你自己看得到这一栏" />
        <Stat en="其中还在处理" value={String(openReports)} unit="条" note="复核中或等班管终裁" tone={openReports > 0 ? 'var(--warn)' : undefined} />
      </StatGrid>

      <div style={{ padding: '22px 0 14px' }}>
        <Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={setTab} />
      </div>

      {tab === 'edit' ? (
        <>
          <Split cols="280px 1fr">
            <SplitCol first>
              <div style={{ padding: '4px 0 30px' }}>
                <Sub title="班级成员" note={`${members.length} 人`} />
                <input
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜姓名或学号"
                  style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--line)', padding: '9px 12px', color: 'var(--fg)', font: 'inherit', fontSize: 13, marginBottom: 8 }}
                />
                {penalties.isLoading ? (
                  <div className="load-bar"><span /></div>
                ) : visible.length === 0 ? (
                  <div style={{ fontSize: 12.5, color: 'var(--fg3)', padding: '10px 0' }}>没有匹配的成员。</div>
                ) : (
                  <div style={{ maxHeight: 520, overflowY: 'auto' }}>
                    {visible.map((member) => {
                      const on = member.userId === picked
                      return (
                        <button
                          key={member.userId}
                          type="button"
                          className="hv-sub"
                          onClick={() => setPicked(member.userId)}
                          style={{ display: 'flex', alignItems: 'center', gap: 10, width: '100%', background: on ? 'var(--sub)' : 'none', border: 0, borderTop: '1px solid var(--line2)', borderLeft: `2px solid ${on ? 'var(--red)' : 'transparent'}`, margin: 0, padding: '11px 8px', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
                        >
                          <span style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0, flex: 1 }}>
                            <span style={{ fontSize: 13, fontWeight: on ? 600 : 400, color: 'var(--fg)' }}>{member.name}</span>
                            <span style={mono('11px', '.02em')}>{member.sid}</span>
                          </span>
                          {member.self && <Pill tone="idle">本人</Pill>}
                          {member.penaltyTotal < 0 && (
                            <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--red)', ...num }}>{f.delta(member.penaltyTotal)}</span>
                          )}
                          {member.openReports > 0 && <Pill tone="warn">{member.openReports}</Pill>}
                        </button>
                      )
                    })}
                  </div>
                )}
              </div>
            </SplitCol>

            <SplitCol>
              <div style={{ padding: '4px 0 30px' }}>
                {!current ? (
                  <Empty title="请选择成员" />
                ) : (
                  <Scorecard
                    key={current.userId}
                    member={current}
                    penaltyItems={penalties.data?.penaltyItems ?? []}
                    busy={actions.file.isPending}
                    canReport={canReport}
                    publicityOpen={publicityOpen}
                    onReport={file}
                  />
                )}
              </div>
            </SplitCol>
          </Split>

          <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '16px 20px', marginTop: 10, display: 'flex', flexDirection: 'column', gap: 8 }}>
            <span style={{ fontSize: 13, fontWeight: 600 }}>复核规则</span>
            <span style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.8, textWrap: 'pretty' }}>
              举报匿名提交，由两名审核人分别复核。结论一致即生效，不一致由班级管理员终裁。
            </span>
          </div>
        </>
      ) : mine.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : myRows.length === 0 ? (
        <Empty title="暂无举报记录" />
      ) : (
        <div>
          {myRows.map((row) => <MyReportRow key={row.id} row={row} />)}
        </div>
      )}

      <div style={{ paddingTop: 26 }}>
        <Note>佐证文件只有审核人和班级管理员能看。对自己的分有意见走「我的申诉」</Note>
      </div>
    </div>
  )
}
