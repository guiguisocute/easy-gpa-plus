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

func TestRuntimeSESDoesNotSwitchProviderDuringSettingsOutage(t *testing.T) {
	fallback := completeSESConfig()
	mailer, err := NewRuntimeSESMailer(stubMailSettings{err: context.DeadlineExceeded}, nil, fallback)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mailer.effective(context.Background())
	if err == nil || cfg.SecretKey != "" {
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

func TestDeliverySnapshotPreservesProviderAcrossHotReload(t *testing.T) {
	cipher, err := opsconfig.CipherFromKey(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	key, err := cipher.Seal("fixture-resend-key")
	if err != nil {
		t.Fatal(err)
	}
	source := &queueTestSettings{mail: opsconfig.DefaultMail()}
	source.mail.Provider, source.mail.SESFrom, source.mail.ResendAPIKey = "resend", "sender@example.org", key
	runtime, err := NewRuntimeSESMailer(source, cipher, SESConfig{})
	if err != nil {
		t.Fatal(err)
	}
	before, provider, err := snapshotDelivery(t.Context(), NewSuppressionMailer(runtime, nil))
	if err != nil || provider != "resend" {
		t.Fatalf("snapshot provider=%q err=%v", provider, err)
	}
	first := before.(*SuppressionMailer).next
	if _, ok := first.(*ResendMailer); !ok {
		t.Fatal("snapshot lost Resend sender")
	}
	// Editing the shared runtime after reservation must affect future sends,
	// while the already reserved attempt keeps its original provider identity.
	source.mail.Provider, source.mail.SMTPHost = "smtp", "smtp.example.org"
	after, provider, err := snapshotDelivery(t.Context(), runtime)
	if err != nil || provider != "smtp" {
		t.Fatalf("reloaded provider=%q err=%v", provider, err)
	}
	if _, ok := after.(*SMTPMailer); !ok || before.(*SuppressionMailer).next != first {
		t.Fatal("hot reload changed the reserved sender")
	}
	if err := runtime.SyncFeedback(t.Context(), nil); err != nil {
		t.Fatalf("non-Tencent channel attempted to access Tencent feedback: %v", err)
	}
}
