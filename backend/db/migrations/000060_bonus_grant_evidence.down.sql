-- Roll back only before uploads exist; never discard persisted evidence.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM evidence WHERE bonus_upload_id IS NOT NULL) THEN
        RAISE EXCEPTION 'Cannot roll back bonus evidence while uploaded files exist';
    END IF;
END $$;
ALTER TABLE evidence DROP CONSTRAINT evidence_one_owner_check;
ALTER TABLE evidence DROP COLUMN bonus_upload_id;
ALTER TABLE evidence ADD CONSTRAINT evidence_one_owner_check CHECK (
    num_nonnulls(submission_id,appeal_id,objection_id,report_id,blind_assignment_id,review_report_id)=1);
DROP TABLE bonus_grant_upload;
