// Package worker wires process lifecycles, the shared outbox relay and Redis
// Stream consumers for every background role.
package worker

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"easygpa/backend/internal/agentjob"
	"easygpa/backend/internal/aijob"
	"easygpa/backend/internal/backupjob"
	"easygpa/backend/internal/config"
	"easygpa/backend/internal/dispatch"
	"easygpa/backend/internal/events"
	"easygpa/backend/internal/exportjob"
	"easygpa/backend/internal/maintenance"
	"easygpa/backend/internal/notify"
	"easygpa/backend/internal/objectstore"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/platformknowledge"
	"easygpa/backend/internal/store"
	"easygpa/backend/internal/workerhealth"
)

func Run(cfg *config.Config, kind string) error {
	if !knownKind(kind) {
		return errors.New("unknown worker kind")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pools, err := store.Open(ctx, cfg.DatabaseURL, cfg.OpsDatabaseURL)
	if err != nil {
		return err
	}
	defer pools.Close()
	client := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	defer client.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = client.Ping(pingCtx).Err()
	cancel()
	if err != nil {
		return err
	}
	if err := client.Set(ctx, workerHeartbeatKey(kind), time.Now().UTC().Format(time.RFC3339Nano), 45*time.Second).Err(); err != nil {
		return fmt.Errorf("initialize %s worker heartbeat: %w", kind, err)
	}
	supervisor, runCtx := errgroup.WithContext(ctx)
	supervisor.Go(func() error { return runHeartbeat(runCtx, client, kind) })
	var group string
	var handler events.Handler
	if kind == "maintenance" {
		if pools.Ops == nil {
			return errors.New("maintenance worker requires OPS_DATABASE_URL")
		}
		objects, err := objectstore.New(objectstore.Config{
			Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3PublicEndpoint, Region: cfg.S3Region,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket, UseSSL: cfg.S3UseSSL, PublicUseSSL: cfg.S3PublicUseSSL,
		})
		if err != nil {
			return err
		}
		runner, err := maintenance.NewRunner(pools.App, pools.Ops, client, objects)
		if err != nil {
			return err
		}
		supervisor.Go(func() error { return events.RunRelay(runCtx, pools.App, client) })
		supervisor.Go(func() error { return runner.Run(runCtx) })
		return finishWorker(supervisor.Wait())
	}
	if kind == "backup" {
		if pools.Ops == nil {
			return errors.New("backup worker requires OPS_DATABASE_URL")
		}
		databaseURL := cfg.MigrationsDatabaseURL
		if databaseURL == "" {
			databaseURL = cfg.DatabaseURL
		}
		objects, err := objectstore.New(objectstore.Config{
			Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3PublicEndpoint, Region: cfg.S3Region,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket, UseSSL: cfg.S3UseSSL, PublicUseSSL: cfg.S3PublicUseSSL,
		})
		if err != nil {
			return err
		}
		remoteCipher, err := opsconfig.CipherFromNamedKey(cfg.BackupRemoteSecretKey, "BACKUP_REMOTE_SECRET_KEY")
		if err != nil {
			return err
		}
		runner, err := backupjob.NewRunnerWithRuntime(pools.Ops, objects, backupjob.Config{
			DatabaseURL: databaseURL, LocalDir: cfg.BackupDir, OffsiteDir: cfg.BackupOffsiteDir,
			Retention:       time.Duration(cfg.BackupRetentionDays) * 24 * time.Hour,
			RemoteRecipient: cfg.BackupRemoteRecipient, RemoteCipher: remoteCipher,
			RemoteAllowLoopback: cfg.AppEnv != "prod",
		}, opsconfig.New(pools.Ops, 0))
		if err != nil {
			return err
		}
		supervisor.Go(func() error { return runner.Run(runCtx) })
		return finishWorker(supervisor.Wait())
	}
	switch kind {
	case "dispatch":
		service, err := dispatch.NewWorker(pools.App)
		if err != nil {
			return err
		}
		group, handler = "dispatch-cg", service.Handle
	case "notify":
		if pools.Ops == nil {
			return errors.New("notification worker requires OPS_DATABASE_URL")
		}
		/* 和 API 一样按运行配置发信：运维在网页上改完通道，Worker 下一封就用新参数，
		   不需要滚动重启。取不到配置时回退到环境变量。 */
		mailCipher, err := opsconfig.CipherFromKey(cfg.MailSecretKey)
		if err != nil {
			return err
		}
		sesMailer, err := notify.NewRuntimeSESMailer(opsconfig.New(pools.Ops, 0), mailCipher, notify.SESConfig{
			Region: cfg.TencentCloudSESRegion, SecretID: cfg.TencentCloudSecretID, SecretKey: cfg.TencentCloudSecretKey,
			From: cfg.TencentCloudSESFrom, FromName: cfg.TencentCloudSESFromName, ReplyTo: cfg.TencentCloudSESReplyTo,
			TemplateIDs: cfg.TencentCloudSESTemplateIDs,
		})
		if err != nil {
			return err
		}
		auditedMailer, err := notify.NewAuditedMailer(notify.NewSuppressionMailer(sesMailer, pools.Ops), pools.Ops)
		if err != nil {
			return err
		}
		policy, err := notify.NewRuntimePolicy(opsconfig.New(pools.Ops, 0), client)
		if err != nil {
			return err
		}
		policyMailer, err := notify.NewPolicyMailer(auditedMailer, policy)
		if err != nil {
			return err
		}
		mailer, err := notify.NewTemplateMailer(policyMailer, notify.TemplateConfig{
			Dir: cfg.MailTemplateDir, BaseURL: cfg.PublicURL,
		})
		if err != nil {
			return err
		}
		service, err := notify.NewWorker(pools.App, mailer)
		if err != nil {
			return err
		}
		group, handler = "notify-cg", service.Handle
		service.ConfigureQueue(pools.Ops, opsconfig.New(pools.Ops, 0))
		supervisor.Go(func() error { return service.RunQueue(runCtx) })
		supervisor.Go(func() error { return sesMailer.RunFeedback(runCtx, pools.Ops) })
	case "export":
		if pools.Ops == nil {
			return errors.New("export worker requires OPS_DATABASE_URL")
		}
		objects, err := objectstore.New(objectstore.Config{
			Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3PublicEndpoint, Region: cfg.S3Region,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket, UseSSL: cfg.S3UseSSL, PublicUseSSL: cfg.S3PublicUseSSL,
		})
		if err != nil {
			return err
		}
		runtimeConfig := opsconfig.New(pools.Ops, 0)
		limiter, err := exportjob.NewPostgresLimiter(pools.App, runtimeConfig)
		if err != nil {
			return err
		}
		service, err := exportjob.NewWorkerWithRuntime(pools.App, objects, limiter, runtimeConfig)
		if err != nil {
			return err
		}
		group, handler = "export-cg", service.Handle
	case "ai":
		objects, err := objectstore.New(objectstore.Config{
			Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3PublicEndpoint, Region: cfg.S3Region,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket, UseSSL: cfg.S3UseSSL, PublicUseSSL: cfg.S3PublicUseSSL,
		})
		if err != nil {
			return err
		}
		aiCipher, err := opsconfig.CipherFromNamedKey(cfg.AIConfigSecretKey, "AI_CONFIG_SECRET_KEY")
		if err != nil {
			return err
		}
		var runtimeConfig *opsconfig.Store
		if pools.Ops != nil {
			runtimeConfig = opsconfig.New(pools.Ops, 0)
		}
		fallback := opsconfig.DefaultAI()
		fallback.BaseURL, fallback.APIKey = cfg.LLMBaseURL, cfg.LLMAPIKey
		fallback.TextModel, fallback.VisionModel, fallback.AgentModel = cfg.LLMTextModel, cfg.LLMVisionModel, cfg.LLMAgentModel
		fallback.MaterialMaxItems, fallback.MaterialMaxPDFPages = cfg.AIMaxBatch, cfg.AIMaxPDFPages
		fallback.MaterialConcurrency = cfg.AIConcurrency
		modelClient := newRuntimeAIClient(runtimeConfig, aiCipher, fallback, cfg.AIEnabled, cfg.AllowPrivateAINetwork(), cfg.LLMTimeout)
		limitsProvider := func(ctx context.Context) (aijob.Limits, error) {
			settings := fallback
			flags := opsconfig.DefaultFlags()
			lifecycle := opsconfig.DefaultLifecycle()
			if runtimeConfig != nil {
				stored, err := runtimeConfig.AI(ctx)
				if err != nil {
					return aijob.Limits{}, err
				}
				settings = stored
				flags, err = runtimeConfig.Flags(ctx)
				if err != nil {
					return aijob.Limits{}, err
				}
				lifecycle, err = runtimeConfig.Lifecycle(ctx)
				if err != nil {
					return aijob.Limits{}, err
				}
			}
			return aijob.Limits{
				Concurrency: settings.MaterialConcurrency, MaxItems: settings.MaterialMaxItems,
				MaxPDFPages: settings.MaterialMaxPDFPages, NativeToolsEnabled: flags.NativeToolsEnabled,
				ConverterTimeoutSeconds: lifecycle.KnowledgeConverterTimeoutSeconds,
			}, nil
		}
		service, err := aijob.NewWorker(pools.App, objects, modelClient, aijob.Config{
			Concurrency: cfg.AIConcurrency, MaxItems: cfg.AIMaxBatch, MaxPDFPages: cfg.AIMaxPDFPages,
			NativeToolsEnabled: true, ConverterTimeoutSeconds: opsconfig.DefaultLifecycle().KnowledgeConverterTimeoutSeconds,
			RuntimeLimits: limitsProvider,
		})
		if err != nil {
			return err
		}
		group, handler = "ai-cg", service.Handle
	case "agent":
		if pools.Ops != nil {
			if err := platformknowledge.Sync(ctx, pools.Ops); err != nil {
				return err
			}
		}
		objects, err := objectstore.New(objectstore.Config{
			Endpoint: cfg.S3Endpoint, PublicEndpoint: cfg.S3PublicEndpoint, Region: cfg.S3Region,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket, UseSSL: cfg.S3UseSSL, PublicUseSSL: cfg.S3PublicUseSSL,
		})
		if err != nil {
			return err
		}
		aiCipher, err := opsconfig.CipherFromNamedKey(cfg.AIConfigSecretKey, "AI_CONFIG_SECRET_KEY")
		if err != nil {
			return err
		}
		var runtimeConfig *opsconfig.Store
		if pools.Ops != nil {
			runtimeConfig = opsconfig.New(pools.Ops, 0)
		}
		fallback := opsconfig.DefaultAI()
		fallback.BaseURL, fallback.APIKey = cfg.LLMBaseURL, cfg.LLMAPIKey
		fallback.TextModel, fallback.VisionModel, fallback.AgentModel = cfg.LLMTextModel, cfg.LLMVisionModel, cfg.LLMAgentModel
		modelClient := newRuntimeAIClient(runtimeConfig, aiCipher, fallback, cfg.AIEnabled, cfg.AllowPrivateAINetwork(), cfg.LLMTimeout)
		provider := func(ctx context.Context) (agentjob.Runtime, error) {
			flags := opsconfig.DefaultFlags()
			flags.AIEnabled = cfg.AIEnabled
			stored := opsconfig.DefaultAI()
			lifecycle := opsconfig.DefaultLifecycle()
			if runtimeConfig != nil {
				var loadErr error
				flags, loadErr = runtimeConfig.Flags(ctx)
				if loadErr != nil {
					return agentjob.Runtime{}, loadErr
				}
				stored, loadErr = runtimeConfig.AI(ctx)
				if loadErr != nil {
					return agentjob.Runtime{}, loadErr
				}
				lifecycle, loadErr = runtimeConfig.Lifecycle(ctx)
				if loadErr != nil {
					return agentjob.Runtime{}, loadErr
				}
			}
			runtime, resolveErr := opsconfig.ResolveAI(stored, aiCipher, fallback, cfg.AllowPrivateAINetwork())
			if resolveErr != nil {
				// Provider credentials now belong to per-purpose routes. The agent
				// worker still needs limits and feature flags even when the legacy
				// singleton connection is intentionally empty.
				runtime = opsconfig.AIRuntime{AI: stored}
			}
			return agentjob.Runtime{Flags: flags, AI: runtime, Lifecycle: lifecycle}, nil
		}
		service, err := agentjob.NewWorker(pools.App, objects, agentjob.Config{Runtime: provider, Client: modelClient, OpsPool: pools.Ops})
		if err != nil {
			return err
		}
		group, handler = "agent-cg", service.Handle
	default:
		return errors.New("unknown worker kind")
	}
	supervisor.Go(func() error { return events.RunRelay(runCtx, pools.App, client) })
	supervisor.Go(func() error { return events.RunConsumer(runCtx, client, group, handler) })
	return finishWorker(supervisor.Wait())
}

func knownKind(kind string) bool {
	switch kind {
	case "dispatch", "notify", "export", "maintenance", "backup", "ai", "agent":
		return true
	default:
		return false
	}
}

func workerHeartbeatKey(kind string) string { return workerhealth.Key(kind) }

func finishWorker(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func runHeartbeat(ctx context.Context, client *redis.Client, kind string) error {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if err := client.Set(ctx, workerHeartbeatKey(kind), now.UTC().Format(time.RFC3339Nano), 45*time.Second).Err(); err != nil {
				return fmt.Errorf("refresh %s worker heartbeat: %w", kind, err)
			}
		}
	}
}
