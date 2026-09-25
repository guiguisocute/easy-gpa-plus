DROP FUNCTION IF EXISTS ops_appoint_class_admin(BIGINT, TEXT, TEXT);
DROP FUNCTION IF EXISTS ops_class_admins(BIGINT);

CREATE TABLE class_token (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    token_hash      BYTEA NOT NULL UNIQUE,
    expires_at      TIMESTAMPTZ NOT NULL,
    redeemed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);

CREATE OR REPLACE FUNCTION auth_find_user(p_identifier TEXT)
RETURNS TABLE (
    user_id BIGINT,
    class_id BIGINT,
    sid TEXT,
    name TEXT,
    password_hash TEXT,
    role TEXT,
    status TEXT,
    token_version BIGINT
)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT u.id, u.class_id, u.sid, u.name, u.password_hash, u.role, u.status, u.token_version
      FROM app_user u
     WHERE lower(u.sid) = lower(trim(p_identifier))
        OR EXISTS (
            SELECT 1 FROM user_email e
             WHERE e.user_id = u.id
               AND e.verified_at IS NOT NULL
               AND e.email_normalized = lower(trim(p_identifier))
        )
     LIMIT 1
$$;

CREATE OR REPLACE FUNCTION auth_find_whitelist(p_sid TEXT, p_name TEXT)
RETURNS TABLE (whitelist_id BIGINT, class_id BIGINT, role TEXT, registered_at TIMESTAMPTZ, active BOOLEAN)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT w.id, w.class_id, w.role, w.registered_at, w.active
      FROM whitelist w
     WHERE w.sid = trim(p_sid) AND w.name = trim(p_name)
     LIMIT 1
$$;

CREATE FUNCTION auth_find_class_token(p_token_hash BYTEA)
RETURNS TABLE (token_id BIGINT, class_id BIGINT, expires_at TIMESTAMPTZ, redeemed_at TIMESTAMPTZ)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT t.id, t.class_id, t.expires_at, t.redeemed_at
      FROM class_token t
     WHERE t.token_hash = p_token_hash
     LIMIT 1
$$;

CREATE FUNCTION auth_consume_class_token(p_token_hash BYTEA)
RETURNS BIGINT
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    v_class_id BIGINT;
BEGIN
    UPDATE class_token
       SET redeemed_at = now()
     WHERE token_hash = p_token_hash
       AND redeemed_at IS NULL
       AND expires_at > now()
    RETURNING class_id INTO v_class_id;
    IF v_class_id IS NULL THEN
        RAISE EXCEPTION 'class token is invalid, expired, or already redeemed' USING ERRCODE = '22023';
    END IF;
    RETURN v_class_id;
END
$$;

REVOKE ALL ON FUNCTION auth_find_user(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_find_whitelist(TEXT, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_find_class_token(BYTEA) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_consume_class_token(BYTEA) FROM PUBLIC;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT EXECUTE ON FUNCTION auth_find_user(TEXT) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION auth_find_whitelist(TEXT, TEXT) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION auth_find_class_token(BYTEA) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION auth_consume_class_token(BYTEA) TO easygpa_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_ops') THEN
        GRANT SELECT, INSERT, UPDATE ON class_token TO easygpa_ops;
        GRANT USAGE, SELECT ON SEQUENCE class_token_id_seq TO easygpa_ops;
    END IF;
END
$$;
