-- “问 Agent”消息的图片附件。对象只允许由所属用户绑定到自己尚未发送的消息，
-- worker 仍通过同一个租户 RLS 上下文读取，不能跨班级拿到对象键。
CREATE TABLE agent_attachment (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id    BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    owner_id    BIGINT NOT NULL,
    message_id  BIGINT,
    object_key  TEXT NOT NULL UNIQUE,
    filename    TEXT NOT NULL,
    media_type  TEXT NOT NULL CHECK (media_type IN ('image/jpeg','image/png','image/webp')),
    size_bytes  BIGINT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 5242880),
    sha256      TEXT CHECK (sha256 IS NULL OR sha256 ~ '^[0-9a-f]{64}$'),
    status      TEXT NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading','ready')),
    uploaded_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status='uploading' AND sha256 IS NULL AND uploaded_at IS NULL)
        OR (status='ready' AND sha256 IS NOT NULL AND uploaded_at IS NOT NULL)),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, owner_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, message_id) REFERENCES agent_message(class_id, id) ON DELETE CASCADE
);

CREATE INDEX agent_attachment_owner_unbound
    ON agent_attachment (class_id, owner_id, created_at DESC) WHERE message_id IS NULL;
CREATE INDEX agent_attachment_message
    ON agent_attachment (class_id, message_id, id) WHERE message_id IS NOT NULL;

ALTER TABLE agent_attachment ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_attachment FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON agent_attachment
    USING (class_id = app_current_class_id())
    WITH CHECK (class_id = app_current_class_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON agent_attachment TO easygpa_app;
        GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
    END IF;
END
$$;
