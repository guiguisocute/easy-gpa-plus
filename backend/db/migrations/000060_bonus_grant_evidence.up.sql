-- Uploads are private to their creator until an atomic bonus grant consumes them.
CREATE TABLE bonus_grant_upload (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id BIGINT NOT NULL REFERENCES class(id),
    created_by BIGINT NOT NULL,
    used_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,created_by) REFERENCES app_user(class_id,id),
    FOREIGN KEY (class_id,used_by) REFERENCES bonus_grant_batch(class_id,id)
);
ALTER TABLE bonus_grant_upload ENABLE ROW LEVEL SECURITY;
ALTER TABLE bonus_grant_upload FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bonus_grant_upload
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE evidence ADD COLUMN bonus_upload_id BIGINT;
ALTER TABLE evidence ADD CONSTRAINT evidence_bonus_upload_fkey
    FOREIGN KEY (class_id,bonus_upload_id) REFERENCES bonus_grant_upload(class_id,id) ON DELETE CASCADE;
ALTER TABLE evidence DROP CONSTRAINT evidence_one_owner_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_one_owner_check CHECK (
    num_nonnulls(submission_id,appeal_id,objection_id,report_id,blind_assignment_id,review_report_id,bonus_upload_id)=1);
ALTER TABLE evidence ADD CONSTRAINT evidence_bonus_upload_note_check CHECK (bonus_upload_id IS NULL OR kind='note');
CREATE INDEX evidence_by_bonus_upload ON evidence(class_id,bonus_upload_id) WHERE bonus_upload_id IS NOT NULL;
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON bonus_grant_upload TO easygpa_app;
        GRANT USAGE,SELECT ON SEQUENCE bonus_grant_upload_id_seq TO easygpa_app;
    END IF;
END $$;
