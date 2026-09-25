/* 运维超管 · 备份与恢复。

   备份本身不稀奇，恢复演练才是。没演练过的备份等于没有备份——
   所以这一页把"最近一次恢复演练"放在和备份同等的位置上。 */

import { useEffect, useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Row, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, TRow } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useBackupRemote, useBackups, useLifecycle, useOpsActions } from '@/api/queries'
import type * as T from '@/api/types'
import type { LifecyclePolicy } from '@/api/types'
import '@/styles/mobile-ops.css'

const KIND_LABEL: Record<string, string> = {
  backup: '全量备份',
  full: '全量备份',
  incremental: '增量备份',
  restore_drill: '恢复演练',
  remote_push: '远程副本',
}

const STATUS_META: Record<string, { label: string; tone: 'ok' | 'warn' | 'bad' | 'idle' }> = {
  complete: { label: '已完成', tone: 'ok' },
  running: { label: '进行中', tone: 'warn' },
  queued: { label: '排队中', tone: 'idle' },
  failed: { label: '失败', tone: 'bad' },
	expired: { label: '已过期清理', tone: 'idle' },
}

const LIFECYCLE_FIELDS: { key: Exclude<keyof LifecyclePolicy, 'backupEnabled'>; name: string; unit: string; type?: 'time' }[] = [
	{ key: 'backupSchedule', name: '每日备份时间', unit: 'Asia/Shanghai', type: 'time' },
	{ key: 'backupRetentionDays', name: '备份保留期', unit: '天' },
	{ key: 'exportRetentionDays', name: '导出文件保留期', unit: '天' },
	{ key: 'knowledgeDeleteGraceHours', name: '知识库删除宽限', unit: '小时' },
	{ key: 'agentAttachmentGraceHours', name: '未发送 Agent 附件宽限', unit: '小时' },
	{ key: 'storageReconcileMinutes', name: '存储全量校准间隔', unit: '分钟' },
	{ key: 'knowledgeMaxPdfPages', name: '知识 PDF 最大页数', unit: '页' },
	{ key: 'knowledgeMaxArchiveMembers', name: '压缩包最大成员数', unit: '个' },
	{ key: 'knowledgeMaxArchiveMb', name: '压缩包解压上限', unit: 'MB' },
	{ key: 'knowledgeMaxArchiveRatio', name: '压缩比上限', unit: '倍' },
	{ key: 'knowledgeMaxArchiveDepth', name: '目录递归深度', unit: '层' },
	{ key: 'knowledgeMaxExtractedTextMb', name: '单文件提取文本上限', unit: 'MB' },
	{ key: 'knowledgeConverterTimeoutSeconds', name: '本地转换超时', unit: '秒' },
]

/* 远程副本（异地备份）。
   本地那两份在同一台机器上，机器坏了就一起没。这一块把每天的备份加密后推到
   机房之外的一个云桶——腾讯 COS、阿里 OSS、Cloudflare R2、AWS S3 都能用。 */
function RemoteBackupCard() {
	const say = useApp((s) => s.say)
	const remote = useBackupRemote()
	const { updateBackupRemote, clearBackupRemote, testBackupRemote, parseBucketUrl } = useOpsActions()
	const [form, setForm] = useState<T.BackupRemoteConfig | null>(null)
	const [link, setLink] = useState('')
	const [accessKey, setAccessKey] = useState('')
	const [secretKey, setSecretKey] = useState('')
	useEffect(() => { if (remote.data?.config && !form) setForm(remote.data.config) }, [remote.data?.config, form])

	if (remote.isLoading || !form) return <div className="load-bar"><span /></div>
	const state = remote.data
	const set = (patch: Partial<T.BackupRemoteConfig>) => setForm({ ...form, ...patch })
	const credentialsReady = (state?.accessKeySet && state?.secretKeySet) || (accessKey !== '' && secretKey !== '')

	const save = () => {
		const patch: T.BackupRemoteUpdate = { ...form }
		// 留空表示不改动已保存的凭据；要清空得用下面那个「清空配置」。
		if (accessKey !== '') patch.accessKey = accessKey
		if (secretKey !== '') patch.secretKey = secretKey
		updateBackupRemote.mutate(patch, {
			onSuccess: () => { setAccessKey(''); setSecretKey(''); say('远程副本配置已保存') },
			onError: (e) => say(e instanceof ApiError ? e.message : '保存失败'),
		})
	}

	return (
		<div style={{ padding: '30px 0 0' }}>
			<Sub title="远程副本" note="每天的备份加密后推到机房外的云桶" />

			{state && !state.secretKeyReady && (
				<div style={{ paddingBottom: 14 }}>
					<Note tone="warn">服务端没配 BACKUP_REMOTE_SECRET_KEY，桶的密钥存不进来。先在部署环境里加上这一项。</Note>
				</div>
			)}
			{state && !state.recipientReady && (
				<div style={{ paddingBottom: 14 }}>
					<Note tone="warn">服务端没配 BACKUP_REMOTE_RECIPIENT（age 公钥），备份加不了密，也就传不上去。</Note>
				</div>
			)}

			<div style={{ display: 'flex', gap: 9, flexWrap: 'wrap', paddingBottom: 16 }}>
				<input
					aria-label="备份桶链接"
					value={link}
					onChange={(e) => setLink(e.target.value)}
					placeholder="粘桶链接，比如 https://名字-125xxx.cos.ap-guangzhou.myqcloud.com"
					style={{ ...fieldStyle, flex: '1 1 260px', minWidth: 0 }}
				/>
				<Btn
					disabled={link.trim() === '' || parseBucketUrl.isPending}
					onClick={() => parseBucketUrl.mutate(link.trim(), {
						onSuccess: (parsed) => { setForm({ ...form, ...parsed }); say('已按链接填好下面几项，请核对') },
						onError: (e) => say(e instanceof ApiError ? e.message : '这条链接认不出来'),
					})}
				>
					{parseBucketUrl.isPending ? '解析中…' : '按链接填写'}
				</Btn>
			</div>

			<div className="ops-auto-form-grid">
				{([
					{ key: 'endpoint', name: '桶地址', hint: '只填主机名，不带 https://' },
					{ key: 'bucket', name: '桶名', hint: '' },
					{ key: 'region', name: '地域', hint: '认不出就自己填，填错多半报 403' },
					{ key: 'prefix', name: '对象前缀', hint: '可留空' },
				] as const).map((field) => (
					<label key={field.key} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
						<span style={{ fontSize: 11.5, color: 'var(--fg2)' }}>{field.name}</span>
						<input value={form[field.key]} onChange={(e) => set({ [field.key]: e.target.value })} style={fieldStyle} />
						{field.hint && <small style={{ color: 'var(--fg3)' }}>{field.hint}</small>}
					</label>
				))}
				<label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
					<span style={{ fontSize: 11.5, color: 'var(--fg2)' }}>桶的 AccessKey</span>
					<input
						className="hv-bfg" type="password" autoComplete="new-password" value={accessKey}
						onChange={(e) => setAccessKey(e.target.value)}
						placeholder={state?.accessKeySet ? '已保存 · 留空表示不改动' : '填写 AccessKey'}
						style={fieldStyle}
					/>
				</label>
				<label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
					<span style={{ fontSize: 11.5, color: 'var(--fg2)' }}>桶的 SecretKey</span>
					<input
						className="hv-bfg" type="password" autoComplete="new-password" value={secretKey}
						onChange={(e) => setSecretKey(e.target.value)}
						placeholder={state?.secretKeySet ? '已保存 · 留空表示不改动' : '填写 SecretKey'}
						style={fieldStyle}
					/>
				</label>
				<label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
					<span style={{ fontSize: 11.5, color: 'var(--fg2)' }}>远程保留期</span>
					<span style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
						<input
							type="number" min={1} max={3650} value={String(form.retentionDays)}
							onChange={(e) => set({ retentionDays: Number(e.target.value) })}
							style={{ ...fieldStyle, minWidth: 0, flex: 1 }}
						/>
						<small style={{ color: 'var(--fg3)' }}>天</small>
					</span>
				</label>
			</div>

			<div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', padding: '14px 0 4px' }}>
				{([
					{ key: 'useSsl', label: '用 HTTPS 连桶' },
					{ key: 'pathStyle', label: '路径式地址（R2、自建 MinIO 选它）' },
					{ key: 'enabled', label: '开启每天推送' },
				] as const).map((toggle) => (
					<label key={toggle.key} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12.5 }}>
						<input type="checkbox" checked={form[toggle.key]} onChange={(e) => set({ [toggle.key]: e.target.checked })} />
						{toggle.label}
					</label>
				))}
			</div>

			<div style={{ marginTop: 14, display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
				<Btn primary tone="ok" disabled={updateBackupRemote.isPending} onClick={save}>
					{updateBackupRemote.isPending ? '保存中…' : '保存配置'}
				</Btn>
				<Btn
					disabled={!credentialsReady || testBackupRemote.isPending}
					onClick={() => testBackupRemote.mutate(undefined, {
						onSuccess: () => say('测试通过 · 写入、读回、删除三步都成功'),
						onError: (e) => say(e instanceof ApiError ? e.message : '连不上这个桶'),
					})}
				>
					{testBackupRemote.isPending ? '测试中…' : '测试连接'}
				</Btn>
				<Btn danger disabled={clearBackupRemote.isPending} onClick={() => {
					if (!window.confirm('清空远程副本配置并停止推送？桶里已有的备份不会删。')) return
					clearBackupRemote.mutate(undefined, {
						onSuccess: () => { setForm(null); setAccessKey(''); setSecretKey(''); say('远程副本配置已清空') },
						onError: (e) => say(e instanceof ApiError ? e.message : '清空失败'),
					})
				}}>清空配置</Btn>
				<Btn onClick={() => { if (remote.data?.config) { setForm(remote.data.config); setAccessKey(''); setSecretKey('') } }}>撤销</Btn>
			</div>

			<div style={{ marginTop: 16 }}>
				<Note>
					测试连接跑在 API 进程里，它和备份进程走的不是同一张网。测试通过只说明密钥和桶是对的。
				</Note>
			</div>
			<div style={{ marginTop: 10 }}>
				<Note tone="warn">
					归档用 age 公钥加密，私钥不在这台机器上。私钥丢了，桶里这些备份就再也打不开。
				</Note>
			</div>
		</div>
	)
}

export default function OpsBackup() {
  const say = useApp((s) => s.say)
	const [page, setPage] = useState(1)
	const backups = useBackups({ page, page_size: 50 })
	const recentBackups = useBackups({ page: 1, page_size: 50 })
	const lifecycle = useLifecycle()
	const { restoreDrill, createBackup, updateLifecycle } = useOpsActions()
	const [policyEdit, setPolicyEdit] = useState<LifecyclePolicy | null>(null)
	useEffect(() => { if (lifecycle.data?.policy && !policyEdit) setPolicyEdit(lifecycle.data.policy) }, [lifecycle.data?.policy, policyEdit])

  const rows = backups.data?.items ?? []
  const policy = backups.data?.policy
	const remote = policy?.remote
	const recentRows = recentBackups.data?.items ?? []
	const drills = recentRows.filter((r) => r.kind === 'restore_drill')
  const lastDrill = drills.find((d) => d.status === 'complete') ?? drills[0]
	const lastBackup = recentRows.find((r) => r.kind !== 'restore_drill' && r.status === 'complete')
  const failed = rows.filter((r) => r.status === 'failed').length

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="BACKUP"
        title="备份与恢复"
        desc="备份计划、保留期和演练"
        side={
		  <span style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}><Btn primary disabled={createBackup.isPending || !policy?.enabled} onClick={() => createBackup.mutate(undefined, { onSuccess: () => say('全量备份已入队'), onError: (e) => say(e instanceof ApiError ? e.message : '发起失败') })}>{createBackup.isPending ? '入队中…' : '立即备份'}</Btn><Btn
            primary
            disabled={restoreDrill.isPending || !policy?.enabled || !lastBackup}
            onClick={() =>
              restoreDrill.mutate(lastBackup?.id, {
                onSuccess: () => say('恢复演练已排队 · 会在一个临时库里回放一遍，校验完就销毁'),
                onError: (e) => say(e instanceof ApiError ? e.message : '发起失败'),
              })
            }
          >
            {restoreDrill.isPending ? '发起中…' : '发起恢复演练'}
		  </Btn></span>
        }
      />

      <StatGrid cols={5}>
		<Stat en="备份策略" value={!policy?.enabled ? '基础设施不可用' : policy.automatic ? policy.schedule.split(' ')[1] : '仅手动'} note={policy?.automatic ? policy.schedule : '自动备份关了，但手动备份和恢复演练还能用'} />
        <Stat en="保留期" value={String(policy?.retentionDays ?? '—')} unit="天" note={remoteNote(policy)} />
        <Stat en="最近备份" value={lastBackup ? f.date(lastBackup.finishedAt ?? lastBackup.createdAt) : '无'} note={lastBackup ? '已完成' : '还没有成功的备份'} tone={lastBackup ? 'var(--ok)' : 'var(--red)'} />
        <Stat
          en="最近演练"
          value={lastDrill ? f.date(lastDrill.finishedAt ?? lastDrill.createdAt) : '从未'}
          note={lastDrill ? STATUS_META[lastDrill.status]?.label ?? lastDrill.status : '没演练过的备份不算备份'}
          tone={lastDrill ? undefined : 'var(--red)'}
        />
        <Stat
          en="最近远程副本"
          value={remote?.lastPushAt ? f.date(remote.lastPushAt) : remote?.enabled ? '等待中' : '未开启'}
          note={remoteStatusNote(remote)}
          tone={!remote?.enabled ? 'var(--red)' : remote.lastPushAt && !remote.lastPushOk ? 'var(--red)' : undefined}
        />
      </StatGrid>

      <div style={{ padding: '26px 0 0' }}>
		<Sub title="备份与演练记录" note={failed > 0 ? `本页 ${failed} 条失败 · 共 ${backups.data?.total ?? 0} 条` : `共 ${backups.data?.total ?? 0} 条 · 第 ${page} 页`} />
        {backups.isLoading ? (
          <div className="load-bar"><span /></div>
        ) : rows.length === 0 ? (
          <Empty title="还没有备份记录" desc="备份任务执行后会在这里显示。" />
        ) : (
		  <Table cols="90px 100px 86px 70px minmax(190px,1fr) 125px 150px">
			<THead cells={['任务 ID', '类型', '状态', '离机', '详情 / 失败原因', '完成时间', '操作']} />
            {rows.map((b) => (
              <TRow
                key={b.id}
                cells={[
                  <span key="a" style={mono('11px', '.02em')}>{b.id.slice(0, 8)}</span>,
                  <span key="b" style={{ color: 'var(--fg)' }}>{KIND_LABEL[b.kind] ?? b.kind}</span>,
                  <Pill key="c" tone={STATUS_META[b.status]?.tone ?? 'idle'}>{STATUS_META[b.status]?.label ?? b.status}</Pill>,
                  <span key="d" style={{ fontSize: 12.5, color: b.offsite ? 'var(--ok)' : 'var(--fg3)' }}>{b.offsite ? '是' : '否'}</span>,
				  <span key="e" style={{ fontSize: 11.5, color: b.status === 'failed' ? 'var(--red)' : 'var(--fg3)', overflowWrap: 'anywhere' }}>{backupDetail(b.detail, b.createdAt)}</span>,
				  <span key="f" style={mono('11.5px', '0')}>{f.dateTime(b.finishedAt ?? b.createdAt)}</span>,
				  <Btn key="g" disabled={b.kind !== 'backup' || b.status !== 'complete' || restoreDrill.isPending} onClick={() => { if (window.confirm(`使用备份 ${b.id.slice(0, 8)} 做隔离恢复演练？`)) restoreDrill.mutate(b.id, { onSuccess: () => say('指定备份的恢复演练已入队'), onError: (e) => say(e instanceof ApiError ? e.message : '发起失败') }) }}>演练此备份</Btn>,
                ]}
              />
            ))}
		  </Table>
        )}
		{(backups.data?.total ?? 0) > 50 && <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 12 }}><Btn disabled={page <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))}>上一页</Btn><Btn disabled={page * 50 >= (backups.data?.total ?? 0)} onClick={() => setPage((value) => value + 1)}>下一页</Btn></div>}
      </div>

	  <RemoteBackupCard />

	  {policyEdit && <div style={{ padding: '30px 0 0' }}>
		<Sub title="数据生命周期与转换上限" note="修改后立即生效" />
		<label style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '12px 0', borderTop: '1px solid var(--line2)', cursor: 'pointer' }}><input type="checkbox" checked={policyEdit.backupEnabled} onChange={(e) => setPolicyEdit({ ...policyEdit, backupEnabled: e.target.checked })} /><span style={{ fontSize: 12.5 }}>启用每日自动备份</span></label>
		<div className="ops-auto-form-grid">
		  {LIFECYCLE_FIELDS.map((item) => <label key={item.key} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}><span style={{ fontSize: 11.5, color: 'var(--fg2)' }}>{item.name}</span><span style={{ display: 'flex', alignItems: 'center', gap: 7 }}><input type={item.type ?? 'number'} min={1} value={String(policyEdit[item.key])} onChange={(e) => setPolicyEdit({ ...policyEdit, [item.key]: item.type === 'time' ? e.target.value : Number(e.target.value) })} style={{ ...fieldStyle, minWidth: 0, flex: 1 }} /><small style={{ color: 'var(--fg3)' }}>{item.unit}</small></span></label>)}
		</div>
		<div style={{ marginTop: 16, display: 'flex', gap: 8 }}><Btn primary disabled={updateLifecycle.isPending} onClick={() => updateLifecycle.mutate(policyEdit, { onSuccess: () => say('生命周期与转换策略已保存'), onError: (e) => say(e instanceof ApiError ? e.message : '保存失败') })}>保存策略</Btn><Btn onClick={() => lifecycle.data?.policy && setPolicyEdit(lifecycle.data.policy)}>撤销</Btn></div>
	  </div>}

      <Split cols="1fr 1fr">
        <SplitCol first>
          <div style={{ padding: '30px 0' }}>
            <Sub title="备份范围" />
            <Row label="PostgreSQL" value="全库逻辑备份，含业务库与运维库" />
            <Row label="对象存储" value="佐证与导出产物，按桶同步到离机" />
            <Row label="Redis" value="不备份 · 可从事件记录重建" />
            <Row label="策略" value={policy ? `${policy.schedule} · 保留 ${policy.retentionDays} 天` : '—'} />
            <Row label="异地" value={remote?.enabled ? `加密后推到 ${remote.bucket} · 留 ${remote.retentionDays} 天` : '没开'} />
            <div style={{ marginTop: 18 }}>
              <Note>
                缓存和消息队列能从事件记录重建，不备份。
              </Note>
            </div>
          </div>
        </SplitCol>

        <SplitCol>
          <div style={{ padding: '30px 0' }}>
            <Sub title="恢复演练" note="在隔离实例上回放，不碰生产" />
            <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.85, textWrap: 'pretty' }}>
              恢复演练验三件事：数据能否恢复、版本能否迁移、关键记录能否读出。
            </div>
            <div style={{ marginTop: 16 }}>
              <Note tone={lastDrill ? undefined : 'warn'}>
                {lastDrill
                  ? `最近一次演练在 ${f.dateTime(lastDrill.createdAt)}，状态 ${STATUS_META[lastDrill.status]?.label ?? lastDrill.status}。`
                  : '一次恢复演练都没做过。这些备份能不能用，现在还不知道。'}
              </Note>
            </div>
          </div>
        </SplitCol>
      </Split>
    </div>
  )
}

/* 三层副本各留多久，一句话说清：本地 → 同机第二份 → 云上那份。 */
function remoteNote(policy?: T.BackupState['policy']) {
	if (!policy) return '仅本地'
	if (policy.remote?.enabled) return `本地 ${policy.retentionDays} 天 · 云上 ${policy.remote.retentionDays} 天`
	return policy.offsite ? '本地两份 · 都在同一台机器上' : '仅本地一份'
}

function remoteStatusNote(remote?: T.BackupState['policy']['remote']) {
	if (!remote?.enabled) return '备份还没有离开这台机器'
	if (!remote.lastPushAt) return `目标桶 ${remote.bucket} · 还没推过`
	return remote.lastPushOk ? `已推到 ${remote.bucket}` : '最近一次推送失败'
}

function backupDetail(detail: unknown, createdAt: string) {
	if (!detail || typeof detail !== 'object') return '—'
	const value = detail as Record<string, unknown>
	if (typeof value.error === 'string') return value.error
	const parts: string[] = []
	if (typeof value.objectCount === 'number') parts.push(`${value.objectCount} 个对象`)
	if (typeof value.objectBytes === 'number') parts.push(f.bytes(value.objectBytes))
	if (typeof value.sourceBackupId === 'string') parts.push(`来源 ${value.sourceBackupId.slice(0, 8)}`)
	if (typeof value.migrationVersion === 'number') parts.push(`迁移 v${value.migrationVersion}`)
	if (typeof value.archiveBytes === 'number') parts.push(`归档 ${f.bytes(value.archiveBytes)}`)
	if (typeof value.bucket === 'string') parts.push(`桶 ${value.bucket}`)
	if (value.verified === true) parts.push('已回读校验')
	if (typeof value.pruneError === 'string') parts.push(`过期清理失败：${value.pruneError}`)
	if (typeof value.path === 'string') parts.push(value.path)
	if (typeof value.retentionDays === 'number') parts.push(`保留至 ${f.dateTime(new Date(new Date(createdAt).getTime() + value.retentionDays * 86400000).toISOString())}`)
	return parts.join(' · ') || '—'
}
