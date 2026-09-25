-- Global product documentation for the role-aware knowledge Agent. Platform
-- rows are deliberately separate from tenant knowledge and are writable only
-- through the Ops connection.

ALTER TABLE outbox_event ALTER COLUMN class_id DROP NOT NULL;
ALTER TABLE outbox_event ADD CONSTRAINT outbox_event_platform_scope
    CHECK (class_id IS NOT NULL OR type = 'platform.knowledge_document.created');

CREATE TABLE platform_knowledge_blob (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sha256             TEXT CHECK (sha256 IS NULL OR sha256 ~ '^[0-9a-f]{64}$'),
    object_key         TEXT NOT NULL UNIQUE,
    media_type         TEXT NOT NULL,
    size_bytes         BIGINT NOT NULL CHECK (size_bytes > 0),
    status             TEXT NOT NULL DEFAULT 'uploading'
                       CHECK (status IN ('uploading','queued','processing','ready','partial','unsupported','failed','superseded','deleted')),
    converter_version  TEXT,
    ocr_route          JSONB NOT NULL DEFAULT '{}'::jsonb,
    usage              JSONB NOT NULL DEFAULT '{}'::jsonb,
    error              TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ
);

CREATE UNIQUE INDEX platform_knowledge_blob_sha256
    ON platform_knowledge_blob (sha256)
    WHERE sha256 IS NOT NULL AND deleted_at IS NULL AND status <> 'superseded';

CREATE TABLE platform_knowledge_document (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_kind      TEXT NOT NULL CHECK (source_kind IN ('builtin','custom')),
    builtin_key      TEXT,
    blob_id          BIGINT REFERENCES platform_knowledge_blob(id),
    filename         TEXT NOT NULL,
    display_name     TEXT NOT NULL,
    allowed_roles    TEXT[] NOT NULL,
    page_tags        TEXT[] NOT NULL DEFAULT '{}'::text[],
    keywords         TEXT[] NOT NULL DEFAULT '{}'::text[],
    sort_order       INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'uploading'
                     CHECK (status IN ('uploading','queued','processing','ready','partial','unsupported','failed','superseded','retired','deleted')),
    enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    searchable       BOOLEAN NOT NULL DEFAULT FALSE,
    extractor        TEXT,
    entries_count    INTEGER NOT NULL DEFAULT 0 CHECK (entries_count >= 0),
    page_count       INTEGER CHECK (page_count IS NULL OR page_count >= 0),
    sheet_count      INTEGER CHECK (sheet_count IS NULL OR sheet_count >= 0),
    content_version  TEXT NOT NULL DEFAULT '',
    content_hash     TEXT CHECK (content_hash IS NULL OR content_hash ~ '^[0-9a-f]{64}$'),
    warning          TEXT,
    error            TEXT,
    uploaded_by      TEXT,
    published_at     TIMESTAMPTZ,
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (cardinality(allowed_roles) > 0),
    CHECK (allowed_roles <@ ARRAY['student','group','class_admin']::text[]),
    CHECK ((source_kind='builtin' AND builtin_key IS NOT NULL AND blob_id IS NULL)
        OR (source_kind='custom' AND builtin_key IS NULL AND blob_id IS NOT NULL))
);

CREATE UNIQUE INDEX platform_knowledge_document_builtin
    ON platform_knowledge_document (builtin_key) WHERE builtin_key IS NOT NULL;
CREATE INDEX platform_knowledge_document_list
    ON platform_knowledge_document (deleted_at, source_kind, sort_order, updated_at DESC);
CREATE INDEX platform_knowledge_document_search
    ON platform_knowledge_document USING gin (allowed_roles)
    WHERE deleted_at IS NULL AND enabled AND searchable;

CREATE TABLE platform_knowledge_entry (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id      BIGINT NOT NULL REFERENCES platform_knowledge_document(id) ON DELETE CASCADE,
    kind             TEXT NOT NULL CHECK (kind IN ('text','pdf_page','word','slide','sheet','image_ocr','archive_member','metadata')),
    locator          JSONB NOT NULL DEFAULT '{}'::jsonb,
    text_object_key  TEXT,
    plain_text       TEXT NOT NULL DEFAULT '',
    line_count       INTEGER NOT NULL DEFAULT 0 CHECK (line_count >= 0),
    char_count       INTEGER NOT NULL DEFAULT 0 CHECK (char_count >= 0),
    metadata         JSONB NOT NULL DEFAULT '{}'::jsonb,
    content_hash     TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX platform_knowledge_entry_document ON platform_knowledge_entry (document_id, id);

CREATE FUNCTION enqueue_platform_knowledge_document(document BIGINT) RETURNS UUID
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE event_id UUID;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM platform_knowledge_document WHERE id=document AND source_kind='custom' AND deleted_at IS NULL) THEN
        RAISE EXCEPTION 'platform knowledge document does not exist';
    END IF;
    INSERT INTO outbox_event (class_id,type,payload)
    VALUES (NULL,'platform.knowledge_document.created',jsonb_build_object('documentId',document::text))
    RETURNING id INTO event_id;
    RETURN event_id;
END
$$;
REVOKE ALL ON FUNCTION enqueue_platform_knowledge_document(BIGINT) FROM PUBLIC;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        REVOKE ALL ON platform_knowledge_blob, platform_knowledge_document, platform_knowledge_entry FROM easygpa_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON
            platform_knowledge_blob,platform_knowledge_document,platform_knowledge_entry TO easygpa_ops;
        GRANT USAGE,SELECT ON
            platform_knowledge_blob_id_seq,platform_knowledge_document_id_seq,platform_knowledge_entry_id_seq TO easygpa_ops;
        GRANT EXECUTE ON FUNCTION enqueue_platform_knowledge_document(BIGINT) TO easygpa_ops;
    END IF;
END
$$;
