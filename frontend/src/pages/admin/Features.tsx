import { useState } from 'react'
import { useGate, useSchemeActions, useSettlementActions, useWindow } from '@/api/queries'
import { ApiError } from '@/api/client'
import { Btn, Empty, Note, PageHead, Pill, Sub, Toggle } from '@/components/ui'
import { mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'

const CAPS: { key: string; name: string; desc: string }[] = [
  { key: 'submit', name: '提交材料', desc: '学生添新条目。关掉之后，已有的草稿还能看' },
  { key: 'edit', name: '修改与补交', desc: '未定分条目的改动与佐证补传' },
  { key: 'appeal', name: '申诉', desc: '对已经定分的条目、基础分和扣分提申诉' },
  { key: 'review', name: '双评审核', desc: '综测小组提交结论、录入基础项' },
  { key: 'arbitrate', name: '仲裁与终裁', desc: '管理员处理冲突与申诉终裁' },
  { key: 'studentReport', name: '学生匿名举报', desc: '全班扣分对学生公开，可以匿名举报。关掉只是不收新的' },
  { key: 'export', name: '导出与算分', desc: '由结算闸门控制，不能在这里开' },
]

export default function AdminFeatures() {
  const win = useWindow()
  const gate = useGate()
  const { setCapability } = useSchemeActions()
  const { force } = useSettlementActions()
  const say = useApp(s => s.say)
  const [reason, setReason] = useState('')
  if (win.isError) return <Empty title="功能开关读取失败" desc="请确认已发布方案并刷新重试。" />
  if (!win.data) return <div className="load-bar"><span /></div>
  const caps = win.data.capabilities
  const gateOpen = !!gate.data?.open
  const locked = !!win.data.window.lockdown && Date.parse(win.data.window.lockdown) <= Date.parse(win.data.serverNow)
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')
  const capOn = (key: string): boolean => key === 'export' ? caps.export.on : !!caps[key as 'submit' | 'edit' | 'appeal' | 'review' | 'arbitrate' | 'studentReport']
  return <>
    <PageHead en="FEATURE SWITCHES" title="功能开关" desc="业务功能和结算闸门" />
    {locked && <Note tone="warn">本班已全系统封锁，功能开关当前只读。需要恢复操作时，请先在「时间窗口」调整封锁时间。</Note>}
      <div style={{ padding: '10px 0 0' }}>
        <Sub title="业务开关" />
        {CAPS.map((c) => {
          const on = capOn(c.key)
          const managed = c.key === 'export'
          return (
            <div key={c.key} style={{ display: 'flex', alignItems: 'center', gap: 16, borderTop: '1px solid var(--line2)', padding: '15px 0', flexWrap: 'wrap' }}>
              <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 200, flex: 1 }}>
                <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>{c.name}</span>
                <span style={{ fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>{c.desc}</span>
              </span>
              <span style={{ ...mono('11.5px', '0'), width: 130, flex: 'none', textAlign: 'right' }}>
                {managed ? '随闸门' : on ? '开放中' : '已关闭'}
              </span>
              <Toggle
                on={managed ? gateOpen : on}
                locked={managed || locked || setCapability.isPending}
                onClick={() => {
                  if (managed || locked || setCapability.isPending) return
                  setCapability.mutate(
					{ key: c.key, on: !on },
                    { onSuccess: () => say(`${c.name} 已${on ? '关闭' : '开启'} · 已保存并写入审计`), onError: fail },
                  )
                }}
              />
            </div>
          )
        })}
        <div style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.8, marginTop: 14, maxWidth: 720, textWrap: 'pretty' }}>
          专业素质分默认开放。导出随结算闸门走。
        </div>
      </div>

          <div style={{ padding: '28px 0' }}>
            <Sub title="结算闸门" note="条件全满足则自动开启" />
            {(gate.data?.conditions ?? []).map((g) => (
              <div key={g.key} style={{ display: 'flex', alignItems: 'flex-start', gap: 12, borderTop: '1px solid var(--line2)', padding: '13px 0' }}>
                <span style={{ width: 7, height: 7, flex: 'none', marginTop: 5, background: g.ok ? 'var(--ok)' : 'var(--line)' }} />
                <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0, flex: 1 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg)' }}>{g.label}</span>
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>{g.detail}</span>
                </span>
                <Pill tone={g.ok ? 'ok' : 'idle'}>{g.ok ? '满足' : '未满足'}</Pill>
              </div>
            ))}

            <div style={{ borderTop: '1px solid var(--line)', marginTop: 16, paddingTop: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>强制开启 · 必填理由（至少 8 字）</span>
              <textarea
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                rows={3}
                disabled={gate.data?.forced}
                placeholder="比如：学院要求周五前上报，剩下 2 条冲突线下开会定，之后再补录"
                style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--line)', padding: '11px 13px', color: 'var(--fg)', font: 'inherit', fontSize: 13, lineHeight: 1.7, resize: 'vertical' }}
              />
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                <Btn
                  danger
                  primary
                  disabled={locked || reason.trim().length < 8 || gate.data?.forced || force.isPending}
                  onClick={() =>
                    force.mutate(reason.trim(), {
                      onSuccess: () => {
                        setReason('')
                        say('闸门已强制开启，理由记进了审计')
                      },
                      onError: fail,
                    })
                  }
                >
                  {gate.data?.forced ? '已强制开启' : '强制开启闸门'}
                </Btn>
                <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>理由会写进审计日志，且不可撤销</span>
              </div>
              {gate.data?.forced && (
                <div style={{ border: '1px solid var(--warn)', background: 'var(--warnBg)', padding: '11px 13px', fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.7 }}>
                  {f.dateTime(gate.data.forcedAt)} 强制开闸：{f.readableText(gate.data.forceReason)}
                </div>
              )}
            </div>
          </div>
  </>
}
