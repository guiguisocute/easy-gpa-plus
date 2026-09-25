-- Preserve the student's filed classification, add append-only classification
-- decisions, and introduce frozen whole-scorecard double-blind review batches.

ALTER TABLE submission
    ADD COLUMN filed_category_key TEXT,
    ADD COLUMN filed_item_key TEXT,
    ADD COLUMN filed_rule_snapshot JSONB;

UPDATE submission
   SET filed_category_key=category_key,
       filed_item_key=item_key,
       filed_rule_snapshot=rule_snapshot;

ALTER TABLE submission
    ALTER COLUMN filed_category_key SET NOT NULL,
    ALTER COLUMN filed_item_key SET NOT NULL,
    ALTER COLUMN filed_rule_snapshot SET NOT NULL;

CREATE FUNCTION prevent_filed_classification_change() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.submitted_at IS NOT NULL AND (
        NEW.filed_category_key IS DISTINCT FROM OLD.filed_category_key
        OR NEW.filed_item_key IS DISTINCT FROM OLD.filed_item_key
        OR NEW.filed_rule_snapshot IS DISTINCT FROM OLD.filed_rule_snapshot
    ) THEN
        RAISE EXCEPTION 'filed classification is immutable after submission';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER submission_filed_classification_immutable
    BEFORE UPDATE OF filed_category_key,filed_item_key,filed_rule_snapshot ON submission
    FOR EACH ROW EXECUTE FUNCTION prevent_filed_classification_change();

ALTER TABLE settlement_run
    ADD COLUMN gate_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE scorecard_audit_batch (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    scheme_id       BIGINT NOT NULL,
    input_hash      TEXT NOT NULL CHECK (char_length(input_hash)=64),
    status          TEXT NOT NULL DEFAULT 'generating'
                    CHECK (status IN ('generating','blocked','open','resolving','complete','stale','failed')),
    random_seed     BIGINT NOT NULL,
    detail          JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    opened_at       TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    stale_at        TIMESTAMPTZ,
    stale_reason    TEXT,
    UNIQUE (class_id,id),
    UNIQUE (class_id,scheme_id,input_hash),
    FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id)
);

CREATE UNIQUE INDEX scorecard_audit_one_active_scheme
    ON scorecard_audit_batch (class_id,scheme_id)
    WHERE status IN ('generating','blocked','open','resolving');
CREATE INDEX scorecard_audit_recent
    ON scorecard_audit_batch (class_id,scheme_id,created_at DESC);

CREATE TABLE scorecard_audit_subject (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    batch_id        UUID NOT NULL,
    student_id      BIGINT NOT NULL,
    snapshot        JSONB NOT NULL,
    snapshot_hash   TEXT NOT NULL CHECK (char_length(snapshot_hash)=64),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id,id),
    UNIQUE (batch_id,student_id),
    FOREIGN KEY (class_id,batch_id) REFERENCES scorecard_audit_batch(class_id,id) ON DELETE CASCADE,
    FOREIGN KEY (class_id,student_id) REFERENCES app_user(class_id,id)
);

CREATE TABLE scorecard_audit_assignment (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    subject_id      BIGINT NOT NULL,
    reviewer_id     BIGINT NOT NULL,
    position        SMALLINT NOT NULL CHECK (position IN (1,2)),
    overlap_count   INTEGER NOT NULL DEFAULT 0 CHECK (overlap_count>=0),
    status          TEXT NOT NULL DEFAULT 'assigned'
                    CHECK (status IN ('assigned','submitted','superseded')),
    result          TEXT CHECK (result IS NULL OR result IN ('pass','issues')),
    note            TEXT NOT NULL DEFAULT '',
    assigned_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at    TIMESTAMPTZ,
    superseded_at   TIMESTAMPTZ,
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,subject_id) REFERENCES scorecard_audit_subject(class_id,id) ON DELETE CASCADE,
    FOREIGN KEY (class_id,reviewer_id) REFERENCES app_user(class_id,id),
    CHECK ((status='submitted' AND result IS NOT NULL AND submitted_at IS NOT NULL)
        OR (status<>'submitted' AND result IS NULL AND submitted_at IS NULL))
);

CREATE UNIQUE INDEX scorecard_audit_active_reviewer
    ON scorecard_audit_assignment (subject_id,reviewer_id) WHERE status<>'superseded';
CREATE UNIQUE INDEX scorecard_audit_active_position
    ON scorecard_audit_assignment (subject_id,position) WHERE status<>'superseded';
CREATE INDEX scorecard_audit_assignment_inbox
    ON scorecard_audit_assignment (class_id,reviewer_id,status,assigned_at,id);

CREATE TABLE scorecard_audit_flag (
    id                   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id             BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    assignment_id        BIGINT NOT NULL,
    target_submission_id BIGINT,
    kind                 TEXT NOT NULL CHECK (kind IN ('duplicate','eligibility','other')),
    reason               TEXT NOT NULL CHECK (char_length(reason) BETWEEN 4 AND 5000),
    status               TEXT NOT NULL DEFAULT 'draft'
                         CHECK (status IN ('draft','submitted','resolved','dismissed','withdrawn')),
    resolved_by          BIGINT,
    resolution_reason    TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at         TIMESTAMPTZ,
    resolved_at          TIMESTAMPTZ,
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,assignment_id) REFERENCES scorecard_audit_assignment(class_id,id) ON DELETE CASCADE,
    FOREIGN KEY (class_id,target_submission_id) REFERENCES submission(class_id,id),
    FOREIGN KEY (class_id,resolved_by) REFERENCES app_user(class_id,id),
    CHECK ((status IN ('resolved','dismissed') AND resolved_by IS NOT NULL AND resolution_reason IS NOT NULL AND resolved_at IS NOT NULL)
        OR (status NOT IN ('resolved','dismissed') AND resolved_by IS NULL AND resolution_reason IS NULL AND resolved_at IS NULL))
);

CREATE INDEX scorecard_audit_flag_admin_queue
    ON scorecard_audit_flag (class_id,status,submitted_at,id) WHERE status='submitted';

CREATE TABLE classification_suggestion (
    id                   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id             BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    scheme_id            BIGINT NOT NULL,
    submission_id        BIGINT NOT NULL,
    suggested_by         BIGINT NOT NULL,
    source               TEXT NOT NULL CHECK (source IN ('item_review','blind_audit','admin')),
    review_id            BIGINT,
    blind_assignment_id  BIGINT,
    from_category_key    TEXT NOT NULL,
    from_item_key        TEXT NOT NULL,
    to_category_key      TEXT NOT NULL,
    to_item_key          TEXT NOT NULL,
    to_rule_snapshot     JSONB NOT NULL,
    scope                TEXT NOT NULL CHECK (scope IN ('within_category','cross_category')),
    reason               TEXT NOT NULL CHECK (char_length(reason) BETWEEN 4 AND 5000),
    status               TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('draft','pending','accepted','rejected','superseded','withdrawn')),
    resolved_by          BIGINT,
    resolution_reason    TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at         TIMESTAMPTZ,
    resolved_at          TIMESTAMPTZ,
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id),
    FOREIGN KEY (class_id,submission_id) REFERENCES submission(class_id,id) ON DELETE CASCADE,
    FOREIGN KEY (class_id,suggested_by) REFERENCES app_user(class_id,id),
    FOREIGN KEY (class_id,review_id) REFERENCES review(class_id,id) ON DELETE SET NULL,
    FOREIGN KEY (class_id,blind_assignment_id) REFERENCES scorecard_audit_assignment(class_id,id) ON DELETE SET NULL,
    FOREIGN KEY (class_id,resolved_by) REFERENCES app_user(class_id,id),
    CHECK ((source='item_review' AND review_id IS NOT NULL AND blind_assignment_id IS NULL)
        OR (source='blind_audit' AND blind_assignment_id IS NOT NULL AND review_id IS NULL)
        OR (source='admin' AND review_id IS NULL AND blind_assignment_id IS NULL)),
    CHECK ((status IN ('accepted','rejected','superseded') AND resolved_by IS NOT NULL AND resolution_reason IS NOT NULL AND resolved_at IS NOT NULL)
        OR (status NOT IN ('accepted','rejected','superseded') AND resolved_by IS NULL AND resolution_reason IS NULL AND resolved_at IS NULL))
);

CREATE INDEX classification_suggestion_admin_queue
    ON classification_suggestion (class_id,status,scope,created_at,id)
    WHERE status='pending';
CREATE UNIQUE INDEX classification_suggestion_one_review
    ON classification_suggestion (review_id) WHERE review_id IS NOT NULL;
CREATE UNIQUE INDEX classification_suggestion_one_blind_assignment_submission
    ON classification_suggestion (blind_assignment_id,submission_id)
    WHERE blind_assignment_id IS NOT NULL AND status IN ('draft','pending');

CREATE TABLE classification_resolution (
    id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id              BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    scheme_id             BIGINT NOT NULL,
    submission_id         BIGINT NOT NULL,
    suggestion_id         BIGINT,
    appeal_id             BIGINT,
    origin_batch_id       UUID,
    decided_by            BIGINT NOT NULL,
    before_category_key   TEXT NOT NULL,
    before_item_key       TEXT NOT NULL,
    after_category_key    TEXT NOT NULL,
    after_item_key        TEXT NOT NULL,
    before_rule_snapshot  JSONB NOT NULL,
    after_rule_snapshot   JSONB NOT NULL,
    before_score          NUMERIC(10,3),
    after_score           NUMERIC(10,3),
    reason                TEXT NOT NULL CHECK (char_length(reason) BETWEEN 4 AND 5000),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id),
    FOREIGN KEY (class_id,submission_id) REFERENCES submission(class_id,id) ON DELETE CASCADE,
    FOREIGN KEY (class_id,suggestion_id) REFERENCES classification_suggestion(class_id,id),
    FOREIGN KEY (class_id,appeal_id) REFERENCES appeal(class_id,id),
    FOREIGN KEY (class_id,origin_batch_id) REFERENCES scorecard_audit_batch(class_id,id),
    FOREIGN KEY (class_id,decided_by) REFERENCES app_user(class_id,id)
);

CREATE INDEX classification_resolution_submission_history
    ON classification_resolution (class_id,submission_id,created_at,id);

ALTER TABLE objection
    ADD COLUMN blind_assignment_id BIGINT,
    ADD COLUMN origin_batch_id UUID,
    ADD CONSTRAINT objection_blind_assignment_fk
        FOREIGN KEY (class_id,blind_assignment_id) REFERENCES scorecard_audit_assignment(class_id,id) ON DELETE SET NULL,
    ADD CONSTRAINT objection_origin_batch_fk
        FOREIGN KEY (class_id,origin_batch_id) REFERENCES scorecard_audit_batch(class_id,id);

ALTER TABLE appeal
    ADD COLUMN original_category_key TEXT,
    ADD COLUMN original_item_key TEXT,
    ADD COLUMN proposed_category_key TEXT,
    ADD COLUMN proposed_item_key TEXT,
    ADD COLUMN original_rule_snapshot JSONB,
    ADD COLUMN resolution_category_key TEXT,
    ADD COLUMN resolution_item_key TEXT,
    ADD COLUMN resolution_rule_snapshot JSONB,
    ADD COLUMN origin_batch_id UUID,
    ADD CONSTRAINT appeal_origin_batch_fk
        FOREIGN KEY (class_id,origin_batch_id) REFERENCES scorecard_audit_batch(class_id,id);

ALTER TABLE appeal DROP CONSTRAINT IF EXISTS appeal_objection_proposal_check;
ALTER TABLE appeal ADD CONSTRAINT appeal_objection_proposal_check CHECK (
    kind='student_appeal'
    OR (kind='reviewer_objection' AND proposed_score IS NOT NULL AND proposal_reason IS NOT NULL)
);

ALTER TABLE appeal_reviewer
    ADD COLUMN category_key TEXT,
    ADD COLUMN item_key TEXT,
    ADD COLUMN rule_snapshot JSONB;

ALTER TABLE scorecard_audit_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE scorecard_audit_batch FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scorecard_audit_batch
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE scorecard_audit_subject ENABLE ROW LEVEL SECURITY;
ALTER TABLE scorecard_audit_subject FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scorecard_audit_subject
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE scorecard_audit_assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE scorecard_audit_assignment FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scorecard_audit_assignment
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE scorecard_audit_flag ENABLE ROW LEVEL SECURITY;
ALTER TABLE scorecard_audit_flag FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scorecard_audit_flag
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE classification_suggestion ENABLE ROW LEVEL SECURITY;
ALTER TABLE classification_suggestion FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON classification_suggestion
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE classification_resolution ENABLE ROW LEVEL SECURITY;
ALTER TABLE classification_resolution FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON classification_resolution
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON
            scorecard_audit_batch,scorecard_audit_subject,scorecard_audit_assignment,
            scorecard_audit_flag,
            classification_suggestion,classification_resolution TO easygpa_app;
        GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
    END IF;
END
$$;
