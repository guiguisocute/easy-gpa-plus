-- Opt-in by the operator after the new SES templates have been approved.
-- The timestamp is also a cutover fence: paused/legacy events are never replayed.
UPDATE ops_config SET value=value || jsonb_build_object(
    'notificationsEnabled',false,'notificationsSince',now(),
    'sesNotificationDomain','notify.example.org'),updated_at=now() WHERE key='mail';

CREATE TABLE IF NOT EXISTS mail_preference (
    class_id BIGINT NOT NULL,
    user_id BIGINT PRIMARY KEY,
    settings JSONB NOT NULL DEFAULT '{}',
    opt_out_token TEXT NOT NULL UNIQUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (class_id,user_id) REFERENCES app_user(class_id,id)
);
CREATE TABLE mail_batch (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    class_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    recipient TEXT NOT NULL,
    template TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','suppressed','failed')),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (class_id,user_id) REFERENCES app_user(class_id,id)
);
CREATE INDEX mail_batch_pending ON mail_batch(next_attempt_at) WHERE status='pending';
CREATE INDEX mail_batch_budget ON mail_batch(user_id,created_at) WHERE status IN ('pending','sent');
CREATE TABLE mail_notification (
    id BIGSERIAL PRIMARY KEY,
    class_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    event_id UUID NOT NULL REFERENCES outbox_event(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('digest','immediate')),
    entity_key TEXT NOT NULL,
    due_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','batched','sent','suppressed')),
    batch_id UUID REFERENCES mail_batch(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (event_id,user_id),
    FOREIGN KEY (class_id,user_id) REFERENCES app_user(class_id,id)
);
CREATE INDEX mail_notification_due ON mail_notification(due_at) WHERE status='pending';
CREATE INDEX mail_notification_recipient ON mail_notification(user_id,entity_key) WHERE status='pending';
CREATE INDEX mail_notification_batch ON mail_notification(batch_id) WHERE batch_id IS NOT NULL;

-- Reserve persisted batches through the same idempotent delivery ledger.
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
        IF p_class IS NULL OR (NOT EXISTS (SELECT 1 FROM outbox_event WHERE id=p_event AND class_id=p_class)
            AND NOT EXISTS (SELECT 1 FROM mail_batch WHERE id=p_event AND class_id=p_class AND lower(recipient)=lower(p_recipient) AND template=p_template)) THEN
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

-- Suppressions outlive an account, address change, domain change and retries.
-- Only a recipient fingerprint is needed, never a second directory of addresses.
CREATE TABLE IF NOT EXISTS mail_suppression (
    recipient_hash TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('business','all')),
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (recipient_hash,scope)
);
INSERT INTO mail_suppression(recipient_hash,scope,reason)
SELECT md5(lower(trim(recipient))), 'business', 'provider_rejected'
FROM mail_delivery WHERE error_code IN (
    'FailedOperation.ReceiverHasUnsubscribed','FailedOperation.RejectedByRecipients',
    'FailedOperation.EmailAddrInBlacklist')
GROUP BY lower(trim(recipient)) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS mail_preference_history (
    id BIGSERIAL PRIMARY KEY,
    class_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    previous_settings JSONB NOT NULL,
    settings JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE FUNCTION record_mail_preference_change() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public,pg_temp AS $$
BEGIN
    IF OLD.settings IS DISTINCT FROM NEW.settings THEN
        INSERT INTO mail_preference_history(class_id,user_id,previous_settings,settings)
            VALUES(NEW.class_id,NEW.user_id,OLD.settings,NEW.settings);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER mail_preference_change AFTER UPDATE OF settings ON mail_preference
    FOR EACH ROW EXECUTE FUNCTION record_mail_preference_change();

ALTER TABLE mail_delivery ADD COLUMN feedback_status TEXT NOT NULL DEFAULT '';
ALTER TABLE mail_delivery ADD COLUMN feedback_checked_at TIMESTAMPTZ;
ALTER TABLE mail_delivery ADD COLUMN feedback_next_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX mail_delivery_feedback_due ON mail_delivery(feedback_next_at)
    WHERE status='sent' AND provider_id IS NOT NULL AND provider_id<>'';

ALTER TABLE mail_preference ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_preference FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON mail_preference USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE mail_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_batch FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON mail_batch USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE mail_notification ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_notification FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON mail_notification USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());

-- Public opt-out capabilities can only reduce consent; never reveal identity
-- or enable a category. GET/page previews do not call this function.
CREATE FUNCTION mail_opt_out(p_token TEXT,p_category TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public,pg_temp SET row_security=off AS $$
DECLARE target mail_preference%ROWTYPE;
BEGIN
    IF length(p_token)<>43 OR p_category NOT IN ('all','progress','results','tasks','decisions','deadlines','receipts','class_activity') THEN RETURN false; END IF;
    SELECT * INTO target FROM mail_preference WHERE opt_out_token=p_token FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    IF p_category='all' THEN
        UPDATE mail_preference SET settings=jsonb_set(settings,'{enabled}','false'),updated_at=now() WHERE user_id=target.user_id;
        UPDATE mail_notification SET status='suppressed' WHERE user_id=target.user_id AND status='pending';
    ELSE
        UPDATE mail_preference SET settings=jsonb_set(settings,'{categories}',coalesce(settings->'categories','{}') || jsonb_build_object(p_category,'off')),updated_at=now() WHERE user_id=target.user_id;
        UPDATE mail_notification SET status='suppressed' WHERE user_id=target.user_id AND category=p_category AND status='pending';
    END IF;
    RETURN true;
END;
$$;
REVOKE ALL ON FUNCTION mail_opt_out(TEXT,TEXT) FROM PUBLIC;

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE ON mail_preference,mail_notification,mail_batch TO easygpa_app;
        GRANT USAGE,SELECT ON SEQUENCE mail_notification_id_seq TO easygpa_app;
        GRANT EXECUTE ON FUNCTION mail_opt_out(TEXT,TEXT) TO easygpa_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
        GRANT SELECT,INSERT,UPDATE ON mail_preference,mail_notification,mail_batch,mail_suppression TO easygpa_ops;
        GRANT SELECT,UPDATE ON mail_delivery TO easygpa_ops;
        GRANT SELECT ON mail_preference_history TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION mail_opt_out(TEXT,TEXT) TO easygpa_ops;
    END IF;
END $$;
