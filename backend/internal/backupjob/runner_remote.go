package backupjob

// remote_push 任务：把一次已经完成的本地备份加密后推到运维配的云桶。
//
// 为什么是独立任务、而不是接在 createBackup 尾巴上：公网传几个 G 会慢、会断、
// 会因为对面限流而失败，而这些都不该让一次好端端的本地备份被判成 failed。
// 分开之后本地备份的成功语义没变，远程那一条自己重试自己失败。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"filippo.io/age"

	"easygpa/backend/internal/opsconfig"
)

func (r *Runner) backupRemote(ctx context.Context) (opsconfig.BackupRemote, error) {
	if r.policy == nil {
		return opsconfig.BackupRemote{}, nil
	}
	return r.policy.BackupRemote(ctx)
}

// enqueueRemotePush 给最近一天内完成、还没推过的本地备份排一条远程任务。
//
// 只看最近 24 小时，是为了让"第一次打开这个开关"不会把保留期内的三十份备份
// 一起塞进队列，把出网带宽和云上账单一次性打满。要补历史副本，运维手动发起。
func (r *Runner) enqueueRemotePush(ctx context.Context) error {
	config, err := r.backupRemote(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled || !config.Configured() {
		return nil
	}
	_, err = r.ops.Exec(ctx, `
		INSERT INTO backup_job (kind,status,offsite,detail)
		SELECT 'remote_push','queued',true,jsonb_build_object('sourceBackupId',b.id::text)
		  FROM backup_job b
		 WHERE b.kind='backup' AND b.status='complete'
		   AND b.finished_at > now() - interval '24 hours'
		   AND NOT EXISTS (
			SELECT 1 FROM backup_job p
			 WHERE p.kind='remote_push' AND p.detail->>'sourceBackupId'=b.id::text
			   AND (p.status IN ('queued','running','complete')
			        OR (p.status='failed' AND p.finished_at > now() - interval '30 minutes'))
		   )
		   AND (
			SELECT count(*) FROM backup_job p
			 WHERE p.kind='remote_push' AND p.detail->>'sourceBackupId'=b.id::text
			   AND p.status='failed'
		   ) < 3
	`)
	return err
}

func (r *Runner) pushRemote(ctx context.Context, item job) (map[string]any, bool, error) {
	sourceID, _ := item.Detail["sourceBackupId"].(string)
	if !jobIDPattern.MatchString(sourceID) {
		return nil, true, errors.New("远程副本任务没有有效的来源备份")
	}
	var raw []byte
	err := r.ops.QueryRow(ctx, `
		SELECT detail FROM backup_job WHERE id=$1::uuid AND kind='backup' AND status='complete'
	`, sourceID).Scan(&raw)
	if err != nil {
		return nil, true, err
	}
	var source map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, true, err
	}
	backupPath, _ := source["path"].(string)
	if !r.allowedBackupPath(backupPath) {
		return nil, true, errors.New("来源备份的路径不在配置的备份根目录里")
	}
	if info, err := os.Stat(backupPath); err != nil || !info.IsDir() {
		return nil, true, fmt.Errorf("来源备份目录不可读：%s", backupPath)
	}

	stored, err := r.backupRemote(ctx)
	if err != nil {
		return nil, true, err
	}
	if !stored.Enabled {
		return nil, true, errors.New("远程备份仓库已被关闭")
	}
	if r.config.RemoteCipher == nil {
		return nil, true, errors.New("服务端未配置 BACKUP_REMOTE_SECRET_KEY，无法解开桶凭据")
	}
	runtime, err := opsconfig.ResolveBackupRemote(stored, r.config.RemoteCipher)
	if err != nil {
		return nil, true, err
	}
	recipient, err := parseRecipient(r.config.RemoteRecipient)
	if err != nil {
		return nil, true, err
	}
	runtime.AllowLoopback = r.config.RemoteAllowLoopback
	store, err := r.remote(runtime)
	if err != nil {
		return nil, true, err
	}

	directory := remoteDirectory(runtime.Prefix, sourceID)
	archiveKey := directory + "archive.tar.gz.age"
	digest, err := uploadArchive(ctx, store, archiveKey, backupPath, recipient)
	if err != nil {
		return nil, true, err
	}
	if err := verifyRemoteArchive(ctx, store, archiveKey, digest); err != nil {
		return nil, true, err
	}

	objectCount := 0
	if value, ok := source["objectCount"].(float64); ok {
		objectCount = int(value)
	}
	// 远程 manifest 只放不敏感的元信息。逐个对象的 key 列表留在加密包里面——
	// 那些 key 含班级和提交 ID，明文放在桶上等于把结构泄给云厂商。
	manifest := map[string]any{
		"backupId": sourceID, "createdAt": r.now().UTC(), "archiveBytes": digest.Bytes,
		"archiveSha256": digest.Whole, "objectCount": objectCount, "encryption": "age-v1",
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, true, err
	}
	if _, err := store.Put(ctx, directory+"manifest.json", strings.NewReader(string(manifestRaw))); err != nil {
		return nil, true, fmt.Errorf("上传 manifest：%w", err)
	}

	pruned, pruneErr := pruneRemote(ctx, store, runtime.Prefix, r.now().Add(-time.Duration(runtime.RetentionDays)*24*time.Hour))
	detail := map[string]any{
		"sourceBackupId": sourceID, "remoteKey": archiveKey, "bucket": runtime.Bucket,
		"endpoint": runtime.Endpoint, "archiveBytes": digest.Bytes, "archiveSha256": digest.Whole,
		// verified 只代表"确实上传成功、大小对得上、首尾块回读一致"。
		// 它不代表这份备份能恢复——那要整包拉回来解密解包才算数。
		"verified": true, "verifiedBy": "size+head/tail-sample", "remoteRetentionDays": runtime.RetentionDays,
	}
	if pruneErr != nil {
		// 过期清理失败不该把一次成功的上传判成失败：副本已经在对面了。
		detail["pruneError"] = truncate(pruneErr.Error(), 500)
	} else if pruned > 0 {
		detail["prunedRemoteBackups"] = pruned
	}
	return detail, true, nil
}

func remoteDirectory(prefix, sourceID string) string {
	return prefix + "backup-" + sourceID + "/"
}

// archiveDigest 是上传过程中顺手算出来的指纹，回读校验拿它做基准。
type archiveDigest struct {
	Bytes int64
	Whole string
	Head  []byte
	Tail  []byte
}

// uploadArchive 一边打包加密一边上传，磁盘上不落第二份。
func uploadArchive(ctx context.Context, store RemoteStore, key, directory string, recipient age.Recipient) (archiveDigest, error) {
	reader, writer := io.Pipe()
	go func() {
		writer.CloseWithError(writeEncryptedArchive(ctx, writer, directory, recipient))
	}()
	// Put 失败时要把管道读端关掉，否则上面那个 goroutine 会一直阻塞在 Write 上。
	defer reader.Close()

	whole := sha256.New()
	head := newHeadBuffer(sampleBytes)
	tail := newTailBuffer(sampleBytes)
	// 用 TeeReader 而不是在生产端 MultiWriter：这样指纹算的正好是 Put 真正读走的
	// 那些字节，中途被截断也会体现在指纹上。
	tapped := io.TeeReader(reader, io.MultiWriter(whole, head, tail))

	size, err := store.Put(ctx, key, tapped)
	if err != nil {
		return archiveDigest{}, fmt.Errorf("上传归档：%w", err)
	}
	if size >= 0 && size != tail.written {
		return archiveDigest{}, fmt.Errorf("上传归档：对面记录 %d 字节，本地送出 %d 字节", size, tail.written)
	}
	return archiveDigest{Bytes: tail.written, Whole: hex.EncodeToString(whole.Sum(nil)), Head: head.Bytes(), Tail: tail.Bytes()}, nil
}

// verifyRemoteArchive 回读校验：比大小，再把首尾各 1 MiB 拉回来比指纹。
//
// 这一步能抓住截断、传成了别的对象、以及两端损坏。它证明不了整包完好——
// 那要把几个 G 全下回来，不适合每天做。
func verifyRemoteArchive(ctx context.Context, store RemoteStore, key string, digest archiveDigest) error {
	size, err := store.Stat(ctx, key)
	if err != nil {
		return fmt.Errorf("回读远程归档：%w", err)
	}
	if size != digest.Bytes {
		return fmt.Errorf("远程归档大小是 %d 字节，本地送出 %d 字节", size, digest.Bytes)
	}
	for _, sample := range []struct {
		name   string
		offset int64
		want   []byte
	}{
		{"首块", 0, digest.Head},
		{"末块", size - int64(len(digest.Tail)), digest.Tail},
	} {
		if len(sample.want) == 0 {
			continue
		}
		got, err := store.Range(ctx, key, sample.offset, int64(len(sample.want)))
		if err != nil {
			return fmt.Errorf("回读远程归档%s：%w", sample.name, err)
		}
		if sha256.Sum256(got) != sha256.Sum256(sample.want) {
			return fmt.Errorf("远程归档%s与本地不一致", sample.name)
		}
	}
	return nil
}

// pruneRemote 按远程保留期删掉过期的整个 backup-<id>/ 前缀。
//
// 远程保留期独立于本地：本地那两份是给恢复演练和快速回滚用的，可以短；
// 云上这份是真正的异地副本，该留得久。
func pruneRemote(ctx context.Context, store RemoteStore, prefix string, cutoff time.Time) (int, error) {
	items, err := store.List(ctx, prefix)
	if err != nil {
		return 0, err
	}
	groups := make(map[string][]string)
	expired := make(map[string]bool)
	for _, item := range items {
		directory, ok := remoteBackupDirectory(prefix, item.Key)
		if !ok {
			continue
		}
		groups[directory] = append(groups[directory], item.Key)
		// 只用归档对象的时间做判据。manifest 是后写的，拿它当基准会让一份备份
		// 的两个对象各自过期，删出半份来。
		if strings.HasSuffix(item.Key, "/archive.tar.gz.age") && item.LastModified.Before(cutoff) {
			expired[directory] = true
		}
	}
	removed := 0
	for directory := range expired {
		for _, key := range groups[directory] {
			if err := store.Remove(ctx, key); err != nil {
				return removed, fmt.Errorf("删除过期远程副本 %s：%w", key, err)
			}
		}
		removed++
	}
	return removed, nil
}

// remoteBackupDirectory 认出一个 key 属于哪一份备份，认不出就返回 false。
// 桶可能是和别的东西共用的，不属于我们这套命名的对象一律不碰。
func remoteBackupDirectory(prefix, key string) (string, bool) {
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(key, prefix)
	name, remainder, found := strings.Cut(rest, "/")
	if !found || remainder == "" || path.Base(remainder) != remainder {
		return "", false
	}
	id, ok := strings.CutPrefix(name, "backup-")
	if !ok || !jobIDPattern.MatchString(id) {
		return "", false
	}
	return prefix + name + "/", true
}
