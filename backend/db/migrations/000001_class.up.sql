-- 多租户的根:班级即租户(§2.1)。
-- 后续所有业务表都带 class_id 外键并启用 RLS(§3.1),迁移在各自文件里追加。
CREATE TABLE class (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        TEXT        NOT NULL,
    archived    BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
