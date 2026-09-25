-- DESTRUCTIVE ROLLBACK: note attachments and unsubmitted appeal drafts have no
-- representation in the previous schema, so they must be removed first.
DELETE FROM evidence WHERE kind = 'note';
DELETE FROM appeal WHERE status = 'draft';

DROP INDEX IF EXISTS evidence_notes;
DROP INDEX IF EXISTS evidence_by_objection;

ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_objection_note_only_check;
ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_one_owner_check;
ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_class_id_objection_id_fkey;
ALTER TABLE evidence DROP COLUMN IF EXISTS kind;
ALTER TABLE evidence DROP COLUMN IF EXISTS objection_id;
ALTER TABLE evidence ADD CONSTRAINT evidence_check
    CHECK ((submission_id IS NOT NULL)::integer + (appeal_id IS NOT NULL)::integer = 1);

ALTER TABLE appeal DROP CONSTRAINT IF EXISTS appeal_status_check;
ALTER TABLE appeal ADD CONSTRAINT appeal_status_check
    CHECK (status IN ('filed', 'reviewing', 'resolved', 'escalated', 'final'));
