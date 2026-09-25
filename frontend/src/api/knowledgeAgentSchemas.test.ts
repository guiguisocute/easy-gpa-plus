import assert from 'node:assert/strict'
import test from 'node:test'
import { agentCitationSchema, knowledgeStateSchema, platformKnowledgeStateSchema } from './knowledgeAgentSchemas.ts'

test('班管文件汇总同时解析班级资料和学生佐证', () => {
  const now = new Date().toISOString()
  const parsed = knowledgeStateSchema.parse({
    policy: { externalProcessingApproved: false, approvedBy: null, approvedAt: null, revokedAt: null },
    limits: { fileMb: 50, filesPerClass: 500, storageMbPerClass: 1024, pdfPages: 64 },
    stats: { totalFiles: 1, classFiles: 0, evidenceFiles: 1, readyFiles: 0, processingFiles: 0, storageBytes: 1024, updatedAt: now },
    documents: [],
    evidence: [{
      id: '9', filename: '获奖证书.pdf', mediaType: 'application/pdf', sizeBytes: 1024, status: 'ready', kind: 'claim',
      source: 'submission', ownerId: '3', title: '竞赛获奖', subjectSid: '2024000002', subjectName: '周二',
      uploaderSid: '2024000002', uploaderName: '周二', createdAt: now,
    }],
    platform: { knowledgeEnabled: false, knowledgeEgressEnabled: false, aiEnabled: false },
  })
  assert.equal(parsed.stats.totalFiles, 1)
  assert.equal(parsed.evidence[0].subjectName, '周二')
})

test('平台知识列表解析来源、角色、状态和配额', () => {
  const parsed = platformKnowledgeStateSchema.parse({
    limits: { fileMb: 20, files: 1000, storageMb: 1024, pdfPages: 64 },
    stats: { totalFiles: 1, readyFiles: 1, processingFiles: 0, storageBytes: 4096 },
    documents: [{
      id: '1', sourceKind: 'builtin', builtinKey: 'student-submit', filename: 'student-submit.md', displayName: '学生提交材料指南',
      allowedRoles: ['student'], views: ['stuSubmit'], keywords: ['在哪里交材料'], sortOrder: 30,
      status: 'ready', enabled: true, searchable: true, extractor: 'builtin/markdown', entries: 1,
      pageCount: null, sheetCount: null, warning: '', error: '', contentVersion: 'abc123', contentHash: 'a'.repeat(64),
      mediaType: 'text/markdown', sizeBytes: 0, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(), publishedAt: null,
    }],
  })
  assert.equal(parsed.documents[0].allowedRoles[0], 'student')
  assert.equal(parsed.documents[0].sourceKind, 'builtin')
})

test('旧引用缺少扩展字段时使用兼容默认值', () => {
  const parsed = agentCitationSchema.parse({
    documentId: '7', entryId: '8', filename: '细则.pdf', logicalPath: '细则.pdf', locator: {}, excerpt: '规则片段',
  })
  assert.equal(parsed.scope, 'class')
  assert.equal(parsed.sourceKind, 'class')
  assert.equal(parsed.downloadable, true)
  assert.equal(parsed.audienceRole, '')
})
