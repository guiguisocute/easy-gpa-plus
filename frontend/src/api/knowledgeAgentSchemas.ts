import { z } from 'zod'

const nullableString = z.string().nullable()
const record = z.record(z.string(), z.unknown())

export const knowledgeDocumentStatusSchema = z.enum([
  'uploading', 'queued', 'processing', 'ready', 'partial', 'unsupported', 'failed', 'superseded', 'deleted',
])

export const knowledgeDocumentSchema = z.object({
  id: z.string(),
  filename: z.string(),
  logicalPath: z.string(),
  displayName: z.string(),
  mediaType: z.string(),
  sizeBytes: z.number().nonnegative(),
  status: knowledgeDocumentStatusSchema,
  searchable: z.boolean(),
  extractor: nullableString,
  entries: z.number().int().nonnegative(),
  pageCount: z.number().int().nonnegative().nullable(),
  sheetCount: z.number().int().nonnegative().nullable(),
  warning: nullableString,
  error: nullableString,
  createdAt: z.string(),
  updatedAt: z.string(),
  publishedAt: nullableString,
})

export const knowledgeEntrySchema = z.object({
  id: z.string(),
  documentId: z.string().optional(),
  filename: z.string().optional(),
  logicalPath: z.string().optional(),
  kind: z.string().optional(),
  locator: record,
  metadata: record.optional(),
  lineCount: z.number().int().nonnegative().optional(),
  charCount: z.number().int().nonnegative().optional(),
  startLine: z.number().int().positive(),
  endLine: z.number().int().nonnegative(),
  text: z.string(),
})

export const knowledgeEvidenceSchema = z.object({
  id: z.string(),
  filename: z.string(),
  mediaType: z.string(),
  sizeBytes: z.number().nonnegative(),
  status: z.enum(['pending', 'ready']),
  kind: z.enum(['claim', 'note']),
  source: z.enum(['submission', 'appeal', 'objection']),
  ownerId: z.string(),
  title: z.string(),
  subjectSid: z.string(),
  subjectName: z.string(),
  uploaderSid: z.string(),
  uploaderName: z.string(),
  createdAt: z.string(),
})

export const knowledgeStateSchema = z.object({
  policy: z.object({
    externalProcessingApproved: z.boolean(),
    approvedBy: nullableString,
    approvedAt: nullableString,
    revokedAt: nullableString,
  }),
  limits: z.object({
    fileMb: z.number().int().positive(),
    filesPerClass: z.number().int().positive(),
    storageMbPerClass: z.number().int().positive(),
    pdfPages: z.number().int().positive(),
  }),
  stats: z.object({
    totalFiles: z.number().int().nonnegative(),
    classFiles: z.number().int().nonnegative(),
    evidenceFiles: z.number().int().nonnegative(),
    readyFiles: z.number().int().nonnegative(),
    processingFiles: z.number().int().nonnegative(),
    storageBytes: z.number().nonnegative(),
    updatedAt: nullableString,
  }),
  documents: z.array(knowledgeDocumentSchema),
  evidence: z.array(knowledgeEvidenceSchema),
  platform: z.object({ knowledgeEnabled: z.boolean(), knowledgeEgressEnabled: z.boolean(), aiEnabled: z.boolean() }),
})

export const knowledgeDocumentDetailSchema = knowledgeDocumentSchema.extend({
  entriesPreview: z.array(knowledgeEntrySchema),
  downloadUrl: z.string(),
  downloadExpiresIn: z.number().int().positive(),
})

export const knowledgePresignSchema = z.object({
  documentId: z.string(),
  uploadUrl: z.string().url(),
  uploadFields: z.record(z.string(), z.string()),
  expiresIn: z.number().int().positive(),
  limits: knowledgeStateSchema.shape.limits,
})

export const platformKnowledgeRoleSchema = z.enum(['student', 'group', 'class_admin'])
export const platformKnowledgeDocumentSchema = z.object({
  id: z.string(), sourceKind: z.enum(['builtin', 'custom']), builtinKey: z.string(), filename: z.string(), displayName: z.string(),
  allowedRoles: z.array(platformKnowledgeRoleSchema).min(1), views: z.array(z.string()), keywords: z.array(z.string()), sortOrder: z.number().int(),
  status: z.enum(['uploading', 'queued', 'processing', 'ready', 'partial', 'unsupported', 'failed', 'superseded', 'retired', 'deleted']),
  enabled: z.boolean(), searchable: z.boolean(), extractor: z.string(), entries: z.number().int().nonnegative(),
  pageCount: z.number().int().nonnegative().nullable(), sheetCount: z.number().int().nonnegative().nullable(), warning: z.string(), error: z.string(),
  contentVersion: z.string(), contentHash: z.string(), mediaType: z.string(), sizeBytes: z.number().nonnegative(),
  createdAt: z.string(), updatedAt: z.string(), publishedAt: nullableString,
})

export const platformKnowledgeStateSchema = z.object({
  limits: z.object({ fileMb: z.number().int().positive(), files: z.number().int().positive(), storageMb: z.number().int().positive(), pdfPages: z.number().int().positive() }),
  stats: z.object({ totalFiles: z.number().int().nonnegative(), readyFiles: z.number().int().nonnegative(), processingFiles: z.number().int().nonnegative(), storageBytes: z.number().nonnegative() }),
  documents: z.array(platformKnowledgeDocumentSchema),
})

export const platformKnowledgeDocumentDetailSchema = platformKnowledgeDocumentSchema.extend({
  entriesPreview: z.array(knowledgeEntrySchema), downloadUrl: z.string(), downloadExpiresIn: z.number().int().nonnegative(), content: z.string().optional(),
})

export const platformKnowledgePresignSchema = z.object({
  documentId: z.string(), uploadUrl: z.string().url(), uploadFields: z.record(z.string(), z.string()), expiresIn: z.number().int().positive(), limits: platformKnowledgeStateSchema.shape.limits,
})

const locatorSchema = record
export const agentCitationSchema = z.object({
  documentId: z.string(),
  entryId: z.string(),
  filename: z.string(),
  logicalPath: z.string(),
  locator: locatorSchema,
  excerpt: z.string(),
	 scope: z.enum(['class', 'platform', 'scheme', 'business']).default('class'),
	 sourceKind: z.string().default('class'),
	 downloadable: z.boolean().default(true),
	 audienceRole: z.union([platformKnowledgeRoleSchema, z.literal('')]).default(''),
})

export const agentAttachmentSchema = z.object({
  id: z.string(),
  filename: z.string(),
  mediaType: z.enum(['image/jpeg', 'image/png', 'image/webp']),
  sizeBytes: z.number().int().positive(),
})

export const agentAttachmentLinkSchema = agentAttachmentSchema.omit({ id: true }).extend({
  attachmentId: z.string(),
  downloadUrl: z.string().url(),
  expiresIn: z.number().int().positive(),
})

export const agentAttachmentPresignSchema = z.object({
  attachmentId: z.string(),
  uploadUrl: z.string().url(),
  uploadFields: z.record(z.string(), z.string()),
  expiresIn: z.number().int().positive(),
})

/* running 是新的中间态：Worker 在工具真正执行之前就落了这一行，
   thought 是配套的一句进度说明，两者一起构成实时回显的思维链与工具链路。 */
export const agentToolTraceSchema = z.object({
  seq: z.number().int().positive(),
  tool: z.enum(['find_files', 'grep', 'read_text', 'inspect_table', 'file_stats', 'current_scheme', 'search_product_help', 'read_current_context', 'read_evidence']),
  status: z.enum(['running', 'complete', 'failed', 'canceled']),
  thought: z.string().default(''),
  summary: z.string(),
  durationMs: z.number().int().nonnegative(),
})

export const agentPageContextSchema = z.object({
  view: z.string(), resourceKind: z.enum(['submission', 'appeal']).optional(), resourceId: z.string().optional(),
  resourceLabel: z.string().optional(), revision: z.string().optional(), evidenceId: z.string().optional(),
  draft: z.object({ reason: z.string(), score: z.string(), category: z.string(), itemKey: z.string() }).optional(),
})

export const agentPageStateSchema = z.object({
  context: agentPageContextSchema, evidenceCount: z.number().int().nonnegative(), canDraft: z.boolean(),
  evidence: z.array(z.object({ id: z.string(), filename: z.string(), mediaType: z.string(), status: z.string(), sizeBytes: z.number(), sha256: z.string() })),
})

export const agentActionSchema = z.object({
  context: agentPageContextSchema.nullable().optional(),
  id: z.string(),
  kind: z.enum(['submission_draft', 'review_draft', 'scheme_draft', 'export_draft', 'arbitration_reason_draft']),
  status: z.enum(['proposed', 'prepared', 'applied', 'rejected', 'expired', 'stale']),
  title: z.string(),
  summary: z.string(),
  diff: z.array(z.object({ field: z.string(), before: z.unknown().optional(), after: z.unknown().optional() })),
  citations: z.array(agentCitationSchema),
  expiresAt: z.string(),
  targetView: z.string(),
  createdAt: z.string(),
})

export const preparedAgentActionSchema = agentActionSchema.extend({
  confirmToken: z.string().min(1),
  confirmExpiresAt: z.string(),
  risk: z.string(),
})

export const appliedAgentActionSchema = z.object({
  id: z.string(),
  kind: z.enum(['submission_draft', 'review_draft', 'scheme_draft', 'export_draft', 'arbitration_reason_draft']),
  status: z.literal('applied'),
  navigateTo: z.string(),
  resourceId: z.string(),
  draft: z.object({ id: z.string(), status: z.literal('draft'), source: z.literal('ai').optional(), name: z.string().optional() }).optional(),
  prefill: record.optional(),
})

export const agentMessageSchema = z.object({
  context: agentPageContextSchema.optional(),
  id: z.string(),
  role: z.enum(['user', 'assistant']),
  status: z.enum(['queued', 'running', 'complete', 'failed', 'canceled']),
  content: z.string(),
  attachments: z.array(agentAttachmentSchema).default([]),
  citations: z.array(agentCitationSchema),
  toolTrace: z.array(agentToolTraceSchema),
  finalThought: z.string().default(''),
  actions: z.array(agentActionSchema),
  sourceRevoked: z.boolean(),
  error: nullableString,
  createdAt: z.string(),
  finishedAt: nullableString,
})

export const agentConversationSchema = z.object({
  id: z.string(),
  title: z.string(),
  messages: z.array(agentMessageSchema),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export const agentConversationsSchema = z.object({
  items: z.array(z.object({
    id: z.string(), title: z.string(), preview: z.string(), createdAt: z.string(), updatedAt: z.string(),
  })),
})

export const agentStatusSchema = z.object({
  enabled: z.boolean(),
  reason: z.enum(['', 'agent_disabled', 'knowledge_disabled', 'knowledge_consent_required', 'quota_exceeded']),
  actionsEnabled: z.boolean(),
  externalProcessingApproved: z.boolean(),
  quota: z.object({ used: z.number().int().nonnegative(), limit: z.number().int().positive() }),
	attachmentQuota: z.object({ maxFileMb: z.number().int().positive(), maxMessageMb: z.number().int().positive(), dailyMb: z.number().int().positive(), dailyUsedBytes: z.number().int().nonnegative(), maxCount: z.number().int().positive() }),
  model: z.object({ configured: z.boolean(), visionConfigured: z.boolean().optional(), inherited: z.boolean() }),
})

export const agentSourceSchema = z.object({
  documentId: z.string(), entryId: z.string(), filename: z.string(), logicalPath: z.string(), locator: record,
  /* 当前已发布方案不是上传的文件，没有原件可下，downloadUrl 为空串。 */
  downloadUrl: z.string(), expiresIn: z.number().int().nonnegative(), content: z.string().default(''), mediaType: z.string().default('application/octet-stream'),
})
