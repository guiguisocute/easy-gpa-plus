/* 运维超管 · 平台审计。

   记的是运维自己干了什么，包括那些被拒绝的越权尝试（route.denied / business_access.denied）。
   这条链路和班级审计是两张表：班级看不到平台审计，平台审计里也没有任何班级业务内容。 */

import { useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Seg, Stat, StatGrid, Sub, Table, THead, TRow } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useOpsAudit } from '@/api/queries'

const TABS = [
  { key: 'all', label: '全部' },
  { key: 'denied', label: '被拒绝' },
  { key: 'write', label: '变更' },
] as const

const ACTION_LABEL: Record<string, string> = {
  'tenant.list': '浏览租户列表',
  'tenant.created': '创建租户',
  'tenant.updated': '修改租户',
  'tenant.admin_appointed': '任命班级管理员',
  'tenant.members_listed': '查看班级成员',
  'template.list': '浏览模板库',
  'template.created': '导入并上架模板',
  'template.import_reused': '重复导入已有模板',
  'template.retired': '下架模板',
  'template.restored': '重新上架模板',
  'mail.read': '查看邮件配置',
  'mail.updated': '修改邮件配置',
  'mail.test_sent': '发送测试邮件',
  'mail.test_failed': '测试邮件失败',
  'mail.rotate_rejected': '拒绝轮换密钥',
  'mail.log_read': '查看投递日志',
  'queue.read': '查看队列',
  'queue.redelivered': '重投失败任务',
	'worker.list': '读取 Worker 状态',
	'worker.scaled': '调整 Worker 副本（历史操作）',
	'worker.scale_failed': 'Worker 扩缩容失败（历史操作）',
  'cron.read': '查看定时任务',
  'backup.list': '查看备份',
  'backup.restore_drill_requested': '发起恢复演练',
  'flags.read': '查看开关',
  'flag.updated': '修改开关',
  'health.read': '查看健康状态',
  'deploy.read': '查看部署信息',
  'audit.read': '查看平台审计',
  'route.denied': '越权尝试被拒',
  'business_access.denied': '业务接口越权被拒',
}

const isDenied = (a: string) => a.endsWith('.denied') || a.endsWith('_rejected') || a.endsWith('_failed')
const isRead = (a: string) => a.endsWith('.read') || a.endsWith('.list') || a.endsWith('_read')

export default function OpsAudit() {
  const [tab, setTab] = useState<(typeof TABS)[number]['key']>('all')
	const [action, setAction] = useState('')
	const [resourceType, setResourceType] = useState('')
	const [actor, setActor] = useState('')
	const [from, setFrom] = useState('')
	const [to, setTo] = useState('')
	const [page, setPage] = useState(1)
	const audit = useOpsAudit({ action: action || undefined, resourceType: resourceType || undefined, actor: actor || undefined, from: from ? new Date(from).toISOString() : undefined, to: to ? new Date(to).toISOString() : undefined, page, page_size: 100 })

  const all = audit.data?.items ?? []
  const rows = all.filter((r) => (tab === 'all' ? true : tab === 'denied' ? isDenied(r.action) : !isRead(r.action) && !isDenied(r.action)))
  const denied = all.filter((r) => isDenied(r.action)).length

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="PLATFORM AUDIT"
        title="平台审计"
        desc="运维操作和被拒访问"
      />

      <StatGrid cols={4}>
		<Stat en="匹配记录" value={String(audit.data?.total ?? 0)} unit="条" note="服务端筛选与分页" />
		<Stat en="变更操作" value={String(all.filter((r) => !isRead(r.action) && !isDenied(r.action)).length)} unit="条" note="班级、令牌与全局配置变更" />
        <Stat en="读取操作" value={String(all.filter((r) => isRead(r.action)).length)} unit="条" note="看列表、看健康状态也记" />
        <Stat en="访问被拒" value={String(denied)} unit="条" note="不允许的业务访问" tone={denied > 0 ? 'var(--red)' : undefined} />
      </StatGrid>

      <div style={{ padding: '22px 0 14px' }}>
        <Sub
          title="记录"
          actions={<Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={setTab} pad="4px 12px" fs={12} />}
        />
      </div>
	  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', paddingBottom: 14 }}>
		<input value={actor} onChange={(e) => { setActor(e.target.value); setPage(1) }} placeholder="操作人" style={{ ...fieldStyle, width: 140 }} />
		<input value={action} onChange={(e) => { setAction(e.target.value); setPage(1) }} placeholder="动作前缀" style={{ ...fieldStyle, width: 170 }} />
		<input value={resourceType} onChange={(e) => { setResourceType(e.target.value); setPage(1) }} placeholder="资源类型" style={{ ...fieldStyle, width: 140 }} />
		<input type="datetime-local" value={from} onChange={(e) => { setFrom(e.target.value); setPage(1) }} style={{ ...fieldStyle, width: 190 }} aria-label="开始时间" />
		<input type="datetime-local" value={to} onChange={(e) => { setTo(e.target.value); setPage(1) }} style={{ ...fieldStyle, width: 190 }} aria-label="结束时间" />
	  </div>

      {audit.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty title="没有这一类记录" desc="换个筛选看看。平台审计只能往后加，改不了也删不掉。" />
      ) : (
        <Table cols="130px 130px 76px minmax(180px,2fr) 130px 110px">
          <THead cells={['时间', '操作人', '类型', '动作', '对象', '来源 IP']} />
          {rows.map((r) => (
            <TRow
              key={r.id}
              cells={[
                <span key="a" style={mono('11.5px', '.02em')}>{f.dateTime(r.createdAt)}</span>,
                <span key="b" style={{ color: 'var(--fg)', fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.actor}</span>,
                <Pill key="c" tone={isDenied(r.action) ? 'bad' : isRead(r.action) ? 'idle' : 'warn'}>
                  {isDenied(r.action) ? '拒绝' : isRead(r.action) ? '读取' : '变更'}
                </Pill>,
                <span key="d" style={{ fontSize: 12.5, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {ACTION_LABEL[r.action] ?? r.action}
                </span>,
                <span key="e" style={{ fontSize: 12, color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {r.resourceType}
                  {r.resourceId ? ` ${r.resourceId}` : ''}
                </span>,
                <span key="f" style={mono('11px', '0')}>{r.ip ?? '—'}</span>,
              ]}
            />
          ))}
        </Table>
      )}
	  <div style={{ display: 'flex', gap: 10, alignItems: 'center', paddingTop: 16 }}><Btn disabled={page <= 1} onClick={() => setPage((value) => value - 1)}>上一页</Btn><span style={mono('11px', '0')}>第 {page} / {Math.max(1, Math.ceil((audit.data?.total ?? 0) / 100))} 页</span><Btn disabled={page * 100 >= (audit.data?.total ?? 0)} onClick={() => setPage((value) => value + 1)}>下一页</Btn></div>

      <div style={{ paddingTop: 26 }}>
        <Note>被拒掉的业务访问都会在这里留下记录。</Note>
      </div>
    </div>
  )
}
