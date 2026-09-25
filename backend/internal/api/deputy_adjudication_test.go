package api

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type adjudicationTx struct {
	pgx.Tx
	role string
	err  error
}

func (tx adjudicationTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return adjudicationRow{role: tx.role, err: tx.err}
}

type adjudicationRow struct {
	role string
	err  error
}

func (row adjudicationRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	switch value := dest[0].(type) {
	case *string:
		*value = row.role
	case *bool:
		*value = row.role == "class_admin"
	}
	return nil
}

func TestAuthorizeAdjudicationScope(t *testing.T) {
	tests := []struct {
		name      string
		actor     Actor
		studentID int64
		role      string
		wantCode  string
	}{
		{name: "administrator handles student", actor: Actor{UserID: 1, Role: "class_admin"}, studentID: 2},
		{name: "administrator must recuse from own matter", actor: Actor{UserID: 1, Role: "class_admin"}, studentID: 1, wantCode: "avoid_self"},
		{name: "deputy handles administrator", actor: Actor{UserID: 2, ClassID: 8, Role: "group", IsDeputy: true}, studentID: 1, role: "class_admin"},
		{name: "deputy cannot handle ordinary member", actor: Actor{UserID: 2, ClassID: 8, Role: "group", IsDeputy: true}, studentID: 3, role: "student", wantCode: "deputy_scope"},
		{name: "deputy must recuse from own matter", actor: Actor{UserID: 2, ClassID: 8, Role: "group", IsDeputy: true}, studentID: 2, role: "group", wantCode: "avoid_self"},
		{name: "ordinary reviewer cannot adjudicate", actor: Actor{UserID: 2, ClassID: 8, Role: "group"}, studentID: 1, role: "class_admin", wantCode: "forbidden"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := authorizeAdjudication(context.Background(), adjudicationTx{role: test.role}, test.actor, test.studentID)
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("authorizeAdjudication() error = %v", err)
				}
				return
			}
			var problem adjudicationProblem
			if !errors.As(err, &problem) || problem.code != test.wantCode {
				t.Fatalf("authorizeAdjudication() error = %#v, want code %q", err, test.wantCode)
			}
		})
	}
}

func TestAuthorizeAdjudicationPropagatesDatabaseFailure(t *testing.T) {
	want := errors.New("database unavailable")
	actor := Actor{UserID: 2, ClassID: 8, Role: "group", IsDeputy: true}
	if got := authorizeAdjudication(context.Background(), adjudicationTx{err: want}, actor, 1); !errors.Is(got, want) {
		t.Fatalf("authorizeAdjudication() error = %v, want %v", got, want)
	}
}

func TestDeputyCanReadOnlyAdministratorTarget(t *testing.T) {
	deputy := Actor{UserID: 2, ClassID: 8, Role: "group", IsDeputy: true}
	allowed, err := deputyCanReadTarget(context.Background(), adjudicationTx{role: "class_admin"}, deputy, 1)
	if err != nil || !allowed {
		t.Fatalf("deputyCanReadTarget(admin) = %v, %v", allowed, err)
	}
	allowed, err = deputyCanReadTarget(context.Background(), adjudicationTx{role: "student"}, deputy, 3)
	if err != nil || allowed {
		t.Fatalf("deputyCanReadTarget(student) = %v, %v", allowed, err)
	}
	allowed, err = deputyCanReadTarget(context.Background(), adjudicationTx{role: "class_admin"}, Actor{Role: "group"}, 1)
	if err != nil || allowed {
		t.Fatalf("ordinary group read = %v, %v", allowed, err)
	}
}
