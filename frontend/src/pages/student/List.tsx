/* 学生 · 我的提交。

   申诉先交回原两名审核人复评（§5.2）；两人一致就结案、学生仍可再提，两人谈不拢则升给班级管理员。
   界面上必须把"这是第几次、下一步归谁"说清楚——学生一共只有两次机会，
   让他在不知道规则的情况下用掉第一次，是这一页最容易犯的错。

   封存与结果确认都在「当前成绩」页（Result.tsx）；申诉整条链路搬到了「我的申诉」（Appeals.tsx）——
   这一页只回答「我交了什么、被判了多少」，申诉那条要跟好几天的流程不该挤在同一个展开区里。 */

import { useState } from 'react'
import { Btn, Empty, PageHead, Pill, Seg, Timeline, type StepState } from '@/components/ui'
import { RichText } from '@/components/Markdown'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { ItemGuide } from '@/components/ItemGuide'
import { ForcedRejectionNotice } from '@/components/ForcedRejectionNotice'
import { ForceRejectSubmissionForm } from '@/components/ForceRejectSubmissionForm'
import { mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { attachClaimableBase } from '@/lib/schemeTree'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useAppeals, useScheme, useSeal, useSubmission, useSubmissionActions, useSubmissions, useWindow } from '@/api/queries'
import { APPEAL_STATUS_LABEL, DECISION_LABEL, type Appeal, type Submission } from '@/api/types'
import { STATUS_LABEL, type SubmissionStatus } from '@/lib/types'

const TABS = [
  { key: 'all', label: '全部' },
  { key: 'wait', label: '在审' },
  { key: 'done', label: '已定分' },
  { key: 'rejected', label: '强制驳回' },
] as const

const TONE: Record<SubmissionStatus, 'ok' | 'warn' | 'bad' | 'idle'> = {
  draft: 'idle',
  pending: 'idle',
  consensus: 'warn',
  scored: 'ok',
  appealing: 'bad',
  arbitrating: 'bad',
  locked: 'ok',
}

function SubItem({
  sub,
  appeals,
  sealed,
  canAppeal,
  canEditWindow,
  resultConfirmed,
}: {
  sub: Submission
  /** 这一条上的申诉，按轮次升序 */
  appeals: Appeal[]
  sealed: boolean
  canAppeal: boolean
  canEditWindow: boolean
  /** 已确认当前成绩 → 申诉入口关闭的提示要说对原因 */
  resultConfirmed: boolean
}) {
  const say = useApp((s) => s.say)
  const go = useApp((s) => s.go)
  const goAppeal = useApp((s) => s.goAppeal)
  const [open, setOpen] = useState(false)
  const [rejecting, setRejecting] = useState(false)

  const detail = useSubmission(open ? sub.id : null)
  const scheme = useScheme()
  const actions = useSubmissionActions()

  /* 正文里能内嵌的佐证＝这一条自己的佐证。渲染器只解析出现在这里的 id。 */
  const quotable = [...(detail.data?.evidence ?? []), ...(detail.data?.noteEvidence ?? [])]

  const settled = sub.status === 'scored' || sub.status === 'locked'
  const canEdit = !sealed && canEditWindow && (sub.status === 'draft' || sub.status === 'pending')
  const snapshot = sub.ruleSnapshot
  /* 下一次申诉是第几轮，决定按钮说什么话。能不能点由后端的 canAppeal 说了算 ——
     已终裁、上一轮未结案、能力关闭这几种情况前端各推一遍迟早推歪。 */
  const nextRound = sub.appealsUsed + 1
  const openAppeal = appeals.find((a) => a.status !== 'final' && a.status !== 'resolved')

  return (
    <div style={{ display: 'flex', flexDirection: 'column', background: open ? 'var(--sub)' : 'transparent' }}>
      <button
        type="button"
        className="hv-sub"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%', background: 'none', border: 0, borderTop: '1px solid var(--line2)', margin: 0, padding: '14px 0', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
      >
        <span style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em', color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {f.submissionTitle(sub.title)}
            {sub.source === 'admin_grant' && <> · 班管直接加分</>}
            {sub.source === 'collective_grant' && <> · 共同加分决议</>}
          </span>
          <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
            {sub.categoryName} · {sub.itemName} · {sub.evidenceCount} 份佐证 ·{' '}
            {sub.submittedAt ? `${f.dayMonth(sub.submittedAt)} 提交` : '未提交'}
          </span>
        </span>
        <span data-r="hidesm" style={{ width: 74, flex: 'none', textAlign: 'right', fontSize: 12.5, color: 'var(--fg3)', ...num }}>
          期望 {f.score(sub.wantScore)}
        </span>
        <span style={{ width: 74, flex: 'none', textAlign: 'right', fontSize: 14, fontWeight: 600, color: sub.finalScore === null ? 'var(--fg3)' : 'var(--fg)', ...num }}>
          {f.score(sub.finalScore)}
        </span>
        <span style={{ width: 92, flex: 'none', display: 'flex', justifyContent: 'flex-end' }}>
          <Pill tone={sub.forceRejection ? 'bad' : TONE[sub.status]}>{sub.forceRejection ? '强制驳回' : sub.forcedScore ? '已强制改分' : STATUS_LABEL[sub.status]}</Pill>
        </span>
        <span style={{ width: 14, flex: 'none', textAlign: 'right', fontSize: 13, color: 'var(--fg3)' }}>{open ? '−' : '+'}</span>
      </button>

      {!open && sub.forcedScore && !sub.forceRejection && <div style={{ paddingBottom: 12, fontSize: 12.5, color: 'var(--red)', lineHeight: 1.7, overflowWrap: 'anywhere' }}>认定分：{f.score(sub.forcedScore.previousScore)} → {f.score(sub.forcedScore.score)} 分 · 改分理由：{sub.forcedScore.reason}</div>}
      {!open && sub.forceRejection && <div style={{ paddingBottom: 12, fontSize: 12.5, color: 'var(--red)', lineHeight: 1.7, overflowWrap: 'anywhere' }}>认定分：{sub.forceRejection.previousScore === null ? '未定分' : f.score(sub.forceRejection.previousScore)} → 0 分 · 驳回理由：{sub.forceRejection.reason}</div>}

      {open && (
        <div style={{ borderTop: '1px solid var(--line2)', padding: '18px 0 24px', display: 'flex', flexDirection: 'column', gap: 18, animation: 'rise .2s ease both' }}>
          {!sub.forceRejection && (detail.data?.submission.forcedScore ?? sub.forcedScore) && <ForcedRejectionNotice rejection={(detail.data?.submission.forcedScore ?? sub.forcedScore)!} />}
          {(detail.data?.submission.forceRejection ?? sub.forceRejection) && <ForcedRejectionNotice rejection={(detail.data?.submission.forceRejection ?? sub.forceRejection)!} />}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div style={{ display: 'flex', gap: 10, alignItems: 'baseline', flexWrap: 'wrap' }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>适用规则</span>
              <span style={{ fontSize: 12, color: 'var(--fg3)' }}>提交时冻结 · {f.date(snapshot.capturedAt)}</span>
              <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>{snapshot.categoryName}</span>
            </div>
            <ItemGuide name={snapshot.item.name} item={attachClaimableBase(snapshot.item, scheme.data)} embedded />
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>学生自报</span>
              <span style={{ fontSize: 13, color: 'var(--fg)' }}>
                {f.claimText(sub.claim, snapshot.item.scoreRule.type === 'per_unit' ? snapshot.item.scoreRule.unit : undefined)}
              </span>
            </div>
          </div>
          {(sub.filedCategory !== sub.category || sub.filedItemKey !== sub.itemKey) && (
            <div style={{ borderLeft: '2px solid var(--warn)', paddingLeft: 12, fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75 }}>
              原始归类：{sub.filedRuleSnapshot?.categoryName ?? sub.filedCategory} / {sub.filedRuleSnapshot?.item.name ?? sub.filedItemKey}<br />
              当前有效归类：{snapshot.categoryName} / {snapshot.item.name}
            </div>
          )}
          {(detail.data?.classificationHistory?.length ?? 0) > 0 && <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
            <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>归类与分数变更轨迹</span>
            {detail.data!.classificationHistory!.map((entry) => <div key={entry.id} style={{ borderLeft: '2px solid var(--line)', paddingLeft: 12, fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.7 }}>
              {entry.beforeCategory}/{entry.beforeItemKey}（{f.score(entry.beforeScore)}） → {entry.afterCategory}/{entry.afterItemKey}（{f.score(entry.afterScore)}）<br />
              {entry.reason} · {f.dateTime(entry.at)}
            </div>)}
          </div>}

          {/* 提交当时填的东西，按提交页的样子还原：佐证走同一个 EvidenceFiles，
              补充说明走同一套 Markdown 渲染。

              以前这里只有一段 whiteSpace:pre-wrap 的纯文本：Markdown 不解析，
              正文里内嵌的佐证也只剩一串 id，而佐证列表压根没渲染过——detail 里的
              evidence 只被 quotable 拿去解引用了。学生展开只能看到标题和结论，
              看不到自己当初交了什么。 */}
          {detail.isLoading ? (
            <div className="load-bar"><span /></div>
          ) : (
            <>
              {(detail.data?.evidence.length ?? 0) > 0 && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>佐证材料 · {detail.data!.evidence.length} 份</span>
                  {/* 不传 onRemove：这里是快照，只能看不能改 */}
                  <EvidenceFiles items={detail.data!.evidence} />
                </div>
              )}
              {(detail.data?.submission.note ?? sub.note) && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>补充说明</span>
                  <RichText value={detail.data?.submission.note ?? sub.note ?? ''} evidence={quotable} />
                </div>
              )}
            </>
          )}

          {/* 结论有理由、没人名——单盲在这里就是这一条（§5.1） */}
          {(detail.data?.reviews.length ?? 0) > 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>审核结论</span>
              {detail.data!.reviews.map((r, i) => (
                <div key={i} style={{ border: '1px solid var(--line)', padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 7 }}>
                  <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
                    {/* 两条结论必然出自两个人：review 表上 (submission_id, reviewer_id) 唯一，
                        一个人交不出第二条。接口按 created_at 排序下发，所以这个编号稳定，
                        而且与下方轨迹里的「审核人 N · 结论」对得上——不编号的话，
                        学生看到两段话却分不清是两个人各说一次，还是一个人改了口。 */}
                    <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>审核人 {i + 1}</span>
                    <Pill tone={r.decision === 'accepted' ? 'ok' : r.decision === 'adjusted' ? 'warn' : 'bad'}>{DECISION_LABEL[r.decision]}</Pill>
                    <span style={{ fontSize: 14, fontWeight: 600, ...num }}>{f.score(r.score)}</span>
                    <span style={{ ...mono('11px', '0'), marginLeft: 'auto' }}>{f.dateTime(r.at)}</span>
                  </div>
                  <RichText value={r.why} evidence={quotable} />
                </div>
              ))}
            </div>
          )}

          {/* 申诉记录整条搬去了「我的申诉」。这里只留一句"它现在在哪"和一个去处：
              学生在这一页要么想知道分怎么来的，要么想知道申诉走到哪了，后者换个页面回答。 */}
          {openAppeal && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', border: '1px solid var(--warn)', background: 'var(--warnBg)', padding: '13px 15px' }}>
              <span style={{ fontSize: 13, color: 'var(--fg)' }}>
                这一条正在申诉 · 第 {openAppeal.round} 次 · {APPEAL_STATUS_LABEL[openAppeal.status]}
              </span>
              <span style={{ marginLeft: 'auto' }}>
                <Btn onClick={() => go('stuAppeals')}>看申诉进度</Btn>
              </span>
            </div>
          )}

          {/* 轨迹是已经发生过的事实，末尾再补一格"现在到哪儿了"：
              还在走的转圈，正常走完打勾，冲突这种要人来收拾的打感叹号。 */}
          <Timeline
            items={[
              ...trailSteps(detail.data?.trail ?? []),
              { title: sub.forceRejection ? '已强制驳回 · 0 分' : NOW[sub.status].title, at: f.dateTime(sub.updatedAt), state: sub.forceRejection ? 'error' : NOW[sub.status].state },
            ]}
          />

          {(
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', borderTop: '1px solid var(--line2)', paddingTop: 16 }}>
              {sub.forcedScore && !sub.forceRejection ? <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>该项已强制改分终裁，如有疑问请联系班级管理员。</span> : sub.forceRejection ? (
                <span style={{ fontSize: 12.5, color: 'var(--red)' }}>{sub.forceRejection.selfRejected ? '本人主动驳回，不能再申诉。' : '已强制驳回，不能再申诉。'}</span>
              ) : openAppeal ? (
                <Btn onClick={() => go('stuAppeals')}>查看申诉进度</Btn>
              ) : settled ? (
                <>
                  {/* 按钮留在这里，但编辑器不在这里开：跳到「我的申诉」并就地展开这一条。 */}
                  <Btn onClick={() => goAppeal('submission', sub.id)} disabled={!canAppeal || !sub.canAppeal}>
                    {nextRound === 1 ? '发起申诉 · 交回原审核人复评' : '再提一次 · 直接送管理员终裁'}
                  </Btn>
                  {/* 不可申诉的原因不自己推：后端只会因为「班管已终裁」或「上一轮还没结案」拒，
                      前者是终局，后者页面上另有那条在途申诉在说话。 */}
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
                    {!canAppeal
                      ? '申诉能力当前未开放'
                      : resultConfirmed
                        ? '你已经确认仅记录已核对，仍可申诉'
                        : !sub.canAppeal
                          ? '班级管理员对这一条已经下过结论，不再受理申诉'
                          : sub.appealsUsed > 0
                            ? `已申诉 ${sub.appealsUsed} 次 · 再提一次就由班级管理员终裁`
                            : '交回原来的两名审核人再看一遍'}
                  </span>
                </>
              ) : sub.status === 'appealing' || sub.status === 'arbitrating' ? (
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>这条在处理中</span>
              ) : (
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>本条尚未定分，定分后才能申诉</span>
              )}
              {/* 草稿：直接接着写。提交页只肯把草稿读回表单，所以这个入口只给草稿。 */}
              {canEdit && sub.status === 'draft' && (
                <Btn onClick={() => go('stuSubmit', sub.id)}>继续编辑</Btn>
              )}
              {/* 在审的想改，退回草稿而不是删掉重来：内容和佐证都留着，同时这一条
                  立刻从审核队列消失，不会出现"审核人正看着、学生同时在改"。 */}
              {canEdit && sub.status === 'pending' && (
                <Btn
                  onClick={() =>
                    actions.withdraw.mutate(sub.id, {
                      onSuccess: () => say('已退回草稿 · 可以接着改了'),
                      onError: (e) => say(e instanceof ApiError ? e.message : '退回失败'),
                    })
                  }
                >
                  撤回并修改
                </Btn>
              )}
              {canEdit && (
                <Btn
                  danger
                  onClick={() => {
                    if (!window.confirm(`删除「${f.submissionTitle(sub.title)}」？佐证会一并删除，无法恢复。`)) return
                    actions.remove.mutate(sub.id, {
                      onSuccess: () => say('已删除'),
                      onError: (e) => say(e instanceof ApiError ? e.message : '删除失败'),
                    })
                  }}
                >
                  删除
                </Btn>
              )}
              {sub.canSelfForceReject && !sub.forceRejection && (
                <Btn danger onClick={() => setRejecting(true)}>强制驳回本人提交并记 0 分</Btn>
              )}
              {sealed && !sub.forceRejection && <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>已封存，不可修改原材料；审核与申诉仍可进行</span>}
            </div>
          )}
          {rejecting && sub.canSelfForceReject && !sub.forceRejection && <ForceRejectSubmissionForm target={sub} selfReject onClose={() => setRejecting(false)} />}
        </div>
      )}
    </div>
  )
}

/* 审计动作到人话的映射。轨迹直接来自 audit_log，动作名是稳定的机器标识。 */
/* 轨迹只留状态真的变了的那些事。被挡掉的三类都不是进展：
   建草稿是学生自己按下的动作；"你查看了本条"是学生自己刚才的点击；
   "审核人查看"只说明有人打开过，学生据此做不了任何事，而它还会因为审核人
   来回翻页刷成一长串。三者在审计日志里照旧完整保留，班级管理员查得到，
   这里只是不摆到学生面前当进度看。 */
const TRAIL_HIDDEN = new Set(['submission.draft_created', 'submission.read', 'review.task_read'])

/* 两条结论必然出自两个人：review 表上 (submission_id, reviewer_id) 是唯一的
   （review_one_current_per_reviewer），一个人交不了第二条。所以按出现顺序编号
   就是事实，不是猜的——学生看得出"这是另一个人给的结论"，但仍然不知道是谁。

   「审核人查看」编不了号：那条审计只写了动作，没写是谁看的
   （review_handlers.go:170 的 metadata 是 null，trail 也不下发 actor_id）。
   要把查看也归到人头上得后端补，写在 docs/RICHTEXT-PASTE-BACKEND-CONTRACT.md。 */
function trailSteps(trail: { action: string; at: string }[]) {
  let decided = 0
  return trail
    .filter((t) => !TRAIL_HIDDEN.has(t.action))
    .map((t) => {
      if (t.action === 'review.decided') {
        decided += 1
        return { title: `审核人 ${decided} · 结论`, at: f.dateTime(t.at) }
      }
      return { title: TRAIL_LABEL[t.action] ?? t.action, at: f.dateTime(t.at) }
    })
}

/* 末尾那一格：这条现在的处境。异常＝需要有人来收拾的状态（双评谈不拢），
   进行中＝还在别人手上，完成＝分已经定下来了。 */
const NOW: Record<SubmissionStatus, { title: string; state: StepState }> = {
  draft: { title: '草稿 · 待你提交', state: 'active' },
  pending: { title: '审核中', state: 'active' },
  consensus: { title: '合议中', state: 'active' },
  appealing: { title: '申诉处理中', state: 'active' },
  arbitrating: { title: '冲突待仲裁', state: 'error' },
  scored: { title: '已定分', state: 'done' },
  locked: { title: '已锁定', state: 'done' },
}

const TRAIL_LABEL: Record<string, string> = {
  'submission.draft_created': '建草稿',
  'submission.updated': '修改',
  'submission.deleted': '删除',
  'submission.submitted': '提交',
  'submission.read': '你查看了本条',
  'review.task_read': '审核人查看',
  'review.decided': '审核结论',
  'review.revised': '审核人修改结论',
  'submission.arbitrated': '管理员终裁',
  'submission.force_rejected': '强制驳回 · 认定为 0 分',
  'appeal.filed': '发起申诉',
  'appeal.withdrawn': '撤回申诉修改',
  'appeal.rereviewed': '审核人复评',
  'appeal.resolved': '复评一致，本轮结案',
  'appeal.escalated': '升给管理员',
  'appeal.final': '申诉终裁',
  'objection.applied': '小组提案生效',
  'evidence.presigned': '上传佐证',
  'evidence.completed': '佐证上传完成',
  'evidence.deleted': '移除佐证',
  'evidence.url_issued': '佐证被查看',
}

export default function StudentList() {
  const go = useApp((s) => s.go)
  const [tab, setTab] = useState<(typeof TABS)[number]['key']>(() => new URLSearchParams(window.location.search).get('force_rejected') === 'true' ? 'rejected' : 'all')
  const [rejectedPage, setRejectedPage] = useState(1)

  const subs = useSubmissions(tab === 'rejected' ? { forceRejected: true, page: rejectedPage, page_size: 20 } : {})
  const seal = useSeal()
  const win = useWindow()
  /* 申诉整表在这里取一次，按 targetId 分给每一行——
     每行各拉一次会在一页里打出几十个同样的请求。 */
  const appeals = useAppeals()
  /* 只读一眼确认状态给申诉提示用；poll:false 免得把成绩核对页的 8 秒轮询带进本页 */


  const sealed = !!seal.data?.sealed
  const resultConfirmed = false

  /* 本页只讲"已经交出去的东西"。草稿还没提交，它属于「提交材料」里各小项自己的草稿箱，
     摆在这里会让人以为已经交了。封存卡里的草稿提醒由 SealCard 自己取。 */
  const all = (subs.data?.items ?? []).filter((r) => r.status !== 'draft')
  const rows = all.filter((r) =>
    tab === 'all' || tab === 'rejected'
      ? true
      : tab === 'wait'
        ? r.status === 'pending' || r.status === 'consensus'
        : r.status === 'scored' || r.status === 'locked',
  )

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="MY SUBMISSIONS"
        title="我的提交"
        desc="草稿、待审、已定分和申诉"
        side={
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
            <span style={{ fontSize: 26, fontWeight: 600, letterSpacing: '-.04em', ...num }}>{tab === 'rejected' ? subs.data?.total ?? 0 : all.length}</span>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>
              {tab === 'rejected' ? '条强制驳回材料' : `条 · 已定分 ${all.filter((r) => r.status === 'scored' || r.status === 'locked').length}`}
            </span>
          </div>
        }
      />

      <div style={{ paddingBottom: 4 }}>
        <Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={(value) => { setTab(value); setRejectedPage(1); const url = new URL(window.location.href); if (value === 'rejected') url.searchParams.set('force_rejected', 'true'); else url.searchParams.delete('force_rejected'); window.history.replaceState(null, '', url) }} />
      </div>

      <div style={{ paddingTop: 12 }}>
        {subs.isLoading ? (
          <div className="load-bar"><span /></div>
        ) : subs.isError ? (
          <Empty title="材料读取失败" desc={subs.error instanceof ApiError ? subs.error.message : '请稍后重试。'} />
        ) : rows.length === 0 ? (
          <Empty title="这一类下没有条目" desc="换个筛选看看，或者去「提交材料」页加一条。" />
        ) : (
          rows.map((r) => (
            <SubItem
              key={r.id}
              sub={r}
              appeals={(appeals.data?.items ?? [])
                .filter((a) => a.targetType === 'submission' && a.targetId === r.id)
                .sort((a, b) => a.round - b.round)}
              sealed={sealed}
              canAppeal={!!win.data?.capabilities.appeal}
              canEditWindow={!!win.data?.capabilities.edit}
              resultConfirmed={resultConfirmed}
            />
          ))
        )}
      </div>

      {tab === 'rejected' && (subs.data?.total ?? 0) > 20 && <div style={{ display: 'flex', alignItems: 'center', gap: 12, paddingTop: 18 }}>
        <Btn disabled={rejectedPage <= 1 || subs.isFetching} onClick={() => setRejectedPage((page) => page - 1)}>上一页</Btn>
        <span style={{ fontSize: 12.5, color: 'var(--fg2)' }}>第 {rejectedPage} / {Math.ceil((subs.data?.total ?? 0) / 20)} 页 · 共 {subs.data?.total} 条</span>
        <Btn disabled={rejectedPage * 20 >= (subs.data?.total ?? 0) || subs.isFetching} onClick={() => setRejectedPage((page) => page + 1)}>下一页</Btn>
      </div>}

      {/* 封存与结果确认都在「当前成绩」页做，这里只留指路。 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', borderTop: '1px solid var(--line)', marginTop: 34, paddingTop: 16 }}>
        <span style={{ fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>
          封存和确认当前成绩都在「当前成绩」页。
        </span>
        <Btn onClick={() => go('stuResult')}>去「当前成绩」</Btn>
      </div>
    </div>
  )
}
