package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

const selfAdjudicationReason = "不能终裁涉及自己的事项，请由副班管处理"

// The deputy retains the group role. This scope is an additional, narrow
// authority over matters whose scored/reported subject is a class administrator.
func isDeputyAdjudication(c *gin.Context) bool {
	return strings.HasPrefix(c.FullPath(), "/api/v1/review/deputy/")
}

type adjudicationProblem struct {
	code    string
	message string
}

func (p adjudicationProblem) Error() string { return p.message }

func authorizeAdjudication(ctx context.Context, tx pgx.Tx, actor Actor, studentID int64) error {
	if grant, ok := ctx.Value(governanceExecutionKey{}).(governanceExecution); ok && grant.ID > 0 && grant.Subject != nil && *grant.Subject == studentID {
		return nil
	}
	if actor.UserID == studentID {
		return adjudicationProblem{"avoid_self", selfAdjudicationReason}
	}
	if actor.Role == "class_admin" {
		return nil
	}
	if actor.Role != "group" || !actor.IsDeputy {
		return adjudicationProblem{"forbidden", "无权终裁这项事项"}
	}
	var role string
	// Lock the target's role through the decision, just as the deputy route
	// locks the actor's appointment. A concurrent role change must not widen
	// the deputy's authority halfway through a transaction.
	err := tx.QueryRow(ctx, `SELECT role FROM app_user WHERE id=$1 AND class_id=$2 FOR SHARE`, studentID, actor.ClassID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role != "class_admin") {
		return adjudicationProblem{"deputy_scope", "副班管只能处理有关班管本人的仲裁与终裁事项"}
	}
	return err
}

func writeAdjudicationFailure(c *gin.Context, err error) bool {
	var problem adjudicationProblem
	if !errors.As(err, &problem) {
		return false
	}
	writeError(c, http.StatusForbidden, problem.code, problem.message, nil)
	return true
}

func requireAdjudicationTarget(c *gin.Context, tx pgx.Tx, actor Actor, studentID int64) bool {
	if err := authorizeAdjudication(c.Request.Context(), tx, actor, studentID); err != nil {
		if !writeAdjudicationFailure(c, err) {
			writeServiceError(c, err)
		}
		return false
	}
	return true
}

func deputyCanReadTarget(ctx context.Context, tx pgx.Tx, actor Actor, studentID int64) (bool, error) {
	if actor.Role != "group" || !actor.IsDeputy {
		return false, nil
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app_user WHERE id=$1 AND class_id=$2 AND role='class_admin')`, studentID, actor.ClassID).Scan(&allowed)
	return allowed, err
}

func withAdjudicationPermission(item gin.H, actor Actor, studentID int64) gin.H {
	item["canAdjudicate"] = actor.UserID != studentID
	item["recusalReason"] = ""
	if actor.UserID == studentID {
		item["recusalReason"] = selfAdjudicationReason
	}
	return item
}
