import { userErrorMessage } from '@/api/errorMessages'
/* 运维超管 · 班级、成员与班管。 */

import { useEffect, useMemo, useState } from 'react'
import { Btn, Empty, Field, Note, Overlay, PageHead, Pill, Stat, StatGrid, Sub, Table, THead, TRow, TextBtn } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { useOpsActions, useTenantMembers, useTenants } from '@/api/queries'
import type { Tenant, TenantMember } from '@/api/types'
import { ROLE_LABEL } from '@/lib/types'

const STATE_META: Record<Tenant['state'], { label: string; tone: 'ok' | 'idle' }> = {
  running: { label: '已启用', tone: 'ok' },
  archived: { label: '未启用', tone: 'idle' },
}

const ROLE_TONE: Record<TenantMember['role'], 'ok' | 'warn' | 'idle'> = {
  class_admin: 'ok',
  group: 'warn',
  student: 'idle',
}

const PAGE_SIZE = 30

type DetailTab = 'members' | 'appoint'

export default function OpsTenants() {
  const say = useApp((s) => s.say)
  const tenants = useTenants()
  const { createTenant, updateTenant, appointAdmin, reconcileTenantStorage } = useOpsActions()
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [adminSid, setAdminSid] = useState('')
  const [adminName, setAdminName] = useState('')
  const [selected, setSelected] = useState<Tenant | null>(null)
  const [detailTab, setDetailTab] = useState<DetailTab>('members')
  const [memberSearch, setMemberSearch] = useState('')
  const [appointedSid, setAppointedSid] = useState('')
  const [appointedName, setAppointedName] = useState('')

  const allRows = useMemo(() => tenants.data?.items ?? [], [tenants.data?.items])
  const selectedTenant = selected ? allRows.find((tenant) => tenant.id === selected.id) ?? selected : null
  const members = useTenantMembers(selectedTenant?.id ?? null)
  const needle = search.trim().toLowerCase()
  const pendingStateId = updateTenant.isPending && updateTenant.variables?.archived !== undefined ? updateTenant.variables.id : null
  const pendingArchived = pendingStateId ? updateTenant.variables?.archived : undefined
  const rows = useMemo(
    () => [...(!needle ? allRows : allRows.filter((tenant) =>
      tenant.name.toLowerCase().includes(needle)
      || (tenant.slug ?? '').toLowerCase().includes(needle)
      || tenant.admins.some((admin) => admin.sid.toLowerCase().includes(needle) || admin.name.toLowerCase().includes(needle))))]
      .sort((a, b) => Number(a.id === pendingStateId && pendingArchived !== undefined ? !pendingArchived : a.archived)
        - Number(b.id === pendingStateId && pendingArchived !== undefined ? !pendingArchived : b.archived)
        || a.name.localeCompare(b.name, 'zh-CN') || Number(a.id) - Number(b.id)),
    [allRows, needle, pendingArchived, pendingStateId],
  )
  const pageCount = Math.max(1, Math.ceil(rows.length / PAGE_SIZE))
  const currentPage = Math.min(page, pageCount)
  const visibleRows = rows.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE)
  const active = allRows.filter((tenant) => !tenant.archived).length
  const registeredAdmins = allRows.flatMap((tenant) => tenant.admins).filter((admin) => admin.registered).length
  const memberNeedle = memberSearch.trim().toLowerCase()
  const memberRows = (members.data?.items ?? []).filter((member) => !memberNeedle
    || member.sid.toLowerCase().includes(memberNeedle)
    || member.name.toLowerCase().includes(memberNeedle))
  const fail = (error: unknown) => say(userErrorMessage(error))

  useEffect(() => {
    if (!selectedTenant) return
    const close = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setSelected(null)
    }
    window.addEventListener('keydown', close)
    return () => window.removeEventListener('keydown', close)
  }, [selectedTenant])

  const openDetail = (tenant: Tenant, tab: DetailTab) => {
    setSelected(tenant)
    setDetailTab(tab)
    setMemberSearch('')
    setAppointedSid('')
    setAppointedName('')
  }

  const renameTenant = (tenant: Tenant) => {
    const next = window.prompt('修改班级名称', tenant.name)?.trim()
    if (!next || next === tenant.name) return
    const duplicate = allRows.find((item) => item.id !== tenant.id && item.name.trim().toLowerCase() === next.toLowerCase())
    const warning = duplicate ? `另一个班级（#${duplicate.id}）已使用同名「${next}」。仍要继续？` : `把「${tenant.name}」改为「${next}」？`
    if (!window.confirm(warning)) return
    updateTenant.mutate({ id: tenant.id, name: next }, { onSuccess: () => say(`已重命名为 ${next}`), onError: fail })
  }

  const toggleTenant = (tenant: Tenant) => {
    const archived = !tenant.archived
    say(`正在${archived ? '停用' : '启用'} ${tenant.name}…`)
    updateTenant.mutate(
      { id: tenant.id, archived },
      {
        onSuccess: () => say(archived ? `已停用 ${tenant.name} · 班级成员将无法登录` : `已启用 ${tenant.name}`),
        onError: fail,
      },
    )
  }

  const chooseMemberAsAdmin = (member: TenantMember) => {
    setAppointedSid(member.sid)
    setAppointedName(member.name)
    setDetailTab('appoint')
  }

  const submitAppointment = () => {
    if (!selectedTenant) return
    appointAdmin.mutate(
      { id: selectedTenant.id, sid: appointedSid.trim(), name: appointedName.trim() },
      {
        onSuccess: (admin) => {
          setAppointedSid('')
          setAppointedName('')
          setDetailTab('members')
          say(`已任命 ${admin.name} 为 ${selectedTenant.name} 的班级管理员`)
        },
        onError: fail,
      },
    )
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="TENANTS"
        title="班级与班管"
        desc="开启班级、任命班管"
        side={<Pill tone="ok">{active} 个已启用</Pill>}
      />

      <StatGrid cols={4}>
        <Stat en="全部班级" value={String(allRows.length)} unit="个" note="包括已启用与未启用班级" />
        <Stat en="已启用" value={String(active)} unit="个" note="班级成员可以登录和注册" />
        <Stat en="未启用" value={String(allRows.length - active)} unit="个" note="名册保留，但不能进入系统" />
        <Stat en="已注册班管" value={String(registeredAdmins)} unit="人" note="已完成注册的班级管理员" />
      </StatGrid>

      <div style={{ padding: '22px 0 14px', display: 'flex', alignItems: 'end', justifyContent: 'space-between', gap: 20, flexWrap: 'wrap' }}>
        <Sub title="班级列表" note={`共 ${rows.length} 个班级`} />
        <input
          className="hv-bfg"
          value={search}
          onChange={(event) => { setSearch(event.target.value); setPage(1) }}
          placeholder="搜索班级、班管姓名或学号"
          aria-label="搜索班级"
          style={{ ...fieldStyle, width: 280, maxWidth: '100%' }}
        />
      </div>

      {tenants.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty title={allRows.length ? '没有匹配的班级' : '还没有班级'} desc={allRows.length ? '换个关键词试试。' : '在下面新建班级并任命首位班管。'} />
      ) : (
        <>
          <Table cols="56px minmax(180px,1.5fr) 110px 100px minmax(170px,1fr) 100px minmax(220px,1.2fr)">
            <THead cells={['ID', '班级', '标识', '状态', '班级管理员', '存储用量', '操作']} />
            {visibleRows.map((tenant) => {
              const statePending = pendingStateId === tenant.id
              const targetArchived = pendingArchived
              return (
                <TRow
                  key={tenant.id}
                  cells={[
                    <span key="a" style={mono('11.5px', '.02em')}>{tenant.id}</span>,
                    <button key="b" type="button" className="ops-tenant-name" onClick={() => openDetail(tenant, 'members')}>{tenant.name}</button>,
                    <span key="c" style={mono('11px', '.02em')}>{tenant.slug ?? '—'}</span>,
                    <Pill key="d" tone={STATE_META[tenant.state].tone}>{statePending ? (targetArchived ? '停用中…' : '启用中…') : STATE_META[tenant.state].label}</Pill>,
                    <span key="e" style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.55 }}>
                      {tenant.admins.length === 0 ? '尚未任命' : tenant.admins.map((admin) => `${admin.name}${admin.registered ? '' : '（待注册）'}`).join('、')}
                    </span>,
                    <span key="f" style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 12.5, color: 'var(--fg3)' }}><span>{f.bytes(tenant.storageBytes)}</span><small>{tenant.storageCalibratedAt ? `校准 ${f.dateTime(tenant.storageCalibratedAt)}` : '尚未校准'}</small></span>,
                    <span key="g" style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                      <TextBtn onClick={() => openDetail(tenant, 'members')}>查看成员</TextBtn>
                      <TextBtn onClick={() => openDetail(tenant, 'appoint')}>任命班管</TextBtn>
                      <TextBtn onClick={() => renameTenant(tenant)}>改名</TextBtn>
                      <TextBtn disabled={reconcileTenantStorage.isPending} onClick={() => reconcileTenantStorage.mutate(tenant.id, { onSuccess: (result) => say(`存储已校准为 ${f.bytes(result.storageBytes)}`), onError: fail })}>校准存储</TextBtn>
                      <TextBtn
                        disabled={updateTenant.isPending}
                        tone={statePending || tenant.archived ? undefined : 'var(--red)'}
                        onClick={() => toggleTenant(tenant)}
                      >
                        {statePending ? (targetArchived ? '停用中…' : '启用中…') : tenant.archived ? '启用' : '停用'}
                      </TextBtn>
                    </span>,
                  ]}
                />
              )
            })}
          </Table>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 10, paddingTop: 14, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 12, color: 'var(--fg3)' }}>
              {`${(currentPage - 1) * PAGE_SIZE + 1}–${Math.min(currentPage * PAGE_SIZE, rows.length)}`} / {rows.length}
            </span>
            <Btn disabled={currentPage <= 1} onClick={() => setPage(currentPage - 1)}>上一页</Btn>
            <span style={mono('11px', '0')}>第 {currentPage} / {pageCount} 页</span>
            <Btn disabled={currentPage >= pageCount} onClick={() => setPage(currentPage + 1)}>下一页</Btn>
          </div>
        </>
      )}

      <div style={{ width: 'min(620px, 100%)', padding: '32px 0' }}>
        <Sub title="新建并开启班级" note="建班的时候必须同时指定第一个班级管理员" />
        <Field label="班级名称" hint="会显示在该班所有人的侧栏里">
          <input className="hv-bfg" value={name} onChange={(event) => setName(event.target.value)} placeholder="24级计算机科学与技术2班" style={fieldStyle} />
        </Field>
        <Field label="班级标识（可选）" hint="留空则自动生成 class-<id>">
          <input className="hv-bfg" value={slug} onChange={(event) => setSlug(event.target.value)} placeholder="example-cs-2024-2" style={{ ...fieldStyle, font: "400 13px/1.4 'JetBrains Mono',monospace" }} />
        </Field>
        <Field label="班管学号">
          <input className="hv-bfg" value={adminSid} onChange={(event) => setAdminSid(event.target.value)} placeholder="2024000001" style={fieldStyle} />
        </Field>
        <Field label="班管姓名" hint="必须与本人注册时填写的姓名一致">
          <input className="hv-bfg" value={adminName} onChange={(event) => setAdminName(event.target.value)} placeholder="姓名" style={fieldStyle} />
        </Field>
        <div style={{ marginTop: 16 }}>
          <Btn
            primary
            disabled={!name.trim() || !adminSid.trim() || !adminName.trim() || createTenant.isPending}
            onClick={() => createTenant.mutate(
              { name: name.trim(), slug: slug.trim() || undefined, adminSid: adminSid.trim(), adminName: adminName.trim() },
              {
                onSuccess: (result) => {
                  setName('')
                  setSlug('')
                  setAdminSid('')
                  setAdminName('')
                  say(`已开启 ${result.name}，并任命 ${result.admin.name} 为班管`)
                },
                onError: fail,
              },
            )}
          >
            {createTenant.isPending ? '创建中…' : '开启班级并任命班管'}
          </Btn>
        </div>
      </div>

      {selectedTenant && (
        <Overlay>
          <div className="ops-tenant-overlay" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && setSelected(null)}>
            <aside className="ops-tenant-panel" role="dialog" aria-modal="true" aria-label={`${selectedTenant.name} 班级详情`}>
              <header>
                <div>
                  <span style={mono('10px', '.12em')}>CLASS #{selectedTenant.id}</span>
                  <strong>{selectedTenant.name}</strong>
                  <small>{selectedTenant.slug ?? '无班级标识'}</small>
                </div>
                <Pill tone={STATE_META[selectedTenant.state].tone}>{STATE_META[selectedTenant.state].label}</Pill>
                <button type="button" onClick={() => setSelected(null)} aria-label="关闭班级详情">关闭</button>
              </header>

              <div className="ops-tenant-tabs" role="tablist" aria-label="班级详情功能">
                <button type="button" role="tab" aria-selected={detailTab === 'members'} className={detailTab === 'members' ? 'is-active' : ''} onClick={() => setDetailTab('members')}>班级成员</button>
                <button type="button" role="tab" aria-selected={detailTab === 'appoint'} className={detailTab === 'appoint' ? 'is-active' : ''} onClick={() => setDetailTab('appoint')}>任命班管</button>
              </div>

              {detailTab === 'members' ? (
                <section className="ops-tenant-section" role="tabpanel">
                  <div className="ops-tenant-members-head">
                    <div>
                      <strong>成员名单</strong>
                      <span>{members.data ? `${members.data.items.length} 人 · ${members.data.items.filter((member) => member.registered).length} 人已注册` : '正在读取…'}</span>
                    </div>
                    <input className="hv-bfg" autoFocus value={memberSearch} onChange={(event) => setMemberSearch(event.target.value)} placeholder="搜索姓名或学号" aria-label="搜索班级成员" style={{ ...fieldStyle, width: 220 }} />
                  </div>
                  {members.isLoading ? (
                    <div className="load-bar"><span /></div>
                  ) : members.isError ? (
                    <Note tone="warn">成员名单没读出来，关掉重试一下。</Note>
                  ) : memberRows.length === 0 ? (
                    <Empty title={members.data?.items.length ? '没有匹配的成员' : '该班还没有成员'} desc={members.data?.items.length ? '换个姓名或学号试试。' : '切到“任命班管”，可以直接加第一个管理员。'} />
                  ) : (
                    <Table cols="116px minmax(80px,1fr) 92px 100px 116px minmax(82px,.7fr)">
                      <THead cells={['学号', '姓名', '身份', '账号状态', '最近登录', '操作']} />
                      {memberRows.map((member) => {
                        const state = memberState(member)
                        return <TRow key={member.whitelistId} cells={[
                          <span key="sid" style={mono('11px', '.01em')}>{member.sid}</span>,
                          <span key="name" style={{ color: 'var(--fg)', fontWeight: 500 }}>{member.name}</span>,
                          <Pill key="role" tone={ROLE_TONE[member.role]}>{ROLE_LABEL[member.role]}</Pill>,
                          <Pill key="status" tone={state.tone}>{state.label}</Pill>,
                          <span key="login" style={{ fontSize: 11.5, color: 'var(--fg3)' }}>{member.lastLoginAt ? f.dateTime(member.lastLoginAt) : '—'}</span>,
                          member.role === 'class_admin' && member.rosterActive
                            ? <span key="action" style={{ fontSize: 11.5, color: 'var(--ok)' }}>当前班管</span>
                            : <TextBtn key="action" onClick={() => chooseMemberAsAdmin(member)}>任命班管</TextBtn>,
                        ]} />
                      })}
                    </Table>
                  )}
                </section>
              ) : (
                <section className="ops-tenant-section ops-tenant-appoint" role="tabpanel">
                  <Sub title="任命班级管理员" note="可以从成员名单里挑，也可以直接填一个还没导入的人" />
                  <Field label="班管学号">
                    <input className="hv-bfg" autoFocus value={appointedSid} onChange={(event) => setAppointedSid(event.target.value)} placeholder="学号" style={fieldStyle} />
                  </Field>
                  <Field label="班管姓名" hint="必须与注册时使用的姓名一致">
                    <input className="hv-bfg" value={appointedName} onChange={(event) => setAppointedName(event.target.value)} placeholder="姓名" style={fieldStyle} />
                  </Field>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 16, flexWrap: 'wrap' }}>
                    <Btn primary disabled={!appointedSid.trim() || !appointedName.trim() || appointAdmin.isPending} onClick={submitAppointment}>
                      {appointAdmin.isPending ? '任命中…' : '确认任命'}
                    </Btn>
                    <Btn onClick={() => setDetailTab('members')}>返回成员名单</Btn>
                  </div>
                  <div style={{ marginTop: 20 }}>
                    <Note>已经注册的人马上就有班管权限；还没注册的，按这份任命信息去注册就行。</Note>
                  </div>
                </section>
              )}
            </aside>
          </div>
        </Overlay>
      )}
    </div>
  )
}

function memberState(member: TenantMember): { label: string; tone: 'ok' | 'warn' | 'bad' | 'idle' } {
  if (!member.rosterActive) return { label: '已移出名单', tone: 'bad' }
  if (!member.registered) return { label: '未注册', tone: 'idle' }
  if (member.accountStatus === 'disabled') return { label: '已停用', tone: 'bad' }
  return { label: '正常', tone: 'ok' }
}
