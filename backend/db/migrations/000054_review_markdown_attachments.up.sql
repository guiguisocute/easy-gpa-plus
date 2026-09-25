-- 终审附件绑定审核席位，举报复核附件与匿名举报原件使用不同归属。
-- 保留匿名举报附件不记录上传人的约束；审核正文仍实名留审计。
ALTER TABLE evidence ADD COLUMN blind_assignment_id BIGINT;
ALTER TABLE evidence ADD COLUMN review_report_id BIGINT;
ALTER TABLE evidence ADD CONSTRAINT evidence_blind_assignment_fkey
    FOREIGN KEY (class_id, blind_assignment_id) REFERENCES scorecard_audit_assignment(class_id,id) ON DELETE CASCADE;
ALTER TABLE evidence ADD CONSTRAINT evidence_review_report_fkey
    FOREIGN KEY (class_id, review_report_id) REFERENCES report(class_id,id) ON DELETE CASCADE;
ALTER TABLE evidence DROP CONSTRAINT evidence_one_owner_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_one_owner_check CHECK (
    num_nonnulls(submission_id,appeal_id,objection_id,report_id,blind_assignment_id,review_report_id)=1);
ALTER TABLE evidence ADD CONSTRAINT evidence_review_note_only_check
    CHECK ((blind_assignment_id IS NULL AND review_report_id IS NULL) OR kind='note');
CREATE INDEX evidence_by_blind_assignment ON evidence(class_id,blind_assignment_id) WHERE blind_assignment_id IS NOT NULL;
CREATE INDEX evidence_by_review_report ON evidence(class_id,review_report_id) WHERE review_report_id IS NOT NULL;
