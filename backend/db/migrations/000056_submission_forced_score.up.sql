-- Explicit administrator corrections retain all previous rulings in audit_log.
ALTER TABLE submission ADD COLUMN forced_score JSONB;
ALTER TABLE submission ADD CONSTRAINT submission_forced_score_check CHECK (
    forced_score IS NULL OR (jsonb_typeof(forced_score)='object'
        AND forced_score ? 'score' AND jsonb_typeof(forced_score->'score')='number'
        AND final_score IS NOT NULL AND status IN ('scored','locked')
        AND (force_rejection IS NOT NULL OR final_score=(forced_score->>'score')::numeric))
);
CREATE FUNCTION protect_submission_forced_score() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.forced_score IS NOT NULL AND (
        NEW.forced_score IS NULL
        OR NEW.category_key IS DISTINCT FROM OLD.category_key
        OR NEW.item_key IS DISTINCT FROM OLD.item_key
        OR NEW.rule_snapshot IS DISTINCT FROM OLD.rule_snapshot
        OR NEW.student_id IS DISTINCT FROM OLD.student_id
        OR NEW.scheme_id IS DISTINCT FROM OLD.scheme_id
    ) THEN
        RAISE EXCEPTION 'submission was force scored'
            USING ERRCODE='23514', CONSTRAINT='submission_forced_score_immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER protect_submission_forced_score BEFORE UPDATE ON submission
    FOR EACH ROW EXECUTE FUNCTION protect_submission_forced_score();

CREATE OR REPLACE FUNCTION prevent_force_rejected_workflow() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    row_data JSONB := to_jsonb(NEW);
    target BIGINT;
    rejected BOOLEAN;
    corrected BOOLEAN;
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
    SELECT force_rejection IS NOT NULL, forced_score IS NOT NULL INTO rejected,corrected FROM submission
        WHERE id=target AND class_id=NEW.class_id FOR UPDATE;
    IF rejected THEN
        RAISE EXCEPTION 'submission was force rejected'
            USING ERRCODE='23514', CONSTRAINT='submission_force_rejection_workflow';
    END IF;
    IF corrected THEN
        RAISE EXCEPTION 'submission was force scored'
            USING ERRCODE='23514', CONSTRAINT='submission_forced_score_workflow';
    END IF;
    RETURN NEW;
END;
$$;
