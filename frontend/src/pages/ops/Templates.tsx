import { readableErrorMessage } from '@/api/errorMessages'
/* 运维超管 · 模板库。平台级评分模板的 JSON 导入、上架与下架。

   模板只是"新班级开局时可以复制的一份 JSON"。上架后各班拷走自己改，
   平台不追踪也不同步——否则模板一改，所有班的历史结算口径都会跟着变。

   模板只保存方案名称、权重与评分树，不保存任何班级的时间、业务开关、评优比例或版本。
   既可由班级管理员共享，也可由运维导入经完整校验的 JSON。模板内容不原地改；
   要调整时由班级管理员复制为本班草稿，避免影响已经复制走的方案。 */

import { useRef, useState } from 'react'
import { Btn, Empty, Note, PageHead, Pill, Seg, Stat, StatGrid, Sub, Table, THead, TRow, TextBtn } from '@/components/ui'
import { fieldStyle, mono } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { useOpsActions, useTemplates, useTemplateShareRequests } from '@/api/queries'

const TABS = [
  { key: 'all', label: '全部' },
  { key: 'active', label: '已上架' },
  { key: 'retired', label: '已下架' },
] as const

export default function OpsTemplates() {
  const say = useApp((s) => s.say)
  const [tab, setTab] = useState<(typeof TABS)[number]['key']>('all')
  const templates = useTemplates()
	const requests = useTemplateShareRequests()
	const { importTemplate, retireTemplate, restoreTemplate, reviewTemplateShare } = useOpsActions()
  const fileRef = useRef<HTMLInputElement>(null)
  const [importName, setImportName] = useState('')
  const [importConfig, setImportConfig] = useState<unknown | null>(null)
  const [fileName, setFileName] = useState('')

  const all = templates.data?.items ?? []
  const rows = all.filter((t) => (tab === 'all' ? true : tab === 'active' ? t.active : !t.active))

  async function readTemplate(file?: File) {
    if (!file) return
    if (file.size > 2 * 1024 * 1024) {
      say('模板 JSON 不能超过 2 MB')
      return
    }
    try {
      const parsed = JSON.parse(await file.text()) as { schemeName?: unknown }
      setImportConfig(parsed)
      setFileName(file.name)
      setImportName(typeof parsed.schemeName === 'string' ? parsed.schemeName : file.name.replace(/\.json$/i, ''))
      say(`已读取 ${file.name}。名称确认好就能上架`)
    } catch {
      setImportConfig(null)
      setFileName('')
      say('文件不是合法的 JSON')
    }
  }

  function submitTemplate() {
    if (!importConfig || !importName.trim()) return
    importTemplate.mutate(
      { name: importName.trim(), config: importConfig },
      {
        onSuccess: (result) => {
          setImportConfig(null)
          setImportName('')
          setFileName('')
          say(result.updated ? `已更新「${result.name}」` : result.restored ? `已重新上架「${result.name}」` : result.created ? `已上架「${result.name}」` : `「${result.name}」已在模板库中`)
        },
        onError: (e) => say(e instanceof ApiError ? (typeof e.detail === 'string' ? `${e.message}：${readableErrorMessage(e.detail, '请检查模板格式、权重和分值范围')}` : e.message) : '模板上架失败'),
      },
    )
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en="TEMPLATES"
        title="模板库"
        desc="管理平台模板与班级共享申请。"
        side={<Pill tone="ok">{all.filter((t) => t.active).length} 个已上架</Pill>}
      />

      <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '16px 18px', marginBottom: 24, display: 'flex', alignItems: 'flex-end', gap: 12, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 220, flex: '1 1 320px' }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg2)' }}>导入 JSON 模板</span>
          <input
            className="hv-bfg"
            value={importName}
            disabled={!importConfig}
            onChange={(event) => setImportName(event.target.value)}
            placeholder="先选择模板 JSON 文件"
            style={{ ...fieldStyle, background: 'var(--bg)' }}
          />
          <span style={{ fontSize: 11.5, color: 'var(--fg3)' }}>{fileName || '只留评分结构。班级自己的运行参数不会进模板库'}</span>
        </div>
        <input
          ref={fileRef}
          type="file"
          accept="application/json,.json"
          style={{ display: 'none' }}
          onChange={(event) => {
            void readTemplate(event.target.files?.[0])
            event.target.value = ''
          }}
        />
        <Btn onClick={() => fileRef.current?.click()}>选择 JSON</Btn>
        <Btn primary disabled={!importConfig || !importName.trim() || importTemplate.isPending} onClick={submitTemplate}>
          {importTemplate.isPending ? '校验并上架中…' : '校验并上架'}
        </Btn>
      </div>

      <StatGrid cols={3}>
        <Stat en="已上架" value={String(all.filter((t) => t.active).length)} unit="个" note="新建评分方案时可选" />
        <Stat en="已下架" value={String(all.filter((t) => !t.active).length)} unit="个" note="以后不出现在选择列表里。已经复制走的不受影响" />
        <Stat en="总计" value={String(all.length)} unit="个" note="来自各班共享" />
      </StatGrid>

	  <div style={{ padding: '24px 0 4px' }}>
		<Sub title="班级共享申请" note="审核通过后进入平台模板库" />
		{(requests.data?.items ?? []).filter((item) => item.status === 'pending').length === 0 ? <Empty title="没有待审核申请" desc="班级把已发布的方案共享出来，就会出现在这里。" /> : <Table cols="120px minmax(180px,1.4fr) minmax(160px,1fr) 130px 210px">
		  <THead cells={['班级', '方案', '申请时间', '状态', '操作']} />
		  {(requests.data?.items ?? []).filter((item) => item.status === 'pending').map((item) => <TRow key={item.id} cells={[
			<span key="tenant">{item.tenantName} <small style={mono('10px', '0')}>#{item.tenantId}</small></span>,
			<span key="name" style={{ fontWeight: 600 }}>{item.name}</span>,
			<span key="time">{f.dateTime(item.createdAt)}</span>,
			<Pill key="status" tone="warn">待审核</Pill>,
			<span key="actions" style={{ display: 'flex', gap: 7 }}><Btn primary disabled={reviewTemplateShare.isPending} onClick={() => { if (window.confirm(`批准“${item.name}”进入平台模板库？`)) reviewTemplateShare.mutate({ id: item.id, decision: 'approved' }, { onSuccess: () => say('共享申请已批准并上架'), onError: (e) => say(e instanceof ApiError ? e.message : '审核失败') }) }}>批准</Btn><Btn danger disabled={reviewTemplateShare.isPending} onClick={() => { const reason = window.prompt('填写驳回原因（必填）')?.trim(); if (reason) reviewTemplateShare.mutate({ id: item.id, decision: 'rejected', reason }, { onSuccess: () => say('共享申请已驳回'), onError: (e) => say(e instanceof ApiError ? e.message : '审核失败') }) }}>驳回</Btn></span>,
		  ]} />)}
		</Table>}
	  </div>

      <div style={{ padding: '22px 0 14px' }}>
        <Sub
          title="模板"
          actions={<Seg items={TABS.map((t) => ({ key: t.key, label: t.label }))} value={tab} onChange={setTab} pad="4px 12px" fs={12} />}
        />
      </div>

      {templates.isLoading ? (
        <div className="load-bar"><span /></div>
      ) : rows.length === 0 ? (
        <Empty
          title="这一类下没有模板"
          desc="可以直接导 JSON，也可以让班级管理员共享过来。"
        />
      ) : (
        <Table cols="56px minmax(180px,2fr) 110px 120px 120px 90px">
          <THead cells={['ID', '模板名称', '来源班级', '上架时间', '下架时间', '操作']} />
          {rows.map((t) => (
            <TRow
              key={t.id}
              cells={[
                <span key="a" style={mono('11.5px', '.02em')}>{t.id}</span>,
                <span key="b" style={{ color: 'var(--fg)', fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{t.name}</span>,
                <span key="c" style={mono('11px', '.02em')}>{t.sourceTenantId ?? '—'}</span>,
                <span key="d" style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{f.date(t.createdAt)}</span>,
                <span key="e" style={{ fontSize: 12.5, color: 'var(--fg3)' }}>{f.date(t.retiredAt)}</span>,
                <span key="f">
                  {t.active ? (
                    <TextBtn
                      tone="var(--red)"
                      onClick={() =>
                        retireTemplate.mutate(t.id, {
                          onSuccess: () => say(`已下架「${t.name}」· 已复制过的班级不受影响`),
                          onError: (e) => say(e instanceof ApiError ? e.message : '下架失败'),
                        })
                      }
                    >
                      下架
                    </TextBtn>
                  ) : (
                    <TextBtn
                      disabled={restoreTemplate.isPending}
                      onClick={() =>
                        restoreTemplate.mutate(t.id, {
                          onSuccess: () => say(`已重新上架「${t.name}」`),
                          onError: (e) => say(e instanceof ApiError ? e.message : '重新上架失败'),
                        })
                      }
                    >
                      重新上架
                    </TextBtn>
                  )}
                </span>,
              ]}
            />
          ))}
        </Table>
      )}

      <div style={{ paddingTop: 26 }}>
        <Note>模板里只有评分规则。复制到班里，时间那些还用那个班自己的设置。下架不影响已复制的。</Note>
      </div>
    </div>
  )
}
