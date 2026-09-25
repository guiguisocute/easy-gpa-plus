-- A tenant's enabled state is the registration boundary. Enabling a class
-- activates every roster row; archiving it deactivates every roster row.

CREATE FUNCTION ops_update_tenant(
    p_class_id BIGINT,
    p_name TEXT,
    p_archived BOOLEAN,
    p_set_archived BOOLEAN
)
RETURNS TABLE (updated_name TEXT, updated_archived BOOLEAN, roster_changed BIGINT)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
DECLARE
    v_name TEXT;
    v_archived BOOLEAN;
    v_roster_changed BIGINT := 0;
BEGIN
    UPDATE class AS c
       SET name = CASE WHEN p_name = '' THEN c.name ELSE p_name END,
           archived = CASE WHEN p_set_archived THEN p_archived ELSE c.archived END,
           archived_at = CASE
               WHEN CASE WHEN p_set_archived THEN p_archived ELSE c.archived END
                   THEN COALESCE(c.archived_at, now())
               ELSE NULL
           END,
           updated_at = now()
     WHERE c.id = p_class_id
     RETURNING c.name, c.archived INTO v_name, v_archived;

    IF NOT FOUND THEN
        RETURN;
    END IF;

    IF p_set_archived THEN
        WITH changed AS (
            UPDATE whitelist AS w
               SET active = NOT v_archived,
                   updated_at = now()
             WHERE w.class_id = p_class_id
               AND w.active IS DISTINCT FROM NOT v_archived
             RETURNING 1
        )
        SELECT count(*) INTO v_roster_changed FROM changed;
    END IF;

    RETURN QUERY SELECT v_name, v_archived, v_roster_changed;
END
$$;

-- Repair the existing production invariant. The whitelist trigger mirrors the
-- state to pre-provisioned, unregistered app_user identities.
UPDATE whitelist AS w
   SET active = NOT c.archived,
       updated_at = now()
  FROM class AS c
 WHERE w.class_id = c.id
   AND w.active IS DISTINCT FROM NOT c.archived;

REVOKE ALL ON FUNCTION ops_update_tenant(BIGINT, TEXT, BOOLEAN, BOOLEAN) FROM PUBLIC;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_ops') THEN
        GRANT EXECUTE ON FUNCTION ops_update_tenant(BIGINT, TEXT, BOOLEAN, BOOLEAN) TO easygpa_ops;
    END IF;
END
$$;
