// Package api wires HTTP transport to explicit domain services and stores.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/auth"
	"easygpa/backend/internal/config"
	"easygpa/backend/internal/notify"
	"easygpa/backend/internal/objectstore"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/platformknowledge"
	"easygpa/backend/internal/ratelimit"
	"easygpa/backend/internal/store"
)

type Dependencies struct {
	GovernanceContext context.Context
	Pools             *store.Pools
	Redis             *redis.Client
	Auth              *auth.Service
	Tokens            *auth.TokenManager
	Mailer            notify.Mailer
	Objects           *objectstore.Client
	// 加密运维填进库的腾讯云 SES API 凭据。没配 MAIL_SECRET_KEY 时为 nil，保存凭据会被拒绝。
	MailCipher *opsconfig.Cipher
	// AI provider key 使用独立的部署密钥；浏览器永远拿不到明文或密文。
	AICipher *opsconfig.Cipher
	// 远程备份桶的 AccessKey/SecretKey 又是一把独立密钥，跟着桶一起轮换，
	// 不牵动邮件和模型那两套。没配 BACKUP_REMOTE_SECRET_KEY 时为 nil。
	BackupRemoteCipher *opsconfig.Cipher
	Limiter            *ratelimit.Limiter
}

// corsOrigins 是允许直传附件的浏览器来源。
//
// 生产来源取自 PUBLIC_URL —— 前端与 API 同源,能打开页面的就是它。
// 另外固定放行 Vite、日常开发 Compose 与隔离 E2E 的默认本地地址。
// 自定义端口通过 PUBLIC_URL 补入。
//
// 各端口都要同时列出 localhost 与 127.0.0.1:浏览器按字面串比对 Origin,
// 二者不会互相等价。vite.config.ts 特意用 host:true 就是因为 Windows 上
// 'localhost' 只解析到 ::1,开发时用 127.0.0.1 打开是常态。
func corsOrigins(publicURL string) []string {
	origins := []string{
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://localhost:35173",
		"http://127.0.0.1:35173",
		"http://localhost:45173",
		"http://127.0.0.1:45173",
	}
	if u, err := url.Parse(strings.TrimSpace(publicURL)); err == nil && u.Scheme != "" && u.Host != "" {
		origin := u.Scheme + "://" + u.Host
		for _, existing := range origins {
			if existing == origin {
				return origins
			}
		}
		origins = append(origins, origin)
	}
	return origins
}

func Run(cfg *config.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pools, err := store.Open(ctx, cfg.DatabaseURL, cfg.OpsDatabaseURL)
	if err != nil {
		return err
	}
	defer pools.Close()
	if pools.Ops != nil {
		if err := platformknowledge.Sync(ctx, pools.Ops); err != nil {
			return err
		}
	}
	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	defer redisClient.Close()
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	err = redisClient.Ping(pingCtx).Err()
	pingCancel()
	if err != nil {
		return fmt.Errorf("Redis 不可用: %w", err)
	}

	/* 发信参数优先取运行配置（运维页里填的），库里没配才用环境变量。
	   自检邮件因此走的就是运维刚填的那套参数，填完立刻能验证。 */
	mailCipher, err := opsconfig.CipherFromKey(cfg.MailSecretKey)
	if err != nil {
		return err
	}
	aiCipher, err := opsconfig.CipherFromNamedKey(cfg.AIConfigSecretKey, "AI_CONFIG_SECRET_KEY")
	if err != nil {
		return err
	}
	backupRemoteCipher, err := opsconfig.CipherFromNamedKey(cfg.BackupRemoteSecretKey, "BACKUP_REMOTE_SECRET_KEY")
	if err != nil {
		return err
	}
	var mailSettings *opsconfig.Store
	if pools.Ops != nil {
		mailSettings = opsconfig.New(pools.Ops, 0)
	}
	mailer, err := notify.NewRuntimeSESMailer(mailSettings, mailCipher, notify.SESConfig{
		Region: cfg.TencentCloudSESRegion, SecretID: cfg.TencentCloudSecretID, SecretKey: cfg.TencentCloudSecretKey,
		From: cfg.TencentCloudSESFrom, FromName: cfg.TencentCloudSESFromName, ReplyTo: cfg.TencentCloudSESReplyTo,
		TemplateIDs: cfg.TencentCloudSESTemplateIDs,
	})
	if err != nil {
		return err
	}
	auditedMailer, err := notify.NewAuditedMailer(notify.NewSuppressionMailer(mailer, pools.Ops), pools.Ops)
	if err != nil {
		return err
	}
	mailPolicy, err := notify.NewRuntimePolicy(mailSettings, redisClient)
	if err != nil {
		return err
	}
	policyMailer, err := notify.NewPolicyMailer(auditedMailer, mailPolicy)
	if err != nil {
		return err
	}
	templateMailer, err := notify.NewTemplateMailer(policyMailer, notify.TemplateConfig{
		Dir: cfg.MailTemplateDir, BaseURL: cfg.PublicURL,
	})
	if err != nil {
		return err
	}
	tokens, err := auth.NewTokenManager(cfg.JWTSecret, cfg.AccessTokenTTL)
	if err != nil {
		return err
	}
	sessions, err := auth.NewSessionStore(redisClient, cfg.RefreshTokenTTL)
	if err != nil {
		return err
	}
	authService, err := auth.NewService(pools.App, redisClient, tokens, sessions, templateMailer, auth.ServiceConfig{
		PublicURL: cfg.PublicURL, OpsAccount: cfg.OpsAccount, OpsPassword: cfg.OpsPassword,
	})
	if err != nil {
		return err
	}
	requestLimiter, err := ratelimit.New(redisClient)
	if err != nil {
		return err
	}
	objects, err := objectstore.New(objectstore.Config{
		Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3PublicEndpoint, Region: cfg.S3Region, AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket, UseSSL: cfg.S3UseSSL, PublicUseSSL: cfg.S3PublicUseSSL,
	})
	if err != nil {
		return err
	}
	// 佐证直传是浏览器跨源 POST,桶上没有 CORS 规则就会卡在预检。
	// 放在启动时做而不是靠人记得去设:换环境、重建桶都会重新踩到这一脚。
	// 失败只告警不阻断——对象存储暂时抽风时,系统其余部分还能用。
	if err := objects.EnsureCORS(ctx, corsOrigins(cfg.PublicURL)); err != nil {
		slog.Warn("bucket cors not applied, browser uploads may fail preflight", "err", err)
	}

	router := NewRouter(cfg, Dependencies{
		GovernanceContext: ctx,
		Pools:             pools, Redis: redisClient, Auth: authService, Tokens: tokens, Mailer: templateMailer,
		Objects: objects, MailCipher: mailCipher, AICipher: aiCipher,
		BackupRemoteCipher: backupRemoteCipher, Limiter: requestLimiter,
	})
	srv := &http.Server{
		Addr: cfg.HTTPAddr, Handler: router,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	select {
	case err := <-errCh:
		return err
	case sig := <-quit:
		slog.Info("shutting down", "signal", sig.String())
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	}
}
