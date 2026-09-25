import { userErrorMessage } from '@/api/errorMessages'
/* 班级管理员 · 审计日志。

   两个与常见做法不同的地方，都是有意为之（§14）：
   1. 读操作也记——谁看过谁的佐证，这在综测这种熟人场景里比写操作更敏感；
   2. 对班级管理员可见——平台把"不可能看见"换成"看见了会留痕"，用透明换可验证。

   读/写的分类由 action 名推出而不是后端给字段：动作名是稳定的机器标识，
   在这里维护一份"哪些算读"的清单，比让后端多存一列冗余分类更容易改对。 */

import { useEffect, useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Seg, Stat, StatGrid, Sub, Table, THead, TRow } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { ROLE_LABEL, type Role } from '@/lib/types'
import { useAuditLog } from '@/api/queries'
import { api } from '@/api/client'
import type { AuditEntry, Page } from '@/api/types'
import { useApp } from '@/stores/app'

const TABS = [
  { key: 'all', label: '全部' },
  { key: 'write', label: '写操作' },
  { key: 'read', label: '读操作' },
] as const

/** 读操作的动作后缀。其余一律按写操作处理——宁可把读误判成写，也不要反过来。 */
const READ_SUFFIX = ['.read', '.list', '.url_issued', '.task_read', '.task_list', '.version_read', '.admin_read', '.admin_list', '.history_read', '.category_read', '.appeal_task_list']
const isRead = (action: string) => READ_SUFFIX.some((s) => action.endsWith(s))

/** 动作名到人话。没覆盖到的直接显示原始动作名，不猜。 */
const ACTION_LABEL: Record<string, string> = {
  'me.read': '读取本人信息',
  'auth.registered': '注册激活',
  'auth.password_changed': '修改密码',
  'auth.password_reset': '重置密码',
  'scheme.read': '读取当前方案',
  'scheme.published': '发布方案',
  'scheme.updated': '修改方案草稿',
  'scheme.created': '新建方案草稿',
  'window.updated': '修改时间线设置',
  'window.capability_updated': '改能力开关',
  'submission.draft_created': '建草稿',
  'submission.submitted': '提交材料',
  'submission.updated': '修改提交',
  'submission.deleted': '删除提交',
  'submission.read': '查看提交',
  'submission.list': '浏览提交列表',
  'submission.admin_list': '管理端浏览提交',
  'submission.arbitrated': '仲裁定分',
  'submission.admin_granted': '班管直接加分',
  'evidence.presigned': '申请上传佐证',
  'evidence.completed': '佐证上传完成',
  'evidence.deleted': '删除佐证',
  'evidence.url_issued': '查看佐证',
  'review.decided': '提交审核结论',
  'review.revised': '修改审核结论',
  'review.task_read': '打开审核任务',
  'review.task_list': '浏览审核任务',
  'base_score.recorded': '录入基础/扣分项',
  'base_score.list': '查看基础/扣分项',
  'appeal.filed': '发起申诉',
  'appeal.withdrawn': '撤回申诉修改',
  'appeal.rereviewed': '提交申诉复评',
  'appeal.resolved': '复评一致结案',
  'appeal.escalated': '升给管理员终裁',
  'appeal.final': '申诉终裁',
  'appeal.read': '查看申诉',
  'objection.created': '新建扣分/异议提案',
  'objection.submitted': '提交提案待终裁',
  'objection.withdrawn': '撤回提案',
  'objection.applied': '提案终裁生效',
  'objection.dismissed': '驳回提案',
  'objection.read': '查看提案',
  'seal.confirmed': '确认封存',
  'seal.auto': '到期自动封存',
  'seal.unsealed': '解除封存',
  'seal.reminder_requested': '提醒未封存者',
  'result.confirmed': '本人确认当前成绩',
  'result.auto_confirmed': '超时自动确认当前成绩',
  'result.scheduled_confirmed': '定时强制确认当前成绩',
  'result.schedule_updated': '设置定时强制确认',
  'result.schedule_paused': '暂停定时强制确认',
  'result.schedule_completed': '执行定时强制确认',
  'dispatch.previewed': '预览分发',
  'dispatch.completed': '执行分发',
  'scorecard_audit.reassigned': '改派整表终审',
  'dispatch.reassigned': '人工改派',
  'dispatch.auto_changed': '开关自动分发',
  'dispatch.reviewer_paused': '暂停/恢复审核人',
  'gpa.imported': '导入专业素质分',
  'gate.forced': '强制开闸',
  'settlement.completed': '执行结算',
  'export.requested': '请求导出',
  'knowledge.document_read': '查看知识文件',
  'knowledge.source_url_issued': '下载引用原件',
  'class_resource.list': '查看班级评定文件',
  'class_resource.url_issued': '下载班级评定文件',
  'agent.attachment_presigned': '添加 Agent 图片',
  'agent.attachment_completed': '完成 Agent 图片上传',
  'agent.attachment_deleted': '移除未发送的 Agent 图片',
  'agent.attachment_url_issued': '查看 Agent 图片',
  'agent.message_created': '向 Agent 提问',
  'whitelist.imported': '导入白名单',
  'whitelist.added': '新建名单学生',
  'whitelist.deleted': '移出名单成员',
  'user.role_changed': '变更角色',
  'user.status_changed': '停用/启用账号',
  'user.account_initialized': '初始化学生账号',
  'user.password_reset_by_admin': '管理员重置密码',
  'audit_log.read': '查看审计日志',
  'view.changed': '切换视角',
}

export default function AdminAudit() {
	const initial = new URLSearchParams(window.location.search)
	const say = useApp((state) => state.say)
	const initialTab = initial.get('auditTab')
	const [tab, setTab] = useState<(typeof TABS)[number]['key']>(initialTab === 'read' || initialTab === 'write' ? initialTab : 'all')
	const [q, setQ] = useState(initial.get('auditActor') ?? '')
	const [action, setAction] = useState(initial.get('auditAction') ?? '')
	const [resourceType, setResourceType] = useState(initial.get('auditResource') ?? '')
	const [from, setFrom] = useState(initial.get('auditFrom') ?? '')
	const [to, setTo] = useState(initial.get('auditTo') ?? '')
	const [page, setPage] = useState(Math.max(1, Number(initial.get('auditPage')) || 1))
	const [exporting, setExporting] = useState(false)

	useEffect(() => {
		const params = new URLSearchParams(window.location.search)
		const values: Record<string, string> = { auditTab: tab, auditActor: q, auditAction: action, auditResource: resourceType, auditFrom: from, auditTo: to, auditPage: String(page) }
		for (const [key, value] of Object.entries(values)) {
			if (value && value !== 'all' && value !== '1') params.set(key, value)
			else params.delete(key)
		}
		const url = `${window.location.pathname}${params.size ? `?${params}` : ''}${window.location.hash}`
		window.history.replaceState(null, '', url)
	}, [action, from, page, q, resourceType, tab, to])

  /* actor 过滤交给后端（它按 sid/姓名 ILIKE 匹配），读写分类在前端做。 */
	const log = useAuditLog({ actor: q.trim() || undefined, action: action.trim() || undefined, resourceType: resourceType.trim() || undefined, from: from ? new Date(from).toISOString() : undefined, to: to ? new Date(to).toISOString() : undefined, page, page_size: 100 })

  const all = log.data?.items ?? []
  const rows = all.filter((r) => (tab === 'all' ? true : tab === 'read' ? isRead(r.action) : !isRead(r.action)))
  const reads = all.filter((r) => isRead(r.action)).length

	const exportAudit = async () => {
		setExporting(true)
		try {
			const cutoff = to ? new Date(to).toISOString() : new Date().toISOString()
			const fetchPage = (current: number) => {
				const params = new URLSearchParams({ page: String(current), page_size: '200', to: cutoff })
				if (q.trim()) params.set('actor', q.trim())
				if (action.trim()) params.set('action', action.trim())
				if (resourceType.trim()) params.set('resourceType', resourceType.trim())
				if (from) params.set('from', new Date(from).toISOString())
				return api.get<Page<AuditEntry>>(`/admin/audit-log?${params}`)
			}
			const first = await fetchPage(1)
			if (first.total > 10000) throw new Error('匹配记录超过 10,000 条，先把时间或动作范围缩小一点再导')
			const items = [...first.items]
			for (let current = 2; current <= Math.ceil(first.total / 200); current++) items.push(...(await fetchPage(current)).items)
			const selected = items.filter((row) => tab === 'all' ? true : tab === 'read' ? isRead(row.action) : !isRead(row.action))
			downloadAuditCSV(selected)
			say(`已导出 ${selected.length} 条筛选后的审计记录`)
		} catch (error) {
			say(userErrorMessage(error, '审计日志导出失败，请稍后重试'))
		} finally {
			setExporting(false)
		}
	}

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="AUDIT LOG"
        title="审计日志"
        desc="谁看了什么、谁改了什么"
		side={<Btn disabled={exporting || (log.data?.total ?? 0) === 0} onClick={() => void exportAudit()}>{exporting ? '导出中…' : '导出筛选结果 CSV'}</Btn>}
      />

      <StatGrid cols={4}>
        <Stat en="总条目" value={String(log.data?.total ?? 0)} unit="条" note="本班全部审计记录" />
        <Stat en="本页写操作" value={String(all.length - reads)} unit="条" note="状态迁移、分发、仲裁、导出" />
        <Stat en="本页读操作" value={String(reads)} unit="条" note="佐证查看、列表浏览" />
        <Stat en="保留期" value="按月归档" note="归档到异地存储，本地只留最近 12 个月" />
      </StatGrid>

      <div style={{ padding: '26px 0 14px' }}>
        <Sub
          title="日志"
          actions={
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              <input
                className="hv-bfg"
                value={q}
                onChange={(e) => {
                  setQ(e.target.value)
                  setPage(1)
                }}
                placeholder="按操作人学号或姓名搜索"
                style={{ ...fieldStyle, width: 220, fontSize: 12.5, padding: '6px 0' }}
              />
			  <input value={action} onChange={(e) => { setAction(e.target.value); setPage(1) }} placeholder="动作，如 submission" style={{ ...fieldStyle, width: 180, fontSize: 12.5 }} />
			  <input value={resourceType} onChange={(e) => { setResourceType(e.target.value); setPage(1) }} placeholder="资源类型" style={{ ...fieldStyle, width: 140, fontSize: 12.5 }} />
			  <input type="datetime-local" value={from} onChange={(e) => { setFrom(e.target.value); setPage(1) }} aria-label="开始时间" style={{ ...fieldStyle, width: 190, fontSize: 12 }} />
			  <input type="datetime-local" value={to} onChange={(e) => { setTo(e.target.value); setPage(1) }} aria-label="结束时间" style={{ ...fieldStyle, width: 190, fontSize: 12 }} />
			  <Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={(value) => { setTab(value); setPage(1) }} pad="4px 12px" fs={12} />
            </div>
          }
        />
      </div>

      {log.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty title="没有匹配的记录" desc="换个关键词或者筛选条件。审计日志只能往后加，删不掉。" />
      ) : (
        <>
          <Table cols="120px 92px 100px 64px minmax(200px,2fr) 130px">
            <THead cells={['时间', '操作人', '身份', '类型', '动作', '对象']} />
            {rows.map((r) => (
              <TRow
                key={r.id}
                cells={[
                  <span key="a" style={mono('11.5px', '.02em')}>{f.dateTime(r.createdAt)}</span>,
                  <span key="b" style={{ color: 'var(--fg)', fontWeight: 500 }}>{r.actor ?? '系统'}</span>,
                  <span key="c">{r.actorRole === 'system' ? '系统' : ROLE_LABEL[r.actorRole as Role] ?? r.actorRole}</span>,
                  <Pill key="d" tone={isRead(r.action) ? 'idle' : 'warn'}>{isRead(r.action) ? '读取' : '写入'}</Pill>,
                  <span key="e" style={{ fontSize: 12.5, color: 'var(--fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {ACTION_LABEL[r.action] ?? r.action}
                  </span>,
                  <span key="f" style={{ fontSize: 12, color: 'var(--fg3)' }}>
                    {r.resourceType}
                    {r.resourceId ? ` #${r.resourceId}` : ''}
                  </span>,
                ]}
              />
            ))}
          </Table>

          <div style={{ display: 'flex', alignItems: 'center', gap: 12, paddingTop: 18 }}>
            <Btn disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>上一页</Btn>
            <span style={{ ...mono('11.5px', '0') }}>
              第 {page} 页 / 共 {Math.max(1, Math.ceil((log.data?.total ?? 0) / 100))} 页
            </span>
            <Btn disabled={page * 100 >= (log.data?.total ?? 0)} onClick={() => setPage((p) => p + 1)}>下一页</Btn>
          </div>
        </>
      )}

      <div style={{ paddingTop: 26 }}>
        <Note>日志按时间一条条往后加。用上面的条件筛，或者导出来看。</Note>
      </div>
    </div>
  )
}

function downloadAuditCSV(items: AuditEntry[]) {
	const quote = (value: unknown) => `"${String(value ?? '').replaceAll('"', '""')}"`
	const rows = items.map((item) => [item.createdAt, item.actorSid ?? '', item.actor ?? '系统', item.actorRole, item.action, item.resourceType, item.resourceId ?? '', item.ip ?? ''].map(quote).join(','))
	const csv = ['time,actor_sid,actor_name,actor_role,action,resource_type,resource_id,ip', ...rows].join('\n')
	const url = URL.createObjectURL(new Blob(['\uFEFF' + csv], { type: 'text/csv;charset=utf-8' }))
	const link = document.createElement('a')
	link.href = url
	link.download = `audit-${new Date().toISOString().slice(0, 10)}.csv`
	document.body.append(link)
	link.click()
	link.remove()
	URL.revokeObjectURL(url)
}
