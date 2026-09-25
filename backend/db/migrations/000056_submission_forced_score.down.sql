CREATE OR REPLACE FUNCTION prevent_force_rejected_workflow() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    row_data JSONB := to_jsonb(NEW);
    target BIGINT;
    rejected BOOLEAN;
BEGIN
    IF TG_TABLE_NAME='classification_suggestion' THEN
        IF NEW.status<>'pending' THEN RETURN NEW; END IF;
        target := (row_data->>'submission_id')::bigint;
    ELSIF TG_TABLE_NAME='appeal' THEN
        IF row_data->>'target_type'<>'submission' OR NEW.status NOT IN ('draft','filed','reviewing','escalated') THEN RETURN NEW; END IF;
        target := (row_data->>'target_id')::bigint;
    ELSE
        IF row_data->>'kind'<>'submission' OR NEW.status NOT IN ('draft','submitted','reviewing','escalated') THEN RETURN NEW; END IF;
        target := (row_data->>'target_id')::bigint;
    END IF;
    SELECT force_rejection IS NOT NULL INTO rejected FROM submission
        WHERE id=target AND class_id=NEW.class_id FOR UPDATE;
    IF rejected THEN
        RAISE EXCEPTION 'submission was force rejected'
            USING ERRCODE='23514', CONSTRAINT='submission_force_rejection_workflow';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER protect_submission_forced_score ON submission;
DROP FUNCTION protect_submission_forced_score();
ALTER TABLE submission DROP CONSTRAINT submission_forced_score_check;
ALTER TABLE submission DROP COLUMN forced_score;
