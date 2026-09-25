package notify

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/store"
)

func notificationDeliveryTestPool(t *testing.T) (*pgxpool.Pool, int64) {
	t.Helper()
	url := os.Getenv("EASYGPA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set EASYGPA_TEST_DATABASE_URL for notification receipt recovery tests")
	}
	pools, err := store.Open(t.Context(), url, "")
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

func TestNotificationWorkerRecoversMissingReceiptFromCaseInsensitiveSentDelivery(t *testing.T) {
	for _, digest := range []bool{false, true} {
		name := "same-event"
		if digest {
			name = "dispatch-digest"
		}
		t.Run(name, func(t *testing.T) {
			pool, classID := notificationDeliveryTestPool(t)
			ctx := t.Context()
			event := events.Event{ID: uuid.NewString(), ClassID: classID, Type: events.ReviewDecided,
				Payload: json.RawMessage(`{"submissionId":1,"reviewId":1,"status":"scored"}`)}
			sourceID := event.ID
			ids := []string{event.ID}
			if digest {
				event.Type = events.DispatchDone
				event.Payload = json.RawMessage(`{"runId":"1","reviewerIds":["1"],"assigned":1,"blocked":0}`)
				sourceID = uuid.NewString()
				ids = append(ids, sourceID)
			}
			createdAt := time.Date(2026, 9, 4, 8, 0, 0, 0, shanghaiLocation)
			err := store.InTenantTx(ctx, pool, classID, func(tx pgx.Tx) error {
				for _, id := range ids {
					if _, err := tx.Exec(ctx, `INSERT INTO outbox_event(id,class_id,type,payload,created_at) VALUES($1::uuid,$2,$3,$4,$5)`, id, classID, event.Type, event.Payload, createdAt); err != nil {
						return err
					}
				}
				_, err := tx.Exec(ctx, `INSERT INTO mail_delivery(class_id,event_id,recipient,provider,provider_id,status) VALUES($1,$2::uuid,'HISTORY@EXAMPLE.TEST','test','accepted-before-worker-crash','sent')`, classID, sourceID)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				err := store.InTenantTx(context.Background(), pool, classID, func(tx pgx.Tx) error {
					for _, id := range ids {
						for _, query := range []string{`DELETE FROM notification_log WHERE event_id=$1::uuid`, `DELETE FROM mail_delivery WHERE event_id=$1::uuid`, `DELETE FROM outbox_event WHERE id=$1::uuid`} {
							if _, err := tx.Exec(context.Background(), query, id); err != nil {
								return err
							}
						}
					}
					return nil
				})
				if err != nil {
					t.Errorf("cleanup notification fixture: %v", err)
				}
			})
			sender := &policyRecordingMailer{}
			worker, err := NewWorker(pool, sender)
			if err != nil {
				t.Fatal(err)
			}
			target := recipient{Email: "history@example.test"}
			for range 2 {
				if err := worker.deliver(ctx, event, "test class", target); err != nil {
					t.Fatal(err)
				}
			}
			if sender.calls != 0 {
				t.Fatalf("accepted mail was sent again: %d", sender.calls)
			}
			var receipts, deliveries int
			err = store.InTenantTx(ctx, pool, classID, func(tx pgx.Tx) error {
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_log WHERE event_id=$1::uuid AND recipient=$2`, event.ID, target.Email).Scan(&receipts); err != nil {
					return err
				}
				return tx.QueryRow(ctx, `SELECT count(*) FROM mail_delivery WHERE event_id=$1::uuid`, sourceID).Scan(&deliveries)
			})
			if err != nil || receipts != 1 || deliveries != 1 {
				t.Fatalf("receipt=%d delivery=%d err=%v", receipts, deliveries, err)
			}
		})
	}
}
