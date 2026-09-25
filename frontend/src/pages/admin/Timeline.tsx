/* 班级管理员 · 时间窗口。

   材料与公示按各自起止时间开放；业务能力开关在独立的「功能开关」页。
   页面上因此不存在"推进到下一阶段"这类按钮——那个动作在这套系统里不存在。

   时间线不再是方案的一部分：窗口、业务开关、评优比例存在 class_timeline 上，
   改它们不会产生新的方案版本。"把截止延后三天"和"发布了一版新的评优方案"
   本来就不是一回事。

   三个时间点分工不同，页面上必须让人一眼看出区别：
     开放     窗口打开
     封存截止 不再收新材料、全班自动封存；审核、申诉、异议、仲裁照常
     全系统封锁 之后谁都不能改任何东西，只剩查看与导出
   公示窗口独立开放已公开佐证的只读访问，不解除写入限制。 */

import { useEffect, useState } from 'react'
import { Btn, Empty, Note, PageHead, Split, SplitCol, Stat, StatGrid, Sub, TextBtn } from '@/components/ui'
import { TimeField } from '@/components/TimeField'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import type { AwardTier } from '@/api/types'
import { useGate, useReviewSLA, useReviewSLAActions, useSchemeActions, useStats, useWindow } from '@/api/queries'

/** ISO → datetime-local 需要的本地时间字符串。直接切 ISO 会丢掉时区偏移。 */
function toLocalInput(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const labelStyle = { fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }
/** 时间与百分比都是数字，统一走等宽——和 SLA 那几个输入框同一套。 */
const numFieldStyle = { ...fieldStyle, font: "400 14px/1.4 'JetBrains Mono',monospace" }
export default function AdminTimeline() {
  const say = useApp((s) => s.say)
  const win = useWindow()
  const gate = useGate()
  const stats = useStats()
  const { setTimeline } = useSchemeActions()
	const sla = useReviewSLA()
	const { update: updateSLA } = useReviewSLAActions()
	const [itemHours, setItemHours] = useState(24)

  const [open, setOpen] = useState('')
  const [close, setClose] = useState('')
  const [lockdown, setLockdown] = useState('')
  const [publicityOpen, setPublicityOpen] = useState('')
  const [publicityClose, setPublicityClose] = useState('')
  const [honorRollTopPercent, setHonorRollTopPercent] = useState<number | null>(null)
  const [awards, setAwards] = useState<AwardTier[] | null>(null)
  const [collegeName, setCollegeName] = useState('')
  const [enrollmentClass, setEnrollmentClass] = useState('')
  const [academicYear, setAcademicYear] = useState('')

  /* 服务端是唯一事实来源；本地输入框只在拿到数据后初始化一次。

     用一个显式的 hydrated 标记，而不是 `v || 服务端值` 那种写法：封锁时间和
     档位列表都可以合法地为空，靠"值是不是空"来判断有没有初始化过，会让管理员
     刚清空的封锁时间在下一次 refetch 时自己长回来。 */
  const [hydrated, setHydrated] = useState(false)
	useEffect(() => {
    if (!win.data || hydrated) return
    setOpen(toLocalInput(win.data.window.open))
    setClose(toLocalInput(win.data.window.close))
    setLockdown(toLocalInput(win.data.window.lockdown))
    setPublicityOpen(toLocalInput(win.data.window.publicity?.open))
    setPublicityClose(toLocalInput(win.data.window.publicity?.close))
    setHonorRollTopPercent(win.data.honorRoll.topPercent)
    setAwards(win.data.honorRoll.awards ?? [])
    setCollegeName(win.data.collegeName ?? '')
    setEnrollmentClass(win.data.enrollmentClass ?? '')
    setAcademicYear(win.data.academicYear ?? '')
    setHydrated(true)
	}, [win.data, hydrated])
	useEffect(() => {
		if (!sla.data) return
		setItemHours(sla.data.itemHours)
	}, [sla.data])

  if (win.isError) {
    return <Empty title="还没有已发布的方案" desc="先去「方案编辑器」发一份方案，才能设置时间窗口。" />
  }
  if (win.isLoading || !win.data) return <div className="load-bar"><span /></div>

  const s = stats.data
  const gateOpen = !!gate.data?.open
  const left = f.daysUntil(win.data.window.close)
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')

  /* 已经封锁时，页面上除了时间线本身，其余控件全部禁用——这与后端中间件的
     白名单是同一条规则的两种表达。禁用是为了别让人白填一遍表单再吃 409。 */
  const lockedAt = win.data.window.lockdown
  const locked = !!lockedAt && new Date(lockedAt) <= new Date(win.data.serverNow)
  const tiers = awards ?? []
  const tierTotal = tiers.reduce((sum, tier) => sum + (Number.isFinite(tier.topPercent) ? tier.topPercent : 0), 0)

  const editTier = (index: number, patch: Partial<AwardTier>) =>
    setAwards(tiers.map((tier, i) => (i === index ? { ...tier, ...patch } : tier)))

  /* 顺序是有含义的：名额按列表顺序累计，第一档拿最前面的名次。所以必须能调
     顺序——否则想在最前面加一个「特等」，就只能把每一行的名字重打一遍。 */
  const moveTier = (index: number, delta: number) => {
    const target = index + delta
    if (target < 0 || target >= tiers.length) return
    const next = [...tiers]
    ;[next[index], next[target]] = [next[target], next[index]]
    setAwards(next)
  }

  const saveTimeline = () => {
    if (!open || !close) return say('请先填写完整的开放与封存截止时间')
    const o = new Date(open)
    const c = new Date(close)
    if (!(o < c)) return say('开放时间必须早于封存截止时间')
    let publicity: { open: string; close: string } | null = null
    if (publicityOpen || publicityClose) {
      const start = new Date(publicityOpen), end = new Date(publicityClose)
      if (!publicityOpen || !publicityClose || !(start < end)) return say('请完整填写公示起止时间，且开始时间须早于结束时间')
      publicity = { open: start.toISOString(), close: end.toISOString() }
    }
    let lockdownISO: string | null = null
    if (lockdown) {
      const l = new Date(lockdown)
      if (Number.isNaN(l.getTime())) return say('全系统封锁时间不是有效时间')
      if (l < c) return say('全系统封锁时间不能早于封存截止时间')
      lockdownISO = l.toISOString()
    }
    if (honorRollTopPercent === null || !Number.isInteger(honorRollTopPercent) || honorRollTopPercent < 0 || honorRollTopPercent > 100) return say('三好学生比例必须是 0 到 100 的整数')
    for (const tier of tiers) {
      if (!tier.name.trim()) return say('每一档评优都要有名称')
      if (!Number.isInteger(tier.topPercent) || tier.topPercent < 0 || tier.topPercent > 100) return say(`「${tier.name}」的名额占比必须是 0 到 100 的整数`)
    }
    if (new Set(tiers.map((tier) => tier.name.trim())).size !== tiers.length) return say('评优档位不能重名')
    if (tierTotal > 100) return say(`各档名额占比累计 ${tierTotal}%，超过了全班`)
    setTimeline.mutate(
      {
        open: o.toISOString(),
        close: c.toISOString(),
        lockdown: lockdownISO,
        publicity,
        honorRollTopPercent,
        awards: tiers.map((tier) => ({ name: tier.name.trim(), topPercent: tier.topPercent })),
        collegeName: collegeName.trim(),
        enrollmentClass: enrollmentClass.trim(),
        academicYear: academicYear.trim(),
      },
      { onSuccess: () => say('时间窗口已保存，并记进了审计。方案版本没变'), onError: fail },
    )
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="TIMELINE"
        title="时间窗口"
        desc="材料、公示、审核时限和评优"
        side={
          <>
            <Btn
              onClick={() => {
                const c = new Date(win.data!.window.close)
                c.setDate(c.getDate() + 3)
                setClose(toLocalInput(c.toISOString()))
                say('已填入 +3 天，点「保存设置」生效')
              }}
            >
              延长 3 天
            </Btn>
            <Btn primary disabled={setTimeline.isPending} onClick={saveTimeline}>
              {setTimeline.isPending ? '保存中…' : '保存设置'}
            </Btn>
          </>
        }
      />

      {locked && (
        <div style={{ border: '1px solid var(--red)', background: 'var(--warnBg)', padding: '13px 15px', margin: '0 0 18px', fontSize: 12.5, lineHeight: 1.8, color: 'var(--fg2)', textWrap: 'pretty' }}>
          <strong style={{ color: 'var(--fg)' }}>本学期已于 {f.dateTime(lockedAt)} 全系统封锁。</strong>
          {' '}全班——包括你自己——都不能再提交、审核、申诉或仲裁了，只能看历史资料和导出。
          把下面的封锁时间往后挪，保存，就解开了。
        </div>
      )}

      <StatGrid cols={4}>
        <Stat en="窗口开放" value={f.dayMonth(win.data.window.open)} note={`至 ${f.date(win.data.window.close)}`} />
        <Stat en="剩余天数" value={String(left)} unit="天" note="到期系统自动封存全部未封存账号" tone={left <= 3 ? 'var(--red)' : undefined} />
        <Stat
          en="已封存"
          value={s ? `${s.sealed} / ${s.users}` : '—'}
          note="不能代学生封存，只能提醒或解封"
          pct={s ? f.pct(s.sealed, s.users) : undefined}
        />
        <Stat
          en="结算闸门"
          value={gateOpen ? '已开启' : '未开启'}
          note={gateOpen ? '导出与算分已放行' : '条件未满足，导出页只能预览'}
          tone={gateOpen ? 'var(--ok)' : 'var(--fg3)'}
        />
      </StatGrid>

		<div style={{ padding: '24px 0 6px' }}>
			<Sub title="审核时限" note="任务超时会提醒负责人；成绩确认由学生自愿操作，不影响结算" />
			<div style={{ display: 'flex', gap: 12, alignItems: 'end', flexWrap: 'wrap', paddingTop: 12 }}>
				<label style={{ display: 'flex', flexDirection: 'column', gap: 6, fontSize: 12.5, color: 'var(--fg2)' }}>普通单项（小时）<input type="number" min={1} max={168} value={itemHours} onChange={(e) => setItemHours(Number(e.target.value))} style={{ ...fieldStyle, width: 120 }} /></label>
				<Btn primary disabled={locked || updateSLA.isPending} onClick={() => updateSLA.mutate({ itemHours }, { onSuccess: () => say('审核时限已更新'), onError: fail })}>{updateSLA.isPending ? '保存中…' : '保存时限'}</Btn>
			</div>
		</div>

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '28px 0' }}>
            <Sub title="材料时间窗口" note={`当前方案 ${win.data.schemeVersion}`} />
            <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '20px 20px' }}>
              <TimeField
                label="开放时间"
                value={open}
                onChange={setOpen}
                hint="这之前学生看不到提交入口。"
              />
              <TimeField
                label="封存截止时间"
                value={close}
                onChange={setClose}
                hint="到点停收新材料，全班自动封存。"
              />
              <TimeField
                label="全系统封锁时间"
                value={lockdown}
                onChange={setLockdown}
                min={close || undefined}
                hint="到点后只能看和导出。不能早于封存截止。"
                extra={
                  lockdown
                    ? <TextBtn onClick={() => setLockdown('')}>清除</TextBtn>
                    : <span style={{ fontSize: 12, color: 'var(--fg3)' }}>可不设</span>
                }
              />
            </div>
            <div style={{ marginTop: 18 }}>
              <Note>
                过了封存截止时间，还没确认提交的学生会被自动封存。
              </Note>
            </div>
          </div>

          <section aria-label="公示窗口设置" style={{ paddingBottom: 28 }}>
            <Sub title="公示窗口" note="选填 · 同时留空则不开放佐证原件"
              actions={(publicityOpen || publicityClose) && <TextBtn onClick={() => { setPublicityOpen(''); setPublicityClose('') }}>清除公示时间</TextBtn>} />
            <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
              <TimeField label="公示开始时间" value={publicityOpen} onChange={setPublicityOpen} hint="到时学生可在举报台看佐证原件。" />
              <TimeField label="公示结束时间" value={publicityClose} onChange={setPublicityClose} min={publicityOpen || undefined} hint="到时关掉原件查看。" />
            </div>
            <div style={{ marginTop: 14 }}><Note>只公示已定分条目的原件，和已公开理由里的附件。不开放举报。封锁后仍按设定时间公示。</Note></div>
          </section>
        </SplitCol>
        <SplitCol>

          <div style={{ padding: '0 0 28px' }}>
            <Sub title="评优档位" note="三好看四项各自的名次，奖学金看总分名次，两套线各算各的" />
            <label style={{ display: 'flex', flexDirection: 'column', gap: 7, paddingTop: 12, maxWidth: 320 }}>
              <span style={labelStyle}>三好学生比例（%）</span>
              <input
                className="hv-bfg"
                type="number"
                min={0}
                max={100}
                step={1}
                value={honorRollTopPercent ?? ''}
                onChange={(e) => setHonorRollTopPercent(Number.isNaN(e.target.valueAsNumber) ? null : e.target.valueAsNumber)}
                style={numFieldStyle}
              />
              <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.7, textWrap: 'pretty' }}>
                四项名次都进这条线才算三好。总分再高，掉一项也不算。
              </span>
            </label>

            {/* 每档填的是本档自己的名额占比，不是累计值——右边实时显示累计，
                免得管理员按"前 15%"的口径去填第三档。 */}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10, paddingTop: 18 }}>
              {tiers.map((tier, index) => (
                <div key={index} style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
                  <span style={{ ...mono('11px', '.06em'), width: 28, flex: 'none' }}>{index + 1}</span>
                  <input
                    className="hv-bfg"
                    value={tier.name}
                    placeholder="档位名称"
                    onChange={(e) => editTier(index, { name: e.target.value })}
                    style={{ ...fieldStyle, flex: 1, minWidth: 160 }}
                  />
                  <input
                    className="hv-bfg"
                    type="number"
                    min={0}
                    max={100}
                    step={1}
                    value={Number.isFinite(tier.topPercent) ? tier.topPercent : ''}
                    onChange={(e) => editTier(index, { topPercent: e.target.valueAsNumber })}
                    style={{ ...numFieldStyle, width: 88 }}
                  />
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)', width: 24 }}>%</span>
                  <span style={{ display: 'flex', gap: 8 }}>
                    <TextBtn disabled={index === 0} onClick={() => moveTier(index, -1)}>上移</TextBtn>
                    <TextBtn disabled={index === tiers.length - 1} onClick={() => moveTier(index, 1)}>下移</TextBtn>
                    <TextBtn onClick={() => setAwards(tiers.filter((_, i) => i !== index))}>删除</TextBtn>
                  </span>
                </div>
              ))}
              {tiers.length === 0 && (
                <div style={{ fontSize: 12.5, color: 'var(--fg3)' }}>一个档位都没设，结算后不会标出奖学金候选人。</div>
              )}
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', marginTop: 14 }}>
              <Btn disabled={tiers.length >= 8} onClick={() => setAwards([...tiers, { name: '', topPercent: 0 }])}>
                添加一档
              </Btn>
              <span style={{ fontSize: 12.5, color: tierTotal > 100 ? 'var(--red)' : 'var(--fg3)' }}>
                累计名额 {tierTotal}%
              </span>
            </div>
            {/* 名额是每档各自四舍五入之后相加的，不是先合成一个累计百分比再取
                一次整。学院通知明写"总获奖人数为一、二、三等相加，不采用总人数
                ×30%"，这两种算法在不少班级人数下差一个人。 */}
            <div style={{ marginTop: 12 }}>
              <Note>每档名额 = 班级人数 × 本档占比，各自四舍五入。同分先看专业素质分，另一人顺延，不超员。</Note>
            </div>
          </div>

          {/* 学院报表要的身份信息。放在这里而不是方案编辑器里：它们和窗口一样
              是这个班这学期的运行期设置，不是评分规则，改它们不该产生新的方案
              版本。年级与专业由教务班级名解析，不用另填。 */}
          <div style={{ padding: '0 0 28px' }}>
            <Sub title="学院报表身份信息" note="只在导出学院附件时用到，不影响任何评分和排名" />
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14, paddingTop: 12, maxWidth: 420 }}>
              <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
                <span style={labelStyle}>评定学年</span>
                <input className="hv-bfg" value={academicYear} placeholder="2025-2026" onChange={(e) => setAcademicYear(e.target.value)} style={fieldStyle} />
                <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.7 }}>这次评的是哪个学年，比如 2026 年秋评 2025-2026 学年。</span>
              </label>
              <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
                <span style={labelStyle}>学院全称</span>
                <input
                  className="hv-bfg"
                  value={collegeName}
                  placeholder="示例学院"
                  onChange={(e) => setCollegeName(e.target.value)}
                  style={fieldStyle}
                />
              </label>
              <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
                <span style={labelStyle}>教务在线班级名</span>
                <input
                  className="hv-bfg"
                  value={enrollmentClass}
                  placeholder="24级计算机科学与技术2班"
                  onChange={(e) => setEnrollmentClass(e.target.value)}
                  style={fieldStyle}
                />
                <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.7, textWrap: 'pretty' }}>
                  填教务在线上的全名，学院附件要求一字不差。年级和专业从这个名字里读。
                </span>
              </label>
            </div>
          </div>
        </SplitCol>


      </Split>

    </div>
  )
}
