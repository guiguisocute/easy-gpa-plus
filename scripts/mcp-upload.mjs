#!/usr/bin/env node
import { createReadStream } from 'node:fs'
import { stat } from 'node:fs/promises'
import { basename, extname } from 'node:path'
import { createHash, randomUUID } from 'node:crypto'
import { connectionOptions, callMCP } from './mcp-http.mjs'

const [submission, file, requestKey = randomUUID()] = process.argv.slice(2)
try {
  if (!/^[1-9][0-9]*$/.test(submission ?? '') || !file || process.argv.length > 5) throw new Error('用法：node scripts/mcp-upload.mjs <草稿 ID> <本地文件> [请求 UUID]；连接参数通过环境变量提供。')
  const connection = connectionOptions()
  const info = await stat(file)
  if (!info.isFile() || info.size < 1 || info.size > 64 * 1024 * 1024) throw new Error('请选择 1 字节至 64 MB 的普通文件。')
  const types = { pdf: 'application/pdf', png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', webp: 'image/webp', gif: 'image/gif', txt: 'text/plain', doc: 'application/msword', docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', xls: 'application/vnd.ms-excel', xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', ppt: 'application/vnd.ms-powerpoint', pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation', heic: 'image/heic' }
  const mediaType = types[extname(file).slice(1).toLowerCase()]
  if (!mediaType) throw new Error('此助手不支持该文件类型。')
  const hash = createHash('sha256')
  for await (const chunk of createReadStream(file)) hash.update(chunk)
  const prepared = await callMCP(connection, 'evidence.prepare_upload', { id: submission, idempotencyKey: `${requestKey}:prepare`, input: { filename: basename(file), mediaType, sizeBytes: info.size, sha256: hash.digest('hex') } })
  const upload = new URL(prepared.uploadUrl)
  const base = new URL(connection.endpoint)
  if (upload.origin !== base.origin || !upload.pathname.startsWith(`${base.pathname}/uploads/`) || upload.username || upload.password || upload.search || upload.hash) throw new Error('上传地址与已配置的 MCP 服务不一致；请检查部署 PUBLIC_URL。')
  const response = await fetch(upload, { method: 'PUT', headers: { Authorization: `Bearer ${connection.token}`, 'Content-Type': mediaType, 'Content-Length': String(info.size) }, body: createReadStream(file), duplex: 'half', redirect: 'error', signal: AbortSignal.timeout(60_000) })
  if (!response.ok) throw new Error(`文件传输失败，HTTP ${response.status}。`)
  const completed = await callMCP(connection, 'evidence.complete_upload', { upload: prepared.uploadId, idempotencyKey: `${requestKey}:complete` })
  console.log(JSON.stringify({ ok: true, evidenceId: prepared.evidenceId, result: completed }))
} catch (error) { console.error(error.message); process.exitCode = 1 }
