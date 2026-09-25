-- Reviewer objection proposals are only suggestions; a class administrator
-- must still make the final decision. Reviewers may therefore include their
-- own scorecard in this queue without gaining a self-service scoring path.

DO $$
DECLARE
    constraint_name NAME;
BEGIN
    SELECT conname
      INTO constraint_name
      FROM pg_constraint
     WHERE conrelid = 'objection'::regclass
       AND contype = 'c'
       AND pg_get_constraintdef(oid) ~ 'proposer_id.*<>.*student_id';

    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE objection DROP CONSTRAINT %I', constraint_name);
    END IF;
END
$$;
