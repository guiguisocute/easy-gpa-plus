import { userErrorMessage } from '@/api/errorMessages'
/* 学生 · 提交材料。表单由方案配置树驱动渲染。

   期望分在前端实时算只是给学生一个预期，最终以后端结算器为准（§2.4 顺序写死、不可配置）。
   普通基础项与扣分项折叠只读；配置 studentClaim 的条件基础项会进入正常申报流程（§4.2）。

   佐证必须挂在一条已存在的记录上（对象键里带 submission-id），所以顺序是
   先存草稿拿到 id，再上传，最后提交。界面把这一步说清楚，而不是让上传按钮神秘地不可点。 */

import { useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Upload } from 'lucide-react'
import { Btn, Empty, Field, Note, PageHead, Pill, RequiredMark, Sub } from '@/components/ui'
import { EnumOptionPicker } from '@/components/EnumOptionPicker'
import { TierMatrix } from '@/components/TierMatrix'
import { wantsMatrix } from '@/lib/tierMatrix'
import { ActivityTable } from '@/components/ActivityTable'
import { EvidenceFiles } from '@/components/EvidenceFiles'
import { AIMaterialPanel } from '@/components/AIMaterialPanel'
import { FileDropzone } from '@/components/FileDropzone'
import { ItemGuide } from '@/components/ItemGuide'
import { MarkdownEditor } from '@/components/MarkdownEditor'
import { RuleSheet } from '@/components/RuleSheet'
import { SchemeTree } from '@/components/SchemeTree'
import { claimableItems, findCategoryBrief, findReadOnlySchemeItem } from '@/lib/schemeTree'
import { CategoryBrief } from '@/components/CategoryBrief'
import { filesFromDrop } from '@/lib/dropFiles'
import { fieldStyle, mono, num } from '@/lib/style'
import * as f from '@/lib/format'
import { useApp } from '@/stores/app'
import { ApiError } from '@/api/client'
import { K, uploadEvidence, useAIStatus, useScheme, useSubmission, useSubmissionActions, useSubmissions, useWindow } from '@/api/queries'
import type { CategoryKey } from '@/lib/types'
import type { Submission } from '@/api/types'
/* 期望分与佐证提示抽到 lib/claim，和方案编辑器的学生端预览共用同一份口径。 */
import { buildClaim, claimOptionLost, evidenceHint, expected, readClaim, scoreBounds } from '@/lib/claim'
import { evidenceAccept, validateEvidenceFile } from '@/lib/evidence'
import { submissionBlockReason } from '@/lib/submissionAvailability'
/* 本地暂存只保住键入手感，数据库草稿才是权威——两层的分工写在 lib/draftCache 顶部。 */
import { clearLocalDraft, localDraftItemKeys, readLocalDraft, writeLocalDraft } from '@/lib/draftCache'
import '@/styles/mobile-pages.css'

type UploadStatus = 'queued' | 'uploading' | 'ready' | 'error'

/** 一个小项的表单内容。本地暂存、数据库草稿、空白表单都归一到这个形状。 */
interface FormState {
  draftId: string | null
  title: string
  qty: string
  level: number
  free: string
  md: string
}

const BLANK_FORM: FormState = { draftId: null, title: '', qty: '', level: 0, free: '', md: '' }

/** 每次按键都写 localStorage 没必要，停手小半秒再落盘。 */
const AUTOSAVE_DELAY_MS = 400

interface UploadItem {
  key: string
  file: File
  status: UploadStatus
  progress: number
  error?: string
  /** 后端按文件的真实格式改了扩展名时的说明。不是错误，别红着显示。 */
  notice?: string
  evidenceId?: string
}

export default function StudentSubmit() {
  const say = useApp((s) => s.say)
  const go = useApp((s) => s.go)
  /* 「我的提交」里点「继续编辑」带过来的草稿 id。 */
  const openDraftId = useApp((s) => s.openDraftId)
  const agentHandoff = useApp((s) => s.agentHandoff)
  const setAgentHandoff = useApp((s) => s.setAgentHandoff)
  /* 本地暂存按学号分桶：机房共用一台机器时，不能让下一个人看到上一个人写了一半的材料。 */
  const sid = useApp((s) => s.user?.sid ?? '')
  const queryClient = useQueryClient()
  const scheme = useScheme()
  const win = useWindow({ live: true })
  const mine = useSubmissions()
  const aiStatus = useAIStatus()
  const actions = useSubmissionActions()

  const [selKey, setSelKey] = useState<string | null>(null)
  const [draftId, setDraftId] = useState<string | null>(null)
  const [title, setTitle] = useState('')
  const [qty, setQty] = useState('')
  const [level, setLevel] = useState(0)
  const [free, setFree] = useState('')
  const [md, setMd] = useState('')
  const [uploads, setUploads] = useState<UploadItem[]>([])
  /* 哪些小项在本机还留着没写完的东西。跟着自动暂存刷新，只用来在树上标点。 */
  const [localDraftKeys, setLocalDraftKeys] = useState<ReadonlySet<string>>(() => new Set<string>())
  const [draftBoxOpen, setDraftBoxOpen] = useState(false)
  const [rulesOpen, setRulesOpen] = useState(false)
  const [aiOpen, setAIOpen] = useState(false)
  const [draggingFiles, setDraggingFiles] = useState(false)
  const [draggedFileCount, setDraggedFileCount] = useState(0)
  const dragDepth = useRef(0)
  const fileInput = useRef<HTMLInputElement>(null)
  const folderInput = useRef<HTMLInputElement>(null)
  const draftIdRef = useRef<string | null>(null)
  const draftCreation = useRef<Promise<string | null> | null>(null)
  const uploadChain = useRef<Promise<void>>(Promise.resolve())
  const resumed = useRef<string | null>(null)

  const draft = useSubmission(draftId)
  const resume = useSubmission(openDraftId)

  useEffect(() => {
    if (agentHandoff?.kind !== 'submission_draft' || agentHandoff.resourceId !== openDraftId) return
    setAgentHandoff(null)
  }, [agentHandoff, openDraftId, setAgentHandoff])

  const cfg = scheme.data
  const flat = useMemo(
    () =>
      (cfg?.categories ?? []).flatMap((c) =>
        claimableItems(c).map((it) => ({ category: c.key, categoryName: c.name, item: it })),
      ),
    [cfg],
  )

  /* complete 接口确认后先把队列项标成 ready；详情查询也看到同一 evidenceId 后，
     才从临时队列移除，交给“已附上的文件”列表长期展示。 */
  useEffect(() => {
    const confirmed = new Set((draft.data?.evidence ?? []).filter((item) => item.status === 'ready').map((item) => item.id))
    if (!confirmed.size) return
    setUploads((current) => {
      const next = current.filter((item) => item.status !== 'ready' || !item.evidenceId || !confirmed.has(item.evidenceId))
      return next.length === current.length ? current : next
    })
  }, [draft.data?.evidence])

  /* 继续编辑：把草稿读回表单，之后的保存都落到同一条上，而不是又新建一条。
     resumed 记住已经读过哪一条——学生随后改了字段或换了小项，不能再被这个 effect 覆盖回去。 */
  useEffect(() => {
    if (!openDraftId || resumed.current === openDraftId || !cfg) return
    const detail = resume.data
    if (!detail) return
    resumed.current = openDraftId
    const source = detail.submission
    /* 只有草稿能被读回表单。否则刷新一次带着旧的 ?draft=，
       就会把已经进审核的记录重新当草稿改。 */
    if (source.status !== 'draft') {
      say('这条已经提交，不能再当草稿编辑')
      return
    }
    const target = flat.find((entry) => entry.item.key === source.itemKey)
    if (!target) {
      say('这条草稿对应的小项已经不在当前方案里了，得重新报一次')
      return
    }
    const restored = readClaim(target.item.scoreRule, source.claim)
    setSelKey(source.itemKey)
    setDraftId(source.id)
    draftIdRef.current = source.id
    setTitle(source.title)
    setMd(source.note ?? '')
    setQty(restored.qty)
    setLevel(restored.level)
    setFree(restored.free)
    if (claimOptionLost(target.item.scoreRule, source.claim)) {
      say(`原来选的档次「${source.claim.option}」已被方案删除，请重新选择`)
    }
  }, [openDraftId, resume.data, cfg, flat, say])

  /* 自动暂存：只写本机，不发请求，所以可以放心跟着每次改动走。
     切到别的小项、甚至关掉页面，回来还能接着写。 */
  useEffect(() => {
    if (!selKey || !sid) return
    const timer = window.setTimeout(() => {
      writeLocalDraft(sid, { itemKey: selKey, draftId, title, qty, level, free, md, savedAt: Date.now() })
      setLocalDraftKeys(localDraftItemKeys(sid))
    }, AUTOSAVE_DELAY_MS)
    return () => window.clearTimeout(timer)
  }, [sid, selKey, draftId, title, qty, level, free, md])

  useEffect(() => setLocalDraftKeys(localDraftItemKeys(sid)), [sid])

  /* 树上标点的小项：本地还留着没写完的东西，或库里已经有草稿。 */
  const draftMarkers = useMemo(() => {
    const keys = new Set(localDraftKeys)
    for (const item of mine.data?.items ?? []) {
      if (item.status === 'draft') keys.add(item.itemKey)
    }
    return keys
  }, [localDraftKeys, mine.data])

  if (scheme.isLoading) return <div className="load-bar"><span /></div>
  if (scheme.isError || !cfg) {
    return <Empty title="提交入口暂时不可用" desc="请稍后刷新重试。" />
  }

  const sel = flat.find((x) => x.item.key === selKey) ?? null
  /* 只有普通基础项与扣分项落到只读说明；条件基础项已由 claimableItems 转成申报项。 */
  const readOnlyItem = !sel ? findReadOnlySchemeItem(cfg, selKey) : null
  /* 导入类大项（专业素质）：树上那一格指向公式与认定口径，不是提交表单。 */
  const briefCategory = !sel && !readOnlyItem ? findCategoryBrief(cfg, selKey) : null

  const serverNow = win.data ? Date.parse(win.data.serverNow) + Math.max(0, Date.now() - win.dataUpdatedAt) : Date.now()
  const blockedReason = submissionBlockReason(win.data, serverNow)
  const canSubmit = !blockedReason
  const rule = sel?.item.scoreRule
  const exp = rule ? expected(rule, qty, level, free) : { raw: null, final: null, capped: false }
  const enumMax = rule?.type === 'enum' ? scoreBounds(rule).hi : null
  const evidence = draft.data?.evidence ?? []
  const readyEvidenceCount = evidence.filter((item) => item.status === 'ready').length
  const pendingEvidenceCount = evidence.filter((item) => item.status === 'pending').length
  const rejectedEvidenceCount = evidence.filter((item) => item.status === 'rejected').length
  const already = (mine.data?.items ?? []).filter((s) => s.status !== 'draft' && s.itemKey === sel?.item.key)
  const uploading = uploads.filter((item) => item.status === 'queued' || item.status === 'uploading').length
  /** 这张表单上有没有东西。决定草稿箱那行说"新的一条"还是"已自动暂存"。 */
  const formTouched = !!(title.trim() || qty.trim() || free.trim() || md.trim() || level > 0)
  const canReceiveFiles = !!sel && canSubmit

  /* 本小项已落库的草稿，最近改动的排在前面。同一个小项可以有多条
     （同一类里记两件不同的事），所以这里是列表而不是单条。 */
  const itemDrafts = (mine.data?.items ?? [])
    .filter((s) => s.status === 'draft' && s.itemKey === sel?.item.key)
    .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))

  const applyForm = (next: FormState) => {
    setDraftId(next.draftId)
    draftIdRef.current = next.draftId
    draftCreation.current = null
    uploadChain.current = Promise.resolve()
    setTitle(next.title)
    setQty(next.qty)
    setLevel(next.level)
    setFree(next.free)
    setMd(next.md)
    setUploads([])
    setDraggingFiles(false)
    setDraggedFileCount(0)
    dragDepth.current = 0
  }

  const reset = () => applyForm(BLANK_FORM)

  /* 切到某个小项时把它自己的内容找回来。
     本地暂存里是还没落库的键入，数据库里是已经保存过的草稿——谁新用谁。 */
  const restoreFor = (key: string) => {
    const target = flat.find((entry) => entry.item.key === key)
    if (!target) {
      reset()
      return
    }
    const known = mine.data?.items ?? []
    const server = known
      .filter((s) => s.status === 'draft' && s.itemKey === key)
      .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))[0]
    const local = readLocalDraft(sid, key)
    /* 暂存里记着的那条草稿可能已经在「我的提交」里被删了。
       删了就只保留文字、当成新的一条，否则保存会 PUT 到一个不存在的 id。 */
    const localDraftId = local?.draftId && known.some((s) => s.id === local.draftId) ? local.draftId : null
    if (local && (!server || local.savedAt > Date.parse(server.updatedAt))) {
      applyForm({ ...local, draftId: localDraftId })
      return
    }
    if (server) {
      const restored = readClaim(target.item.scoreRule, server.claim)
      applyForm({ draftId: server.id, title: server.title, md: server.note ?? '', ...restored })
      return
    }
    reset()
  }

  /* 从草稿箱里挑一条来写。本地暂存记的是"这个小项最近在写哪一条"，
     所以打开别的草稿时要一并覆盖掉，否则下次进来又被暂存拽回上一条。 */
  const openItemDraft = (target: Submission) => {
    if (!sel) return
    const restored = readClaim(sel.item.scoreRule, target.claim)
    const next: FormState = { draftId: target.id, title: target.title, md: target.note ?? '', ...restored }
    applyForm(next)
    writeLocalDraft(sid, { itemKey: sel.item.key, ...next, savedAt: Date.now() })
    setLocalDraftKeys(localDraftItemKeys(sid))
    setDraftBoxOpen(false)
  }

  const removeItemDraft = (id: string) => {
    actions.remove.mutate(id, {
      onSuccess: () => {
        say('草稿已删除')
        /* 删掉的正好是手上这条，就把表单连同本地暂存一起清干净，
           不然暂存还指着一个不存在的 id。 */
        if (id === draftIdRef.current && sel) {
          clearLocalDraft(sid, sel.item.key)
          setLocalDraftKeys(localDraftItemKeys(sid))
          reset()
        }
      },
      onError: (e) => say(e instanceof ApiError ? e.message : '删除失败'),
    })
  }

  const pick = (key: string) => {
    if (uploading > 0) {
      say('还有文件在上传，等传完再换小项')
      return
    }
    setDraftBoxOpen(false)
    setSelKey(key)
    restoreFor(key)
    /* 换了小项就不再是在编辑那条草稿了，把 ?draft= 摘掉，免得刷新又把它拉回来。 */
    if (openDraftId) go('stuSubmit')
  }

  /* 不拦空标题：佐证必须挂在已存在的记录上，而"先传照片再补名称"是很自然的顺序。
     标题的必填留到提交那一刻——后端也是这么分的。 */
  async function save(): Promise<string | null> {
    if (!sel || !rule) return null
    const body = {
      category: sel.category as CategoryKey,
      itemKey: sel.item.key,
      title: title.trim(),
      claim: buildClaim(rule, qty, level, free),
      note: md,
    }
    try {
      const currentDraftId = draftIdRef.current
      if (currentDraftId) {
        await actions.update.mutateAsync({ id: currentDraftId, ...body })
        return currentDraftId
      }
      const created = await actions.create.mutateAsync(body)
      draftIdRef.current = created.id
      setDraftId(created.id)
      return created.id
    } catch (e) {
      say(e instanceof ApiError ? e.message : '保存失败')
      return null
    }
  }

  /* 「存为草稿」和「提交」都必须先把当前表单真的写回去。
     ensureDraft 只负责"有一个 id 可以挂佐证"，草稿已存在时它直接返回、一个字段都不写——
     按钮只调它的话，点了没反应，提交时交上去的还是上一次保存的旧值。
     建草稿正在飞的时候先等它落地，再走更新，避免并发建出第二条。 */
  async function saveDraft(): Promise<string | null> {
    if (draftCreation.current) await draftCreation.current
    return save()
  }

  function ensureDraft(): Promise<string | null> {
    if (draftIdRef.current) return Promise.resolve(draftIdRef.current)
    if (draftCreation.current) return draftCreation.current
    const pending = save()
    draftCreation.current = pending
    void pending.finally(() => {
      if (draftCreation.current === pending) draftCreation.current = null
    })
    return pending
  }

  const updateUpload = (key: string, change: Partial<Omit<UploadItem, 'key' | 'file'>>) => {
    setUploads((current) => current.map((item) => (item.key === key ? { ...item, ...change } : item)))
  }

  async function runUploads(submissionId: string, items: UploadItem[]) {
    let cursor = 0
    const worker = async () => {
      while (cursor < items.length) {
        const item = items[cursor++]
        updateUpload(item.key, { status: 'uploading', progress: 1, error: undefined })
        try {
          const result = await uploadEvidence(submissionId, item.file, (progress) => {
            // PUT 到 100% 后还有一次 complete 确认；确认前最多显示 99%。
            updateUpload(item.key, { progress: Math.min(99, Math.max(1, progress)) })
          })
          updateUpload(item.key, {
            status: 'ready',
            progress: 100,
            evidenceId: result.evidenceId,
            // 名字变了说明这份文件的真实格式和扩展名对不上。说一声就行，不用他们做什么。
            notice: result.filename === item.file.name ? undefined : `这份文件的真实格式和扩展名对不上，已按真实格式存成「${result.filename}」`,
          })
        } catch (error) {
          updateUpload(item.key, {
            status: 'error',
            error: userErrorMessage(error, '上传失败，请稍后重试'),
          })
        }
      }
    }
    await Promise.all(Array.from({ length: Math.min(3, items.length) }, worker))
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: K.submission(submissionId) }),
      queryClient.invalidateQueries({ queryKey: ['submissions'] }),
    ])
  }

  function scheduleUploads(submissionId: string, items: UploadItem[]) {
    const scheduled = uploadChain.current.then(() => runUploads(submissionId, items))
    uploadChain.current = scheduled.catch(() => undefined)
    return scheduled
  }

  async function onFiles(list: FileList | readonly File[] | null) {
    if (!list?.length || !sel) return
    const files = Array.from(list)
    const nextUploads: UploadItem[] = []
    for (const file of files) {
      const error = validateEvidenceFile(file, sel.item.evidence)
      if (error) say(error)
      else nextUploads.push({ key: crypto.randomUUID(), file, status: 'queued', progress: 0 })
    }
    if (!nextUploads.length) return
    setUploads((current) => [...current, ...nextUploads])
    const id = await ensureDraft()
    if (!id) {
      for (const item of nextUploads) {
        updateUpload(item.key, { status: 'error', error: '草稿建立失败，请重试' })
      }
      return
    }
    await scheduleUploads(id, nextUploads)
  }

  async function retryUpload(item: UploadItem) {
    updateUpload(item.key, { status: 'queued', progress: 0, error: undefined, notice: undefined })
    const id = await ensureDraft()
    if (!id) {
      updateUpload(item.key, { status: 'error', error: '草稿建立失败，请重试' })
      return
    }
    await scheduleUploads(id, [item])
  }

  function onDragEnter(event: DragEvent<HTMLDivElement>) {
    if (!event.dataTransfer.types.includes('Files')) return
    event.preventDefault()
    dragDepth.current += 1
    if (canReceiveFiles) {
      setDraggingFiles(true)
      setDraggedFileCount(Array.from(event.dataTransfer.items).filter((item) => item.kind === 'file').length)
    }
  }

  function onDragOver(event: DragEvent<HTMLDivElement>) {
    if (!event.dataTransfer.types.includes('Files')) return
    event.preventDefault()
    event.dataTransfer.dropEffect = canReceiveFiles ? 'copy' : 'none'
  }

  function onDragLeave(event: DragEvent<HTMLDivElement>) {
    if (dragDepth.current === 0) return
    event.preventDefault()
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (dragDepth.current === 0) {
      setDraggingFiles(false)
      setDraggedFileCount(0)
    }
  }

  function onDrop(event: DragEvent<HTMLDivElement>) {
    if (!event.dataTransfer.types.includes('Files')) return
    event.preventDefault()
    dragDepth.current = 0
    setDraggingFiles(false)
    setDraggedFileCount(0)
    if (!sel) {
      say('先在左边选一个能报的小项，再把佐证文件拖进来')
      return
    }
    if (!canSubmit) {
      say('当前不能添加佐证文件')
      return
    }
    void filesFromDrop(event.dataTransfer).then((files) => {
      if (!files.length) {
        say('这次拖进来没读到文件，换「选择文件」或「选择文件夹」试试')
        return
      }
      return onFiles(files)
    })
  }

  async function finalSubmit() {
    if (uploading > 0) {
      say('请等所有文件上传完成后再提交')
      return
    }
    /* 草稿可以没有标题，进审核不行——审核人得知道自己在看什么。 */
    if (!title.trim()) {
      say('请填写事项名称。')
      return
    }
    const id = await saveDraft()
    if (!id) return
    try {
      const submitted = await actions.submit.mutateAsync(id)
      say(submitted.status === 'scored' ? '提交成功 · 已定分' : '提交成功 · 等待审核')
      /* 进了审核就不再是草稿，本地那份必须一起清掉——否则下次点回这个小项，
         暂存会把已提交的内容又摆回一张空表单里。 */
      if (selKey) clearLocalDraft(sid, selKey)
      setLocalDraftKeys(localDraftItemKeys(sid))
      reset()
      if (openDraftId) go('stuSubmit')
    } catch (e) {
      say(e instanceof ApiError ? e.message : '提交失败')
    }
  }

  return (
    <div
      onDragEnter={onDragEnter}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      style={{ animation: 'rise .28s ease both' }}
    >
      <PageHead
        en="SUBMIT EVIDENCE"
        title="提交材料"
        desc={`填写事项并上传佐证 · 规则${f.schemeStamp(cfg)}`}
        /* 入口跟随后端运行时开关；运维页切换后无需重新构建前端。 */
        side={aiStatus.data?.enabled ? <Btn onClick={() => setAIOpen(true)}>用 AI 批量整理</Btn> : undefined}
      />

      {aiStatus.data?.enabled && <AIMaterialPanel open={aiOpen} onClose={() => setAIOpen(false)} scheme={cfg} />}

      {!canSubmit && (
        <div style={{ border: '1px solid var(--red)', background: 'var(--redBg)', padding: '13px 16px', marginBottom: 18, fontSize: 12.5, color: 'var(--fg2)' }}>
          <span role="status">{win.isError ? '提交状态读取失败，请刷新状态后重试。' : blockedReason}</span>
          <div style={{ marginTop: 10 }}><Btn disabled={win.isFetching} onClick={() => void win.refetch()}>{win.isFetching ? '正在刷新…' : '刷新提交状态'}</Btn></div>
        </div>
      )}

      <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '252px 1fr', gap: 0, borderTop: '1px solid var(--line)' }}>
        <SchemeTree config={cfg} selectedKey={selKey} onSelect={pick} draftKeys={draftMarkers} />

        <div className="submission-form-column" style={{ minWidth: 0, padding: '22px 0 22px 30px', display: 'flex', flexDirection: 'column', gap: 20 }}>
          {!selKey ? (
            <Empty title="请选择小项" />
          ) : briefCategory ? (
            <CategoryBrief category={briefCategory} weight={cfg.weights[briefCategory.key]} />
          ) : (
            <div style={{ border: '1px solid var(--line)', display: 'flex', flexDirection: 'column' }}>
              <ItemGuide
                name={sel ? sel.item.name : readOnlyItem?.name ?? selKey}
                item={sel?.item ?? null}
                readOnlyHint={sel ? undefined : '这个小项由审核人照班级记录录，你这边只能看。'}
                extra={sel ? (
                  <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 5, flex: 'none' }}>
                    <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>本小项已提交</span>
                    <span style={{ fontSize: 17, fontWeight: 600, ...num }}>{already.length} 条</span>
                  </div>
                ) : undefined}
              />

              {/* 这个小项自己的草稿箱。草稿不是提交，所以它不该出现在「我的提交」里——
                  就摆在写它的地方，点开能看到之前存过哪几条、直接打开继续写。
                  同一小项可以记多条，因此「另起一条」是必须留的出口。 */}
              {sel && (draftId || itemDrafts.length > 0) && (
                <div style={{ borderBottom: '1px solid var(--line)', background: 'var(--bg)' }}>
                  <div style={{ padding: '10px 22px', display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                    <button
                      type="button"
                      className="hv-fg"
                      aria-expanded={draftBoxOpen}
                      disabled={itemDrafts.length === 0}
                      onClick={() => setDraftBoxOpen((v) => !v)}
                      style={{ display: 'flex', alignItems: 'center', gap: 6, border: 0, background: 'none', padding: 0, font: 'inherit', fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)', cursor: itemDrafts.length ? 'pointer' : 'default' }}
                    >
                      <span style={{ width: 10, textAlign: 'center', color: 'var(--fg3)' }}>{itemDrafts.length === 0 ? '' : draftBoxOpen ? '▾' : '▸'}</span>
                      本小项草稿箱 · {itemDrafts.length} 条未提交
                    </button>
                    <span style={{ fontSize: 12.5, color: 'var(--fg3)', minWidth: 0, textWrap: 'pretty' }}>
                      {draftId ? `正在编辑 #${draftId}` : formTouched ? '新的一条 · 暂存在本机，还没存成草稿' : '新的一条'}
                    </span>
                    <span style={{ marginLeft: 'auto', flex: 'none' }}>
                      <Btn
                        disabled={!canSubmit || uploading > 0}
                        onClick={() => {
                          clearLocalDraft(sid, sel.item.key)
                          setLocalDraftKeys(localDraftItemKeys(sid))
                          reset()
                          setDraftBoxOpen(false)
                          say('另起了一条 · 原来那条还在这个小项的草稿箱里')
                        }}
                      >
                        ＋另起一条
                      </Btn>
                    </span>
                  </div>

                  {draftBoxOpen && itemDrafts.length > 0 && (
                    <div style={{ borderTop: '1px solid var(--line2)', padding: '4px 22px 12px' }}>
                      {itemDrafts.map((d) => {
                        const current = d.id === draftId
                        return (
                          <div key={d.id} style={{ display: 'flex', alignItems: 'center', gap: 11, padding: '9px 0', borderBottom: '1px solid var(--line2)', flexWrap: 'wrap' }}>
                            <span style={{ ...mono('11px', '.02em'), flex: 'none', color: current ? 'var(--fg)' : 'var(--fg3)' }}>#{d.id}</span>
                            <span style={{ fontSize: 12.5, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: current ? 600 : 400 }}>
                              {f.submissionTitle(d.title)}
                            </span>
                            <span style={{ fontSize: 11.5, color: 'var(--fg3)', flex: 'none' }}>
                              {d.evidenceCount} 份佐证 · {f.dateTime(d.updatedAt)}
                            </span>
                            <span style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 12, flex: 'none' }}>
                              {current ? (
                                <span style={{ fontSize: 12, color: 'var(--fg3)' }}>正在编辑</span>
                              ) : (
                                <button
                                  type="button"
                                  className="hv-fg"
                                  disabled={uploading > 0}
                                  onClick={() => openItemDraft(d)}
                                  style={{ border: 0, background: 'none', padding: 0, font: 'inherit', fontSize: 12, color: 'var(--fg2)', cursor: 'pointer' }}
                                >
                                  打开
                                </button>
                              )}
                              <button
                                type="button"
                                className="hv-red"
                                disabled={!canSubmit || actions.remove.isPending}
                                onClick={() => removeItemDraft(d.id)}
                                style={{ border: 0, background: 'none', padding: 0, font: 'inherit', fontSize: 12, color: 'var(--fg3)', cursor: 'pointer' }}
                              >
                                删除
                              </button>
                            </span>
                          </div>
                        )
                      })}
                    </div>
                  )}
                </div>
              )}

              {!sel ? (
                <div className="submission-form-body" style={{ padding: '24px 22px', display: 'flex', flexDirection: 'column', gap: 16 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', color: 'var(--fg2)' }}>
                      <path d="M6 11V8a6 6 0 0 1 12 0v3" />
                      <path d="M5 11h14v9H5z" />
                    </svg>
                    <span style={{ fontSize: 14.5, fontWeight: 600, letterSpacing: '-.02em' }}>这一项没有提交入口</span>
                  </div>
                  <div style={{ fontSize: 13, color: 'var(--fg2)', lineHeight: 1.85, maxWidth: 600, textWrap: 'pretty' }}>
                    {readOnlyItem?.kind === 'penalty'
                      ? '扣分是负分，由审核人照通报和学院文件录，不用你报。'
                      : '基础项默认满分、只扣不加，由审核人照班级出勤册和记录录，不用你报。'}
                    依据和得分在「综测总览」看，有意见可以申诉
                  </div>
                  <div style={{ display: 'flex', alignItems: 'baseline', gap: 14, borderTop: '1px solid var(--line2)', paddingTop: 16 }}>
                    <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>录入方</span>
                    <span style={{ marginLeft: 'auto', textAlign: 'right', fontSize: 13, color: 'var(--fg2)' }}>审核人（两名，他们知道是你，你不知道是他们）</span>
                  </div>
                </div>
              ) : (
                <div className="submission-form-body" style={{ padding: '24px 22px', display: 'flex', flexDirection: 'column', gap: 24 }}>
                  {/* 本学年归到这一项下的活动。摆在事项名称上面：先看名单再抄名字，
                      比填完再回头核对省一轮。 */}
                  {sel?.item.activities?.length ? <ActivityTable activities={sel.item.activities} /> : null}

                  <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr', gap: 24 }}>
                    <Field label="事项名称" required hint="主办方和时间，方便核对。">
                      <input
                        value={title}
                        onChange={(e) => setTitle(e.target.value)}
                        placeholder={rule?.type === 'enum' ? '如实填写' : '如实填写'}
                        style={{ ...fieldStyle, border: '1px solid var(--line)', borderBottom: '1px solid var(--line)', padding: '11px 13px' }}
                      />
                    </Field>

                    {rule?.type === 'per_unit' && (
                      <Field label={`${rule.unit}数 · 可留空`} hint={`每${rule.unit} ${rule.per} 分${rule.cap != null ? `，小项上限 ${rule.cap} 分` : '，不封顶'}；留空则由审核人根据佐证定分`}>
                        <input
                          type="number"
                          min={0}
                          step="any"
                          value={qty}
                          onChange={(e) => setQty(e.target.value)}
                          inputMode="decimal"
                          placeholder="留空则由审核人根据佐证定分"
                          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px', fontWeight: 600, ...num }}
                        />
                      </Field>
                    )}

                    {rule?.type === 'threshold' && (
                      <Field label={`已完成${rule.unit}数`} hint={`至少 ${rule.minimum} ${rule.unit}；达到后获得 ${rule.award} 分，未达到则本项为 0 分`}>
                        <input
                          type="number"
                          min={0}
                          step="any"
                          value={qty}
                          onChange={(e) => setQty(e.target.value)}
                          inputMode="decimal"
                          placeholder={`请输入已完成的${rule.unit}数`}
                          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px', fontWeight: 600, ...num }}
                        />
                      </Field>
                    )}

                    {rule?.type === 'free' && (
                      <Field label="期望加分 · 可留空" hint={`允许范围 ${rule.min} — ${rule.max} 分；留空则由审核人根据佐证定分`}>
                        <input
                          type="number"
                          min={rule.min}
                          max={rule.max}
                          step="0.01"
                          value={free}
                          onChange={(e) => setFree(e.target.value)}
                          inputMode="decimal"
                          placeholder="留空则由审核人根据佐证定分"
                          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px', fontWeight: 600, ...num }}
                        />
                      </Field>
                    )}
                  </div>

                  {rule?.type === 'enum' && (
                    <>
                      {/* 档位总表。和审核台看到的是同一个组件：学生挑档之前先看得见
                          「一等和二等差多少」，而不是靠一档一档点过去试。 */}
                      {wantsMatrix(rule) && (
                        <div>
                          <div style={{ fontSize: 12, fontWeight: 600, letterSpacing: '.01em', color: 'var(--fg2)', paddingBottom: 10 }}>
                            档位总表
                          </div>
                          <TierMatrix rule={rule} picked={rule.options[level]?.label} />
                        </div>
                      )}
                      <EnumOptionPicker
                        rule={rule}
                        value={level}
                        onChange={(next) => {
                          setLevel(next)
                          setFree(String(rule.options[next]?.score ?? ''))
                        }}
                      />
                      <Field
                        label="期望加分 · 可调整"
                        hint={`你可以按实际情况在 0 — ${enumMax} 分内修改，最终由审核人认定`}
                      >
                        <input
                          type="number"
                          min={0}
                          max={enumMax ?? undefined}
                          step="0.01"
                          value={free}
                          onChange={(event) => setFree(event.target.value)}
                          inputMode="decimal"
                          placeholder={`建议 ${rule.options[level]?.score ?? 0} 分`}
                          style={{ ...fieldStyle, border: '1px solid var(--line)', padding: '11px 13px', fontWeight: 600, ...num }}
                        />
                      </Field>
                    </>
                  )}

                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>
                      佐证材料
                      {/* 必传与否由方案逐小项配置，星号跟着方案走，不写死 */}
                      {sel.item.evidence?.required && <RequiredMark />}
                    </span>
                    <FileDropzone
                      active={draggingFiles}
                      disabled={!canReceiveFiles}
                      icon={<Upload size={24} strokeWidth={1.4} />}
                      title={draggingFiles
                        ? `松开就能添加${draggedFileCount > 0 ? ` ${draggedFileCount} 个` : ''}文件或文件夹`
                        : uploading > 0
                          ? `正在上传 ${uploading} 个文件 · 仍可继续添加`
                          : '将文件或文件夹拖至此区域'}
                      /* 格式与体积的完整清单留给下面那行小字，投放区里只说下一步做什么。
                         把「必传·PDF/常用图片/Word/Excel/PPT/文本/WPS/压缩包/MP4·单份≤50MB」
                         整条塞进来，占了两行还没人读。 */
                      hint="或点击下方按钮上传"
                      actions={<><Btn disabled={!canReceiveFiles} onClick={() => fileInput.current?.click()}>选择文件</Btn><Btn disabled={!canReceiveFiles} onClick={() => folderInput.current?.click()}>选择文件夹</Btn></>}
                    />
                    <span style={{ fontSize: 11.5, color: 'var(--fg3)', lineHeight: 1.6, textWrap: 'pretty' }}>
                      {evidenceHint(sel.item)}{!draftId && ' · 首次上传会先保存当前草稿'}
                    </span>
                    <input
                      ref={fileInput}
                      type="file"
                      multiple
                      hidden
                      disabled={!canSubmit}
                      accept={evidenceAccept(sel.item.evidence?.types)}
                      onChange={(event) => {
                        void onFiles(event.target.files)
                        event.target.value = ''
                      }}
                    />
                    <input
                      ref={(node) => { folderInput.current = node; node?.setAttribute('webkitdirectory', '') }}
                      type="file"
                      multiple
                      hidden
                      disabled={!canSubmit}
                      onChange={(event) => {
                        void onFiles(event.target.files)
                        event.target.value = ''
                      }}
                    />
                    {uploads.length > 0 && (
                      <div style={{ border: '1px solid var(--line)', display: 'flex', flexDirection: 'column' }}>
                        <div style={{ padding: '10px 12px', borderBottom: '1px solid var(--line2)', display: 'flex', alignItems: 'center', gap: 8, fontSize: 12.5, color: 'var(--fg2)' }}>
                          <span style={{ fontWeight: 600 }}>本次上传队列</span>
                          <span style={{ marginLeft: 'auto', ...mono('10px', '.02em') }}>最多 3 份并行</span>
                        </div>
                        {uploads.map((item) => {
                          const active = item.status === 'queued' || item.status === 'uploading'
                          return (
                            <div key={item.key} style={{ padding: '11px 12px', borderBottom: '1px solid var(--line2)', display: 'flex', flexDirection: 'column', gap: 8 }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
                                <Pill tone={item.status === 'ready' ? 'ok' : item.status === 'error' ? 'bad' : 'warn'}>
                                  {item.status === 'queued' ? '等待上传' : item.status === 'uploading' ? `${item.progress}%` : item.status === 'ready' ? '已附上' : '上传失败'}
                                </Pill>
                                <span title={item.file.name} style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>{item.file.name}</span>
                                <span style={{ marginLeft: 'auto', flex: 'none', fontSize: 12, color: 'var(--fg3)' }}>{f.bytes(item.file.size)}</span>
                                {item.status === 'error' && (
                                  <>
                                    <button type="button" onClick={() => void retryUpload(item)} style={{ background: 'none', border: 0, padding: 0, color: 'var(--fg2)', font: 'inherit', fontSize: 12, cursor: 'pointer' }}>重试</button>
                                    <button type="button" className="hv-red" onClick={() => setUploads((current) => current.filter((upload) => upload.key !== item.key))} style={{ background: 'none', border: 0, padding: 0, color: 'var(--fg3)', font: 'inherit', fontSize: 12, cursor: 'pointer' }}>移除</button>
                                  </>
                                )}
                              </div>
                              {active && (
                                <div
                                  role="progressbar"
                                  aria-label={`${item.file.name} 上传进度`}
                                  aria-valuemin={0}
                                  aria-valuemax={100}
                                  aria-valuenow={item.progress}
                                  style={{ height: 4, background: 'var(--line2)', overflow: 'hidden' }}
                                >
                                  <div style={{ width: `${item.progress}%`, height: '100%', background: 'var(--red)', transition: 'width .16s ease' }} />
                                </div>
                              )}
                              {item.error && <span style={{ fontSize: 12, color: 'var(--red)', lineHeight: 1.5 }}>{item.error}</span>}
                              {item.notice && <span style={{ fontSize: 12, color: 'var(--fg3)', lineHeight: 1.5 }}>{item.notice}</span>}
                            </div>
                          )
                        })}
                      </div>
                    )}
                    {evidence.length > 0 && (
                      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, paddingTop: 3 }}>
                        <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>附件记录</span>
                        <span style={{ fontSize: 12, color: 'var(--fg3)' }}>
                          已确认 {readyEvidenceCount} 份
                          {pendingEvidenceCount > 0 ? ` · 待确认 ${pendingEvidenceCount} 份` : ''}
                          {rejectedEvidenceCount > 0 ? ` · 已拒绝 ${rejectedEvidenceCount} 份` : ''}
                        </span>
                      </div>
                    )}
                    <EvidenceFiles
                      items={evidence}
                      onRemove={(eid) =>
                        draftId &&
                        actions.removeEvidence.mutate(
                          { id: draftId, eid },
                          { onSuccess: () => void draft.refetch(), onError: (e) => say(e instanceof ApiError ? e.message : '移除失败') },
                        )
                      }
                    />
                  </div>

                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    <Sub title="补充说明" />
                    {/* 与申诉理由、审核意见共用同一个编辑器和同一套渲染：
                        学生在这里写的 `1.` 和在申诉里写的 `1.` 必须是同一个意思，
                        否则"我写的时候是列表，审核人看到的是一行字"这种事没人说得清。 */}
                    <MarkdownEditor
                      value={md}
                      onChange={setMd}
                      minHeight={200}
                      evidence={evidence}
                      placeholder="补充一下怎么参与的、持续多久、谁能证明"
                    />
                  </div>

                  <Note tone={exp.capped ? 'warn' : undefined}>
                    <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
                      <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--fg2)' }}>期望加分预览</span>
                      <span style={{ fontSize: 20, fontWeight: 600, color: 'var(--fg)', ...num }}>
                        {exp.final == null ? '待审核人定分' : f.score(exp.final)}
                      </span>
                      {exp.capped && <span style={{ fontSize: 12.5 }}>已按允许范围计入</span>}
                      <span style={{ ...mono('11px', '.02em'), marginLeft: 'auto' }}>{f.schemeStamp(cfg)}</span>
                    </div>
                  </Note>

                  <div className="submission-form-actions" style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                    <Btn primary disabled={!canSubmit || uploading > 0 || actions.submit.isPending} onClick={() => void finalSubmit()}>
                      {actions.submit.isPending ? '提交中…' : uploading > 0 ? `等待 ${uploading} 份文件上传` : '提交并进入审核'}
                    </Btn>
                    <Btn disabled={!canSubmit || actions.create.isPending || actions.update.isPending} onClick={() => void saveDraft().then((id) => id && say('已存为草稿 · 草稿不送审，封存前随时可以补交'))}>
                      存为草稿
                    </Btn>
                    {draftId && <span style={{ ...mono('11px', '.02em') }}>草稿 #{draftId}</span>}
                    <span style={{ marginLeft: 'auto', fontSize: 12.5, color: 'var(--fg3)', textWrap: 'pretty' }}>
                      定分或封存之前，都还能改、能撤回
                    </span>
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {/* 规则速查放在表单下面：写材料的人最需要它，却一直只有审核人那一页有。
          和「我的审核量」共用同一个组件——两边读同一段文字，才不会出现
          "学生以为能加分、审核人认为不算"这种各有各依据的争执。 */}
      <div style={{ borderTop: '1px solid var(--line)', marginTop: 26, paddingTop: 4 }}>
        <button
          type="button"
          className="hv-fg"
          aria-expanded={rulesOpen}
          onClick={() => setRulesOpen((v) => !v)}
          style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', border: 0, background: 'none', padding: '16px 0 6px', font: 'inherit', textAlign: 'left', cursor: 'pointer' }}
        >
          <span style={{ width: 12, color: 'var(--fg3)' }}>{rulesOpen ? '▾' : '▸'}</span>
          <span style={{ fontSize: 13, fontWeight: 600, letterSpacing: '-.015em' }}>规则速查</span>
          <span style={{ fontSize: 12.5, color: 'var(--fg3)' }}>照已发布方案的原文列出来，加分以它为准 · {f.schemeStamp(cfg)}</span>
        </button>
        {rulesOpen && (
          <div style={{ paddingBottom: 24 }}>
            <RuleSheet config={cfg} heading={false} />
          </div>
        )}
      </div>
    </div>
  )
}
