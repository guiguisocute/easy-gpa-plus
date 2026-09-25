-- Existing self-objection history is retained on rollback. NOT VALID restores
-- the legacy rule for future writes without making rollback destroy data.

ALTER TABLE objection
    DROP CONSTRAINT IF EXISTS objection_proposer_not_student_check;

ALTER TABLE objection
    ADD CONSTRAINT objection_proposer_not_student_check
    CHECK (proposer_id <> student_id) NOT VALID;
