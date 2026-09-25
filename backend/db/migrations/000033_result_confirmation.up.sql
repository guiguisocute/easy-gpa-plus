-- 终审结果确认。学生确认的对象是一个具体版本的终审结论：(batch_id,batch_completed_at)
-- 就是那一版的身份。批次被 stale（解封/外部改动重建）或重新打开又重新完成（申诉、
-- 问题闭环改了分）之后，这一行自动失去效力，学生必须重新确认；判定规则的唯一权威
-- 表述见 docs/RESULT-CONFIRM-BACKEND-CONTRACT.md §1.2。行永不删改：每一次确认
--（包括被后续改动作废的那些）都是审计事实。
CREATE TABLE result_confirmation (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id           BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    student_id         BIGINT NOT NULL,
    scheme_id          BIGINT NOT NULL,
    batch_id           UUID NOT NULL,
    batch_completed_at TIMESTAMPTZ NOT NULL,
    source             TEXT NOT NULL CHECK (source IN ('manual','auto')),
    confirmed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 当时生效的确认时限；日后调整 confirm_hours 不改写既成事实。
    deadline_at        TIMESTAMPTZ NOT NULL,
    UNIQUE (class_id,id),
    UNIQUE (class_id,student_id,batch_id,batch_completed_at),
    FOREIGN KEY (class_id,student_id) REFERENCES app_user(class_id,id),
    FOREIGN KEY (class_id,scheme_id) REFERENCES scheme(class_id,id),
    FOREIGN KEY (class_id,batch_id) REFERENCES scorecard_audit_batch(class_id,id)
);

CREATE INDEX result_confirmation_student
    ON result_confirmation (class_id,scheme_id,student_id,confirmed_at DESC);

ALTER TABLE result_confirmation ENABLE ROW LEVEL SECURITY;
ALTER TABLE result_confirmation FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON result_confirmation
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        -- 只授 SELECT,INSERT：确认一经写入就是历史，应用层无权改删。
        GRANT SELECT,INSERT ON result_confirmation TO easygpa_app;
        GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
    END IF;
END
$$;

-- 学生确认时限，与 item/scorecard SLA 同住一张班级级时间参数表。上限放宽到
-- 720 小时（30 天）而不是 168：审核 SLA 逾期只发提醒，确认超时会直接替学生
-- 做出终局动作，横跨假期两周是正当配置。
ALTER TABLE review_sla_config
    ADD COLUMN confirm_hours INTEGER NOT NULL DEFAULT 72
    CHECK (confirm_hours BETWEEN 1 AND 720);
