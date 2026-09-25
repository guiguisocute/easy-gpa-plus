package backupjob

// 对着一个真的 S3 兼容服务端跑完整条远程副本路径。默认跳过，和
// objectstore 的集成测试一个规矩：
//
//	EASYGPA_INTEGRATION=1 REMOTE_ENDPOINT=127.0.0.1:9000 REMOTE_BUCKET=easygpa-remote \
//	REMOTE_ACCESS_KEY=... REMOTE_SECRET_KEY=... go test ./internal/backupjob -run Integration -v
//
// 这条覆盖的是单元测试里那个内存假实现证明不了的部分：长度未知的流式上传、
// 分片参数、范围读、前缀列举与删除，在真实服务端上到底成不成立。

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"easygpa/backend/internal/opsconfig"
)

func TestRemoteBucketIntegration(t *testing.T) {
	if os.Getenv("EASYGPA_INTEGRATION") != "1" {
		t.Skip("set EASYGPA_INTEGRATION=1 to run against a real S3-compatible bucket")
	}
	runtime := opsconfig.BackupRemoteRuntime{
		AllowLoopback: true,
		Endpoint:      os.Getenv("REMOTE_ENDPOINT"),
		Bucket:        os.Getenv("REMOTE_BUCKET"),
		Region:        os.Getenv("REMOTE_REGION"),
		Prefix:        "integration/" + randomHex(16) + "/",
		UseSSL:        os.Getenv("REMOTE_USE_SSL") == "true",
		AccessKey:     os.Getenv("REMOTE_ACCESS_KEY"),
		SecretKey:     os.Getenv("REMOTE_SECRET_KEY"),
		// 自建 MinIO 和 R2 都只认路径式；腾讯 COS、阿里 OSS 要把这一项设成 false。
		PathStyle: os.Getenv("REMOTE_PATH_STYLE") != "false",
	}
	store, err := NewRemoteStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	t.Run("连通性自检三步都过", func(t *testing.T) {
		probe, err := ProbeRemote(ctx, store, runtime.Prefix)
		if err != nil {
			t.Fatalf("ProbeRemote: %v (%+v)", err, probe)
		}
		if !probe.Wrote || !probe.Read || !probe.Removed {
			t.Fatalf("三步应当全过：%+v", probe)
		}
	})

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sourceID := "44444444-4444-4444-4444-444444444444"
	directory := remoteDirectory(runtime.Prefix, sourceID)
	archiveKey := directory + "archive.tar.gz.age"
	source := writeSampleBackup(t)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		items, err := store.List(cleanup, runtime.Prefix)
		if err != nil {
			return
		}
		for _, item := range items {
			_ = store.Remove(cleanup, item.Key)
		}
	})

	var digest archiveDigest
	t.Run("流式上传后回读校验通过", func(t *testing.T) {
		digest, err = uploadArchive(ctx, store, archiveKey, source, identity.Recipient())
		if err != nil {
			t.Fatalf("uploadArchive: %v", err)
		}
		if err := verifyRemoteArchive(ctx, store, archiveKey, digest); err != nil {
			t.Fatalf("verifyRemoteArchive: %v", err)
		}
	})

	t.Run("从桶里下回来能解密解包", func(t *testing.T) {
		data, err := store.Range(ctx, archiveKey, 0, digest.Bytes)
		if err != nil {
			t.Fatalf("下载归档：%v", err)
		}
		decrypted, err := age.Decrypt(bytes.NewReader(data), identity)
		if err != nil {
			t.Fatalf("解密：%v", err)
		}
		uncompressed, err := gzip.NewReader(decrypted)
		if err != nil {
			t.Fatalf("解压：%v", err)
		}
		plain, err := io.ReadAll(uncompressed)
		if err != nil {
			t.Fatalf("读归档：%v", err)
		}
		// tar 里文件名是明文存的，够证明这确实是那份备份。
		for _, name := range []string{"database.dump", "objects/class-1/proof.pdf"} {
			if !strings.Contains(string(plain), name) {
				t.Errorf("解出来的归档里没有 %s", name)
			}
		}
	})

	// 上面那份样本只有几百 KB，走的是单片路径。真备份是几个 G，必然分片，
	// 而"长度未知的流 + 显式 PartSize + 单线程"这套组合正是最容易翻车的地方：
	// 少了这一条，第一次真备份才会发现传上去的东西是坏的。
	t.Run("跨分片的大归档同样能传上去并解开", func(t *testing.T) {
		big := t.TempDir()
		// 随机字节压不动，保证归档确实超过两个分片。
		payload := make([]byte, 40<<20)
		if _, err := rand.Read(payload); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(big, "database.dump"), payload, 0o600); err != nil {
			t.Fatal(err)
		}
		key := runtime.Prefix + "backup-55555555-5555-5555-5555-555555555555/archive.tar.gz.age"
		bigDigest, err := uploadArchive(ctx, store, key, big, identity.Recipient())
		if err != nil {
			t.Fatalf("大归档上传失败：%v", err)
		}
		if bigDigest.Bytes <= 2*remotePartSize {
			t.Fatalf("这份归档只有 %d 字节，没有跨过分片边界，这条用例没验到东西", bigDigest.Bytes)
		}
		if err := verifyRemoteArchive(ctx, store, key, bigDigest); err != nil {
			t.Fatalf("大归档回读校验失败：%v", err)
		}
		// 分片拼错时大小往往仍然对得上，所以要整包拉回来解密验证。
		data, err := store.Range(ctx, key, 0, bigDigest.Bytes)
		if err != nil {
			t.Fatalf("下载大归档：%v", err)
		}
		decrypted, err := age.Decrypt(bytes.NewReader(data), identity)
		if err != nil {
			t.Fatalf("大归档解密失败（多半是分片顺序错了）：%v", err)
		}
		uncompressed, err := gzip.NewReader(decrypted)
		if err != nil {
			t.Fatalf("大归档解压失败：%v", err)
		}
		archive := tar.NewReader(uncompressed)
		header, err := archive.Next()
		if err != nil {
			t.Fatalf("读大归档：%v", err)
		}
		if header.Name != "database.dump" || header.Size != int64(len(payload)) {
			t.Fatalf("归档内容不对：%s %d", header.Name, header.Size)
		}
		restored, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(restored, payload) {
			t.Fatal("解出来的内容和原文件不一致")
		}
		if err := store.Remove(ctx, key); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("过期清理只删自己那套命名的对象", func(t *testing.T) {
		foreign := runtime.Prefix + "not-ours.txt"
		if _, err := store.Put(ctx, foreign, strings.NewReader("keep me")); err != nil {
			t.Fatal(err)
		}
		removed, err := pruneRemote(ctx, store, runtime.Prefix, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("pruneRemote: %v", err)
		}
		if removed != 1 {
			t.Fatalf("应当删掉 1 份备份，实际 %d", removed)
		}
		if _, err := store.Stat(ctx, foreign); err != nil {
			t.Fatalf("桶里不属于本系统的对象被删了：%v", err)
		}
		if _, err := store.Stat(ctx, archiveKey); err == nil {
			t.Fatal("过期备份的归档没有删掉")
		}
	})
}
