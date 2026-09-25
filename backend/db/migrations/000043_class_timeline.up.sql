-- 把窗口、业务开关和评优口径从 scheme.config 里搬出来，另加一个全系统封锁时间。
--
-- 起因是一条说不通的因果：管理员把截止时间延长三天，系统就凭空造出一个"新的
-- 评优方案版本"，还要把 base_score 和 student_gpa 两张表整体复制到新 scheme_id
-- 下面。方案是评分规则，窗口是这个班这学期怎么排日程，两件事没有任何绑定的
-- 必要。剥开之后，改时间线就只是改一行。
--
-- scheme.config 仍然是不可变的——这一点没有松动，只是它现在只装它真正拥有的
-- 东西：weights 和 categories。000037 对 platform_template 做过同一件事。
--
-- 三个时间点的分工：
--   open_at     窗口打开
--   close_at    封存截止：不能再交新材料，全班自动封存。审核、申诉、异议照常
--   lockdown_at 全系统封锁：之后谁都不能改任何东西，只剩查看与导出
--
-- lockdown_at 可空。允许它等于 close_at（封存即封死，只是不留复议尾巴），
-- 但不允许早于 close_at——那样封存动作本身就会撞上封锁。

CREATE TABLE class_timeline (
    class_id     BIGINT PRIMARY KEY REFERENCES class(id) ON DELETE CASCADE,
    open_at      TIMESTAMPTZ NOT NULL,
    close_at     TIMESTAMPTZ NOT NULL,
    lockdown_at  TIMESTAMPTZ,
    capabilities JSONB NOT NULL,
    honor_roll   JSONB NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (open_at < close_at),
    CHECK (lockdown_at IS NULL OR lockdown_at >= close_at)
);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON class_timeline TO easygpa_app;
    END IF;
END
$$;

ALTER TABLE class_timeline ENABLE ROW LEVEL SECURITY;
ALTER TABLE class_timeline FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON class_timeline
    USING (class_id=app_current_class_id()) WITH CHECK (class_id=app_current_class_id());

-- 回填：取每个班最新的已发布方案，把它的运行期设置抬进新表。
-- 没有已发布方案的班不建行——第一次读时由 classtimeline.Load 按默认值补上，
-- 在这里替它们编一套日期没有意义。
--
-- honorRoll.awards 在老数据里不存在（档位一直硬编码在前端），统一补上
-- 一等 5 / 二等 10 / 三等 15，也就是搬进来之前学生页显示的那三档。
INSERT INTO class_timeline (class_id, open_at, close_at, capabilities, honor_roll)
SELECT s.class_id,
       (s.config->'window'->>'open')::timestamptz,
       (s.config->'window'->>'close')::timestamptz,
       s.config->'capabilities',
       jsonb_build_object(
           'topPercent', COALESCE((s.config->'honorRoll'->>'topPercent')::int, 30),
           'awards', jsonb_build_array(
               jsonb_build_object('name','一等综合素质奖学金','topPercent',5),
               jsonb_build_object('name','二等综合素质奖学金','topPercent',10),
               jsonb_build_object('name','三等综合素质奖学金','topPercent',15)
           )
       )
  FROM (
       SELECT DISTINCT ON (class_id) class_id, config
         FROM scheme
        WHERE status='published'
        ORDER BY class_id, version DESC, id DESC
  ) s
 WHERE s.config ? 'window'
   AND s.config ? 'capabilities'
   AND (s.config->'window'->>'open') IS NOT NULL
   AND (s.config->'window'->>'close') IS NOT NULL
   AND (s.config->'window'->>'open')::timestamptz < (s.config->'window'->>'close')::timestamptz
ON CONFLICT (class_id) DO NOTHING;

-- 不从老的 scheme.config 里删这三个键。
--
-- 本来想删干净，但 scheme_published_immutable 触发器会挡住——已发布方案不可变
-- 是这套系统真正依赖的保证（提交的规则快照、结算 run 都挂在这些行上），为了让
-- JSONB 看起来整齐而临时关掉它，不划算。
--
-- 而且不删也不会有歧义：scheme.Config 上这三个字段现在是 `json:"-"`，应用根本
-- 读不出来，它们是惰性的。老行里留着的那份窗口，也确实是它发布当时的历史事实。
-- 新发布的版本不会再带这几个键。

-- 奖学金档位从"前端按名次现算"变成结算器算定的快照字段。
-- 历史结算行留 NULL：那些快照产出时系统里根本没有档位这个概念，回填等于编造。
ALTER TABLE settlement ADD COLUMN award_tier TEXT;

COMMENT ON COLUMN settlement.award_tier IS
    '结算时命中的奖学金档位名称；NULL 表示未进档或该行早于档位功能';
