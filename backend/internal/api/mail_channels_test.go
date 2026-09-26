package api

import (
	"encoding/json"
	"strings"
	"testing"

	"easygpa/backend/internal/config"
	"easygpa/backend/internal/opsconfig"
)

func mailChannelTestCipher(t *testing.T) *opsconfig.Cipher {
	t.Helper()
	cipher, err := opsconfig.NewCipher(strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestMailChannelsEncryptEveryCredentialAndPreserveOmittedFields(t *testing.T) {
	cipher := mailChannelTestCipher(t)
	for _, field := range []string{"sesSecretId", "sesSecretKey", "smtpUsername", "smtpPassword", "aliyunAccessKeyId", "aliyunAccessKeySecret", "resendApiKey"} {
		t.Run(field, func(t *testing.T) {
			plain := "example-only-" + field
			input := map[string]any{field: plain}
			if err := normalizeMailChannels(input, cipher); err != nil {
				t.Fatal(err)
			}
			sealed, ok := input[field].(string)
			if !ok || !opsconfig.IsSealed(sealed) || sealed == plain {
				t.Fatal("credential was not encrypted before persistence")
			}
			opened, err := cipher.Open(sealed)
			if err != nil || opened != plain {
				t.Fatalf("encrypted credential did not round-trip: %v", err)
			}
			if len(input) != 1 {
				t.Fatal("normalization populated omitted fields and would overwrite other saved credentials")
			}
			if err := normalizeMailChannels(map[string]any{field: plain}, nil); err == nil {
				t.Fatal("saving a credential without MAIL_SECRET_KEY must fail")
			}
			clear := map[string]any{field: ""}
			if err := normalizeMailChannels(clear, nil); err != nil || clear[field] != "" {
				t.Fatalf("explicit credential deletion must work without an encryption key: %v", err)
			}
		})
	}
}

func TestMailChannelsRejectInvalidTransportSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input map[string]any
	}{
		{"provider number", map[string]any{"provider": float64(1)}},
		{"unknown provider", map[string]any{"provider": "other"}},
		{"empty provider", map[string]any{"provider": ""}},
		{"cleartext SMTP", map[string]any{"smtpSecurity": "none"}},
		{"SMTP port string", map[string]any{"smtpPort": "587"}},
		{"SMTP port zero", map[string]any{"smtpPort": float64(0)}},
		{"SMTP port overflow", map[string]any{"smtpPort": float64(65536)}},
		{"SMTP port fraction", map[string]any{"smtpPort": 587.5}},
		{"SMTP URL", map[string]any{"smtpHost": "https://smtp.example.org"}},
		{"SMTP embedded port", map[string]any{"smtpHost": "smtp.example.org:587"}},
		{"SMTP credentials in host", map[string]any{"smtpHost": "user@smtp.example.org"}},
		{"SMTP host wrong type", map[string]any{"smtpHost": true}},
		{"Aliyun unsupported region", map[string]any{"aliyunRegion": "ap-southeast-2"}},
		{"Aliyun sender display length", map[string]any{"provider": "aliyun_dm", "sesFromName": strings.Repeat("班", 16)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := normalizeMailChannels(tc.input, nil); err == nil {
				t.Fatal("invalid transport setting was accepted")
			}
		})
	}
	for _, provider := range []string{"tencent_ses", "smtp", "aliyun_dm", "resend"} {
		input := map[string]any{"provider": " " + strings.ToUpper(provider) + " "}
		if err := normalizeMailChannels(input, nil); err != nil || input["provider"] != provider {
			t.Fatalf("valid provider %s was not normalized: %v", provider, err)
		}
	}
	for _, security := range []string{"starttls", "tls"} {
		input := map[string]any{"smtpHost": "SMTP.EXAMPLE.ORG", "smtpPort": float64(587), "smtpSecurity": security}
		if err := normalizeMailChannels(input, nil); err != nil || input["smtpHost"] != "smtp.example.org" {
			t.Fatalf("valid SMTP settings rejected: %v", err)
		}
	}
	for _, region := range []string{"cn-hangzhou", "ap-southeast-1", "us-east-1", "eu-central-1"} {
		if err := normalizeMailChannels(map[string]any{"aliyunRegion": region}, nil); err != nil {
			t.Fatalf("supported Aliyun region %s rejected: %v", region, err)
		}
	}
}

func TestMailChannelsRedactAllSecretsButKeepReadiness(t *testing.T) {
	cipher := mailChannelTestCipher(t)
	sealed, err := cipher.Seal("example-only-secret-canary")
	if err != nil {
		t.Fatal(err)
	}
	settings := opsconfig.Mail{
		Provider: "smtp", SMTPHost: "smtp.example.org", SMTPPort: 587, SMTPSecurity: "starttls", SESFrom: "sender@example.org",
		SESSecretID: sealed, SESSecretKey: sealed, SMTPUsername: sealed, SMTPPassword: sealed,
		AliyunAccessKeyID: sealed, AliyunAccessKeySecret: sealed, ResendAPIKey: sealed,
	}
	server := &Server{deps: Dependencies{MailCipher: cipher}}
	status := server.mailChannelStatus(settings)
	redacted, source, idSet, keySet := redactOpsMailConfig(settings)
	if source != "database" || !idSet || !keySet || status["configured"] != true {
		t.Fatal("redaction lost the configured source or readiness")
	}
	for _, field := range []string{"smtpUsernameSet", "smtpPasswordSet", "aliyunAccessKeyIdSet", "aliyunAccessKeySecretSet", "resendApiKeySet"} {
		if status[field] != true {
			t.Fatalf("missing presence flag %s", field)
		}
	}
	status["config"] = redacted
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "enc:v1:") || strings.Contains(string(encoded), "example-only-secret-canary") {
		t.Fatal("GET configuration would expose plaintext or encrypted credentials")
	}
	if redacted.SMTPHost != settings.SMTPHost || settings.SMTPPassword != sealed {
		t.Fatal("redaction must preserve public settings and leave the stored configuration intact")
	}
}

func TestSMTPDestinationChangeRequiresBothCredentials(t *testing.T) {
	current := opsconfig.Mail{SMTPHost: "smtp.example.org", SMTPPort: 587, SMTPUsername: "stored-user", SMTPPassword: "stored-password"}
	for _, tc := range []struct {
		name string
		host string
		port int
	}{
		{"host", "other.example.org", 587},
		{"port", "smtp.example.org", 465},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := current
			next.SMTPHost, next.SMTPPort = tc.host, tc.port
			for _, input := range []map[string]any{{}, {"smtpUsername": "new-user"}, {"smtpPassword": "new-password"}} {
				if err := validateSMTPDestinationChange(current, next, input); err == nil {
					t.Fatal("changed destination reused a saved credential without explicit consent")
				}
			}
			for _, input := range []map[string]any{{"smtpUsername": "new-user", "smtpPassword": "new-password"}, {"smtpUsername": "", "smtpPassword": ""}} {
				if err := validateSMTPDestinationChange(current, next, input); err != nil {
					t.Fatalf("explicitly replacing or clearing both credentials must be accepted: %v", err)
				}
			}
		})
	}
	if err := validateSMTPDestinationChange(current, current, map[string]any{}); err != nil {
		t.Fatalf("unrelated settings must preserve credentials: %v", err)
	}
	relay := current
	relay.SMTPUsername, relay.SMTPPassword = "", ""
	if err := validateSMTPDestinationChange(relay, opsconfig.Mail{SMTPHost: "relay.example.org", SMTPPort: 587}, map[string]any{}); err != nil {
		t.Fatalf("TLS relay without saved credentials has no credentials to disclose: %v", err)
	}
}

func TestMailChannelStatusUsesActualEnvironmentAndKeepsProvidersIsolated(t *testing.T) {
	templates := map[string]uint64{}
	for index, name := range opsconfig.RequiredMailTemplates {
		templates[name] = uint64(index + 1)
	}
	server := &Server{cfg: &config.Config{
		TencentCloudSESRegion: "ap-guangzhou", TencentCloudSecretID: "example-env-id", TencentCloudSecretKey: "example-env-key",
		TencentCloudSESFrom: "sender@example.org", TencentCloudSESTemplateIDs: templates,
	}}
	for _, provider := range []string{"", "tencent_ses"} {
		status := server.mailChannelStatus(opsconfig.Mail{Provider: provider})
		if status["configured"] != true || status["feedbackSupported"] != true || status["restartRequired"] != false {
			t.Fatalf("complete environment fallback was not ready: %#v", status)
		}
	}
	for _, provider := range []string{"smtp", "aliyun_dm", "resend"} {
		status := server.mailChannelStatus(opsconfig.Mail{Provider: provider})
		if status["configured"] != false || status["feedbackSupported"] != false || status["configurationError"] == nil {
			t.Fatalf("incomplete %s borrowed Tencent credentials or feedback: %#v", provider, status)
		}
	}
	partial := server.mailChannelStatus(opsconfig.Mail{Provider: "tencent_ses", SESFrom: "custom@example.org"})
	if partial["configured"] != false {
		t.Fatal("partial database channel must not combine itself with environment credentials")
	}
	server.cfg.TencentCloudSecretKey = ""
	if server.mailChannelStatus(opsconfig.Mail{Provider: "tencent_ses"})["configured"] != false {
		t.Fatal("incomplete environment fallback must not be reported as ready")
	}
}
