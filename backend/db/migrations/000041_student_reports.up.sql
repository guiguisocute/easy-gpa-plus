-- 学生匿名举报。
--
-- 综测小组以外的学生也可以对同学的计分提出举报，对象和「扣分与异议」一样有三种：
-- 基础项（base）、扣分项（penalty）、已定分条目（submission）。和小组提案的区别有两条：
--   1. 举报是匿名的——同学、班长、审核员都读不到举报人；
--   2. 举报不进班级管理员的小组提案队列，先由两名审核人背靠背复核，
--      两人一致即落定，不一致才升给班级管理员终裁。
--
-- 匿名怎么落到数据库上：举报本体（report）里根本没有举报人这一列，举报人单独存进
-- report_reporter，而 easygpa_app 对这张表**只有 INSERT 权限**。业务真正需要的三个
-- 问题——「我是不是已经举报过这一条」「我提过哪些举报」「我今天提了几条」——都由
-- SECURITY DEFINER 函数回答，三个函数都要求先给出举报人再回答，反过来查不了。
-- 这和 000002 里 auth_find_user 的思路一致：窄口子给出精确匹配，不给任意扫描。
-- 平台运维（easygpa_ops）保留 SELECT，用于滥用调查。
--
-- 分值语义按 kind 分：
--   penalty     —— proposed_score = 次数 × 单价，必为负；落定时**累加**到已有扣分上；
--   base        —— proposed_score 是建议的新得分，必须低于当前分；落定时覆盖；
--   submission  —— proposed_score 是建议的新认定分，必须低于当前分；落定时覆盖。
-- 三种共同的硬约束：举报只能让被举报人的分变差，绝不能变好。举报不是送分的通道。

CREATE TABLE report (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('base','penalty','submission')),
    -- 被举报人。举报人不在这张表里，这是匿名的前提。
    student_id      BIGINT NOT NULL,
    scheme_id       BIGINT NOT NULL,
    category_key    TEXT NOT NULL,
    item_key        TEXT NOT NULL,
    -- base_score.id 或 submission.id。扣分项在还没有记录时为空。
    target_id       BIGINT,
    -- 举报当时的分，只作展示与"不得变好"的判定基线。
    current_score   NUMERIC(10,3),
    quantity        NUMERIC(10,3),
    proposed_score  NUMERIC(10,3) NOT NULL,
    basis           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'reviewing'
                    CHECK (status IN ('reviewing','applied','dismissed','escalated','final')),
    final_score     NUMERIC(10,3),
    decided_by      BIGINT,
    decision_reason TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id),
    FOREIGN KEY (class_id, decided_by) REFERENCES app_user(class_id, id),
    -- 次数只属于扣分项：基础项和已定分条目报的是分，不是次数。
    CHECK ((kind='penalty' AND quantity IS NOT NULL AND proposed_score < 0)
        OR (kind<>'penalty' AND quantity IS NULL AND proposed_score >= 0)),
    -- 终态必须有分和理由；escalated 是等班管签字，还不算终态。
    CHECK ((status IN ('reviewing','escalated') AND decided_at IS NULL AND decision_reason IS NULL)
        OR (status IN ('applied','dismissed','final') AND decided_at IS NOT NULL AND decision_reason IS NOT NULL)),
    -- 只有班管终裁（final）才会记下签字人；两人一致落定的那两种没有单一签字人。
    CHECK ((status='final' AND decided_by IS NOT NULL) OR (status<>'final' AND decided_by IS NULL))
);

CREATE INDEX report_by_student ON report (class_id, student_id, created_at DESC, id DESC);
CREATE INDEX report_admin_queue ON report (class_id, status, created_at, id)
    WHERE status='escalated';

ALTER TABLE report ENABLE ROW LEVEL SECURITY;
ALTER TABLE report FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON report
    USING (class_id = app_current_class_id())
    WITH CHECK (class_id = app_current_class_id());

-- 两名背靠背复核人。和 submission_reviewer + review 是同一套形状：
-- uphold 按举报的分落定，adjust 改分（只能往对被举报人有利的方向改），
-- reject 判举报不成立，维持原样。
CREATE TABLE report_reviewer (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id      BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    report_id     BIGINT NOT NULL,
    reviewer_id   BIGINT NOT NULL,
    assigned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decision      TEXT,
    score         NUMERIC(10,3),
    reason        TEXT,
    spent_seconds INT,
    decided_at    TIMESTAMPTZ,
    UNIQUE (report_id, reviewer_id),
    FOREIGN KEY (class_id, report_id) REFERENCES report(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, reviewer_id) REFERENCES app_user(class_id, id),
    CHECK ((decision IS NULL AND score IS NULL AND reason IS NULL AND spent_seconds IS NULL AND decided_at IS NULL)
        OR (decision IN ('uphold','adjust','reject') AND score IS NOT NULL AND reason IS NOT NULL
            AND spent_seconds IS NOT NULL AND decided_at IS NOT NULL))
);

CREATE INDEX report_reviewer_queue ON report_reviewer (class_id, reviewer_id, decided_at, assigned_at);

ALTER TABLE report_reviewer ENABLE ROW LEVEL SECURITY;
ALTER TABLE report_reviewer FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON report_reviewer
    USING (class_id = app_current_class_id())
    WITH CHECK (class_id = app_current_class_id());

-- 举报人身份。业务连接只写不读。
CREATE TABLE report_reporter (
    report_id   BIGINT PRIMARY KEY,
    class_id    BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    reporter_id BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (class_id, report_id) REFERENCES report(class_id, id) ON DELETE CASCADE,
    FOREIGN KEY (class_id, reporter_id) REFERENCES app_user(class_id, id)
);

CREATE INDEX report_reporter_by_person ON report_reporter (class_id, reporter_id, report_id);

ALTER TABLE report_reporter ENABLE ROW LEVEL SECURITY;
ALTER TABLE report_reporter FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON report_reporter
    USING (class_id = app_current_class_id() OR current_user='easygpa_ops')
    WITH CHECK (class_id = app_current_class_id());

-- 「这个人是不是已经举报过这一条」。必须先给出举报人，所以拿不到"这条是谁报的"。
CREATE FUNCTION report_already_filed(
    p_class_id BIGINT, p_reporter_id BIGINT, p_student_id BIGINT,
    p_scheme_id BIGINT, p_kind TEXT, p_category_key TEXT, p_item_key TEXT
) RETURNS BOOLEAN
LANGUAGE sql SECURITY DEFINER STABLE
SET search_path = public
AS $$
    SELECT EXISTS (
        SELECT 1 FROM report_reporter rr JOIN report r ON r.id=rr.report_id
         WHERE rr.class_id=p_class_id AND rr.reporter_id=p_reporter_id
           AND r.student_id=p_student_id AND r.scheme_id=p_scheme_id
           AND r.kind=p_kind AND r.category_key=p_category_key AND r.item_key=p_item_key
           AND r.status IN ('reviewing','escalated')
    )
$$;

-- 「我提过哪些举报」。同样只能从人查到举报，不能从举报查到人。
CREATE FUNCTION report_ids_by_reporter(p_class_id BIGINT, p_reporter_id BIGINT)
RETURNS SETOF BIGINT
LANGUAGE sql SECURITY DEFINER STABLE
SET search_path = public
AS $$
    SELECT report_id FROM report_reporter
     WHERE class_id=p_class_id AND reporter_id=p_reporter_id
$$;

-- 今天这个人提了几条，用来限流。同样是单向的。
CREATE FUNCTION report_count_today(p_class_id BIGINT, p_reporter_id BIGINT)
RETURNS INTEGER
LANGUAGE sql SECURITY DEFINER STABLE
SET search_path = public
AS $$
    SELECT count(*)::int FROM report_reporter
     WHERE class_id=p_class_id AND reporter_id=p_reporter_id
       AND created_at >= date_trunc('day', now())
$$;

-- 学生举报开关加在 scheme.capabilities 上，和 submit/edit/appeal/review 同一组，
-- 由「时间线与窗口」管理。这里**不回填**已发布的方案，有两个原因：
--   1. 已发布的方案是不可变的（000002 的 scheme_immutable 触发器），UPDATE 会直接报错；
--   2. 也不需要回填——JSON 里没有这个键时，Go 反序列化成 Capabilities 的零值 false，
--      正好是"默认关闭"。班级管理员打开开关时会克隆出一个带这个键的新版本。
-- 换句话说：旧方案照常读，读出来就是关的。

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON report TO easygpa_app;
        GRANT USAGE, SELECT ON SEQUENCE report_id_seq TO easygpa_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON report_reviewer TO easygpa_app;
        GRANT USAGE, SELECT ON SEQUENCE report_reviewer_id_seq TO easygpa_app;
        -- 只写不读：举报人身份对业务连接是单向的。
        GRANT INSERT ON report_reporter TO easygpa_app;
        GRANT EXECUTE ON FUNCTION report_already_filed(BIGINT,BIGINT,BIGINT,BIGINT,TEXT,TEXT,TEXT) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION report_ids_by_reporter(BIGINT,BIGINT) TO easygpa_app;
        GRANT EXECUTE ON FUNCTION report_count_today(BIGINT,BIGINT) TO easygpa_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
        GRANT SELECT ON report, report_reviewer, report_reporter TO easygpa_ops;
    END IF;
END
$$;
