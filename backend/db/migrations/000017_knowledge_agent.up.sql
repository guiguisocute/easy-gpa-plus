-- Per-class knowledge base and tool-driven conversational agent.  Every
-- business row remains tenant-owned; ops_config contains only platform limits
-- and provider routing, never class documents or conversations.

CREATE TABLE knowledge_policy (
    class_id                       BIGINT PRIMARY KEY REFERENCES class(id) ON DELETE CASCADE,
    external_processing_approved  BOOLEAN NOT NULL DEFAULT FALSE,
    approved_by                    BIGINT,
    approved_at                    TIMESTAMPTZ,
    revoked_at                     TIMESTAMPTZ,
    updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (class_id, approved_by) REFERENCES app_user(class_id, id)
);

CREATE TABLE knowledge_blob (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id           BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    sha256             TEXT CHECK (sha256 IS NULL OR sha256 ~ '^[0-9a-f]{64}$'),
    object_key         TEXT NOT NULL UNIQUE,
    media_type         TEXT NOT NULL,
    size_bytes         BIGINT NOT NULL CHECK (size_bytes > 0),
    status             TEXT NOT NULL DEFAULT 'uploading'
                       CHECK (status IN ('uploading','queued','processing','ready','partial','unsupported','failed','superseded','deleted')),
    converter_version  TEXT,
    usage              JSONB NOT NULL DEFAULT '{}'::jsonb,
    error              TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    UNIQUE (class_id, id)
);

CREATE UNIQUE INDEX knowledge_blob_class_sha256
    ON knowledge_blob (class_id, sha256)
    WHERE sha256 IS NOT NULL AND deleted_at IS NULL AND status <> 'superseded';

CREATE TABLE knowledge_document (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id       BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    blob_id        BIGINT NOT NULL,
    filename       TEXT NOT NULL,
    logical_path   TEXT NOT NULL,
    display_name   TEXT NOT NULL,
    visibility     TEXT NOT NULL DEFAULT 'admin' CHECK (visibility IN ('admin','review','class')),
    status         TEXT NOT NULL DEFAULT 'uploading'
                   CHECK (status IN ('uploading','queued','processing','ready','partial','unsupported','failed','superseded','deleted')),
    searchable     BOOLEAN NOT NULL DEFAULT FALSE,
    extractor      TEXT,
    entries_count  INTEGER NOT NULL DEFAULT 0 CHECK (entries_count >= 0),
    page_count     INTEGER CHECK (page_count IS NULL OR page_count >= 0),
    sheet_count    INTEGER CHECK (sheet_count IS NULL OR sheet_count >= 0),
    warning        TEXT,
    error          TEXT,
    uploaded_by    BIGINT NOT NULL,
    published_at   TIMESTAMPTZ,
    deleted_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, blob_id) REFERENCES knowledge_blob(class_id, id),
    FOREIGN KEY (class_id, uploaded_by) REFERENCES app_user(class_id, id)
);

CREATE INDEX knowledge_document_list ON knowledge_document (class_id, deleted_at, updated_at DESC, id DESC);
CREATE INDEX knowledge_document_blob ON knowledge_document (class_id, blob_id) WHERE deleted_at IS NULL;
CREATE INDEX knowledge_document_visible ON knowledge_document (class_id, visibility, status) WHERE deleted_at IS NULL;

CREATE TABLE knowledge_entry (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id         BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    blob_id          BIGINT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('text','pdf_page','word','slide','sheet','image_ocr','archive_member','metadata','scheme')),
    locator          JSONB NOT NULL DEFAULT '{}'::jsonb,
    text_object_key  TEXT,
    plain_text       TEXT NOT NULL DEFAULT '',
    line_count       INTEGER NOT NULL DEFAULT 0 CHECK (line_count >= 0),
    char_count       INTEGER NOT NULL DEFAULT 0 CHECK (char_count >= 0),
    metadata         JSONB NOT NULL DEFAULT '{}'::jsonb,
    content_hash     TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, blob_id) REFERENCES knowledge_blob(class_id, id) ON DELETE CASCADE
);

CREATE INDEX knowledge_entry_blob ON knowledge_entry (class_id, blob_id, id);

CREATE TABLE agent_conversation (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id    BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    owner_id    BIGINT NOT NULL,
    title       TEXT NOT NULL DEFAULT '新对话',
    deleted_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, owner_id) REFERENCES app_user(class_id, id)
);

CREATE INDEX agent_conversation_owner ON agent_conversation (class_id, owner_id, updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE agent_message (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id         BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    conversation_id  BIGINT NOT NULL,
    actor_id         BIGINT NOT NULL,
    actor_role       TEXT NOT NULL CHECK (actor_role IN ('student','group','class_admin')),
    role             TEXT NOT NULL CHECK (role IN ('user','assistant')),
    status           TEXT NOT NULL DEFAULT 'complete' CHECK (status IN ('queued','running','complete','failed','canceled')),
    content          TEXT NOT NULL DEFAULT '',
    model            TEXT,
    usage            JSONB NOT NULL DEFAULT '{}'::jsonb,
    citations        JSONB NOT NULL DEFAULT '[]'::jsonb,
    tool_trace       JSONB NOT NULL DEFAULT '[]'::jsonb,
    request_context  JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_hash     TEXT,
    error            TEXT,
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, conversation_id) REFERENCES agent_conversation(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, actor_id) REFERENCES app_user(class_id, id)
);

CREATE INDEX agent_message_conversation ON agent_message (class_id, conversation_id, created_at, id);
CREATE INDEX agent_message_queue ON agent_message (class_id, status, created_at) WHERE status IN ('queued','running');

CREATE TABLE agent_tool_call (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id     BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    message_id   BIGINT NOT NULL,
    seq          INTEGER NOT NULL CHECK (seq > 0),
    tool         TEXT NOT NULL,
    arguments    JSONB NOT NULL DEFAULT '{}'::jsonb,
    result_hash  TEXT,
    summary      TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL CHECK (status IN ('complete','failed','canceled')),
    duration_ms  BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (message_id, seq),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, message_id) REFERENCES agent_message(class_id, id) ON DELETE CASCADE
);

CREATE TABLE agent_action (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id            BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    message_id          BIGINT NOT NULL,
    owner_id            BIGINT NOT NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('submission_draft','review_draft','scheme_draft','export_draft')),
    status              TEXT NOT NULL DEFAULT 'proposed'
                        CHECK (status IN ('proposed','prepared','applied','rejected','expired','stale')),
    title               TEXT NOT NULL,
    summary             TEXT NOT NULL DEFAULT '',
    payload             JSONB NOT NULL,
    diff                JSONB NOT NULL DEFAULT '[]'::jsonb,
    citations           JSONB NOT NULL DEFAULT '[]'::jsonb,
    target_view         TEXT NOT NULL,
    confirm_token_hash  BYTEA,
    confirm_expires_at  TIMESTAMPTZ,
    expires_at          TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours',
    result              JSONB,
    idempotency_key     UUID NOT NULL DEFAULT gen_random_uuid(),
    prepared_at         TIMESTAMPTZ,
    applied_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    UNIQUE (class_id, idempotency_key),
    FOREIGN KEY (class_id, message_id) REFERENCES agent_message(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, owner_id) REFERENCES app_user(class_id, id)
);

CREATE INDEX agent_action_owner ON agent_action (class_id, owner_id, status, expires_at);

DO $$
DECLARE
    table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'knowledge_policy','knowledge_blob','knowledge_document','knowledge_entry',
        'agent_conversation','agent_message','agent_tool_call','agent_action'
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

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON
            knowledge_policy, knowledge_blob, knowledge_document, knowledge_entry,
            agent_conversation, agent_message, agent_tool_call, agent_action
            TO easygpa_app;
        GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
    END IF;
END
$$;

UPDATE ops_config
   SET value = value || '{
       "knowledgeEnabled": false,
       "agentActionsEnabled": false,
       "knowledgeEgressEnabled": false
   }'::jsonb,
       updated_at = now()
 WHERE key = 'flags';

UPDATE ops_config
   SET value = value || '{
       "agentModel": "",
       "agentMaxSteps": 24,
       "agentTimeoutSeconds": 180,
       "agentToolResultKb": 32,
       "agentDailyMessages": 50,
       "knowledgeMaxFilesPerClass": 1000,
       "knowledgeMaxStorageMbPerClass": 1024
   }'::jsonb,
       updated_at = now()
 WHERE key = 'ai';
