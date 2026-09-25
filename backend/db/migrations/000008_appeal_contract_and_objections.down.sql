DROP TABLE IF EXISTS objection;

ALTER TABLE appeal_reviewer
    DROP CONSTRAINT IF EXISTS appeal_reviewer_decision_check,
    DROP COLUMN IF EXISTS decision;
ALTER TABLE appeal_reviewer ADD CONSTRAINT appeal_reviewer_check CHECK (
    (score IS NULL AND reason IS NULL AND spent_seconds IS NULL AND decided_at IS NULL)
    OR (score IS NOT NULL AND reason IS NOT NULL AND spent_seconds IS NOT NULL AND decided_at IS NOT NULL)
);

ALTER TABLE appeal DROP CONSTRAINT IF EXISTS appeal_previous_round_fk;
ALTER TABLE appeal DROP COLUMN IF EXISTS previous_appeal_id;
ALTER TABLE appeal DROP CONSTRAINT IF EXISTS appeal_status_check;
UPDATE appeal SET status='assigned' WHERE status='reviewing';
ALTER TABLE appeal ADD CONSTRAINT appeal_status_check
    CHECK (status IN ('filed', 'assigned', 'resolved', 'escalated', 'final'));

CREATE UNIQUE INDEX appeal_one_active_reviewer_objection
    ON appeal (class_id, target_type, target_id)
    WHERE kind='reviewer_objection' AND status IN ('filed','assigned','escalated');
