package api

// 运维页的「远程副本」：配一个云桶，把每天的备份加密后推到机房之外。
//
// 桶的 AccessKey/SecretKey 和邮件那套腾讯云凭据一样是"写得进、读不出"：
// 加密后落库，任何接口都只回"设没设过"，不回内容也不回密文。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/backupjob"
	"easygpa/backend/internal/opsconfig"
)

// allowLoopbackBucket 只在非生产环境放行本机地址，用来对着开发栈里的
// Garage/MinIO 试这一整条链路。生产上任何解析到内网的桶地址都要拒绝。
func (s *Server) allowLoopbackBucket() bool { return s.cfg.AppEnv != "prod" }

func (s *Server) opsBackupRemote(c *gin.Context) {
	config, err := s.opsConfig.BackupRemote(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "backup_remote.read", "backup_remote", "backup_remote", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	redacted, accessKeySet, secretKeySet := redactBackupRemote(config)
	c.JSON(http.StatusOK, gin.H{
		"config": redacted, "accessKeySet": accessKeySet, "secretKeySet": secretKeySet,
		// secretKeyReady 说的是服务端能不能加密保存凭据（BACKUP_REMOTE_SECRET_KEY），
		// recipientReady 说的是能不能加密归档（BACKUP_REMOTE_RECIPIENT）。
		// 两个都是部署给的，缺哪个界面上要分别讲清楚。
		"secretKeyReady": s.deps.BackupRemoteCipher != nil,
		"recipientReady": strings.TrimSpace(s.cfg.BackupRemoteRecipient) != "",
	})
}

// redactBackupRemote 把两个凭据字段清空。密文也不下发：浏览器永远只知道
// "设没设过"，不知道内容。
func redactBackupRemote(config opsconfig.BackupRemote) (opsconfig.BackupRemote, bool, bool) {
	accessKeySet := strings.TrimSpace(config.AccessKey) != ""
	secretKeySet := strings.TrimSpace(config.SecretKey) != ""
	config.AccessKey = ""
	config.SecretKey = ""
	return config, accessKeySet, secretKeySet
}

type backupRemoteInput struct {
	Enabled       *bool   `json:"enabled"`
	Endpoint      *string `json:"endpoint"`
	Bucket        *string `json:"bucket"`
	Region        *string `json:"region"`
	Prefix        *string `json:"prefix"`
	UseSSL        *bool   `json:"useSsl"`
	PathStyle     *bool   `json:"pathStyle"`
	AccessKey     *string `json:"accessKey"`
	SecretKey     *string `json:"secretKey"`
	RetentionDays *int    `json:"retentionDays"`
}

// bindStrict 拒绝不认识的字段。前端拼错一个键名时要当场报错，
// 而不是静默地什么都没改、运维以为存上了。
func bindStrict(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func (s *Server) updateOpsBackupRemote(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var input backupRemoteInput
	if err := bindStrict(c, &input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "远程备份配置参数不正确", nil)
		return
	}
	stored, err := s.opsConfig.BackupRemote(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// 整份读出来改完再整份写回，而不是像邮件那样做 jsonb 合并：这几个字段互相
	// 牵制（enabled 要求另外四项齐全、pathStyle 决定 endpoint 的含义），
	// 只校验"这次传来的那几个"会放过一半合法一半不合法的组合。
	next, changed := applyBackupRemoteInput(stored, input)
	if len(changed) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "没有要修改的字段", nil)
		return
	}
	if err := validateBackupRemoteUpdate(next, s.allowLoopbackBucket()); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "backup_remote_invalid", err.Error(), nil)
		return
	}
	if err := sealBackupRemoteSecrets(&next, input, s.deps.BackupRemoteCipher); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "backup_remote_invalid", err.Error(), nil)
		return
	}
	if next.Enabled && !next.Configured() {
		writeError(c, http.StatusUnprocessableEntity, "backup_remote_incomplete", "开启前要先把桶地址、桶名和两个凭据都填好", nil)
		return
	}
	raw, err := json.Marshal(next)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
		UPDATE ops_config SET value=$1::jsonb,updated_at=now() WHERE key='backup_remote'
	`, raw); err != nil {
		writeServiceError(c, err)
		return
	}
	s.opsConfig.Invalidate("backup_remote")
	if err := s.appendOpsAudit(c.Request.Context(), c, "backup_remote.updated", "backup_remote", "backup_remote", map[string]any{
		"fields": changed, "bucket": next.Bucket, "endpoint": next.Endpoint, "enabled": next.Enabled,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// applyBackupRemoteInput 把这次提交的字段盖到已存的配置上，并回报改了哪些。
// 凭据两项在这里只记名字，真正的加密在 sealBackupRemoteSecrets。
func applyBackupRemoteInput(stored opsconfig.BackupRemote, input backupRemoteInput) (opsconfig.BackupRemote, []string) {
	changed := make([]string, 0, 10)
	if input.Enabled != nil {
		stored.Enabled, changed = *input.Enabled, append(changed, "enabled")
	}
	if input.Endpoint != nil {
		stored.Endpoint, changed = strings.TrimSpace(*input.Endpoint), append(changed, "endpoint")
	}
	if input.Bucket != nil {
		stored.Bucket, changed = strings.TrimSpace(*input.Bucket), append(changed, "bucket")
	}
	if input.Region != nil {
		stored.Region, changed = strings.TrimSpace(*input.Region), append(changed, "region")
	}
	if input.Prefix != nil {
		stored.Prefix, changed = strings.TrimSpace(*input.Prefix), append(changed, "prefix")
	}
	if input.UseSSL != nil {
		stored.UseSSL, changed = *input.UseSSL, append(changed, "useSsl")
	}
	if input.PathStyle != nil {
		stored.PathStyle, changed = *input.PathStyle, append(changed, "pathStyle")
	}
	if input.RetentionDays != nil {
		stored.RetentionDays, changed = *input.RetentionDays, append(changed, "retentionDays")
	}
	if input.AccessKey != nil {
		changed = append(changed, "accessKey")
	}
	if input.SecretKey != nil {
		changed = append(changed, "secretKey")
	}
	return stored, changed
}

func validateBackupRemoteUpdate(value opsconfig.BackupRemote, allowLoopback bool) error {
	// 四项都空表示这套配置还没填，允许存着（比如只想先改保留期）。
	// 一旦填了任何一项，就要求它自洽。
	if value.Endpoint != "" || value.Bucket != "" {
		if err := opsconfig.ValidateBackupRemote(value, allowLoopback); err != nil {
			return err
		}
	}
	if value.RetentionDays != 0 && (value.RetentionDays < 1 || value.RetentionDays > 3650) {
		return errors.New("远程保留天数必须在 1—3650 之间")
	}
	return nil
}

// sealBackupRemoteSecrets 加密这次填的桶凭据。
//
// 没传这个键 = 不改动；传了空串 = 显式清空；传了值 = 加密后覆盖。
// 没配 BACKUP_REMOTE_SECRET_KEY 时明确拒绝，而不是悄悄存明文。
func sealBackupRemoteSecrets(target *opsconfig.BackupRemote, input backupRemoteInput, cipher *opsconfig.Cipher) error {
	for _, item := range []struct {
		incoming *string
		field    *string
		name     string
	}{
		{input.AccessKey, &target.AccessKey, "AccessKey"},
		{input.SecretKey, &target.SecretKey, "SecretKey"},
	} {
		if item.incoming == nil {
			continue
		}
		secret := strings.TrimSpace(*item.incoming)
		if secret == "" {
			*item.field = ""
			continue
		}
		if cipher == nil {
			return errors.New("服务端未配置 BACKUP_REMOTE_SECRET_KEY，无法安全保存桶凭据；请先设置该环境变量再重试")
		}
		sealed, err := cipher.Seal(secret)
		if err != nil {
			return err
		}
		*item.field = sealed
	}
	return nil
}

// parseOpsBucketURL 把运维粘的桶链接拆成表单字段。
//
// 放在服务端而不是前端：解析规则要和真正连桶时用的是同一份，而且顺手就把
// 内网地址挡在这里，不用等到保存那一步。
func (s *Server) parseOpsBucketURL(c *gin.Context) {
	var input struct {
		URL string `json:"url"`
	}
	if err := bindStrict(c, &input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "桶链接参数不正确", nil)
		return
	}
	parsed, err := opsconfig.ParseBucketURL(input.URL)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "bucket_url_invalid", err.Error(), nil)
		return
	}
	if err := opsconfig.ValidateBucketEndpoint(parsed.Endpoint, parsed.UseSSL, s.allowLoopbackBucket()); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "bucket_url_invalid", err.Error(), nil)
		return
	}
	if err := opsconfig.ValidateBucketName(parsed.Bucket); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "bucket_url_invalid", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"endpoint": parsed.Endpoint, "bucket": parsed.Bucket, "region": parsed.Region,
		"prefix": parsed.Prefix, "useSsl": parsed.UseSSL, "pathStyle": parsed.PathStyle,
	})
}

// testOpsBackupRemote 用库里存的那套参数往桶里写、读、删一个小对象。
//
// **这个测试跑在 api 进程里，而 api 有出网权限。备份 Worker 是另一张网。**
// 测试通过只说明凭据和桶配对了，不说明 worker-backup 能连上去——那要看
// backend.compose.yaml 里 worker-backup 有没有挂 egress 网络。
func (s *Server) testOpsBackupRemote(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	stored, err := s.opsConfig.BackupRemote(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !stored.Configured() {
		writeError(c, http.StatusConflict, "backup_remote_incomplete", "远程备份桶还没配齐：地址、桶名和两个凭据都要填", nil)
		return
	}
	if s.deps.BackupRemoteCipher == nil {
		writeError(c, http.StatusServiceUnavailable, "backup_remote_key_missing", "服务端未配置 BACKUP_REMOTE_SECRET_KEY，无法解开已保存的桶凭据", nil)
		return
	}
	runtime, err := opsconfig.ResolveBackupRemote(stored, s.deps.BackupRemoteCipher)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "backup_remote_invalid", err.Error(), nil)
		return
	}
	runtime.AllowLoopback = s.allowLoopbackBucket()
	store, err := backupjob.NewRemoteStore(runtime)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "backup_remote_invalid", err.Error(), nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	probe, probeErr := backupjob.ProbeRemote(ctx, store, runtime.Prefix)
	if probeErr != nil {
		_ = s.appendOpsAudit(c.Request.Context(), c, "backup_remote.test_failed", "backup_remote", "backup_remote", map[string]any{
			"bucket": runtime.Bucket, "wrote": probe.Wrote, "read": probe.Read, "error": probeErr.Error(),
		})
		writeError(c, http.StatusBadGateway, "backup_remote_unreachable", probeErr.Error(), gin.H{
			"wrote": probe.Wrote, "read": probe.Read, "removed": probe.Removed,
		})
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "backup_remote.test_passed", "backup_remote", "backup_remote", map[string]any{
		"bucket": runtime.Bucket, "endpoint": runtime.Endpoint,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "wrote": probe.Wrote, "read": probe.Read, "removed": probe.Removed,
		"recipientReady": strings.TrimSpace(s.cfg.BackupRemoteRecipient) != "",
	})
}

// clearOpsBackupRemote 清空整套远程配置并关掉开关。
// 换桶、换云或者不想再往外传时用它，语义等同邮件页那个"回到部署默认"。
func (s *Server) clearOpsBackupRemote(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	cleared := opsconfig.DefaultBackupRemote()
	raw, err := json.Marshal(cleared)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
		UPDATE ops_config SET value=$1::jsonb,updated_at=now() WHERE key='backup_remote'
	`, raw); err != nil {
		writeServiceError(c, err)
		return
	}
	s.opsConfig.Invalidate("backup_remote")
	if err := s.appendOpsAudit(c.Request.Context(), c, "backup_remote.cleared", "backup_remote", "backup_remote", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
