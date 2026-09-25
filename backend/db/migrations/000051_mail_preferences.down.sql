-- A code rollback must not resume the old notifier. Export preferences and
-- suppression records before an explicitly requested schema downgrade.
DROP FUNCTION IF EXISTS mail_opt_out(TEXT,TEXT);
CREATE OR REPLACE FUNCTION ops_start_mail_delivery(p_class BIGINT,p_event UUID,p_recipient TEXT,p_template TEXT)
RETURNS TABLE(delivery_id BIGINT,delivery_status TEXT,message_id TEXT)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off
AS $$
DECLARE prior mail_delivery%ROWTYPE; next_attempt INTEGER := 1; new_id BIGINT;
BEGIN
    IF p_recipient IS NULL OR btrim(p_recipient)='' OR p_template IS NULL OR btrim(p_template)='' THEN
        RAISE EXCEPTION 'mail delivery metadata is required';
    END IF;
    IF p_event IS NOT NULL THEN
        IF p_class IS NULL OR NOT EXISTS (SELECT 1 FROM outbox_event WHERE id=p_event AND class_id=p_class) THEN
            RAISE EXCEPTION 'mail event does not belong to tenant';
        END IF;
        PERFORM pg_advisory_xact_lock(hashtextextended(p_event::text || ':' || p_recipient,0));
        SELECT * INTO prior FROM mail_delivery
         WHERE event_id=p_event AND lower(recipient)=lower(p_recipient) AND status IN ('sent','queued')
         ORDER BY CASE status WHEN 'sent' THEN 0 ELSE 1 END,created_at DESC,id DESC LIMIT 1;
        IF FOUND THEN
            RETURN QUERY SELECT prior.id,prior.status,COALESCE(prior.provider_id,'');
            RETURN;
        END IF;
        SELECT COALESCE(max(attempt),0)+1 INTO next_attempt FROM mail_delivery
         WHERE event_id=p_event AND lower(recipient)=lower(p_recipient);
    END IF;
    INSERT INTO mail_delivery(class_id,event_id,recipient,provider,status,attempt,template)
    VALUES(p_class,p_event,p_recipient,'tencent_ses','queued',next_attempt,p_template)
    RETURNING id INTO new_id;
    RETURN QUERY SELECT new_id,'new'::text,''::text;
END
$$;
DROP TABLE IF EXISTS mail_notification;
DROP TABLE IF EXISTS mail_batch;
DROP TRIGGER IF EXISTS mail_preference_change ON mail_preference;
DROP FUNCTION IF EXISTS record_mail_preference_change();
DROP POLICY IF EXISTS tenant_isolation ON mail_preference;
-- Keep permanent opt-outs, preferences and their history across rollback.
-- RLS remains enabled with no policy until the new version is deployed again.
DROP INDEX IF EXISTS mail_delivery_feedback_due;
ALTER TABLE mail_delivery DROP COLUMN IF EXISTS feedback_status;
ALTER TABLE mail_delivery DROP COLUMN IF EXISTS feedback_checked_at;
ALTER TABLE mail_delivery DROP COLUMN IF EXISTS feedback_next_at;
