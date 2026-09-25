-- Support appeal drafts and attachments embedded in review/decision markdown.

ALTER TABLE appeal DROP CONSTRAINT IF EXISTS appeal_status_check;
ALTER TABLE appeal ADD CONSTRAINT appeal_status_check
    CHECK (status IN ('draft', 'filed', 'reviewing', 'resolved', 'escalated', 'final'));

ALTER TABLE evidence
    ADD COLUMN objection_id BIGINT,
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'claim'
        CHECK (kind IN ('claim', 'note'));

ALTER TABLE evidence
    ADD CONSTRAINT evidence_class_id_objection_id_fkey
        FOREIGN KEY (class_id, objection_id) REFERENCES objection(class_id, id) ON DELETE CASCADE;

ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_one_owner_check
    CHECK ((submission_id IS NOT NULL)::integer
         + (appeal_id IS NOT NULL)::integer
         + (objection_id IS NOT NULL)::integer = 1);

ALTER TABLE evidence ADD CONSTRAINT evidence_objection_note_only_check
    CHECK (objection_id IS NULL OR kind = 'note');

CREATE INDEX evidence_by_objection
    ON evidence (class_id, objection_id) WHERE objection_id IS NOT NULL;
CREATE INDEX evidence_notes
    ON evidence (class_id, kind, created_by) WHERE kind = 'note';
