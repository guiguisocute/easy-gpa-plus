-- Two-stage student appeals and reviewer-raised score objections.
-- Round 1 is reconsidered independently by the original review pair; round 2
-- and reviewer objections are finalized by the class administrator.

ALTER TABLE appeal
    DROP CONSTRAINT appeal_class_id_target_type_target_id_student_id_key;

ALTER TABLE appeal
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'student_appeal'
        CHECK (kind IN ('student_appeal', 'reviewer_objection')),
    ADD COLUMN round SMALLINT NOT NULL DEFAULT 1
        CHECK (round IN (1, 2)),
    ADD COLUMN filed_by BIGINT,
    ADD COLUMN original_score NUMERIC(10,3),
    ADD COLUMN proposed_score NUMERIC(10,3),
    ADD COLUMN proposal_reason TEXT;

UPDATE appeal a
   SET filed_by = a.student_id,
       original_score = CASE
           WHEN a.target_type = 'submission' THEN (
               SELECT s.final_score FROM submission s WHERE s.id = a.target_id
           )
           ELSE (
               SELECT b.score FROM base_score b WHERE b.id = a.target_id
           )
       END,
       status = CASE WHEN a.status IN ('filed', 'assigned') THEN 'escalated' ELSE a.status END,
       handler_id = CASE WHEN a.status IN ('filed', 'assigned') THEN NULL ELSE a.handler_id END;

ALTER TABLE appeal
    ALTER COLUMN filed_by SET NOT NULL,
    ADD CONSTRAINT appeal_filed_by_fk
        FOREIGN KEY (class_id, filed_by) REFERENCES app_user(class_id, id),
    ADD CONSTRAINT appeal_kind_round_check
        CHECK ((kind = 'student_appeal') OR (kind = 'reviewer_objection' AND round = 1)),
    ADD CONSTRAINT appeal_objection_proposal_check
        CHECK ((kind = 'student_appeal' AND proposed_score IS NULL AND proposal_reason IS NULL)
            OR (kind = 'reviewer_objection' AND proposed_score IS NOT NULL AND proposal_reason IS NOT NULL));

CREATE UNIQUE INDEX appeal_one_student_round
    ON appeal (class_id, target_type, target_id, student_id, round)
    WHERE kind = 'student_appeal';

CREATE UNIQUE INDEX appeal_one_active_reviewer_objection
    ON appeal (class_id, target_type, target_id)
    WHERE kind = 'reviewer_objection' AND status IN ('filed', 'assigned', 'escalated');

CREATE TABLE appeal_reviewer (
    class_id       BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    appeal_id      BIGINT NOT NULL,
    reviewer_id    BIGINT NOT NULL,
    position       SMALLINT NOT NULL CHECK (position IN (1, 2)),
    score          NUMERIC(10,3),
    reason         TEXT,
    spent_seconds  INTEGER CHECK (spent_seconds IS NULL OR spent_seconds >= 0),
    decided_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (appeal_id, reviewer_id),
    UNIQUE (appeal_id, position),
    FOREIGN KEY (class_id, appeal_id) REFERENCES appeal(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, reviewer_id) REFERENCES app_user(class_id, id),
    CHECK ((score IS NULL AND reason IS NULL AND spent_seconds IS NULL AND decided_at IS NULL)
        OR (score IS NOT NULL AND reason IS NOT NULL AND spent_seconds IS NOT NULL AND decided_at IS NOT NULL))
);

CREATE INDEX appeal_reviewer_inbox
    ON appeal_reviewer (class_id, reviewer_id, decided_at, appeal_id);

ALTER TABLE appeal_reviewer ENABLE ROW LEVEL SECURITY;
ALTER TABLE appeal_reviewer FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON appeal_reviewer
    USING (class_id = app_current_class_id())
    WITH CHECK (class_id = app_current_class_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON appeal_reviewer TO easygpa_app;
    END IF;
END
$$;
