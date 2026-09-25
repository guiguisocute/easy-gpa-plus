DROP TABLE IF EXISTS review_sla_config;
DROP INDEX IF EXISTS scorecard_audit_student_recent;
DROP INDEX IF EXISTS scorecard_audit_one_active_student;
CREATE UNIQUE INDEX scorecard_audit_one_active_scheme
    ON scorecard_audit_batch (class_id,scheme_id)
    WHERE status IN ('generating','blocked','open','resolving');
ALTER TABLE scorecard_audit_batch
    DROP CONSTRAINT IF EXISTS scorecard_audit_batch_student_fk,
    DROP COLUMN IF EXISTS invalidated_at,
    DROP COLUMN IF EXISTS invalidated_reason,
    DROP COLUMN IF EXISTS student_id;
