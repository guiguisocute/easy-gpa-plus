/* 学生 · 发起申诉。

   从「我的申诉」拆出来的独立页：那边只看进度和结论，这边才动手写。
   总览、我的提交、当前成绩、申诉详情上的「发起申诉」都跳到这里（?v=stuFileAppeal），
   URL 带着 ?appeal=类型:id 时直接选中那一笔。 */

import { useEffect, useMemo, useState } from 'react'
import { AppealComposer } from '@/components/AppealComposer'
import { Btn, Empty, Note, PageHead } from '@/components/ui'
import { collectAppealTargets } from '@/lib/appealTargets'
import { num } from '@/lib/style'
import * as f from '@/lib/format'
import { useAppeals, useBaseItems, useMyScorecard, useScheme, useSubmissions, useWindow } from '@/api/queries'
import { useApp } from '@/stores/app'

export default function FileAppeal() {
  const go = useApp((s) => s.go)
  const goAppeal = useApp((s) => s.goAppeal)
  const openAppealTarget = useApp((s) => s.openAppealTarget)
  const scheme = useScheme()
  const submissions = useSubmissions()
  const baseItems = useBaseItems()
  const win = useWindow()
  const scorecard = useMyScorecard({ poll: false })
  const appeals = useAppeals()

  const canAppeal = !!win.data?.capabilities.appeal
  const confirmed = !!scorecard.data?.confirmation?.confirmed
  const rows = useMemo(
    () =>
      collectAppealTargets({
        canAppeal,
        confirmed,
        submissions: submissions.data,
        baseItems: baseItems.data,
        scheme: scheme.data,
      }),
    [canAppeal, confirmed, submissions.data, baseItems.data, scheme.data],
  )

  const [picked, setPicked] = useState<string | null>(null)

  useEffect(() => {
    if (openAppealTarget && rows.some((row) => row.key === openAppealTarget)) {
      setPicked(openAppealTarget)
      return
    }
    if (openAppealTarget) setPicked(null)
  }, [openAppealTarget, rows])

  const current = rows.find((row) => row.key === picked)

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="FILE APPEAL"
        title="发起申诉"
        desc="挑一笔结果，写清楚哪里判错了。"
        side={<Btn onClick={() => go('stuAppeals')}>我的申诉</Btn>}
      />

      {submissions.isLoading || baseItems.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty
          title={canAppeal ? '现在没有可以申诉的条目' : '申诉能力当前未开放'}
          desc={confirmed ? '你已经确认仅记录已核对，仍可申诉。' : canAppeal ? '条目得先定了分，才谈得上有没有异议。' : '等班级管理员打开申诉窗口。'}
        />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 28 }}>
          <section style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
              <strong style={{ fontSize: 17, fontWeight: 650, letterSpacing: '-.025em', color: 'var(--fg)' }}>1 · 选择要申诉的认定结果</strong>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.65 }}>只列出还能申诉的条目。</span>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(280px,1fr))', gap: 10 }}>
              {rows.map((row) => {
                const on = row.key === picked
                const locked = !!current && !on
                return (
                  <button
                    key={row.key}
                    type="button"
                    className={on ? undefined : 'hv-sub'}
                    aria-pressed={on}
                    disabled={locked}
                    title={locked ? '先把下面那份草稿取消掉，再换一笔' : undefined}
                    onClick={() => goAppeal(row.type, row.id)}
                    style={{ display: 'flex', flexDirection: 'column', gap: 9, width: '100%', border: `1px solid ${on ? 'var(--red)' : 'var(--line)'}`, borderLeftWidth: on ? 3 : 1, background: on ? 'var(--redBg)' : 'none', margin: 0, padding: '14px 16px', font: 'inherit', textAlign: 'left', cursor: locked ? 'not-allowed' : 'pointer', minWidth: 0, opacity: locked ? 0.5 : 1 }}
                  >
                    <span style={{ display: 'flex', alignItems: 'flex-start', gap: 10, minWidth: 0 }}>
                      <span style={{ width: 9, height: 9, marginTop: 5, flex: 'none', borderRadius: 999, border: `1px solid ${on ? 'var(--red)' : 'var(--line)'}`, background: on ? 'var(--red)' : 'transparent' }} />
                      <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                        <strong style={{ fontSize: 14, fontWeight: 650, color: 'var(--fg)', lineHeight: 1.45 }}>{row.title}</strong>
                        <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.55 }}>{row.path}</span>
                      </span>
                    </span>
                    <span style={{ display: 'flex', alignItems: 'baseline', gap: 10, paddingLeft: 19, flexWrap: 'wrap' }}>
                      <span style={{ fontSize: 24, fontWeight: 650, letterSpacing: '-.04em', color: 'var(--fg)', ...num }}>{f.score(row.score)}</span>
                      <span style={{ fontSize: 12, color: 'var(--fg3)' }}>当前认定分</span>
                      <span style={{ fontSize: 12, color: row.used > 0 ? 'var(--warn)' : 'var(--fg3)' }}>
                        {row.used > 0 ? `已申诉 ${row.used} 次 · 再申诉将由班级管理员终裁` : '还没申诉过'}
                      </span>
                    </span>
                  </button>
                )
              })}
            </div>
            {openAppealTarget && !rows.some((row) => row.key === openAppealTarget) && (
              <Note>链接指的那一笔现在不能申诉，从上面另挑一笔。</Note>
            )}
          </section>

          <section style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
              <strong style={{ fontSize: 17, fontWeight: 650, letterSpacing: '-.025em', color: 'var(--fg)' }}>2 · 填写主张、理由与佐证</strong>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)', lineHeight: 1.65 }}>取消将删除本条草稿及新上传的佐证。</span>
            </div>
            {current ? (
              <div style={{ border: '1px solid var(--line)', padding: '20px clamp(16px,3vw,28px)' }}>
                <AppealComposer
                  key={current.key}
                  target={{ type: current.type, id: current.id }}
                  round={(current.used + 1) as 1 | 2}
                  claim={current.type === 'submission' ? { category: current.category, itemKey: current.itemKey, score: current.score } : undefined}
                  onSubmitted={() => {
                    void appeals.refetch()
                    go('stuAppeals')
                  }}
                  onCancel={() => {
                    setPicked(null)
                    go('stuFileAppeal')
                  }}
                />
              </div>
            ) : (
              <div style={{ border: '1px dashed var(--line)', padding: '24px 20px', fontSize: 13, color: 'var(--fg3)', lineHeight: 1.7, textAlign: 'center' }}>
                请选择申诉对象。
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  )
}
