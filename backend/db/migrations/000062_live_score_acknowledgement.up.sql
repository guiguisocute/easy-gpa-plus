-- Acknowledgement is optional and refers to the exact live scorecard version.
CREATE TABLE score_acknowledgement (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 class_id BIGINT NOT NULL REFERENCES class(id),
 student_id BIGINT NOT NULL,
 scheme_id BIGINT NOT NULL,
 revision TEXT NOT NULL CHECK (revision ~ '^[a-f0-9]{64}$'),
 confirmed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE (class_id, student_id, scheme_id, revision),
 FOREIGN KEY (class_id,student_id) REFERENCES app_user(class_id,id),
 FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id)
);
ALTER TABLE score_acknowledgement ENABLE ROW LEVEL SECURITY;
ALTER TABLE score_acknowledgement FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON score_acknowledgement
 USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
  GRANT SELECT,INSERT ON score_acknowledgement TO easygpa_app;
  GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
 END IF;
END $$;
-- Old audit and confirmation rows remain historical records only.
