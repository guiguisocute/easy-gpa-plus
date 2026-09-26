import type { MailConfig } from '@/api/types'
import { db, persist } from './db'
import { fail } from './errors'

const secretFields = {
  sesSecretId: 'sesSecretIdSet', sesSecretKey: 'sesSecretKeySet',
  smtpUsername: 'smtpUsernameSet', smtpPassword: 'smtpPasswordSet',
  aliyunAccessKeyId: 'aliyunAccessKeyIdSet', aliyunAccessKeySecret: 'aliyunAccessKeySecretSet',
  resendApiKey: 'resendApiKeySet',
} as const

const settingFields = new Set([
  'provider', 'perMinute', 'perDay', 'quietStart', 'quietEnd', 'notificationsEnabled',
  'sesRegion', 'sesFrom', 'sesFromName', 'sesReplyTo', 'sesNotificationFrom', 'sesNotificationFromName',
  'sesTemplateIds', 'smtpHost', 'smtpPort', 'smtpSecurity', 'aliyunRegion',
])

function defaults(): MailConfig {
  return {
    config: {
      provider: 'tencent_ses', perMinute: 20, perDay: 400, quietStart: '22:00', quietEnd: '07:00',
      notificationsEnabled: false, sesRegion: 'ap-hongkong', sesFrom: 'accounts@example.org',
      sesFromName: '综测演示平台', sesNotificationFrom: 'notify@example.org', sesNotificationFromName: '班级通知',
      sesTemplateIds: { verification_code: 1001, password_reset: 1002, mail_test: 1003, notification_alert: 1004, notification_digest: 1005 },
      smtpPort: 587, smtpSecurity: 'starttls', aliyunRegion: 'cn-hangzhou',
    },
    activeProvider: 'tencent_ses', secretSource: 'database', secretKeyReady: true,
    sesSecretIdSet: true, sesSecretKeySet: true, smtpUsernameSet: false, smtpPasswordSet: false,
    aliyunAccessKeyIdSet: false, aliyunAccessKeySecretSet: false, resendApiKeySet: false,
    notificationStats: { suppressedRecipients: 2, pendingBatches: 0, failedBatches: 0, lastFeedbackAt: null },
    restartRequired: false,
  }
}

export function mockMailConfig(): MailConfig {
  const value = db().mail ??= defaults()
  const c = value.config
  const provider = c.provider ?? 'tencent_ses'
  const configured = !!c.sesFrom && (provider === 'smtp'
    ? !!c.smtpHost && !!c.smtpPort && !!value.smtpUsernameSet === !!value.smtpPasswordSet
    : provider === 'aliyun_dm' ? !!value.aliyunAccessKeyIdSet && !!value.aliyunAccessKeySecretSet
      : provider === 'resend' ? !!value.resendApiKeySet
      : !!value.sesSecretIdSet && !!value.sesSecretKeySet && ['mail_test', 'verification_code', 'password_reset'].every(k => !!c.sesTemplateIds?.[k]))
  return structuredClone({ ...value, activeProvider: provider, configured, feedbackSupported: provider === 'tencent_ses' })
}

export function updateMockMail(input: Record<string, unknown>) {
  const next = mockMailConfig()
  const destinationChanged = (input.smtpHost !== undefined && input.smtpHost !== next.config.smtpHost)
    || (input.smtpPort !== undefined && input.smtpPort !== next.config.smtpPort)
  if (destinationChanged && (next.smtpUsernameSet || next.smtpPasswordSet) && (input.smtpUsername === undefined || input.smtpPassword === undefined)) {
    fail(422, 'mail_config_invalid', '更换 SMTP 主机或端口时，需要重新填写或清空已有用户名和密码')
  }
  for (const [key, value] of Object.entries(input)) {
    if (key in secretFields) {
      if (typeof value !== 'string') fail(422, 'mail_config_invalid', '凭据必须是文本')
      // Demo stores only readiness flags. Never retain secrets in memory or localStorage.
      next[secretFields[key as keyof typeof secretFields]] = !!(value as string).trim()
    } else if (settingFields.has(key)) {
      Object.assign(next.config, { [key]: value })
    } else fail(422, 'mail_config_invalid', '邮件配置包含不允许的字段')
  }
  if (!['tencent_ses', 'smtp', 'aliyun_dm', 'resend'].includes(next.config.provider ?? '')) fail(422, 'mail_config_invalid', '请选择支持的邮件通道')
  next.secretSource = 'database'
  db().mail = next
  persist()
}

export function resetMockMail() {
  const next = defaults()
  next.secretSource = 'environment'
  next.sesSecretIdSet = false
  next.sesSecretKeySet = false
  next.config.sesFrom = ''
  next.config.sesNotificationFrom = ''
  next.config.sesTemplateIds = {}
  db().mail = next
  persist()
  return { cleared: true }
}
