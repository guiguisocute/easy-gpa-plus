import { useRef, useState } from 'react'
import { createBonusGrantUpload, removeBonusGrantEvidence, uploadMarkdownAttachment, useBonusGrantAction, useBonusGrants, useReviewStudents, useScheme, useWindow } from '@/api/queries'
import type { BonusGrantInput, Evidence, ReviewStudent } from '@/api/types'
import { userErrorMessage } from '@/api/errorMessages'
import { ActivityTable } from '@/components/ActivityTable'
import { EnumOptionPicker } from '@/components/EnumOptionPicker'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { FilePicker } from '@/components/EvidenceUploader'
import { ItemGuide } from '@/components/ItemGuide'
import { SchemeTree } from '@/components/SchemeTree'
import { TierMatrix, wantsMatrix } from '@/components/TierMatrix'
import { Btn, Empty, Field, Note, PageHead, Pill, Seg, Sub, TextBtn } from '@/components/ui'
import { buildClaim, expected, scoreBounds } from '@/lib/claim'
import * as f from '@/lib/format'
import { claimableItems } from '@/lib/schemeTree'
import { validateEvidenceFile } from '@/lib/evidence'
import { fieldStyle, num } from '@/lib/style'
import { useApp } from '@/stores/app'
import '@/styles/mobile-pages.css'

const inputStyle = { ...fieldStyle, width: '100%', border: '1px solid var(--line)', padding: '11px 13px' }
const tabs = [{ key: 'create', label: '直接加分' }, { key: 'history', label: '加分记录' }]
type Confirmation = { input: BonusGrantInput; members: ReviewStudent[]; score: number; itemName: string }

export default function Bonus({collective = false}: {collective?: boolean}) {
  const scheme = useScheme()
  const roster = useReviewStudents(collective)
  const win = useWindow()
  const history = useBonusGrants(collective)
  const action = useBonusGrantAction(collective)
  const say = useApp((s) => s.say)
  const go = useApp((s) => s.go)
  const [tab, setTab] = useState('create')
  const [key, setKey] = useState<string | null>(null)
  const [qty, setQty] = useState('')
  const [level, setLevel] = useState(0)
  const [free, setFree] = useState('')
  const [title, setTitle] = useState('')
  const [note, setNote] = useState('')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null)
  const uploadId = useRef<string | null>(null)
  const uploadBusy = useRef(false)
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState('')
  const [evidence, setEvidence] = useState<Evidence[]>([])
  const [failedFiles, setFailedFiles] = useState<{ file: File; message: string }[]>([])

  const cfg = scheme.data
  const option = cfg?.categories.flatMap((category) => claimableItems(category).map((item) => ({ category, item }))).find(({ item }) => item.key === key)
  const rule = option?.item.scoreRule
  const preview = rule ? expected(rule, qty, level, free) : null
  const bounds = rule ? scoreBounds(rule) : null
  const members = roster.data?.items ?? []
  const visible = members.filter((m) => `${m.sid} ${m.name}`.toLowerCase().includes(search.trim().toLowerCase()))
  const chosen = members.filter((m) => selected.has(m.userId))
  const locked = !!win.data?.window.lockdown && Date.parse(win.data.window.lockdown) <= Date.now()
  const valid = !!option && !!preview && preview.final !== null && Number.isFinite(preview.final) && preview.final > 0 && preview.final <= 99999 &&
    !(preview.capped && (rule?.type === 'free' || rule?.type === 'enum')) && title.trim() !== '' &&
    [...note.trim()].length >= 4 && [...note.trim()].length <= 2000 && chosen.length > 0 && chosen.length <= 1000 &&
    !locked && !!win.data && !roster.isError && !uploading && failedFiles.length === 0

  async function upload(files: File[]) {
    if (uploadBusy.current || confirmation || locked || action.isPending || !option) return
    uploadBusy.current = true; setUploading(true)
    const failures = failedFiles.filter(row => !files.includes(row.file))
    for (const [index, file] of files.entries()) {
      try {
        const invalid = validateEvidenceFile(file, option.item.evidence)
        if (invalid) throw new Error(invalid)
        if (!uploadId.current) uploadId.current = (await createBonusGrantUpload(collective)).id
        setUploadProgress(`上传 ${index + 1}/${files.length}…`)
        const saved = await uploadMarkdownAttachment({ kind: 'note', owner: collective ? 'governance/bonus-grant-upload' : 'admin/bonus-grant-upload', id: uploadId.current }, file,
          pct => setUploadProgress(`上传 ${index + 1}/${files.length} · ${pct}%`))
        setEvidence(current => [...current, saved])
      } catch (error) { failures.push({ file, message: userErrorMessage(error, '上传失败，请重试') }) }
    }
    setFailedFiles(failures); setUploading(false); setUploadProgress(''); uploadBusy.current = false
  }
  async function removeEvidence(id: string) {
    if (!uploadId.current || uploadBusy.current || confirmation || locked || action.isPending) return
    uploadBusy.current = true; setUploading(true)
    try {
      await removeBonusGrantEvidence(uploadId.current, id, collective)
      setEvidence(current => current.filter(file => file.id !== id))
    } catch (error) { say(userErrorMessage(error, '移除失败，请重试')) }
    finally { setUploading(false); uploadBusy.current = false }
  }

  function chooseItem(next: string) {
    setKey(next); setQty(''); setLevel(0); setFree('')
  }
  function review() {
    if (!valid || !cfg || !option || !rule || preview?.final == null) return
    setConfirmation({
      input: { requestId: crypto.randomUUID(), schemeVersion: cfg.version, studentIds: chosen.map((m) => m.userId),
        ...(uploadId.current ? { uploadId: uploadId.current } : {}),
        category: option.category.key, itemKey: option.item.key, title: title.trim(), note: note.trim(), claim: buildClaim(rule, qty, level, free) },
      members: chosen, score: preview.final, itemName: `${option.category.name} · ${option.item.name}`,
    })
  }
  function submit() {
    if (!confirmation || action.isPending) return
    action.mutate(confirmation.input, {
      onSuccess: (result) => {
        say(collective ? '共同加分提案已发布，受益成员回避表决，通过后才记分' : `已为 ${result.count} 人各记入 ${f.score(result.score)} 分`)
        setConfirmation(null); setTitle(''); setNote(''); setSelected(new Set()); setTab('history')
        uploadId.current = null; setEvidence([]); setFailedFiles([])
      },
      onError: (error) => say(userErrorMessage(error, '加分失败，请重试；同一批次不会重复记分')),
    })
  }

  return <>
    <PageHead en={collective ? 'COLLECTIVE BONUS' : 'DIRECT BONUS'} title={collective ? '共同加分提案' : '加分台'} desc={collective ? '冻结依据与受益名单 · 受益成员回避 · 决议通过后执行' : '无争议事项，直接记分'} />
    <Seg items={collective ? [{key:'create',label:'发起提案'},{key:'history',label:'已执行记录'}] : tabs} value={tab} onChange={(next) => { if (!action.isPending && !uploading) setTab(next) }} />
    {tab === 'history' ? <section style={{ paddingTop: 24 }}>
      <Sub title="最近 100 批加分" note="后来改分的，以当前认定为准" actions={<TextBtn onClick={() => void history.refetch()}>刷新</TextBtn>} />
      {history.isLoading ? <div className="load-bar"><span /></div>
        : history.isError ? <Empty title="加分记录读取失败" desc="请刷新重试。" />
        : !history.data?.items.length ? <Empty title="还没有直接加分记录" desc="完成加分后，会在这里保留每批事项与成员明细。" />
        : history.data.items.map((batch) => <details key={batch.id} style={{ borderTop: '1px solid var(--line)', padding: '16px 0' }}>
          <summary style={{ cursor: 'pointer', lineHeight: 1.8, fontSize: 14 }}>
            {batch.title} · {batch.members.length} 人 · 每人原始加分 {f.score(batch.score)} 分 <span style={{ color: 'var(--fg3)', fontSize: 12 }}>{f.dateTime(batch.createdAt)}</span>
          </summary>
          {!!batch.evidence?.length && <section aria-label="本批加分佐证" style={{ paddingTop: 16 }}><Sub title="加分佐证" note={`${batch.evidence.length} 份 · 本批成员共用`} /><EvidenceFiles items={batch.evidence} /></section>}
          <p style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: 13, color: 'var(--fg2)' }}>{batch.note}</p>
          <div style={{ overflowX: 'auto' }}><table style={{ width: '100%', fontSize: 13, textAlign: 'left', borderCollapse: 'collapse' }}>
            <thead><tr>{['学号', '姓名', '小项', '当前认定分'].map((label) => <th key={label} style={{ padding: 10 }}>{label}</th>)}</tr></thead>
            <tbody>{batch.members.map((member) => <tr key={member.id} style={{ borderTop: '1px solid var(--line2)' }}>
              <td style={{ padding: 10 }}>{member.sid}</td><td style={{ padding: 10 }}>{member.name}</td><td style={{ padding: 10 }}>{member.itemName}</td><td style={{ padding: 10, ...num }}>{f.score(member.score)}</td>
            </tr>)}</tbody>
          </table></div>
        </details>)}
      {!collective && <div style={{ paddingTop: 18 }}><Btn onClick={() => go('admReviewProgress')}>前往审核明细纠正分数</Btn></div>}
    </section> : <>
      <div style={{ paddingTop: 20 }}><Note>{collective ? '本班在册成员都可列为受益人，含未注册者。受益人不进入本次表决，须至少三名无利益冲突的成员赞成。人数不足时不能自行记分。' : '记分马上生效，不用初审。举报台会标成「班管直接加分」。本班启用成员都能选，含未注册和你自己。'}</Note></div>
      {locked && <Note tone="warn">本班已全系统封锁，当前只能查看加分记录。</Note>}
      {scheme.isLoading || roster.isLoading || win.isLoading ? <div className="load-bar"><span /></div>
        : !cfg || scheme.isError || roster.isError || win.isError ? <Empty title="加分入口暂时不可用" desc="请刷新方案、名单与时间线后重试。" />
        : <fieldset disabled={!!confirmation || locked || action.isPending || uploading} style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}>
          <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '252px minmax(0,1fr)', borderTop: '1px solid var(--line)', marginTop: 20 }}>
            <SchemeTree config={cfg} selectedKey={key} onSelect={chooseItem} />
            <div style={{ minWidth: 0, padding: '22px 0 22px 24px' }}>
              {!option || !rule ? <Empty title="选择需要加分的小项" desc="选方案里的申报项或条件基础项。" /> : <>
                <ItemGuide name={option.item.name} item={option.item} />
                {option.item.activities?.length ? <ActivityTable activities={option.item.activities} defaultOpen /> : null}
                <div style={{ display: 'flex', flexDirection: 'column', gap: 20, paddingTop: 20 }}>
                  <Field label="统一事项名称" required><input aria-label="统一事项名称" maxLength={100} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="如：本学期班级志愿服务" style={inputStyle} /></Field>
                  {(rule.type === 'per_unit' || rule.type === 'threshold') && <Field label={`${rule.unit}数`} required hint={rule.type === 'per_unit' ? `每${rule.unit} ${rule.per} 分${rule.cap == null ? '' : `，本小项封顶 ${rule.cap} 分`}` : `达到 ${rule.minimum} ${rule.unit}记 ${rule.award} 分`}>
                    <input aria-label={`${rule.unit}数`} type="number" min={0} step="any" value={qty} onChange={(e) => setQty(e.target.value)} style={inputStyle} />
                  </Field>}
                  {rule.type === 'enum' && <>
                    {wantsMatrix(rule) && <TierMatrix rule={rule} picked={rule.options[level]?.label} />}
                    <EnumOptionPicker rule={rule} value={level} onChange={(next) => { setLevel(next); setFree(String(rule.options[next]?.score ?? '')) }} />
                  </>}
                  {(rule.type === 'enum' || rule.type === 'free') && <Field label="每人加分" required hint={`允许 ${bounds?.lo} — ${bounds?.hi} 分；直接记为认定分`}>
                    <input aria-label="每人加分" type="number" min={Math.max(0, bounds?.lo ?? 0)} max={bounds?.hi} step="0.001" value={free} onChange={(e) => setFree(e.target.value)} placeholder={rule.type === 'enum' ? `默认 ${rule.options[level]?.score ?? 0} 分` : '填写正分值'} style={inputStyle} />
                  </Field>}
                  <section aria-label="加分佐证上传">
                    <Sub title="加分佐证" note="选填 · 本批成员共用" />
                    <FilePicker title="上传本批加分的佐证" busy={uploading || !!confirmation || locked || action.isPending}
                      actionLabel={uploading ? uploadProgress || '正在处理附件…' : '选择文件'} onFiles={files => void upload(files)} />
                    <div style={{ paddingTop: 12 }}><EvidenceFiles items={evidence} readOnly={uploading || !!confirmation || locked || action.isPending} onRemove={id => void removeEvidence(id)} /></div>
                    {failedFiles.length > 0 && <div role="alert" style={{ paddingTop: 12 }}>
                      {failedFiles.map(({ file, message }, index) => <p key={index} style={{ color: 'var(--red)', fontSize: 13, overflowWrap: 'anywhere' }}>{file.name}：{message}</p>)}
                      <div style={{ display: 'flex', gap: 12 }}><Btn disabled={uploading} onClick={() => void upload(failedFiles.map(row => row.file))}>重试失败文件</Btn><TextBtn disabled={uploading} onClick={() => setFailedFiles([])}>移除失败文件</TextBtn></div>
                    </div>}
                  </section>
                  <Field label="统一加分依据" required hint="会出现在计分轨迹里。写清事项和条件。">
                    <textarea aria-label="统一加分依据" value={note} maxLength={2000} onChange={(e) => setNote(e.target.value)} rows={4} style={{ ...inputStyle, resize: 'vertical' }} />
                  </Field>
                  <Note tone={preview?.capped ? 'warn' : undefined}>
                    每人认定分：<strong style={num}>{f.score(preview?.final ?? null)}</strong> 分{preview?.capped && ' · 已触及小项分值范围，请核对'}。总成绩仍按方案封顶、互斥及权重计算。已有结算会失效，学生会看到更新后的成绩并可重新核对。
                  </Note>
                </div>
              </>}
            </div>
          </div>
          <section style={{ padding: '22px 0', borderTop: '1px solid var(--line)' }}>
            <Sub title="选择加分成员" note={`已选 ${chosen.length} / ${members.length} 人`} actions={<>
              <TextBtn onClick={() => setSelected(new Set([...selected, ...visible.map((m) => m.userId)]))}>全选当前结果</TextBtn>
              <TextBtn onClick={() => setSelected(new Set())}>清空选择</TextBtn>
            </>} />
            <input aria-label="搜索加分成员" type="search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="搜索学号或姓名" style={{ ...inputStyle, maxWidth: 360, marginBottom: 14 }} />
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(210px,1fr))', gap: 8, maxHeight: 360, overflowY: 'auto' }}>
              {visible.map((m) => <label key={m.userId} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: 12, border: `1px solid ${selected.has(m.userId) ? 'var(--red)' : 'var(--line)'}`, fontSize: 13, cursor: 'pointer' }}>
                <input type="checkbox" checked={selected.has(m.userId)} onChange={(e) => setSelected((old) => { const next = new Set(old); if (e.target.checked) next.add(m.userId); else next.delete(m.userId); return next })} />
                <span>{m.name} {m.self && <Pill>本人</Pill>}<small style={{ display: 'block', color: 'var(--fg3)', paddingTop: 4 }}>{m.sid}</small></span>
              </label>)}
            </div>
            {!visible.length && <Empty title="没有匹配的成员" />}
          </section>
          <Btn primary disabled={!valid} onClick={review}>核对 {chosen.length} 人的加分</Btn>
        </fieldset>}
      {confirmation && <section aria-label="确认本批加分" style={{ marginTop: 24, padding: 22, border: '1px solid var(--red)' }}>
        <Sub title="确认本批加分" note={`${confirmation.members.length} 人，每人 ${f.score(confirmation.score)} 分`} />
        <p style={{ fontSize: 14 }}>{confirmation.input.title} · {confirmation.itemName}</p>
        {evidence.length > 0 && <section aria-label="确认加分佐证"><Sub title="加分佐证" note={`${evidence.length} 份 · 将附到本批每位成员的记录`} /><EvidenceFiles items={evidence} /></section>}
        <p style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: 13 }}>{confirmation.input.note}</p>
        <div style={{ maxHeight: 200, overflowY: 'auto', fontSize: 13, lineHeight: 2 }}>{confirmation.members.map((m) => <div key={m.userId}>{m.name} · {m.sid}{m.self ? ' · 本人' : ''}</div>)}</div>
        <p style={{ fontSize: 12, color: 'var(--fg3)' }}>{collective ? '发布后依据与受益名单冻结，先讨论再表决。只有获得无利益冲突成员的多数支持，系统才会记分。' : '确认后一次性记分；请求重试不会重复加分。后续纠正可在审核明细处理，本人事项仍须回避终裁。'}</p>
        <div style={{ display: 'flex', gap: 12 }}>
          <Btn primary disabled={action.isPending || locked} onClick={submit}>{action.isPending ? '正在保存…' : collective ? '确认发布提案' : '确认直接加分'}</Btn>
          <Btn disabled={action.isPending} onClick={() => setConfirmation(null)}>返回修改</Btn>
        </div>
      </section>}
    </>}
  </>
}
