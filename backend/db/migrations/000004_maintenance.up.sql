ALTER TABLE export_job DROP CONSTRAINT export_job_status_check;
ALTER TABLE export_job
    ADD CONSTRAINT export_job_status_check
    CHECK (status IN ('queued', 'running', 'complete', 'failed', 'expired'));

CREATE TABLE maintenance_run (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id    BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    job_name    TEXT NOT NULL CHECK (job_name IN ('window_reminder')),
    run_key     TEXT NOT NULL,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, job_name, run_key)
);

ALTER TABLE maintenance_run ENABLE ROW LEVEL SECURITY;
ALTER TABLE maintenance_run FORCE ROW LEVEL SECURITY;
CREATE POLICY maintenance_run_tenant_isolation ON maintenance_run
    USING (class_id = app_current_class_id())
    WITH CHECK (class_id = app_current_class_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON maintenance_run TO easygpa_app;
        GRANT USAGE, SELECT ON SEQUENCE maintenance_run_id_seq TO easygpa_app;
    END IF;
END
$$;
