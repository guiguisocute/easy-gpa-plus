// Package opsconfig exposes platform-wide operational settings to the API and
// workers. Values live in the ops database so they can be changed without a
// process restart; a short cache keeps them off the request hot path.
package opsconfig

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

const defaultTTL = 2 * time.Second

type Flags struct {
	Maintenance               bool     `json:"maintenance"`
	Registration              bool     `json:"registration"`
	AIEnabled                 bool     `json:"aiEnabled"`
	KnowledgeEnabled          bool     `json:"knowledgeEnabled"`
	AgentActionsEnabled       bool     `json:"agentActionsEnabled"`
	KnowledgeEgressEnabled    bool     `json:"knowledgeEgressEnabled"`
	NativeToolsEnabled        bool     `json:"nativeToolsEnabled"`
	UploadMaxMB               int      `json:"uploadMaxMb"`
	RequestConcurrency        int      `json:"requestConcurrency"`
	ExportConcurrency         int      `json:"exportConcurrency"`
	PasswordHashConcurrency   int      `json:"passwordHashConcurrency"`
	APIRateLimitPerMinute     int      `json:"apiRateLimitPerMinute"`
	APIRateLimitBurst         int      `json:"apiRateLimitBurst"`
	AuthLoginPerMinute        int      `json:"authLoginPerMinute"`
	AuthRefreshPerMinute      int      `json:"authRefreshPerMinute"`
	AuthRegisterPerHour       int      `json:"authRegisterPerHour"`
	AuthForgotPerHour         int      `json:"authForgotPerHour"`
	AuthResetPerHour          int      `json:"authResetPerHour"`
	EvidencePresignPerHour    int      `json:"evidencePresignPerHour"`
	EvidenceDailyMB           int      `json:"evidenceDailyMb"`
	AIPresignPerHour          int      `json:"aiPresignPerHour"`
	AgentPresignPerHour       int      `json:"agentPresignPerHour"`
	KnowledgePresignPerHour   int      `json:"knowledgePresignPerHour"`
	AIBatchActionsPerHour     int      `json:"aiBatchActionsPerHour"`
	AgentMessagesPerMinute    int      `json:"agentMessagesPerMinute"`
	ExportRequestsPerHour     int      `json:"exportRequestsPerHour"`
	KnowledgeReprocessPerHour int      `json:"knowledgeReprocessPerHour"`
	EvidenceAllowedFormats    []string `json:"evidenceAllowedFormats"`
}

func DefaultFlags() Flags {
	return Flags{
		Registration:            true,
		NativeToolsEnabled:      true,
		UploadMaxMB:             50,
		RequestConcurrency:      64,
		ExportConcurrency:       1,
		PasswordHashConcurrency: 4,
		APIRateLimitPerMinute:   180,
		APIRateLimitBurst:       60,
		// 这几条按 IP 计，而一个班从同一个校园 NAT 出去。开放窗口那天集体登录、
		// 或者一个班同批注册，10/分钟、20/小时会把绝大多数人挡在门外，而他们
		// 无计可施。爆破防护不靠这里：登录另有一道按账号的限流（15 分钟 20 次），
		// 注册则由白名单兜底——学号不在名单上，请求再多也注册不出账号。
		AuthLoginPerMinute:   120,
		AuthRefreshPerMinute: 60,
		AuthRegisterPerHour:  120,
		AuthForgotPerHour:    5,
		AuthResetPerHour:     10,
		// 上传类阈值要压得住方案允许的量，否则真正生效的是这里而不是业务配额：
		// 一批最多 100 张材料、每天 5 批，presign 300/小时会在半路把人拦下。
		EvidencePresignPerHour:    300,
		EvidenceDailyMB:           1024,
		AIPresignPerHour:          600,
		AgentPresignPerHour:       120,
		KnowledgePresignPerHour:   120,
		AIBatchActionsPerHour:     10,
		AgentMessagesPerMinute:    20,
		ExportRequestsPerHour:     10,
		KnowledgeReprocessPerHour: 30,
		EvidenceAllowedFormats:    DefaultEvidenceAllowedFormats(),
	}
}

type Mail struct {
	NotificationsEnabled    bool      `json:"notificationsEnabled"`
	NotificationsSince      time.Time `json:"notificationsSince"`
	SESNotificationDomain   string    `json:"sesNotificationDomain,omitempty"`
	SESNotificationFrom     string    `json:"sesNotificationFrom,omitempty"`
	SESNotificationFromName string    `json:"sesNotificationFromName,omitempty"`
	Provider                string    `json:"provider"`
	PerMinute               int       `json:"perMinute"`
	PerDay                  int       `json:"perDay"`
	QuietStart              string    `json:"quietStart"`
	QuietEnd                string    `json:"quietEnd"`
	// 腾讯云 SES API 通道。SecretId/SecretKey 都以 enc:v1 密文保存，任何接口
	// 都不会把它们下发给浏览器。全部留空时回退到部署环境变量。
	SESRegion      string            `json:"sesRegion,omitempty"`
	SESSecretID    string            `json:"sesSecretId,omitempty"`
	SESSecretKey   string            `json:"sesSecretKey,omitempty"`
	SESFrom        string            `json:"sesFrom,omitempty"`
	SESFromName    string            `json:"sesFromName,omitempty"`
	SESReplyTo     string            `json:"sesReplyTo,omitempty"`
	SESTemplateIDs map[string]uint64 `json:"sesTemplateIds,omitempty"`
	// Provider credentials stay separate so switching a channel never sends
	// another service's secret to the newly selected endpoint.
	SMTPHost              string `json:"smtpHost,omitempty"`
	SMTPPort              int    `json:"smtpPort,omitempty"`
	SMTPSecurity          string `json:"smtpSecurity,omitempty"`
	SMTPUsername          string `json:"smtpUsername,omitempty"`
	SMTPPassword          string `json:"smtpPassword,omitempty"`
	AliyunRegion          string `json:"aliyunRegion,omitempty"`
	AliyunAccessKeyID     string `json:"aliyunAccessKeyId,omitempty"`
	AliyunAccessKeySecret string `json:"aliyunAccessKeySecret,omitempty"`
	ResendAPIKey          string `json:"resendApiKey,omitempty"`
}

// RequiredMailTemplates is the one registry shared by template loading,
// Tencent Cloud template-ID validation, and the ops UI.
var RequiredMailTemplates = []string{
	"mail_test", "password_reset", "verification_code",
}

// Kept for inspecting old messages; these templates cannot be sent anymore.
var LegacyMailTemplates = []string{
	"appeal_pending",
	"appeal_received",
	"appeal_resolved",
	"final_review_ready",
	"review_decided",
	"result_confirmed",
	"ruling_notice",
	"scorecard_audit_task",
	"seal_confirmed",
	"settlement_done",
	"task_new",
	"window_reminder",
}

// New business templates are optional while notifications are paused. Their
// review must never make verification or password recovery unavailable.
var NotificationMailTemplates = []string{"notification_alert", "notification_digest"}

func AllMailTemplates() []string {
	return append(append(append([]string(nil), RequiredMailTemplates...), LegacyMailTemplates...), NotificationMailTemplates...)
}

// AI is the persisted OpenAI-compatible provider configuration. APIKey is
// either empty or an enc:v1 ciphertext; it must never be serialized to a
// browser response without being cleared first.
type AI struct {
	BaseURL                       string   `json:"baseUrl,omitempty"`
	APIKey                        string   `json:"apiKey,omitempty"`
	TextModel                     string   `json:"textModel,omitempty"`
	VisionModel                   string   `json:"visionModel,omitempty"`
	AgentModel                    string   `json:"agentModel,omitempty"`
	AgentMaxSteps                 int      `json:"agentMaxSteps,omitempty"`
	AgentTimeoutSeconds           int      `json:"agentTimeoutSeconds,omitempty"`
	AgentMaxAnswerKB              int      `json:"agentMaxAnswerKb,omitempty"`
	AgentToolResultKB             int      `json:"agentToolResultKb,omitempty"`
	AgentToolScanMB               int      `json:"agentToolScanMb,omitempty"`
	AgentDailyMessages            int      `json:"agentDailyMessages,omitempty"`
	MaterialDailyBatches          int      `json:"materialDailyBatches,omitempty"`
	MaterialActiveBatches         int      `json:"materialActiveBatches,omitempty"`
	KnowledgeMaxFilesPerClass     int      `json:"knowledgeMaxFilesPerClass,omitempty"`
	KnowledgeMaxStorageMBPerClass int      `json:"knowledgeMaxStorageMbPerClass,omitempty"`
	MaterialMaxItems              int      `json:"materialMaxItems,omitempty"`
	MaterialMaxFileMB             int      `json:"materialMaxFileMb,omitempty"`
	MaterialMaxBatchMB            int      `json:"materialMaxBatchMb,omitempty"`
	MaterialMaxPDFPages           int      `json:"materialMaxPdfPages,omitempty"`
	MaterialConcurrency           int      `json:"materialConcurrency,omitempty"`
	MaterialRetentionDays         int      `json:"materialRetentionDays,omitempty"`
	MaterialAllowedFormats        []string `json:"materialAllowedFormats,omitempty"`
	AgentAttachmentMaxFileMB      int      `json:"agentAttachmentMaxFileMb,omitempty"`
	AgentAttachmentMaxMessageMB   int      `json:"agentAttachmentMaxMessageMb,omitempty"`
	AgentAttachmentDailyMB        int      `json:"agentAttachmentDailyMb,omitempty"`
	AgentAttachmentMaxCount       int      `json:"agentAttachmentMaxCount,omitempty"`
}

func DefaultAI() AI {
	return AI{
		AgentMaxSteps: 24, AgentTimeoutSeconds: 180, AgentMaxAnswerKB: 128, AgentToolResultKB: 32, AgentToolScanMB: 64,
		AgentDailyMessages: 50, MaterialDailyBatches: 5, MaterialActiveBatches: 3, KnowledgeMaxFilesPerClass: 1000,
		KnowledgeMaxStorageMBPerClass: 1024,
		MaterialMaxItems:              100, MaterialMaxFileMB: 50, MaterialMaxBatchMB: 256, MaterialMaxPDFPages: 64,
		MaterialConcurrency: 2, MaterialRetentionDays: 7,
		MaterialAllowedFormats:   DefaultMaterialAllowedFormats(),
		AgentAttachmentMaxFileMB: 5, AgentAttachmentMaxMessageMB: 12,
		AgentAttachmentDailyMB: 100, AgentAttachmentMaxCount: 4,
	}
}

// Lifecycle contains hot-reloadable retention and conversion policy. Paths,
// credentials and immutable safety ceilings remain deployment configuration.
type Lifecycle struct {
	BackupEnabled                    bool   `json:"backupEnabled"`
	BackupSchedule                   string `json:"backupSchedule"`
	BackupRetentionDays              int    `json:"backupRetentionDays"`
	ExportRetentionDays              int    `json:"exportRetentionDays"`
	KnowledgeDeleteGraceHours        int    `json:"knowledgeDeleteGraceHours"`
	AgentAttachmentGraceHours        int    `json:"agentAttachmentGraceHours"`
	StorageReconcileMinutes          int    `json:"storageReconcileMinutes"`
	KnowledgeMaxPDFPages             int    `json:"knowledgeMaxPdfPages"`
	KnowledgeMaxArchiveMembers       int    `json:"knowledgeMaxArchiveMembers"`
	KnowledgeMaxArchiveMB            int    `json:"knowledgeMaxArchiveMb"`
	KnowledgeMaxArchiveRatio         int    `json:"knowledgeMaxArchiveRatio"`
	KnowledgeMaxArchiveDepth         int    `json:"knowledgeMaxArchiveDepth"`
	KnowledgeMaxExtractedTextMB      int    `json:"knowledgeMaxExtractedTextMb"`
	KnowledgeConverterTimeoutSeconds int    `json:"knowledgeConverterTimeoutSeconds"`
}

func DefaultLifecycle() Lifecycle {
	return Lifecycle{
		BackupEnabled: true, BackupSchedule: "02:30", BackupRetentionDays: 30,
		ExportRetentionDays: 7, KnowledgeDeleteGraceHours: 168,
		AgentAttachmentGraceHours: 24, StorageReconcileMinutes: 60,
		KnowledgeMaxPDFPages: 64, KnowledgeMaxArchiveMembers: 500,
		KnowledgeMaxArchiveMB: 256, KnowledgeMaxArchiveRatio: 100,
		KnowledgeMaxArchiveDepth: 20, KnowledgeMaxExtractedTextMB: 32,
		KnowledgeConverterTimeoutSeconds: 60,
	}
}

func (m Mail) SESHasSettings() bool {
	return strings.TrimSpace(m.SESSecretID) != "" || strings.TrimSpace(m.SESSecretKey) != "" ||
		strings.TrimSpace(m.SESFrom) != "" || strings.TrimSpace(m.SESFromName) != "" ||
		strings.TrimSpace(m.SESReplyTo) != "" || len(m.SESTemplateIDs) > 0
}

// SESConfigured indicates that the database settings can independently send
// every repository template without borrowing any environment credential.
func (m Mail) SESConfigured() bool {
	if strings.TrimSpace(m.SESSecretID) == "" || strings.TrimSpace(m.SESSecretKey) == "" || strings.TrimSpace(m.SESFrom) == "" {
		return false
	}
	for _, name := range RequiredMailTemplates {
		if m.SESTemplateIDs[name] == 0 {
			return false
		}
	}
	return true
}

func DefaultMail() Mail {
	return Mail{Provider: "tencent_ses", PerMinute: 2, PerDay: 600, QuietStart: "22:00", QuietEnd: "07:00", SESRegion: "ap-guangzhou", SMTPPort: 587, SMTPSecurity: "starttls", AliyunRegion: "cn-hangzhou"}
}

type cachedValue struct {
	raw       json.RawMessage
	expiresAt time.Time
}

type Store struct {
	pool *pgxpool.Pool
	ttl  time.Duration
	now  func() time.Time
	load func(context.Context, string) (json.RawMessage, error)

	mu          sync.RWMutex
	cache       map[string]cachedValue
	generations map[string]uint64
	loads       singleflight.Group
}

func New(pool *pgxpool.Pool, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	store := &Store{
		pool: pool, ttl: ttl, now: time.Now,
		cache: make(map[string]cachedValue), generations: make(map[string]uint64),
	}
	if pool != nil {
		store.load = func(ctx context.Context, key string) (json.RawMessage, error) {
			var raw json.RawMessage
			err := pool.QueryRow(ctx, `SELECT value FROM ops_config WHERE key=$1`, key).Scan(&raw)
			return raw, err
		}
	}
	return store
}

func (s *Store) Flags(ctx context.Context) (Flags, error) {
	result := DefaultFlags()
	if s == nil || s.pool == nil {
		return result, nil
	}
	raw, err := s.value(ctx, "flags")
	if err != nil {
		return Flags{}, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return Flags{}, err
	}
	result.normalize()
	return result, nil
}

func (s *Store) Mail(ctx context.Context) (Mail, error) {
	result := DefaultMail()
	if s == nil || s.pool == nil {
		return result, nil
	}
	raw, err := s.value(ctx, "mail")
	if err != nil {
		return Mail{}, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return Mail{}, err
	}
	result.normalize()
	return result, nil
}

func (s *Store) AI(ctx context.Context) (AI, error) {
	result := DefaultAI()
	if s == nil || s.pool == nil {
		return result, nil
	}
	raw, err := s.value(ctx, "ai")
	if err != nil {
		return AI{}, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return AI{}, err
	}
	result.normalize()
	return result, nil
}

func (s *Store) Lifecycle(ctx context.Context) (Lifecycle, error) {
	result := DefaultLifecycle()
	if s == nil || s.pool == nil {
		return result, nil
	}
	raw, err := s.value(ctx, "lifecycle")
	if err != nil {
		return Lifecycle{}, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return Lifecycle{}, err
	}
	result.normalize()
	return result, nil
}

func (s *Store) Invalidate(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.cache, key)
	s.generations[key]++
	s.loads.Forget(key)
	s.mu.Unlock()
}

func (s *Store) value(ctx context.Context, key string) (json.RawMessage, error) {
	if s == nil || s.load == nil {
		return nil, errors.New("ops config database is unavailable")
	}
	if item, _, ok := s.cached(key); ok && s.now().Before(item.expiresAt) {
		return append(json.RawMessage(nil), item.raw...), nil
	}

	result := s.loads.DoChan(key, func() (any, error) {
		if item, _, ok := s.cached(key); ok && s.now().Before(item.expiresAt) {
			return append(json.RawMessage(nil), item.raw...), nil
		}

		_, generation, _ := s.cached(key)
		raw, err := s.load(ctx, key)
		if err != nil {
			// Keep using the last known value during a transient ops-database outage.
			if item, _, ok := s.cached(key); ok && len(item.raw) > 0 {
				return append(json.RawMessage(nil), item.raw...), nil
			}
			return nil, err
		}
		raw = append(json.RawMessage(nil), raw...)
		s.mu.Lock()
		// Invalidate may race with the database read after an operator update. Do
		// not let that older read repopulate the cache after the invalidation.
		if s.generations[key] == generation {
			s.cache[key] = cachedValue{raw: raw, expiresAt: s.now().Add(s.ttl)}
		}
		s.mu.Unlock()
		return raw, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case loaded := <-result:
		if loaded.Err != nil {
			return nil, loaded.Err
		}
		raw, ok := loaded.Val.(json.RawMessage)
		if !ok {
			return nil, errors.New("ops config cache returned an invalid value")
		}
		return append(json.RawMessage(nil), raw...), nil
	}
}

func (s *Store) cached(key string) (cachedValue, uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.cache[key]
	return item, s.generations[key], ok
}

func boundedInt(value, fallback, maximum int) int {
	if value <= 0 {
		return min(fallback, maximum)
	}
	return min(value, maximum)
}

func (f *Flags) normalize() {
	defaults := DefaultFlags()
	f.UploadMaxMB = boundedInt(f.UploadMaxMB, defaults.UploadMaxMB, 50)
	f.RequestConcurrency = boundedInt(f.RequestConcurrency, defaults.RequestConcurrency, 512)
	f.ExportConcurrency = boundedInt(f.ExportConcurrency, defaults.ExportConcurrency, 8)
	f.PasswordHashConcurrency = boundedInt(f.PasswordHashConcurrency, defaults.PasswordHashConcurrency, 16)
	f.APIRateLimitPerMinute = boundedInt(f.APIRateLimitPerMinute, defaults.APIRateLimitPerMinute, 100000)
	f.APIRateLimitBurst = boundedInt(f.APIRateLimitBurst, defaults.APIRateLimitBurst, f.APIRateLimitPerMinute)
	f.AuthLoginPerMinute = boundedInt(f.AuthLoginPerMinute, defaults.AuthLoginPerMinute, 120)
	f.AuthRefreshPerMinute = boundedInt(f.AuthRefreshPerMinute, defaults.AuthRefreshPerMinute, 600)
	f.AuthRegisterPerHour = boundedInt(f.AuthRegisterPerHour, defaults.AuthRegisterPerHour, 1000)
	f.AuthForgotPerHour = boundedInt(f.AuthForgotPerHour, defaults.AuthForgotPerHour, 120)
	f.AuthResetPerHour = boundedInt(f.AuthResetPerHour, defaults.AuthResetPerHour, 120)
	f.EvidencePresignPerHour = boundedInt(f.EvidencePresignPerHour, defaults.EvidencePresignPerHour, 5000)
	f.EvidenceDailyMB = boundedInt(f.EvidenceDailyMB, defaults.EvidenceDailyMB, 10000)
	f.AIPresignPerHour = boundedInt(f.AIPresignPerHour, defaults.AIPresignPerHour, 5000)
	f.AgentPresignPerHour = boundedInt(f.AgentPresignPerHour, defaults.AgentPresignPerHour, 5000)
	f.KnowledgePresignPerHour = boundedInt(f.KnowledgePresignPerHour, defaults.KnowledgePresignPerHour, 5000)
	f.AIBatchActionsPerHour = boundedInt(f.AIBatchActionsPerHour, defaults.AIBatchActionsPerHour, 500)
	f.AgentMessagesPerMinute = boundedInt(f.AgentMessagesPerMinute, defaults.AgentMessagesPerMinute, 600)
	f.ExportRequestsPerHour = boundedInt(f.ExportRequestsPerHour, defaults.ExportRequestsPerHour, 500)
	f.KnowledgeReprocessPerHour = boundedInt(f.KnowledgeReprocessPerHour, defaults.KnowledgeReprocessPerHour, 500)
	formats, err := NormalizeEvidenceAllowedFormats(f.EvidenceAllowedFormats)
	if err != nil {
		f.EvidenceAllowedFormats = defaults.EvidenceAllowedFormats
	} else {
		f.EvidenceAllowedFormats = formats
	}
}

func (m *Mail) normalize() {
	defaults := DefaultMail()
	if m.Provider == "" {
		m.Provider = defaults.Provider
	}
	if m.PerMinute <= 0 {
		m.PerMinute = defaults.PerMinute
	}
	if m.PerDay <= 0 {
		m.PerDay = defaults.PerDay
	}
	if m.QuietStart == "" {
		m.QuietStart = defaults.QuietStart
	}
	if m.QuietEnd == "" {
		m.QuietEnd = defaults.QuietEnd
	}
	if m.SESRegion == "" {
		m.SESRegion = defaults.SESRegion
	}
	if m.SESTemplateIDs == nil {
		m.SESTemplateIDs = make(map[string]uint64)
	}
	if m.SMTPPort == 0 {
		m.SMTPPort = defaults.SMTPPort
	}
	if m.SMTPSecurity == "" {
		m.SMTPSecurity = defaults.SMTPSecurity
	}
	if m.AliyunRegion == "" {
		m.AliyunRegion = defaults.AliyunRegion
	}
}

func (a *AI) normalize() {
	defaults := DefaultAI()
	if a.AgentMaxSteps <= 0 {
		a.AgentMaxSteps = defaults.AgentMaxSteps
	}
	if a.AgentTimeoutSeconds <= 0 {
		a.AgentTimeoutSeconds = defaults.AgentTimeoutSeconds
	}
	a.AgentMaxAnswerKB = boundedInt(a.AgentMaxAnswerKB, defaults.AgentMaxAnswerKB, 512)
	if a.AgentToolResultKB <= 0 {
		a.AgentToolResultKB = defaults.AgentToolResultKB
	}
	a.AgentToolScanMB = boundedInt(a.AgentToolScanMB, defaults.AgentToolScanMB, 256)
	if a.AgentDailyMessages <= 0 {
		a.AgentDailyMessages = defaults.AgentDailyMessages
	}
	a.MaterialDailyBatches = boundedInt(a.MaterialDailyBatches, defaults.MaterialDailyBatches, 100)
	a.MaterialActiveBatches = boundedInt(a.MaterialActiveBatches, defaults.MaterialActiveBatches, 20)
	if a.KnowledgeMaxFilesPerClass <= 0 {
		a.KnowledgeMaxFilesPerClass = defaults.KnowledgeMaxFilesPerClass
	}
	if a.KnowledgeMaxStorageMBPerClass <= 0 {
		a.KnowledgeMaxStorageMBPerClass = defaults.KnowledgeMaxStorageMBPerClass
	}
	if a.MaterialMaxItems <= 0 {
		a.MaterialMaxItems = defaults.MaterialMaxItems
	}
	if a.MaterialMaxFileMB <= 0 {
		a.MaterialMaxFileMB = defaults.MaterialMaxFileMB
	}
	a.MaterialMaxBatchMB = boundedInt(a.MaterialMaxBatchMB, defaults.MaterialMaxBatchMB, 2048)
	if a.MaterialMaxPDFPages <= 0 {
		a.MaterialMaxPDFPages = defaults.MaterialMaxPDFPages
	}
	if a.MaterialConcurrency <= 0 {
		a.MaterialConcurrency = defaults.MaterialConcurrency
	}
	if a.MaterialRetentionDays <= 0 {
		a.MaterialRetentionDays = defaults.MaterialRetentionDays
	}
	if a.AgentAttachmentMaxFileMB <= 0 {
		a.AgentAttachmentMaxFileMB = defaults.AgentAttachmentMaxFileMB
	}
	if a.AgentAttachmentMaxMessageMB <= 0 {
		a.AgentAttachmentMaxMessageMB = defaults.AgentAttachmentMaxMessageMB
	}
	if a.AgentAttachmentDailyMB <= 0 {
		a.AgentAttachmentDailyMB = defaults.AgentAttachmentDailyMB
	}
	if a.AgentAttachmentMaxCount <= 0 {
		a.AgentAttachmentMaxCount = defaults.AgentAttachmentMaxCount
	}
	formats, err := NormalizeMaterialAllowedFormats(a.MaterialAllowedFormats)
	if err != nil {
		a.MaterialAllowedFormats = defaults.MaterialAllowedFormats
	} else {
		a.MaterialAllowedFormats = formats
	}
}

func (l *Lifecycle) normalize() {
	defaults := DefaultLifecycle()
	if _, err := time.Parse("15:04", l.BackupSchedule); err != nil {
		l.BackupSchedule = defaults.BackupSchedule
	}
	if l.BackupRetentionDays <= 0 {
		l.BackupRetentionDays = defaults.BackupRetentionDays
	}
	if l.ExportRetentionDays <= 0 {
		l.ExportRetentionDays = defaults.ExportRetentionDays
	}
	if l.KnowledgeDeleteGraceHours <= 0 {
		l.KnowledgeDeleteGraceHours = defaults.KnowledgeDeleteGraceHours
	}
	if l.AgentAttachmentGraceHours <= 0 {
		l.AgentAttachmentGraceHours = defaults.AgentAttachmentGraceHours
	}
	if l.StorageReconcileMinutes <= 0 {
		l.StorageReconcileMinutes = defaults.StorageReconcileMinutes
	}
	if l.KnowledgeMaxPDFPages <= 0 {
		l.KnowledgeMaxPDFPages = defaults.KnowledgeMaxPDFPages
	}
	l.KnowledgeMaxPDFPages = boundedInt(l.KnowledgeMaxPDFPages, defaults.KnowledgeMaxPDFPages, 256)
	l.KnowledgeMaxArchiveMembers = boundedInt(l.KnowledgeMaxArchiveMembers, defaults.KnowledgeMaxArchiveMembers, 2000)
	l.KnowledgeMaxArchiveMB = boundedInt(l.KnowledgeMaxArchiveMB, defaults.KnowledgeMaxArchiveMB, 512)
	l.KnowledgeMaxArchiveRatio = boundedInt(l.KnowledgeMaxArchiveRatio, defaults.KnowledgeMaxArchiveRatio, 200)
	l.KnowledgeMaxArchiveDepth = boundedInt(l.KnowledgeMaxArchiveDepth, defaults.KnowledgeMaxArchiveDepth, 32)
	l.KnowledgeMaxExtractedTextMB = boundedInt(l.KnowledgeMaxExtractedTextMB, defaults.KnowledgeMaxExtractedTextMB, 64)
	l.KnowledgeConverterTimeoutSeconds = boundedInt(l.KnowledgeConverterTimeoutSeconds, defaults.KnowledgeConverterTimeoutSeconds, 300)
}
