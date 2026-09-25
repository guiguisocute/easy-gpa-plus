package api

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
)

func enqueueEvent(ctx context.Context, tx pgx.Tx, classID int64, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_event (class_id,type,payload) VALUES ($1,$2,$3)`, classID, eventType, raw)
	return err
}

func enqueueTypedEvent[T any](ctx context.Context, tx pgx.Tx, classID int64, kind events.Kind[T], payload T) error {
	return events.Enqueue(ctx, tx, classID, kind, payload)
}
