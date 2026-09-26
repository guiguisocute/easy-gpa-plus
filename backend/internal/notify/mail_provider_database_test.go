package notify

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMailProviderDatabaseReservationAndOutcomePreserveChannel(t *testing.T) {
	url := os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL")
	if url == "" {
		t.Skip("set EASYGPA_TEST_ADMIN_DATABASE_URL for isolated mail provider database tests")
	}
	admin, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE easygpa_ops")
		return err
	}
	ops, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ops.Close)
	recorder := postgresDeliveryRecorder{pool: ops}

	for _, provider := range []string{"smtp", "aliyun_dm", "resend", "tencent_ses"} {
		for _, outcome := range []string{"finish", "reconcile"} {
			t.Run(provider+"/"+outcome, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				attempt, err := recorder.Start(ctx, Message{
					To: "provider-ledger-test@example.org", Template: "ops_test", provider: provider,
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := admin.Exec(cleanup, `DELETE FROM mail_delivery WHERE id=$1`, attempt.ID); err != nil {
						t.Error(err)
					}
				})
				if attempt.ID == 0 || attempt.Status != "new" {
					t.Fatalf("platform mail was not newly reserved: %#v", attempt)
				}
				assertStored := func(wantStatus, wantReceipt string) {
					t.Helper()
					var actualProvider, actualStatus, actualReceipt string
					var classID *int64
					if err := admin.QueryRow(ctx, `SELECT provider,status,COALESCE(provider_id,''),class_id FROM mail_delivery WHERE id=$1`, attempt.ID).
						Scan(&actualProvider, &actualStatus, &actualReceipt, &classID); err != nil {
						t.Fatal(err)
					}
					if actualProvider != provider || actualStatus != wantStatus || actualReceipt != wantReceipt || classID != nil {
						t.Fatalf("provider=%q status=%q receipt=%q class=%v", actualProvider, actualStatus, actualReceipt, classID)
					}
				}
				// The provider must already be correct before there is any receipt,
				// so a timeout or provider rejection is logged against its own channel.
				assertStored("queued", "")
				const receipt = "receipt-without-provider-prefix"
				if outcome == "finish" {
					if err := recorder.Finish(ctx, attempt.ID, receipt, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := admin.Exec(ctx, `UPDATE mail_delivery SET created_at=now()-interval '3 minutes' WHERE id=$1`, attempt.ID); err != nil {
						t.Fatal(err)
					}
					var reconciled int
					if err := ops.QueryRow(ctx, `SELECT count(*) FROM ops_reconcile_mail_delivery($1,'sent',$2)`, attempt.ID, receipt).Scan(&reconciled); err != nil {
						t.Fatal(err)
					}
					if reconciled != 1 {
						t.Fatalf("expected one reconciled platform mail, got %d", reconciled)
					}
				}
				assertStored("sent", receipt)
			})
		}
	}
}
