import type { EvidenceRule } from './types'

/** 平台允许的常用、不可执行佐证格式。HTML/SVG/脚本等主动内容不在这里。 */
export const COMMON_EVIDENCE_TYPES = [
  'pdf',
  'jpg', 'jpeg', 'png', 'gif', 'webp', 'heic',
  'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx',
  'txt', 'csv',
  'wps', 'et', 'dps',
  'zip', 'rar', '7z',
  'mp4',
] as const

export const DEFAULT_EVIDENCE_MAX_MB = 50

const mediaTypeByExtension: Record<string, string> = {
  pdf: 'application/pdf',
  jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif', webp: 'image/webp', heic: 'image/heic',
  doc: 'application/msword',
  docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  xls: 'application/vnd.ms-excel',
  xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  ppt: 'application/vnd.ms-powerpoint',
  pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  txt: 'text/plain', csv: 'text/csv',
  wps: 'application/vnd.ms-works', et: 'application/octet-stream', dps: 'application/octet-stream',
  zip: 'application/zip', rar: 'application/vnd.rar', '7z': 'application/x-7z-compressed',
  mp4: 'video/mp4',
}

export function fileExtension(filename: string) {
  const index = filename.lastIndexOf('.')
  return index >= 0 ? filename.slice(index + 1).trim().toLowerCase() : ''
}

function normalizedTypes(types: readonly string[]) {
  return [...new Set(types.map((type) => type.trim().toLowerCase().replace(/^\./, '')).filter(Boolean))]
}

/** 显式白名单始终严格执行；只有没有配置佐证规则时才使用平台常用格式。 */
export function effectiveEvidenceTypes(types: readonly string[] | undefined) {
  if (!types) return [...COMMON_EVIDENCE_TYPES]
  return normalizedTypes(types)
}

export function evidenceAccept(types: readonly string[] | undefined) {
  return effectiveEvidenceTypes(types).map((type) => `.${type}`).join(',')
}

export function evidenceMediaType(file: Pick<File, 'name' | 'type'>) {
  return file.type.trim().toLowerCase() || mediaTypeByExtension[fileExtension(file.name)] || 'application/octet-stream'
}

export function validateEvidenceFile(file: Pick<File, 'name' | 'size'>, rule: EvidenceRule | undefined): string | null {
  if (file.size <= 0) return `${file.name} 是空文件，不能上传`
  const extension = fileExtension(file.name)
  if (!extension || !effectiveEvidenceTypes(rule?.types).includes(extension)) {
    return extension ? `${file.name} 的 .${extension} 格式不受支持` : `${file.name} 没有文件扩展名`
  }
  const maxMb = rule?.maxMb ?? DEFAULT_EVIDENCE_MAX_MB
  if (file.size > maxMb * 1024 * 1024) {
    return `${file.name} 超过单份 ${maxMb} MB 的限制；请压缩或拆分文件，仍无法处理时联系管理员提高方案上限`
  }
  return null
}

export type EvidenceKind = 'image' | 'pdf' | 'doc' | 'sheet' | 'slide' | 'text' | 'archive' | 'file'

/** 图标与"能不能出缩略图"都看它。以 media_type 为准，缺失时退回扩展名——
    老记录里 media_type 可能是 application/octet-stream。 */
export function evidenceKind(mediaType: string | undefined, filename: string): EvidenceKind {
  const type = (mediaType ?? '').trim().toLowerCase()
  if (type.startsWith('image/') && type !== 'image/svg+xml') return 'image'
  if (type === 'application/pdf') return 'pdf'
  switch (fileExtension(filename)) {
    case 'jpg': case 'jpeg': case 'png': case 'gif': case 'webp': case 'heic': return 'image'
    case 'pdf': return 'pdf'
    case 'doc': case 'docx': case 'wps': return 'doc'
    case 'xls': case 'xlsx': case 'csv': case 'et': return 'sheet'
    case 'ppt': case 'pptx': case 'dps': return 'slide'
    case 'txt': return 'text'
    case 'zip': case 'rar': case '7z': return 'archive'
    default: return 'file'
  }
}

/** HEIC 浏览器普遍不解码，别给它挂缩略图占位——只会显示一个碎图标。 */
export function canThumbnail(kind: EvidenceKind, filename: string) {
  return kind === 'image' && fileExtension(filename) !== 'heic'
}

export function evidenceTypesLabel(types: readonly string[] | undefined) {
  const effective = effectiveEvidenceTypes(types)
  if (effective.length === COMMON_EVIDENCE_TYPES.length && COMMON_EVIDENCE_TYPES.every((type) => effective.includes(type))) {
    return 'PDF / 常用图片 / Word / Excel / PPT / 文本 / WPS / 压缩包 / MP4 视频'
  }
  return effective.map((type) => `.${type}`).join(' / ')
}
