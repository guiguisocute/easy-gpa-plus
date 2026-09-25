package api

import (
	"encoding/json"
	"strings"
	"testing"

	"easygpa/backend/internal/opsconfig"
)

func testBackupRemoteCipher(t *testing.T) *opsconfig.Cipher {
	t.Helper()
	cipher, err := opsconfig.NewNamedCipher(strings.Repeat("b", 32), "BACKUP_REMOTE_SECRET_KEY")
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

// 桶凭据是"写得进、读不出"。这条断言的是序列化之后的字节里也搜不到——
// 光看结构体字段被清空不够，omitempty 掉一个但另一个漏下就是泄露。
func TestRedactBackupRemoteNeverSerialisesCredentials(t *testing.T) {
	cipher := testBackupRemoteCipher(t)
	sealedAccess, err := cipher.Seal("AKIDsecretaccess")
	if err != nil {
		t.Fatal(err)
	}
	sealedSecret, err := cipher.Seal("supersecretvalue")
	if err != nil {
		t.Fatal(err)
	}
	config := opsconfig.BackupRemote{
		Enabled: true, Endpoint: "cos.ap-guangzhou.myqcloud.com", Bucket: "easygpa",
		AccessKey: sealedAccess, SecretKey: sealedSecret, RetentionDays: 90,
	}
	redacted, accessKeySet, secretKeySet := redactBackupRemote(config)
	if !accessKeySet || !secretKeySet {
		t.Fatal("已保存的凭据应当报告为已设置")
	}
	raw, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, forbidden := range []string{"AKIDsecretaccess", "supersecretvalue", sealedAccess, sealedSecret, "enc:v1"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("下发的配置里出现了不该出现的内容 %q：%s", forbidden, body)
		}
	}
}

func TestSealBackupRemoteSecretsHandlesAbsentEmptyAndNewValues(t *testing.T) {
	cipher := testBackupRemoteCipher(t)
	existing, err := cipher.Seal("old-access")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("没传这个键就是不改动", func(t *testing.T) {
		target := opsconfig.BackupRemote{AccessKey: existing}
		if err := sealBackupRemoteSecrets(&target, backupRemoteInput{}, cipher); err != nil {
			t.Fatal(err)
		}
		if target.AccessKey != existing {
			t.Fatal("没提交的凭据被改动了")
		}
	})

	t.Run("传空串是显式清空", func(t *testing.T) {
		target := opsconfig.BackupRemote{AccessKey: existing}
		input := backupRemoteInput{AccessKey: new("  ")}
		if err := sealBackupRemoteSecrets(&target, input, cipher); err != nil {
			t.Fatal(err)
		}
		if target.AccessKey != "" {
			t.Fatalf("空串应当清空凭据，得到 %q", target.AccessKey)
		}
	})

	t.Run("传了值就加密后覆盖", func(t *testing.T) {
		target := opsconfig.BackupRemote{}
		input := backupRemoteInput{SecretKey: new("brand-new-secret")}
		if err := sealBackupRemoteSecrets(&target, input, cipher); err != nil {
			t.Fatal(err)
		}
		if !opsconfig.IsSealed(target.SecretKey) {
			t.Fatalf("凭据没有被加密：%q", target.SecretKey)
		}
		plain, err := cipher.Open(target.SecretKey)
		if err != nil || plain != "brand-new-secret" {
			t.Fatalf("密文解不回原值：%q %v", plain, err)
		}
	})
}

// 没配 BACKUP_REMOTE_SECRET_KEY 时必须明确拒绝，而不是悄悄降级存明文。
func TestSealBackupRemoteSecretsRefusesWithoutCipher(t *testing.T) {
	target := opsconfig.BackupRemote{}
	input := backupRemoteInput{AccessKey: new("would-be-plaintext")}
	err := sealBackupRemoteSecrets(&target, input, nil)
	if err == nil {
		t.Fatal("没有加密密钥时保存凭据必须失败")
	}
	if !strings.Contains(err.Error(), "BACKUP_REMOTE_SECRET_KEY") {
		t.Fatalf("错误信息要点明缺哪个环境变量，得到：%v", err)
	}
	if target.AccessKey != "" {
		t.Fatalf("拒绝之后不能留下任何明文：%q", target.AccessKey)
	}
}

func TestApplyBackupRemoteInputOnlyTouchesSubmittedFields(t *testing.T) {
	stored := opsconfig.BackupRemote{
		Enabled: true, Endpoint: "cos.ap-guangzhou.myqcloud.com", Bucket: "easygpa",
		Region: "ap-guangzhou", UseSSL: true, RetentionDays: 90,
	}
	retention := 30
	next, changed := applyBackupRemoteInput(stored, backupRemoteInput{RetentionDays: &retention})
	if next.RetentionDays != 30 {
		t.Fatalf("retentionDays = %d, want 30", next.RetentionDays)
	}
	if next.Endpoint != stored.Endpoint || next.Bucket != stored.Bucket || !next.Enabled {
		t.Fatal("没提交的字段被改动了")
	}
	if len(changed) != 1 || changed[0] != "retentionDays" {
		t.Fatalf("changed = %v，只应当记 retentionDays", changed)
	}

	_, none := applyBackupRemoteInput(stored, backupRemoteInput{})
	if len(none) != 0 {
		t.Fatalf("空提交不该记任何改动：%v", none)
	}
}

func TestValidateBackupRemoteUpdateChecksWholeResult(t *testing.T) {
	// 用回环地址配 allowLoopback，让这组用例完全不碰 DNS：CI 上解析一个真域名
	// 又慢又会因为网络抖动变成假失败，而这里要验的是校验顺序，不是域名存不存在。
	valid := opsconfig.BackupRemote{
		Endpoint: "127.0.0.1:9000", Bucket: "easygpa", UseSSL: false, RetentionDays: 90,
	}
	// 四项都没填时允许存着，比如只想先改保留期。
	if err := validateBackupRemoteUpdate(opsconfig.BackupRemote{RetentionDays: 30}, false); err != nil {
		t.Fatalf("空配置只改保留期应当放行：%v", err)
	}
	if err := validateBackupRemoteUpdate(valid, true); err != nil {
		t.Fatalf("合法配置被拒绝：%v", err)
	}
	// 同一份配置在生产口径下必须被拒：本机地址和明文 HTTP 都不允许。
	if err := validateBackupRemoteUpdate(valid, false); err == nil {
		t.Fatal("生产口径下明文 HTTP 的本机桶地址应当被拒绝")
	}
	for name, mutate := range map[string]func(*opsconfig.BackupRemote){
		"保留期超范围":     func(v *opsconfig.BackupRemote) { v.RetentionDays = 4000 },
		"前缀带空格":      func(v *opsconfig.BackupRemote) { v.Prefix = "back ups/" },
		"桶名带大写":      func(v *opsconfig.BackupRemote) { v.Bucket = "EasyGPA Plus" },
		"地址带 scheme": func(v *opsconfig.BackupRemote) { v.Endpoint = "https://127.0.0.1:9000" },
	} {
		t.Run(name, func(t *testing.T) {
			broken := valid
			mutate(&broken)
			if err := validateBackupRemoteUpdate(broken, true); err == nil {
				t.Fatalf("%s 应当被拒绝", name)
			}
		})
	}
}
