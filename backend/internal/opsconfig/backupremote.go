package opsconfig

// 远程备份仓库：把每天的备份推到机房之外的一个 S3 协议桶（腾讯 COS、阿里 OSS、
// Cloudflare R2、AWS S3 都算）。桶的 AccessKey/SecretKey 和邮件那套一样以
// enc:v1 密文落库，任何接口都不回显。
//
// 运维在网页上粘的是一整条桶链接，ParseBucketURL 把它拆成 endpoint/bucket/
// region/路径风格四项填进表单——**拆出来的只是草稿**，最终以表单里那四个字段
// 为准。各家云的域名规则会变，猜错了要能当场改，而不是卡在解析器上。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"easygpa/backend/internal/netguard"
)

type BackupRemote struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Bucket   string `json:"bucket"`
	Region   string `json:"region"`
	Prefix   string `json:"prefix"`
	UseSSL   bool   `json:"useSsl"`
	// PathStyle 为 true 时走 endpoint/bucket/key，false 时走 bucket.endpoint/key。
	// 腾讯 COS 与阿里 OSS 用后者，R2 和自建 MinIO 用前者。
	PathStyle bool `json:"pathStyle"`
	// AccessKey/SecretKey 是"写得进、读不出"：加密后落库，任何接口都不回显。
	AccessKey     string `json:"accessKey,omitempty"`
	SecretKey     string `json:"secretKey,omitempty"`
	RetentionDays int    `json:"retentionDays"`
}

func DefaultBackupRemote() BackupRemote {
	// 远程副本的意义就是留得比本地久，所以默认 90 天而不是跟着本地的 30 天。
	return BackupRemote{UseSSL: true, RetentionDays: 90}
}

// Configured 表示这套参数够不够真的推一次备份。Enabled 只是运维的意图。
func (b BackupRemote) Configured() bool {
	return strings.TrimSpace(b.Endpoint) != "" && strings.TrimSpace(b.Bucket) != "" &&
		strings.TrimSpace(b.AccessKey) != "" && strings.TrimSpace(b.SecretKey) != ""
}

func (b *BackupRemote) normalize() {
	defaults := DefaultBackupRemote()
	b.Endpoint = strings.TrimSpace(b.Endpoint)
	b.Bucket = strings.TrimSpace(b.Bucket)
	b.Region = strings.TrimSpace(b.Region)
	b.Prefix = normalizeBackupPrefix(b.Prefix)
	if b.RetentionDays <= 0 {
		b.RetentionDays = defaults.RetentionDays
	}
	b.RetentionDays = boundedInt(b.RetentionDays, defaults.RetentionDays, 3650)
}

// normalizeBackupPrefix 统一成 "" 或者 "xxx/"。前缀参与拼 key，留一个没有斜杠
// 结尾的值会把两级目录粘成一个名字。
func normalizeBackupPrefix(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	return value + "/"
}

func (s *Store) BackupRemote(ctx context.Context) (BackupRemote, error) {
	result := DefaultBackupRemote()
	if s == nil || s.pool == nil {
		return result, nil
	}
	raw, err := s.value(ctx, "backup_remote")
	if err != nil {
		return BackupRemote{}, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return BackupRemote{}, err
	}
	result.normalize()
	return result, nil
}

// ValidateBackupRemote 校验那四个连接字段与保留期。凭据不在这里管——它们在
// API 层加密，到这一步已经是密文。
func ValidateBackupRemote(value BackupRemote, allowLoopback bool) error {
	if err := ValidateBucketEndpoint(value.Endpoint, value.UseSSL, allowLoopback); err != nil {
		return err
	}
	if err := ValidateBucketName(value.Bucket); err != nil {
		return err
	}
	if normalizeBackupPrefix(value.Prefix) != "" && !backupPrefixAllowed(value.Prefix) {
		return errors.New("对象前缀只能包含字母、数字、下划线、短横线和斜杠")
	}
	if value.RetentionDays < 1 || value.RetentionDays > 3650 {
		return errors.New("远程保留天数必须在 1—3650 之间")
	}
	return nil
}

func backupPrefixAllowed(value string) bool {
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '_' || character == '-' || character == '/':
		default:
			return false
		}
	}
	return true
}

func ValidateBucketName(value string) error {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 63 {
		return errors.New("桶名长度必须在 3—63 之间")
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '-' || character == '.':
		default:
			return errors.New("桶名只能包含小写字母、数字、短横线和点")
		}
	}
	return nil
}

// ValidateBucketEndpoint 拦住指向内网的地址。运维填的是一个任意主机名，
// 不挡的话这就是一个从生产内网发起请求的入口（元数据服务、数据库、Garage
// 本身都在那张网上）。
//
// allowLoopback 只在开发环境为 true，用来对着本机的 MinIO/Garage 试。
func ValidateBucketEndpoint(endpoint string, useSSL, allowLoopback bool) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("桶地址未填写")
	}
	if strings.Contains(endpoint, "://") {
		return errors.New("桶地址只填主机名，不要带 http:// 或 https://")
	}
	host := endpoint
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		host = h
	}
	if host == "" {
		return errors.New("桶地址格式不正确")
	}
	if allowLoopback && isLoopbackHost(host) {
		return nil
	}
	if !useSSL {
		return errors.New("远程备份桶必须使用 HTTPS")
	}
	addresses, err := netguard.BackupAddresses(context.Background(), host)
	if err != nil {
		return fmt.Errorf("桶地址解析失败：%s", host)
	}
	for _, address := range addresses {
		if netguard.Blocked(address.IP) {
			return fmt.Errorf("桶地址解析到内网地址 %s，不允许作为远程备份目标", address)
		}
	}
	return nil
}

// ParseBucketURL 把运维粘的桶链接拆成表单的四个字段。
//
// 只有两条规则，不按云厂商打表：
//   - 链接里还有路径段  → 路径风格，主机是 endpoint，第一段是桶名
//     （https://s3.us-west-2.amazonaws.com/easygpa、R2、自建 MinIO）
//   - 路径为空          → 虚拟主机风格，第一个 DNS 标签是桶名，其余是 endpoint
//     （https://easygpa-125xxx.cos.ap-guangzhou.myqcloud.com、阿里 OSS）
func ParseBucketURL(raw string) (BackupRemote, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return BackupRemote{}, errors.New("桶链接未填写")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return BackupRemote{}, errors.New("桶链接格式不正确")
	}
	if parsed.User != nil {
		return BackupRemote{}, errors.New("桶链接里不要带账号密码，凭据请填在下面的两个框里")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return BackupRemote{}, errors.New("桶链接不允许带查询参数或片段")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return BackupRemote{}, errors.New("桶链接必须是 http 或 https")
	}

	result := BackupRemote{UseSSL: scheme == "https", RetentionDays: DefaultBackupRemote().RetentionDays}
	host := parsed.Host
	segments := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	if len(segments) > 0 {
		result.PathStyle = true
		result.Endpoint = host
		result.Bucket = segments[0]
		result.Prefix = normalizeBackupPrefix(strings.Join(segments[1:], "/"))
	} else {
		label, rest, found := strings.Cut(host, ".")
		if !found || rest == "" {
			return BackupRemote{}, errors.New("从链接里认不出桶名，请改用 主机名/桶名 的写法")
		}
		result.Bucket = label
		result.Endpoint = rest
	}
	if result.Bucket == "" {
		return BackupRemote{}, errors.New("从链接里认不出桶名")
	}
	result.Region = guessBucketRegion(result.Endpoint)
	return result, nil
}

// guessBucketRegion 只覆盖几家常见云的域名写法，认不出就留空让人自己填。
// region 参与 SigV4 签名，填错了报的错通常是 403，很难反推，所以宁可留空。
func guessBucketRegion(endpoint string) string {
	host := endpoint
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	labels := strings.Split(host, ".")
	switch {
	case strings.HasSuffix(host, ".r2.cloudflarestorage.com"):
		return "auto"
	case len(labels) > 1 && labels[0] == "cos":
		// cos.ap-guangzhou.myqcloud.com
		return labels[1]
	case len(labels) > 0 && strings.HasPrefix(labels[0], "oss-"):
		// oss-cn-hangzhou.aliyuncs.com：阿里的 region 就是这一整段
		return labels[0]
	case len(labels) > 1 && labels[0] == "s3" && labels[1] != "amazonaws":
		// s3.us-west-2.amazonaws.com
		return labels[1]
	case host == "s3.amazonaws.com":
		return "us-east-1"
	default:
		return ""
	}
}

// BackupRemoteRuntime 是解密后的可用参数。它只在 Worker 和运维接口内部存在，
// 不进任何 JSON 响应。
type BackupRemoteRuntime struct {
	AllowLoopback bool
	Endpoint      string
	Bucket        string
	Region        string
	Prefix        string
	UseSSL        bool
	PathStyle     bool
	AccessKey     string
	SecretKey     string
	RetentionDays int
}

// ResolveBackupRemote 解开两个凭据字段。凭据只接受 enc:v1 密文——读到裸值说明
// 有人绕过接口直接改了库，那时候宁可拒绝也不要拿它去连外网。
func ResolveBackupRemote(stored BackupRemote, cipher *Cipher) (BackupRemoteRuntime, error) {
	stored.normalize()
	runtime := BackupRemoteRuntime{
		Endpoint: stored.Endpoint, Bucket: stored.Bucket, Region: stored.Region,
		Prefix: stored.Prefix, UseSSL: stored.UseSSL, PathStyle: stored.PathStyle,
		RetentionDays: stored.RetentionDays,
	}
	for _, item := range []struct {
		sealed string
		target *string
		name   string
	}{
		{stored.AccessKey, &runtime.AccessKey, "AccessKey"},
		{stored.SecretKey, &runtime.SecretKey, "SecretKey"},
	} {
		value := strings.TrimSpace(item.sealed)
		if value == "" {
			return BackupRemoteRuntime{}, fmt.Errorf("远程备份桶的 %s 未配置", item.name)
		}
		if !IsSealed(value) {
			return BackupRemoteRuntime{}, fmt.Errorf("远程备份桶的 %s 不是本系统加密的格式，拒绝使用", item.name)
		}
		plain, err := cipher.Open(value)
		if err != nil {
			return BackupRemoteRuntime{}, fmt.Errorf("远程备份桶的 %s：%w", item.name, err)
		}
		*item.target = plain
	}
	return runtime, nil
}
