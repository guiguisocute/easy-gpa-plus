/* 综测小组 · 处理申诉。学生第 1 次申诉，原封不动交回原班人马复评（DESIGN §5.2）。

   四件事必须做对：
   1. 复评的是**原来那两个人**——不是排除原审核人另找一个。学生要的是"你再看一遍我的理由"，
      换个人从头判等于把当初的双评作废，也让原审核人永远得不到"我判错了"的反馈；
   2. 背靠背只保护第二轮——第一轮的两条结论学生早就看得到了，对同行藏没有意义；
      但同行这一次复评了什么，在两人都交完之前一律不下发；
   3. 复评只有两种结论：维持原判、改判。两种都必须写理由，理由原样回给学生；
   4. 两人复评一致即落定并结案，学生不服还能再提一次；不一致则直接升给班级管理员终裁，
      而班管一签字这一笔就封死了——所以「还能不能再申诉」看的是班管下没下过结论，不是次数。

   这一页与「审核工作台」刻意不共用一个台面：那边是首评队列（背靠背、看不到别人任何东西），
   这边是复评（看得到第一轮的全部结论和学生的反驳）。两种信息可见性混在一个页面上迟早会漏。 */

import { useEffect, useState } from 'react'
import { BackLink, Btn, ChoiceChip, Empty, Note, PageHead, Pill, Row, Seg, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, TRow, Timeline } from '@/components/ui'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { scoreBounds } from '@/lib/claim'
import { readableTrail } from '@/lib/trail'
import { needsMyRereview, rereviewNotice, rereviewProgressLabel } from '@/lib/appealProgress'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { AppealBrief, StepBar } from '@/components/AppealBrief'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { RichText } from '@/components/Markdown'
import { RuleCard } from '@/components/RuleCard'
import { ReviewSubject } from '@/components/ReviewSubject'
import { useReviewAppeal, useReviewAppeals, useRereviewActions, useScheme } from '@/api/queries'
import { claimableItems, schemePathName } from '@/lib/schemeTree'
import { findCurrentItem } from '@/lib/ruleDiff'
import type { CategoryKey } from '@/lib/types'
import {
  APPEAL_STATUS_LABEL,
  appealRoundLabel,
  DECISION_LABEL,
  REREVIEW_LABEL,
  type Appeal,
  type AppealDetail,
  type AppealStatus,
  type Evidence,
  type NamedReview,
  type RereviewDecision,
  type StudentReview,
} from '@/api/types'

const SCOPES = [
  { key: 'pending', label: '待我复评', desc: '学生申诉了，等我再看一遍' },
  { key: 'done', label: '我已复评', desc: '我交了，等同行或等终裁' },
  { key: 'all', label: '全部', desc: '我参与过的全部申诉' },
] as const

type Scope = (typeof SCOPES)[number]['key']

const TONE: Record<AppealStatus, 'ok' | 'warn' | 'bad' | 'idle'> = {
  filed: 'idle',
  reviewing: 'warn',
  escalated: 'bad',
  resolved: 'ok',
  final: 'ok',
}

const TARGET_LABEL = { submission: '提交条目', base_score: '基础项', penalty_score: '扣分项' } as const

/* 复评允许的分值区间。提交条目跟着规则快照走（与工作台同一个 scoreBounds），
   基础项是 0—满分，扣分项是负值到 0。前端先拦一道，后端仍会再验一次。 */
function bounds(d: AppealDetail): { lo: number; hi: number } {
  if (d.targetType === 'base_score') return { lo: 0, hi: d.fullScore ?? 0 }
  if (d.targetType === 'penalty_score') return { lo: -Infinity, hi: 0 }
  const rule = d.ruleSnapshot?.item.scoreRule
  return rule ? scoreBounds(rule) : { lo: 0, hi: Infinity }
}

/** 原始结论。小组视角下同行不署名——单盲对学生，对同行也一样，只有管理员看得到人名。 */
function firstRoundReason(r: StudentReview | NamedReview) {
  return 'reviewer' in r ? r.reason : r.why
}

/* 佐证一律走 EvidenceFiles：图片出缩略图、其余按类型出图标。
   这里原本自己画了一份只有文件名的灰框，和工作台、仲裁台各有一份，三份还都不出缩略图。 */
function EvidenceList({ items, title, note }: { items: Evidence[]; title: string; note?: string }) {
  if (items.length === 0) return null
  return (
    <div style={{ paddingTop: 18 }}>
      <Sub title={title} note={note} />
      <EvidenceFiles items={items} />
    </div>
  )
}

/* ---- 复评详情 ---- */

function AppealDesk({ id, queue, onOpen, onBack }: { id: string; queue: Appeal[]; onOpen: (id: string) => void; onBack: () => void }) {
  const say = useApp((s) => s.say)
  const detail = useReviewAppeal(id)
  const rereview = useRereviewActions()
  const scheme = useScheme()
  /* 队列就是刚才那张列表，顺序照搬：翻到哪一条，回列表时也在那一条附近。 */
  const at = queue.findIndex((row) => row.id === id)

  const [decision, setDecision] = useState<RereviewDecision>('uphold')
  const [score, setScore] = useState('')
  const [category, setCategory] = useState<CategoryKey>('moral')
  const [itemKey, setItemKey] = useState('')
  const [why, setWhy] = useState('')
  const [uploading, setUploading] = useState(false)
  const [startedAt, setStartedAt] = useState(() => Date.now())

  /* 换一条申诉就重置计时与表单：用时是写进 review 记录的事实，不能带着上一条的秒数。 */
  useEffect(() => {
    setDecision('uphold')
    setScore('')
    setWhy('')
    setStartedAt(Date.now())
  }, [id])

  const d = detail.data
  const initialCategory = d?.proposedCategory ?? d?.originalCategory ?? d?.category
  const initialItemKey = d?.proposedItemKey ?? d?.originalItemKey ?? d?.itemKey
  const initialScore = d?.proposedScore
  const targetType = d?.targetType
  useEffect(() => {
    if (targetType !== 'submission' || !initialCategory || !initialItemKey) return
    setCategory(initialCategory)
    setItemKey(initialItemKey)
    setScore(initialScore == null ? '' : String(initialScore))
  }, [id, targetType, initialCategory, initialItemKey, initialScore])

  if (detail.isLoading) return <div className="load-bar"><span /></div>
  if (!d) return <Empty title="申诉不存在" desc="可能已经结案，或者不归你复评了。" />

  const me = d.handlers.find((h) => h.mine)
  const peers = d.handlers.filter((h) => !h.mine)
  /* 正文里能内嵌的佐证＝申诉补传的 + 原提交的。两份合起来给渲染器，
     它只解析出现在这里的 id，别处的一律当成"不属于本条"挡掉。 */
  const quotable = [...(d.evidence ?? []), ...(d.originalEvidence ?? []), ...(d.noteEvidence ?? [])]
  const categoryConfig = scheme.data?.categories.find((row) => row.key === category)
  const targetItems = categoryConfig ? claimableItems(categoryConfig) : []
  const targetRule = d.targetType === 'submission' ? targetItems.find((item) => item.key === itemKey)?.scoreRule : undefined
  const { lo, hi } = targetRule ? scoreBounds(targetRule) : bounds(d)
  const baseline = d.baselineScore ?? d.currentScore
  const scoreNum = decision === 'uphold' ? (baseline ?? 0) : Number(score)
  const scoreBad = decision === 'adjust' && (score.trim() === '' || Number.isNaN(scoreNum) || scoreNum < lo || scoreNum > hi)
  const classificationBad = decision === 'adjust' && d.targetType === 'submission' && !itemKey
  const canReview = needsMyRereview(d)
  const reviewing = d.round === 1 && d.status === 'reviewing'
  const canSubmit = canReview && !scoreBad && !classificationBad && why.trim().length >= 6 && !rereview.isPending

  const submit = () => {
    if (!canSubmit || uploading) return
    const spent = Math.max(1, Math.round((Date.now() - startedAt) / 1000))
    const outcomeCategory = decision === 'uphold' ? d.originalCategory ?? d.category : category
    const outcomeItem = decision === 'uphold' ? d.originalItemKey ?? d.itemKey : itemKey
    rereview.mutate(
      { id: d.id, decision, category: d.targetType === 'submission' ? outcomeCategory : undefined, itemKey: d.targetType === 'submission' ? outcomeItem : undefined, score: scoreNum, reason: why.trim(), spentSeconds: spent },
      {
        onSuccess: (r) => {
          say(
            r.conflict
              ? '复评已提交 · 和对方不一致，已经升给班级管理员定'
              : r.bothDecided
                ? `复评已提交 · 两人一致，认定 ${f.score(r.resolvedScore)} 分，本轮申诉结案`
                : '复评已提交 · 等另一个人复评（他看不到你写了什么）',
          )
          onBack()
        },
        onError: (e) => say(e instanceof ApiError ? e.message : '提交失败'),
      },
    )
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <BackLink label="回到申诉列表" onClick={onBack} />

      <PageHead
        en={`APPEAL · #${d.id} · ROUND ${d.round}`}
        title={d.target}
        desc={`${TARGET_LABEL[d.targetType]} · ${appealRoundLabel(d.round)} · 申诉于 ${f.dateTime(d.createdAt)}`}
        side={
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 8 }}>
            <Pill tone={TONE[d.status]}>{APPEAL_STATUS_LABEL[d.status]}</Pill>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 7 }}>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>当前认定</span>
              <span style={{ fontSize: 34, fontWeight: 600, letterSpacing: '-.045em', ...num }}>{f.score(d.currentScore)}</span>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
                分{canReview && ` · 可判 ${lo === -Infinity ? '不设下限' : lo} — ${hi === Infinity ? '不封顶' : hi}`}
              </span>
            </div>
          </div>
        }
      />
      <div style={{ paddingBottom: 20 }}>
        <ReviewSubject
          label="当前复评学生"
          name={d.student}
          sid={d.studentId}
          meta={`申诉对象：${d.target} · ${TARGET_LABEL[d.targetType]} · ${appealRoundLabel(d.round)}`}
        />
      </div>

      {!reviewing && <div style={{ paddingBottom: 20 }}><Note>{rereviewNotice(d)}</Note></div>}
      {d.resolutionScore != null && <section aria-label="本轮处理结论" style={{ border: '1px solid var(--line)', padding: 20, marginBottom: 20 }}>
        <Sub title={d.status === 'final' ? '本轮终裁结论' : '本轮处理结论'} note={f.dateTime(d.resolvedAt)} />
        <Row label="本轮认定分" value={`${f.score(d.resolutionScore)} 分`} />
        <RichText value={d.resolutionReason ?? ''} evidence={quotable} />
      </section>}

      {queue.length > 1 && (
        <div style={{ paddingBottom: 18 }}>
          <StepBar
            at={at}
            total={queue.length}
            prevLabel={at > 0 ? `${queue[at - 1].student} · ${queue[at - 1].target}` : ''}
            nextLabel={at >= 0 && at < queue.length - 1 ? `${queue[at + 1].student} · ${queue[at + 1].target}` : ''}
            onPrev={() => at > 0 && onOpen(queue[at - 1].id)}
            onNext={() => at >= 0 && at < queue.length - 1 && onOpen(queue[at + 1].id)}
          />
        </div>
      )}

      {/* 学生可以在理由里内嵌佐证图，能引的就是 quotable 这份清单里的东西。 */}
      <AppealBrief
        reason={d.reason}
        evidence={quotable}
        note="这一轮要回应的就是它"
        originalPath={d.targetType === 'submission' ? schemePathName(scheme.data, d.originalCategory ?? d.category, d.originalItemKey ?? d.itemKey) : undefined}
        originalScore={d.baselineScore}
        proposedPath={d.targetType === 'submission' ? schemePathName(scheme.data, d.proposedCategory ?? d.category, d.proposedItemKey ?? d.itemKey) : undefined}
        proposedScore={d.proposedScore}
      />

      <Split cols="1.25fr 1fr">
        <SplitCol first>
          <div style={{ padding: '24px 0' }}>
            {d.targetType === 'submission' ? (
              <>
                <Sub title="首次审核结论" note="审核人姓名不公开" />
                <div style={{ display: 'flex', flexDirection: 'column', gap: 12, paddingBottom: 8 }}>
                  {(d.originalReviews ?? []).map((r, i) => (
                    <div key={i} style={{ border: '1px solid var(--line)', padding: '13px 15px', display: 'flex', flexDirection: 'column', gap: 8 }}>
                      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
                        {/* 同学生端一样只编号不署名：两条必然出自两个人，编号让复评人说得出
                            "我改的是哪一条"，而不必知道那是谁。 */}
                        <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>审核人 {i + 1}</span>
                        <Pill tone={r.decision === 'accepted' ? 'ok' : r.decision === 'rejected' ? 'bad' : 'warn'}>{DECISION_LABEL[r.decision]}</Pill>
                        <span style={{ fontSize: 16, fontWeight: 600, ...num }}>{f.score(r.score)}</span>
                        <span style={{ ...mono('11px', '0'), marginLeft: 'auto' }}>{f.dateTime(r.at)}</span>
                      </div>
                      {firstRoundReason(r) ? (
                        <RichText value={firstRoundReason(r)} evidence={quotable} />
                      ) : (
                        <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>（未填写理由）</div>
                      )}
                    </div>
                  ))}
                  {(d.originalReviews?.length ?? 0) === 0 && (
                    <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>这条没有可回放的首评结论。</div>
                  )}
                </div>

                {/* 与初审、成绩核对同一张规则卡：档位铺开、学生选的那一档高亮。
                    复评要判的往往正是"该不该换一档"，只给一行摘要看不出别的档值多少。 */}
                {d.ruleSnapshot && (
                  <div style={{ paddingTop: 20 }}>
                    <RuleCard
                      item={d.ruleSnapshot.item}
                      categoryName={d.ruleSnapshot.categoryName}
                      capturedAt={d.ruleSnapshot.capturedAt}
                      currentItem={findCurrentItem(scheme.data, d.ruleSnapshot.item.key)}
                    />
                  </div>
                )}
              </>
            ) : (
              <>
                <Sub title="录入依据" note="由原录入人复核" />
                <div style={{ border: '1px solid var(--line)', padding: '14px 16px', background: 'var(--sub)' }}>
                  {d.basis ? (
                    <RichText value={d.basis} evidence={quotable} />
                  ) : (
                    <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>（未填写依据）</span>
                  )}
                </div>
                <div style={{ paddingTop: 14 }}>
                  <Row label="当前分值" value={f.score(d.currentScore)} />
                  <Row label={d.targetType === 'base_score' ? '满分' : '单次扣分'} value={d.fullScore === null || d.fullScore === undefined ? '—' : f.score(d.fullScore)} />
                </div>
              </>
            )}

            <EvidenceList items={d.evidence ?? []} title="申诉时补传的佐证" note="会和新旧佐证一起给复评人" />
            <EvidenceList items={d.originalEvidence ?? []} title="原有佐证" />

            <div style={{ paddingTop: 24 }}>
              <Sub title="轨迹" />
              <Timeline
                items={readableTrail(d.trail).map((t) => ({
                  title: t.title,
                  at: f.dateTime(t.at),
                  tone: t.action.includes('escalat') || t.action.includes('final') ? 'var(--red)' : 'var(--fg3)',
                }))}
              />
            </div>
          </div>
        </SplitCol>

        <SplitCol>
          {/* 左栏加了整张规则表以后能滚好几屏，写复评的这一栏跟着吸顶。 */}
          <div data-r="deskside" style={{ paddingBlock: 24, display: 'flex', flexDirection: 'column', gap: 24 }}>
            <div>
              <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 12 }}>本轮复评进度</div>
              {/* 同行一律不署名。小组这一侧是背靠背的：知道"谁在跟我一起看"，
                  就等于知道该找谁对口径，第二个人的独立判断也就没了。只有班管看得到人名。 */}
              {[
                { who: '你', decided: !!me?.decided, bold: true },
                ...peers.map((p, i) => ({ who: peers.length > 1 ? `另一位复评人 ${i + 1}` : '另一位复评人', decided: p.decided, bold: false })),
              ].map((p, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0' }}>
                  <span style={{ width: 5, height: 5, flex: 'none', background: p.decided ? 'var(--ok)' : reviewing ? 'var(--warn)' : 'var(--fg3)' }} />
                  <span style={{ fontSize: 12.5, fontWeight: p.bold ? 600 : 400, color: 'var(--fg)' }}>{p.who}</span>
                  <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>{p.decided ? '已提交复评' : reviewing ? '尚未提交' : '未提交 · 复评已结束'}</span>
                </div>
              ))}
              <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.7, marginTop: 12, textWrap: 'pretty' }}>
                {reviewing ? '两人一致就结案，不一致交班级管理员。' : '以下保留本轮复评的提交情况。'}
              </div>
            </div>

            {/* 两人都交完之后才回放同行的复评内容——在那之前后端把 rereview 恒置为 null */}
            {peers.filter((p) => p.rereview).map((p) => (
              <div key={p.id} style={{ border: '1px solid var(--line)', padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>另一位复评人的复评</span>
                  <Pill tone={p.rereview!.decision === 'uphold' ? 'idle' : 'warn'}>{REREVIEW_LABEL[p.rereview!.decision]}</Pill>
                  <span style={{ fontSize: 16, fontWeight: 600, marginLeft: 'auto', ...num }}>{f.score(p.rereview!.score)}</span>
                </div>
                {d.targetType === 'submission' && <div style={{ fontSize: 12, color: 'var(--fg3)' }}>归类：{schemePathName(scheme.data, p.rereview!.category ?? d.category, p.rereview!.itemKey ?? d.itemKey)}</div>}
                <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75 }}>{p.rereview!.reason}</div>
              </div>
            ))}

            {me?.rereview ? (
              <div style={{ border: '1px solid var(--line)', padding: 20, background: 'var(--sub)' }}>
                <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', paddingBottom: 10 }}>你已就这条申诉给出复评</div>
                <Row label="结论" value={REREVIEW_LABEL[me.rereview.decision]} />
                {d.targetType === 'submission' && <Row label="复评归类" value={schemePathName(scheme.data, me.rereview.category ?? d.category, me.rereview.itemKey ?? d.itemKey)} />}
                <Row label="认定分" value={f.score(me.rereview.score)} />
                <Row label="提交于" value={f.dateTime(me.rereview.at)} />
                <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75, marginTop: 12 }}>{me.rereview.reason}</div>
                <div style={{ fontSize: 12.5, color: 'var(--fg3)', marginTop: 12 }}>
                  {reviewing ? '你已提交，等待另一位复评人或终裁人处理。' : rereviewNotice(d)}
                </div>
              </div>
            ) : !canReview ? (
              <Note>{rereviewNotice(d)}</Note>
            ) : (
              <div style={{ border: '1px solid var(--line)', padding: 20, background: 'var(--sub)' }}>
                <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', paddingBottom: 12 }}>我的复评</div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 9 }}>
                  <ChoiceChip on={decision === 'uphold'} tone="ok" onClick={() => setDecision('uphold')}>
                    维持原判 · {f.score(baseline)} 分
                  </ChoiceChip>
                  <ChoiceChip on={decision === 'adjust'} tone="warn" onClick={() => setDecision('adjust')}>
                    改判
                  </ChoiceChip>
                </div>

                {decision === 'adjust' && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 20 }}>
                    {d.targetType === 'submission' && <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 9 }}>
                      <select value={category} onChange={(event) => { const next = event.target.value as CategoryKey; setCategory(next); const config = scheme.data?.categories.find((row) => row.key === next); setItemKey(config ? claimableItems(config)[0]?.key ?? '' : '') }} style={fieldStyle}>
                        {scheme.data?.categories.map((row) => <option key={row.key} value={row.key}>{row.name}</option>)}
                      </select>
                      <select value={itemKey} onChange={(event) => setItemKey(event.target.value)} style={fieldStyle}>
                        {targetItems.map((item) => <option key={item.key} value={item.key}>{item.name}</option>)}
                      </select>
                    </div>}
                    <label style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>
                      新的认定分 · 规则允许 {lo === -Infinity ? '不设下限' : lo} — {hi === Infinity ? '不封顶' : hi}
                    </span>
                    <input
                      value={score}
                      onChange={(e) => setScore(e.target.value)}
                      inputMode="decimal"
                      placeholder={`原为 ${f.score(baseline)}`}
                      style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${scoreBad && score.trim() ? 'var(--red)' : 'var(--warn)'}`, padding: '11px 13px', color: 'var(--fg)', fontSize: 20, fontWeight: 600, ...num }}
                    />
                    {scoreBad && score.trim() !== '' && (
                      <span style={{ fontSize: 12, color: 'var(--red)' }}>
                        请填 {lo === -Infinity ? '任意负值' : lo} — {hi === Infinity ? '任意' : hi} 之间的数字
                      </span>
                    )}
                    </label>
                  </div>
                )}

                <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 20 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: why.trim() && why.trim().length < 6 ? 'var(--red)' : 'var(--fg2)' }}>
                    复评理由
                  </span>
                  <MarkdownEditor
                    onUploadingChange={setUploading}
                    value={why}
                    onChange={setWhy}
                    minHeight={104}
                    invalid={why.trim() !== '' && why.trim().length < 6}
                    evidence={quotable}
                    uploadTarget={{ kind: 'note', owner: 'appeal', id: d.id }}
                    placeholder={decision === 'uphold' ? '就算维持原判，也要正面回应学生提的那一点，不然这一轮对他毫无意义' : '写清哪一条依据改变了你的判断'}
                  />
                </div>

                <div style={{ display: 'flex', gap: 10, marginTop: 20, flexWrap: 'wrap' }}>
                  <Btn primary tone={decision === 'adjust' ? 'warn' : 'ok'} disabled={uploading || !canSubmit} onClick={submit}>
                    {rereview.isPending ? '提交中…' : '提交复评'}
                  </Btn>
                  <Btn onClick={onBack}>先回列表</Btn>
                </div>
              </div>
            )}

            <Note>
              第一次申诉由原来的审核人复评，再申诉一次就由班级管理员裁。
            </Note>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}

/* ---- 列表 ---- */

export default function ReviewAppeals() {
  const [scope, setScope] = useState<Scope>('pending')
  const [open, setOpen] = useState<string | null>(null)

  const pending = useReviewAppeals('pending')
  const done = useReviewAppeals('done')
  const all = useReviewAppeals('all')
  const byScope = { pending, done, all }

  const current = byScope[scope]
  const rows: Appeal[] = current.data?.items ?? []

  /* 详情页拿的是当前这一组的顺序，「下一条」才和列表看到的一致。 */
  if (open) return <AppealDesk id={open} queue={rows} onOpen={setOpen} onBack={() => setOpen(null)} />

  const allRows: Appeal[] = all.data?.items ?? []
  const escalated = allRows.filter((a) => a.status === 'escalated').length
  const closed = allRows.filter((a) => a.status === 'resolved' || a.status === 'final').length

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="APPEAL DESK"
        title="处理申诉"
        desc="复评派给我的申诉"
      />

      <StatGrid cols={4}>
        <Stat en="待我复评" value={String(pending.data?.items.length ?? 0)} unit="条" note="学生等的就是这几条" tone={(pending.data?.items.length ?? 0) > 0 ? 'var(--warn)' : undefined} />
        <Stat en="我已复评" value={String(done.data?.items.length ?? 0)} unit="条" note="我已提交过复评的申诉" />
        <Stat en="已升终裁" value={String(escalated)} unit="条" note="复评不一致，归班级管理员" tone={escalated > 0 ? 'var(--red)' : undefined} />
        <Stat en="已结案" value={String(closed)} unit="条" note="复评结案或已终裁" />
      </StatGrid>

      <div style={{ padding: '22px 0 14px' }}>
        <Seg items={SCOPES.map((s) => ({ key: s.key, label: s.label }))} value={scope} onChange={setScope} />
      </div>

      {current.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty
          title="这一组现在是空的"
          desc={scope === 'pending' ? '没有等你复评的申诉。' : '换个筛选看看。'}
        />
      ) : (
        <Table cols="76px minmax(160px,1.6fr) 96px 96px 88px 124px 104px">
          <THead cells={['编号', '申诉对象与理由', '学生', '轮次', '当前分', '复评进度', '状态']} />
          {rows.map((a) => {
            return (
              <TRow
                key={a.id}
                label={`打开申诉 ${a.id} · ${a.student} · ${a.target}`}
                onClick={() => setOpen(a.id)}
                cells={[
                  <span key="a" style={{ ...mono('11.5px', '.02em'), color: 'var(--fg2)' }}>#{a.id}</span>,
                  <span key="b" style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                    <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.target}</span>
                    <span style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.reason}</span>
                  </span>,
                  <span key="c">{a.student}</span>,
                  <span key="d" style={{ fontSize: 12.5, color: a.round === 2 ? 'var(--red)' : 'var(--fg3)' }}>第 {a.round} 次</span>,
                  <span key="e" style={num}>{f.score(a.currentScore)}</span>,
                  <span key="f" style={{ fontSize: 12.5, color: needsMyRereview(a) ? 'var(--warn)' : 'var(--fg3)', ...num }}>
                    {rereviewProgressLabel(a)}
                  </span>,
                  <Pill key="g" tone={TONE[a.status]}>{APPEAL_STATUS_LABEL[a.status]}</Pill>,
                ]}
              />
            )
          })}
        </Table>
      )}

      <div style={{ paddingTop: 26 }}>
        <Note>第一次申诉由原来的审核人复评，再申诉一次由班级管理员裁。</Note>
      </div>
    </div>
  )
}
