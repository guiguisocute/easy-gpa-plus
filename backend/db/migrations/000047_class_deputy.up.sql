-- A deputy retains their reviewer identity and receives only the authority to
-- adjudicate class administrators' own cases. No existing member is appointed.
ALTER TABLE app_user ADD COLUMN is_deputy boolean NOT NULL DEFAULT false;
ALTER TABLE app_user ADD CONSTRAINT app_user_deputy_eligible
    CHECK (NOT is_deputy OR (role = 'group' AND status = 'active'));
CREATE UNIQUE INDEX app_user_one_deputy_per_class ON app_user (class_id) WHERE is_deputy;

-- Cover both class-admin and platform-admin role/status changes. Restoring an
-- account later must not silently restore its former appointment.
CREATE FUNCTION clear_ineligible_class_deputy() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.is_deputy AND (NEW.role <> 'group' OR NEW.status <> 'active' OR NEW.class_id <> OLD.class_id) THEN
        NEW.is_deputy := false;
        INSERT INTO audit_log (class_id,actor_role,action,resource_type,resource_id,before_data,after_data,metadata)
        VALUES (OLD.class_id,'system','user.deputy_revoked','user',OLD.id::text,
                '{"isDeputy":true}'::jsonb,'{"isDeputy":false}'::jsonb,
                jsonb_build_object('reason','member_eligibility_changed','role',NEW.role,'status',NEW.status));
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER app_user_clear_ineligible_deputy
    BEFORE UPDATE OF role,status,class_id ON app_user
    FOR EACH ROW EXECUTE FUNCTION clear_ineligible_class_deputy();
