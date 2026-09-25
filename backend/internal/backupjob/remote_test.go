package backupjob

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

// memoryStore 是不联网的 RemoteStore。远程那条路径的三段——上传、回读校验、
// 过期清理——都能在它上面跑完。
type memoryStore struct {
	objects  map[string][]byte
	modified map[string]time.Time
	putErr   error
	removed  []string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{objects: map[string][]byte{}, modified: map[string]time.Time{}}
}

func (m *memoryStore) Put(_ context.Context, key string, reader io.Reader) (int64, error) {
	if m.putErr != nil {
		return 0, m.putErr
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	m.objects[key] = data
	if _, exists := m.modified[key]; !exists {
		m.modified[key] = time.Now()
	}
	return int64(len(data)), nil
}

func (m *memoryStore) Stat(_ context.Context, key string) (int64, error) {
	data, ok := m.objects[key]
	if !ok {
		return 0, errors.New("not found: " + key)
	}
	return int64(len(data)), nil
}

func (m *memoryStore) Range(_ context.Context, key string, offset, length int64) ([]byte, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, errors.New("not found: " + key)
	}
	if offset < 0 || offset+length > int64(len(data)) {
		return nil, errors.New("range out of bounds")
	}
	return append([]byte(nil), data[offset:offset+length]...), nil
}

func (m *memoryStore) List(_ context.Context, prefix string) ([]RemoteObject, error) {
	items := make([]RemoteObject, 0, len(m.objects))
	for key, data := range m.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		items = append(items, RemoteObject{Key: key, Size: int64(len(data)), LastModified: m.modified[key]})
	}
	return items, nil
}

func (m *memoryStore) Remove(_ context.Context, key string) error {
	delete(m.objects, key)
	delete(m.modified, key)
	m.removed = append(m.removed, key)
	return nil
}

// writeSampleBackup 造一个长得像真备份的目录：一个 dump、一个 manifest、
// 两个佐证对象。内容要够大，才能让首尾抽样真的落在不同的字节上。
func writeSampleBackup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"database.dump":                    bytes.Repeat([]byte("dump-payload;"), 4096),
		"manifest.json":                    []byte(`{"objectCount":2}`),
		"objects/class-1/proof.pdf":        bytes.Repeat([]byte("pdf-bytes;"), 2048),
		"objects/class-2/sub-9/photo.jpeg": bytes.Repeat([]byte("jpeg-bytes;"), 2048),
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// 这一条是整个功能的地基：推上去的东西必须真的能用私钥解开、解包，
// 并且里面就是原来那几个文件。传上去了但解不开，等于没备份。
func TestUploadArchiveCanBeDecryptedAndUnpacked(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	source := writeSampleBackup(t)
	store := newMemoryStore()
	const key = "backups/backup-test/archive.tar.gz.age"

	digest, err := uploadArchive(context.Background(), store, key, source, identity.Recipient())
	if err != nil {
		t.Fatalf("uploadArchive 报错：%v", err)
	}
	if digest.Bytes == 0 || digest.Whole == "" {
		t.Fatal("上传指纹为空")
	}
	if int64(len(store.objects[key])) != digest.Bytes {
		t.Fatalf("落到桶里 %d 字节，指纹记的是 %d", len(store.objects[key]), digest.Bytes)
	}

	decrypted, err := age.Decrypt(bytes.NewReader(store.objects[key]), identity)
	if err != nil {
		t.Fatalf("解密失败：%v", err)
	}
	uncompressed, err := gzip.NewReader(decrypted)
	if err != nil {
		t.Fatalf("解压失败：%v", err)
	}
	found := map[string]int{}
	archive := tar.NewReader(uncompressed)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("读归档失败：%v", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		content, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		found[header.Name] = len(content)
	}
	for _, name := range []string{"database.dump", "manifest.json", "objects/class-1/proof.pdf", "objects/class-2/sub-9/photo.jpeg"} {
		if found[name] == 0 {
			t.Errorf("归档里缺少 %s", name)
		}
	}
	if len(found) != 4 {
		t.Fatalf("归档里有 %d 个文件，期望 4 个：%v", len(found), found)
	}
}

func TestUploadArchivePropagatesUploadFailure(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore()
	store.putErr = errors.New("对面 503")
	// 上传失败时不能卡住：打包 goroutine 要能从管道上退出来。
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := uploadArchive(context.Background(), store, "k", writeSampleBackup(t), identity.Recipient()); err == nil {
			t.Error("上传失败时 uploadArchive 应当报错")
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("上传失败后 uploadArchive 没有返回，打包 goroutine 卡在管道上了")
	}
}

func TestVerifyRemoteArchiveDetectsTamperingAndTruncation(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	const key = "backup-x/archive.tar.gz.age"
	upload := func(t *testing.T) (*memoryStore, archiveDigest) {
		t.Helper()
		store := newMemoryStore()
		digest, err := uploadArchive(context.Background(), store, key, writeSampleBackup(t), identity.Recipient())
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyRemoteArchive(context.Background(), store, key, digest); err != nil {
			t.Fatalf("刚上传完的归档应当校验通过：%v", err)
		}
		return store, digest
	}

	t.Run("首块被改", func(t *testing.T) {
		store, digest := upload(t)
		store.objects[key][7] ^= 0xff
		if err := verifyRemoteArchive(context.Background(), store, key, digest); err == nil {
			t.Fatal("首块被篡改应当被抓出来")
		}
	})
	t.Run("末块被改", func(t *testing.T) {
		store, digest := upload(t)
		data := store.objects[key]
		data[len(data)-3] ^= 0xff
		if err := verifyRemoteArchive(context.Background(), store, key, digest); err == nil {
			t.Fatal("末块被篡改应当被抓出来")
		}
	})
	t.Run("被截断", func(t *testing.T) {
		store, digest := upload(t)
		store.objects[key] = store.objects[key][:len(store.objects[key])-16]
		if err := verifyRemoteArchive(context.Background(), store, key, digest); err == nil {
			t.Fatal("截断应当被抓出来")
		}
	})
	t.Run("换成了别的对象", func(t *testing.T) {
		store, digest := upload(t)
		store.objects[key] = bytes.Repeat([]byte("x"), int(digest.Bytes))
		if err := verifyRemoteArchive(context.Background(), store, key, digest); err == nil {
			t.Fatal("内容整体不符应当被抓出来")
		}
	})
}

func TestPruneRemoteRemovesOnlyExpiredBackups(t *testing.T) {
	const prefix = "easygpa/"
	fresh := "11111111-1111-1111-1111-111111111111"
	stale := "22222222-2222-2222-2222-222222222222"
	now := time.Now()
	store := newMemoryStore()
	for id, age := range map[string]time.Duration{fresh: 24 * time.Hour, stale: 200 * 24 * time.Hour} {
		for _, name := range []string{"archive.tar.gz.age", "manifest.json"} {
			key := prefix + "backup-" + id + "/" + name
			store.objects[key] = []byte("payload")
			store.modified[key] = now.Add(-age)
		}
	}
	// 桶可能是和别的东西共用的。不属于我们这套命名的对象一根汗毛都不能碰。
	foreign := prefix + "someone-elses-file.txt"
	store.objects[foreign] = []byte("not ours")
	store.modified[foreign] = now.Add(-999 * 24 * time.Hour)

	removed, err := pruneRemote(context.Background(), store, prefix, now.Add(-90*24*time.Hour))
	if err != nil {
		t.Fatalf("pruneRemote 报错：%v", err)
	}
	if removed != 1 {
		t.Fatalf("删掉了 %d 份备份，期望 1 份", removed)
	}
	if _, exists := store.objects[prefix+"backup-"+stale+"/archive.tar.gz.age"]; exists {
		t.Error("过期备份的归档没删掉")
	}
	if _, exists := store.objects[prefix+"backup-"+stale+"/manifest.json"]; exists {
		t.Error("过期备份的 manifest 没删掉，会留下半份")
	}
	if _, exists := store.objects[prefix+"backup-"+fresh+"/archive.tar.gz.age"]; !exists {
		t.Error("没到期的备份被误删")
	}
	if _, exists := store.objects[foreign]; !exists {
		t.Error("桶里不属于本系统的对象被删了")
	}
}

func TestRemoteBackupDirectoryIgnoresForeignKeys(t *testing.T) {
	const prefix = "easygpa/"
	id := "33333333-3333-3333-3333-333333333333"
	if got, ok := remoteBackupDirectory(prefix, prefix+"backup-"+id+"/archive.tar.gz.age"); !ok || got != prefix+"backup-"+id+"/" {
		t.Fatalf("认不出自己的对象：%q %v", got, ok)
	}
	for _, key := range []string{
		"other/backup-" + id + "/archive.tar.gz.age", // 前缀不对
		prefix + "backup-not-a-uuid/archive.tar.gz.age",
		prefix + "notbackup-" + id + "/archive.tar.gz.age",
		prefix + "backup-" + id, // 没有下级对象
		prefix + "backup-" + id + "/nested/deeper.bin",
	} {
		if _, ok := remoteBackupDirectory(prefix, key); ok {
			t.Errorf("不该认领这个 key：%s", key)
		}
	}
}

func TestParseRecipientRejectsPrivateKeyAndGarbage(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRecipient(identity.Recipient().String()); err != nil {
		t.Fatalf("合法公钥被拒绝：%v", err)
	}
	// 把私钥填进生产环境变量是最危险的那种配错：备份还能加密，但私钥从此
	// 和密文放在同一台机器上，客户端加密的意义整个没了。
	_, err = parseRecipient(identity.String())
	if err == nil || !strings.Contains(err.Error(), "私钥") {
		t.Fatalf("填成私钥时应当明确点出来，得到：%v", err)
	}
	if _, err := parseRecipient(""); err == nil {
		t.Fatal("空公钥应当被拒绝")
	}
	if _, err := parseRecipient("age1-not-a-real-key"); err == nil {
		t.Fatal("乱填的公钥应当被拒绝")
	}
}

func TestProbeRemoteReportsWhichStepFailed(t *testing.T) {
	store := newMemoryStore()
	probe, err := ProbeRemote(context.Background(), store, "easygpa/")
	if err != nil {
		t.Fatalf("ProbeRemote 报错：%v", err)
	}
	if !probe.Wrote || !probe.Read || !probe.Removed {
		t.Fatalf("三步都该通过：%+v", probe)
	}
	if len(store.objects) != 0 {
		t.Fatalf("自检对象没有清理干净：%v", store.objects)
	}

	failing := newMemoryStore()
	failing.putErr = errors.New("AccessDenied")
	probe, err = ProbeRemote(context.Background(), failing, "easygpa/")
	if err == nil {
		t.Fatal("写不进去时应当报错")
	}
	if probe.Wrote {
		t.Fatal("写失败时 Wrote 不该是 true")
	}
}

func TestTailBufferKeepsLastBytes(t *testing.T) {
	tail := newTailBuffer(4)
	// 分多次写，且跨过环形缓冲的回绕点。
	for _, chunk := range []string{"ab", "cde", "f", "ghi"} {
		if _, err := tail.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(tail.Bytes()); got != "fghi" {
		t.Fatalf("tail = %q, want %q", got, "fghi")
	}
	if tail.written != 9 {
		t.Fatalf("written = %d, want 9", tail.written)
	}

	// 写入总量不足 size 时，返回的就是写进去的那些。
	short := newTailBuffer(8)
	if _, err := short.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if got := string(short.Bytes()); got != "abc" {
		t.Fatalf("tail = %q, want %q", got, "abc")
	}

	// 单次写就超过容量时只留末尾那一段。
	big := newTailBuffer(3)
	if _, err := big.Write([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	if got := string(big.Bytes()); got != "fgh" {
		t.Fatalf("tail = %q, want %q", got, "fgh")
	}
}

func TestHeadBufferKeepsFirstBytes(t *testing.T) {
	head := newHeadBuffer(4)
	for _, chunk := range []string{"ab", "cde", "fgh"} {
		if _, err := head.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(head.Bytes()); got != "abcd" {
		t.Fatalf("head = %q, want %q", got, "abcd")
	}
}

func TestWriteEncryptedArchiveRejectsIrregularFiles(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	// 跟随符号链接会把备份目录外的东西吸进异地副本；原样存则在恢复时留下
	// 一个指向别处的悬空链接。两种都不行，所以直接拒绝。
	if err := writeEncryptedArchive(context.Background(), io.Discard, root, identity.Recipient()); err == nil {
		t.Fatal("备份目录里有符号链接时应当拒绝打包")
	}
}
