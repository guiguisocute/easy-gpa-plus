CREATE TABLE agent_connection (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 class_id BIGINT NOT NULL REFERENCES class(id),
 user_id BIGINT NOT NULL,
 name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
 token_prefix TEXT NOT NULL,
 token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
 scopes TEXT[] NOT NULL,
 auth_version BIGINT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 last_used_at TIMESTAMPTZ,
 UNIQUE(class_id,id),
 FOREIGN KEY(class_id,user_id) REFERENCES app_user(class_id,id)
);
CREATE INDEX agent_connection_owner ON agent_connection(class_id,user_id);
ALTER TABLE export_job ADD COLUMN agent_connection_id BIGINT;
ALTER TABLE export_job ADD CONSTRAINT export_job_agent_connection_fk FOREIGN KEY(class_id,agent_connection_id) REFERENCES agent_connection(class_id,id);

CREATE TABLE agent_operation (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 class_id BIGINT NOT NULL,
 connection_id BIGINT NOT NULL,
 idempotency_key TEXT NOT NULL CHECK(length(idempotency_key) BETWEEN 8 AND 100),
 tool TEXT NOT NULL,
 arguments JSONB NOT NULL,
 request_hash TEXT NOT NULL,
 state_hash TEXT NOT NULL DEFAULT '',
 preview JSONB NOT NULL DEFAULT '{}',
 status TEXT NOT NULL CHECK(status IN ('pending','approved','rejected','applied')),
 result JSONB,
 result_status INTEGER,
 approved_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL,
 UNIQUE(connection_id,idempotency_key),
 FOREIGN KEY(class_id,connection_id) REFERENCES agent_connection(class_id,id)
);
CREATE TABLE agent_upload (
 id TEXT PRIMARY KEY,
 class_id BIGINT NOT NULL,
 connection_id BIGINT NOT NULL,
 evidence_id BIGINT NOT NULL,
 submission_id BIGINT NOT NULL,
 sha256 TEXT NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 size_bytes BIGINT NOT NULL CHECK(size_bytes BETWEEN 1 AND 67108864),
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','uploaded','completed')),
 expires_at TIMESTAMPTZ NOT NULL,
 FOREIGN KEY(class_id,connection_id) REFERENCES agent_connection(class_id,id),
 FOREIGN KEY(class_id,evidence_id) REFERENCES evidence(class_id,id) ON DELETE CASCADE,
 FOREIGN KEY(class_id,submission_id) REFERENCES submission(class_id,id) ON DELETE CASCADE
);
ALTER TABLE agent_connection ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_connection FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_operation ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_operation FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_upload ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_upload FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON agent_connection USING(class_id=app_current_class_id()) WITH CHECK(class_id=app_current_class_id());
CREATE POLICY tenant_isolation ON agent_operation USING(class_id=app_current_class_id()) WITH CHECK(class_id=app_current_class_id());
CREATE POLICY tenant_isolation ON agent_upload USING(class_id=app_current_class_id()) WITH CHECK(class_id=app_current_class_id());
-- Revocation persists even if an account/class is later enabled again.
CREATE FUNCTION revoke_changed_agent_connections() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='app_user' THEN
  UPDATE agent_connection SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=NEW.id AND class_id=NEW.class_id;
 ELSE
  UPDATE agent_connection SET revoked_at=COALESCE(revoked_at,now()) WHERE class_id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER revoke_user_agent_connections AFTER UPDATE OF password_hash,token_version,role,status,is_deputy ON app_user
 FOR EACH ROW WHEN (OLD.password_hash IS DISTINCT FROM NEW.password_hash OR OLD.token_version IS DISTINCT FROM NEW.token_version OR OLD.role IS DISTINCT FROM NEW.role OR OLD.status IS DISTINCT FROM NEW.status OR OLD.is_deputy IS DISTINCT FROM NEW.is_deputy)
 EXECUTE FUNCTION revoke_changed_agent_connections();
CREATE TRIGGER revoke_class_agent_connections AFTER UPDATE OF archived ON class
 FOR EACH ROW WHEN (NEW.archived AND NOT OLD.archived) EXECUTE FUNCTION revoke_changed_agent_connections();

-- Authentication has no tenant context yet. Expose a hash-bound, minimal
-- projection through the same restricted definer pattern as web authentication;
-- do not grant the operations role direct access to users or connection tables.
CREATE FUNCTION mcp_authenticate(p_hash BYTEA)
RETURNS TABLE(id BIGINT,class_id BIGINT,user_id BIGINT,scopes TEXT[],auth_version BIGINT,expires_at TIMESTAMPTZ,role TEXT,is_deputy BOOLEAN)
LANGUAGE sql SECURITY DEFINER SET search_path=public,pg_temp AS $$
 WITH permitted AS MATERIALIZED (
  SELECT ac.id,ac.class_id,ac.user_id,ac.scopes,ac.auth_version,ac.expires_at,u.role,u.is_deputy
  FROM agent_connection ac JOIN app_user u ON u.id=ac.user_id AND u.class_id=ac.class_id JOIN class cl ON cl.id=ac.class_id
  WHERE ac.token_hash=p_hash AND ac.revoked_at IS NULL AND ac.expires_at>now()
    AND u.status='active' AND u.password_hash IS NOT NULL AND u.token_version=ac.auth_version AND NOT cl.archived
 ), touched AS (
  UPDATE agent_connection SET last_used_at=now()
  WHERE agent_connection.id IN (SELECT permitted.id FROM permitted)
    AND (last_used_at IS NULL OR last_used_at<now()-interval '30 seconds')
 ) SELECT * FROM permitted
$$;
REVOKE ALL ON FUNCTION mcp_authenticate(BYTEA) FROM PUBLIC;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
  GRANT SELECT,INSERT,UPDATE ON agent_connection,agent_operation,agent_upload TO easygpa_app;
  GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
 END IF;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
  GRANT EXECUTE ON FUNCTION mcp_authenticate(BYTEA) TO easygpa_ops;
 END IF;
END $$;
