package scheme

import "strings"

// DefaultEvidenceMaxMB is large enough for scanned multi-page PDFs while
// keeping a bounded platform limit. Evidence is uploaded directly to object
// storage, so this does not increase the API request-body limit.
const DefaultEvidenceMaxMB int64 = 50

// commonEvidenceTypes is the platform's safe, non-executable evidence set.
// Active web/script formats (html, svg, js, etc.) stay excluded: evidence is
// downloaded by reviewers and never needs to execute in a browser.
var commonEvidenceTypes = []string{
	"pdf",
	"jpg", "jpeg", "png", "gif", "webp", "heic",
	"doc", "docx", "xls", "xlsx", "ppt", "pptx",
	"txt", "csv",
	"wps", "et", "dps",
	"zip", "rar", "7z",
	"mp4",
}

// CommonEvidenceTypes returns a copy so callers cannot mutate the package
// default while constructing a scheme item.
func CommonEvidenceTypes() []string {
	return append([]string(nil), commonEvidenceTypes...)
}

// EffectiveEvidenceTypes is the single policy boundary used by validation and
// UI hints. An explicit scheme allow-list is always strict; only a missing rule
// falls back to the platform defaults.
func EffectiveEvidenceTypes(rule *EvidenceRule) []string {
	if rule == nil {
		return CommonEvidenceTypes()
	}
	return normalizeEvidenceTypes(rule.Types)
}

func normalizeEvidenceTypes(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
