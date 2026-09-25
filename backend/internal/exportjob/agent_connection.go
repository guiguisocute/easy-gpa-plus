package exportjob

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

var errAgentConnectionRevoked = errors.New("MCP connection expired or revoked; export cancelled")

// Background work rechecks the connection both before reading the snapshot and
// before publishing its result. The lock orders finalization with revocation.
func validateJobAgentConnection(ctx context.Context, tx pgx.Tx, id *int64, userID int64) error {
	if id == nil {
		return nil
	}
	var valid int64
	err := tx.QueryRow(ctx, `SELECT ac.id FROM agent_connection ac JOIN app_user u ON u.id=ac.user_id AND u.class_id=ac.class_id JOIN class c ON c.id=ac.class_id WHERE ac.id=$1 AND ac.user_id=$2 AND ac.revoked_at IS NULL AND ac.expires_at>now() AND ac.scopes @> ARRAY['export']::text[] AND u.status='active' AND u.role='class_admin' AND u.token_version=ac.auth_version AND NOT c.archived FOR SHARE OF ac,u,c`, *id, userID).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return errAgentConnectionRevoked
	}
	return err
}
