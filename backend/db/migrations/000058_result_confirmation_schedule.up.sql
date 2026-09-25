-- A class-wide confirmation deadline is explicitly armed only after final
-- reviews finish. Pausing is durable; resolving an issue never rearms it.
CREATE TABLE result_confirmation_schedule (
    class_id BIGINT PRIMARY KEY REFERENCES class(id) ON DELETE CASCADE,
    scheme_id BIGINT NOT NULL,
    deadline_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('disabled','scheduled','paused','completed')),
    revision BIGINT NOT NULL DEFAULT 1,
    paused_at TIMESTAMPTZ,
    pause_reason TEXT NOT NULL DEFAULT '',
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id)
);
ALTER TABLE result_confirmation_schedule ENABLE ROW LEVEL SECURITY;
ALTER TABLE result_confirmation_schedule FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON result_confirmation_schedule
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE ON result_confirmation_schedule TO easygpa_app;
    END IF;
END $$;

CREATE FUNCTION scheduled_confirmation_classes() RETURNS TABLE(class_id BIGINT)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT r.class_id FROM public.result_confirmation_schedule r JOIN public.class c ON c.id=r.class_id
    WHERE r.status='scheduled' AND NOT c.archived ORDER BY r.class_id;
$$;
REVOKE ALL ON FUNCTION scheduled_confirmation_classes() FROM PUBLIC;
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
        GRANT EXECUTE ON FUNCTION scheduled_confirmation_classes() TO easygpa_ops;
    END IF;
END $$;

ALTER TABLE result_confirmation DROP CONSTRAINT result_confirmation_source_check;
ALTER TABLE result_confirmation ADD CONSTRAINT result_confirmation_source_check
    CHECK (source IN ('manual','auto','scheduled'));
