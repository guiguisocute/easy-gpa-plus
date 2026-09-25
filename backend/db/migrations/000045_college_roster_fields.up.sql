-- 补上学院报表要用、而系统一直没存的三个字段。
--
-- 学院的附件4、附件5 要 性别、年级、专业、学年、学院、"与教务在线一致的班级
-- 名"。这些以前只能手工补，但其中大部分本来就是已知的，不该让班委一格一格敲：
--
--   性别            per-student，真的是新数据 → whitelist.gender
--   教务在线班级名  per-class，如"24级计算机科学与技术2班" → class_timeline
--   学院全称        per-class，一次填一个词 → class_timeline
--   年级 / 专业     从教务班级名解析，不另存（"24级计算机科学与技术2班"
--                   → 2024级 / 计算机科学与技术）
--   评定学年        独立于提交窗口；秋季评定通常评上一学年
--
-- 教务班级名和 class.name 是两个东西：库里这个班叫"示例班级"，教务在线叫
-- "24级计算机科学与技术2班"，而附件3 明写"班级名称请务必和教务在线上班级名称
-- 保持一致"。所以它必须单独存一份，不能拿 class.name 顶。
--
-- 三个字段都可空：老班没填之前，报表里对应格子留空，而不是编一个。
-- 身份证号、银行卡号、开户行不在这里——那是另一类数据，要连加密存储和访问
-- 控制一起设计，本次不碰。

ALTER TABLE whitelist ADD COLUMN gender TEXT
    CHECK (gender IS NULL OR gender IN ('男', '女'));

COMMENT ON COLUMN whitelist.gender IS
    '学生性别，随名单一起导入；NULL 表示名单里没带这一列';

ALTER TABLE class_timeline ADD COLUMN college_name TEXT;
ALTER TABLE class_timeline ADD COLUMN enrollment_class TEXT;
ALTER TABLE class_timeline ADD COLUMN academic_year TEXT NOT NULL
    DEFAULT (to_char(CURRENT_DATE - INTERVAL '1 year', 'YYYY') || '-' || to_char(CURRENT_DATE, 'YYYY'));
COMMENT ON COLUMN class_timeline.academic_year IS
    '实际评定学年，如2025-2026；初始为上一年度至本年度，班管可修改，不从封存日期推断';

COMMENT ON COLUMN class_timeline.college_name IS
    '学院全称，如"示例学院"；填进学院报表的学院列';
COMMENT ON COLUMN class_timeline.enrollment_class IS
    '教务在线上的班级名，如"24级计算机科学与技术2班"；报表的年级与专业由它解析';
