// Package backupjob creates durable database/object-store backups and executes
// restore drills against randomly named temporary databases.
package backupjob

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/db/migrations"
	"easygpa/backend/internal/objectstore"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/safeexec"
)

const runnerLock int64 = 184246984857

var jobIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)

type Config struct {
	DatabaseURL string
	LocalDir    string
	OffsiteDir  string
	Retention   time.Duration
	// RemoteRecipient 是 age 公钥（age1...），用来加密推往云桶的归档。
	// 私钥不在这台机器上，见 archive.go 的说明。
	RemoteRecipient string
	// RemoteCipher 解开运维页存进库的桶 AccessKey/SecretKey。没配
	// BACKUP_REMOTE_SECRET_KEY 时为 nil，远程推送会带明确原因失败。
	RemoteCipher        *opsconfig.Cipher
	RemoteAllowLoopback bool
}

type Runner struct {
	ops     *pgxpool.Pool
	objects *objectstore.Client
	config  Config
	command commandRunner
	now     func() time.Time
	policy  lifecycleSource
	remote  remoteFactory
}

type lifecycleSource interface {
	Lifecycle(context.Context) (opsconfig.Lifecycle, error)
	BackupRemote(context.Context) (opsconfig.BackupRemote, error)
}

type commandRunner interface {
	Run(context.Context, []string, string, ...string) error
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, env []string, name string, args ...string) error {
	allowed := map[string]bool{"pg_dump": true, "pg_restore": true, "createdb": true, "dropdb": true}
	if !allowed[name] {
		return fmt.Errorf("backup command %q is not allowed", name)
	}
	output, err := safeexec.Run(ctx, name, args, safeexec.Options{ExtraEnv: env, MaxOutput: 256 * 1024})
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 2000 {
			message = message[:2000]
		}
		if message != "" {
			return fmt.Errorf("%s: %w: %s", name, err, message)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func NewRunner(ops *pgxpool.Pool, objects *objectstore.Client, config Config) (*Runner, error) {
	return NewRunnerWithRuntime(ops, objects, config, nil)
}

func NewRunnerWithRuntime(ops *pgxpool.Pool, objects *objectstore.Client, config Config, policy lifecycleSource) (*Runner, error) {
	if ops == nil || objects == nil || strings.TrimSpace(config.DatabaseURL) == "" || strings.TrimSpace(config.LocalDir) == "" {
		return nil, errors.New("backup worker dependencies are required")
	}
	local, err := safeRoot(config.LocalDir)
	if err != nil {
		return nil, fmt.Errorf("BACKUP_DIR: %w", err)
	}
	config.LocalDir = local
	if config.OffsiteDir != "" {
		offsite, err := safeRoot(config.OffsiteDir)
		if err != nil {
			return nil, fmt.Errorf("BACKUP_OFFSITE_DIR: %w", err)
		}
		if sameOrNested(local, offsite) || sameOrNested(offsite, local) {
			return nil, errors.New("BACKUP_DIR and BACKUP_OFFSITE_DIR must be independent directories")
		}
		config.OffsiteDir = offsite
	}
	if config.Retention <= 0 {
		config.Retention = 30 * 24 * time.Hour
	}
	if _, err := parsePostgresURL(config.DatabaseURL); err != nil {
		return nil, err
	}
	return &Runner{
		ops: ops, objects: objects, config: config, command: osCommandRunner{},
		now: time.Now, policy: policy, remote: NewRemoteStore,
	}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	conn, err := r.ops.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, runnerLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errors.New("another backup worker already owns the runner lock")
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, runnerLock) }()
	if _, err := r.ops.Exec(ctx, `UPDATE backup_job SET status='queued' WHERE status='running'`); err != nil {
		return err
	}
	if err := os.MkdirAll(r.config.LocalDir, 0o700); err != nil {
		return err
	}
	if r.config.OffsiteDir != "" {
		if err := os.MkdirAll(r.config.OffsiteDir, 0o700); err != nil {
			return err
		}
	}
	if err := r.reconcileCatalog(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := r.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Runner) tick(ctx context.Context) error {
	policy, err := r.lifecycle(ctx)
	if err != nil {
		return err
	}
	retention := time.Duration(policy.BackupRetentionDays) * 24 * time.Hour
	// Apply policy changes even on days without a new backup. This also keeps
	// the catalog honest when an operator moves or removes a backup directory.
	if err := r.cleanupExpired(ctx, retention); err != nil {
		return err
	}
	if err := r.reconcileCatalog(ctx); err != nil {
		return err
	}
	if err := r.enqueueDaily(ctx, policy); err != nil {
		return err
	}
	if err := r.enqueueRemotePush(ctx); err != nil {
		return err
	}
	for {
		job, ok, err := r.claim(ctx)
		if err != nil || !ok {
			return err
		}
		detail, offsite, runErr := r.execute(ctx, job, policy)
		if err := r.finish(ctx, job.ID, detail, offsite, runErr); err != nil {
			return err
		}
	}
}

func (r *Runner) enqueueDaily(ctx context.Context, policy opsconfig.Lifecycle) error {
	if !policy.BackupEnabled {
		return nil
	}
	local := r.now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	scheduled, _ := time.Parse("15:04", policy.BackupSchedule)
	if local.Hour()*60+local.Minute() < scheduled.Hour()*60+scheduled.Minute() {
		return nil
	}
	date := local.Format("2006-01-02")
	_, err := r.ops.Exec(ctx, `
		INSERT INTO backup_job (kind,status,offsite,detail)
		SELECT 'backup','queued',false,jsonb_build_object('scheduledDate',$1::text)
		 WHERE NOT EXISTS (
			SELECT 1 FROM backup_job
			 WHERE kind='backup' AND detail->>'scheduledDate'=$1
			   AND status IN ('queued','running','complete')
		 )
		   AND (
			SELECT count(*) FROM backup_job
			 WHERE kind='backup' AND detail->>'scheduledDate'=$1 AND status='failed'
		   ) < 3
		   AND NOT EXISTS (
			SELECT 1 FROM backup_job
			 WHERE kind='backup' AND detail->>'scheduledDate'=$1 AND status='failed'
			   AND finished_at > now() - interval '30 minutes'
		 )
	`, date)
	return err
}

type job struct {
	ID     string
	Kind   string
	Detail map[string]any
}

func (r *Runner) claim(ctx context.Context) (job, bool, error) {
	tx, err := r.ops.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return job{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var item job
	var raw []byte
	err = tx.QueryRow(ctx, `
		SELECT id::text,kind,detail FROM backup_job
		 WHERE status='queued'
		 ORDER BY CASE kind WHEN 'restore_drill' THEN 0 WHEN 'backup' THEN 1 ELSE 2 END,created_at,id
		 FOR UPDATE SKIP LOCKED LIMIT 1
	`).Scan(&item.ID, &item.Kind, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return job{}, false, nil
	}
	if err != nil {
		return job{}, false, err
	}
	if !jobIDPattern.MatchString(item.ID) || json.Unmarshal(raw, &item.Detail) != nil {
		return job{}, false, errors.New("backup job contains invalid data")
	}
	started, _ := json.Marshal(map[string]any{"startedAt": r.now().UTC()})
	if _, err := tx.Exec(ctx, `UPDATE backup_job SET status='running',detail=detail||$1::jsonb WHERE id=$2::uuid`, started, item.ID); err != nil {
		return job{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return job{}, false, err
	}
	return item, true, nil
}

func (r *Runner) execute(ctx context.Context, item job, policy opsconfig.Lifecycle) (map[string]any, bool, error) {
	switch item.Kind {
	case "backup":
		return r.createBackup(ctx, item, time.Duration(policy.BackupRetentionDays)*24*time.Hour)
	case "restore_drill":
		return r.restoreDrill(ctx, item)
	case "remote_push":
		return r.pushRemote(ctx, item)
	default:
		return nil, false, fmt.Errorf("unknown backup job kind %q", item.Kind)
	}
}

type manifest struct {
	CreatedAt   time.Time        `json:"createdAt"`
	ObjectCount int              `json:"objectCount"`
	ObjectBytes int64            `json:"objectBytes"`
	Database    string           `json:"database"`
	Objects     []objectManifest `json:"objects,omitempty"`
}

type objectManifest struct {
	Key    string `json:"key"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (r *Runner) createBackup(ctx context.Context, item job, retention time.Duration) (map[string]any, bool, error) {
	work, finalPath, offsite := backupPaths(r.config.LocalDir, r.config.OffsiteDir, item.ID)
	if err := os.RemoveAll(work); err != nil {
		return nil, false, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if err := os.RemoveAll(finalPath); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Join(work, "objects"), 0o700); err != nil {
		return nil, false, err
	}
	target, err := parsePostgresURL(r.config.DatabaseURL)
	if err != nil {
		return nil, false, err
	}
	dumpPath := filepath.Join(work, "database.dump")
	args := append(target.commandArgs(), "--format=custom", "--no-owner", "--file", dumpPath, "--dbname", target.Database)
	if err := r.command.Run(ctx, target.environment(), "pg_dump", args...); err != nil {
		return nil, false, err
	}
	objects, count, bytes, err := r.mirrorObjects(ctx, filepath.Join(work, "objects"))
	if err != nil {
		return nil, false, err
	}
	info := manifest{CreatedAt: r.now().UTC(), ObjectCount: count, ObjectBytes: bytes, Database: "database.dump", Objects: objects}
	raw, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(filepath.Join(work, "manifest.json"), raw, 0o600); err != nil {
		return nil, false, err
	}
	if err := os.Rename(work, finalPath); err != nil {
		return nil, false, err
	}
	_ = r.cleanupExpired(ctx, retention)
	return map[string]any{
		"path": finalPath, "databaseDump": "database.dump", "objectCount": count,
		"objectBytes": bytes, "retentionDays": int(retention / (24 * time.Hour)),
	}, offsite, nil
}

func backupPaths(localDir, offsiteDir, id string) (string, string, bool) {
	root := localDir
	offsite := false
	if offsiteDir != "" {
		root = offsiteDir
		offsite = true
	}
	name := "backup-" + id
	return filepath.Join(root, "."+name+".partial"), filepath.Join(root, name), offsite
}

func (r *Runner) mirrorObjects(ctx context.Context, root string) ([]objectManifest, int, int64, error) {
	items, err := r.objects.List(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	manifestItems := make([]objectManifest, 0, len(items))
	var total int64
	for _, item := range items {
		rel, err := safeObjectPath(item.Key)
		if err != nil {
			return nil, 0, 0, err
		}
		destination := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return nil, 0, 0, err
		}
		source, err := r.objects.Open(ctx, item.Key)
		if err != nil {
			return nil, 0, 0, err
		}
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = source.Close()
			return nil, 0, 0, err
		}
		digest := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(file, digest), source)
		closeErr := errors.Join(file.Close(), source.Close())
		if copyErr != nil || closeErr != nil {
			return nil, 0, 0, errors.Join(copyErr, closeErr)
		}
		if written != item.Size {
			return nil, 0, 0, fmt.Errorf("object %q size changed during backup", item.Key)
		}
		total += written
		manifestItems = append(manifestItems, objectManifest{Key: item.Key, Path: rel, Size: written, SHA256: hex.EncodeToString(digest.Sum(nil))})
	}
	return manifestItems, len(items), total, nil
}

func (r *Runner) restoreDrill(ctx context.Context, item job) (map[string]any, bool, error) {
	sourceID, _ := item.Detail["sourceBackupId"].(string)
	if !jobIDPattern.MatchString(sourceID) {
		return nil, false, errors.New("restore drill has no valid source backup")
	}
	var raw []byte
	var sourceOffsite bool
	err := r.ops.QueryRow(ctx, `SELECT detail,offsite FROM backup_job WHERE id=$1::uuid AND kind='backup' AND status='complete'`, sourceID).Scan(&raw, &sourceOffsite)
	if err != nil {
		return nil, false, err
	}
	var source map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, false, err
	}
	backupPath, _ := source["path"].(string)
	if !r.allowedBackupPath(backupPath) {
		return nil, false, errors.New("source backup path is outside configured backup roots")
	}
	manifestPath := filepath.Join(backupPath, "manifest.json")
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, false, fmt.Errorf("backup manifest: %w", err)
	}
	var info manifest
	if err := json.Unmarshal(rawManifest, &info); err != nil {
		return nil, false, fmt.Errorf("backup manifest: %w", err)
	}
	dumpPath := filepath.Join(backupPath, "database.dump")
	if _, err := os.Stat(dumpPath); err != nil {
		return nil, false, fmt.Errorf("database dump: %w", err)
	}
	if err := verifyObjects(backupPath, info.Objects); err != nil {
		return nil, false, err
	}
	target, err := parsePostgresURL(r.config.DatabaseURL)
	if err != nil {
		return nil, false, err
	}
	temporaryDatabase := "easygpa_drill_" + randomHex(8)
	args := append(target.commandArgs(), temporaryDatabase)
	if err := r.command.Run(ctx, target.environment(), "createdb", args...); err != nil {
		return nil, false, err
	}
	dropped := false
	defer func() {
		if dropped {
			return
		}
		dropCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		dropArgs := append(target.commandArgs(), "--if-exists", "--force", temporaryDatabase)
		_ = r.command.Run(dropCtx, target.environment(), "dropdb", dropArgs...)
	}()
	restoreArgs := append(target.commandArgs(), "--exit-on-error", "--no-owner", "--dbname", temporaryDatabase, dumpPath)
	if err := r.command.Run(ctx, target.environment(), "pg_restore", restoreArgs...); err != nil {
		return nil, false, err
	}
	temporaryURL := target.withDatabase(temporaryDatabase)
	pool, err := migrations.OpenPool(ctx, temporaryURL)
	if err != nil {
		return nil, false, err
	}
	if err := migrations.Up(ctx, pool); err != nil {
		pool.Close()
		return nil, false, err
	}
	var migrationVersion, classes, settlements int64
	err = pool.QueryRow(ctx, `
		SELECT (SELECT COALESCE(max(version),0) FROM schema_migration),
		       (SELECT count(*) FROM class),
		       (SELECT count(*) FROM settlement_run)
	`).Scan(&migrationVersion, &classes, &settlements)
	pool.Close()
	if err != nil {
		return nil, false, err
	}
	dropArgs := append(target.commandArgs(), "--if-exists", "--force", temporaryDatabase)
	if err := r.command.Run(ctx, target.environment(), "dropdb", dropArgs...); err != nil {
		return nil, false, err
	}
	dropped = true
	return map[string]any{
		"sourceBackupId": sourceID, "migrationVersion": migrationVersion,
		"sourceOffsite": sourceOffsite, "classCount": classes, "settlementRunCount": settlements,
		"objectCount":                len(info.Objects),
		"temporaryDatabaseDestroyed": true,
	}, false, nil
}

func verifyObjects(backupPath string, objects []objectManifest) error {
	for _, item := range objects {
		rel, err := safeObjectPath(item.Path)
		if err != nil || rel != filepath.Clean(filepath.FromSlash(item.Path)) {
			return fmt.Errorf("backup manifest contains unsafe object path %q", item.Path)
		}
		filePath := filepath.Join(backupPath, "objects", rel)
		fileInfo, lstatErr := os.Lstat(filePath)
		if lstatErr != nil || !fileInfo.Mode().IsRegular() {
			return fmt.Errorf("backup object %q is not a regular file", item.Key)
		}
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("backup object %q: %w", item.Key, err)
		}
		info, statErr := file.Stat()
		digest := sha256.New()
		written, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if statErr != nil || copyErr != nil || closeErr != nil {
			return fmt.Errorf("verify backup object %q: %w", item.Key, errors.Join(statErr, copyErr, closeErr))
		}
		if info.Size() != item.Size || written != item.Size || !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), item.SHA256) {
			return fmt.Errorf("backup object %q failed checksum verification", item.Key)
		}
	}
	return nil
}

func (r *Runner) finish(ctx context.Context, id string, detail map[string]any, offsite bool, runErr error) error {
	status := "complete"
	if detail == nil {
		detail = make(map[string]any)
	}
	if runErr != nil {
		status = "failed"
		detail["error"] = truncate(runErr.Error(), 2000)
	}
	detail["finishedAt"] = r.now().UTC()
	raw, _ := json.Marshal(detail)
	_, err := r.ops.Exec(ctx, `
		UPDATE backup_job SET status=$1,offsite=$2,detail=detail||$3::jsonb,finished_at=now()
		 WHERE id=$4::uuid AND status='running'
	`, status, offsite, raw, id)
	return err
}

func (r *Runner) cleanupExpired(ctx context.Context, retention time.Duration) error {
	if retention <= 0 {
		retention = r.config.Retention
	}
	cutoff := r.now().Add(-retention)
	for _, root := range []string{r.config.LocalDir, r.config.OffsiteDir} {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "backup-") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.ModTime().Before(cutoff) {
				target := filepath.Join(root, entry.Name())
				if !sameOrNested(root, target) || target == root {
					return errors.New("refusing to clean an unsafe backup path")
				}
				if err := os.RemoveAll(target); err != nil {
					return err
				}
				id := strings.TrimPrefix(entry.Name(), "backup-")
				if jobIDPattern.MatchString(id) {
					_, err := r.ops.Exec(ctx, `
						UPDATE backup_job
						   SET status='expired',detail=detail||jsonb_build_object('expiredAt',now()),finished_at=COALESCE(finished_at,now())
						 WHERE id=$1::uuid AND kind='backup' AND status='complete'
					`, id)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (r *Runner) lifecycle(ctx context.Context) (opsconfig.Lifecycle, error) {
	if r.policy != nil {
		return r.policy.Lifecycle(ctx)
	}
	value := opsconfig.DefaultLifecycle()
	value.BackupRetentionDays = int(r.config.Retention / (24 * time.Hour))
	if value.BackupRetentionDays <= 0 {
		value.BackupRetentionDays = 30
	}
	return value, nil
}

// reconcileCatalog makes the database list describe files that can actually
// be restored. A missing directory turns a formerly successful row into a
// visible failure; valid orphan directories are registered instead of hidden.
func (r *Runner) reconcileCatalog(ctx context.Context) error {
	rows, err := r.ops.Query(ctx, `SELECT id::text,detail->>'path' FROM backup_job WHERE kind='backup' AND status='complete'`)
	if err != nil {
		return err
	}
	known := make(map[string]bool)
	for rows.Next() {
		var id string
		var path *string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return err
		}
		known[id] = true
		if path == nil || !r.allowedBackupPath(*path) {
			continue
		}
		if info, err := os.Stat(*path); err != nil || !info.IsDir() {
			if _, updateErr := r.ops.Exec(ctx, `
				UPDATE backup_job SET status='failed',detail=detail||jsonb_build_object('catalogError','backup directory missing','catalogCheckedAt',now()) WHERE id=$1::uuid
			`, id); updateErr != nil {
				rows.Close()
				return updateErr
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, root := range []string{r.config.LocalDir, r.config.OffsiteDir} {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "backup-") {
				continue
			}
			id := strings.TrimPrefix(entry.Name(), "backup-")
			if !jobIDPattern.MatchString(id) || known[id] {
				continue
			}
			path := filepath.Join(root, entry.Name())
			status := "complete"
			if _, err := os.Stat(filepath.Join(path, "manifest.json")); err != nil {
				status = "failed"
			}
			if _, err := os.Stat(filepath.Join(path, "database.dump")); err != nil {
				status = "failed"
			}
			_, err := r.ops.Exec(ctx, `
				INSERT INTO backup_job (id,kind,status,offsite,detail,finished_at)
				VALUES ($1::uuid,'backup',$2,$3,jsonb_build_object('path',$4,'catalogRecovered',true),now())
				ON CONFLICT (id) DO NOTHING
			`, id, status, root == r.config.OffsiteDir && r.config.OffsiteDir != "", path)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func safeRoot(value string) (string, error) {
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = filepath.Clean(resolved)
	}
	if abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return "", errors.New("filesystem root is not allowed")
	}
	return abs, nil
}

func sameOrNested(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (r *Runner) allowedBackupPath(value string) bool {
	if value == "" {
		return false
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	path = filepath.Clean(resolved)
	for _, root := range []string{r.config.LocalDir, r.config.OffsiteDir} {
		if root == "" {
			continue
		}
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		if rootErr == nil && sameOrNested(resolvedRoot, path) && path != resolvedRoot {
			return true
		}
	}
	return false
}

func safeObjectPath(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe object key %q", key)
	}
	parts := strings.Split(clean, string(filepath.Separator))
	for index, part := range parts {
		if part == "" || strings.ContainsAny(part, "<>:\"|?*") {
			parts[index] = "__key_" + hex.EncodeToString([]byte(part))
		}
		for _, character := range part {
			if character < 0x20 {
				return "", fmt.Errorf("unsafe object key %q", key)
			}
		}
	}
	return filepath.Join(parts...), nil
}

type postgresTarget struct {
	URL      string
	Host     string
	Port     string
	User     string
	Password string
	Database string
	SSLMode  string
}

func parsePostgresURL(value string) (postgresTarget, error) {
	config, err := pgxpool.ParseConfig(value)
	if err != nil {
		return postgresTarget{}, fmt.Errorf("parse backup database URL: %w", err)
	}
	if config.ConnConfig.Host == "" || config.ConnConfig.User == "" || config.ConnConfig.Database == "" {
		return postgresTarget{}, errors.New("backup database URL needs host, user and database")
	}
	sslMode := "disable"
	if config.ConnConfig.TLSConfig != nil {
		sslMode = "require"
	}
	return postgresTarget{
		URL: value, Host: config.ConnConfig.Host, Port: fmt.Sprint(config.ConnConfig.Port),
		User: config.ConnConfig.User, Password: config.ConnConfig.Password,
		Database: config.ConnConfig.Database, SSLMode: sslMode,
	}, nil
}

func (t postgresTarget) commandArgs() []string {
	return []string{"--host", t.Host, "--port", t.Port, "--username", t.User, "--no-password"}
}

func (t postgresTarget) environment() []string {
	return []string{"PGPASSWORD=" + t.Password, "PGSSLMODE=" + t.SSLMode}
}

func (t postgresTarget) withDatabase(database string) string {
	config, err := pgxpool.ParseConfig(t.URL)
	if err != nil {
		return ""
	}
	config.ConnConfig.Database = database
	return config.ConnString()
}

func randomHex(bytes int) string {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(value)
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
