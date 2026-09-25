package api

import (
	"strings"
	"testing"

	"easygpa/backend/internal/opsconfig"
)

func completeTemplateIDInput() map[string]any {
	result := make(map[string]any, len(opsconfig.RequiredMailTemplates))
	for index, name := range opsconfig.RequiredMailTemplates {
		result[name] = float64(1000 + index)
	}
	return result
}

func TestNormalizeMailSESEncryptsCredentialsAndValidatesTemplates(t *testing.T) {
	cipher, err := opsconfig.CipherFromKey(strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"sesRegion": "AP-GUANGZHOU", "sesFrom": "notice@gpa.example.org", "sesFromName": "EasyGPA Plus",
		"sesSecretId": "secret-id", "sesSecretKey": "secret-key", "sesTemplateIds": completeTemplateIDInput(),
	}
	if err := normalizeMailSES(input, cipher); err != nil {
		t.Fatal(err)
	}
	if input["sesRegion"] != "ap-guangzhou" || !opsconfig.IsSealed(input["sesSecretId"].(string)) || !opsconfig.IsSealed(input["sesSecretKey"].(string)) {
		t.Fatalf("normalized input = %#v", input)
	}
}

func TestNormalizeMailSESRejectsIncompleteTemplateMap(t *testing.T) {
	input := map[string]any{"sesTemplateIds": map[string]any{"mail_test": float64(123)}}
	if err := normalizeMailSES(input, nil); err == nil || !strings.Contains(err.Error(), "缺少腾讯云模板 ID") {
		t.Fatalf("normalizeMailSES() error = %v", err)
	}
}

func TestNormalizeMailSESRejectsVariableStyleFromAddress(t *testing.T) {
	input := map[string]any{"sesFrom": "EasyGPA Plus <notice@gpa.example.org>"}
	if err := normalizeMailSES(input, nil); err == nil || !strings.Contains(err.Error(), "纯邮箱地址") {
		t.Fatalf("normalizeMailSES() error = %v", err)
	}
}

func TestRedactOpsMailConfigKeepsDatabaseSource(t *testing.T) {
	templateIDs := make(map[string]uint64, len(opsconfig.RequiredMailTemplates))
	for index, name := range opsconfig.RequiredMailTemplates {
		templateIDs[name] = uint64(index + 1)
	}
	config := opsconfig.Mail{
		Provider:       "tencent_ses",
		SESRegion:      "ap-hongkong",
		SESSecretID:    "enc:v1:id",
		SESSecretKey:   "enc:v1:key",
		SESFrom:        "noreply@example.org",
		SESTemplateIDs: templateIDs,
	}

	redacted, source, secretIDSet, secretKeySet := redactOpsMailConfig(config)
	if source != "database" || !secretIDSet || !secretKeySet {
		t.Fatalf("source=%q secretIDSet=%v secretKeySet=%v", source, secretIDSet, secretKeySet)
	}
	if redacted.SESSecretID != "" || redacted.SESSecretKey != "" {
		t.Fatal("redacted config returned SES credentials")
	}
}
