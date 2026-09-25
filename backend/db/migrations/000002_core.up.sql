-- EasyGPA Plus non-AI schema. All tenant-owned rows carry class_id and are guarded
-- by forced RLS. The HTTP role is deliberately NOBYPASSRLS (see compose init).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE class
    ADD COLUMN slug TEXT,
    ADD COLUMN storage_bytes BIGINT NOT NULL DEFAULT 0 CHECK (storage_bytes >= 0),
    ADD COLUMN archived_at TIMESTAMPTZ,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE UNIQUE INDEX class_slug_unique ON class (lower(slug)) WHERE slug IS NOT NULL;

CREATE TABLE class_token (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    token_hash      BYTEA NOT NULL UNIQUE,
    expires_at      TIMESTAMPTZ NOT NULL,
    redeemed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);

CREATE TABLE whitelist (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    sid             TEXT NOT NULL,
    name            TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'student'
                    CHECK (role IN ('student', 'group', 'class_admin')),
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    registered_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    UNIQUE (class_id, sid),
    UNIQUE (sid)
);

CREATE TABLE app_user (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    whitelist_id    BIGINT NOT NULL,
    sid             TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    password_hash   TEXT NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('student', 'group', 'class_admin')),
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    token_version   BIGINT NOT NULL DEFAULT 1,
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, whitelist_id) REFERENCES whitelist(class_id, id),
    UNIQUE (whitelist_id)
);

CREATE TABLE user_email (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    user_id         BIGINT NOT NULL,
    email           TEXT NOT NULL,
    email_normalized TEXT NOT NULL UNIQUE,
    is_primary      BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, email_normalized),
    FOREIGN KEY (class_id, user_id) REFERENCES app_user(class_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX user_email_one_primary
    ON user_email (user_id) WHERE is_primary AND verified_at IS NOT NULL;

CREATE TABLE scheme (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    version         INTEGER,
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    config          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by      BIGINT NOT NULL,
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    lock_version    BIGINT NOT NULL DEFAULT 1,
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, created_by) REFERENCES app_user(class_id, id)
);

CREATE UNIQUE INDEX scheme_published_version_unique
    ON scheme (class_id, version) WHERE status = 'published';
CREATE UNIQUE INDEX scheme_one_draft_name
    ON scheme (class_id, lower(name)) WHERE status = 'draft';

CREATE TABLE submission (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    student_id      BIGINT NOT NULL,
    scheme_id       BIGINT NOT NULL,
    scheme_version  INTEGER NOT NULL,
    category_key    TEXT NOT NULL,
    item_key        TEXT NOT NULL,
    title           TEXT NOT NULL,
    claim           JSONB NOT NULL DEFAULT '{}'::jsonb,
    requested_score NUMERIC(10,3),
    final_score     NUMERIC(10,3),
    status          TEXT NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'pending', 'consensus', 'scored', 'appealing', 'arbitrating', 'locked')),
    source          TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'ai')),
    rule_snapshot   JSONB NOT NULL,
    markdown_note   TEXT NOT NULL DEFAULT '',
    appeal_used     BOOLEAN NOT NULL DEFAULT FALSE,
    lock_version    BIGINT NOT NULL DEFAULT 1,
    submitted_at    TIMESTAMPTZ,
    scored_at       TIMESTAMPTZ,
    locked_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id)
);

CREATE INDEX submission_student_status ON submission (class_id, student_id, status, id DESC);
CREATE INDEX submission_review_queue ON submission (class_id, category_key, status, submitted_at, id);

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
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id),
    FOREIGN KEY (class_id, created_by) REFERENCES app_user(class_id, id)
);

CREATE UNIQUE INDEX assignment_one_active_category
    ON assignment (class_id, scheme_id, category_key) WHERE active;

CREATE TABLE assignment_reviewer (
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    assignment_id   BIGINT NOT NULL,
    reviewer_id     BIGINT NOT NULL,
    position        SMALLINT NOT NULL CHECK (position IN (1, 2)),
    PRIMARY KEY (assignment_id, reviewer_id),
    UNIQUE (assignment_id, position),
    FOREIGN KEY (class_id, assignment_id) REFERENCES assignment(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, reviewer_id) REFERENCES app_user(class_id, id)
);

CREATE TABLE review (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    submission_id   BIGINT NOT NULL,
    reviewer_id     BIGINT NOT NULL,
    assignment_id   BIGINT,
    decision        TEXT NOT NULL CHECK (decision IN ('accepted', 'adjusted', 'rejected')),
    score           NUMERIC(10,3) NOT NULL,
    reason          TEXT NOT NULL,
    spent_seconds   INTEGER NOT NULL DEFAULT 0 CHECK (spent_seconds >= 0),
    superseded_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, submission_id) REFERENCES submission(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, reviewer_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, assignment_id) REFERENCES assignment(class_id, id)
);

CREATE UNIQUE INDEX review_one_current_per_reviewer
    ON review (submission_id, reviewer_id) WHERE superseded_at IS NULL;

CREATE TABLE base_score (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    student_id      BIGINT NOT NULL,
    scheme_id       BIGINT NOT NULL,
    category_key    TEXT NOT NULL,
    item_key        TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('base', 'penalty')),
    full_score      NUMERIC(10,3),
    score           NUMERIC(10,3) NOT NULL,
    basis           TEXT NOT NULL,
    recorded_by     BIGINT NOT NULL,
    appeal_used     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    UNIQUE (class_id, student_id, scheme_id, category_key, item_key),
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id),
    FOREIGN KEY (class_id, recorded_by) REFERENCES app_user(class_id, id),
    CHECK ((kind = 'base' AND full_score IS NOT NULL AND score >= 0 AND score <= full_score)
        OR (kind = 'penalty' AND full_score IS NULL AND score <= 0))
);

CREATE TABLE appeal (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id         BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    target_type      TEXT NOT NULL CHECK (target_type IN ('submission', 'base_score', 'penalty_score')),
    target_id        BIGINT NOT NULL,
    student_id       BIGINT NOT NULL,
    reason           TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'filed'
                     CHECK (status IN ('filed', 'assigned', 'resolved', 'escalated', 'final')),
    handler_id       BIGINT,
    resolution_score NUMERIC(10,3),
    resolution_reason TEXT,
    resolved_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    UNIQUE (class_id, target_type, target_id, student_id),
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, handler_id) REFERENCES app_user(class_id, id)
);

CREATE TABLE evidence (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    submission_id   BIGINT,
    appeal_id       BIGINT,
    object_key      TEXT NOT NULL UNIQUE,
    filename        TEXT NOT NULL,
    media_type      TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256          TEXT,
    object_etag     TEXT,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'rejected')),
    created_by      BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, submission_id) REFERENCES submission(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, appeal_id) REFERENCES appeal(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, created_by) REFERENCES app_user(class_id, id),
    CHECK ((submission_id IS NOT NULL)::integer + (appeal_id IS NOT NULL)::integer = 1)
);

CREATE TABLE seal (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    student_id      BIGINT NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('manual', 'auto')),
    sealed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    unsealed_at     TIMESTAMPTZ,
    unsealed_by     BIGINT,
    unseal_reason   TEXT,
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, unsealed_by) REFERENCES app_user(class_id, id)
);

CREATE UNIQUE INDEX seal_one_active_per_student
    ON seal (class_id, student_id) WHERE unsealed_at IS NULL;

CREATE TABLE student_gpa (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    student_id      BIGINT NOT NULL,
    scheme_id       BIGINT NOT NULL,
    score           NUMERIC(10,3) NOT NULL CHECK (score >= 0 AND score <= 100),
    details         JSONB NOT NULL DEFAULT '{}'::jsonb,
    import_batch    UUID NOT NULL,
    imported_by     BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    UNIQUE (class_id, student_id, scheme_id),
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id),
    FOREIGN KEY (class_id, imported_by) REFERENCES app_user(class_id, id)
);

CREATE TABLE gate_override (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    scheme_id       BIGINT NOT NULL,
    reason          TEXT NOT NULL,
    created_by      BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id),
    FOREIGN KEY (class_id, created_by) REFERENCES app_user(class_id, id)
);

CREATE TABLE settlement_run (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    scheme_id       BIGINT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'complete' CHECK (status IN ('running', 'complete', 'failed')),
    trigger_kind    TEXT NOT NULL CHECK (trigger_kind IN ('automatic', 'forced')),
    triggered_by    BIGINT NOT NULL,
    config_snapshot JSONB NOT NULL,
    stale_at        TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id),
    FOREIGN KEY (class_id, triggered_by) REFERENCES app_user(class_id, id)
);

CREATE INDEX settlement_run_latest ON settlement_run (class_id, scheme_id, created_at DESC);

CREATE TABLE settlement_invalidation (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    run_id          BIGINT NOT NULL,
    reason          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, run_id) REFERENCES settlement_run(class_id, id) ON DELETE CASCADE
);

CREATE TABLE settlement (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    run_id          BIGINT NOT NULL,
    student_id      BIGINT NOT NULL,
    category_scores JSONB NOT NULL,
    total_score     NUMERIC(10,3) NOT NULL,
    class_rank      INTEGER NOT NULL CHECK (class_rank > 0),
    major_rank      INTEGER NOT NULL CHECK (major_rank > 0),
    honor           BOOLEAN NOT NULL,
    details         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    UNIQUE (run_id, student_id),
    FOREIGN KEY (class_id, run_id) REFERENCES settlement_run(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id)
);

CREATE TABLE export_job (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    run_id          BIGINT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('summary', 'detail', 'archive')),
    status          TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    object_key      TEXT,
    error_message   TEXT,
    requested_by    BIGINT NOT NULL,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, run_id) REFERENCES settlement_run(class_id, id),
    FOREIGN KEY (class_id, requested_by) REFERENCES app_user(class_id, id)
);

CREATE TABLE notification_log (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    event_id        UUID NOT NULL,
    recipient       TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (event_id, recipient)
);

CREATE TABLE mail_delivery (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    event_id        UUID,
    recipient       TEXT NOT NULL,
    provider        TEXT NOT NULL,
    provider_id     TEXT,
    status          TEXT NOT NULL CHECK (status IN ('queued', 'sent', 'soft_bounce', 'hard_bounce', 'failed', 'suppressed')),
    attempt         INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    actor_id        BIGINT,
    actor_role      TEXT NOT NULL,
    action          TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_id     TEXT,
    before_data     JSONB,
    after_data      JSONB,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip_address      INET,
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (class_id, actor_id) REFERENCES app_user(class_id, id)
);

CREATE INDEX audit_log_query ON audit_log (class_id, created_at DESC, action);

-- The outbox is system coordination metadata, not business data. It is outside
-- RLS so a worker can claim events, then open a tenant transaction for payloads.
CREATE TABLE outbox_event (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    type            TEXT NOT NULL,
    payload         JSONB NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    available_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX outbox_event_ready ON outbox_event (available_at, created_at) WHERE published_at IS NULL;

CREATE TABLE platform_template (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name             TEXT NOT NULL,
    config           JSONB NOT NULL,
    source_class_id  BIGINT REFERENCES class(id) ON DELETE SET NULL,
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at       TIMESTAMPTZ
);

CREATE TABLE ops_config (
    key             TEXT PRIMARY KEY,
    value           JSONB NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ops_audit (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor           TEXT NOT NULL,
    action          TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_id     TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip_address      INET,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE backup_job (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind            TEXT NOT NULL CHECK (kind IN ('backup', 'restore_drill')),
    status          TEXT NOT NULL CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    offsite         BOOLEAN NOT NULL DEFAULT FALSE,
    detail          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

INSERT INTO ops_config (key, value) VALUES
    ('flags', '{"maintenance":false,"registration":true,"aiEnabled":false,"uploadMaxMb":50,"requestConcurrency":64,"exportConcurrency":1}'::jsonb),
    ('mail', '{"provider":"smtp","perMinute":2,"perDay":600,"quietStart":"22:00","quietEnd":"07:00"}'::jsonb)
ON CONFLICT (key) DO NOTHING;

-- Tenant context helper. The second set_config argument in Go is always true,
-- so the value is automatically discarded when that request transaction ends.
CREATE FUNCTION app_current_class_id() RETURNS BIGINT
LANGUAGE sql STABLE PARALLEL SAFE
AS $$
    SELECT NULLIF(current_setting('app.current_class', true), '')::BIGINT
$$;

-- Auth is the only pre-JWT flow. These narrow SECURITY DEFINER lookups reveal
-- only an exact identity match; arbitrary tenant scans remain impossible.
CREATE FUNCTION auth_find_user(p_identifier TEXT)
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

CREATE FUNCTION auth_find_whitelist(p_sid TEXT, p_name TEXT)
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

CREATE FUNCTION reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = '55000';
END
$$;

CREATE TRIGGER audit_log_append_only
BEFORE UPDATE OR DELETE ON audit_log
FOR EACH ROW EXECUTE FUNCTION reject_mutation();

CREATE TRIGGER ops_audit_append_only
BEFORE UPDATE OR DELETE ON ops_audit
FOR EACH ROW EXECUTE FUNCTION reject_mutation();

CREATE FUNCTION reject_published_scheme_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'published' THEN
        RAISE EXCEPTION 'published schemes are immutable' USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER scheme_published_immutable
BEFORE UPDATE OR DELETE ON scheme
FOR EACH ROW EXECUTE FUNCTION reject_published_scheme_mutation();

CREATE TRIGGER settlement_run_append_only
BEFORE UPDATE OR DELETE ON settlement_run
FOR EACH ROW
WHEN (OLD.status = 'complete')
EXECUTE FUNCTION reject_mutation();

CREATE TRIGGER settlement_append_only
BEFORE UPDATE OR DELETE ON settlement
FOR EACH ROW EXECUTE FUNCTION reject_mutation();

-- Forced RLS is important: it also applies when a deployment accidentally uses
-- the table owner. The normal easygpa_app role is additionally NOBYPASSRLS.
ALTER TABLE class ENABLE ROW LEVEL SECURITY;
ALTER TABLE class FORCE ROW LEVEL SECURITY;
CREATE POLICY class_tenant_isolation ON class
    USING (id = app_current_class_id())
    WITH CHECK (id = app_current_class_id());

DO $$
DECLARE
    table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'whitelist', 'app_user', 'user_email', 'scheme', 'submission',
        'assignment', 'assignment_reviewer', 'review', 'base_score', 'appeal',
        'evidence', 'seal', 'student_gpa', 'gate_override', 'settlement_run',
        'settlement_invalidation', 'settlement', 'export_job', 'notification_log',
        'mail_delivery', 'audit_log'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (class_id = app_current_class_id()) WITH CHECK (class_id = app_current_class_id())',
            table_name
        );
    END LOOP;
END
$$;

-- Roles are created by deploy/postgres/init-roles.sh on a fresh database.
-- Conditional grants keep the migration usable in an external managed PG where
-- an administrator may create the roles immediately before running migrations.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT USAGE ON SCHEMA public TO easygpa_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON
            class, whitelist, app_user, user_email, scheme, submission, assignment,
            assignment_reviewer, review, base_score, appeal, evidence, seal,
            student_gpa, gate_override, settlement_run, settlement_invalidation,
            settlement, export_job,
            notification_log, mail_delivery, audit_log, outbox_event, platform_template
            TO easygpa_app;
        GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
        GRANT EXECUTE ON FUNCTION app_current_class_id() TO easygpa_app;
        GRANT EXECUTE ON FUNCTION auth_find_user(TEXT) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION auth_find_whitelist(TEXT, TEXT) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION auth_find_class_token(BYTEA) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION auth_consume_class_token(BYTEA) TO easygpa_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_ops') THEN
        GRANT USAGE ON SCHEMA public TO easygpa_ops;
        GRANT SELECT, INSERT, UPDATE ON class, class_token, platform_template, ops_config, ops_audit, backup_job TO easygpa_ops;
        GRANT DELETE ON platform_template TO easygpa_ops;
        GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_ops;
    END IF;
END
$$;

REVOKE ALL ON FUNCTION auth_find_user(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_find_whitelist(TEXT, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_find_class_token(BYTEA) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_consume_class_token(BYTEA) FROM PUBLIC;
