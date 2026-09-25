// Keep translation keys separate from server prose. New locales can replace
// this catalog without changing request handling or matching English messages.
const messages: Record<string, string> = {
  network_error: '网络连接中断，请检查网络后重试',
  request_timeout: '请求超时，请稍后重试',
  request_cancelled: '操作已取消',
  invalid_response: '服务返回的内容异常，请刷新页面后重试',
  invalid_request: '提交内容格式不正确，请检查后重试',
  invalid_credentials: '账号或密码错误，请检查后重试',
  unauthenticated: '登录已过期，请重新登录',
  forbidden: '当前账号无权执行此操作',
  not_found: '要查看的内容不存在或已被删除，请返回列表刷新',
  conflict: '内容或状态已发生变化，请刷新页面后重试',
  rate_limit_exceeded: '操作过于频繁，请稍等片刻后重试',
  internal_error: '服务暂时无法完成此操作，请稍后重试',
  submission_invalid: '申报内容未通过校验，请检查所选小项、数量、档位和期望分数',
  scheme_invalid: '评分方案未通过校验，请检查各项名称、权重和分值范围',
  template_invalid: '评分模板未通过校验，请检查各项名称、权重和分值范围',
  invalid_config: '方案格式不正确，请检查配置内容后重试',
  invalid_window: '时间线设置不正确，请检查开放、封存和封锁时间的先后顺序',
  evidence_required: '请先上传该小项所需的佐证，等待上传完成后再提交',
  evidence_invalid: '佐证文件不符合要求，请检查文件格式和大小',
  content_mismatch: '文件内容与所选格式不一致，请重新选择原始文件上传',
  upload_failed: '文件上传失败，请稍后重试',
  upload_expired: '本次上传已过期，请重新上传文件',
  file_too_large: '文件超过允许的大小，请压缩或拆分后再上传',
  dispatch_invalid: '审核人设置不正确，请检查是否存在重复、停用或不可用的成员',
  claim_quantity_required: '请填写已完成的数量',
  claim_quantity_invalid: '数量须为大于或等于 0 的有效数字',
  claim_option_invalid: '请选择当前方案中的档位；不确定时可先保存草稿',
  claim_score_required: '请填写期望分数',
  claim_score_invalid: '请填写有效的期望分数',
  claim_rule_invalid: '当前小项的计分规则不可用，请联系班级管理员检查方案',
}

// Compatibility with older API releases. Structured reason keys take priority.
const legacyMessages: Record<string, string> = {
  'score is required': messages.claim_score_required,
  'quantity is required': messages.claim_quantity_required,
  'quantity must be a finite non-negative number': messages.claim_quantity_invalid,
  'score must be finite': messages.claim_score_invalid,
  'enum score must be finite': messages.claim_score_invalid,
  'refresh token is invalid or has been rotated': messages.unauthenticated,
  'reviewer pool contains an invalid or duplicate user ID': messages.dispatch_invalid,
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

function translatedReason(detail: unknown): string | undefined {
  const data = record(detail)
  if (typeof data?.reason !== 'string') return undefined
  if (Object.hasOwn(messages, data.reason)) return messages[data.reason]
  const params = record(data.params)
  const min = typeof params?.min === 'number' && Number.isFinite(params.min) ? params.min : undefined
  const max = typeof params?.max === 'number' && Number.isFinite(params.max) ? params.max : undefined
  if (data.reason === 'claim_score_below_minimum' && min !== undefined) return `期望分数不能低于 ${min} 分`
  if (data.reason === 'claim_score_above_maximum' && max !== undefined) return `期望分数不能超过 ${max} 分`
  if (data.reason === 'claim_score_out_of_range' && min !== undefined && max !== undefined) return `期望分数须在 ${min} 至 ${max} 分之间`
  return undefined
}

/** Only display useful user-facing prose; JSON/parser, browser and provider
 * diagnostics must not become a toast, even if they quote some Chinese input. */
export function readableErrorMessage(message: unknown, fallback: string): string {
  if (typeof message !== 'string' || !message.trim()) return fallback
  const text = message.trim()
  if (Object.hasOwn(legacyMessages, text)) return legacyMessages[text]
  if (/failed to fetch|networkerror|network request failed|load failed|network connection.*lost/i.test(text)) return messages.network_error
  if (/^(?:Unexpected\b|Expected\b|SyntaxError\b|TypeError\b|Error:|JSON\b.*(?:parse|position)|invalid character\b)|<\/?(?:html|head|body)\b/i.test(text)) return fallback
  return /\p{Script=Han}/u.test(text) ? text : fallback
}

export function apiErrorMessage(status: number, code: string, message: unknown, detail?: unknown): string {
  const reason = translatedReason(detail)
  if (reason) return reason
  const fallback = Object.hasOwn(messages, code) ? messages[code] : statusMessage(status)
  return readableErrorMessage(message, fallback)
}

function statusMessage(status: number): string {
  if (status === 0) return messages.network_error
  if (status === 401) return messages.unauthenticated
  if (status === 403) return messages.forbidden
  if (status === 404) return messages.not_found
  if (status === 408 || status === 504) return messages.request_timeout
  if (status === 409) return messages.conflict
  if (status === 413) return messages.file_too_large
  if (status === 429) return messages.rate_limit_exceeded
  if (status >= 500) return messages.internal_error
  if (status === 400 || status === 422) return '提交内容未通过校验，请检查填写的信息后重试'
  return '操作未能完成，请刷新页面后重试'
}

export function userErrorMessage(error: unknown, fallback = '操作失败，请稍后重试'): string {
  if (error instanceof Error) {
    if (error.name === 'AbortError') return messages.request_cancelled
    if (error.name === 'TimeoutError') return messages.request_timeout
    return readableErrorMessage(error.message, fallback)
  }
  return fallback
}
