// Package config 把环境变量收敛为一个强类型结构(十二要素,§15.2)。
// 只在进程启动时读取一次;业务代码不直接碰 os.Getenv。
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultJWTSecret      = "dev-only-change-me-32bytes-min!!"
	defaultOpsPassword    = "ops-dev-change-me"
	defaultAIConfigSecret = "easygpa-dev-ai-config-key-000001"
)

type Config struct {
	AppEnv    string // dev | prod
	HTTPAddr  string
	PublicURL string
	ImageTag  string
	GitSHA    string
	// TrustedProxies is the explicit proxy hop allowlist used when resolving
	// X-Forwarded-For. An empty list means forwarded client IP headers are ignored.
	TrustedProxies          []string
	APIRateLimitPerMinute   int
	APIRateLimitBurst       int
	PasswordHashConcurrency int
	// E2ETestToken allows the isolated local E2E stack to bypass distributed
	// rate limits without weakening normal development or production traffic.
	// Production rejects any configured value.
	E2ETestToken   string
	MCPDisabled    bool
	MCPMaxTTLHours int

	DatabaseURL           string
	MigrationsDatabaseURL string
	OpsDatabaseURL        string
	RedisAddr             string
	RedisPassword         string

	S3Endpoint       string
	S3PublicEndpoint string
	S3Region         string
	S3AccessKey      string
	S3SecretKey      string
	S3Bucket         string
	S3UseSSL         bool
	S3PublicUseSSL   bool

	TencentCloudSecretID       string
	TencentCloudSecretKey      string
	TencentCloudSESRegion      string
	TencentCloudSESFrom        string
	TencentCloudSESFromName    string
	TencentCloudSESReplyTo     string
	TencentCloudSESTemplateIDs map[string]uint64
	MailTemplateDir            string
	// 加密运维页保存的腾讯云 SecretId 与 SecretKey。未配置时只能使用
	// 上述部署环境变量，浏览器端永远不会收到凭据明文或密文。
	MailSecretKey string

	BackupDir           string
	BackupOffsiteDir    string
	BackupRetentionDays int
	// 远程备份桶（异地副本）。BackupRemoteSecretKey 加密运维页存进库的桶
	// AccessKey/SecretKey；BackupRemoteRecipient 是 age 公钥，用来加密推上去的
	// 归档。**对应的私钥不放这台机器**——生产被拿下时，攻击者能写新备份但读不了
	// 历史备份。桶地址、桶名这些非机密项在运维页上配，不走环境变量。
	BackupRemoteSecretKey string
	BackupRemoteRecipient string

	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	CookieSecure    bool

	OpsAccount  string
	OpsPassword string

	AIEnabled bool
	// AIAllowPrivateNetwork is a deployment-only SSRF escape hatch for
	// intentionally self-hosted providers. It is never editable from ops UI.
	AIAllowPrivateNetwork bool

	// AI 材料整理助手(§19)。AIConfigSecretKey 只用于加密网页保存的
	// provider key；LLM* 继续作为无运维库时的部署回退。
	AIConfigSecretKey string
	LLMBaseURL        string
	LLMAPIKey         string
	LLMTextModel      string
	LLMVisionModel    string
	LLMAgentModel     string
	LLMTimeout        time.Duration
	AIMaxBatch        int
	AIConcurrency     int
	AIMaxPDFPages     int
}

func Load() (*Config, error) {
	return LoadFor("api")
}

// LoadFor validates only the credentials required by one process role. The
// shared binary must not force every worker to receive every platform secret.
func LoadFor(command string) (*Config, error) {
	c := &Config{
		AppEnv:         getenv("APP_ENV", "dev"),
		HTTPAddr:       getenv("HTTP_ADDR", ":8080"),
		PublicURL:      getenv("PUBLIC_URL", "http://localhost:5173"),
		ImageTag:       getenv("IMAGE_TAG", "dev"),
		GitSHA:         getenv("GIT_SHA", "unknown"),
		TrustedProxies: splitCSV(os.Getenv("TRUSTED_PROXIES")),
		E2ETestToken:   strings.TrimSpace(os.Getenv("E2E_TEST_TOKEN")),
		MCPDisabled:    getenv("MCP_ENABLED", "true") != "true",

		DatabaseURL:           os.Getenv("DATABASE_URL"),
		MigrationsDatabaseURL: os.Getenv("MIGRATIONS_DATABASE_URL"),
		OpsDatabaseURL:        os.Getenv("OPS_DATABASE_URL"),
		RedisAddr:             getenv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:         os.Getenv("REDIS_PASSWORD"),

		S3Endpoint:       getenv("S3_ENDPOINT", "localhost:3900"),
		S3PublicEndpoint: getenv("S3_PUBLIC_ENDPOINT", getenv("S3_ENDPOINT", "localhost:3900")),
		S3Region:         getenv("S3_REGION", "garage"),
		S3AccessKey:      os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:      os.Getenv("S3_SECRET_KEY"),
		S3Bucket:         getenv("S3_BUCKET", "easygpa"),
		S3UseSSL:         os.Getenv("S3_USE_SSL") == "true",
		// 对外签名的 scheme 与内部连接分开。放到公网时,浏览器要走
		// https://<公开域名>/...,而集群内仍然是明文的 garage:3900——
		// 用同一个开关的话,把签名改成 https 就等于把内部连接也改成 TLS,直接连不上。
		S3PublicUseSSL: getenv("S3_PUBLIC_USE_SSL", os.Getenv("S3_USE_SSL")) == "true",

		TencentCloudSecretID:    os.Getenv("TENCENTCLOUD_SECRET_ID"),
		TencentCloudSecretKey:   os.Getenv("TENCENTCLOUD_SECRET_KEY"),
		TencentCloudSESRegion:   getenv("TENCENTCLOUD_SES_REGION", "ap-guangzhou"),
		TencentCloudSESFrom:     os.Getenv("TENCENTCLOUD_SES_FROM"),
		TencentCloudSESFromName: os.Getenv("TENCENTCLOUD_SES_FROM_NAME"),
		TencentCloudSESReplyTo:  os.Getenv("TENCENTCLOUD_SES_REPLY_TO"),
		MailTemplateDir:         os.Getenv("MAIL_TEMPLATE_DIR"),
		MailSecretKey:           os.Getenv("MAIL_SECRET_KEY"),

		BackupDir:             os.Getenv("BACKUP_DIR"),
		BackupOffsiteDir:      os.Getenv("BACKUP_OFFSITE_DIR"),
		BackupRemoteSecretKey: os.Getenv("BACKUP_REMOTE_SECRET_KEY"),
		BackupRemoteRecipient: os.Getenv("BACKUP_REMOTE_RECIPIENT"),

		JWTSecret:    getenv("JWT_SECRET", defaultJWTSecret),
		CookieSecure: os.Getenv("COOKIE_SECURE") == "true",

		OpsAccount:  getenv("OPS_ACCOUNT", "ops@localhost"),
		OpsPassword: getenv("OPS_PASSWORD", defaultOpsPassword),

		AIEnabled:             os.Getenv("AI_ENABLED") == "true",
		AIAllowPrivateNetwork: os.Getenv("AI_ALLOW_PRIVATE_NETWORK") == "true",
		AIConfigSecretKey:     os.Getenv("AI_CONFIG_SECRET_KEY"),

		LLMBaseURL:     getenv("LLM_BASE_URL", ""),
		LLMAPIKey:      os.Getenv("LLM_API_KEY"),
		LLMTextModel:   getenv("LLM_TEXT_MODEL", ""),
		LLMVisionModel: getenv("LLM_VISION_MODEL", ""),
		LLMAgentModel:  os.Getenv("LLM_AGENT_MODEL"),
	}

	var err error
	c.AccessTokenTTL, err = durationEnv("ACCESS_TOKEN_TTL", 15*time.Minute)
	if err != nil {
		return nil, err
	}
	c.RefreshTokenTTL, err = durationEnv("REFRESH_TOKEN_TTL", 30*24*time.Hour)
	if err != nil {
		return nil, err
	}
	c.BackupRetentionDays, err = intEnv("BACKUP_RETENTION_DAYS", 30)
	if err != nil {
		return nil, err
	}
	c.LLMTimeout, err = durationEnv("LLM_TIMEOUT", 90*time.Second)
	if err != nil {
		return nil, err
	}
	c.AIMaxBatch, err = intEnv("AI_MAX_BATCH", 100)
	if err != nil {
		return nil, err
	}
	c.AIConcurrency, err = intEnv("AI_CONCURRENCY", 2)
	if err != nil {
		return nil, err
	}
	c.AIMaxPDFPages, err = intEnv("AI_MAX_PDF_PAGES", 64)
	if err != nil {
		return nil, err
	}
	c.APIRateLimitPerMinute, err = intEnv("API_RATE_LIMIT_PER_MINUTE", 180)
	if err != nil {
		return nil, err
	}
	c.APIRateLimitBurst, err = intEnv("API_RATE_LIMIT_BURST", 60)
	if err != nil {
		return nil, err
	}
	c.PasswordHashConcurrency, err = intEnv("PASSWORD_HASH_CONCURRENCY", 4)
	if err != nil {
		return nil, err
	}
	c.MCPMaxTTLHours, err = intEnv("MCP_MAX_TTL_HOURS", 24)
	if err != nil {
		return nil, err
	}
	if c.MCPMaxTTLHours < 1 || c.MCPMaxTTLHours > 168 {
		return nil, fmt.Errorf("MCP_MAX_TTL_HOURS 必须在 1—168 之间")
	}
	c.TencentCloudSESRegion = strings.ToLower(strings.TrimSpace(c.TencentCloudSESRegion))
	c.TencentCloudSESTemplateIDs, err = templateIDsEnv("TENCENTCLOUD_SES_TEMPLATE_IDS")
	if err != nil {
		return nil, err
	}

	if c.AppEnv != "dev" && c.AppEnv != "prod" {
		return nil, fmt.Errorf("APP_ENV 只能是 dev 或 prod")
	}
	if c.AppEnv == "prod" && c.E2ETestToken != "" {
		return nil, fmt.Errorf("prod 环境禁止设置 E2E_TEST_TOKEN")
	}
	if c.AppEnv == "dev" && strings.TrimSpace(c.AIConfigSecretKey) == "" {
		c.AIConfigSecretKey = defaultAIConfigSecret
	}

	// 生产环境缺关键配置直接拒绝启动，但只校验当前进程真正使用的凭据。
	if c.AppEnv == "prod" {
		needsDatabase := command == "api" || strings.HasPrefix(command, "worker:")
		needsOpsDatabase := command == "api" || command == "worker:notify" || command == "worker:export" || command == "worker:maintenance" || command == "worker:backup" || command == "worker:ai" || command == "worker:agent"
		needsAIKey := command == "api" || command == "worker:ai" || command == "worker:agent"
		needsBackup := command == "api" || command == "worker:backup"
		if needsDatabase && c.DatabaseURL == "" {
			return nil, fmt.Errorf("prod 环境必须设置 DATABASE_URL")
		}
		if needsOpsDatabase && c.OpsDatabaseURL == "" {
			return nil, fmt.Errorf("prod 环境必须设置 OPS_DATABASE_URL")
		}
		if command == "api" {
			if len(c.JWTSecret) < 32 || c.JWTSecret == defaultJWTSecret {
				return nil, fmt.Errorf("prod 环境必须设置非默认且至少 32 字节的 JWT_SECRET")
			}
			if c.OpsPassword == "" || c.OpsPassword == defaultOpsPassword {
				return nil, fmt.Errorf("prod 环境必须设置非默认 OPS_PASSWORD")
			}
			if !c.CookieSecure {
				return nil, fmt.Errorf("prod 环境必须设置 COOKIE_SECURE=true")
			}
		}
		if needsAIKey && strings.TrimSpace(c.AIConfigSecretKey) == "" {
			return nil, fmt.Errorf("prod 环境必须设置 AI_CONFIG_SECRET_KEY，供运维页加密模型 API Key")
		}
		if needsBackup && (c.BackupDir == "" || c.BackupOffsiteDir == "" || c.BackupDir == c.BackupOffsiteDir) {
			return nil, fmt.Errorf("prod 环境必须设置不同的 BACKUP_DIR 与 BACKUP_OFFSITE_DIR")
		}
		if (command == "migrate" || command == "migrate:down" || command == "worker:backup") && c.MigrationsDatabaseURL == "" {
			return nil, fmt.Errorf("prod 环境必须设置 MIGRATIONS_DATABASE_URL")
		}
	}
	if c.AccessTokenTTL <= 0 || c.RefreshTokenTTL <= c.AccessTokenTTL {
		return nil, fmt.Errorf("REFRESH_TOKEN_TTL 必须大于 ACCESS_TOKEN_TTL，且二者都为正数")
	}
	if c.BackupRetentionDays < 1 || c.BackupRetentionDays > 3650 {
		return nil, fmt.Errorf("BACKUP_RETENTION_DAYS 必须在 1—3650 之间")
	}
	// 只检查形状，真正的解析在 backupjob。填错了要在启动时就炸，而不是等到
	// 凌晨两点半那一次远程推送才发现——那时候没人在看日志。
	if recipient := strings.TrimSpace(c.BackupRemoteRecipient); recipient != "" {
		if strings.HasPrefix(recipient, "AGE-SECRET-KEY-") {
			return nil, fmt.Errorf("BACKUP_REMOTE_RECIPIENT 填成了 age 私钥。这里只能放公钥（age1 开头），私钥必须离线保管，不能出现在生产机上")
		}
		if !strings.HasPrefix(recipient, "age1") {
			return nil, fmt.Errorf("BACKUP_REMOTE_RECIPIENT 必须是 age 公钥（age1 开头）")
		}
	}
	if c.LLMTimeout < 5*time.Second || c.LLMTimeout > 10*time.Minute {
		return nil, fmt.Errorf("LLM_TIMEOUT 必须在 5 秒到 10 分钟之间")
	}
	if c.AIMaxBatch < 1 || c.AIMaxBatch > 100 {
		return nil, fmt.Errorf("AI_MAX_BATCH 必须在 1—100 之间")
	}
	if c.AIConcurrency < 1 || c.AIConcurrency > 8 {
		return nil, fmt.Errorf("AI_CONCURRENCY 必须在 1—8 之间")
	}
	if c.AIMaxPDFPages < 1 || c.AIMaxPDFPages > 64 {
		return nil, fmt.Errorf("AI_MAX_PDF_PAGES 必须在 1—64 之间")
	}
	if c.APIRateLimitPerMinute < 10 || c.APIRateLimitPerMinute > 100000 {
		return nil, fmt.Errorf("API_RATE_LIMIT_PER_MINUTE 必须在 10—100000 之间")
	}
	if c.APIRateLimitBurst < 1 || c.APIRateLimitBurst > c.APIRateLimitPerMinute {
		return nil, fmt.Errorf("API_RATE_LIMIT_BURST 必须在 1 与 API_RATE_LIMIT_PER_MINUTE 之间")
	}
	if c.PasswordHashConcurrency < 1 || c.PasswordHashConcurrency > 16 {
		return nil, fmt.Errorf("PASSWORD_HASH_CONCURRENCY 必须在 1—16 之间")
	}
	for _, proxy := range c.TrustedProxies {
		if net.ParseIP(proxy) == nil {
			if _, _, err := net.ParseCIDR(proxy); err != nil {
				return nil, fmt.Errorf("TRUSTED_PROXIES 包含无效 IP 或 CIDR %q", proxy)
			}
		}
	}
	switch c.TencentCloudSESRegion {
	case "ap-guangzhou", "ap-hongkong":
	default:
		return nil, fmt.Errorf("TENCENTCLOUD_SES_REGION 只能是 ap-guangzhou 或 ap-hongkong")
	}
	return c, nil
}

func (c *Config) AllowPrivateAINetwork() bool {
	// Local model endpoints are an explicit deployment opt-in too. Treating
	// APP_ENV=dev as an implicit exception makes a mislabelled public instance
	// an SSRF pivot even when AI_ALLOW_PRIVATE_NETWORK=false.
	return c != nil && c.AIAllowPrivateNetwork
}

func splitCSV(raw string) []string {
	values := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func durationEnv(key string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 不是合法时长: %w", key, err)
	}
	return d, nil
}

func intEnv(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 不是合法整数: %w", key, err)
	}
	return value, nil
}

func templateIDsEnv(key string) (map[string]uint64, error) {
	result := make(map[string]uint64)
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("%s 必须是模板名到正整数 ID 的 JSON 对象: %w", key, err)
	}
	for name, id := range result {
		if strings.TrimSpace(name) == "" || id == 0 {
			return nil, fmt.Errorf("%s 中的模板名不能为空且 ID 必须是正整数", key)
		}
	}
	return result, nil
}
