package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/store"
)

type Worker struct {
	pool *pgxpool.Pool
}

func NewWorker(pool *pgxpool.Pool) (*Worker, error) {
	if pool == nil {
		return nil, errors.New("dispatch worker database is required")
	}
	return &Worker{pool: pool}, nil
}

func (w *Worker) Handle(ctx context.Context, event events.Event) error {
	if event.Type != events.SubmissionCreated {
		return nil
	}
	payload, err := events.Decode(event, events.SubmissionCreatedEvent())
	if err != nil || payload.SubmissionID <= 0 {
		return errors.New("submission.created payload is incomplete")
	}
	return store.InTenantTx(ctx, w.pool, event.ClassID, func(tx pgx.Tx) error {
		var collective bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM class_governance WHERE class_id=$1 AND mode IN ('enrolling','collective'))`, event.ClassID).Scan(&collective); err != nil {
			return err
		}
		if collective {
			return nil
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ClassLockKey(event.ClassID)); err != nil {
			return err
		}
		var automatic bool
		if err := tx.QueryRow(ctx, `SELECT dispatch_auto FROM class WHERE id=$1`, event.ClassID).Scan(&automatic); err != nil {
			return err
		}
		if !automatic {
			return nil
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM submission_reviewer WHERE submission_id=$1 AND active)`, payload.SubmissionID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}

		var submission Submission
		var schemeID int64
		var status string
		if err := tx.QueryRow(ctx, `
			SELECT s.id,s.student_id,s.scheme_id,s.title,student.name,s.submitted_at,s.status
			  FROM submission s JOIN app_user student ON student.id=s.student_id
			 WHERE s.id=$1
		`, payload.SubmissionID).Scan(&submission.ID, &submission.StudentID, &schemeID, &submission.Title,
			&submission.Student, &submission.SubmittedAt, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		// submission.created is also emitted while a draft is being composed.
		// The submit endpoint emits it again, so the draft event is a safe no-op.
		if status != "pending" && status != "consensus" {
			return nil
		}
		submission.ReviewedBy = make(map[int64]bool)
		submission.AssignedReviewers = make(map[int64]bool)

		/* 均衡的基数必须跟管理员在分发页读到的是同一份（api.loadDispatchPool）。
		   这里曾经按这条提交自己的 scheme_id 统计，于是每发布一版新方案，所有人的累计量
		   就地归零：刚背了二十条旧方案条目的人会被当成闲人，新条目优先派给他。
		   现在同样以结算为界，换版不再重置任何人的工作量。 */
		rows, err := tx.Query(ctx, `
			SELECT u.id,u.sid,u.name,u.role,u.dispatch_paused,
			       (SELECT count(*) FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id
			         WHERE sr.reviewer_id=u.id AND s.status NOT IN ('draft','locked') AND sr.active),
			       (SELECT count(*) FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id
			         WHERE sr.reviewer_id=u.id AND s.status NOT IN ('draft','locked') AND sr.active
			           AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=sr.submission_id
			                            AND r.reviewer_id=u.id AND r.superseded_at IS NULL)),
			       (SELECT count(*) FROM review r JOIN submission s ON s.id=r.submission_id
			         WHERE r.reviewer_id=u.id AND s.status NOT IN ('draft','locked') AND r.superseded_at IS NULL)
			  FROM app_user u
			 WHERE u.status='active' AND u.role IN ('group','class_admin')
			 ORDER BY u.id
		`)
		if err != nil {
			return err
		}
		pool := make([]Reviewer, 0)
		loads := make(map[int64]Load)
		for rows.Next() {
			var reviewer Reviewer
			if err := rows.Scan(&reviewer.UserID, &reviewer.SID, &reviewer.Name, &reviewer.Role, &reviewer.Paused,
				&reviewer.Assigned, &reviewer.Pending, &reviewer.Done); err != nil {
				rows.Close()
				return err
			}
			pool = append(pool, reviewer)
			loads[reviewer.UserID] = Load{Assigned: reviewer.Assigned, Pending: reviewer.Pending}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE submission_reviewer sr SET active=false
			  FROM submission s
			 WHERE sr.submission_id=s.id AND sr.active AND sr.reviewer_id=s.student_id
			   AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=sr.reviewer_id AND r.superseded_at IS NULL)
		`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE scorecard_audit_assignment a SET status='superseded', superseded_at=now()
			  FROM scorecard_audit_subject subject
			 WHERE a.subject_id=subject.id AND a.status='assigned' AND a.reviewer_id=subject.student_id
		`); err != nil {
			return err
		}

		seed := eventSeed(event.ID)
		plan, err := Balance([]Submission{submission}, pool, loads, seed, true)
		if err != nil {
			return err
		}
		var runID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO dispatch_run (class_id,scheme_id,policy,random_seed,avoid_self,trigger,assigned,blocked,created_by)
			VALUES ($1,$2,$3,$4,true,'auto',$5,$6,NULL) RETURNING id
		`, event.ClassID, schemeID, Policy, seed, len(plan.Assignments), len(plan.Blocked)).Scan(&runID); err != nil {
			return err
		}
		reviewerIDs := make([]string, 0, 2)
		for _, assignment := range plan.Assignments {
			for position, reviewerID := range assignment.Reviewers {
				if _, err := tx.Exec(ctx, `
					INSERT INTO submission_reviewer (class_id,submission_id,reviewer_id,position,run_id)
					VALUES ($1,$2,$3,$4,$5)
				`, event.ClassID, assignment.SubmissionID, reviewerID, position+1, runID); err != nil {
					return err
				}
				reviewerIDs = append(reviewerIDs, strconv.FormatInt(reviewerID, 10))
			}
		}
		after, _ := json.Marshal(map[string]any{"plan": plan, "assigned": len(plan.Assignments)})
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,after_data,metadata)
			VALUES ($1,NULL,'system','dispatch.completed','dispatch_run',$2::bigint::text,$3,
			        jsonb_build_object('sourceEvent',$4::text,'trigger','auto'))
		`, event.ClassID, runID, after, event.ID); err != nil {
			return err
		}
		return events.Enqueue(ctx, tx, event.ClassID, events.DispatchDoneEvent(), events.DispatchDonePayload{
			RunID: strconv.FormatInt(runID, 10), Trigger: "auto", Assigned: len(plan.Assignments),
			Blocked: len(plan.Blocked), ReviewerIDs: reviewerIDs,
		})
	})
}

func eventSeed(id string) int64 {
	sum := sha256.Sum256([]byte(id))
	value := int64(binary.BigEndian.Uint64(sum[:8]) & (1<<63 - 1))
	if value == 0 {
		return 1
	}
	return value
}

// ClassLockKey serializes all automatic, batch and handover writes for one
// class. Loading counts after taking this lock is what makes incremental runs
// and concurrent event delivery safe.
func ClassLockKey(classID int64) int64 {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(classID))
	sum := sha256.Sum256(append([]byte("easygpa.dispatch.class\x00"), raw[:]...))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}
