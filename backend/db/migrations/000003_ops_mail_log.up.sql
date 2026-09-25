-- Platform operators may inspect delivery health but must never receive a
-- business identity. The SECURITY DEFINER projection hashes recipients before
-- the BYPASSRLS ops role can observe a row.
CREATE FUNCTION ops_mail_delivery_log(p_limit INTEGER DEFAULT 200)
RETURNS TABLE (
    delivery_id BIGINT,
    tenant_id BIGINT,
    event_id UUID,
    recipient_hash TEXT,
    provider TEXT,
    status TEXT,
    attempt INTEGER,
    error_message TEXT,
    created_at TIMESTAMPTZ
)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT d.id,d.class_id,d.event_id,
           encode(digest(lower(d.recipient),'sha256'),'hex'),
           d.provider,d.status,d.attempt,d.error_message,d.created_at
      FROM mail_delivery d
     ORDER BY d.created_at DESC,d.id DESC
     LIMIT LEAST(GREATEST(p_limit,1),1000)
$$;

CREATE FUNCTION ops_outbox_pending() RETURNS BIGINT
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
    SELECT count(*) FROM outbox_event WHERE published_at IS NULL
$$;

REVOKE ALL ON FUNCTION ops_mail_delivery_log(INTEGER) FROM PUBLIC;
REVOKE ALL ON FUNCTION ops_outbox_pending() FROM PUBLIC;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_ops') THEN
        GRANT EXECUTE ON FUNCTION ops_mail_delivery_log(INTEGER) TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION ops_outbox_pending() TO easygpa_ops;
        GRANT SELECT ON schema_migration TO easygpa_ops;
    END IF;
END
$$;
