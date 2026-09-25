DROP FUNCTION IF EXISTS auth_consume_class_token(BYTEA);
DROP FUNCTION IF EXISTS auth_find_class_token(BYTEA);
DROP FUNCTION IF EXISTS auth_find_whitelist(TEXT, TEXT);
DROP FUNCTION IF EXISTS auth_find_user(TEXT);

DROP TRIGGER IF EXISTS settlement_append_only ON settlement;
DROP TRIGGER IF EXISTS settlement_run_append_only ON settlement_run;
DROP TRIGGER IF EXISTS scheme_published_immutable ON scheme;
DROP TRIGGER IF EXISTS ops_audit_append_only ON ops_audit;
DROP TRIGGER IF EXISTS audit_log_append_only ON audit_log;
DROP FUNCTION IF EXISTS reject_published_scheme_mutation();
DROP FUNCTION IF EXISTS reject_mutation();

DROP TABLE IF EXISTS backup_job;
DROP TABLE IF EXISTS ops_audit;
DROP TABLE IF EXISTS ops_config;
DROP TABLE IF EXISTS platform_template;
DROP TABLE IF EXISTS outbox_event;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS mail_delivery;
DROP TABLE IF EXISTS notification_log;
DROP TABLE IF EXISTS export_job;
DROP TABLE IF EXISTS settlement;
DROP TABLE IF EXISTS settlement_invalidation;
DROP TABLE IF EXISTS settlement_run;
DROP TABLE IF EXISTS gate_override;
DROP TABLE IF EXISTS student_gpa;
DROP TABLE IF EXISTS seal;
DROP TABLE IF EXISTS evidence;
DROP TABLE IF EXISTS appeal;
DROP TABLE IF EXISTS base_score;
DROP TABLE IF EXISTS review;
DROP TABLE IF EXISTS assignment_reviewer;
DROP TABLE IF EXISTS assignment;
DROP TABLE IF EXISTS submission;
DROP TABLE IF EXISTS scheme;
DROP TABLE IF EXISTS user_email;
DROP TABLE IF EXISTS app_user;
DROP TABLE IF EXISTS whitelist;
DROP TABLE IF EXISTS class_token;

DROP POLICY IF EXISTS class_tenant_isolation ON class;
ALTER TABLE class DISABLE ROW LEVEL SECURITY;
DROP FUNCTION IF EXISTS app_current_class_id();

DROP INDEX IF EXISTS class_slug_unique;
ALTER TABLE class
    DROP COLUMN IF EXISTS slug,
    DROP COLUMN IF EXISTS storage_bytes,
    DROP COLUMN IF EXISTS archived_at,
    DROP COLUMN IF EXISTS updated_at;
