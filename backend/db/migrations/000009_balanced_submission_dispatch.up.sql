-- Replace fixed category pairs with reproducible, balanced per-submission
-- reviewer assignments. This is migration 000009 because 000008 is already
-- deployed for the appeal/objection contract.

ALTER TABLE app_user
    ADD COLUMN dispatch_paused BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE class
    ADD COLUMN dispatch_auto BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE dispatch_run (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id    BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    scheme_id   BIGINT NOT NULL,
    policy      TEXT NOT NULL DEFAULT 'balanced_per_submission'
                CHECK (policy='balanced_per_submission'),
    random_seed BIGINT NOT NULL,
    avoid_self  BOOLEAN NOT NULL DEFAULT TRUE,
    trigger     TEXT NOT NULL CHECK (trigger IN ('auto','manual','rebalance','handover')),
    assigned    INTEGER NOT NULL DEFAULT 0 CHECK (assigned >= 0),
    blocked     INTEGER NOT NULL DEFAULT 0 CHECK (blocked >= 0),
    created_by  BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id),
    FOREIGN KEY (class_id,created_by) REFERENCES app_user(class_id,id)
);

CREATE INDEX dispatch_run_recent
    ON dispatch_run (class_id,created_at DESC,id DESC);

CREATE TABLE submission_reviewer (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id      BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    submission_id BIGINT NOT NULL,
    reviewer_id   BIGINT NOT NULL,
    position      SMALLINT NOT NULL CHECK (position IN (1,2)),
    run_id        BIGINT,
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    assigned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,submission_id) REFERENCES submission(class_id,id) ON DELETE CASCADE,
    FOREIGN KEY (class_id,reviewer_id) REFERENCES app_user(class_id,id),
    FOREIGN KEY (class_id,run_id) REFERENCES dispatch_run(class_id,id)
);

CREATE UNIQUE INDEX submission_reviewer_active
    ON submission_reviewer (submission_id,reviewer_id) WHERE active;
CREATE UNIQUE INDEX submission_reviewer_position
    ON submission_reviewer (submission_id,position) WHERE active;
CREATE INDEX submission_reviewer_queue
    ON submission_reviewer (class_id,reviewer_id,submission_id) WHERE active;

-- Preserve the currently active category assignment as the initial queue.
INSERT INTO submission_reviewer
    (class_id,submission_id,reviewer_id,position,active,assigned_at)
SELECT s.class_id,s.id,ar.reviewer_id,ar.position,true,a.created_at
  FROM submission s
  JOIN assignment a ON a.class_id=s.class_id AND a.scheme_id=s.scheme_id
                   AND a.category_key=s.category_key AND a.active
  JOIN assignment_reviewer ar ON ar.class_id=a.class_id AND ar.assignment_id=a.id
 WHERE s.status<>'draft' AND ar.reviewer_id<>s.student_id
ON CONFLICT DO NOTHING;

-- Keep a historical submission_reviewer row for any old review that was made
-- under an assignment no longer active. These rows are not put back in queues.
WITH missing AS (
    SELECT r.class_id,r.submission_id,r.reviewer_id,r.created_at,
           row_number() OVER (PARTITION BY r.submission_id ORDER BY r.created_at,r.id) AS ordinal
      FROM review r
     WHERE NOT EXISTS (
         SELECT 1 FROM submission_reviewer sr
          WHERE sr.submission_id=r.submission_id AND sr.reviewer_id=r.reviewer_id
     )
)
INSERT INTO submission_reviewer
    (class_id,submission_id,reviewer_id,position,active,assigned_at)
SELECT class_id,submission_id,reviewer_id,((ordinal-1)%2+1)::smallint,false,created_at
  FROM missing;

ALTER TABLE review
    DROP CONSTRAINT IF EXISTS review_class_id_assignment_id_fkey;

UPDATE review r
   SET assignment_id=(
       SELECT sr.id FROM submission_reviewer sr
        WHERE sr.class_id=r.class_id AND sr.submission_id=r.submission_id
          AND sr.reviewer_id=r.reviewer_id
        ORDER BY sr.active DESC,sr.assigned_at DESC,sr.id DESC LIMIT 1
   );

ALTER TABLE review
    ADD CONSTRAINT review_submission_reviewer_fk
        FOREIGN KEY (class_id,assignment_id) REFERENCES submission_reviewer(class_id,id);

DROP TABLE assignment_reviewer;
DROP TABLE assignment;

ALTER TABLE dispatch_run ENABLE ROW LEVEL SECURITY;
ALTER TABLE dispatch_run FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON dispatch_run
    USING (class_id=app_current_class_id())
    WITH CHECK (class_id=app_current_class_id());

ALTER TABLE submission_reviewer ENABLE ROW LEVEL SECURITY;
ALTER TABLE submission_reviewer FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON submission_reviewer
    USING (class_id=app_current_class_id())
    WITH CHECK (class_id=app_current_class_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON dispatch_run,submission_reviewer TO easygpa_app;
        GRANT USAGE,SELECT ON SEQUENCE dispatch_run_id_seq,submission_reviewer_id_seq TO easygpa_app;
    END IF;
END
$$;
