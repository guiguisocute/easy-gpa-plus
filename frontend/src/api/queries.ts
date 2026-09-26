/* 取数层。每个 hook 对应后端一个接口，页面不自己拼 URL。

   缓存键的第一段就是资源名，跟接口路径一一对应；写操作结束后按资源名整段失效，
   不做精细的乐观更新——这套后台的写操作都要经过审核/仲裁，界面上多等一次往返
   比"本地先改、服务端再否决"造成的错觉要诚实。 */

import { useMutation, useQuery, useQueryClient, type UseQueryOptions } from '@tanstack/react-query'
import { ApiError, api } from './client'
import { useApp } from '@/stores/app'
import { adjudicationKey, adjudicationPath, useAdjudicationBase, useAdjudicationScope } from './adjudicationScope'
import type * as T from './types'
import type { CategoryKey, Role, SchemeConfig, SubmissionStatus, User } from '@/lib/types'
import { evidenceMediaType, fileExtension } from '@/lib/evidence'
import { patchTenantAdmin, patchTenantList, type TenantUpdate } from '@/lib/opsTenants'
import {
  agentAttachmentLinkSchema,
  agentAttachmentPresignSchema,
  agentAttachmentSchema,
  agentConversationSchema,
  agentConversationsSchema,
  agentSourceSchema,
  agentStatusSchema,
  agentPageStateSchema,
  appliedAgentActionSchema,
  knowledgeDocumentDetailSchema,
  knowledgeEntrySchema,
  knowledgePresignSchema,
  knowledgeStateSchema,
	platformKnowledgeDocumentDetailSchema,
	platformKnowledgePresignSchema,
	platformKnowledgeStateSchema,
  preparedAgentActionSchema,
} from './knowledgeAgentSchemas'

/* ---- 缓存键 ---- */

export const K = {
  me: ['me'] as const,
  emails: ['me', 'emails'] as const,
  mailPreferences: ['me', 'mail-preferences'] as const,
  scheme: ['scheme'] as const,
  window: ['window'] as const,
  classResources: ['class-resources'] as const,
  submissions: (q?: string) => ['submissions', q ?? ''] as const,
  submission: (id: string) => ['submissions', 'detail', id] as const,
  aiBatch: (id: string) => ['ai', 'batch', id] as const,
  aiStatus: ['ai', 'status'] as const,
  baseItems: ['me', 'base-items'] as const,
  score: ['me', 'score'] as const,
  seal: ['me', 'seal'] as const,
  myScorecard: ['me', 'scorecard'] as const,
  evidenceLink: (eid: string) => ['evidence', eid, 'link'] as const,
  appeals: ['appeals'] as const,
  appeal: (id: string) => ['appeals', id] as const,
  reviewTasks: (tab: T.ReviewTab) => ['review', 'tasks', tab] as const,
  reviewTask: (id: string) => ['review', 'task', id] as const,
  reviewAppeals: (scope: string) => ['review', 'appeals', scope] as const,
  reviewAppeal: (id: string) => ['review', 'appeals', 'detail', id] as const,
  reviewStudents: ['review', 'students'] as const,
  adminObjections: (q?: string) => ['admin', 'objections', q ?? ''] as const,
  classPenalties: ['class-penalties'] as const,
  myReports: ['reports', 'mine'] as const,
  reviewReport: (id: string) => ['review', 'reports', id] as const,
  adminReports: ['admin', 'reports'] as const,
  reviewCategory: ['review', 'category'] as const,
  reviewHistory: ['review', 'history'] as const,
  adminSchemes: ['admin', 'schemes'] as const,
  adminTemplates: ['admin', 'templates'] as const,
	adminTemplateShares: ['admin', 'template-shares'] as const,
  adminScheme: (id: string) => ['admin', 'scheme', id] as const,
  adminClass: ['admin', 'class'] as const,
  adminSeals: ['admin', 'seals'] as const,
  whitelist: ['admin', 'whitelist'] as const,
  adminUsers: ['admin', 'users'] as const,
  adminSubmissions: (q?: string) => ['admin', 'submissions', q ?? ''] as const,
  adminAppeals: (q?: string) => ['admin', 'appeals', q ?? ''] as const,
	classificationSuggestions: ['admin', 'classification-suggestions'] as const,
  gpa: ['admin', 'gpa'] as const,
  gate: ['admin', 'gate'] as const,
  stats: ['admin', 'stats'] as const,
  reviewSLA: ['admin', 'review-sla'] as const,
	adminReviewProgress: (query: string) => ['admin', 'review-progress', query] as const,
  exportJob: (id: string) => ['admin', 'export', id] as const,
  auditLog: (q?: string) => ['admin', 'audit-log', q ?? ''] as const,
  knowledge: ['admin', 'knowledge'] as const,
  knowledgeDocument: (id: string) => ['admin', 'knowledge', 'document', id] as const,
  knowledgeEntry: (id: string, start: number, end: number) => ['admin', 'knowledge', 'entry', id, start, end] as const,
  agentStatus: ['agent', 'status'] as const,
  agentConversations: ['agent', 'conversations'] as const,
  agentConversation: (id: string) => ['agent', 'conversation', id] as const,
  agentAttachmentLink: (id: string) => ['agent', 'attachment', id, 'link'] as const,
  ops: (name: string) => ['ops', name] as const,
	opsTenantMembers: (id: string) => ['ops', 'tenant', id, 'members'] as const,
	platformKnowledge: ['ops', 'platform-knowledge'] as const,
	platformKnowledgeDocument: (id: string) => ['ops', 'platform-knowledge', id] as const,
}

/** 写操作后按前缀整段失效。传 'admin' 会把所有 admin/* 一起刷掉。 */
function useInvalidate() {
  const qc = useQueryClient()
  return (...prefixes: readonly (readonly unknown[])[]) => {
    for (const p of prefixes) void qc.invalidateQueries({ queryKey: p })
  }
}

type Opts<T> = Omit<UseQueryOptions<T, Error, T, readonly unknown[]>, 'queryKey' | 'queryFn'>

function useApiQuery<T>(key: readonly unknown[], path: string, opts?: Opts<T>) {
  return useQuery<T, Error, T, readonly unknown[]>({
    queryKey: key,
    queryFn: ({ signal }) => api.get<T>(path, signal),
    ...opts,
  })
}

function useAdjudicationQuery<T>(key: readonly unknown[], path: string, opts?: Opts<T>) {
  const scope = useAdjudicationScope()
  return useApiQuery<T>(adjudicationKey(scope, key), adjudicationPath(scope, path), opts)
}

function search(params: Record<string, string | number | undefined>) {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? '?' + s : ''
}

/* ---- 鉴权 ---- */

export const authApi = {
  login: (account: string, password: string) => api.post<T.SessionTokens>('/auth/login', { account, password }),
  /* 注册三步：核对身份 → 设密码(建号并登录) → 登录后可选绑邮箱(走 /me/emails)。 */
  registerCheck: (body: { sid: string; name: string }) =>
    api.post<T.RegisterTicket>('/auth/register/check', body),
  registerComplete: (ticket: string, password: string) =>
    api.post<T.SessionTokens>('/auth/register/complete', { ticket, password }),
  /* 第三步复用登录后的邮箱接口：注册完成即登录态，access token 已经在手，
     不必为"注册中绑邮箱"再造一套匿名接口。 */
  bindEmail: (email: string) => api.post<T.AddEmailChallenge>('/me/emails', { email }),
  bindEmailVerify: (challengeId: string, code: string) =>
    api.post<T.UserEmail>('/me/emails/verify', { challengeId, code }),
  forgotPassword: (account: string) => api.post<void>('/auth/forgot-password', { account }),
  resetPassword: (token: string, password: string) => api.post<void>('/auth/reset-password', { token, password }),
  logout: () => api.post<void>('/auth/logout'),
}

export const useMe = (enabled = true) => useApiQuery<User>(K.me, '/me', { enabled, retry: false })

/* ---- 账号 ---- */

export const useMyEmails = () => useApiQuery<T.List<T.UserEmail>>(K.emails, '/me/emails')
export const useMailPreferences = () => useApiQuery<T.MailPreferenceState>(K.mailPreferences, '/me/mail-preferences')

export function useAccountActions() {
  const invalidate = useInvalidate()
  return {
    updateMailPreferences: useMutation({
      mutationFn: (value: T.MailPreferences) => api.put<void>('/me/mail-preferences', value),
      onSuccess: () => invalidate(K.mailPreferences),
    }),
    addEmail: useMutation({
      mutationFn: (email: string) => api.post<T.AddEmailChallenge>('/me/emails', { email }),
    }),
    verifyEmail: useMutation({
      mutationFn: (v: { challengeId: string; code: string }) => api.post<T.UserEmail>('/me/emails/verify', v),
      onSuccess: () => invalidate(K.emails),
    }),
    setPrimary: useMutation({
      mutationFn: (id: string) => api.put<void>(`/me/emails/${id}/primary`),
      onSuccess: () => invalidate(K.emails),
    }),
    removeEmail: useMutation({
      mutationFn: (id: string) => api.del<void>(`/me/emails/${id}`),
      onSuccess: () => invalidate(K.emails),
    }),
    changePassword: useMutation({
      mutationFn: (v: { currentPassword: string; newPassword: string }) => api.put<void>('/me/password', v),
    }),
    viewAs: useMutation({ mutationFn: (role: Role) => api.post<void>('/me/view-as', { role }) }),
  }
}

/* ---- 方案与窗口 ---- */

export const useScheme = () => useApiQuery<SchemeConfig>(K.scheme, '/scheme/current', { retry: false })
// The submission form must observe an administrator unsealing it in another session.
export const useWindow = (opts?: { live?: boolean }) => useApiQuery<T.WindowState>(K.window, '/window', {
  retry: false,
  ...(opts?.live ? { staleTime: 0, refetchOnMount: 'always' as const, refetchOnWindowFocus: true, refetchInterval: 10000 } : {}),
})
export const useClassResources = () => useApiQuery<T.List<T.ClassResource>>(K.classResources, '/class-resources')
export const getClassResourceDownload = (id: string) => api.get<T.ClassResourceDownload>(`/class-resources/${id}/url`)
export const useAIStatus = () => useApiQuery<T.AIStatus>(K.aiStatus, '/ai/status', {
  retry: false,
  staleTime: 5000,
  refetchInterval: 10000,
})

/* ---- 学生：提交 ---- */

export const useSubmissions = (filter: { category?: string; status?: string; forceRejected?: boolean; page?: number; page_size?: number } = {}) => {
  const { forceRejected, ...rest } = filter
  const qs = search({ ...rest, force_rejected: forceRejected ? 'true' : undefined })
  return useApiQuery<T.Page<T.Submission>>(K.submissions(qs), '/submissions' + qs)
}

export const useSubmission = (id: string | null) =>
  useApiQuery<T.SubmissionDetail>(K.submission(id ?? ''), `/submissions/${id}`, { enabled: !!id })

export interface SubmissionInput {
  category: CategoryKey
  itemKey: string
  title: string
  claim: T.Claim
  note?: string
}

export function useSubmissionActions() {
  const invalidate = useInvalidate()
  const after = () => invalidate(K.submissions(), ['submissions'], K.seal, K.score, ['admin'])
  return {
    create: useMutation({
      mutationFn: (v: SubmissionInput) =>
        api.post<{ id: string; status: SubmissionStatus; requestedScore: number | null }>('/submissions', v),
      onSuccess: after,
    }),
    update: useMutation({
      mutationFn: ({ id, ...v }: SubmissionInput & { id: string }) =>
        api.put<{ id: string; status: SubmissionStatus; requestedScore: number | null }>(`/submissions/${id}`, v),
      onSuccess: after,
    }),
    remove: useMutation({ mutationFn: (id: string) => api.del<void>(`/submissions/${id}`), onSuccess: after }),
    /* 退回草稿。在审的条目学生想改时走这条，而不是删掉重传一遍。 */
    withdraw: useMutation({
      mutationFn: (id: string) => api.post<{ id: string; status: SubmissionStatus }>(`/submissions/${id}/withdraw`),
      onSuccess: after,
    }),
    submit: useMutation({
      mutationFn: (id: string) => api.post<{ id: string; status: SubmissionStatus }>(`/submissions/${id}/submit`),
      onSuccess: after,
    }),
    removeEvidence: useMutation({
      mutationFn: (v: { id: string; eid: string }) => api.del<void>(`/submissions/${v.id}/evidence/${v.eid}`),
      onSuccess: after,
    }),
  }
}

/* complete 返回的名字不一定等于传上去的那个：微信、QQ 存下来的图常常是 WebP 或
   HEIC 顶着 .jpg 的名字，后端认出真实格式后会把扩展名改对再收下。 */
export interface CompletedUpload {
  evidenceId: string
  status: string
  filename: string
  mediaType: string
}

/** 佐证直传：presign → 带大小约束的 S3 POST → complete。中间那步不经过后端。 */
export async function uploadEvidence(submissionId: string, file: File, onProgress?: (pct: number) => void) {
  const mediaType = evidenceMediaType(file)
  const pre = await api.post<T.PresignResult>(`/submissions/${submissionId}/evidence/presign`, {
    filename: file.name,
    mediaType,
    sizeBytes: file.size,
  })
  try {
    await postObject(pre, file, onProgress)
    return await api.post<CompletedUpload>(
      `/submissions/${submissionId}/evidence/${pre.evidenceId}/complete`,
    )
  } catch (error) {
    // presign 已经建立 pending 记录。上传失败时把占位记录一并清掉，
    // 否则学生会同时看到“上传失败”和一份永远待确认的幽灵附件。
    try {
      await api.del<void>(`/submissions/${submissionId}/evidence/${pre.evidenceId}`)
    } catch {
      // 保留最初的上传错误；对象存储或数据库的残留由维护任务兜底。
    }
    throw error
  }
}

export async function uploadAppealEvidence(appealId: string, file: File, onProgress?: (pct: number) => void) {
  const mediaType = evidenceMediaType(file)
  const pre = await api.post<T.PresignResult>(`/appeals/${appealId}/evidence/presign`, {
    filename: file.name,
    mediaType,
    sizeBytes: file.size,
  })
  try {
    await postObject(pre, file, onProgress)
    const completed = await api.post<CompletedUpload>(`/appeals/${appealId}/evidence/${pre.evidenceId}/complete`)
    return completedEvidence(completed, file)
  } catch (error) {
    try {
      await api.del<void>(`/appeals/${appealId}/evidence/${pre.evidenceId}`)
    } catch {
      // 保留原始上传错误；仅未完成的记录允许由这个清理接口删除。
    }
    throw error
  }
}

/** AI 原始材料也走对象存储直传，不把图片 Base64 或文件内容送进业务 API。 */
export function aiAssetMediaType(file: Pick<File, 'name' | 'type'>) {
  switch (fileExtension(file.name)) {
    case 'jpg': case 'jpeg': return 'image/jpeg'
    case 'png': return 'image/png'
    case 'webp': return 'image/webp'
    case 'pdf': return 'application/pdf'
    default: return evidenceMediaType(file)
  }
}

/* 429 不是"失败"，是"等一下再来"，服务端连 retryAfter 一起给了。批量上传时把令
   牌桶用到底是正常现象，让这张卡片直接红掉、要用户自己一张张点重传，是把服务端
   的节流当成了用户的错。等一会儿自动再试，等不到才算失败。 */
function rateLimitDelayMs(error: unknown) {
  if (!(error instanceof ApiError) || error.status !== 429) return null
  const detail = error.detail && typeof error.detail === 'object' ? error.detail as { retryAfter?: unknown } : {}
  const seconds = typeof detail.retryAfter === 'number' && Number.isFinite(detail.retryAfter) ? detail.retryAfter : 2
  // 封顶 20 秒：真要等三分钟，还不如把失败摆出来让人决定。
  return Math.min(Math.max(seconds, 1), 20) * 1000
}

async function retryOnRateLimit<T>(call: () => Promise<T>, attempts = 3): Promise<T> {
  for (let attempt = 1; ; attempt++) {
    try {
      return await call()
    } catch (error) {
      const delay = rateLimitDelayMs(error)
      if (delay == null || attempt >= attempts) throw error
      await new Promise((resolve) => setTimeout(resolve, delay))
    }
  }
}

export async function uploadAIAsset(batchId: string, file: File, onProgress?: (pct: number) => void) {
  const mediaType = aiAssetMediaType(file)
  const pre = await retryOnRateLimit(() =>
    api.post<{ assetId: string; uploadUrl: string; uploadFields: Record<string, string> }>(`/ai/batches/${batchId}/assets/presign`, {
      filename: file.name,
      mediaType,
      sizeBytes: file.size,
    }))
  try {
    await postObject(pre, file, onProgress)
    return await api.post<{ assetId: string; status: string; filename: string }>(
      `/ai/batches/${batchId}/assets/${pre.assetId}/complete`,
    )
  } catch (error) {
    try {
      await api.del<void>(`/ai/batches/${batchId}/assets/${pre.assetId}`)
    } catch {
      // 保留原始上传错误；过期材料仍由维护任务兜底。
    }
    throw error
  }
}

export const useAIBatch = (id: string | null) =>
  useApiQuery<T.AIBatch>(K.aiBatch(id ?? ''), `/ai/batches/${id}`, {
    enabled: !!id,
    retry: false,
    refetchInterval: (query) => {
      const status = query.state.data?.status
      if (status === 'queued' || status === 'processing') return 1500
      // review 状态包含短时效预览链接；静默续签，不覆盖学生正在编辑的候选。
      return status === 'review' ? 8 * 60 * 1000 : false
    },
  })

export function useAIBatchActions() {
  const invalidate = useInvalidate()
  return {
    create: useMutation({ mutationFn: () => api.post<T.AICreateBatch>('/ai/batches') }),
    start: useMutation({
      mutationFn: (id: string) => api.post<{ id: string; status: T.AIBatchStatus }>(`/ai/batches/${id}/start`),
      onSuccess: (_, id) => invalidate(K.aiBatch(id)),
    }),
    apply: useMutation({
      mutationFn: (v: { id: string; candidates: T.AICandidate[] }) =>
        api.post<T.AIApplyResult>(`/ai/batches/${v.id}/apply`, { candidates: v.candidates }),
      onSuccess: (_, input) => invalidate(K.aiBatch(input.id), ['submissions'], K.seal, K.score),
    }),
    remove: useMutation({
      mutationFn: (id: string) => api.del<void>(`/ai/batches/${id}`),
      onSuccess: (_, id) => invalidate(K.aiBatch(id)),
    }),
    /* 把没读出来的材料补跑一遍再重新归组。会盖掉现有候选，调用方必须先确认。 */
    recompose: useMutation({
      mutationFn: (id: string) => api.post<{ id: string; status: T.AIBatchStatus; retryingItems: number }>(`/ai/batches/${id}/recompose`),
      onSuccess: (_, id) => invalidate(K.aiBatch(id)),
    }),
    /* 停在当前块，把已经归好的候选交出去。和 remove 不同：那个会连识图结果一起删。 */
    stopCompose: useMutation({
      mutationFn: (id: string) => api.post<{ id: string; status: T.AIBatchStatus; completeChunks: number }>(`/ai/batches/${id}/stop-compose`),
      onSuccess: (_, id) => invalidate(K.aiBatch(id)),
    }),
    removeAsset: useMutation({
      mutationFn: ({ batchId, assetId }: { batchId: string; assetId: string }) =>
        api.del<void>(`/ai/batches/${batchId}/assets/${assetId}`),
      onSuccess: (_, input) => invalidate(K.aiBatch(input.batchId)),
    }),
  }
}

export type NoteEvidenceOwner = 'submission' | 'appeal' | 'objection' | 'report' | 'report-review' | 'admin/bonus-grant-upload' | 'governance/bonus-grant-upload' | 'governance/objection'
export type MarkdownUploadTarget =
  | { kind: 'note'; owner: NoteEvidenceOwner; id: string }
  | { kind: 'appeal-claim'; id: string }

function completedEvidence(completed: CompletedUpload, file: File): T.Evidence {
  return {
    id: completed.evidenceId,
    name: completed.filename,
    mediaType: completed.mediaType,
    sizeBytes: file.size,
    sha256: null,
    status: 'ready',
    uploadedAt: new Date().toISOString(),
  }
}

export async function uploadMarkdownAttachment(target: MarkdownUploadTarget, file: File, onProgress?: (pct: number) => void) {
  if (target.kind === 'appeal-claim') return uploadAppealEvidence(target.id, file, onProgress)
  const mediaType = evidenceMediaType(file)
  const base = `/${target.owner}s/${target.id}/notes`
  const pre = await api.post<T.PresignResult>(`${base}/presign`, {
    filename: file.name,
    mediaType,
    sizeBytes: file.size,
  })
  try {
    await postObject(pre, file, onProgress)
    const completed = await api.post<CompletedUpload>(`${base}/${pre.evidenceId}/complete`)
    return completedEvidence(completed, file)
  } catch (error) {
    try {
      await api.del<void>(`${base}/${pre.evidenceId}`)
    } catch {
      // 保留原始上传错误；服务端维护任务会兜底清理残留对象。
    }
    throw error
  }
}

/* 提案草稿的佐证附件。走的是和正文贴图同一条 note evidence 通道
   （POST /objections/:id/notes/presign，router.go:149-151）：后端只认提案本人，
   而且只在提案还是草稿时放行——提交给班管之后就不能再往里塞东西了。 */
export const uploadObjectionEvidence = (id: string, file: File, onProgress?: (pct: number) => void, collective = false) =>
  uploadMarkdownAttachment({ kind: 'note', owner: collective ? 'governance/objection' : 'objection', id }, file, onProgress)

/* 举报的佐证附件。同一条 note evidence 通道，但后端那一路不记上传人，审计也不记
   actor——举报是匿名的，附件跟着匿名（note_evidence_handlers.go）。
   只在还没有复核人下过结论之前放行：判完了再补料，两个人看到的就不是同一份东西。 */
export const uploadReportEvidence = (id: string, file: File, onProgress?: (pct: number) => void) =>
  uploadMarkdownAttachment({ kind: 'note', owner: 'report', id }, file, onProgress)

/* 用 XHR 而不是 fetch：只有 XHR 能报上传进度。Policy 字段必须原样提交，
   file 放最后；浏览器生成 multipart Content-Type，所以不手工覆盖请求头。 */
function postObject(upload: { uploadUrl: string; uploadFields: Record<string, string> }, file: File, onProgress?: (pct: number) => void, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    const abort = () => xhr.abort()
    const form = new FormData()
    for (const [key, value] of Object.entries(upload.uploadFields)) form.append(key, value)
    form.append('file', file, file.name)
    xhr.open('POST', upload.uploadUrl)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      signal?.removeEventListener('abort', abort)
      if (xhr.status >= 200 && xhr.status < 300) resolve()
      else reject(new ApiError(xhr.status, xhr.status === 403 ? 'upload_expired' : xhr.status === 413 ? 'file_too_large' : 'upload_failed', ''))
    }
    xhr.onerror = () => {
      signal?.removeEventListener('abort', abort)
      reject(new ApiError(0, 'network_error', ''))
    }
    xhr.onabort = () => {
      signal?.removeEventListener('abort', abort)
      reject(new DOMException('上传已取消', 'AbortError'))
    }
    if (signal?.aborted) {
      reject(new DOMException('上传已取消', 'AbortError'))
      return
    }
    signal?.addEventListener('abort', abort, { once: true })
    xhr.send(form)
  })
}

/* disposition=inline 请求"浏览器就地渲染"而不是下载；能不能内联由后端按存下来的
   media_type 决定，回包里的 inline 才是最终结果。 */
export const evidenceUrl = (eid: string, disposition?: 'inline') =>
  api.get<T.EvidenceLink>(`/evidence/${eid}/url` + (disposition ? `?disposition=${disposition}` : ''))

/** 缩略图与预览共用的链接。预签名 10 分钟有效，这里缓存 9 分钟：
    每渲染一次就重新签发，会把审计日志刷满 evidence.url_issued。 */
export const useEvidenceLink = (eid: string, enabled: boolean) =>
  useQuery<T.EvidenceLink, Error, T.EvidenceLink, readonly unknown[]>({
    queryKey: K.evidenceLink(eid),
    queryFn: () => evidenceUrl(eid, 'inline'),
    enabled,
    staleTime: 9 * 60_000,
    gcTime: 9 * 60_000,
    retry: false,
  })

/* ---- 学生：基础项 / 分数 / 封存 ---- */

export const useBaseItems = () => useApiQuery<T.List<T.BaseItem>>(K.baseItems, '/me/base-items', { retry: false })
const manualScoreRefresh = {
  retry: false,
  refetchOnMount: 'always' as const,
  refetchInterval: false as const,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
}
export const useMyScore = () => useApiQuery<T.MyScore>(K.score, '/me/score', manualScoreRefresh)

export const useSeal = () => useApiQuery<T.SealState>(K.seal, '/me/seal', { retry: false })

/* ---- 学生：实时成绩与核对 ---- */


export const useMyScorecard = (opts?: { poll?: boolean }) =>
  useApiQuery<T.MyScorecard>(K.myScorecard, '/me/scorecard', {
    retry: false,
    refetchInterval: opts?.poll === false ? undefined : 8000,
  })

export function useResultConfirmActions() {
 const invalidate = useInvalidate()
 return useMutation({
  mutationFn: (value: { revision: string }) => api.post<{ confirmed: boolean; confirmedAt: string; revision: string }>('/me/scorecard/confirm', value),
  onSuccess: () => invalidate(K.myScorecard),
 })
}

export function useSealActions() {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: (v: { phrase: string; sid: string }) =>
      api.post<{ id: string; sealed: boolean; draftsExcluded: number }>('/me/seal', v),
    onSuccess: () => invalidate(K.window, K.seal, K.myScorecard, ['submissions'], ['admin']),
  })
}

/* ---- 申诉（两轮制） ---- */

export const useAppeals = () => useApiQuery<T.List<T.Appeal>>(K.appeals, '/appeals')
export const useAppeal = (id: string | null) =>
  useAdjudicationQuery<T.AppealDetail>(K.appeal(id ?? ''), `/appeals/${id}`, { enabled: !!id })

/** 补传佐证之后刷新那一条申诉。上传走的是直传对象存储，不经过 useMutation。 */
export function useInvalidateAppeal() {
  const invalidate = useInvalidate()
  return (id: string) => invalidate(K.appeal(id), K.appeals)
}

/** 学生发起申诉。轮次由后端按"这个对象已经申诉过几次"判定，前端不传。 */
export function useAppealActions() {
  const invalidate = useInvalidate()
  return {
    createDraft: useMutation({
      mutationFn: (v: { targetType: T.AppealTargetType; targetId: string }) =>
        api.post<{ id: string }>('/appeals/draft', v),
    }),
    submitDraft: useMutation({
      mutationFn: ({ id, ...claim }: { id: string; reason: string; category?: CategoryKey; itemKey?: string; score?: number }) =>
		api.post<{ id: string; round: T.AppealRound; status: T.AppealStatus; handlers: number }>(`/appeals/${id}/submit`, claim),
      /* 申诉改变待处理进度，成绩表同步刷新。 */
      onSuccess: (_result, input) => invalidate(K.appeal(input.id), K.appeals, ['submissions'], K.baseItems, K.myScorecard, ['admin'], ['review']),
    }),
    deleteDraft: useMutation({
      mutationFn: (id: string) => api.del<void>(`/appeals/${id}`),
      onSuccess: (_result, id) => invalidate(K.appeal(id), K.appeals, ['submissions'], K.baseItems),
    }),
    /* 复评人还没下结论时，把已提交的申诉退回草稿，理由和佐证都留着。 */
    withdraw: useMutation({
      mutationFn: (id: string) => api.post<{ id: string; status: string }>(`/appeals/${id}/withdraw`),
      onSuccess: (_result, id) => invalidate(K.appeal(id), K.appeals, ['submissions'], K.baseItems, K.myScorecard, ['admin'], ['review']),
    }),
    file: useMutation({
      mutationFn: (v: { targetType: T.AppealTargetType; targetId: string; reason: string; category?: CategoryKey; itemKey?: string; score?: number }) =>
        api.post<{ id: string; round: T.AppealRound; status: T.AppealStatus; handlers: number }>('/appeals', v),
      onSuccess: () => invalidate(K.appeals, ['submissions'], K.baseItems, K.myScorecard, ['admin'], ['review']),
    }),
  }
}

/* ---- 综测小组：处理申诉（第一轮复评） ---- */

export const useReviewAppeals = (scope: 'pending' | 'done' | 'all') =>
  useApiQuery<T.List<T.AppealTask>>(K.reviewAppeals(scope), `/review/appeals?scope=${scope}`, { refetchInterval: 15000 })

export const useReviewAppeal = (id: string | null) =>
  useApiQuery<T.AppealDetail>(K.reviewAppeal(id ?? ''), `/review/appeals/${id}`, { enabled: !!id, refetchInterval: 15000 })

export function useRereviewActions() {
  const invalidate = useInvalidate()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; decision: T.RereviewDecision; category?: CategoryKey; itemKey?: string; score: number; reason: string; spentSeconds: number }) =>
      api.post<T.RereviewResult>(`/review/appeals/${id}/rereview`, v),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['review'] })
      invalidate(['admin'], ['submissions'], K.appeals)
    },
    onError: (error) => {
      if (error instanceof ApiError && (error.status === 409 || error.status === 403)) void invalidate(['review', 'appeals'])
    },
  })
}

/* ---- 综测小组：扣分与异议 ---- */

export const useReviewStudents = (collective = false) => useApiQuery<T.List<T.ReviewStudent>>(collective ? ['governance','students'] : K.reviewStudents, collective ? '/governance/students' : '/review/students')
export const useBonusGrants = (collective = false) => useApiQuery<T.List<T.BonusGrantBatch>>([collective ? 'governance' : 'admin', 'bonus-grants'], `/${collective ? 'governance' : 'admin'}/bonus-grants`)
export const createBonusGrantUpload = (collective = false) => api.post<{ id: string }>(`/${collective ? 'governance' : 'admin'}/bonus-grant-uploads`)
export const removeBonusGrantEvidence = (id: string, eid: string, collective = false) => api.del<void>(`/${collective ? 'governance' : 'admin'}/bonus-grant-uploads/${id}/notes/${eid}`)
export function useBonusGrantAction(collective = false) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: T.BonusGrantInput) => api.post<{ id: string; count: number; score: number }>(collective ? '/governance/proposals' : '/admin/bonus-grants', collective ? {requestId: input.requestId, kind:'bonus', action:'bonus', title:input.title, body:input.note, payload:input} : input),
    onSuccess: async () => {
      await Promise.all([
        ['governance'], ['admin'], ['review'], ['submissions'], ['score-history'], K.score, K.myScorecard, K.classPenalties, K.seal,
      ].map((queryKey) => qc.invalidateQueries({ queryKey })))
    },
  })
}

/* 共治模式把同一批处理接口挂在 /governance 下，由「是否生效成员」代替小组角色
   放行（router.go 的 collective 组）。前端只换前缀，页面和组件一份。 */
const deskBase = (collective: boolean) => (collective ? '/governance' : '/review')
const deskKey = (collective: boolean) => (collective ? 'governance' : 'review')

export const useScorecard = (uid: string | null, collective = false) =>
  useApiQuery<T.StudentScorecard>([deskKey(collective), 'scorecard', uid ?? ''], `${deskBase(collective)}/students/${uid}/scorecard`, { enabled: !!uid })

export const useScoreHistory = (kind: T.ReportKind, id: string | null, audience: 'student' | 'reviewer', collective = false) =>
  useApiQuery<T.ScoreHistory>(['score-history', audience, deskKey(collective), kind, id],
    audience === 'student' ? `/class-penalties/${kind}/${id}/history` : `${deskBase(collective)}/score-history/${kind}/${id}`,
    { enabled: !!id, refetchInterval: 15000 })

export const useObjections = (filter: { status?: string } = {}, collective = false) => {
  const qs = search(filter)
  return useApiQuery<T.List<T.Objection>>([deskKey(collective), 'objections', qs], `${deskBase(collective)}/objections` + qs)
}

export interface ObjectionInput {
  kind: T.ObjectionKind
  studentUserId: string
  category: CategoryKey
  itemKey: string
  /** base/penalty 从未录入过时为 null；submission 必填 */
  targetId: string | null
  proposedScore: number
  quantity?: number | null
  basis: string
}

/** 计分卡上一行能给出的部分：学生是选出来的，不由行自己决定。 */
export type NewObjection = Omit<ObjectionInput, 'studentUserId'>

/** 附件传完之后要把提案列表拉一遍——草稿箱那份 noteEvidence 是随列表下发的。 */
export function useInvalidateObjections(collective = false) {
  const invalidate = useInvalidate()
  return () => invalidate(['review'], ['admin'], ...(collective ? [['governance'] as const] : []))
}

export function useObjectionActions(collective = false) {
  const invalidate = useInvalidate()
  const base = deskBase(collective)
  /* 提案会改动某个学生的计分卡视图（本人已挂几条提案），所以整段一起失效。 */
  const after = () => invalidate(['review'], ['admin'], ...(collective ? [['governance'] as const] : []))
  return {
    create: useMutation({
      mutationFn: (v: ObjectionInput) => api.post<{ id: string; status: T.ObjectionStatus }>(`${base}/objections`, v),
      onSuccess: after,
    }),
    update: useMutation({
      mutationFn: ({ id, ...v }: ObjectionInput & { id: string }) => api.put<{ id: string }>(`${base}/objections/${id}`, v),
      onSuccess: after,
    }),
    remove: useMutation({ mutationFn: (id: string) => api.del<void>(`${base}/objections/${id}`), onSuccess: after }),
    /* 批量提交是这一页的核心动作：一次点头把整批交出去，
       而不是每条弹一次确认——扣分录入天然是成批的。 */
    submit: useMutation({
      mutationFn: (ids: string[]) => api.post<{ batchId: string; submitted: number }>(`${base}/objections/submit`, { ids }),
      onSuccess: after,
    }),
    withdraw: useMutation({
      mutationFn: (id: string) => api.post<{ id: string; status: T.ObjectionStatus }>(`${base}/objections/${id}/withdraw`),
      onSuccess: after,
    }),
  }
}

/* ---- 综测小组 ---- */

export const useReviewTasks = (tab: T.ReviewTab, enabled = true) =>
  useApiQuery<{ items: (T.ReviewTask | T.AppealTask | T.ReportTask)[]; tab: string }>(K.reviewTasks(tab), `/review/tasks?tab=${tab}`, { enabled })

export const useReviewTask = (id: string | null, refreshOnMount = false) =>
  useApiQuery<T.ReviewTaskDetail>(K.reviewTask(id ?? ''), `/review/tasks/${id}`, { enabled: !!id, refetchOnMount: refreshOnMount ? 'always' : true })

/* ---- 学生匿名举报 ---- */

/** 全班扣分总览。开关关掉时后端直接 403，这里不预判，交给页面显示后端那句话。 */
export const useClassPenalties = (enabled = true) =>
  useApiQuery<T.ClassPenalties>(K.classPenalties, '/class-penalties', { enabled })

export const useMyReports = (enabled = true) =>
  useApiQuery<T.List<T.MyReport>>(K.myReports, '/reports/mine', { enabled })

/* 举报建完之后还要再传佐证，那一步落在 mutation 的 onSuccess 之外，
   所以要能再刷一次——否则「我提交的举报」上那条会一直显示没带附件。 */
export function useInvalidateReports() {
  const invalidate = useInvalidate()
  return () => invalidate(K.classPenalties, K.myReports, ['review'])
}

export function useReportActions() {
  const invalidate = useInvalidate()
  return {
    file: useMutation({
      mutationFn: (v: {
        studentUserId: string
        kind: T.ReportKind
        category: CategoryKey
        itemKey: string
        /** base_score.id 或 submission.id；扣分项从无到有时没有 */
        targetId?: string
        /** 只有扣分项传次数 */
        quantity?: number
        /** 基础项与已定分条目传建议分 */
        proposedScore?: number
        basis: string
      }) => api.post<{ id: string; status: T.ReportStatus; reviewers: number }>('/reports', v),
      /* 举报会立刻进两个人的审核队列，也会改变全班总览上的"未结举报"计数。 */
      onSuccess: () => invalidate(K.classPenalties, K.myReports, ['review']),
    }),
  }
}

/** 举报复核任务详情。队列里的 id 带 `report:` 前缀，调用前必须剥掉。 */
export const useReviewReport = (id: string | null) =>
  useApiQuery<T.ReportTaskDetail>(K.reviewReport(id ?? ''), `/review/reports/${id}`, { enabled: !!id })

export const useReportTarget = (id: string) =>
  useApiQuery<T.ReportTargetDetail>(['review', 'report-target', id], `/review/reports/${id}/target`)

export function useReportReviewActions() {
  const invalidate = useInvalidate()
  return {
    decide: useMutation({
      mutationFn: ({ id, ...v }: { id: string; decision: T.ReportDecision; score?: number; reason: string; spentSeconds: number }) =>
        api.post<{ reportStatus: T.ReportStatus; bothDecided: boolean; conflict: boolean; finalScore: number | null }>(`/review/reports/${id}/decision`, v),
      onSuccess: () => invalidate(['review'], ['admin'], K.classPenalties, K.myReports),
    }),
  }
}

export const useAdminReports = () => useAdjudicationQuery<T.List<T.AdminReport>>(K.adminReports, '/admin/reports')

export function useReportFinalActions() {
  const invalidate = useInvalidate()
  const base = useAdjudicationBase()
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; score: number; reason: string }) =>
      api.post<{ id: string; status: T.ReportStatus; score: number }>(`${base}/reports/${id}/final`, v),
    onSuccess: () => invalidate(['admin'], ['review'], K.classPenalties, K.myReports),
  })
}

/** 我的审核量 + 按大项的分布。按条均衡之后没有"我的大项"了，两块数据一次取回。 */
export const useReviewCategory = () =>
  useApiQuery<{ items: T.CategorySummary[]; load: T.ReviewLoad }>(K.reviewCategory, '/review/category')
export const useReviewHistory = () => useApiQuery<T.List<T.ReviewHistoryRow>>(K.reviewHistory, '/review/history')

/* 这里曾经有一个 recordBaseItem（POST /review/base-items，审核人直接写基础分与扣分）。
   两轮申诉改造时它被撤掉了：基础分与扣分现在一律走「扣分与异议」的提案 → 管理员终裁，
   小组手上不再有直接改分的能力。留着它等于留一条绕开终裁的后门。 */
export function useReviewActions() {
  const invalidate = useInvalidate()
  return {
    decide: useMutation({
      mutationFn: ({ id, ...v }: { id: string; reviewId?: string; decision: T.ReviewDecision; score?: number; reason: string; spentSeconds: number; classificationSuggestion?: { category: CategoryKey; itemKey: string; reason: string } }) =>
        v.reviewId
          ? api.put<T.ReviewDecisionResult>(`/review/tasks/${id}/decision`, v)
          : api.post<T.ReviewDecisionResult>(`/review/tasks/${id}/decision`, v),
      onSuccess: () => invalidate(['review'], ['admin'], ['submissions']),
    }),
  }
}

/* ---- 管理端：方案与窗口 ---- */

export const useAdminSchemes = () => useApiQuery<T.List<T.SchemeMeta>>(K.adminSchemes, '/admin/scheme')
export const useAvailableTemplates = () => useApiQuery<T.List<T.AvailableTemplate>>(K.adminTemplates, '/admin/templates')
export const useAdminTemplateShareRequests = () => useApiQuery<T.List<T.AdminTemplateShareRequest>>(K.adminTemplateShares, '/admin/template-share-requests')
export const useAdminScheme = (id: string | null) =>
  useApiQuery<T.SchemeDraft>(K.adminScheme(id ?? ''), `/admin/scheme/${id}`, { enabled: !!id })
export const useAdminClass = () => useApiQuery<T.ClassInfo>(K.adminClass, '/admin/class')

export function useSchemeActions() {
  const invalidate = useInvalidate()
  const after = () => invalidate(['admin'], K.scheme, K.window)
  return {
    create: useMutation({
      mutationFn: (v: { name: string; config?: unknown; templateId?: number; sourceSchemeId?: number }) =>
        api.post<{ id: string; lockVersion: number }>('/admin/scheme', v),
      onSuccess: after,
    }),
    update: useMutation({
      mutationFn: ({ id, ...v }: { id: string; name: string; config: unknown; lockVersion: number }) =>
        api.put<{ id: string; lockVersion: number }>(`/admin/scheme/${id}`, v),
      onSuccess: after,
    }),
    publish: useMutation({
      mutationFn: (id: string) => api.post<{ id: string; version: number; config: SchemeConfig }>(`/admin/scheme/${id}/publish`),
      onSuccess: after,
    }),
    remove: useMutation({
      mutationFn: (id: string) => api.del<void>(`/admin/scheme/${id}`),
      onSuccess: after,
    }),
	share: useMutation({ mutationFn: (id: string) => api.post<{ requestId: string; status: string }>(`/admin/scheme/${id}/share`), onSuccess: after }),
    /* 改窗口与改能力开关都会克隆出一个新的已发布版本，不是原地改，
       所以成功后整个 admin 段都要重新取。 */
    /* 时间线不再产生方案版本，所以这两个 mutation 只失效 window 相关的查询。
       后端仍然接受旧的 /admin/window 作为别名，但前端只走新路径。 */
    setTimeline: useMutation({
      mutationFn: (v: {
        open: string
        close: string
        lockdown: string | null
        publicity?: T.PublicityWindow | null
        honorRollTopPercent: number
        awards: T.AwardTier[]
        collegeName: string
        enrollmentClass: string
        academicYear: string
      }) => api.put<unknown>('/admin/timeline', v),
      onSuccess: after,
    }),
    setCapability: useMutation({
      mutationFn: (v: { key: string; on: boolean }) => api.put<unknown>('/admin/window/capabilities', v),
      onSuccess: after,
    }),
  }
}

/* ---- 管理端：名册与封存 ---- */

export const useWhitelist = () => useApiQuery<T.List<T.WhitelistRow>>(K.whitelist, '/admin/whitelist')
export const useAdminUsers = () => useApiQuery<T.List<T.AdminUser>>(K.adminUsers, '/admin/users')
export const useDeputyAssignment = () => useApiQuery<{ deputy: { id: string; sid: string; name: string; registered: boolean } | null }>(['admin', 'deputy'], '/admin/deputy')
export const useAdminSeals = () => useApiQuery<T.List<T.AdminSeal>>(K.adminSeals, '/admin/seals', { refetchInterval: 10000 })
export const useAdminReviewProgress = (query: string) =>
  useApiQuery<{ items: T.AdminReviewProgressRow[]; page: number; pageSize: number; total: number }>(K.adminReviewProgress(query), `/admin/review-progress${query}`, { refetchInterval: 10000 })
export function useReviewProgressActions() {
	const invalidate = useInvalidate()
	return { remind: useMutation({ mutationFn: (v: { kind: string; q: string; reviewer: string }) => api.post<{ recipientCount: number; overdueCount: number }>('/admin/review-progress/remind', v), onSuccess: () => invalidate(['admin', 'review-progress']) }) }
}

export function useForceRejectSubmission(selfReject = false, forceScore = false) {
  const qc = useQueryClient()
  const base = useAdjudicationBase()
  return useMutation({
    mutationFn: ({ id, reason, score, previousScore }: { id: string; reason: string; score?: number; previousScore?: number }) =>
      api.post<void>(`${selfReject ? '' : base}/submissions/${id}/${forceScore && !selfReject ? 'force-score' : 'force-reject'}`, { reason, score, previousScore }),
    onSuccess: async () => {
      await Promise.all([
        ['admin'], ['review'], ['submissions'], ['score-history'], K.score, K.myScorecard, K.appeals, K.baseItems, K.seal,
      ].map((queryKey) => qc.invalidateQueries({ queryKey })))
    },
  })
}
export const useReviewSLA = () => useApiQuery<{ itemHours: number }>(K.reviewSLA, '/admin/review-sla')
export function useReviewSLAActions() {
	const invalidate = useInvalidate()
	return { update: useMutation({ mutationFn: (v: { itemHours: number }) => api.put<{ itemHours: number }>('/admin/review-sla', v), onSuccess: () => invalidate(['admin']) }) }
}

export function useRosterActions() {
  const invalidate = useInvalidate()
  const after = () => invalidate(['admin'])
  return {
    setDeputy: useMutation({
      mutationFn: (userId: string | null) => api.put<void>('/admin/deputy', { userId }),
      onSuccess: () => invalidate(['admin'], ['review']),
    }),
    addWhitelist: useMutation({
      mutationFn: (v: { sid: string; name: string }) => api.post<{ id: string }>('/admin/whitelist', v),
      onSuccess: after,
    }),
    importWhitelist: useMutation({
      mutationFn: (csv: string) => api.post<{ imported: number }>('/admin/whitelist/import', { csv }),
      onSuccess: after,
    }),
    removeWhitelist: useMutation({ mutationFn: (id: string) => api.del<void>(`/admin/whitelist/${id}`), onSuccess: after }),
    setRole: useMutation({
      mutationFn: (v: { id: string; role: string }) => api.put<void>(`/admin/users/${v.id}/role`, { role: v.role }),
      onSuccess: after,
    }),
    setStatus: useMutation({
      mutationFn: (v: { id: string; status: 'active' | 'disabled' }) => api.put<void>(`/admin/users/${v.id}/status`, { status: v.status }),
      onSuccess: after,
    }),
    resetMemberPassword: useMutation({
      mutationFn: (id: string) => api.post<{ reset: number }>(`/admin/users/${id}/reset-password`),
      onSuccess: after,
    }),
    resetMemberPasswords: useMutation({
      mutationFn: () => api.post<{ reset: number }>('/admin/users/reset-password'),
      onSuccess: after,
    }),
    remind: useMutation({ mutationFn: (v?: { userId?: string }) => api.post<{ recipientCount: number }>('/admin/seals/remind', v ?? {}) }),
    unseal: useMutation({
      mutationFn: (v: { uid: string; reason: string }) => api.post<void>(`/admin/seals/${v.uid}/unseal`, { reason: v.reason }),
      onSuccess: () => invalidate(['admin'], K.window, K.seal, K.myScorecard, ['submissions']),
    }),
  }
}

/* ---- 管理端：分发 ---- */

/** 当前分发状态：每个人背了多少、还有多少没分出去、种子是多少（§5.3）。 */
export const useDispatch = () => useApiQuery<T.DispatchState>(['admin', 'dispatch'], '/admin/dispatch', { retry: false })

/** 某个审核人手上的待审队列。只有展开某人时才拉。 */
export const useDispatchQueue = (uid: string | null) =>
  useApiQuery<T.List<T.DispatchQueueRow>>(['admin', 'dispatch', 'queue', uid ?? ''], `/admin/dispatch/reviewers/${uid}/queue`, {
    enabled: !!uid,
  })

/** 试算与执行的入参同一套：includeAssigned=true 表示连已分配但还没开评的一起重排。 */
export interface DispatchInput {
  seed?: string
  avoidSelf?: boolean
  includeAssigned?: boolean
}

export function useDispatchActions() {
  const invalidate = useInvalidate()
  const after = () => invalidate(['admin'], ['review'])
  return {
    preview: useMutation({ mutationFn: (v: DispatchInput) => api.post<T.DispatchPlan>('/admin/dispatch/preview', v) }),
    run: useMutation({
      mutationFn: (v: DispatchInput) => api.post<T.DispatchRunResult>('/admin/dispatch/run', v),
      onSuccess: after,
    }),
    setAuto: useMutation({
      mutationFn: (on: boolean) => api.put<{ auto: boolean }>('/admin/dispatch/auto', { on }),
      onSuccess: after,
    }),
    setPaused: useMutation({
      mutationFn: (v: { uid: string; paused: boolean }) =>
        api.put<T.DispatchReviewer>(`/admin/dispatch/reviewers/${v.uid}`, { paused: v.paused }),
      onSuccess: after,
    }),
    /* 换掉某一条上的某一个人。已经交了结论的那一位换不动——
       换走他等于抹掉一条已存在的裁定，那是仲裁的事，不是分发的事。 */
    reassign: useMutation({
      mutationFn: (v: { submissionId: string; from: string; to: string; reason: string }) =>
        api.put<{ submissionId: string; reviewers: T.DispatchReviewer[] }>(`/admin/dispatch/submissions/${v.submissionId}`, {
          from: v.from,
          to: v.to,
          reason: v.reason,
        }),
      onSuccess: after,
    }),
    /* 整体转出：某人退出班委或长期失联时，把他手上没开评的全部转走。
       省略 to 就交给均衡算法重新摊到其余人头上。 */
    handover: useMutation({
      mutationFn: (v: { from: string; to?: string; reason: string }) =>
        api.post<{ moved: number; rows: T.DispatchPlanRow[] }>('/admin/dispatch/handover', v),
      onSuccess: after,
    }),
  }
}

/* ---- 管理端：仲裁 ---- */

export const useAdminSubmissions = (filter: { id?: string; status?: string; category?: string; student?: string; q?: string; page?: number; page_size?: number; forceRejectable?: boolean } = {}, enabled = true) => {
  const { forceRejectable, ...rest } = filter
  const qs = search({ ...rest, force_rejectable: forceRejectable ? 'true' : undefined })
  return useAdjudicationQuery<T.Page<T.AdminSubmission>>(K.adminSubmissions(qs), '/admin/submissions' + qs, { enabled })
}

export const useAdminAppeals = (filter: { status?: string; category?: string; student?: string; q?: string; origin?: 'batch' | 'direct'; page?: number; page_size?: number } = {}) => {
  const qs = search(filter)
	return useAdjudicationQuery<T.Page<T.Appeal>>(K.adminAppeals(qs), '/admin/appeals' + qs)
}

export function useArbitrationActions() {
  const invalidate = useInvalidate()
  const base = useAdjudicationBase()
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; decision?: T.RereviewDecision; category?: CategoryKey; itemKey?: string; score: number; reason: string }) =>
		api.post<{ id: string; status: SubmissionStatus; finalScore: number; category: CategoryKey; itemKey: string }>(`${base}/submissions/${id}/arbitrate`, v),
    onSuccess: () => invalidate(['admin'], ['review'], ['submissions']),
  })
}

/** 申诉终裁。两轮制下只有管理员能签发 final，小组那边只有复评。 */
export function useAppealFinalActions() {
  const invalidate = useInvalidate()
  const base = useAdjudicationBase()
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; decision?: T.RereviewDecision; category?: CategoryKey; itemKey?: string; score: number; reason: string }) =>
		api.post<{ id: string; status: T.AppealStatus; score: number; category: CategoryKey; itemKey: string }>(`${base}/appeals/${id}/final`, v),
    onSuccess: () => invalidate(['admin'], ['review'], ['submissions'], K.appeals),
  })
}

export const useClassificationSuggestions = () => useAdjudicationQuery<T.List<T.ClassificationSuggestion>>(K.classificationSuggestions, '/admin/classification-suggestions?status=pending')

export function useClassificationActions() {
	const invalidate = useInvalidate()
	const base = useAdjudicationBase()
	const after = () => invalidate(K.classificationSuggestions, ['admin'], ['review'], ['submissions'])
	return {
		suggest: useMutation({
			mutationFn: ({ submissionId, ...v }: { submissionId: string; category: CategoryKey; itemKey: string; reason: string }) => api.post<{ id: string; scope: T.ClassificationScope; status: string }>(`${base}/submissions/${submissionId}/classification/suggest`, v),
			onSuccess: after,
		}),
		resolve: useMutation({
			mutationFn: ({ submissionId, ...v }: { submissionId: string; suggestionId?: string; category: CategoryKey; itemKey: string; score?: number; reason: string; originBatchId?: string }) => api.post<T.ClassificationResolution>(`${base}/submissions/${submissionId}/classification/resolve`, v),
			onSuccess: after,
		}),
	}
}

/* ---- 管理端：小组异议终裁 ---- */

export const useAdminObjections = (filter: { status?: string; category?: string; student?: string; q?: string; page?: number; page_size?: number } = {}) => {
  const qs = search(filter)
	return useAdjudicationQuery<T.Page<T.Objection>>(K.adminObjections(qs), '/admin/objections' + qs)
}

export function useObjectionDecisionActions() {
  const invalidate = useInvalidate()
  const base = useAdjudicationBase()
  const after = () => invalidate(['admin'], ['review'], ['submissions'], ['score-history'])
  return {
    decide: useMutation({
      mutationFn: ({ id, ...v }: { id: string; action: 'apply' | 'adjust' | 'dismiss'; score?: number; reason: string }) =>
        api.post<T.ObjectionDecisionResult>(`${base}/objections/${id}/decide`, v),
      onSuccess: after,
    }),
    /* 整批通过/驳回。一个班一学期的缺勤扣分可能上百条，逐条签名不现实；
       批量动作只有 apply 与 dismiss，改分必须逐条看——那才是需要判断的地方。 */
    decideBatch: useMutation({
      mutationFn: (v: { ids: string[]; action: 'apply' | 'dismiss'; reason: string }) =>
        api.post<{ decided: number }>(`${base}/objections/decide-batch`, v),
      onSuccess: after,
    }),
  }
}

/* ---- 管理端：专业素质分 ---- */

/* 共治成员读同一份导入结果：核验提案要对着"谁还缺"来写，光有一个粘贴框没法核。
   写入口不在这里——共治只能通过提案执行 gpa 导入。 */
export const useGpa = (collective = false) =>
  useApiQuery<T.GpaState>(collective ? ['governance', 'gpa'] : K.gpa, `${collective ? '/governance' : '/admin'}/gpa`, { retry: false })

export function useGpaActions() {
  const invalidate = useInvalidate()
  const after = () => invalidate(['admin'])
  return {
    paste: useMutation({
      mutationFn: (text: string) => api.post<T.GpaImportResult>('/admin/gpa/paste', { text }),
      onSuccess: after,
    }),
    /* 文件走 multipart，不能用 api.post（那边固定 JSON 序列化）。 */
    upload: useMutation({
      mutationFn: (file: File) => {
        const form = new FormData()
        form.append('file', file)
        return api.form<T.GpaImportResult>('/admin/gpa/import', form)
      },
      onSuccess: after,
    }),
  }
}

/* ---- 管理端：闸门、结算、导出 ---- */

export const useGate = (collective = false) =>
  useApiQuery<T.Gate>(collective ? ['governance', 'gate'] : K.gate, `${collective ? '/governance' : '/admin'}/gate`, { retry: false })
export const useStats = () => useApiQuery<T.AdminStats>(K.stats, '/admin/stats', manualScoreRefresh)

export function useSettlementActions() {
  const invalidate = useInvalidate()
  return {
    force: useMutation({
      mutationFn: (reason: string) => api.post<T.Gate>('/admin/gate/force', { reason }),
      onSuccess: () => invalidate(['admin']),
    }),
    settle: useMutation({
      mutationFn: () => api.post<T.SettlementResult>('/admin/settle'),
      onSuccess: () => invalidate(['admin'], ['submissions'], K.score),
    }),
    requestExport: useMutation({
      mutationFn: (kind: T.ExportKind) => api.post<T.ExportJob>('/admin/export', { kind }),
      onSuccess: () => invalidate(['admin']),
    }),
  }
}

/** Export polling backs off on errors and never refetches a link on focus. */
export const useExportJob = (jobId: string | null) =>
  useApiQuery<T.ExportJob>(K.exportJob(jobId ?? ''), `/admin/export/${jobId}`, {
    enabled: !!jobId,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: (count, error) => count < 3 && (!(error instanceof ApiError) || error.status === 0 || error.status === 429 || error.status >= 500),
    retryDelay: (attempt, error) => Math.max(Math.min(2000 * 2 ** attempt, 30_000), error instanceof ApiError && error.status === 429 ? Number((error.detail as { retryAfter?: number } | null)?.retryAfter ?? 30) * 1000 : 0),
    refetchInterval: (query) => {
      if (query.state.error) return false
      const s = query.state.data?.status
      return s === 'queued' || s === 'running' ? 5000 : false
    },
  })

/* ---- 管理端：审计 ---- */

export const useAuditLog = (filter: { action?: string; resourceType?: string; actor?: string; from?: string; to?: string; page?: number; page_size?: number } = {}) => {
  const qs = search(filter)
  return useApiQuery<T.Page<T.AuditEntry>>(K.auditLog(qs), '/admin/audit-log' + qs)
}

/* ---- 班管知识库 ---- */

export const useKnowledge = () =>
  useQuery<T.KnowledgeState, Error, T.KnowledgeState, readonly unknown[]>({
    queryKey: K.knowledge,
    queryFn: async ({ signal }) => knowledgeStateSchema.parse(await api.get<unknown>('/admin/knowledge', signal)),
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.documents.some((document) => ['uploading', 'queued', 'processing'].includes(document.status)) ? 2000 : false,
  })

export const useKnowledgeDocument = (id: string | null) =>
  useQuery<T.KnowledgeDocumentDetail, Error, T.KnowledgeDocumentDetail, readonly unknown[]>({
    queryKey: K.knowledgeDocument(id ?? ''),
    queryFn: async ({ signal }) => knowledgeDocumentDetailSchema.parse(await api.get<unknown>(`/admin/knowledge/documents/${id}`, signal)),
    enabled: !!id,
    retry: false,
  })

export const useKnowledgeEntry = (id: string | null, startLine = 1, endLine = 200) =>
  useQuery<T.KnowledgeEntry, Error, T.KnowledgeEntry, readonly unknown[]>({
    queryKey: K.knowledgeEntry(id ?? '', startLine, endLine),
    queryFn: async ({ signal }) => knowledgeEntrySchema.parse(await api.get<unknown>(`/admin/knowledge/entries/${id}?startLine=${startLine}&endLine=${endLine}`, signal)),
    enabled: !!id,
    retry: false,
  })

export async function uploadKnowledgeDocument(
  file: File,
  logicalPath: string,
  onProgress?: (percent: number) => void,
  signal?: AbortSignal,
) {
  const mediaType = file.type || evidenceMediaType(file)
  const pre = knowledgePresignSchema.parse(await api.post<unknown>('/admin/knowledge/documents/presign', {
    filename: file.name,
    logicalPath,
    mediaType,
    sizeBytes: file.size,
  }))
  try {
    await postObject(pre, file, onProgress, signal)
    await api.post(`/admin/knowledge/documents/${pre.documentId}/complete`, { etag: '' })
    return pre.documentId
  } catch (error) {
    try {
      await api.del(`/admin/knowledge/documents/${pre.documentId}`)
    } catch {
      // 保留最初错误；未完成对象由后端维护任务清理。
    }
    throw error
  }
}

export function useKnowledgeActions() {
  const invalidate = useInvalidate()
  const after = () => invalidate(K.knowledge)
  return {
    setPolicy: useMutation({
      mutationFn: (externalProcessingApproved: boolean) => api.put<void>('/admin/knowledge/policy', { externalProcessingApproved }),
      onSuccess: after,
    }),
    updateDocument: useMutation({
      mutationFn: ({ id, ...input }: { id: string; displayName: string }) =>
        api.put<void>(`/admin/knowledge/documents/${id}`, input),
      onSuccess: (_, input) => invalidate(K.knowledge, K.knowledgeDocument(input.id)),
    }),
    reprocess: useMutation({
      mutationFn: (id: string) => api.post(`/admin/knowledge/documents/${id}/reprocess`),
      onSuccess: (_, id) => invalidate(K.knowledge, K.knowledgeDocument(id)),
    }),
    remove: useMutation({
      mutationFn: (id: string) => api.del<void>(`/admin/knowledge/documents/${id}`),
      onSuccess: after,
    }),
  }
}

/* ---- 全班工具型 Agent ---- */

export const useAgentStatus = () =>
  useQuery<T.AgentStatus, Error, T.AgentStatus, readonly unknown[]>({
    queryKey: K.agentStatus,
    queryFn: async ({ signal }) => agentStatusSchema.parse(await api.get<unknown>('/agent/status', signal)),
    retry: false,
    staleTime: 5000,
    refetchInterval: 15000,
  })

export const useAgentConversations = (enabled = true) =>
  useQuery<T.List<T.AgentConversationSummary>, Error, T.List<T.AgentConversationSummary>, readonly unknown[]>({
    queryKey: K.agentConversations,
    queryFn: async ({ signal }) => agentConversationsSchema.parse(await api.get<unknown>('/agent/conversations', signal)),
    enabled,
    retry: false,
  })

export const useAgentConversation = (id: string | null, enabled = true, awaitingNewMessage = false) =>
  useQuery<T.AgentConversation, Error, T.AgentConversation, readonly unknown[]>({
    queryKey: K.agentConversation(id ?? ''),
    queryFn: async ({ signal }) => agentConversationSchema.parse(await api.get<unknown>(`/agent/conversations/${id}`, signal)),
    enabled: enabled && !!id,
    retry: false,
    /* 运行中每 800ms 拉一次。Worker 在每个工具步骤开始时就落库，所以这个间隔
       决定了思维链和工具链路的回显延迟；再快就只是空转，后端一步通常要几秒。 */
    refetchInterval: (query) =>
      awaitingNewMessage || query.state.data?.messages.some((message) => message.status === 'queued' || message.status === 'running') ? 800 : false,
  })

export const useAgentAttachmentLink = (id: string, enabled = true) =>
  useQuery<T.AgentAttachmentLink, Error, T.AgentAttachmentLink, readonly unknown[]>({
    queryKey: K.agentAttachmentLink(id),
    queryFn: async ({ signal }) => agentAttachmentLinkSchema.parse(await api.get<unknown>(`/agent/attachments/${id}/url`, signal)),
    enabled: enabled && !!id,
    staleTime: 9 * 60_000,
    gcTime: 9 * 60_000,
    retry: false,
  })

export async function uploadAgentAttachment(file: File, onProgress?: (percent: number) => void) {
  const mediaType = agentImageMediaType(file)
  const pre = agentAttachmentPresignSchema.parse(await api.post<unknown>('/agent/attachments/presign', {
    filename: file.name,
    mediaType,
    sizeBytes: file.size,
  }))
  try {
    await postObject(pre, file, onProgress)
    return agentAttachmentSchema.parse(await api.post<unknown>(`/agent/attachments/${pre.attachmentId}/complete`, { etag: '' }))
  } catch (error) {
    try {
      await api.del(`/agent/attachments/${pre.attachmentId}`)
    } catch {
      // 保留上传本身的错误；未绑定对象由维护策略兜底。
    }
    throw error
  }
}

export const removeAgentAttachment = (id: string) => api.del<void>(`/agent/attachments/${id}`)

function agentImageMediaType(file: Pick<File, 'name' | 'type'>): T.AgentAttachment['mediaType'] {
  const type = file.type.trim().toLowerCase()
  if (type === 'image/jpeg' || type === 'image/png' || type === 'image/webp') return type
  const extension = fileExtension(file.name)
  if (extension === 'jpg' || extension === 'jpeg') return 'image/jpeg'
  if (extension === 'png') return 'image/png'
  if (extension === 'webp') return 'image/webp'
  return type as T.AgentAttachment['mediaType']
}

/* 点引用时才去要下载地址：预签名地址只活 10 分钟，提前拿好的那份等到用户真去点
   往往已经过期，而且每条引用都预取会平白多出一串审计。 */
export const useAgentSourceDownload = () =>
  useMutation({
    /* 文件级引用的 entryId 是空串，直接拼进路径会变成结尾多一个斜杠、匹配不到路由；
       后端把 0 当作"整份文件"。 */
    mutationFn: async (citation: T.AgentCitation) =>
      agentSourceSchema.parse(await api.get<unknown>(`/agent/sources/${citation.documentId}/${citation.entryId || '0'}?${new URLSearchParams({ audienceRole: citation.audienceRole, revision: String(citation.locator.revision ?? '') })}`)),
  })

export const readAgentPageContext = async (context: T.AgentPageContext, observedAt = '') => agentPageStateSchema.parse(await api.get<unknown>(`/agent/context?${new URLSearchParams({
  view: context.view, resourceKind: context.resourceKind ?? '', resourceId: context.resourceId ?? '', evidenceId: context.evidenceId ?? '', observedAt,
})}`))

export function useAgentPageContext(context: T.AgentPageContext | null, observedVersion: string) {
  const user = useApp((s) => s.user)
  return useQuery({ queryKey: ['agent-page-context', user?.classId, user?.sid, context?.view, context?.resourceKind, context?.resourceId, context?.evidenceId, observedVersion],
    queryFn: () => readAgentPageContext(context!), enabled: !!context?.resourceId, staleTime: 0, refetchInterval: 15000, retry: false,
  })
}

export function useAgentActions() {
  const invalidate = useInvalidate()
  const refresh = (id?: string) => invalidate(K.agentConversations, id ? K.agentConversation(id) : ['agent', 'conversation'])
  return {
    createConversation: useMutation({
      mutationFn: async (title?: string) => agentConversationSchema.parse(await api.post<unknown>('/agent/conversations', { title })),
      onSuccess: () => refresh(),
    }),
    deleteConversation: useMutation({
      mutationFn: (id: string) => api.del<void>(`/agent/conversations/${id}`),
      onSuccess: () => refresh(),
    }),
    sendMessage: useMutation({
      mutationFn: (input: { conversationId: string; content: string; attachmentIds?: string[]; context?: T.AgentPageContext }) =>
        api.post<{ messageId: string; status: 'queued' }>(`/agent/conversations/${input.conversationId}/messages`, {
          content: input.content,
          attachmentIds: input.attachmentIds ?? [],
          context: input.context,
        }),
      onSuccess: (_, input) => refresh(input.conversationId),
    }),
    cancelMessage: useMutation({
      mutationFn: ({ messageId }: { messageId: string; conversationId: string }) => api.post(`/agent/messages/${messageId}/cancel`),
      onSuccess: (_, input) => refresh(input.conversationId),
    }),
    prepareAction: useMutation({
      mutationFn: async (id: string) => preparedAgentActionSchema.parse(await api.post<unknown>(`/agent/actions/${id}/prepare`)),
      onSuccess: () => refresh(),
    }),
    applyAction: useMutation({
      mutationFn: async ({ id, confirmToken }: { id: string; confirmToken: string }) =>
        appliedAgentActionSchema.parse(await api.post<unknown>(`/agent/actions/${id}/apply`, { confirmToken })),
      onSuccess: () => refresh(),
    }),
    rejectAction: useMutation({
      mutationFn: (id: string) => api.post(`/agent/actions/${id}/reject`),
      onSuccess: () => refresh(),
    }),
  }
}

/* ---- 运维台 ---- */

export const useTenants = () => useApiQuery<T.List<T.Tenant>>(K.ops('tenants'), '/ops/tenants')
export const useTenantMembers = (id: string | null) => useApiQuery<T.List<T.TenantMember>>(
	K.opsTenantMembers(id ?? ''),
	`/ops/tenants/${id}/members`,
	{ enabled: !!id, retry: false },
)
export const useTemplates = () => useApiQuery<T.List<T.PlatformTemplate>>(K.ops('templates'), '/ops/templates')
export const useTemplateShareRequests = (status = '') => {
	const qs = search({ status })
	return useApiQuery<T.List<T.TemplateShareRequest>>(K.ops('template-share' + qs), '/ops/template-share-requests' + qs)
}
export const useMailConfig = () => useApiQuery<T.MailConfig>(K.ops('mail'), '/ops/mail')
export const useAgentConfig = () => useApiQuery<T.AIConfigState>(K.ops('agent'), '/ops/agent')
export const usePlatformKnowledge = () =>
	useQuery<T.PlatformKnowledgeState, Error, T.PlatformKnowledgeState, readonly unknown[]>({
		queryKey: K.platformKnowledge,
		queryFn: async ({ signal }) => platformKnowledgeStateSchema.parse(await api.get<unknown>('/ops/agent/knowledge', signal)),
		retry: false,
		refetchInterval: (query) => query.state.data?.documents.some((item) => ['uploading', 'queued', 'processing'].includes(item.status)) ? 2000 : false,
	})

export const usePlatformKnowledgeDocument = (id: string | null) =>
	useQuery<T.PlatformKnowledgeDocumentDetail, Error, T.PlatformKnowledgeDocumentDetail, readonly unknown[]>({
		queryKey: K.platformKnowledgeDocument(id ?? ''),
		queryFn: async ({ signal }) => platformKnowledgeDocumentDetailSchema.parse(await api.get<unknown>(`/ops/agent/knowledge/documents/${id}`, signal)),
		enabled: !!id, retry: false,
	})

export async function uploadPlatformKnowledgeDocument(file: File, onProgress?: (percent: number) => void) {
	const mediaType = file.type || evidenceMediaType(file)
	const pre = platformKnowledgePresignSchema.parse(await api.post<unknown>('/ops/agent/knowledge/documents/presign', { filename: file.name, mediaType, sizeBytes: file.size }))
	try {
		await postObject(pre, file, onProgress)
		await api.post(`/ops/agent/knowledge/documents/${pre.documentId}/complete`, { etag: '' })
		return pre.documentId
	} catch (error) {
		try { await api.del(`/ops/agent/knowledge/documents/${pre.documentId}`) } catch { /* maintenance clears abandoned objects */ }
		throw error
	}
}

export function usePlatformKnowledgeActions() {
	const invalidate = useInvalidate()
	const refresh = (id?: string) => invalidate(K.platformKnowledge, ...(id ? [K.platformKnowledgeDocument(id)] : []))
	return {
		update: useMutation({
			mutationFn: ({ id, ...input }: { id: string; displayName?: string; allowedRoles?: T.PlatformKnowledgeRole[]; enabled?: boolean }) => api.put<void>(`/ops/agent/knowledge/documents/${id}`, input),
			onSuccess: (_, input) => refresh(input.id),
		}),
		reprocess: useMutation({ mutationFn: (id: string) => api.post(`/ops/agent/knowledge/documents/${id}/reprocess`), onSuccess: (_, id) => refresh(id) }),
		remove: useMutation({ mutationFn: (id: string) => api.del<void>(`/ops/agent/knowledge/documents/${id}`), onSuccess: () => refresh() }),
	}
}
export const useAIProviders = () => useApiQuery<T.List<T.AIProvider>>(K.ops('agent-providers'), '/ops/agent/providers')
export const useModelRoutes = () => useApiQuery<T.ModelRouteState>(K.ops('agent-routes'), '/ops/agent/routes')
export const useMailLog = (filter: { status?: string; tenantId?: string; from?: string; to?: string; page?: number; page_size?: number } = {}) => {
	const qs = search(filter)
	return useApiQuery<T.Page<T.MailLogRow>>(K.ops('mail-log' + qs), '/ops/mail/log' + qs)
}
export const useQueues = () => useApiQuery<T.QueueState>(K.ops('queues'), '/ops/queues', { refetchInterval: 5000 })
export const useDeadLetters = (before = '') => {
	const qs = search({ limit: 100, before: before || undefined })
	return useApiQuery<T.DeadLetterState>(K.ops('dead-letters' + qs), '/ops/queues/dead' + qs, { refetchInterval: 5000 })
}
export const useWorkers = () => useApiQuery<T.List<T.WorkerState>>(K.ops('workers'), '/ops/workers', { refetchInterval: 5000, retry: false })
export const useCron = () => useApiQuery<T.List<T.CronRow>>(K.ops('cron'), '/ops/cron')
export const useBackups = (filter: { page?: number; page_size?: number; kind?: string; status?: string } = {}) => {
	const qs = search(filter)
	return useApiQuery<T.BackupState>(K.ops('backups' + qs), '/ops/backups' + qs)
}
export const useLifecycle = () => useApiQuery<T.LifecycleState>(K.ops('lifecycle'), '/ops/lifecycle')
export const useBackupRemote = () => useApiQuery<T.BackupRemoteState>(K.ops('backup-remote'), '/ops/backups/remote')
export const useFlags = () => useApiQuery<T.FlagsState>(K.ops('flags'), '/ops/flags')
export const useHealth = () => useApiQuery<T.HealthState>(K.ops('health'), '/ops/health', { refetchInterval: 10000 })
export const useDeploy = () => useApiQuery<T.DeployState>(K.ops('deploy'), '/ops/deploy')
export const useOpsAudit = (filter: { action?: string; resourceType?: string; actor?: string; from?: string; to?: string; page?: number; page_size?: number } = {}) => {
	const qs = search(filter)
	return useApiQuery<T.Page<T.OpsAuditEntry>>(K.ops('audit' + qs), '/ops/audit' + qs)
}

export function useOpsActions() {
  const invalidate = useInvalidate()
  const qc = useQueryClient()
  const after = () => invalidate(['ops'], ['ai'])
  const refreshTenants = () => invalidate(K.ops('tenants'))
  return {
    createTenant: useMutation({
      mutationFn: (v: { name: string; slug?: string; adminSid: string; adminName: string }) => api.post<T.TenantCreated>('/ops/tenants', v),
      onSuccess: refreshTenants,
    }),
    updateTenant: useMutation<unknown, Error, TenantUpdate, { previous?: T.List<T.Tenant> }>({
      mutationFn: ({ id, ...v }) => api.put<unknown>(`/ops/tenants/${id}`, v),
      onMutate: async (input) => {
        await qc.cancelQueries({ queryKey: K.ops('tenants') })
        const previous = qc.getQueryData<T.List<T.Tenant>>(K.ops('tenants'))
        qc.setQueryData<T.List<T.Tenant>>(K.ops('tenants'), (current) => patchTenantList(current, input))
        return { previous }
      },
      onError: (error, _input, context) => {
        // A session change clears the cache; do not restore another account's
        // optimistic snapshot when its now-obsolete request is cancelled.
        if (error instanceof DOMException && error.name === 'AbortError') return
        if (context?.previous) qc.setQueryData(K.ops('tenants'), context.previous)
      },
      onSettled: refreshTenants,
    }),
    appointAdmin: useMutation({
      mutationFn: ({ id, ...v }: { id: string; sid: string; name: string }) => api.put<T.TenantAdmin>(`/ops/tenants/${id}/admin`, v),
      onSuccess: (admin, input) => {
        qc.setQueryData<T.List<T.Tenant>>(K.ops('tenants'), (current) => patchTenantAdmin(current, input.id, admin))
        invalidate(K.opsTenantMembers(input.id), K.ops('tenants'))
      },
    }),
    importTemplate: useMutation({
      mutationFn: (v: { name: string; config: unknown }) =>
        api.post<{ id: string; name: string; active: boolean; created: boolean; restored: boolean; updated: boolean }>('/ops/templates', v),
      onSuccess: after,
    }),
    retireTemplate: useMutation({ mutationFn: (id: string) => api.del<void>(`/ops/templates/${id}`), onSuccess: after }),
    restoreTemplate: useMutation({ mutationFn: (id: string) => api.post<void>(`/ops/templates/${id}/restore`), onSuccess: after }),
	reviewTemplateShare: useMutation({ mutationFn: ({ id, ...v }: { id: string; decision: 'approved' | 'rejected'; reason?: string }) => api.post(`/ops/template-share-requests/${id}/review`, v), onSuccess: after }),
    updateMail: useMutation({ mutationFn: (v: Record<string, unknown>) => api.put<void>('/ops/mail', v), onSuccess: after }),
    testMail: useMutation({ mutationFn: (to: string) => api.post<{ sent: boolean; messageId: string }>('/ops/mail/test', { to }), onSettled: after }),
    reconcileMail: useMutation({
      mutationFn: ({ id, ...input }: { id: string; status: 'sent' | 'failed'; messageId?: string }) => api.post<{ id: string; status: 'sent' | 'failed'; eventId: string | null }>(`/ops/mail/log/${id}/reconcile`, input),
      onSuccess: after,
    }),
    /* 清空库里存的发信通道，回退到部署环境变量。 */
    clearMailChannel: useMutation({ mutationFn: () => api.post<{ cleared: boolean }>('/ops/mail/rotate-key'), onSuccess: after }),
    updateAgent: useMutation({
      mutationFn: (v: {
        baseUrl?: string
        apiKey?: string
        textModel?: string
        visionModel?: string
        agentModel?: string
        agentMaxSteps?: number
        agentTimeoutSeconds?: number
        agentMaxAnswerKb?: number
        agentToolResultKb?: number
        agentToolScanMb?: number
        agentDailyMessages?: number
        materialDailyBatches?: number
        materialActiveBatches?: number
        knowledgeMaxFilesPerClass?: number
        knowledgeMaxStorageMbPerClass?: number
        materialMaxItems?: number
        materialMaxFileMb?: number
        materialMaxBatchMb?: number
        materialMaxPdfPages?: number
        materialConcurrency?: number
        materialRetentionDays?: number
        materialAllowedFormats?: T.AIMaterialFormat[]
		agentAttachmentMaxFileMb?: number
		agentAttachmentMaxMessageMb?: number
		agentAttachmentDailyMb?: number
		agentAttachmentMaxCount?: number
      }) => api.put<void>('/ops/agent', v),
      onSuccess: after,
    }),
    clearAgentKey: useMutation({ mutationFn: () => api.del<void>('/ops/agent/key'), onSuccess: after }),
    resetAgent: useMutation({ mutationFn: () => api.del<void>('/ops/agent'), onSuccess: after }),
    testAgent: useMutation({
      mutationFn: (slot: 'text' | 'vision' | 'agent') => api.post<T.AITestResult>('/ops/agent/test', { slot }),
    }),
    createAIProvider: useMutation({
      mutationFn: (v: {
        name: string
        baseUrl: string
        apiKey: string
        timeoutSeconds: number
        maxRetries: number
        capabilities: T.AIProviderCapabilities
        enabled: boolean
      }) => api.post<{ id: string }>('/ops/agent/providers', v),
      onSuccess: after,
    }),
    updateAIProvider: useMutation({
      mutationFn: ({ id, ...v }: {
        id: string
        name?: string
        baseUrl?: string
        apiKey?: string
        timeoutSeconds?: number
        maxRetries?: number
        capabilities?: T.AIProviderCapabilities
        enabled?: boolean
      }) => api.put<void>(`/ops/agent/providers/${id}`, v),
      onSuccess: after,
    }),
    deleteAIProvider: useMutation({ mutationFn: (id: string) => api.del<void>(`/ops/agent/providers/${id}`), onSuccess: after }),
    clearAIProviderKey: useMutation({ mutationFn: (id: string) => api.del<void>(`/ops/agent/providers/${id}/key`), onSuccess: after }),
    updateModelRoute: useMutation({
      mutationFn: ({ purpose, ...v }: { purpose: T.ModelPurpose; providerId: string; model: string; parameters?: Record<string, unknown> }) =>
        api.put<void>(`/ops/agent/routes/${purpose}`, v),
      onSuccess: after,
    }),
    deleteModelRoute: useMutation({ mutationFn: (purpose: T.ModelPurpose) => api.del<void>(`/ops/agent/routes/${purpose}`), onSuccess: after }),
    testModelRoute: useMutation({
      mutationFn: (purpose: T.ModelPurpose) => api.post<T.ModelRouteTestResult>(`/ops/agent/routes/${purpose}/test`),
    }),
    redeliver: useMutation({
      mutationFn: (ids: string[]) => api.post<{ redelivered: number }>('/ops/queues/dead/redeliver', { ids }),
      onSuccess: after,
    }),
	createBackup: useMutation({ mutationFn: () => api.post<{ id: string; status: string }>('/ops/backups'), onSuccess: after }),
    restoreDrill: useMutation({
      mutationFn: (backupId?: string) => api.post<{ id: string; status: string }>('/ops/backups/restore-drill', { backupId: backupId ?? '' }),
      onSuccess: after,
    }),
	updateLifecycle: useMutation({ mutationFn: (policy: T.LifecyclePolicy) => api.put<T.LifecycleState>('/ops/lifecycle', policy), onSuccess: after }),
	updateBackupRemote: useMutation({ mutationFn: (patch: T.BackupRemoteUpdate) => api.put<void>('/ops/backups/remote', patch), onSuccess: after }),
	clearBackupRemote: useMutation({ mutationFn: () => api.del<void>('/ops/backups/remote'), onSuccess: after }),
	testBackupRemote: useMutation({ mutationFn: () => api.post<T.BackupRemoteTestResult>('/ops/backups/remote/test') }),
	parseBucketUrl: useMutation({ mutationFn: (url: string) => api.post<T.BucketUrlParsed>('/ops/backups/remote/parse', { url }) }),
	reconcileTenantStorage: useMutation({ mutationFn: (id: string) => api.post<{ id: string; storageBytes: number; storageCalibratedAt: string }>(`/ops/tenants/${id}/storage/reconcile`), onSuccess: refreshTenants }),
    setFlag: useMutation({
      mutationFn: (v: { key: string; value: boolean | number | string[] }) => api.put<void>(`/ops/flags/${v.key}`, { value: v.value }),
      onSuccess: after,
    }),
  }
}
