package agentjob

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/knowledge"
	"easygpa/backend/internal/store"
)

type conversionObjects struct {
	source  []byte
	putErr  error
	cancel  context.CancelFunc
	puts    int
	removed []string
}

func (o *conversionObjects) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(o.source)), nil
}

func (o *conversionObjects) Put(context.Context, string, string, io.Reader, int64) error {
	o.puts++
	if o.cancel != nil {
		o.cancel()
	}
	return o.putErr
}

func (o *conversionObjects) Remove(_ context.Context, key string) error {
	o.removed = append(o.removed, key)
	return nil
}

func TestPersistConversionRejectsInvalidUTF8BeforeObjectWrite(t *testing.T) {
	objects := &conversionObjects{}
	w := &Worker{objects: objects}
	result := knowledge.Result{Status: "ready", Entries: []knowledge.Entry{{Text: "invalid\xb5"}}}
	if err := w.persistConversion(context.Background(), 1, 1, result); err == nil {
		t.Fatal("tenant conversion accepted invalid UTF-8")
	}
	if err := w.persistPlatformConversion(context.Background(), 1, "", result); err == nil {
		t.Fatal("platform conversion accepted invalid UTF-8")
	}
	if objects.puts != 0 || len(objects.removed) != 0 {
		t.Fatal("invalid result must not touch stored objects")
	}
}

func TestProcessingFailureSurvivesCancellationAndPropagatesWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writeErr := errors.New("status database unavailable")
	err := persistProcessingFailure(ctx, "转换结果保存失败", func(failureCtx context.Context, message string) error {
		if failureCtx.Err() != nil || message == "" {
			t.Fatal("failure write must outlive the canceled conversion")
		}
		deadline, ok := failureCtx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("failure write needs a bounded deadline")
		}
		return writeErr
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("failure write was swallowed: %v", err)
	}
}

func TestSafeConversionErrorNeverCutsUTF8OrKeepsNUL(t *testing.T) {
	got := safeConversionError(errors.New(strings.Repeat("中", 350) + "\xb5\x00"))
	if !utf8.ValidString(got) || strings.ContainsRune(got, '\x00') || len([]rune(got)) != 300 {
		t.Fatalf("unsafe error: %q", got)
	}
	got = safeConversionError(errors.New("短错误\xb5\x00"))
	if !utf8.ValidString(got) || strings.ContainsRune(got, '\x00') {
		t.Fatalf("unsafe short error: %q", got)
	}
}

func TestDocumentPersistenceFailureMarksBothRecordsAndCanRetry(t *testing.T) {
	pool, classID := conversionTestPool(t)
	ctx := context.Background()
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	member, err := zipWriter.Create("rules.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(member, "conversion regression fixture %d", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	documentID, blobID, sourceKey := conversionFixture(t, pool, classID, int64(archive.Len()))
	putErr := errors.New("derived object storage unavailable")
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	objects := &conversionObjects{source: archive.Bytes(), putErr: putErr, cancel: cancel}
	w := &Worker{pool: pool, objects: objects, runtime: func(context.Context) (Runtime, error) { return Runtime{}, nil }}
	if err := w.processDocument(workCtx, classID, documentID); !errors.Is(err, putErr) {
		t.Fatalf("original persistence error was lost: %v", err)
	}
	assertConversionState(t, pool, classID, documentID, "failed", true)
	for _, key := range objects.removed {
		if key == sourceKey {
			t.Fatal("failed conversion removed the original file")
		}
	}
	objects.putErr, objects.cancel = nil, nil
	if err := w.processDocument(ctx, classID, documentID); err != nil {
		t.Fatalf("failed conversion could not be retried: %v", err)
	}
	assertConversionState(t, pool, classID, documentID, "ready", false)
	var entries int
	if err := store.InTenantTx(ctx, pool, classID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM knowledge_entry WHERE blob_id=$1`, blobID).Scan(&entries)
	}); err != nil || entries != 1 {
		t.Fatalf("retry entries = %d, error = %v", entries, err)
	}
}

func conversionTestPool(t *testing.T) (*pgxpool.Pool, int64) {
	t.Helper()
	url := os.Getenv("EASYGPA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set EASYGPA_TEST_DATABASE_URL for knowledge conversion database tests")
	}
	pools, err := store.Open(context.Background(), url, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pools.Close)
	classID := int64(1)
	if raw := os.Getenv("EASYGPA_TEST_CLASS_ID"); raw != "" {
		classID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || classID <= 0 {
			t.Fatal("invalid EASYGPA_TEST_CLASS_ID")
		}
	}
	return pools.App, classID
}

func conversionFixture(t *testing.T, pool *pgxpool.Pool, classID, size int64) (documentID, blobID int64, sourceKey string) {
	t.Helper()
	ctx := context.Background()
	sourceKey = fmt.Sprintf("test-knowledge-%d/source.zip", time.Now().UnixNano())
	err := store.InTenantTx(ctx, pool, classID, func(tx pgx.Tx) error {
		var uploaderID int64
		if err := tx.QueryRow(ctx, `SELECT id FROM app_user ORDER BY id LIMIT 1`).Scan(&uploaderID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO knowledge_blob (class_id,object_key,media_type,size_bytes,status) VALUES ($1,$2,'application/zip',$3,'queued') RETURNING id`, classID, sourceKey, size).Scan(&blobID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO knowledge_document (class_id,blob_id,filename,logical_path,display_name,visibility,status,uploaded_by) VALUES ($1,$2,'test.zip','test.zip','conversion test','class','queued',$3) RETURNING id`, classID, blobID, uploaderID).Scan(&documentID)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := store.InTenantTx(context.Background(), pool, classID, func(tx pgx.Tx) error {
			if _, err := tx.Exec(context.Background(), `DELETE FROM knowledge_document WHERE id=$1`, documentID); err != nil {
				return err
			}
			_, err := tx.Exec(context.Background(), `DELETE FROM knowledge_blob WHERE id=$1`, blobID)
			return err
		})
		if err != nil {
			t.Errorf("cleanup conversion fixture: %v", err)
		}
	})
	return documentID, blobID, sourceKey
}

func assertConversionState(t *testing.T, pool *pgxpool.Pool, classID, documentID int64, want string, wantError bool) {
	t.Helper()
	var documentStatus, blobStatus string
	var documentError, blobError bool
	err := store.InTenantTx(context.Background(), pool, classID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT d.status,b.status,d.error IS NOT NULL,b.error IS NOT NULL FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id WHERE d.id=$1`, documentID).Scan(&documentStatus, &blobStatus, &documentError, &blobError)
	})
	if err != nil {
		t.Fatal(err)
	}
	if documentStatus != want || blobStatus != want || documentError != wantError || blobError != wantError {
		t.Fatalf("document/blob states = %s/%s, errors = %t/%t; want %s, errors = %t", documentStatus, blobStatus, documentError, blobError, want, wantError)
	}
}
