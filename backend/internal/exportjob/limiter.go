package exportjob

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/opsconfig"
)

const exportLockNamespace int32 = 1163415632

type Lease interface {
	Release(context.Context)
}

type Limiter interface {
	Acquire(context.Context) (Lease, error)
}

type PostgresLimiter struct {
	pool   *pgxpool.Pool
	config *opsconfig.Store
}

func NewPostgresLimiter(pool *pgxpool.Pool, config *opsconfig.Store) (*PostgresLimiter, error) {
	if pool == nil || config == nil {
		return nil, errors.New("export limiter dependencies are required")
	}
	return &PostgresLimiter{pool: pool, config: config}, nil
}

func (l *PostgresLimiter) Acquire(ctx context.Context) (Lease, error) {
	flags, err := l.config.Flags(ctx)
	if err != nil {
		return nil, err
	}
	for slot := 0; slot < flags.ExportConcurrency; slot++ {
		conn, err := l.pool.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		var locked bool
		err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1,$2)`, exportLockNamespace, int32(slot)).Scan(&locked)
		if err != nil {
			conn.Release()
			return nil, err
		}
		if locked {
			return &postgresLease{conn: conn, slot: int32(slot)}, nil
		}
		conn.Release()
	}
	return nil, events.Defer(30*time.Second, "导出并发额度已满")
}

type postgresLease struct {
	conn *pgxpool.Conn
	slot int32
}

func (l *postgresLease) Release(ctx context.Context) {
	if l == nil || l.conn == nil {
		return
	}
	_, _ = l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1,$2)`, exportLockNamespace, l.slot)
	l.conn.Release()
	l.conn = nil
}
