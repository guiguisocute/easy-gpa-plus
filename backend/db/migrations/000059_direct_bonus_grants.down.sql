-- Never silently erase the provenance of scores already granted.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM bonus_grant_batch) THEN
        RAISE EXCEPTION 'Direct bonus grants exist; preserve their provenance before downgrading';
    END IF;
END $$;
DROP INDEX submission_bonus_grant_student;
ALTER TABLE submission DROP CONSTRAINT submission_bonus_grant_source_check;
ALTER TABLE submission DROP CONSTRAINT submission_bonus_grant_fk;
ALTER TABLE submission DROP COLUMN bonus_grant_batch_id;
ALTER TABLE submission DROP CONSTRAINT submission_source_check;
ALTER TABLE submission ADD CONSTRAINT submission_source_check CHECK (source IN ('manual','ai'));
DROP TABLE bonus_grant_batch;
