// Keep the demo's editable surface aligned with opsconfig.DefaultFlags.
export const DEFAULT_OPS_FLAGS: Record<string, boolean | number | string[]> = {
  maintenance: false, registration: true, aiEnabled: false, knowledgeEnabled: false,
  agentActionsEnabled: false, knowledgeEgressEnabled: false, nativeToolsEnabled: true,
  uploadMaxMb: 50, requestConcurrency: 64, exportConcurrency: 1, passwordHashConcurrency: 4,
  apiRateLimitPerMinute: 180, apiRateLimitBurst: 60,
  authLoginPerMinute: 120, authRefreshPerMinute: 60, authRegisterPerHour: 120,
  authForgotPerHour: 5, authResetPerHour: 10,
  evidencePresignPerHour: 300, evidenceDailyMb: 1024, aiPresignPerHour: 600,
  agentPresignPerHour: 120, knowledgePresignPerHour: 120, aiBatchActionsPerHour: 10,
  agentMessagesPerMinute: 20, exportRequestsPerHour: 10, knowledgeReprocessPerHour: 30,
  evidenceAllowedFormats: ['pdf', 'jpg', 'jpeg', 'png', 'gif', 'webp', 'heic', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'txt', 'csv', 'wps', 'et', 'dps', 'zip', 'rar', '7z', 'mp4'],
}
