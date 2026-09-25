/* 班级管理员 · 班级成员。

   注册的判定条件是（学号, 姓名）二元组命中白名单且未被占用；角色也由白名单的 role 字段给出。
   底下仍然是两份数据：白名单管"谁能注册、注册后是什么身份"，账号表管"谁现在是什么身份"。
   这一页按学号把它们并成一张表，但没有把这个区别抹掉——未注册的行身份标着「注册后」；
   升降身份、停用、重置密码与解封只出现在适用的已注册行上，走的是账号那一侧并写审计。
   重置密码只清密码和登录会话，保留成员身份、绑定邮箱与综测数据。 */

import { useEffect, useRef, useState } from 'react'
import { Btn, Empty, Field, Note, PageHead, Pill, Seg, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, TRow, TextBtn } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useAdminUsers, useDeputyAssignment, useRosterActions, useWhitelist } from '@/api/queries'
import { ROLE_LABEL, type Role } from '@/lib/types'
import { canResetMemberPassword, countResettableMemberPasswords, mergeMembers } from '@/lib/roster'

const TABS = [
  { key: 'all', label: '全部' },
  { key: 'registered', label: '已注册' },
  { key: 'pending', label: '未注册' },
  { key: 'sealed', label: '已封存' },
] as const

const ROLE_TONE: Record<string, 'ok' | 'warn' | 'idle'> = {
  class_admin: 'ok',
  group: 'warn',
  student: 'idle',
}

export default function AdminRoster() {
  const say = useApp((s) => s.say)
  const me = useApp((s) => s.user)
  const [tab, setTab] = useState<(typeof TABS)[number]['key']>('all')
  const [paste, setPaste] = useState('')
  const [creating, setCreating] = useState(false)
  const [deputyChoice, setDeputyChoice] = useState('')
  const [newStudent, setNewStudent] = useState({ sid: '', name: '' })
  const [passwordReset, setPasswordReset] = useState<
    { kind: 'single'; uid: string; sid: string; name: string } | { kind: 'all'; count: number } | null
  >(null)
  const [unsealing, setUnsealing] = useState<{ uid: string; name: string } | null>(null)
  const [reason, setReason] = useState('')
	const csvInput = useRef<HTMLInputElement>(null)
  const passwordResetConfirmation = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!passwordReset) return
    passwordResetConfirmation.current?.focus({ preventScroll: true })
    passwordResetConfirmation.current?.scrollIntoView({ block: 'center' })
  }, [passwordReset])

  const whitelist = useWhitelist()
  const users = useAdminUsers()
  const deputyAssignment = useDeputyAssignment()
  const a = useRosterActions()

  const wl = whitelist.data?.items ?? []
  const accounts = users.data?.items ?? []
  const deputy = deputyAssignment.data?.deputy
  const deputyCandidates = accounts.filter((account) => account.role === 'group' && account.status === 'active')
  const members = mergeMembers(wl, accounts)
  const resettablePasswords = countResettableMemberPasswords(members)
  const rows = members.filter((m) =>
    tab === 'all' ? true : tab === 'registered' ? m.account : tab === 'pending' ? !m.account : m.account?.sealed,
  )

  /* 粘贴框接受 "学号,姓名,身份,性别"，导入时补一行 CSV 表头交给后端解析——
     后端已经有一份带校验的 CSV 解析，前端再写一份只会多一处口径。

     性别只喂学院报表的性别列，留空就是留空，不参与"谁能注册"的判定；表头写死
     四列，少填的列后端按空处理，所以老的两列三列名单照样能贴进来。 */
  const parsed = paste.split('\n').map((l) => l.trim()).filter(Boolean)
  const missingGender = wl.filter((r) => r.role === 'student' && !r.gender).length
  const fail = (e: unknown) => say(e instanceof ApiError ? e.message : '操作失败')
	const importRows = async () => {
		const csv = 'sid,name,role,gender\n' + parsed.join('\n')
		try {
			const result = await a.importWhitelist.mutateAsync(csv)
			say(`已导入 ${result.imported} 条`)
			setPaste('')
		} catch (error) {
			if (error instanceof ApiError) downloadWhitelistErrors(csv, error)
			fail(error)
		}
	}
	const createStudent = async () => {
		try {
			await a.addWhitelist.mutateAsync({ sid: newStudent.sid.trim(), name: newStudent.name.trim() })
			say(`已新建学生 · ${newStudent.name.trim()} · 等待本人注册`)
			setNewStudent({ sid: '', name: '' })
			setCreating(false)
		} catch (error) {
			fail(error)
		}
	}
	const confirmPasswordReset = async () => {
		if (!passwordReset) return
		try {
			if (passwordReset.kind === 'single') {
				await a.resetMemberPassword.mutateAsync(passwordReset.uid)
				say(`已重置 ${passwordReset.name} 的密码 · 让本人从注册入口重新设一个`)
			} else {
				const result = await a.resetMemberPasswords.mutateAsync()
				say(`已重置 ${result.reset} 个成员的密码 · 让他们从注册入口重新设`)
			}
			setPasswordReset(null)
		} catch (error) {
			fail(error)
		}
	}
	const passwordResetPending = a.resetMemberPassword.isPending || a.resetMemberPasswords.isPending

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="ROSTER"
        title="班级成员"
        desc="名单、身份和账号"
        side={
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', justifyContent: 'flex-end' }}>
            <Btn primary onClick={() => { setCreating((value) => !value); setPasswordReset(null) }}>{creating ? '收起新建' : '新建学生'}</Btn>
            <Btn
              onClick={() => {
                // 导出的列和导入接受的列保持一致，导出一份改几格再导回去就能用。
                const csv = 'sid,name,role,gender\n' + wl.map((r) => `${r.sid},${r.name},${r.role},${r.gender}`).join('\n')
                const url = URL.createObjectURL(new Blob(['﻿' + csv], { type: 'text/csv;charset=utf-8' }))
                const el = document.createElement('a')
                el.href = url
                el.download = 'whitelist.csv'
                el.click()
                URL.revokeObjectURL(url)
                say('已导出当前白名单 · csv')
              }}
            >
              导出名单
            </Btn>
          </div>
        }
      />

      <div style={{ border: '1px solid var(--line)', padding: '16px 18px', marginBottom: 22 }}>
        <Sub title="副班管" note="班管从已启用、已注册的综测小组成员里挑一个，每班一名。" />
        <div style={{ fontSize: 12.5, lineHeight: 1.8, color: 'var(--fg2)', marginBottom: 12 }}>
          {deputyAssignment.isLoading ? '正在读取副班管任命。' : deputy ? <>现任副班管：<strong style={{ color: 'var(--fg)' }}>{deputy.name} · {deputy.sid}</strong>{!deputy.registered && '（等待重新注册）'}。</> : deputyAssignment.isError ? '副班管任命没读出来，刷新一下再试。' : '尚未任命副班管。'}
          班管自己的事由副班管在「副班管仲裁」页处理。他原本的综测小组活照做。
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
          <select aria-label="副班管人选" value={deputyChoice} onChange={(event) => setDeputyChoice(event.target.value)} style={{ ...fieldStyle, width: 260, maxWidth: '100%' }}>
            <option value="">{deputyCandidates.length ? '选择综测小组成员' : '暂无可任命的综测小组成员'}</option>
            {deputyCandidates.map((account) => <option key={account.id} value={account.id}>{account.name} · {account.sid}{account.isDeputy ? '（现任）' : ''}</option>)}
          </select>
          <Btn primary disabled={!deputyChoice || deputyChoice === deputy?.id || a.setDeputy.isPending} onClick={() => a.setDeputy.mutate(deputyChoice, {
            onSuccess: () => { setDeputyChoice(''); say('副班管已任命 · 让他刷新一下页面，就能看到仲裁入口') }, onError: fail,
          })}>{a.setDeputy.isPending ? '保存中…' : deputy ? '更换副班管' : '任命副班管'}</Btn>
          {deputy && <Btn disabled={a.setDeputy.isPending} onClick={() => a.setDeputy.mutate(null, {
            onSuccess: () => { setDeputyChoice(''); say('已撤销副班管 · 他还是综测小组成员') }, onError: fail,
          })}>撤销任命</Btn>}
        </div>
      </div>

      {creating && (
        <form
          onSubmit={(event) => { event.preventDefault(); void createStudent() }}
          style={{ border: '1px solid var(--line)', marginBottom: 22, padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 14 }}
        >
          <Sub title="新建学生" note="加进本班名单。存完由学生自己去注册。" />
          <div data-r="split" style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 18 }}>
            <Field label="学号">
              <input
                autoFocus
                value={newStudent.sid}
                onChange={(event) => setNewStudent((value) => ({ ...value, sid: event.target.value }))}
                maxLength={64}
                autoComplete="off"
                placeholder="输入学号"
                style={fieldStyle}
              />
            </Field>
            <Field label="姓名">
              <input
                value={newStudent.name}
                onChange={(event) => setNewStudent((value) => ({ ...value, name: event.target.value }))}
                maxLength={100}
                autoComplete="off"
                placeholder="输入真实姓名"
                style={fieldStyle}
              />
            </Field>
          </div>
          <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
            <Btn type="submit" primary disabled={!newStudent.sid.trim() || !newStudent.name.trim() || a.addWhitelist.isPending}>
              {a.addWhitelist.isPending ? '保存中…' : '保存学生'}
            </Btn>
            <Btn onClick={() => { setCreating(false); setNewStudent({ sid: '', name: '' }) }}>取消</Btn>
          </div>
        </form>
      )}

      <StatGrid cols={4}>
        <Stat en="成员总数" value={String(members.length)} unit="人" note="含学生、综测小组与管理员" />
        <Stat en="已注册" value={String(members.filter((m) => m.account).length)} unit="人" note="已设置密码并完成注册" />
        <Stat en="未注册" value={String(members.filter((m) => !m.account).length)} unit="人" note="名单有效，当前需要注册" tone="var(--warn)" />
        <Stat
          en="已停用"
          value={String(accounts.filter((u) => u.status === 'disabled').length)}
          unit="人"
          note="账号无法登录，已提交材料保留"
          tone="var(--red)"
        />
      </StatGrid>

      <div style={{ padding: '22px 0 14px' }}>
        <Sub
          title="成员"
          note="筛选、调整身份或管理账号"
          actions={
            <Btn
              danger
              disabled={resettablePasswords === 0 || passwordResetPending}
              onClick={() => { setPasswordReset({ kind: 'all', count: resettablePasswords }); setCreating(false) }}
              title={resettablePasswords === 0 ? '现在没有已启用而且已注册的学生或综测小组账号' : '把所有已启用、已注册的学生和综测小组密码全部重置'}
            >
              批量重置密码（{resettablePasswords} 人）
            </Btn>
          }
        />
        <Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={setTab} />
      </div>

      {whitelist.isLoading || users.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty title="这一类下没有人" desc="换个筛选看看，或者在下面批量导入。" />
      ) : (
        <Table cols="116px 80px 104px 92px minmax(168px,220px) 108px minmax(220px,1fr)">
          <THead cells={['学号', '姓名', '身份', '状态', '主邮箱', '注册时间', '操作']} />
          {rows.map((m) => {
            const u = m.account
            const self = me?.sid === m.sid
            const state = !u
              ? { label: '未注册', color: 'var(--fg3)' }
              : u.status !== 'active'
                ? { label: '已停用', color: 'var(--red)' }
                : u.auditStatus === 'blocked'
                  ? { label: '审核阻塞', color: 'var(--red)' }
                  : u.auditStatus === 'sealed_unreviewed'
                    ? { label: '已封存但未审核', color: 'var(--warn)' }
                    : u.sealed
                      ? { label: '已封存', color: 'var(--ok)' }
                      : { label: '正常', color: 'var(--ok)' }
            return (
              <TRow
                key={m.sid}
                cells={[
                  <span key="a" style={mono('11.5px', '.02em')}>{m.sid}</span>,
                  <span key="b" style={{ color: 'var(--fg)', fontWeight: 500 }}>{m.name}</span>,
                  <span key="c" style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                    <Pill tone={ROLE_TONE[m.role] ?? 'idle'}>{ROLE_LABEL[m.role as Role]}</Pill>
                    {u?.isDeputy && <Pill tone="ok">副班管</Pill>}
                    {!u && <span style={{ fontSize: 11, color: 'var(--fg3)' }}>注册后</span>}
                  </span>,
                  <span key="d" style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 4 }}>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: state.color, lineHeight: 1.4 }}>
                      <i style={{ width: 6, height: 6, display: 'block', background: state.color }} />{state.label}
                    </span>
                    {u?.sealed && u.auditStatus !== 'complete' && u.auditBlocker && (
                      <small style={{ color: u.auditStatus === 'blocked' ? 'var(--red)' : 'var(--fg3)', lineHeight: 1.4 }}>{u.auditBlocker}</small>
                    )}
                  </span>,
                  <span key="e" title={u?.primaryEmail ?? undefined} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: u?.primaryEmail ? 'var(--fg2)' : 'var(--fg3)' }}>
                    {u?.primaryEmail ?? '—'}
                  </span>,
                  <span key="f" style={{ fontSize: 12, color: 'var(--fg3)' }}>{f.dateTime(m.registeredAt)}</span>,
                  <span key="g" style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                    {!u && m.whitelistId && (
                      <TextBtn
                        tone="var(--red)"
                        onClick={() =>
                          a.removeWhitelist.mutate(m.whitelistId as string, { onSuccess: () => say(`已移出名单 · ${m.name}`), onError: fail })
                        }
                      >
                        移出
                      </TextBtn>
                    )}
                    {u && u.role !== 'class_admin' && (
                      <TextBtn
                        onClick={() =>
                          a.setRole.mutate(
                            { id: u.id, role: u.role === 'student' ? 'group' : 'student' },
                            { onSuccess: () => say(`已${u.role === 'student' ? '升为综测小组' : '降为学生'} · ${m.name} · 已写入审计`), onError: fail },
                          )
                        }
                      >
                        {u.role === 'student' ? '升为小组' : '降为学生'}
                      </TextBtn>
                    )}
                    {u && !self && (
                      <TextBtn
                        tone={u.status === 'active' ? 'var(--red)' : undefined}
                        onClick={() =>
                          a.setStatus.mutate(
                            { id: u.id, status: u.status === 'active' ? 'disabled' : 'active' },
                            { onSuccess: () => say(`已${u.status === 'active' ? '停用' : '启用'} ${m.name}`), onError: fail },
                          )
                        }
                      >
                        {u.status === 'active' ? '停用' : '启用'}
                      </TextBtn>
                    )}
                    {u && (u.role === 'student' || u.role === 'group') && (
                      <TextBtn
                        tone="var(--red)"
                        disabled={!canResetMemberPassword(m) || passwordResetPending}
                        title={u.status !== 'active' ? '请先启用账号再重置密码' : undefined}
                        onClick={() => {
                          setPasswordReset({ kind: 'single', uid: u.id, sid: m.sid, name: m.name })
                          setCreating(false)
                        }}
                      >
                        重置密码
                      </TextBtn>
                    )}
                    {u?.sealed && (
                      <TextBtn
                        onClick={() => {
                          setUnsealing({ uid: u.id, name: m.name })
                          setReason('')
                        }}
                      >
                        解封
                      </TextBtn>
                    )}
                  </span>,
                ]}
              />
            )
          })}
        </Table>
      )}

      {passwordReset && (
        <div
          ref={passwordResetConfirmation}
          role="region"
          aria-labelledby="password-reset-title"
          tabIndex={-1}
          style={{ border: '1px solid var(--red)', background: 'var(--redBg)', marginTop: 18, padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 12 }}
        >
          <strong id="password-reset-title" style={{ fontSize: 13, color: 'var(--fg)' }}>
            {passwordReset.kind === 'single'
              ? `重置 ${passwordReset.name}（${passwordReset.sid}）的密码？`
              : `重置当前 ${passwordReset.count} 个成员的密码？`}
          </strong>
          <span style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75 }}>
            旧密码马上失效，所有设备下线。其他数据都还在。
          </span>
          <span style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.75 }}>
            {passwordReset.kind === 'all' ? '范围是已启用而且已注册的学生和综测小组，班级管理员不在内。' : ''}
            让他们在登录页点「首次使用 · 注册」重设。
          </span>
          <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
            <Btn primary danger disabled={passwordResetPending} onClick={() => void confirmPasswordReset()}>
              {passwordResetPending ? '重置中…' : '确认重置密码'}
            </Btn>
            <Btn disabled={passwordResetPending} onClick={() => setPasswordReset(null)}>取消</Btn>
          </div>
        </div>
      )}

      {/* 解封必须写理由，所以不做成点一下就生效的行内按钮：选中谁之后在这里补理由再确认。 */}
      {unsealing && (
        <div style={{ border: '1px solid var(--line)', marginTop: 18, padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 12 }}>
          <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>解封 {unsealing.name} · 必填理由（至少 4 字）</span>
          <textarea
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            rows={3}
            aria-label="解封理由"
            placeholder="比如：学生漏了一份志愿服务证明，班会上确认过，允许补交"
            style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--line)', padding: '11px 13px', color: 'var(--fg)', font: 'inherit', fontSize: 13, lineHeight: 1.7, resize: 'vertical' }}
          />
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <Btn
              primary
              disabled={reason.trim().length < 4 || a.unseal.isPending}
              onClick={() =>
                a.unseal.mutate(
                  { uid: unsealing.uid, reason: reason.trim() },
                  {
                    onSuccess: () => {
                      say(`已解封 ${unsealing.name} · 理由写入审计日志`)
                      setUnsealing(null)
                      setReason('')
                    },
                    onError: fail,
                  },
                )
              }
            >
              {a.unseal.isPending ? '解封中…' : '确认解封'}
            </Btn>
            <Btn onClick={() => setUnsealing(null)}>取消</Btn>
            <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>理由会记进审计。提交和修改仍须在开放时间内进行。</span>
          </div>
          <p style={{ margin: 0, fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.8 }}>
            解封后可在材料开放期间补交，再重新封存。成绩或问题进度变化后会提示学生重新核对，核对不阻塞结算。其他学生继续审核；已有结算会失效，待业务事项完成后重新结算。
          </p>
        </div>
      )}

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '30px 0' }}>
            <Sub title="批量导入白名单" note="每行一条：学号,姓名,身份,性别" />
            <Field label="粘贴名单" hint="没填身份就是学生。学号姓名身份只改还没注册的人。性别随时能补。">
              <textarea
                value={paste}
                onChange={(e) => setPaste(e.target.value)}
                rows={7}
                placeholder={'2023211001,王一,student,女\n2023211002,李二,group,男'}
                style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--line)', padding: '11px 13px', color: 'var(--fg)', font: "400 12.5px/1.8 'JetBrains Mono',monospace", resize: 'vertical' }}
              />
            </Field>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 14, flexWrap: 'wrap' }}>
			  <input ref={csvInput} type="file" accept=".csv,text/csv" hidden onChange={async (e) => { const file = e.target.files?.[0]; e.target.value = ''; if (!file) return; if (file.size > 1024 * 1024) return say('CSV 不能超过 1 MB'); const lines = (await file.text()).replace(/^\uFEFF/, '').split(/\r?\n/); if (/^\s*sid\s*,\s*name\s*,\s*role\s*(,\s*gender\s*)?$/i.test(lines[0] ?? '')) lines.shift(); setPaste(lines.join('\n').trim()); say(`已读取 ${file.name} · 请预览后再导入`) }} />
			  <Btn onClick={() => csvInput.current?.click()}>选择 CSV 文件</Btn>
              <Btn
                primary
                  disabled={parsed.length === 0 || a.importWhitelist.isPending}
				onClick={() => void importRows()}
              >
                  {a.importWhitelist.isPending ? '导入中…' : `导入 ${parsed.length || ''} 条`}
              </Btn>
              <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>导入会写入审计日志</span>
            </div>
            {/* 性别只在学院报表的性别列用得到，所以提示放在这里、只在真缺的时候
                出现：让人知道现在导出会空几格，而不是逼着谁一定要填。 */}
            {missingGender > 0 && (
              <div style={{ marginTop: 14 }}>
                <Note tone="warn">名单里还有 {missingGender} 人没填性别，学院报表附件5 会空一列。导出名单填好再导回来。</Note>
              </div>
            )}
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '30px 0' }}>
            <Sub title="授权说明" />
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14, fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.85 }}>
              <div>
                <strong style={{ color: 'var(--fg)' }}>学生</strong>：交材料、看自己的分、提申诉。看不到别人。
              </div>
              <div>
                <strong style={{ color: 'var(--fg)' }}>综测小组</strong>：在分给自己的大项里做判断。看不到另一名审核人写了什么，学生也不知道他是谁。
              </div>
              <div>
                <strong style={{ color: 'var(--fg)' }}>副班管</strong>：专门裁班管自己的事，不会因此拿到其他班管权限。
              </div>
              <div>
                <strong style={{ color: 'var(--fg)' }}>班级管理员</strong>：管名单和账号、仲裁终裁、分发导出、查审计。看得到所有人的真名。
              </div>
              <div style={{ color: 'var(--fg3)' }}>
                封存只是不让再提交和修改。不能替学生封存，只能提醒。解封要写理由。
              </div>
              <div style={{ color: 'var(--fg3)' }}>平台运维账号不算班级成员，不在这里管。</div>
              <div style={{ color: 'var(--fg3)' }}>
                最后一个还能用的管理员不能降级也不能停用，不然这个班就没人能仲裁了。
              </div>
            </div>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}

function downloadWhitelistErrors(source: string, error: ApiError) {
	const detail = error.detail && typeof error.detail === 'object' ? error.detail as Record<string, unknown> : {}
	const line = typeof detail.line === 'number' ? detail.line : null
	const row = line ? (source.split(/\r?\n/)[line - 1] ?? '') : ''
	const quote = (value: unknown) => `"${String(value ?? '').replaceAll('"', '""')}"`
	const csv = `line,row,error\n${quote(line)},${quote(row)},${quote(error.message)}\n`
	const url = URL.createObjectURL(new Blob(['\uFEFF' + csv], { type: 'text/csv;charset=utf-8' }))
	const link = document.createElement('a')
	link.href = url
	link.download = 'whitelist-import-errors.csv'
	document.body.append(link)
	link.click()
	link.remove()
	URL.revokeObjectURL(url)
}
