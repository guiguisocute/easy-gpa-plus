-- A forced rejection is an immutable correction of one submitted item. Keep
-- the original reviews, evidence and score in history, but never let an older
-- review/appeal transaction restore this item's contribution afterwards.
ALTER TABLE submission ADD COLUMN force_rejection JSONB;
ALTER TABLE submission ADD CONSTRAINT submission_force_rejection_score_check CHECK (
    force_rejection IS NULL OR (
        jsonb_typeof(force_rejection)='object'
        AND final_score IS NOT NULL AND final_score=0
        AND status IN ('scored','locked')
    )
);

CREATE FUNCTION protect_submission_force_rejection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.force_rejection IS NOT NULL AND (
        NEW.force_rejection IS DISTINCT FROM OLD.force_rejection
        OR NEW.category_key IS DISTINCT FROM OLD.category_key
        OR NEW.item_key IS DISTINCT FROM OLD.item_key
        OR NEW.rule_snapshot IS DISTINCT FROM OLD.rule_snapshot
        OR NEW.student_id IS DISTINCT FROM OLD.student_id
        OR NEW.scheme_id IS DISTINCT FROM OLD.scheme_id
    ) THEN
        RAISE EXCEPTION 'submission was force rejected'
            USING ERRCODE='23514', CONSTRAINT='submission_force_rejection_immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER protect_submission_force_rejection BEFORE UPDATE ON submission
    FOR EACH ROW EXECUTE FUNCTION protect_submission_force_rejection();

-- Serialize the creation/reopening of dependent workflows with the submission
-- lock taken by force-reject. This also covers a request that read the old
-- score just before the correction committed. Closing existing work remains
-- allowed, and completed historical decisions are retained.
CREATE FUNCTION prevent_force_rejected_workflow() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER prevent_force_rejected_appeal BEFORE INSERT OR UPDATE ON appeal
    FOR EACH ROW EXECUTE FUNCTION prevent_force_rejected_workflow();
CREATE TRIGGER prevent_force_rejected_objection BEFORE INSERT OR UPDATE ON objection
    FOR EACH ROW EXECUTE FUNCTION prevent_force_rejected_workflow();
CREATE TRIGGER prevent_force_rejected_classification BEFORE INSERT OR UPDATE ON classification_suggestion
    FOR EACH ROW EXECUTE FUNCTION prevent_force_rejected_workflow();
CREATE TRIGGER prevent_force_rejected_report BEFORE INSERT OR UPDATE ON report
    FOR EACH ROW EXECUTE FUNCTION prevent_force_rejected_workflow();
