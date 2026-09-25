package opsconfig

import (
	"strings"
	"testing"
)

func TestParseBucketURLRecognisesCommonClouds(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		endpoint  string
		bucket    string
		region    string
		prefix    string
		pathStyle bool
	}{
		{
			name: "腾讯 COS 虚拟主机式", raw: "https://easygpa-1250000000.cos.ap-guangzhou.myqcloud.com",
			endpoint: "cos.ap-guangzhou.myqcloud.com", bucket: "easygpa-1250000000", region: "ap-guangzhou",
		},
		{
			name: "阿里 OSS 虚拟主机式", raw: "https://easygpa.oss-cn-hangzhou.aliyuncs.com",
			endpoint: "oss-cn-hangzhou.aliyuncs.com", bucket: "easygpa", region: "oss-cn-hangzhou",
		},
		{
			name: "Cloudflare R2 路径式", raw: "https://abc123.r2.cloudflarestorage.com/easygpa",
			endpoint: "abc123.r2.cloudflarestorage.com", bucket: "easygpa", region: "auto", pathStyle: true,
		},
		{
			name: "AWS S3 路径式带前缀", raw: "https://s3.us-west-2.amazonaws.com/easygpa/backups/nightly",
			endpoint: "s3.us-west-2.amazonaws.com", bucket: "easygpa", region: "us-west-2",
			prefix: "backups/nightly/", pathStyle: true,
		},
		{
			name: "没写 scheme 时按 https 补全", raw: "easygpa.oss-cn-hangzhou.aliyuncs.com",
			endpoint: "oss-cn-hangzhou.aliyuncs.com", bucket: "easygpa", region: "oss-cn-hangzhou",
		},
		{
			name: "认不出地域时留空由人填", raw: "https://storage.example.com/easygpa",
			endpoint: "storage.example.com", bucket: "easygpa", pathStyle: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseBucketURL(testCase.raw)
			if err != nil {
				t.Fatalf("ParseBucketURL(%q) 报错：%v", testCase.raw, err)
			}
			if got.Endpoint != testCase.endpoint {
				t.Errorf("endpoint = %q, want %q", got.Endpoint, testCase.endpoint)
			}
			if got.Bucket != testCase.bucket {
				t.Errorf("bucket = %q, want %q", got.Bucket, testCase.bucket)
			}
			if got.Region != testCase.region {
				t.Errorf("region = %q, want %q", got.Region, testCase.region)
			}
			if got.Prefix != testCase.prefix {
				t.Errorf("prefix = %q, want %q", got.Prefix, testCase.prefix)
			}
			if got.PathStyle != testCase.pathStyle {
				t.Errorf("pathStyle = %v, want %v", got.PathStyle, testCase.pathStyle)
			}
			if !got.UseSSL {
				t.Error("https 链接解析出来应当 UseSSL = true")
			}
		})
	}
}

func TestParseBucketURLRejectsUnusableInput(t *testing.T) {
	cases := map[string]string{
		"空串":         "",
		"带账号密码":      "https://key:secret@cos.ap-guangzhou.myqcloud.com/easygpa",
		"带查询参数":      "https://cos.ap-guangzhou.myqcloud.com/easygpa?x=1",
		"带片段":        "https://cos.ap-guangzhou.myqcloud.com/easygpa#frag",
		"不是 http 协议": "s3://easygpa/backups",
		"单标签主机认不出桶名": "https://localhost",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBucketURL(raw); err == nil {
				t.Fatalf("ParseBucketURL(%q) 应当拒绝", raw)
			}
		})
	}
}

// http:// 的桶链接能解析出来（本机 MinIO 要用），但存的时候会被
// ValidateBucketEndpoint 挡住——解析和准入是两道关。
func TestParseBucketURLKeepsPlainHTTPForValidationToReject(t *testing.T) {
	got, err := ParseBucketURL("http://127.0.0.1:9000/easygpa")
	if err != nil {
		t.Fatal(err)
	}
	if got.UseSSL {
		t.Fatal("http 链接不该被当成 UseSSL")
	}
	if err := ValidateBucketEndpoint(got.Endpoint, got.UseSSL, false); err == nil {
		t.Fatal("生产口径下必须拒绝明文 HTTP 的桶地址")
	}
	if err := ValidateBucketEndpoint(got.Endpoint, got.UseSSL, true); err != nil {
		t.Fatalf("开发口径下本机地址应当放行：%v", err)
	}
}

func TestValidateBucketEndpointRejectsSchemeAndEmpty(t *testing.T) {
	if err := ValidateBucketEndpoint("", true, false); err == nil {
		t.Fatal("空地址应当被拒绝")
	}
	err := ValidateBucketEndpoint("https://cos.ap-guangzhou.myqcloud.com", true, false)
	if err == nil || !strings.Contains(err.Error(), "只填主机名") {
		t.Fatalf("带 scheme 的地址应当被明确拒绝，得到：%v", err)
	}
}

func TestValidateBucketNameBounds(t *testing.T) {
	if err := ValidateBucketName("ok-bucket-1"); err != nil {
		t.Fatalf("合法桶名被拒绝：%v", err)
	}
	for _, name := range []string{"ab", strings.Repeat("a", 64), "Has-Upper", "under_score"} {
		if err := ValidateBucketName(name); err == nil {
			t.Errorf("桶名 %q 应当被拒绝", name)
		}
	}
}

func TestBackupRemoteNormalizePrefixAndRetention(t *testing.T) {
	value := BackupRemote{Prefix: "/backups/nightly/", RetentionDays: 0}
	value.normalize()
	if value.Prefix != "backups/nightly/" {
		t.Fatalf("prefix = %q, want %q", value.Prefix, "backups/nightly/")
	}
	if value.RetentionDays != DefaultBackupRemote().RetentionDays {
		t.Fatalf("保留期为 0 时应当回落到默认值，得到 %d", value.RetentionDays)
	}
	empty := BackupRemote{Prefix: "  /  "}
	empty.normalize()
	if empty.Prefix != "" {
		t.Fatalf("只有斜杠的前缀应当归一成空串，得到 %q", empty.Prefix)
	}
}

func TestBackupRemoteConfiguredNeedsAllFour(t *testing.T) {
	full := BackupRemote{Endpoint: "cos.ap-guangzhou.myqcloud.com", Bucket: "easygpa", AccessKey: "a", SecretKey: "b"}
	if !full.Configured() {
		t.Fatal("四项齐全时应当算配好了")
	}
	for _, missing := range []BackupRemote{
		{Bucket: "easygpa", AccessKey: "a", SecretKey: "b"},
		{Endpoint: "e", AccessKey: "a", SecretKey: "b"},
		{Endpoint: "e", Bucket: "easygpa", SecretKey: "b"},
		{Endpoint: "e", Bucket: "easygpa", AccessKey: "a"},
	} {
		if missing.Configured() {
			t.Errorf("缺一项时不该算配好了：%+v", missing)
		}
	}
}

func TestResolveBackupRemoteRefusesPlaintextCredentials(t *testing.T) {
	cipher, err := NewNamedCipher(strings.Repeat("k", 32), "BACKUP_REMOTE_SECRET_KEY")
	if err != nil {
		t.Fatal(err)
	}
	sealedAccess, err := cipher.Seal("AKIDexample")
	if err != nil {
		t.Fatal(err)
	}
	sealedSecret, err := cipher.Seal("secret-value")
	if err != nil {
		t.Fatal(err)
	}
	stored := BackupRemote{
		Endpoint: "cos.ap-guangzhou.myqcloud.com", Bucket: "easygpa",
		AccessKey: sealedAccess, SecretKey: sealedSecret, RetentionDays: 90,
	}
	runtime, err := ResolveBackupRemote(stored, cipher)
	if err != nil {
		t.Fatalf("ResolveBackupRemote 报错：%v", err)
	}
	if runtime.AccessKey != "AKIDexample" || runtime.SecretKey != "secret-value" {
		t.Fatal("凭据没有正确解密")
	}

	// 有人绕过接口直接把裸值写进库时，宁可拒绝也不要拿它去连外网。
	stored.SecretKey = "plaintext-secret"
	if _, err := ResolveBackupRemote(stored, cipher); err == nil {
		t.Fatal("明文凭据必须被拒绝")
	}
}
