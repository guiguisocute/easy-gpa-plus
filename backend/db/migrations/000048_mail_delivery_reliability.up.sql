-- Platform test mail has no tenant. Tenant RLS continues to exclude these rows.
ALTER TABLE mail_delivery ALTER COLUMN class_id DROP NOT NULL;
ALTER TABLE mail_delivery ADD COLUMN template TEXT NOT NULL DEFAULT 'legacy_notification';
ALTER TABLE mail_delivery ADD COLUMN error_code TEXT;
ALTER TABLE mail_delivery ADD COLUMN finished_at TIMESTAMPTZ;

UPDATE mail_delivery SET finished_at=created_at WHERE status IN ('sent','failed');
WITH numbered AS (
    SELECT id,row_number() OVER (PARTITION BY event_id,recipient ORDER BY created_at,id) AS n
      FROM mail_delivery WHERE event_id IS NOT NULL
)
UPDATE mail_delivery d SET attempt=n.n FROM numbered n WHERE d.id=n.id;

CREATE INDEX mail_delivery_event_recipient ON mail_delivery(event_id,recipient,created_at DESC);

CREATE FUNCTION ops_lookup_mail_delivery(p_event UUID,p_recipient TEXT)
RETURNS TABLE(delivery_id BIGINT,delivery_status TEXT,message_id TEXT)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public,pg_temp SET row_security=off
AS $$
    SELECT COALESCE(d.id,0),COALESCE(d.status,''),COALESCE(d.provider_id,'')
      FROM (SELECT 1) stub LEFT JOIN LATERAL (
          SELECT id,status,provider_id FROM mail_delivery
           WHERE event_id=p_event AND lower(recipient)=lower(p_recipient) AND status IN ('sent','queued')
           ORDER BY CASE status WHEN 'sent' THEN 0 ELSE 1 END,created_at DESC,id DESC LIMIT 1
      ) d ON true
$$;

-- Only the trusted ops connection can reserve or complete an outbound attempt.
-- The projection never returns the recipient, subject, token or message body.
CREATE FUNCTION ops_start_mail_delivery(p_class BIGINT,p_event UUID,p_recipient TEXT,p_template TEXT)
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

CREATE FUNCTION ops_finish_mail_delivery(p_id BIGINT,p_status TEXT,p_message_id TEXT,p_error_code TEXT,p_error TEXT)
RETURNS VOID LANGUAGE plpgsql SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off
AS $$
BEGIN
    IF p_status NOT IN ('sent','failed','suppressed','queued') THEN RAISE EXCEPTION 'invalid delivery outcome'; END IF;
    UPDATE mail_delivery SET status=p_status,provider_id=NULLIF(p_message_id,''),
        error_code=NULLIF(p_error_code,''),error_message=NULLIF(p_error,''),finished_at=now()
     WHERE id=p_id AND status='queued';
    IF NOT FOUND THEN RAISE EXCEPTION 'mail attempt is missing or already completed'; END IF;
END
$$;

CREATE FUNCTION ops_reconcile_mail_delivery(p_id BIGINT,p_status TEXT,p_message_id TEXT)
RETURNS TABLE(event_id UUID,tenant_id BIGINT)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public,pg_temp SET row_security=off
AS $$
BEGIN
    IF p_status NOT IN ('sent','failed') OR (p_status='sent' AND btrim(COALESCE(p_message_id,''))='') THEN
        RAISE EXCEPTION 'invalid reconciliation outcome' USING ERRCODE='22023';
    END IF;
    RETURN QUERY UPDATE mail_delivery d SET status=p_status,provider_id=NULLIF(p_message_id,''),
        error_code=CASE WHEN p_status='failed' THEN 'Operator.ConfirmedNotAccepted' ELSE NULL END,
        error_message=CASE WHEN p_status='failed' THEN 'Operator.ConfirmedNotAccepted' ELSE NULL END,
        finished_at=now()
     WHERE d.id=p_id AND d.status='queued' AND d.created_at<now()-interval '2 minutes'
     RETURNING d.event_id,d.class_id;
END
$$;

-- Old releases scan tenant_id into int64. Keep their projections usable during
-- a code rollback after platform deliveries have been recorded; v3 uses NULL.
CREATE OR REPLACE FUNCTION ops_mail_delivery_log(p_limit INTEGER DEFAULT 200)
RETURNS TABLE(delivery_id BIGINT,tenant_id BIGINT,event_id UUID,recipient_hash TEXT,
    provider TEXT,status TEXT,attempt INTEGER,error_message TEXT,created_at TIMESTAMPTZ)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public,pg_temp SET row_security=off
AS $$
    SELECT d.id,COALESCE(d.class_id,0),d.event_id,encode(digest(lower(d.recipient),'sha256'),'hex'),
           d.provider,d.status,d.attempt,d.error_message,d.created_at
      FROM mail_delivery d ORDER BY d.created_at DESC,d.id DESC
     LIMIT LEAST(GREATEST(p_limit,1),1000)
$$;
CREATE OR REPLACE FUNCTION ops_mail_delivery_log_v2(
    p_status TEXT DEFAULT '',p_tenant BIGINT DEFAULT NULL,
    p_from TIMESTAMPTZ DEFAULT NULL,p_to TIMESTAMPTZ DEFAULT NULL,
    p_limit INTEGER DEFAULT 50,p_offset INTEGER DEFAULT 0
)
RETURNS TABLE(delivery_id BIGINT,tenant_id BIGINT,event_id UUID,recipient_hash TEXT,
    provider TEXT,status TEXT,attempt INTEGER,error_message TEXT,created_at TIMESTAMPTZ,total_count BIGINT)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public,pg_temp SET row_security=off
AS $$
    SELECT d.id,COALESCE(d.class_id,0),d.event_id,encode(digest(lower(d.recipient),'sha256'),'hex'),
           d.provider,d.status,d.attempt,d.error_message,d.created_at,count(*) OVER ()
      FROM mail_delivery d
     WHERE (p_status='' OR d.status=p_status) AND (p_tenant IS NULL OR d.class_id=p_tenant)
       AND (p_from IS NULL OR d.created_at>=p_from) AND (p_to IS NULL OR d.created_at<=p_to)
     ORDER BY d.created_at DESC,d.id DESC
     LIMIT LEAST(GREATEST(p_limit,1),200) OFFSET GREATEST(p_offset,0)
$$;

CREATE FUNCTION ops_mail_delivery_log_v3(
    p_status TEXT DEFAULT '',p_tenant BIGINT DEFAULT NULL,
    p_from TIMESTAMPTZ DEFAULT NULL,p_to TIMESTAMPTZ DEFAULT NULL,
    p_limit INTEGER DEFAULT 50,p_offset INTEGER DEFAULT 0
)
RETURNS TABLE(delivery_id BIGINT,tenant_id BIGINT,event_id UUID,recipient_hash TEXT,
    provider TEXT,status TEXT,attempt INTEGER,error_message TEXT,created_at TIMESTAMPTZ,
    total_count BIGINT,template TEXT,error_code TEXT,message_id TEXT)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off
AS $$
    SELECT d.id,d.class_id,d.event_id,encode(digest(lower(d.recipient),'sha256'),'hex'),
           d.provider,d.status,d.attempt,d.error_message,d.created_at,count(*) OVER (),
           d.template,d.error_code,d.provider_id
      FROM mail_delivery d
     WHERE (p_status='' OR d.status=p_status) AND (p_tenant IS NULL OR d.class_id=p_tenant)
       AND (p_from IS NULL OR d.created_at>=p_from) AND (p_to IS NULL OR d.created_at<=p_to)
     ORDER BY d.created_at DESC,d.id DESC
     LIMIT LEAST(GREATEST(p_limit,1),200) OFFSET GREATEST(p_offset,0)
$$;

REVOKE ALL ON FUNCTION ops_start_mail_delivery(BIGINT,UUID,TEXT,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION ops_lookup_mail_delivery(UUID,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION ops_reconcile_mail_delivery(BIGINT,TEXT,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION ops_finish_mail_delivery(BIGINT,TEXT,TEXT,TEXT,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION ops_mail_delivery_log_v3(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ,INTEGER,INTEGER) FROM PUBLIC;
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
        GRANT EXECUTE ON FUNCTION ops_start_mail_delivery(BIGINT,UUID,TEXT,TEXT) TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION ops_lookup_mail_delivery(UUID,TEXT) TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION ops_reconcile_mail_delivery(BIGINT,TEXT,TEXT) TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION ops_finish_mail_delivery(BIGINT,TEXT,TEXT,TEXT,TEXT) TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION ops_mail_delivery_log_v3(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ,INTEGER,INTEGER) TO easygpa_ops;
    END IF;
END $$;
