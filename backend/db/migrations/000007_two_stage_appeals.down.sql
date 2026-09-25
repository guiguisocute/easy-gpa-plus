DROP TABLE IF EXISTS appeal_reviewer;

DROP INDEX IF EXISTS appeal_one_active_reviewer_objection;
DROP INDEX IF EXISTS appeal_one_student_round;

DELETE FROM appeal WHERE kind = 'reviewer_objection' OR round > 1;

ALTER TABLE appeal
    DROP CONSTRAINT IF EXISTS appeal_objection_proposal_check,
    DROP CONSTRAINT IF EXISTS appeal_kind_round_check,
    DROP CONSTRAINT IF EXISTS appeal_filed_by_fk,
    DROP COLUMN IF EXISTS proposal_reason,
    DROP COLUMN IF EXISTS proposed_score,
    DROP COLUMN IF EXISTS original_score,
    DROP COLUMN IF EXISTS filed_by,
    DROP COLUMN IF EXISTS round,
    DROP COLUMN IF EXISTS kind;

ALTER TABLE appeal
    ADD CONSTRAINT appeal_class_id_target_type_target_id_student_id_key
        UNIQUE (class_id, target_type, target_id, student_id);
