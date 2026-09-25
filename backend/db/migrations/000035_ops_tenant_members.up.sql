-- Ops needs a narrow, audited projection for inspecting a class roster. Keep
-- direct whitelist and account-table access out of the ops role.

CREATE FUNCTION ops_tenant_members(p_class_id BIGINT)
RETURNS TABLE (
    whitelist_id BIGINT,
    user_id BIGINT,
    sid TEXT,
    name TEXT,
    role TEXT,
    roster_active BOOLEAN,
    registered BOOLEAN,
    account_status TEXT,
    registered_at TIMESTAMPTZ,
    last_login_at TIMESTAMPTZ
)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT w.id,
           u.id,
           w.sid,
           COALESCE(u.name, w.name),
           COALESCE(u.role, w.role),
           w.active,
           u.id IS NOT NULL,
           u.status,
           w.registered_at,
           u.last_login_at
      FROM whitelist w
      LEFT JOIN app_user u ON u.whitelist_id = w.id
     WHERE w.class_id = p_class_id
     ORDER BY CASE COALESCE(u.role, w.role)
                  WHEN 'class_admin' THEN 0
                  WHEN 'group' THEN 1
                  ELSE 2
              END,
              w.sid,
              w.id
$$;

REVOKE ALL ON FUNCTION ops_tenant_members(BIGINT) FROM PUBLIC;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_ops') THEN
        GRANT EXECUTE ON FUNCTION ops_tenant_members(BIGINT) TO easygpa_ops;
    END IF;
END
$$;
