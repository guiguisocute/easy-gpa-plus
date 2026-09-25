-- 归组过去是整批一次调用、全程在内存里攒结果、成功才写一次库。任何一处失败——上游
-- 超时、worker 被重新部署掐掉、模型返回空——都会把前面十几分钟的识图连同已经归好的
-- 候选一起作废。逐图识别没有这个问题，因为每张图都是一行 ai_item 的检查点。
--
-- 这张表把同一套检查点给归组：每块一行，成功即落盘。重跑时只补 status <> 'complete'
-- 的块，已经归好的照原样带走。

CREATE TABLE ai_compose_chunk (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id      BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    batch_id      BIGINT NOT NULL,
    seq           INTEGER NOT NULL CHECK (seq >= 0),
    status        TEXT NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued','processing','complete','failed')),
    -- 这一块喂进去的观察，形如 [{"assetId":"12","page":0}]。重跑时据此重建输入，
    -- 不必依赖观察在批次里的下标顺序——那个顺序会随失败材料的重试而改变。
    observations  JSONB NOT NULL DEFAULT '[]'::jsonb,
    candidates    JSONB NOT NULL DEFAULT '[]'::jsonb,
    warnings      JSONB NOT NULL DEFAULT '[]'::jsonb,
    raw_response  JSONB,
    usage         JSONB NOT NULL DEFAULT '{}'::jsonb,
    model         TEXT,
    duration_ms   BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    attempts      INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    error         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (batch_id, seq),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, batch_id) REFERENCES ai_batch(class_id, id) ON DELETE CASCADE
);

CREATE INDEX ai_compose_chunk_batch_status ON ai_compose_chunk (class_id, batch_id, status, seq);

-- 推理模型先吐思考、再吐正文：实测归组时思考 11 秒就开始，正文要到 64 秒。只回显
-- 正文的话，等待期的头一分钟界面上必然一片空白。存末尾一段够看即可。
ALTER TABLE ai_batch ADD COLUMN compose_thinking TEXT NOT NULL DEFAULT '';

-- 「停止归组」和「放弃整批」是两件事：前者停在当前块、把已经归好的候选交给学生，
-- 后者连识图结果一起删。worker 在块与块之间读这个标记。
ALTER TABLE ai_batch ADD COLUMN compose_stop_requested BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE ai_compose_chunk ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_compose_chunk FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_compose_chunk
    USING (class_id = app_current_class_id())
    WITH CHECK (class_id = app_current_class_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON ai_compose_chunk TO easygpa_app;
        GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
    END IF;
END
$$;
