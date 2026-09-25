package notify

import (
	"context"
	"strings"
	"testing"

	"easygpa/backend/internal/opsconfig"
)

type stubMailSettings struct {
	mail opsconfig.Mail
	err  error
}

func (s stubMailSettings) Mail(context.Context) (opsconfig.Mail, error) { return s.mail, s.err }

func TestSESConfigFromSettingsDecryptsBothCredentials(t *testing.T) {
	cipher, err := opsconfig.CipherFromKey(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	secretID, _ := cipher.Seal("stored-id")
	secretKey, _ := cipher.Seal("stored-key")
	fallback := completeSESConfig()
	settings := opsconfig.Mail{
		Provider: "tencent_ses", SESRegion: "ap-hongkong", SESSecretID: secretID, SESSecretKey: secretKey,
		SESFrom: "database@gpa.example.org", SESFromName: "数据库通道", SESTemplateIDs: fallback.TemplateIDs,
	}
	cfg, err := SESConfigFromSettings(settings, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecretID != "stored-id" || cfg.SecretKey != "stored-key" || cfg.Region != "ap-hongkong" {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestRuntimeSESFallsBackWhenDatabaseHasNoChannel(t *testing.T) {
	fallback := completeSESConfig()
	mailer, err := NewRuntimeSESMailer(stubMailSettings{mail: opsconfig.DefaultMail()}, nil, fallback)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mailer.effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecretID != fallback.SecretID || cfg.From != fallback.From {
		t.Fatalf("fallback config = %#v", cfg)
	}
}

func TestRuntimeSESFallsBackDuringSettingsOutage(t *testing.T) {
	fallback := completeSESConfig()
	mailer, err := NewRuntimeSESMailer(stubMailSettings{err: context.DeadlineExceeded}, nil, fallback)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mailer.effective(context.Background())
	if err != nil || cfg.SecretKey != fallback.SecretKey {
		t.Fatalf("effective() = %#v, %v", cfg, err)
	}
}

func TestRuntimeSESRejectsPartialDatabaseConfigInsteadOfMixingSources(t *testing.T) {
	fallback := completeSESConfig()
	settings := opsconfig.DefaultMail()
	settings.SESFrom = "partial@gpa.example.org"
	mailer, err := NewRuntimeSESMailer(stubMailSettings{mail: settings}, nil, fallback)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mailer.effective(context.Background())
	if err == nil || !strings.Contains(err.Error(), "配置不完整") {
		t.Fatalf("effective() error = %v", err)
	}
}

func TestSESConfigFromSettingsRejectsPlaintextCredential(t *testing.T) {
	settings := opsconfig.DefaultMail()
	settings.SESSecretID = "plain-text"
	if _, err := SESConfigFromSettings(settings, nil); err == nil || !strings.Contains(err.Error(), "不是加密格式") {
		t.Fatalf("SESConfigFromSettings() error = %v", err)
	}
}

func TestRuntimeSESConstructorRejectsPartialEnvironmentConfig(t *testing.T) {
	_, err := NewRuntimeSESMailer(nil, nil, SESConfig{Region: "ap-guangzhou", SecretID: "only-id"})
	if err == nil {
		t.Fatal("partial environment config was accepted")
	}
}
