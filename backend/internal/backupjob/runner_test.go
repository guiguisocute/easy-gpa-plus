package backupjob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeObjectPathRejectsTraversal(t *testing.T) {
	for _, key := range []string{"../secret", "a/../../secret", "/absolute"} {
		if runtime.GOOS == "windows" && key == "/absolute" {
			// filepath.IsAbs follows Windows drive/UNC rules; traversal cases above
			// still cover escaping the configured root on this platform.
			continue
		}
		if _, err := safeObjectPath(key); err == nil {
			t.Fatalf("safeObjectPath(%q) accepted an unsafe key", key)
		}
	}
	got, err := safeObjectPath("class-1/submission-2/proof.pdf")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("class-1", "submission-2", "proof.pdf")
	if got != want {
		t.Fatalf("safeObjectPath() = %q, want %q", got, want)
	}
}

func TestSafeRootRejectsFilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	}
	if _, err := safeRoot(root); err == nil {
		t.Fatalf("safeRoot(%q) accepted a filesystem root", root)
	}
}

func TestPostgresCommandDoesNotExposePasswordInArguments(t *testing.T) {
	target, err := parsePostgresURL("postgres://backup:very-secret@db.example:5433/easygpa?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(target.commandArgs(), " ")
	if strings.Contains(arguments, "very-secret") {
		t.Fatal("database password leaked into process arguments")
	}
	if !strings.Contains(strings.Join(target.environment(), " "), "PGPASSWORD=very-secret") {
		t.Fatal("database password missing from subprocess environment")
	}
	if target.withDatabase("easygpa_drill_test") == "" {
		t.Fatal("temporary database connection string is empty")
	}
}

func TestBackupRootsMustBeIndependent(t *testing.T) {
	root := t.TempDir()
	local, err := safeRoot(filepath.Join(root, "local"))
	if err != nil {
		t.Fatal(err)
	}
	offsite, err := safeRoot(filepath.Join(root, "local", "offsite"))
	if err != nil {
		t.Fatal(err)
	}
	if !sameOrNested(local, offsite) {
		t.Fatal("nested offsite directory was not detected")
	}
}

func TestVerifyObjectsDetectsCorruption(t *testing.T) {
	root := t.TempDir()
	objectPath := filepath.Join(root, "objects", "class-1", "proof.txt")
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("verified backup content")
	if err := os.WriteFile(objectPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	items := []objectManifest{{
		Key: "class-1/proof.txt", Path: "class-1/proof.txt", Size: int64(len(content)),
		SHA256: hex.EncodeToString(digest[:]),
	}}
	if err := verifyObjects(root, items); err != nil {
		t.Fatalf("verifyObjects() rejected a valid object: %v", err)
	}
	if err := os.WriteFile(objectPath, []byte("corrupted backup content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyObjects(root, items); err == nil {
		t.Fatal("verifyObjects() accepted corrupted content")
	}
}

func TestBackupPathsStageInsideFinalVolume(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local")
	offsite := filepath.Join(t.TempDir(), "offsite")
	work, finalPath, isOffsite := backupPaths(local, offsite, "test-id")
	if !isOffsite {
		t.Fatal("offsite backup was not identified")
	}
	if filepath.Dir(work) != offsite || filepath.Dir(finalPath) != offsite {
		t.Fatalf("work and final paths must share the offsite root: %q %q", work, finalPath)
	}
	if filepath.Base(work) != ".backup-test-id.partial" || filepath.Base(finalPath) != "backup-test-id" {
		t.Fatalf("unexpected backup paths: %q %q", work, finalPath)
	}

	work, finalPath, isOffsite = backupPaths(local, "", "local-id")
	if isOffsite || filepath.Dir(work) != local || filepath.Dir(finalPath) != local {
		t.Fatalf("local-only backup paths are incorrect: %q %q", work, finalPath)
	}
}

func TestAllowedBackupPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "backup-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	runner := &Runner{config: Config{LocalDir: root}}
	if runner.allowedBackupPath(link) {
		t.Fatal("backup path symlink escaping the configured root was accepted")
	}
}

func TestVerifyObjectsRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	objectPath := filepath.Join(root, "objects", "proof.txt")
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, objectPath); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := verifyObjects(root, []objectManifest{{Key: "proof.txt", Path: "proof.txt", Size: 7}}); err == nil {
		t.Fatal("backup object symlink was accepted")
	}
}

func TestBackupCommandRunnerRejectsUnknownBinary(t *testing.T) {
	err := (osCommandRunner{}).Run(context.Background(), nil, "sh", "-c", "exit 0")
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("unknown backup command error = %v", err)
	}
}
