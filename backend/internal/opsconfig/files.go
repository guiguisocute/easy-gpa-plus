package opsconfig

import (
	"errors"
	"fmt"
	"strings"
)

var evidenceAllowedFormatOrder = []string{
	"pdf", "jpg", "jpeg", "png", "gif", "webp", "heic",
	"doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt", "csv",
	"wps", "et", "dps", "zip", "rar", "7z", "mp4",
}

func DefaultEvidenceAllowedFormats() []string {
	return append([]string(nil), evidenceAllowedFormatOrder...)
}

func NormalizeEvidenceAllowedFormats(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("平台允许的佐证格式至少保留一种")
	}
	known := make(map[string]bool, len(evidenceAllowedFormatOrder))
	for _, item := range evidenceAllowedFormatOrder {
		known[item] = true
	}
	selected := make(map[string]bool, len(values))
	for _, raw := range values {
		value := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if !known[value] {
			return nil, fmt.Errorf("不支持的平台佐证格式：%s", raw)
		}
		selected[value] = true
	}
	result := make([]string, 0, len(selected))
	for _, value := range evidenceAllowedFormatOrder {
		if selected[value] {
			result = append(result, value)
		}
	}
	return result, nil
}
