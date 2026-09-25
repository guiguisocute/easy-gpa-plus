-- Close the gap between operational APIs and the two management consoles.

ALTER TABLE class
    ADD COLUMN storage_calibrated_at TIMESTAMPTZ;

ALTER TABLE backup_job DROP CONSTRAINT backup_job_status_check;
ALTER TABLE backup_job
    ADD CONSTRAINT backup_job_status_check
    CHECK (status IN ('queued','running','complete','failed','expired'));

-- Class administrators may propose a published scheme, but only Ops can make
-- it a platform-wide template. The request owns an immutable JSON snapshot.
CREATE TABLE template_share_request (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    scheme_id       BIGINT NOT NULL,
    requested_by    BIGINT NOT NULL,
    name            TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    config          JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','approved','rejected','canceled')),
    review_reason   TEXT,
    reviewed_by     TEXT,
    template_id     BIGINT REFERENCES platform_template(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at     TIMESTAMPTZ,
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id) ON DELETE CASCADE,
    FOREIGN KEY (class_id,requested_by) REFERENCES app_user(class_id,id)
);

CREATE UNIQUE INDEX template_share_one_pending
    ON template_share_request (class_id,scheme_id) WHERE status='pending';
CREATE INDEX template_share_ops_queue
    ON template_share_request (status,created_at,id);

ALTER TABLE template_share_request ENABLE ROW LEVEL SECURITY;
ALTER TABLE template_share_request FORCE ROW LEVEL SECURITY;
CREATE POLICY template_share_tenant_isolation ON template_share_request
    USING (class_id=app_current_class_id() OR current_user='easygpa_ops')
    WITH CHECK (class_id=app_current_class_id() OR current_user='easygpa_ops');

-- The original core migration granted platform_template DML to the business
-- role. Remove that cross-tenant write path; requests above are the only path.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        REVOKE INSERT,UPDATE,DELETE ON platform_template FROM easygpa_app;
        GRANT SELECT,INSERT,UPDATE ON template_share_request TO easygpa_app;
        GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON template_share_request TO easygpa_ops;
    END IF;
END
$$;

-- The configurable Agent single-image limit can be raised by Ops, while this
-- database ceiling remains an immutable safety boundary.
ALTER TABLE agent_attachment DROP CONSTRAINT agent_attachment_size_bytes_check;
ALTER TABLE agent_attachment
    ADD CONSTRAINT agent_attachment_size_bytes_check
    CHECK (size_bytes > 0 AND size_bytes <= 52428800);

UPDATE ops_config
   SET value=value||'{"evidenceAllowedFormats":["pdf","jpg","jpeg","png","gif","webp","heic","doc","docx","xls","xlsx","ppt","pptx","txt","csv","wps","et","dps","zip","rar","7z","mp4"]}'::jsonb,
       updated_at=now()
 WHERE key='flags' AND NOT (value ? 'evidenceAllowedFormats');

INSERT INTO ops_config (key,value) VALUES (
    'lifecycle',
    '{
      "backupEnabled":true,
      "backupSchedule":"02:30",
      "backupRetentionDays":30,
      "exportRetentionDays":7,
      "knowledgeDeleteGraceHours":168,
      "agentAttachmentGraceHours":24,
      "storageReconcileMinutes":60,
      "knowledgeMaxPdfPages":64,
      "knowledgeMaxArchiveMembers":500,
      "knowledgeMaxArchiveMb":500,
      "knowledgeMaxArchiveRatio":100,
      "knowledgeMaxArchiveDepth":20
    }'::jsonb
) ON CONFLICT (key) DO NOTHING;

-- Remove a previously accepted but never consumed mail-template field. Static
-- audited templates remain the only delivery templates until a real renderer
-- and variable validator exist.
UPDATE ops_config SET value=value-'templates',updated_at=now() WHERE key='mail';

CREATE FUNCTION ops_mail_delivery_log_v2(
    p_status TEXT DEFAULT '', p_tenant BIGINT DEFAULT NULL,
    p_from TIMESTAMPTZ DEFAULT NULL, p_to TIMESTAMPTZ DEFAULT NULL,
    p_limit INTEGER DEFAULT 50, p_offset INTEGER DEFAULT 0
)
RETURNS TABLE (
    delivery_id BIGINT, tenant_id BIGINT, event_id UUID, recipient_hash TEXT,
    provider TEXT, status TEXT, attempt INTEGER, error_message TEXT,
    created_at TIMESTAMPTZ, total_count BIGINT
)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT d.id,d.class_id,d.event_id,
           encode(digest(lower(d.recipient),'sha256'),'hex'),
           d.provider,d.status,d.attempt,d.error_message,d.created_at,
           count(*) OVER ()
      FROM mail_delivery d
     WHERE (p_status='' OR d.status=p_status)
       AND (p_tenant IS NULL OR d.class_id=p_tenant)
       AND (p_from IS NULL OR d.created_at>=p_from)
       AND (p_to IS NULL OR d.created_at<=p_to)
     ORDER BY d.created_at DESC,d.id DESC
     LIMIT LEAST(GREATEST(p_limit,1),200)
    OFFSET GREATEST(p_offset,0)
$$;

REVOKE ALL ON FUNCTION ops_mail_delivery_log_v2(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ,INTEGER,INTEGER) FROM PUBLIC;

CREATE FUNCTION ops_mail_delivery_log_count_v2(
    p_status TEXT DEFAULT '', p_tenant BIGINT DEFAULT NULL,
    p_from TIMESTAMPTZ DEFAULT NULL, p_to TIMESTAMPTZ DEFAULT NULL
)
RETURNS BIGINT
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT count(*)
      FROM mail_delivery d
     WHERE (p_status='' OR d.status=p_status)
       AND (p_tenant IS NULL OR d.class_id=p_tenant)
       AND (p_from IS NULL OR d.created_at>=p_from)
       AND (p_to IS NULL OR d.created_at<=p_to)
$$;

REVOKE ALL ON FUNCTION ops_mail_delivery_log_count_v2(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ) FROM PUBLIC;

CREATE FUNCTION ops_agent_daily_usage()
RETURNS TABLE (message_count BIGINT, attachment_count BIGINT, attachment_bytes BIGINT)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT (SELECT count(*) FROM agent_message WHERE role='user' AND created_at>=date_trunc('day',now())),
           (SELECT count(*) FROM agent_attachment WHERE created_at>=date_trunc('day',now())),
           (SELECT COALESCE(sum(size_bytes),0) FROM agent_attachment WHERE created_at>=date_trunc('day',now()))
$$;

REVOKE ALL ON FUNCTION ops_agent_daily_usage() FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
        GRANT EXECUTE ON FUNCTION ops_mail_delivery_log_v2(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ,INTEGER,INTEGER) TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION ops_mail_delivery_log_count_v2(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ) TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION ops_agent_daily_usage() TO easygpa_ops;
    END IF;
END
$$;
