package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Keep quota locks in a namespace that cannot collide with business locks.
const userQuotaLockNamespace int64 = 0x454750415f71756f

const classQuotaLockNamespace int64 = 0x454750415f636c73

var errEvidenceDailyQuotaExceeded = errors.New("evidence daily quota exceeded")

func lockUserQuota(ctx context.Context, tx pgx.Tx, userID int64) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1::bigint)", userQuotaLockNamespace+userID)
	return err
}

func lockClassQuota(ctx context.Context, tx pgx.Tx, classID int64) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1::bigint)", classQuotaLockNamespace+classID)
	return err
}

// reserveEvidenceQuota serializes all evidence presigns for a user and
// accounts for the declared bytes before the row is inserted. Counting the
// reservation row (including rejected uploads) prevents a client from
// repeatedly presigning large objects and avoiding the byte budget by never
// completing them.
func reserveEvidenceQuota(ctx context.Context, tx pgx.Tx, userID, sizeBytes int64, dailyMB int) error {
	if userID <= 0 || sizeBytes <= 0 || dailyMB <= 0 {
		return fmt.Errorf("invalid evidence quota parameters")
	}
	if err := lockUserQuota(ctx, tx, userID); err != nil {
		return err
	}
	limit := int64(dailyMB) * 1024 * 1024
	if sizeBytes > limit {
		return errEvidenceDailyQuotaExceeded
	}
	var used int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(size_bytes),0) FROM evidence
		 WHERE created_by=$1 AND created_at>=date_trunc('day',now())
	`, userID).Scan(&used); err != nil {
		return err
	}
	if used > limit-sizeBytes {
		return errEvidenceDailyQuotaExceeded
	}
	return nil
}
