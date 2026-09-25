// Package store contains explicit PostgreSQL transaction helpers. Business
// code receives pgx.Tx, never a bare pool, after authentication.
package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pools struct {
	App *pgxpool.Pool
	Ops *pgxpool.Pool
}

func Open(ctx context.Context, appURL, opsURL string) (*Pools, error) {
	if appURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	app, err := openPool(ctx, appURL)
	if err != nil {
		return nil, fmt.Errorf("open app database: %w", err)
	}
	pools := &Pools{App: app}
	if err := validateAppRole(ctx, app); err != nil {
		app.Close()
		return nil, err
	}
	if opsURL != "" {
		pools.Ops, err = openPool(ctx, opsURL)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("open ops database: %w", err)
		}
		if err := validateOpsRole(ctx, pools.Ops); err != nil {
			pools.Ops.Close()
			app.Close()
			return nil, err
		}
	}
	return pools, nil
}

func openPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 16
	cfg.MinIdleConns = 1
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func (p *Pools) Close() {
	if p == nil {
		return
	}
	if p.App != nil {
		p.App.Close()
	}
	if p.Ops != nil {
		p.Ops.Close()
	}
}

// BeginTenant starts the request transaction and injects class_id with the
// transaction-local form of set_config. The value cannot leak through pooling.
func BeginTenant(ctx context.Context, pool *pgxpool.Pool, classID int64) (pgx.Tx, error) {
	return BeginTenantWithOptions(ctx, pool, classID, pgx.TxOptions{})
}

func BeginTenantWithOptions(ctx context.Context, pool *pgxpool.Pool, classID int64, options pgx.TxOptions) (pgx.Tx, error) {
	if classID <= 0 {
		return nil, errors.New("class ID must be positive")
	}
	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_class', $1, true)", strconv.FormatInt(classID, 10)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("set tenant context: %w", err)
	}
	return tx, nil
}

// InTenantTx is useful outside HTTP middleware, notably event workers.
func InTenantTx(ctx context.Context, pool *pgxpool.Pool, classID int64, fn func(pgx.Tx) error) error {
	tx, err := BeginTenant(ctx, pool, classID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tenant transaction: %w", err)
	}
	return nil
}

func validateAppRole(ctx context.Context, pool *pgxpool.Pool) error {
	var name string
	var superuser, bypassRLS bool
	err := pool.QueryRow(ctx, `
		SELECT current_user, r.rolsuper, r.rolbypassrls
		  FROM pg_roles r
		 WHERE r.rolname = current_user
	`).Scan(&name, &superuser, &bypassRLS)
	if err != nil {
		return fmt.Errorf("inspect app database role: %w", err)
	}
	if superuser || bypassRLS {
		return fmt.Errorf("database role %q must be NOSUPERUSER and NOBYPASSRLS", name)
	}
	return nil
}

func validateOpsRole(ctx context.Context, pool *pgxpool.Pool) error {
	var name string
	var superuser, bypassRLS bool
	err := pool.QueryRow(ctx, `
		SELECT current_user,r.rolsuper,r.rolbypassrls
		  FROM pg_roles r WHERE r.rolname=current_user
	`).Scan(&name, &superuser, &bypassRLS)
	if err != nil {
		return fmt.Errorf("inspect ops database role: %w", err)
	}
	if superuser || !bypassRLS {
		return fmt.Errorf("ops database role %q must be NOSUPERUSER and BYPASSRLS", name)
	}
	return nil
}
