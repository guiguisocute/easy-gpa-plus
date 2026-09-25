-- Class activation and the first class-admin appointment are platform-ops
-- responsibilities. Registration now always requires an exact active roster
-- entry; there is no transferable invitation secret.

DROP FUNCTION IF EXISTS auth_consume_class_token(BYTEA);
DROP FUNCTION IF EXISTS auth_find_class_token(BYTEA);
DROP TABLE IF EXISTS class_token;

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
      JOIN class c ON c.id = u.class_id
     WHERE NOT c.archived
       AND (
            lower(u.sid) = lower(trim(p_identifier))
            OR EXISTS (
                SELECT 1 FROM user_email e
                 WHERE e.user_id = u.id
                   AND e.verified_at IS NOT NULL
                   AND e.email_normalized = lower(trim(p_identifier))
            )
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
      JOIN class c ON c.id = w.class_id
     WHERE w.sid = trim(p_sid)
       AND w.name = trim(p_name)
       AND NOT c.archived
     LIMIT 1
$$;

CREATE FUNCTION ops_class_admins(p_class_id BIGINT)
RETURNS JSONB
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT COALESCE(
        jsonb_agg(
            jsonb_build_object(
                'sid', w.sid,
                'name', w.name,
                'registered', w.registered_at IS NOT NULL
            ) ORDER BY w.created_at, w.id
        ),
        '[]'::jsonb
    )
      FROM whitelist w
     WHERE w.class_id = p_class_id
       AND w.role = 'class_admin'
       AND w.active
$$;

CREATE FUNCTION ops_appoint_class_admin(p_class_id BIGINT, p_sid TEXT, p_name TEXT)
RETURNS TABLE (whitelist_id BIGINT, registered BOOLEAN)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    v_sid TEXT := trim(p_sid);
    v_name TEXT := trim(p_name);
    v_existing_class_id BIGINT;
    v_whitelist_id BIGINT;
    v_registered BOOLEAN;
BEGIN
    IF v_sid = '' OR v_name = '' THEN
        RAISE EXCEPTION 'student id and name are required' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM class c WHERE c.id = p_class_id) THEN
        RAISE EXCEPTION 'class does not exist' USING ERRCODE = 'P0002';
    END IF;

    SELECT w.class_id, w.id, w.registered_at IS NOT NULL
      INTO v_existing_class_id, v_whitelist_id, v_registered
      FROM whitelist w
     WHERE w.sid = v_sid
     FOR UPDATE;

    IF FOUND AND v_existing_class_id <> p_class_id THEN
        RAISE EXCEPTION 'student id is already assigned to another class'
            USING ERRCODE = '23505', CONSTRAINT = 'whitelist_sid_key';
    END IF;

    IF v_whitelist_id IS NULL THEN
        INSERT INTO whitelist (class_id, sid, name, role, active)
        VALUES (p_class_id, v_sid, v_name, 'class_admin', TRUE)
        RETURNING id, registered_at IS NOT NULL INTO v_whitelist_id, v_registered;
    ELSE
        UPDATE whitelist
           SET name = v_name,
               role = 'class_admin',
               active = TRUE,
               updated_at = now()
         WHERE id = v_whitelist_id;
    END IF;

    UPDATE app_user AS u
       SET name = v_name,
           role = 'class_admin',
           status = 'active',
           token_version = u.token_version + CASE
               WHEN u.name IS DISTINCT FROM v_name
                 OR u.role <> 'class_admin'
                 OR u.status <> 'active' THEN 1
               ELSE 0
           END,
           updated_at = now()
     WHERE u.whitelist_id = v_whitelist_id;

    RETURN QUERY SELECT v_whitelist_id, v_registered;
END
$$;

REVOKE ALL ON FUNCTION auth_find_user(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_find_whitelist(TEXT, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION ops_class_admins(BIGINT) FROM PUBLIC;
REVOKE ALL ON FUNCTION ops_appoint_class_admin(BIGINT, TEXT, TEXT) FROM PUBLIC;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT EXECUTE ON FUNCTION auth_find_user(TEXT) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION auth_find_whitelist(TEXT, TEXT) TO easygpa_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_ops') THEN
        GRANT EXECUTE ON FUNCTION ops_class_admins(BIGINT) TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION ops_appoint_class_admin(BIGINT, TEXT, TEXT) TO easygpa_ops;
    END IF;
END
$$;
