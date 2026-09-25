-- Each request is one atomic, replay-safe class-admin grant, including self grants.
CREATE TABLE bonus_grant_batch (
    class_id BIGINT NOT NULL REFERENCES class(id),
    id UUID NOT NULL,
    created_by BIGINT NOT NULL,
    request JSONB NOT NULL,
    score NUMERIC(10,3) NOT NULL CHECK (score > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (class_id,id),
    FOREIGN KEY (class_id,created_by) REFERENCES app_user(class_id,id)
);
ALTER TABLE bonus_grant_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE bonus_grant_batch FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bonus_grant_batch
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT ON bonus_grant_batch TO easygpa_app;
    END IF;
END $$;
ALTER TABLE submission DROP CONSTRAINT submission_source_check;
ALTER TABLE submission ADD COLUMN bonus_grant_batch_id UUID;
ALTER TABLE submission ADD CONSTRAINT submission_source_check CHECK (source IN ('manual','ai','admin_grant'));
ALTER TABLE submission ADD CONSTRAINT submission_bonus_grant_fk
    FOREIGN KEY (class_id,bonus_grant_batch_id) REFERENCES bonus_grant_batch(class_id,id);
ALTER TABLE submission ADD CONSTRAINT submission_bonus_grant_source_check
    CHECK ((source='admin_grant') = (bonus_grant_batch_id IS NOT NULL));
CREATE UNIQUE INDEX submission_bonus_grant_student ON submission(class_id,bonus_grant_batch_id,student_id)
    WHERE bonus_grant_batch_id IS NOT NULL;
