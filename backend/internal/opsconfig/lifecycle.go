package opsconfig

import (
	"errors"
	"time"
)

// ValidateLifecycle enforces the operationally safe editable range. These are
// policy knobs, while hard process and filesystem safety limits stay in code.
func ValidateLifecycle(value Lifecycle) (Lifecycle, error) {
	if _, err := time.Parse("15:04", value.BackupSchedule); err != nil {
		return Lifecycle{}, errors.New("备份时间必须是 HH:MM")
	}
	checks := []struct {
		value, min, max int
		message         string
	}{
		{value.BackupRetentionDays, 1, 3650, "备份保留天数必须在 1—3650 之间"},
		{value.ExportRetentionDays, 1, 365, "导出保留天数必须在 1—365 之间"},
		{value.KnowledgeDeleteGraceHours, 1, 8760, "知识库删除宽限必须在 1—8760 小时之间"},
		{value.AgentAttachmentGraceHours, 1, 720, "Agent 附件宽限必须在 1—720 小时之间"},
		{value.StorageReconcileMinutes, 5, 1440, "存储校准间隔必须在 5—1440 分钟之间"},
		{value.KnowledgeMaxPDFPages, 1, 256, "知识库 PDF 页数必须在 1—256 之间"},
		{value.KnowledgeMaxArchiveMembers, 1, 2000, "压缩包文件数必须在 1—2000 之间"},
		{value.KnowledgeMaxArchiveMB, 1, 512, "压缩包解压上限必须在 1—512 MB 之间"},
		{value.KnowledgeMaxArchiveRatio, 1, 200, "压缩比上限必须在 1—200 之间"},
		{value.KnowledgeMaxArchiveDepth, 1, 32, "压缩包目录深度必须在 1—32 之间"},
		{value.KnowledgeMaxExtractedTextMB, 1, 64, "提取文本上限必须在 1—64 MB 之间"},
		{value.KnowledgeConverterTimeoutSeconds, 5, 300, "转换工具超时必须在 5—300 秒之间"},
	}
	for _, check := range checks {
		if check.value < check.min || check.value > check.max {
			return Lifecycle{}, errors.New(check.message)
		}
	}
	return value, nil
}
