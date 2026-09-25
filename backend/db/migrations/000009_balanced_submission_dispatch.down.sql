ALTER TABLE review DROP CONSTRAINT IF EXISTS review_submission_reviewer_fk;
UPDATE review SET assignment_id=NULL;

CREATE TABLE assignment (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    scheme_id       BIGINT NOT NULL,
    category_key    TEXT NOT NULL,
    policy          TEXT NOT NULL DEFAULT 'per_category_fixed_pair',
    random_seed     BIGINT NOT NULL,
    avoid_self      BOOLEAN NOT NULL DEFAULT TRUE,
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_by      BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id,id),
    FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id),
    FOREIGN KEY (class_id,created_by) REFERENCES app_user(class_id,id)
);

CREATE UNIQUE INDEX assignment_one_active_category
    ON assignment (class_id,scheme_id,category_key) WHERE active;

CREATE TABLE assignment_reviewer (
    class_id      BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    assignment_id BIGINT NOT NULL,
    reviewer_id   BIGINT NOT NULL,
    position      SMALLINT NOT NULL CHECK (position IN (1,2)),
    PRIMARY KEY (assignment_id,reviewer_id),
    UNIQUE (assignment_id,position),
    FOREIGN KEY (class_id,assignment_id) REFERENCES assignment(class_id,id) ON DELETE CASCADE,
    FOREIGN KEY (class_id,reviewer_id) REFERENCES app_user(class_id,id)
);

ALTER TABLE assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE assignment FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assignment
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());
ALTER TABLE assignment_reviewer ENABLE ROW LEVEL SECURITY;
ALTER TABLE assignment_reviewer FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assignment_reviewer
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());

ALTER TABLE review
    ADD CONSTRAINT review_class_id_assignment_id_fkey
        FOREIGN KEY (class_id,assignment_id) REFERENCES assignment(class_id,id);

DROP TABLE submission_reviewer;
DROP TABLE dispatch_run;
ALTER TABLE app_user DROP COLUMN dispatch_paused;
ALTER TABLE class DROP COLUMN dispatch_auto;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON assignment,assignment_reviewer TO easygpa_app;
        GRANT USAGE,SELECT ON SEQUENCE assignment_id_seq TO easygpa_app;
    END IF;
END
$$;
