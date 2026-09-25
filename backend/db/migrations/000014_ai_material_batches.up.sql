-- AI material organisation is an optional, tenant-owned enhancement. Original
-- files, per-page perception and batch-level candidates are deliberately kept
-- separate so PDF pages can reuse the image pipeline without becoming evidence
-- objects themselves.

CREATE TABLE ai_batch (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id          BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    student_id        BIGINT NOT NULL,
    scheme_id         BIGINT NOT NULL,
    scheme_version    INTEGER NOT NULL,
    scheme_snapshot   JSONB NOT NULL,
    status            TEXT NOT NULL DEFAULT 'uploading'
                      CHECK (status IN ('uploading','queued','processing','review','complete','failed','canceled','expired')),
    total_count       INTEGER NOT NULL DEFAULT 0 CHECK (total_count >= 0),
    processed_count   INTEGER NOT NULL DEFAULT 0 CHECK (processed_count >= 0),
    result            JSONB NOT NULL DEFAULT '{"candidates":[],"warnings":[]}'::jsonb,
    raw_response      JSONB,
    usage             JSONB NOT NULL DEFAULT '{}'::jsonb,
    vision_model      TEXT NOT NULL,
    text_model        TEXT NOT NULL,
    prompt_version    TEXT NOT NULL,
    error             TEXT,
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    expires_at        TIMESTAMPTZ NOT NULL DEFAULT now() + interval '7 days',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id)
);

CREATE TABLE ai_asset (
    id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id              BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    batch_id              BIGINT NOT NULL,
    object_key            TEXT NOT NULL UNIQUE,
    filename              TEXT NOT NULL,
    media_type            TEXT NOT NULL,
    size_bytes            BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256                TEXT,
    object_etag           TEXT,
    status                TEXT NOT NULL DEFAULT 'pending'
                          CHECK (status IN ('pending','ready','processing','complete','failed','applied','rejected')),
    page_count            INTEGER CHECK (page_count IS NULL OR page_count BETWEEN 1 AND 64),
    applied_submission_id BIGINT,
    error                 TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, batch_id) REFERENCES ai_batch(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, applied_submission_id) REFERENCES submission(class_id, id) ON DELETE SET NULL (applied_submission_id)
);

CREATE TABLE ai_item (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id       BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    batch_id       BIGINT NOT NULL,
    asset_id       BIGINT NOT NULL,
    page_no        INTEGER NOT NULL DEFAULT 0 CHECK (page_no BETWEEN 0 AND 64),
    status         TEXT NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('queued','processing','complete','failed')),
    perception     JSONB,
    raw_response   JSONB,
    model          TEXT,
    usage          JSONB NOT NULL DEFAULT '{}'::jsonb,
    duration_ms    BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    attempts       INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    error          TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (asset_id, page_no),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, batch_id) REFERENCES ai_batch(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, asset_id) REFERENCES ai_asset(class_id, id) ON DELETE CASCADE
);

CREATE INDEX ai_batch_student_status ON ai_batch (class_id, student_id, status, created_at DESC);
CREATE INDEX ai_batch_expiry ON ai_batch (expires_at) WHERE status IN ('uploading','review','failed','canceled');
CREATE INDEX ai_asset_batch_status ON ai_asset (class_id, batch_id, status, id);
CREATE INDEX ai_item_batch_status ON ai_item (class_id, batch_id, status, id);

DO $$
DECLARE
    table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['ai_batch', 'ai_asset', 'ai_item']
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

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON ai_batch, ai_asset, ai_item TO easygpa_app;
        GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
    END IF;
END
$$;
