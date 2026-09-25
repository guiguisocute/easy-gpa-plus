package config

import (
	"strings"
	"testing"
)

func setValidProductionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DATABASE_URL", "postgres://app@example/easygpa")
	t.Setenv("OPS_DATABASE_URL", "postgres://ops@example/easygpa")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("OPS_PASSWORD", "a-production-only-password")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("BACKUP_DIR", "/var/lib/easygpa-backup-local")
	t.Setenv("BACKUP_OFFSITE_DIR", "/mnt/offsite/easygpa")
	t.Setenv("AI_CONFIG_SECRET_KEY", strings.Repeat("a", 32))
}

func TestLoadRejectsDevelopmentJWTSecretInProduction(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("JWT_SECRET", defaultJWTSecret)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("Load() error = %v, want JWT_SECRET error", err)
	}
}

func TestLoadRequiresSecureRefreshCookieInProduction(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("COOKIE_SECURE", "false")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("Load() error = %v, want COOKIE_SECURE error", err)
	}
}

func TestLoadRejectsUnknownEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("Load() error = %v, want APP_ENV error", err)
	}
}

func TestLoadRejectsE2ETestTokenInProduction(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("E2E_TEST_TOKEN", "must-never-be-enabled-in-production")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "E2E_TEST_TOKEN") {
		t.Fatalf("Load() error = %v, want E2E_TEST_TOKEN error", err)
	}
}

func TestLoadRequiresIndependentProductionBackupTargets(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("BACKUP_OFFSITE_DIR", "/var/lib/easygpa-backup-local")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "BACKUP_OFFSITE_DIR") {
		t.Fatalf("Load() error = %v, want backup directory error", err)
	}
}

func TestLoadRejectsUnknownTencentSESRegion(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("TENCENTCLOUD_SES_REGION", "ap-mars")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TENCENTCLOUD_SES_REGION") {
		t.Fatalf("Load() error = %v, want TENCENTCLOUD_SES_REGION error", err)
	}
}

func TestLoadParsesTencentSESTemplateIDs(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("TENCENTCLOUD_SES_TEMPLATE_IDS", `{"mail_test":123,"password_reset":456}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TencentCloudSESTemplateIDs["mail_test"] != 123 || cfg.TencentCloudSESTemplateIDs["password_reset"] != 456 {
		t.Fatalf("template IDs = %#v", cfg.TencentCloudSESTemplateIDs)
	}
}

func TestLoadRejectsInvalidTencentSESTemplateIDs(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("TENCENTCLOUD_SES_TEMPLATE_IDS", `{"mail_test":0}`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TENCENTCLOUD_SES_TEMPLATE_IDS") {
		t.Fatalf("Load() error = %v, want template ID error", err)
	}
}

func TestLoadProvidesStableDevelopmentAIConfigCipherKey(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("AI_CONFIG_SECRET_KEY", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AIConfigSecretKey != defaultAIConfigSecret || len(cfg.AIConfigSecretKey) != 32 {
		t.Fatalf("development AI config key is not the expected 32-byte fallback")
	}
}

func TestLoadRejectsInvalidTrustedProxy(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1/32,not-a-network")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Fatalf("Load() error = %v, want TRUSTED_PROXIES error", err)
	}
}

func TestLoadRejectsRateLimitBurstAboveRate(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("API_RATE_LIMIT_PER_MINUTE", "100")
	t.Setenv("API_RATE_LIMIT_BURST", "101")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "API_RATE_LIMIT_BURST") {
		t.Fatalf("Load() error = %v, want API_RATE_LIMIT_BURST error", err)
	}
}

func TestAllowPrivateAINetworkRequiresExplicitOptIn(t *testing.T) {
	dev := &Config{AppEnv: "dev"}
	if dev.AllowPrivateAINetwork() {
		t.Fatal("development mode must not implicitly allow private AI endpoints")
	}
	dev.AIAllowPrivateNetwork = true
	if !dev.AllowPrivateAINetwork() {
		t.Fatal("explicit private AI network opt-in was ignored")
	}
	prod := &Config{AppEnv: "prod"}
	if prod.AllowPrivateAINetwork() {
		t.Fatal("production must keep private AI endpoints blocked by default")
	}
}

func TestLoadForDispatchDoesNotRequireUnrelatedProductionSecrets(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DATABASE_URL", "postgres://app@example/easygpa")
	t.Setenv("OPS_DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("OPS_PASSWORD", "")
	t.Setenv("AI_CONFIG_SECRET_KEY", "")
	t.Setenv("BACKUP_DIR", "")
	t.Setenv("BACKUP_OFFSITE_DIR", "")
	if _, err := LoadFor("worker:dispatch"); err != nil {
		t.Fatalf("dispatch rejected least-privilege environment: %v", err)
	}
}

func TestLoadForBackupRequiresMigrationCredential(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DATABASE_URL", "postgres://app@example/easygpa")
	t.Setenv("OPS_DATABASE_URL", "postgres://ops@example/easygpa")
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("BACKUP_DIR", "/var/lib/easygpa-backup-local")
	t.Setenv("BACKUP_OFFSITE_DIR", "/mnt/offsite/easygpa")
	if _, err := LoadFor("worker:backup"); err == nil || !strings.Contains(err.Error(), "MIGRATIONS_DATABASE_URL") {
		t.Fatalf("LoadFor() error = %v, want migration credential error", err)
	}
}
