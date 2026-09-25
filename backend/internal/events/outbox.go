package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Enqueue writes a typed event to the transactional outbox. The caller owns
// the transaction, so the event commits or rolls back with the business write.
func Enqueue[T any](ctx context.Context, tx pgx.Tx, classID int64, kind Kind[T], payload T) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", kind.Name(), err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_event (class_id,type,payload) VALUES ($1,$2,$3)`, classID, kind.Name(), raw); err != nil {
		return fmt.Errorf("enqueue %s event: %w", kind.Name(), err)
	}
	return nil
}
