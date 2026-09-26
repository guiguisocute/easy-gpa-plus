/** A blank credential input preserves the stored value. Only an explicit clear
 * may send an empty string; hidden credentials from other providers never leak
 * into the active provider's update. */
const CONNECTION_FIELDS: Record<string, readonly string[]> = {
  smtp: ['smtpHost', 'smtpPort', 'smtpSecurity'],
  tencent_ses: ['sesRegion'],
  aliyun_dm: ['aliyunRegion'],
  resend: [],
}
const SECRET_FIELDS: Record<string, readonly string[]> = {
  smtp: ['smtpUsername', 'smtpPassword'],
  tencent_ses: ['sesSecretId', 'sesSecretKey'],
  aliyun_dm: ['aliyunAccessKeyId', 'aliyunAccessKeySecret'],
  resend: ['resendApiKey'],
}
const SENDER_FIELDS = ['sesFrom', 'sesFromName', 'sesReplyTo', 'sesNotificationFrom', 'sesNotificationFromName']

export function mailChannelUpdate(provider: string, edits: Record<string, string>, cleared: readonly string[], current: Record<string, unknown>): Record<string, unknown> {
  if (!Object.hasOwn(CONNECTION_FIELDS, provider)) throw new Error('请选择支持的发信服务')
  const payload: Record<string, unknown> = { provider }
  for (const key of [...SENDER_FIELDS, ...CONNECTION_FIELDS[provider]]) {
    if (edits[key] !== undefined) payload[key] = edits[key].trim()
  }
  if (provider === 'smtp') {
    const rawPort = edits.smtpPort ?? current.smtpPort ?? 587
    const port = Number(rawPort)
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('SMTP 端口必须为 1—65535 的整数')
    payload.smtpPort = port
    const security = edits.smtpSecurity ?? current.smtpSecurity ?? 'starttls'
    if (security !== 'starttls' && security !== 'tls') throw new Error('请选择 STARTTLS 或 TLS 加密连接')
    payload.smtpSecurity = security
  }
  for (const key of SECRET_FIELDS[provider]) {
    if (cleared.includes(key)) payload[key] = ''
    else if (edits[key]) {
      // SMTP passwords may legitimately contain leading/trailing whitespace.
      const value = key === 'smtpPassword' ? edits[key] : edits[key].trim()
      if (value) payload[key] = value
    }
  }
  return payload
}
