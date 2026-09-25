-- Student-scoped scorecard audit rounds. Existing batch rows remain valid
-- history; new rounds are keyed by the student that was sealed.
ALTER TABLE scorecard_audit_batch
    ADD COLUMN IF NOT EXISTS student_id BIGINT;

ALTER TABLE scorecard_audit_batch
    ADD CONSTRAINT scorecard_audit_batch_student_fk
    FOREIGN KEY (class_id,student_id) REFERENCES app_user(class_id,id);

DROP INDEX IF EXISTS scorecard_audit_one_active_scheme;
CREATE UNIQUE INDEX scorecard_audit_one_active_student
    ON scorecard_audit_batch (class_id,scheme_id,student_id)
    WHERE student_id IS NOT NULL
      AND status IN ('generating','blocked','open','resolving','complete');

CREATE INDEX scorecard_audit_student_recent
    ON scorecard_audit_batch (class_id,scheme_id,student_id,created_at DESC);

ALTER TABLE scorecard_audit_batch
    ADD COLUMN IF NOT EXISTS invalidated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS invalidated_reason TEXT;

UPDATE scorecard_audit_batch
   SET status='stale',stale_at=now(),stale_reason='replaced by student-scoped scorecard review',
       invalidated_at=now(),invalidated_reason='replaced by student-scoped scorecard review'
 WHERE student_id IS NULL AND status IN ('generating','blocked','open','resolving');

CREATE TABLE IF NOT EXISTS review_sla_config (
    class_id BIGINT PRIMARY KEY REFERENCES class(id) ON DELETE CASCADE,
    item_hours INTEGER NOT NULL DEFAULT 24 CHECK (item_hours BETWEEN 1 AND 168),
    scorecard_hours INTEGER NOT NULL DEFAULT 24 CHECK (scorecard_hours BETWEEN 1 AND 168),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON review_sla_config TO easygpa_app;
    END IF;
END
$$;

ALTER TABLE review_sla_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE review_sla_config FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON review_sla_config
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());

UPDATE class SET dispatch_auto=TRUE WHERE dispatch_auto IS DISTINCT FROM TRUE;
