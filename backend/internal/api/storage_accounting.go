package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
)

const storageReconcileHookKey = "easygpa.storage_reconcile_hook"

// reconcileClassStorage measures live S3 objects, not database declarations.
// This keeps failed presigns, extracted knowledge objects and export archives
// from making the tenant capacity number fictional.
func (s *Server) reconcileClassStorage(ctx context.Context, classID int64) error {
	if classID <= 0 || s.deps.Objects == nil || s.deps.Pools == nil || s.deps.Pools.Ops == nil {
		return errors.New("storage accounting dependencies are unavailable")
	}
	prefix := "class-" + strconv.FormatInt(classID, 10) + "/"
	objects, err := s.deps.Objects.ListPrefix(ctx, prefix)
	if err != nil {
		return err
	}
	var total int64
	for _, object := range objects {
		if object.Size < 0 {
			return fmt.Errorf("object %q has a negative size", object.Key)
		}
		total += object.Size
	}
	command, err := s.deps.Pools.Ops.Exec(ctx, `
		UPDATE class SET storage_bytes=$1,storage_calibrated_at=now(),updated_at=now() WHERE id=$2
	`, total, classID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("storage tenant no longer exists")
	}
	return nil
}

func (s *Server) scheduleStorageReconcile(c *gin.Context, classID int64) {
	if _, exists := c.Get(storageReconcileHookKey); exists {
		return
	}
	c.Set(storageReconcileHookKey, true)
	afterCommit(c, func(ctx context.Context) error { return s.reconcileClassStorage(ctx, classID) })
}

func (s *Server) removeObjectAfterCommit(c *gin.Context, classID int64, key string) {
	afterCommit(c, func(ctx context.Context) error { return s.deps.Objects.Remove(ctx, key) })
	s.scheduleStorageReconcile(c, classID)
}
