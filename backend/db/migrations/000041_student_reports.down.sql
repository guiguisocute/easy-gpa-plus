-- 回退学生匿名举报。举报数据整体删除：report_reporter 里存着举报人身份，
-- 留一张没人再读的表比删掉更糟。已经落到 base_score 的扣分不在这里回退——
-- 那些分是审核人或班级管理员签过字的，撤销要走业务动作，不是迁移。

DROP FUNCTION IF EXISTS report_count_today(BIGINT,BIGINT);
DROP FUNCTION IF EXISTS report_ids_by_reporter(BIGINT,BIGINT);
DROP FUNCTION IF EXISTS report_already_filed(BIGINT,BIGINT,BIGINT,BIGINT,TEXT,TEXT,TEXT);

DROP TABLE IF EXISTS report_reporter;
DROP TABLE IF EXISTS report_reviewer;
DROP TABLE IF EXISTS report;

-- capabilities.studentReport 不清理：已发布的方案不可变，改不动；而多出来的一个
-- 布尔键对旧代码无害——Go 反序列化会直接忽略它。
