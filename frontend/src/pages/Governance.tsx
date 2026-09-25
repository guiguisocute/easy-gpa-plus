import { useState } from 'react'
import {
  PageHead,
  Btn,
  Empty,
  BackLink,
  Note,
  Pill,
  Seg,
  Stat,
  StatGrid,
  Sub,
  Table,
  THead,
  TRow,
  TextBtn,
} from '@/components/ui'
import {
  useGovernance,
  useGovernanceAction,
  useGovernanceProposals,
  type GovernanceState,
  type ProposalView,
} from '@/api/governance'
import { userErrorMessage } from '@/api/errorMessages'
import { useApp } from '@/stores/app'
import { COLLECTIVE_NAV, type View } from '@/lib/nav'
import {
  governanceDate as date,
  governanceQueue,
  isFinished,
  isReview,
  statusName,
  statusTone,
} from '@/lib/governanceView'
import Bonus from './admin/Bonus'
import Gpa from './admin/Gpa'
import Objections from './group/Objections'
import GovernanceSettlement from './GovernanceSettlement'
import GovernanceProposalForm from './GovernanceProposalForm'
import { CaseView, ProposalCard } from './GovernanceDetails'
import './governance.css'

/* 这几页复用的是普通模式那一份，各自带着自己的 PageHead，
   共治外壳不能再顶一个上去——顶上去就是两个标题叠在一起。 */
const OWN_PAGE_HEAD: View[] = ['govBonus', 'govObjections', 'govGpa']

const affairs: { view: View; title: string; desc: string }[] = [
  {
    view: 'govBonus',
    title: '共同加分',
    desc: '为同一事项发起单人或多人加分提案。',
  },
  {
    view: 'govObjections',
    title: '扣分与成绩异议',
    desc: '提交事实与依据，由随机小组评审。',
  },
  {
    view: 'govGpa',
    title: '专业分核验',
    desc: '全班成绩与原件一同核验，发起人回避。',
  },
  {
    view: 'govTimeline',
    title: '时间窗口',
    desc: '调整材料与封锁时间，须达到重大事项门槛。',
  },
  {
    view: 'govExport',
    title: '导出授权',
    desc: '按用途申请一次文件领取授权。',
  },
  {
    view: 'govSettle',
    title: '结算检查',
    desc: '检查业务完成情况，通过后生成正式结果。',
  },
  {
    view: 'stuClassPenalties',
    title: '匿名举报',
    desc: '查看公开轨迹并提交有依据的举报。',
  },
]

function useRun() {
  const action = useGovernanceAction(),
    say = useApp((s) => s.say)
  async function run(path: string, body?: unknown, method: 'put' | 'post' = 'post') {
    try {
      const result = await action.mutateAsync({ path, body, method })
      say(result.notice ?? '已保存')
      return true
    } catch (error) {
      say(userErrorMessage(error))
      return false
    }
  }
  return { run, pending: action.isPending }
}

export default function Governance() {
  const state = useGovernance(),
    view = useApp((s) => s.view),
    go = useApp((s) => s.go)
  const lists = ['govHome', 'govProposals', 'govReviews'].includes(view)
  const proposals = useGovernanceProposals(
    lists && state.data?.config.mode !== 'centralized' && !!state.data,
  )
  const { run, pending } = useRun()
  const [selected, setSelected] = useState<string | null>(() =>
    new URLSearchParams(location.search).get('proposal'),
  )
  const [filter, setFilter] = useState('active')
  function select(id: string | null) {
    setSelected(id)
    const url = new URL(location.href)
    if (id) url.searchParams.set('proposal', id)
    else url.searchParams.delete('proposal')
    history.replaceState(null, '', url)
  }
  if (!state.data)
    return (
      <Empty
        title="正在读取班级共治"
        desc={state.error ? userErrorMessage(state.error) : undefined}
      />
    )
  const s = state.data,
    items = proposals.data?.items ?? []
  const joined = !!s.mine.joinedAt && !s.mine.leftAt
  const reviews = view === 'govReviews'
  const current = COLLECTIVE_NAV.find((n) => n.view === view) ?? COLLECTIVE_NAV[0]
  if (selected && reviews)
    return (
      <CaseView
        key={selected}
        id={selected}
        back={() => select(null)}
        queue={governanceQueue(items, true, 'all')}
        onSelect={select}
      />
    )
  const proposal = items.find((i) => i.proposal.id === selected)
  return (
    <div className="governance-page">
      {current.under === 'govOperations' && (
        <BackLink label="返回共同事务" onClick={() => go('govOperations')} />
      )}
      {view === 'govNewProposal' && (
        <BackLink label="返回提案与表决" onClick={() => go('govProposals')} />
      )}
      {!OWN_PAGE_HEAD.includes(view) && (
        <PageHead
          en={current.en}
          title={current.label}
          desc={current.desc}
          side={
            lists ? (
              <Btn
                onClick={() => {
                  void state.refetch()
                  void proposals.refetch()
                }}
              >
                刷新
              </Btn>
            ) : undefined
          }
        />
      )}
      {s.config.mode === 'enrolling' && (
        <Note tone="warn">
          共治招募中 ·{' '}
          {s.config.enrollmentCloseAt ? `${date(s.config.enrollmentCloseAt)}结束公示` : '准备启动'}
          。<TextBtn onClick={() => go('govParticipation')}>查看参与与启动规则 →</TextBtn>
        </Note>
      )}
      {view === 'govHome' && (
        <>
          <div style={{ marginBottom: 24 }}>
            <Note tone="warn">
              共治模式为测试版，尚未经过真实班级的生产验证，规则与界面可能调整。
            </Note>
          </div>
          <Sub
            title="先处理与你有关的事"
            note="共同参与，各自独立判断"
            actions={
              <Btn onClick={() => go('govParticipation')}>
                {joined ? '管理我的参与' : '了解并加入共治'}
              </Btn>
            }
          />
          <Note>
            {joined
              ? '你已加入本周期共治。提交材料、参与投票、承担评审相互独立。'
              : '你仍可提交材料和查看成绩；自愿加入后，可参与后续提案和随机评审。'}
          </Note>
          <StatGrid cols={3}>
            {[
              {
                view: 'govReviews' as View,
                label: '待我评审',
                count: items.filter(
                  (i) =>
                    isReview(i) && i.eligible && !i.mySubmitted && !i.myChoice && !isFinished(i),
                ).length,
                desc: '分配给我的独立评审',
              },
              {
                view: 'govProposals' as View,
                label: '可参与的提案',
                count: items.filter(
                  (i) =>
                    !isReview(i) && i.eligible && !i.mySubmitted && !i.myChoice && !isFinished(i),
                ).length,
                desc: '讨论公示与待表决的事项',
              },
              {
                view: 'govOperations' as View,
                label: '共同事务',
                count: '→',
                desc: '加分、核验、时间与结算',
              },
            ].map((card) => (
              <div key={card.view}>
                <Stat
                  en={card.label}
                  value={String(card.count)}
                  note={card.desc}
                  tone="var(--red)"
                />
                <div style={{ marginTop: 14 }}>
                  <TextBtn onClick={() => go(card.view)}>进入{card.label} →</TextBtn>
                </div>
              </div>
            ))}
          </StatGrid>
          {proposals.error && (
            <div role="alert">
              <Note tone="bad">{userErrorMessage(proposals.error)}</Note>
            </div>
          )}
          <section className="gov-section">
            <Sub
              title="正在进行的共同提案"
              actions={<Btn onClick={() => go('govProposals')}>查看全部</Btn>}
            />
            <ProposalRows
              items={governanceQueue(items, false, 'active').slice(0, 3)}
              open={(id) => {
                go('govProposals')
                const url = new URL(location.href)
                url.searchParams.set('proposal', id)
                history.replaceState(null, '', url)
              }}
              pending={proposals.isPending}
            />
          </section>
          <Note>
            在册 {s.counts.roster} · 已注册 {s.counts.registered} · 已交材料 {s.counts.submitted} ·
            主动加入 {s.counts.enrolled}。未注册与未交材料的成员继续保留成绩、排名和结算资格。
          </Note>
        </>
      )}
      {['govProposals', 'govReviews'].includes(view) && (
        <>
          {selected && !reviews ? (
            <>
              <BackLink label="返回提案列表" onClick={() => select(null)} />
              {proposal ? (
                <ProposalCard item={proposal} pending={pending} run={run} openCase={select} />
              ) : (
                <Empty title={proposals.isPending ? '正在读取提案' : '提案不存在或当前不可访问'} />
              )}
            </>
          ) : (
            <>
              <div className="gov-list-toolbar">
                <div role="group" aria-label={reviews ? '评审筛选' : '提案筛选'}>
                  <Seg
                    value={filter}
                    onChange={setFilter}
                    items={[
                      { key: 'active', label: '进行中' },
                      { key: 'mine', label: reviews ? '待我评审' : '待我参与' },
                      { key: 'done', label: '已结束' },
                      { key: 'all', label: '全部' },
                    ]}
                  />
                </div>
                {!reviews && s.config.mode === 'collective' && (
                  <Btn primary disabled={!joined} onClick={() => go('govNewProposal')}>
                    发起提案
                  </Btn>
                )}
              </div>
              <p className="gov-list-hint">
                {reviews
                  ? '这里列出分配给你或与你有关的案件。进入详情后查看佐证，独立提交意见。'
                  : '先读提案，再进入详情讨论和表决；每项提案的名单在发布时冻结。'}
              </p>
              {proposals.error && (
                <div role="alert">
                  <Note tone="bad">{userErrorMessage(proposals.error)}</Note>
                </div>
              )}
              <ProposalRows
                items={governanceQueue(items, reviews, filter)}
                open={select}
                pending={proposals.isPending}
              />
            </>
          )}
        </>
      )}
      {view === 'govParticipation' && <Participation state={s} run={run} pending={pending} />}
      {view === 'govOperations' && (
        <>
          <p>选择一件事开始。共同提案进入「提案与表决」，需要评审的异议进入随机分配流程。</p>
          <Table cols="170px minmax(260px,1fr) 90px">
            <THead cells={['共同事务', '办理说明', '操作']} />
            {affairs.map((a) => (
              <TRow
                key={a.view}
                cells={[
                  a.title,
                  a.desc,
                  <Btn
                    key="open"
                    disabled={s.config.mode !== 'collective'}
                    onClick={() => go(a.view)}
                  >
                    进入
                  </Btn>,
                ]}
              />
            ))}
          </Table>
        </>
      )}
      {view === 'govBonus' && <Bonus collective />}
      {view === 'govObjections' && <Objections collective />}
      {view === 'govGpa' && <Gpa collective />}
      {(view === 'govSettle' || view === 'govExport') && (
        <GovernanceSettlement kind={view === 'govSettle' ? 'settle' : 'export'} joined={joined} />
      )}
      {(view === 'govNewProposal' || view === 'govTimeline') &&
        (joined ? (
          <GovernanceProposalForm timeline={view === 'govTimeline'} />
        ) : (
          <Empty title="自愿加入后可发起提案" />
        ))}
    </div>
  )
}

function ProposalRows({
  items,
  open,
  pending,
}: {
  items: ProposalView[]
  open: (id: string) => void
  pending: boolean
}) {
  if (!items.length) return <Empty title={pending ? '正在读取事项' : '暂时没有符合条件的事项'} />
  return (
    <Table cols="minmax(230px,1fr) 120px 150px 150px">
      <THead cells={['事项', '已参与 / 名单', '截止时间', '状态']} />
      {items.map((item) => (
        <TRow
          key={item.proposal.id}
          label={item.proposal.title}
          onClick={() => open(item.proposal.id)}
          cells={[
            <div key="title">
              <div style={{ color: 'var(--fg)', fontWeight: 500 }}>{item.proposal.title}</div>
              <div style={{ fontSize: 12, color: 'var(--fg3)', marginTop: 5 }}>
                {isReview(item) ? '独立评审' : '共同提案'}
              </div>
            </div>,
            `${item.participated} / ${item.proposal.electorateCount}`,
            date(item.proposal.closesAt),
            <Pill key="status" tone={statusTone[item.proposal.status]}>
              {statusName[item.proposal.status] ?? item.proposal.status}
            </Pill>,
          ]}
        />
      ))}
    </Table>
  )
}

function Participation({
  state: s,
  run,
  pending,
}: {
  state: GovernanceState
  run: (path: string, body?: unknown, method?: 'put' | 'post') => Promise<boolean>
  pending: boolean
}) {
  const joined = !!s.mine.joinedAt && !s.mine.leftAt,
    [reviewer, setReviewer] = useState(false),
    go = useApp((a) => a.go)
  return (
    <>
      <section className="gov-section">
        <Sub title="我的参与方式" />
        <p>不交材料也能参与投票和评审。不参与治理不会扣分；加入满 24 小时后进入新提案名单。</p>
        {joined ? (
          <>
            <p>你已主动加入{s.mine.reviewer ? '，并愿意承担随机评审。' : '。'}</p>
            <div className="gov-actions">
              <Btn
                disabled={pending}
                onClick={() =>
                  void run('/membership', { join: true, reviewer: !s.mine.reviewer }, 'put')
                }
              >
                {s.mine.reviewer ? '暂停接收新评审' : '加入随机评审池'}
              </Btn>
              <Btn
                disabled={pending}
                onClick={() => void run('/membership', { join: false }, 'put')}
              >
                退出后续共治
              </Btn>
            </div>
          </>
        ) : (
          <>
            <label className="gov-check">
              <input
                type="checkbox"
                checked={reviewer}
                onChange={(e) => setReviewer(e.target.checked)}
              />
              同时愿意承担随机评审
            </label>
            <div className="gov-actions">
              <Btn
                primary
                disabled={pending}
                onClick={() => void run('/membership', { join: true, reviewer }, 'put')}
              >
                自愿加入本周期共治
              </Btn>
            </div>
          </>
        )}
        <p>现在加入或退出不会改变已有投票名单，已分配的评审仍须处理或等待补位。</p>
      </section>
      <section className="gov-section">
        <Sub title="本周期章程" />
        <StatGrid cols={3}>
          <Stat
            en="日常共同事项"
            value={String(s.ordinaryRequired)}
            unit="票"
            note={`当前 ${s.counts.electorate} 名生效参与者。发布时冻结名单，沉默不算同意。`}
          />
          <Stat
            en="重大事项"
            value={String(s.protectedRequired)}
            unit="票"
            note={`还须达到全班 ${s.counts.roster} 名在册成员三分之一的底线，人数不足不降低门槛。`}
          />
          <Stat
            en="随机评审池"
            value={String(s.counts.reviewers)}
            unit="人"
            note={
              s.capacityReason ||
              ((s.config.profile ?? s.suggestedProfile.name) === 'compact'
                ? '精简章程：最多三人初评，另组三人独立申诉。'
                : '标准章程：最多五人初评，另组五人独立申诉。')
            }
          />
        </StatGrid>
      </section>
      {s.config.mode === 'enrolling' && (
        <section className="gov-section">
          <Sub title="准备启动共治" />
          <p>招募至少 7 天，达到评审容量后再对章程投票，通过后公示 24 小时生效。</p>
          {s.canConfigure && (
            <>
              <p>
                初始化负责人请先完成本周期的评分方案、名单与资料准备。其他成员使用同一工作台参与招募。
              </p>
              <div className="gov-actions">
                {[
                  ['govSetupRoster', '准备名单'],
                  ['govSetupScheme', '评分方案'],
                  ['govSetupFeatures', '业务开关'],
                  ['govSetupFiles', '班级资料'],
                ].map(([v, label]) => (
                  <Btn key={v} onClick={() => go(v as View)}>
                    {label}
                  </Btn>
                ))}
              </div>
            </>
          )}
          <div className="gov-actions">
            <Btn
              disabled={!joined || pending || !!s.capacityReason}
              onClick={() =>
                void run('/proposals', {
                  requestId: crypto.randomUUID(),
                  kind: 'activate',
                  action: 'activate',
                  title: '启动本周期班级共治',
                  body: '采用公开的主动参与、本人回避、独立申诉和冻结名单表决规则。',
                  payload: {},
                })
              }
            >
              发起启动表决
            </Btn>
          </div>
        </section>
      )}
    </>
  )
}
