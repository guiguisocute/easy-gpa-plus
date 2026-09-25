package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"easygpa/backend/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type governanceClockActorKey struct{}
type governanceSink struct {
	header http.Header
	status int
}

func (w *governanceSink) Header() http.Header    { return w.header }
func (w *governanceSink) WriteHeader(status int) { w.status = status }
func (w *governanceSink) Write(raw []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return len(raw), nil
}

// This private in-process router has no listener and never casts votes. The
// same transaction, lockdown and decision handlers close due ballots. Multiple
// API replicas serialize on the class advisory lock and execution is idempotent.
func (s *Server) governanceScheduler(ctx context.Context) {
	if s.deps.Pools == nil || s.deps.Pools.Ops == nil {
		return
	}
	r := gin.New()
	r.Use(gin.Recovery(), func(c *gin.Context) {
		actor, ok := c.Request.Context().Value(governanceClockActorKey{}).(Actor)
		if !ok {
			c.AbortWithStatus(404)
			return
		}
		c.Set(actorContextKey, actor)
		c.Set("governance.automatic", true)
	}, s.tenantTransaction(), s.maintenanceModeMiddleware(), s.classLockdownMiddleware(), s.governanceGuard())
	r.POST("/api/v1/governance/proposals/:id/close", s.closeGovernanceProposal)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		func() {
			scan, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			rows, err := s.deps.Pools.Ops.Query(scan, `SELECT id,class_id,author_id FROM governance_due_proposals()`)
			if err != nil {
				slog.Error("governance scheduler scan failed", "error", err)
				return
			}
			type task struct{ id, class, user int64 }
			tasks := []task{}
			for rows.Next() {
				var t task
				if err = rows.Scan(&t.id, &t.class, &t.user); err != nil {
					break
				}
				tasks = append(tasks, t)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				slog.Error("governance scheduler scan failed", "error", err)
				return
			}
			for _, t := range tasks {
				work, stop := context.WithTimeout(ctx, 30*time.Second)
				actor := Actor{UserID: t.user, ClassID: t.class, Role: "student"}
				request, e := http.NewRequestWithContext(context.WithValue(work, governanceClockActorKey{}, actor), "POST", fmt.Sprintf("http://internal/api/v1/governance/proposals/%d/close", t.id), nil)
				if e == nil {
					sink := &governanceSink{header: make(http.Header)}
					r.ServeHTTP(sink, request)
					if sink.status >= 500 {
						slog.Error("governance decision retry scheduled", "proposal", t.id, "status", sink.status)
					}
				}
				// Back off blocked/error cases so they cannot starve later due ballots.
				e = store.InTenantTx(work, s.deps.Pools.App, t.class, func(tx pgx.Tx) error {
					_, e := tx.Exec(work, `UPDATE governance_proposal SET next_check_at=now()+interval '15 minutes' WHERE id=$1`, t.id)
					return e
				})
				if e != nil && ctx.Err() == nil {
					slog.Error("governance retry schedule failed", "proposal", t.id, "error", e)
				}
				stop()
			}
		}()
	}
}
