/* 确认全部提交完成（封存）卡。

   封存（§2.2）三件事必须做对：
   1. 两步确认——逐字键入短语 + 本人学号，两项都对才可提交；
   2. 封存只关"提交与修改"，审核、申诉、查看一概不受影响；
   3. 封存不冻结申诉权，否则学生会为保住申诉权拒绝封存，结算闸门永远开不了。

   确认短语取自后端 /me/seal 的 confirmationPhrase，前端不写死——
   两边各存一份文案，改动时必然有一边忘记改。

   零 props：数据全部自取（同键查询走 TanStack 缓存，不产生额外请求），
   调用点一行 <SealCard />，不需要在页面之间对齐参数含义。 */

import { useState } from 'react'
import { Btn, Pill } from '@/components/ui'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useSeal, useSealActions, useSubmissions, useWindow } from '@/api/queries'

export function SealCard() {
  const say = useApp((s) => s.say)
  const user = useApp((s) => s.user)
  const [step, setStep] = useState<0 | 1>(0)
  const [phrase, setPhrase] = useState('')
  const [sid, setSid] = useState('')

  const subs = useSubmissions()
  const seal = useSeal()
  const win = useWindow()
  const sealMutation = useSealActions()

  const sealed = !!seal.data?.sealed
  const SEAL_PHRASE = seal.data?.confirmationPhrase ?? '全部提交完成'
  const mySid = user?.sid ?? ''
  const okPhrase = phrase.trim() === SEAL_PHRASE
  const okSid = sid.trim() === mySid
  const canSeal = okPhrase && okSid

  /* 草稿平时不在「我的提交」列表里露面，但封存会把它们永久排除在外——这一处必须说出来。 */
  const drafts = (subs.data?.items ?? []).filter((r) => r.status === 'draft')
  const borderFor = (touched: boolean, ok: boolean) => (!touched ? 'var(--line)' : ok ? 'var(--ok)' : 'var(--red)')
  const windowClose = win.data?.window.close ?? seal.data?.windowClose

  return (
    <div style={{ border: '1px solid var(--line)', marginTop: 34, display: 'flex', flexDirection: 'column' }}>
      <div style={{ background: 'var(--sub)', borderBottom: '1px solid var(--line)', padding: '18px 22px', display: 'flex', alignItems: 'flex-start', gap: 18, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 7, minWidth: 0, flex: 1 }}>
          <div style={{ fontSize: 16, fontWeight: 600, letterSpacing: '-.025em' }}>确认全部提交完成（封存）</div>
          <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75, maxWidth: 600, textWrap: 'pretty' }}>
            截止前可补交、修改。封存后不可提交或修改，审核与申诉仍可进行。{f.dateTime(windowClose)} 前未封存的账号将由系统自动封存。
          </div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 5, flex: 'none' }}>
          <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>窗口截止</span>
          <span style={{ font: "400 12px/1 'JetBrains Mono',monospace", color: 'var(--red)' }}>{f.dateTime(windowClose)}</span>
        </div>
      </div>

      {sealed ? (
        <div style={{ padding: 22, display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap', animation: 'rise .2s ease both' }}>
          <Pill tone="ok">已封存 · {f.dateTime(seal.data?.sealedAt)} {seal.data?.source === 'auto' ? '（到期自动）' : '确认'}</Pill>
          <span style={{ fontSize: 12.5, color: 'var(--fg2)', maxWidth: 460, lineHeight: 1.7, textWrap: 'pretty' }}>
            已不可提交或修改。审核与申诉仍可进行。
          </span>
          <div style={{ marginLeft: 'auto', flex: 'none' }}>
            <Btn onClick={() => say('请联系班级管理员解封。')}>需要解封？</Btn>
          </div>
        </div>
      ) : step === 0 ? (
        <div style={{ padding: 22, display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
          <Btn primary onClick={() => setStep(1)}>
            我已全部提交完成
          </Btn>
        </div>
      ) : (
        <div style={{ padding: 22, display: 'flex', flexDirection: 'column', gap: 20, animation: 'rise .2s ease both' }}>
          <div style={{ border: '1px solid var(--line)', padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 9 }}>
            <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>封存前请确认</span>
            {/* 草稿平时不在本页露面，但封存会把它们永久排除在外——这一处必须说出来。 */}
            {drafts.length === 0 ? (
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>没有未提交的草稿。</span>
            ) : (
              drafts.map((d) => (
                <div key={d.id} style={{ display: 'flex', alignItems: 'center', gap: 11, borderTop: '1px solid var(--line2)', padding: '10px 0 0' }}>
                  <span style={{ width: 5, height: 5, flex: 'none', background: 'var(--red)' }} />
                  <span style={{ fontSize: 13, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f.submissionTitle(d.title)}</span>
                  <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)', flex: 'none' }}>仍是草稿，封存后不再计入</span>
                </div>
              ))
            )}
            {drafts.length > 0 && (
              <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.7, textWrap: 'pretty' }}>
                草稿在「提交材料」各小项的草稿箱里，交上去才算数。
              </span>
            )}
          </div>

          <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 22 }}>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>第一步 · 键入「{SEAL_PHRASE}」</span>
              <input
                value={phrase}
                onChange={(e) => setPhrase(e.target.value)}
                onPaste={(e) => {
                  /* 逐字键入是这个确认的全部意义所在，粘贴会让它退化成一次点击。 */
                  e.preventDefault()
                  say('确认短语得一个字一个字打，不能粘贴')
                }}
                placeholder={SEAL_PHRASE}
                style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${borderFor(!!phrase.trim(), okPhrase)}`, padding: '11px 13px', color: 'var(--fg)', fontSize: 14.5, fontWeight: 500 }}
              />
              <span style={{ fontSize: 12.5, color: okPhrase ? 'var(--ok)' : phrase.trim() ? 'var(--red)' : 'var(--fg3)' }}>
                {!phrase.trim() ? '逐字键入，不支持粘贴' : okPhrase ? '与提示一致' : '与提示不一致'}
              </span>
            </label>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>第二步 · 键入你的学号</span>
              <input
                value={sid}
                onChange={(e) => setSid(e.target.value)}
                placeholder={mySid}
                style={{ width: '100%', background: 'var(--bg)', border: `1px solid ${borderFor(!!sid.trim(), okSid)}`, padding: '11px 13px', color: 'var(--fg)', font: "400 14.5px/1.2 'JetBrains Mono',monospace" }}
              />
              <span style={{ fontSize: 12.5, color: okSid ? 'var(--ok)' : sid.trim() ? 'var(--red)' : 'var(--fg3)' }}>
                {!sid.trim() ? '与登录学号一致才生效' : okSid ? '与登录学号一致' : '与登录学号不一致'}
              </span>
            </label>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', borderTop: '1px solid var(--line2)', paddingTop: 16 }}>
            <Btn
              primary
              disabled={!canSeal || sealMutation.isPending}
              onClick={() =>
                sealMutation.mutate(
                  { phrase: phrase.trim(), sid: sid.trim() },
                  {
                    onSuccess: (r) =>
                      say(
                        r.draftsExcluded > 0
                          ? `已封存 · ${r.draftsExcluded} 条草稿未计入`
                          : '已封存，不可再提交或修改。审核与申诉仍可进行。',
                      ),
                    onError: (e) => say(e instanceof ApiError ? e.message : '封存失败'),
                  },
                )
              }
            >
              {sealMutation.isPending ? '封存中…' : '确认封存'}
            </Btn>
            <Btn
              onClick={() => {
                setStep(0)
                setPhrase('')
                setSid('')
              }}
            >
              暂不封存
            </Btn>
            <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)' }}>封存后如需修改，须由班级管理员解封。</span>
          </div>
        </div>
      )}
    </div>
  )
}
