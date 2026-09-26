package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"easygpa/backend/internal/config"
	"easygpa/backend/internal/opsconfig"
)

func TestDeploymentConfigurationExcludesCredentials(t *testing.T) {
	const secret = "test-only-secret-never-in-response"
	server := &Server{cfg: &config.Config{
		PublicURL: "https://gpa.example.org", TrustedProxies: []string{"127.0.0.1"},
		AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 30 * 24 * time.Hour,
		CookieSecure: true, MCPMaxTTLHours: 24,
		S3Endpoint: "storage.example.org", S3PublicEndpoint: "files.example.org", S3Bucket: "example",
		DatabaseURL: secret, MigrationsDatabaseURL: secret, OpsDatabaseURL: secret,
		RedisPassword: secret, S3AccessKey: secret, S3SecretKey: secret, JWTSecret: secret,
		OpsPassword: secret, LLMAPIKey: secret, TencentCloudSecretID: secret, TencentCloudSecretKey: secret,
		MailSecretKey: secret, AIConfigSecretKey: secret, BackupRemoteSecretKey: secret,
	}}
	data, err := json.Marshal(server.deploymentConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("deployment summary exposed a credential")
	}
	var result struct {
		PublicURL string `json:"publicUrl"`
		Auth      struct {
			Access int64 `json:"accessTokenTtlSeconds"`
		} `json:"auth"`
		Secrets struct {
			Mail bool `json:"mailReady"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.PublicURL != server.cfg.PublicURL || result.Auth.Access != 900 {
		t.Fatal("startup settings missing from summary")
	}
	if result.Secrets.Mail {
		t.Fatal("readiness must use initialized cipher, not merely a nonempty environment value")
	}
	cipher, err := opsconfig.NewCipher(strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	server.deps.MailCipher = cipher
	data, _ = json.Marshal(server.deploymentConfiguration())
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Secrets.Mail {
		t.Fatal("initialized mail cipher must be shown as ready")
	}
}
