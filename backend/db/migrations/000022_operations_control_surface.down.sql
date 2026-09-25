DROP INDEX IF EXISTS template_share_ops_queue;
DROP FUNCTION IF EXISTS ops_agent_daily_usage();
DROP FUNCTION IF EXISTS ops_mail_delivery_log_count_v2(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ);
DROP FUNCTION IF EXISTS ops_mail_delivery_log_v2(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ,INTEGER,INTEGER);
DROP INDEX IF EXISTS template_share_one_pending;
DROP TABLE IF EXISTS template_share_request;

ALTER TABLE agent_attachment DROP CONSTRAINT IF EXISTS agent_attachment_size_bytes_check;
ALTER TABLE agent_attachment
    ADD CONSTRAINT agent_attachment_size_bytes_check
    CHECK (size_bytes > 0 AND size_bytes <= 5242880);

ALTER TABLE backup_job DROP CONSTRAINT IF EXISTS backup_job_status_check;
UPDATE backup_job SET status='failed' WHERE status='expired';
ALTER TABLE backup_job
    ADD CONSTRAINT backup_job_status_check
    CHECK (status IN ('queued','running','complete','failed'));

DELETE FROM ops_config WHERE key='lifecycle';
UPDATE ops_config SET value=value-'evidenceAllowedFormats',updated_at=now() WHERE key='flags';
ALTER TABLE class DROP COLUMN IF EXISTS storage_calibrated_at;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON platform_template TO easygpa_app;
    END IF;
END
$$;
