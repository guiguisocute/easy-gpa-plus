package notify

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMailDeliveryDatabaseReservationsRecoveryAndTenantBoundary(t *testing.T) {
	url := os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL")
	if url == "" {
		t.Skip("set EASYGPA_TEST_ADMIN_DATABASE_URL for isolated mail ledger database tests")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var classID int64
	var eventID string
	if err := tx.QueryRow(ctx, `INSERT INTO class(name) VALUES('mail ledger regression') RETURNING id`).Scan(&classID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO outbox_event(class_id,type,payload,published_at) VALUES($1,'review.decided','{}',now()) RETURNING id::text`, classID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	start := func(class any, event any, recipient string) (int64, string) {
		t.Helper()
		var id int64
		var status, message string
		if err := tx.QueryRow(ctx, `SELECT * FROM ops_start_mail_delivery($1,$2::uuid,$3,'review_decided')`, class, event, recipient).Scan(&id, &status, &message); err != nil {
			t.Fatal(err)
		}
		return id, status
	}
	id, status := start(classID, eventID, "fixture@example.invalid")
	if status != "new" {
		t.Fatal("first attempt not reserved")
	}
	duplicate, status := start(classID, eventID, "FIXTURE@example.invalid")
	if duplicate != id || status != "queued" {
		t.Fatal("unresolved attempt was duplicated")
	}
	if _, err := tx.Exec(ctx, `SELECT ops_finish_mail_delivery($1,'failed','','FailedOperation.FrequencyLimit','FailedOperation.FrequencyLimit')`, id); err != nil {
		t.Fatal(err)
	}
	second, status := start(classID, eventID, "fixture@example.invalid")
	if second == id || status != "new" {
		t.Fatal("confirmed rejection cannot retry")
	}
	var attempt int
	if err := tx.QueryRow(ctx, `SELECT attempt FROM mail_delivery WHERE id=$1`, second).Scan(&attempt); err != nil || attempt != 2 {
		t.Fatalf("attempt=%d err=%v", attempt, err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ops_reconcile_mail_delivery($1,'sent','message-id')`, second).Scan(&n); err != nil || n != 0 {
		t.Fatal("in-flight attempt can be reconciled too early")
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_delivery SET created_at=now()-interval '3 minutes' WHERE id=$1`, second); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ops_reconcile_mail_delivery($1,'sent','message-id')`, second).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reconciliation rows=%d err=%v", n, err)
	}
	accepted, status := start(classID, eventID, "fixture@example.invalid")
	if accepted != second || status != "sent" {
		t.Fatal("accepted attempt sent again")
	}
	platform, _ := start(nil, nil, "platform@example.invalid")
	var tenant *int64
	var hash string
	if err := tx.QueryRow(ctx, `SELECT tenant_id,recipient_hash FROM ops_mail_delivery_log_v3() WHERE delivery_id=$1`, platform).Scan(&tenant, &hash); err != nil || tenant != nil || len(hash) != 64 {
		t.Fatal("new platform projection is not nullable and masked")
	}
	var legacyTenant int64
	if err := tx.QueryRow(ctx, `SELECT tenant_id FROM ops_mail_delivery_log_v2() WHERE delivery_id=$1`, platform).Scan(&legacyTenant); err != nil || legacyTenant != 0 {
		t.Fatal("old API projection broke after platform send")
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE easygpa_app`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_class',$1,true)`, strconv.FormatInt(classID, 10)); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM mail_delivery WHERE id=$1`, platform).Scan(&n); err != nil || n != 0 {
		t.Fatal("tenant can read platform delivery")
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT has_function_privilege(current_user,'ops_start_mail_delivery(bigint,uuid,text,text)','execute')`).Scan(&allowed); err != nil || allowed {
		t.Fatal("app role can invoke privileged mail writer")
	}
}
