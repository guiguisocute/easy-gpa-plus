-- 三列都是纯附加的报表字段，去掉不影响任何评分、排名或结算记录。
-- 会丢的是已经导入的性别和班级设置，降级后要重新导一次名单、重填一次学院与
-- 教务班级名。

ALTER TABLE class_timeline DROP COLUMN IF EXISTS academic_year;
ALTER TABLE class_timeline DROP COLUMN IF EXISTS enrollment_class;
ALTER TABLE class_timeline DROP COLUMN IF EXISTS college_name;
ALTER TABLE whitelist DROP COLUMN IF EXISTS gender;
