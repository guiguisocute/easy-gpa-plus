/* 共治的「结算检查」和「导出授权」。两页都只是把导出中心已有的那几块摆过来：
   闸门条件、结算四步、文件清单、学院报表对照。唯一不同的是最后一个按钮——
   班管直接执行，共治发起提案，由表决替他按下去。 */

import { useRef, useState } from 'react'
import { ExportProducts } from '@/components/ExportProducts'
import { CollegeExportGuide } from '@/components/CollegeExportGuide'
import { SettlementConditions, SettlementSteps } from '@/components/SettlementGate'
import { Btn, Empty, Field, Note, Pill, Stat, StatGrid, Sub } from '@/components/ui'
import { fieldStyle } from '@/lib/style'
import { useGate } from '@/api/queries'
import { useGovernanceAction } from '@/api/governance'
import { userErrorMessage } from '@/api/errorMessages'
import { useApp } from '@/stores/app'
import type { ExportKind } from '@/api/types'

export default function GovernanceSettlement({ kind, joined }: { kind: 'settle' | 'export'; joined: boolean }) {
  const gate = useGate(true)
  const action = useGovernanceAction()
  const say = useApp((s) => s.say)
  const [product, setProduct] = useState<ExportKind>('summary')
  const [purpose, setPurpose] = useState('')
  const request = useRef(crypto.randomUUID())

  if (gate.isError)
    return <Empty title="还没有已发布的方案" desc="先完成方案与名单准备，才能检查结算条件。" />
  if (gate.isLoading || !gate.data) return <div className="load-bar"><span /></div>

  const g = gate.data
  const unmet = g.conditions.filter((c) => !c.ok)

  const run = async (body: unknown, path: string, notice: string) => {
    try {
      const result = await action.mutateAsync({ path, body })
      say(result.notice ?? notice)
      return true
    } catch (error) {
      say(userErrorMessage(error))
      return false
    }
  }

  return (
    <>
      <StatGrid cols={4}>
        <Stat
          en="闸门状态"
          value={g.open ? '已开启' : '未开启'}
          note={unmet.map((c) => c.label).join(' · ') || '全部条件满足'}
          tone={g.open ? 'var(--ok)' : 'var(--red)'}
        />
        <Stat en="覆盖人数" value={`${g.sealedCount} / ${g.studentCount}`} note="全班都完成了才能结算" />
        <Stat en="专业分" value={`${g.gpaImportedCount} / ${g.studentCount}`} note="缺一人就过不了闸门" tone={g.gpaImportedCount >= g.studentCount ? 'var(--ok)' : 'var(--red)'} />
        <Stat en="未结争议" value={String(g.pendingConflicts)} unit="条" note="评审未形成多数的案件" tone={g.pendingConflicts ? 'var(--red)' : undefined} />
      </StatGrid>

      <SettlementConditions gate={g} />

      {kind === 'settle' ? (
        <>
          <SettlementSteps note="任何生效参与者都可以请求检查；条件不满足时系统不会记一笔假的结算。" />
          <section className="gov-section">
            <Sub title="业务完成后，由系统结算" />
            <p>
              材料已封存或窗口已截止、专业分覆盖全班、单项审核和争议处理完成后，任何生效参与者都可以请求结算。没有材料不等于有一份待审空表；个人成绩核对不阻塞结算。
            </p>
            <div className="gov-actions">
              <Btn
                primary
                disabled={!joined || action.isPending}
                onClick={() => void run({}, '/settle', '结算条件已检查')}
              >
                检查条件并请求结算
              </Btn>
            </div>
          </section>
        </>
      ) : (
        <>
          <section className="gov-section">
            <Sub title="选择要授权的文件" note="一次授权一种，发起人领取" />
            <Note>授权限于这一次导出，发起人领取文件。仍须满足结算条件。</Note>
            <ExportProducts
              selected={product}
              badge={<Pill>已选择</Pill>}
              action={(value) => (
                <Btn
                  disabled={action.isPending}
                  onClick={() => {
                    setProduct(value)
                    request.current = crypto.randomUUID()
                  }}
                >
                  {product === value ? '已选择' : '选择'}
                </Btn>
              )}
            />
            <form
              className="gov-form"
              onSubmit={async (e) => {
                e.preventDefault()
                const ok = await run(
                  {
                    requestId: request.current,
                    kind: 'ordinary',
                    action: 'export',
                    title: '申请领取本周期正式导出',
                    body: purpose,
                    payload: { kind: product },
                  },
                  '/proposals',
                  '已发布导出授权提案，发起人回避本次表决',
                )
                if (ok) {
                  request.current = crypto.randomUUID()
                  setPurpose('')
                }
              }}
            >
              <Field label="用途与领取说明" required hint="写清这份文件交给谁、做什么用。这段话会随提案公示。">
                <textarea
                  aria-label="用途与领取说明"
                  style={{ ...fieldStyle, resize: 'vertical' }}
                  required
                  minLength={4}
                  maxLength={2000}
                  rows={4}
                  value={purpose}
                  onChange={(e) => {
                    setPurpose(e.target.value)
                    request.current = crypto.randomUUID()
                  }}
                />
              </Field>
              <div>
                <Btn primary type="submit" disabled={!joined || action.isPending}>
                  发布导出授权提案
                </Btn>
              </div>
            </form>
            <Note>表决通过后，回到「提案与表决」里那条提案上领取文件；授权只对这一次生成的文件有效。</Note>
          </section>
          <CollegeExportGuide />
        </>
      )}
    </>
  )
}
