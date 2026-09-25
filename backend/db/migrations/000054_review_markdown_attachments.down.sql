-- 已有审核附件时拒绝降级，避免回滚静默丢失留档材料。
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM evidence WHERE blind_assignment_id IS NOT NULL OR review_report_id IS NOT NULL) THEN
        RAISE EXCEPTION 'Review attachments exist; preserve them before downgrading';
    END IF;
END $$;
ALTER TABLE evidence DROP CONSTRAINT evidence_one_owner_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_one_owner_check CHECK (
    num_nonnulls(submission_id,appeal_id,objection_id,report_id)=1);
ALTER TABLE evidence DROP CONSTRAINT evidence_review_note_only_check;
ALTER TABLE evidence DROP COLUMN blind_assignment_id;
ALTER TABLE evidence DROP COLUMN review_report_id;
