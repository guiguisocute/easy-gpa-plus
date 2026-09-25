package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type queueTestMail struct {
	mu       sync.Mutex
	messages []Message
}

func (m *queueTestMail) Send(_ context.Context, msg Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return "fixture-message", nil
}

type queueTestSettings struct{ mail opsconfig.Mail }

func (s *queueTestSettings) Mail(context.Context) (opsconfig.Mail, error) { return s.mail, nil }

func TestMailQueueConsentBurstPauseLimitsAndTenantBoundary(t *testing.T) {
	url := os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL")
	if url == "" {
		t.Skip("set EASYGPA_TEST_ADMIN_DATABASE_URL for mail queue database regression")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	rolePool := func(role string) *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, err := c.Exec(ctx, "SET ROLE "+role); return err }
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	app, ops := rolePool("easygpa_app"), rolePool("easygpa_ops")
	var classID, userID, whitelistID int64
	if err = admin.QueryRow(ctx, `INSERT INTO class(name,archived) VALUES('mail queue fixture',false) RETURNING id`).Scan(&classID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		for _, table := range []string{"mail_notification", "mail_batch", "mail_preference", "mail_delivery", "notification_log", "outbox_event"} {
			if _, err := admin.Exec(cleanup, "DELETE FROM "+table+" WHERE class_id=$1", classID); err != nil {
				t.Error(err)
			}
		}
		if _, err := admin.Exec(cleanup, `DELETE FROM class WHERE id=$1`, classID); err != nil {
			t.Error(err)
		}
	})
	sid := fmt.Sprintf("MAIL-FIXTURE-%d", classID)
	if err = admin.QueryRow(ctx, `INSERT INTO whitelist(class_id,sid,name) VALUES($1,$2,'测试成员') RETURNING id`, classID, sid).Scan(&whitelistID); err != nil {
		t.Fatal(err)
	}
	if err = admin.QueryRow(ctx, `UPDATE app_user SET password_hash='fixture',status='active' WHERE class_id=$1 AND whitelist_id=$2 AND sid=$3 RETURNING id`, classID, whitelistID, sid).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	address := fmt.Sprintf("queue-%d@example.invalid", classID)
	if _, err = admin.Exec(ctx, `INSERT INTO user_email(class_id,user_id,email,email_normalized,is_primary,verified_at) VALUES($1,$2,$3,$3,true,now())`, classID, userID, address); err != nil {
		t.Fatal(err)
	}
	settings := &queueTestSettings{mail: opsconfig.Mail{NotificationsEnabled: true, NotificationsSince: time.Now().Add(-time.Minute)}}
	sender := &queueTestMail{}
	audited, err := NewAuditedMailer(sender, ops)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorker(app, audited)
	w.ConfigureQueue(ops, settings)
	var token string
	p := DefaultPreferences()
	p.QuietStart = "00:00"
	p.QuietEnd = "00:00"
	p.Categories["receipts"] = MailImmediate
	p.DailyLimit = 1
	if err = store.InTenantTx(ctx, app, classID, func(tx pgx.Tx) error {
		_, value, err := LoadPreferences(ctx, tx, classID, userID)
		token = value
		if err != nil {
			return err
		}
		return SavePreferences(ctx, tx, classID, userID, p)
	}); err != nil {
		t.Fatal(err)
	}
	event := func(entity int, at time.Time) events.Event {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"studentId": userID, "submissionId": entity})
		e := events.Event{ClassID: classID, Type: events.SealConfirmed, Payload: raw}
		if err := admin.QueryRow(ctx, `INSERT INTO outbox_event(class_id,type,payload,created_at,published_at) VALUES($1,$2,$3,$4,now()) RETURNING id::text`, classID, e.Type, raw, at).Scan(&e.ID); err != nil {
			t.Fatal(err)
		}
		return e
	}
	enqueue := func(e events.Event) {
		t.Helper()
		if err := w.Handle(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	count := func(status string) int {
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM mail_notification WHERE user_id=$1 AND status=$2`, userID, status).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	enqueue(event(0, time.Now().Add(-time.Hour)))
	if count("pending") != 0 {
		t.Fatal("legacy backlog queued")
	}
	for i := 1; i <= 25; i++ {
		e := event(i, time.Now())
		enqueue(e)
		enqueue(e)
	}
	if count("pending") != 25 {
		t.Fatal("event replay duplicated queue")
	}
	enqueue(event(1, time.Now()))
	if count("pending") != 25 {
		t.Fatal("superseded material was not coalesced")
	}
	if _, err = admin.Exec(ctx, `UPDATE mail_notification SET due_at=now()-interval '1 second' WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	reserved, prepareErr := w.prepareBatch(ctx, classID, userID, settings.mail.NotificationsSince)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	if reserved == "" {
		t.Fatalf("no batch reserved: pending=%d suppressed=%d", count("pending"), count("suppressed"))
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			if err := w.Flush(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(sender.messages) != 1 || count("sent") != 25 {
		var state string
		var next time.Time
		var dbNow time.Time
		_ = admin.QueryRow(ctx, `SELECT status,next_attempt_at,now() FROM mail_batch WHERE id=$1::uuid`, reserved).Scan(&state, &next, &dbNow)
		t.Fatalf("burst sent %d messages; %d items, batch=%s pending=%d suppressed=%d next=%s dbNow=%s goNow=%s", len(sender.messages), count("sent"), state, count("pending"), count("suppressed"), next, dbNow, time.Now())
	}
	if sender.messages[0].Template != TemplateNotificationAlert || sender.messages[0].Data["opt_out_token"] != token {
		t.Fatal("new template/opt-out capability missing")
	}
	enqueue(event(30, time.Now()))
	if _, err = admin.Exec(ctx, `UPDATE mail_notification SET due_at=now()-interval '1 second' WHERE user_id=$1 AND status='pending'`, userID); err != nil {
		t.Fatal(err)
	}
	if err = w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 {
		t.Fatal("daily recipient cap exceeded")
	}
	var ok bool
	if err = app.QueryRow(ctx, `SELECT mail_opt_out($1,'receipts')`, token).Scan(&ok); err != nil || !ok {
		t.Fatalf("anonymous opt-out failed: %v", err)
	}
	if count("pending") != 0 {
		t.Fatal("category opt-out retained pending items")
	}
	enqueue(event(31, time.Now()))
	if count("pending") != 0 {
		t.Fatal("category opt-out did not persist")
	}
	if err = app.QueryRow(ctx, `SELECT mail_opt_out($1,'all')`, token).Scan(&ok); err != nil || !ok {
		t.Fatal(err)
	}
	if err = app.QueryRow(ctx, `SELECT mail_opt_out($1,'all')`, token).Scan(&ok); err != nil || !ok {
		t.Fatal("repeat opt-out must be idempotent")
	}
	if err = app.QueryRow(ctx, `SELECT mail_opt_out($1,'all')`, strings.Repeat("x", 43)).Scan(&ok); err != nil || ok {
		t.Fatal("guessed token accepted")
	}
	if err = store.InTenantTx(ctx, app, classID+100000, func(tx pgx.Tx) error {
		var n int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM mail_preference WHERE user_id=$1`, userID).Scan(&n)
		if n != 0 {
			t.Error("cross-tenant preference leak")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.InTenantTx(ctx, app, classID, func(tx pgx.Tx) error { return SavePreferences(ctx, tx, classID, userID, p) }); err != nil {
		t.Fatal(err)
	}
	enqueue(event(32, time.Now()))
	if count("pending") != 1 {
		t.Fatal("explicit preference re-enable failed")
	}
	settings.mail.NotificationsEnabled = false
	if err = w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	enqueue(event(33, time.Now()))
	if count("pending") != 0 {
		t.Fatal("pause kept an email backlog")
	}
	settings.mail.NotificationsEnabled = true
	settings.mail.NotificationsSince = time.Now()
	if err = w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 {
		t.Fatal("unpause sent old events")
	}
	p.DailyLimit = 8
	p.Categories["receipts"] = MailDigest
	if err = store.InTenantTx(ctx, app, classID, func(tx pgx.Tx) error { return SavePreferences(ctx, tx, classID, userID, p) }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{34, 35} {
		enqueue(event(id, time.Now()))
	}
	if _, err = admin.Exec(ctx, `UPDATE mail_notification SET due_at=now()-interval '1 second' WHERE user_id=$1 AND status='pending'`, userID); err != nil {
		t.Fatal(err)
	}
	if err = w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 2 || sender.messages[1].Template != TemplateNotificationDigest {
		t.Fatalf("daily digest did not combine updates: messages=%d pending=%d sent=%d suppressed=%d", len(sender.messages), count("pending"), count("sent"), count("suppressed"))
	}
	enqueue(event(36, time.Now()))
	if _, err = admin.Exec(ctx, `UPDATE mail_notification SET due_at=now()-interval '1 second' WHERE user_id=$1 AND status='pending'`, userID); err != nil {
		t.Fatal(err)
	}
	if err = w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 2 {
		t.Fatal("more than one digest sent in a day")
	}
	// Explicit high frequency emits one current event per message. Replay,
	// limits, downgrade of reserved mail and opt-out still apply.
	if err = app.QueryRow(ctx, `SELECT mail_opt_out($1,'receipts')`, token).Scan(&ok); err != nil || !ok {
		t.Fatal("clear pending digest before choosing frequent")
	}
	p.Categories["receipts"], p.DailyLimit = MailFrequent, 6
	if err = store.InTenantTx(ctx, app, classID, func(tx pgx.Tx) error { return SavePreferences(ctx, tx, classID, userID, p) }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{100, 101, 102} {
		e := event(id, time.Now())
		enqueue(e)
		enqueue(e)
	}
	for i := range 2 {
		if err = w.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		if len(sender.messages) != 3+i || !sender.messages[2+i].Frequent || sender.messages[2+i].Text != "操作回执：1 项更新" {
			t.Fatal("frequent choice must send one deduplicated event")
		}
	}
	reserved, err = w.prepareBatch(ctx, classID, userID, settings.mail.NotificationsSince)
	if err != nil || reserved == "" {
		t.Fatalf("reserve frequent message: %v", err)
	}
	p.Categories["receipts"] = MailDigest
	if err = store.InTenantTx(ctx, app, classID, func(tx pgx.Tx) error { return SavePreferences(ctx, tx, classID, userID, p) }); err != nil {
		t.Fatal(err)
	}
	if err = w.sendBatch(ctx, classID, reserved); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 4 || count("pending") != 1 {
		t.Fatal("switching reserved frequent mail to digest sent or lost it")
	}
	p.Categories["receipts"], p.DailyLimit = MailFrequent, 4
	if err = store.InTenantTx(ctx, app, classID, func(tx pgx.Tx) error { return SavePreferences(ctx, tx, classID, userID, p) }); err != nil {
		t.Fatal(err)
	}
	if err = w.Flush(ctx); err != nil || len(sender.messages) != 4 {
		t.Fatal("frequent mode bypassed the user's daily cap")
	}
	if err = app.QueryRow(ctx, `SELECT mail_opt_out($1,'all')`, token).Scan(&ok); err != nil || !ok {
		t.Fatal("frequent opt-out failed")
	}
	enqueue(event(103, time.Now()))
	if err = w.Flush(ctx); err != nil || len(sender.messages) != 4 || count("pending") != 0 {
		t.Fatal("frequent delivery ignored opt-out")
	}
	if err = saveSuppression(ctx, ops, address, "business", "provider_unsubscribed"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM mail_suppression WHERE recipient_hash=md5($1)`, address)
	})
	guard := NewSuppressionMailer(sender, ops)
	if _, err = guard.Send(ctx, Message{To: address, Template: TemplateNotificationAlert}); err == nil {
		t.Fatal("provider unsubscribe did not block mail")
	}
	if _, err = guard.Send(ctx, Message{To: address, Template: TemplateVerificationCode}); err != nil {
		t.Fatal("business unsubscribe blocked requested authentication")
	}
}
