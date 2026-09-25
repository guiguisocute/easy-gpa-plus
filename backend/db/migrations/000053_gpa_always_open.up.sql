-- GPA 导入不再属于可配置业务开关，也不受材料提交窗口限制。
-- 清除所有已有班级（含归档班）的旧开关和截止；没有时间线的班由应用默认值覆盖。
-- 不改开放/封存/全系统封锁时间，不改历史方案、结算快照或已导入成绩。
UPDATE class_timeline
   SET capabilities = capabilities - 'gpa', updated_at = now()
 WHERE capabilities ? 'gpa';
