/* 专业素质分。班级管理员与共治成员共用这一页。

   这一项不经过提交与双评：由教务成绩加权得到，学生端只读、不可申诉。
   页面只显示"导入了什么"，不预览合成总分——总分与三好判定由结算器产出快照（§2.4），
   在这里再算一遍就等于有了第二套算法，两个数字对不上时没人说得清哪个算数。

   两种模式的差别只在最后一步：普通模式班管直接导入；共治模式把同一份表和正式原件
   一起挂成核验提案，发起人回避，表决通过后由系统执行同一个导入。覆盖名单、缺谁、
   模板长什么样，两边看到的是同一份，所以不另起一页。 */

import { useRef, useState } from 'react'
import { Btn, Empty, Field, Note, PageHead, Pill, Seg, Split, SplitCol, Stat, StatGrid, Sub, Table, THead, TRow } from '@/components/ui'
import { GpaTextInput } from '@/components/GpaTextInput'
import { FilePicker } from '@/components/EvidenceUploader'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { userErrorMessage } from '@/api/errorMessages'
import { createBonusGrantUpload, removeBonusGrantEvidence, uploadMarkdownAttachment, useGpa, useGpaActions } from '@/api/queries'
import { useGovernanceAction } from '@/api/governance'
import type { Evidence } from '@/api/types'

export default function AdminGpa({ collective = false }: { collective?: boolean } = {}) {
  const say = useApp((s) => s.say)
  const gpa = useGpa(collective)
  const { paste: pasteMutation, upload } = useGpaActions()
  const proposal = useGovernanceAction()
  const [text, setText] = useState('')
  const [basis, setBasis] = useState('')
  const [tab, setTab] = useState<'list' | 'import'>('list')
  const [mismatch, setMismatch] = useState<string | null>(null)
  const [files, setFiles] = useState<Evidence[]>([])
  const [busy, setBusy] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  const uploadId = useRef<string | null>(null)
  const requestId = useRef(crypto.randomUUID())

  if (gpa.isError) {
    return <Empty title="还没有已发布的方案" desc="先把方案发布出去，才能导专业素质分。" />
  }
  if (gpa.isLoading || !gpa.data) return <div className="load-bar"><span /></div>

  const d = gpa.data
  const lines = text.split('\n').map((l) => l.trim()).filter(Boolean).length
  const weight = d.weights.major ?? 0
  const pending = pasteMutation.isPending || upload.isPending || proposal.isPending || busy

  /* 后端在名单对不上时返回 422 + detail{unknownSids, nameMismatches}，
     把它摊开显示比只说一句"导入失败"有用得多。 */
  const onError = (e: unknown) => {
    if (e instanceof ApiError && e.detail && typeof e.detail === 'object') {
      const detail = e.detail as { unknownSids?: string[]; nameMismatches?: { sid: string; expected: string; actual: string; row: number }[] }
      const parts: string[] = []
      if (detail.unknownSids?.length) parts.push(`不在本班名单：${detail.unknownSids.join('、')}`)
      if (detail.nameMismatches?.length)
        parts.push(
          `姓名对不上：${detail.nameMismatches.map((m) => `第 ${m.row} 行 ${m.sid}（名单是「${m.expected}」，文件里是「${m.actual}」）`).join('；')}`,
        )
      setMismatch(parts.join('\n') || e.message)
      return
    }
    setMismatch(null)
    say(e instanceof ApiError ? e.message : '导入失败')
  }

  const onDone = (r: { imported: number; missing: { sid: string; name: string }[]; complete: boolean }) => {
    setText('')
    setMismatch(null)
    setTab('list')
    say(
      r.complete
        ? `已导入 ${r.imported} 条 · 全班都齐了 · 之前的结算结果已标为过期`
        : `已导入 ${r.imported} 条 · 还缺 ${r.missing.length} 人：${r.missing.slice(0, 3).map((m) => m.name).join('、')}${r.missing.length > 3 ? '…' : ''}`,
    )
  }

  /* 共治的原件走和共同加分同一条上传通道：先建一个上传单，再把文件挂上去。
     提案发布时后端核对这个上传单属于发起人且尚未使用过。 */
  const takeFiles = async (picked: File[]) => {
    if (busy) return
    setBusy(true)
    try {
      if (!uploadId.current) uploadId.current = (await createBonusGrantUpload(true)).id
      for (const file of picked) {
        const saved = await uploadMarkdownAttachment({ kind: 'note', owner: 'governance/bonus-grant-upload', id: uploadId.current }, file)
        setFiles((old) => [...old, saved])
      }
      requestId.current = crypto.randomUUID()
    } catch (error) {
      say(userErrorMessage(error, '上传失败，请重试'))
    } finally {
      setBusy(false)
    }
  }
  const dropFile = async (id: string) => {
    if (busy || !uploadId.current) return
    setBusy(true)
    try {
      await removeBonusGrantEvidence(uploadId.current, id, true)
      setFiles((old) => old.filter((file) => file.id !== id))
      requestId.current = crypto.randomUUID()
    } catch (error) {
      say(userErrorMessage(error, '移除失败，请重试'))
    } finally {
      setBusy(false)
    }
  }
  const propose = async () => {
    if (pending || !files.length || lines === 0) return
    setMismatch(null)
    try {
      await proposal.mutateAsync({
        path: '/proposals',
        body: {
          requestId: requestId.current,
          kind: 'ordinary',
          action: 'gpa',
          title: '核验并导入本周期专业素质分',
          body: basis,
          payload: { text, uploadId: uploadId.current },
        },
      })
      say('已发布核验提案，发起人回避本次表决')
      requestId.current = crypto.randomUUID()
      uploadId.current = null
      setFiles([])
      setText('')
      setBasis('')
      setTab('list')
    } catch (error) {
      setMismatch(userErrorMessage(error, '提案发布失败，请核对成绩表和原件'))
    }
  }

  return (
    <div style={{ animation: 'rise .28s ease both' }}>
      <PageHead
        en={collective ? 'GPA VERIFICATION' : 'GPA IMPORT'}
        title={collective ? '专业分核验' : '专业素质分'}
        desc={collective ? `核验来源与录入，按 ${weight} 计入总分；发起人回避本次表决` : `导入教务加权均分，按 ${weight} 计入总分`}
        side={
          <>
            <Pill tone={d.complete ? 'ok' : d.imported > 0 ? 'warn' : 'idle'}>
              {d.imported === 0 ? '未导入' : d.complete ? `已导入 · ${f.date(d.latestAt)}` : `部分导入 ${d.imported}/${d.total}`}
            </Pill>
            <Btn
              onClick={() => {
                const csv = 'sid,name,score\n' + d.items.map((r) => `${r.sid},${r.name},`).join('\n')
                const url = URL.createObjectURL(new Blob(['﻿' + csv], { type: 'text/csv;charset=utf-8' }))
                const a = document.createElement('a')
                a.href = url
                a.download = 'gpa-template.csv'
                a.click()
                URL.revokeObjectURL(url)
                say('模板下好了 · 填最后一列就行')
              }}
            >
              下载模板
            </Btn>
          </>
        }
      />

      <StatGrid cols={4}>
        <Stat
          en="覆盖人数"
          value={`${d.imported} / ${d.total}`}
          note="缺一人就无法开闸结算"
          tone={d.complete ? 'var(--ok)' : 'var(--red)'}
        />
        <Stat en="均分" value={f.score(d.average)} note="已导入部分的平均值" />
        <Stat en="权重" value={String(weight)} note="四项里占比最大的一项" />
        <Stat en="三好候选" value={`各项前 ${d.honorTopPercent}%`} note="四个维度均达线，结算后确定" />
      </StatGrid>

      <div style={{ padding: '26px 0 14px' }}>
        <Sub
          title={tab === 'list' ? '导入结果' : collective ? '发起核验提案' : '导入'}
          note={tab === 'list' ? '查看最近一次导入结果' : '每行一条：学号,均分 或 学号,姓名,均分'}
          actions={
            <Seg
              items={[
                { key: 'list', label: '名单' },
                { key: 'import', label: collective ? '发起核验' : '导入' },
              ]}
              value={tab}
              onChange={setTab}
              pad="4px 12px"
              fs={12}
            />
          }
        />
      </div>

      {tab === 'list' ? (
        <>
          <Table cols="108px 96px 96px minmax(140px,1fr) 120px">
            <THead cells={['学号', '姓名', '加权均分', '导入批次', '更新时间']} />
            {d.items.map((r) => (
              <TRow
                key={r.userId}
                bg={r.score === null ? 'var(--redBg)' : undefined}
                cells={[
                  <span key="a" style={mono('11.5px', '.02em')}>{r.sid}</span>,
                  <span key="b" style={{ color: 'var(--fg)', fontWeight: 500 }}>{r.name}</span>,
                  <span key="c" style={{ ...num, fontWeight: 600, color: r.score === null ? 'var(--red)' : 'var(--fg)' }}>
                    {r.score === null ? '缺' : f.score(r.score)}
                  </span>,
                  <span key="d" style={mono('11px', '0')}>{r.batch ? r.batch.slice(0, 8) : '—'}</span>,
                  <span key="e" style={{ fontSize: 12, color: 'var(--fg3)' }}>{f.dateTime(r.updatedAt)}</span>,
                ]}
              />
            ))}
          </Table>

          <div style={{ paddingTop: 26 }}>
            <Note tone={d.complete ? undefined : 'warn'}>
              {d.complete
                ? '全班成绩已导入，可以继续结算。'
                : `还有 ${d.total - d.imported} 人还没有成绩，结算闸门的第三个条件就过不了。`}
            </Note>
          </div>
        </>
      ) : (
        <Split cols="1.2fr 1fr">
          <SplitCol first>
            <div style={{ paddingBottom: 26 }}>
              {collective && (
                <div style={{ paddingBottom: 18 }}>
                  <Sub title="正式成绩原件" note="必传 · 表决时和粘贴的表格一起核对" />
                  <FilePicker title="上传正式成绩原件" busy={pending} onFiles={(picked) => void takeFiles(picked)} />
                  <div style={{ paddingTop: 12 }}>
                    <EvidenceFiles items={files} readOnly={pending} onRemove={(id) => void dropFile(id)} />
                  </div>
                </div>
              )}
              <GpaTextInput value={text} onChange={setText} disabled={pending} />
              {collective ? (
                <div style={{ paddingTop: 20, display: 'flex', flexDirection: 'column', gap: 18 }}>
                  <Field label="来源与用途" required hint="写清成绩来自哪份文件、什么时候出的。这段话会随提案公示。">
                    <textarea
                      aria-label="来源与用途"
                      value={basis}
                      onChange={(e) => setBasis(e.target.value)}
                      rows={4}
                      minLength={4}
                      maxLength={2000}
                      style={{ ...fieldStyle, resize: 'vertical' }}
                    />
                  </Field>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                    <Btn primary disabled={pending || !files.length || lines === 0 || [...basis.trim()].length < 4} onClick={() => void propose()}>
                      {proposal.isPending ? '发布中…' : `发布核验提案 · ${lines || 0} 条`}
                    </Btn>
                    <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>通过后由系统整批导入</span>
                  </div>
                </div>
              ) : (
                <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 14, flexWrap: 'wrap' }}>
                  <Btn
                    primary
                    disabled={lines === 0 || pasteMutation.isPending}
                    onClick={() => pasteMutation.mutate(text, { onSuccess: onDone, onError })}
                  >
                    {pasteMutation.isPending ? '导入中…' : `导入 ${lines || ''} 条`}
                  </Btn>
                  <input
                    ref={fileRef}
                    type="file"
                    accept=".csv,.xlsx"
                    style={{ display: 'none' }}
                    onChange={(e) => {
                      const file = e.target.files?.[0]
                      e.target.value = ''
                      if (file) upload.mutate(file, { onSuccess: onDone, onError })
                    }}
                  />
                  <Btn disabled={upload.isPending} onClick={() => fileRef.current?.click()}>
                    {upload.isPending ? '上传中…' : '选择 CSV / XLSX'}
                  </Btn>
                  <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>导入会覆盖上一次</span>
                </div>
              )}

              {mismatch && (
                <div role="alert" style={{ border: '1px solid var(--red)', background: 'var(--redBg)', padding: '13px 15px', marginTop: 16, fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.8, whiteSpace: 'pre-wrap' }}>
                  {mismatch}
                </div>
              )}
            </div>
          </SplitCol>
          <SplitCol>
            <div style={{ paddingBottom: 26, display: 'flex', flexDirection: 'column', gap: 16 }}>
              <Note tone="warn">
                {collective
                  ? '成绩表必须覆盖全班在册成员，含未注册和没有材料的人；少一行就发不出提案。'
                  : '先确认这是教务的最终成绩。中途覆盖会让已发出的排名和三好名单作废。'}
              </Note>
              <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.85 }}>
                学号必须属于本班，均分范围为 0—100；填了姓名要和名单对得上。有一行不对，整批都不导。
              </div>
              {collective && (
                <div style={{ fontSize: 12.5, color: 'var(--fg2)', lineHeight: 1.85 }}>
                  表决只核对来源与抄录是否一致，不能借表决改个人专业分。通过后系统按冻结的那份数据导入。
                </div>
              )}
            </div>
          </SplitCol>
        </Split>
      )}
    </div>
  )
}
