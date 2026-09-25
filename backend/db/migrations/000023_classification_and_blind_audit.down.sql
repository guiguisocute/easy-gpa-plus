ALTER TABLE appeal_reviewer
    DROP COLUMN IF EXISTS rule_snapshot,
    DROP COLUMN IF EXISTS item_key,
    DROP COLUMN IF EXISTS category_key;

ALTER TABLE appeal
    DROP CONSTRAINT IF EXISTS appeal_origin_batch_fk,
    DROP COLUMN IF EXISTS origin_batch_id,
    DROP COLUMN IF EXISTS resolution_rule_snapshot,
    DROP COLUMN IF EXISTS resolution_item_key,
    DROP COLUMN IF EXISTS resolution_category_key,
    DROP COLUMN IF EXISTS original_rule_snapshot,
    DROP COLUMN IF EXISTS proposed_item_key,
    DROP COLUMN IF EXISTS proposed_category_key,
    DROP COLUMN IF EXISTS original_item_key,
    DROP COLUMN IF EXISTS original_category_key;

ALTER TABLE appeal DROP CONSTRAINT IF EXISTS appeal_objection_proposal_check;
UPDATE appeal SET proposed_score=NULL,proposal_reason=NULL WHERE kind='student_appeal';
ALTER TABLE appeal ADD CONSTRAINT appeal_objection_proposal_check CHECK (
    (kind='student_appeal' AND proposed_score IS NULL AND proposal_reason IS NULL)
    OR (kind='reviewer_objection' AND proposed_score IS NOT NULL AND proposal_reason IS NOT NULL)
);

ALTER TABLE objection
    DROP CONSTRAINT IF EXISTS objection_origin_batch_fk,
    DROP CONSTRAINT IF EXISTS objection_blind_assignment_fk,
    DROP COLUMN IF EXISTS origin_batch_id,
    DROP COLUMN IF EXISTS blind_assignment_id;

DROP TABLE IF EXISTS classification_resolution;
DROP TABLE IF EXISTS classification_suggestion;
DROP TABLE IF EXISTS scorecard_audit_flag;
DROP TABLE IF EXISTS scorecard_audit_assignment;
DROP TABLE IF EXISTS scorecard_audit_subject;
DROP TABLE IF EXISTS scorecard_audit_batch;

DROP TRIGGER IF EXISTS submission_filed_classification_immutable ON submission;
DROP FUNCTION IF EXISTS prevent_filed_classification_change();

ALTER TABLE submission
    DROP COLUMN IF EXISTS filed_rule_snapshot,
    DROP COLUMN IF EXISTS filed_item_key,
    DROP COLUMN IF EXISTS filed_category_key;

ALTER TABLE settlement_run DROP COLUMN IF EXISTS gate_snapshot;
