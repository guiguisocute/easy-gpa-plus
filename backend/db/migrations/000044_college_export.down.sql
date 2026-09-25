-- 降级前必须先清掉 college 行，否则收窄后的 CHECK 立不住。
--
-- 删掉的是导出任务记录本身，不是任何业务数据：学院报表是从结算快照现渲染的，
-- 快照原样留着，降级后重新点一次汇总表照样出得来。对象存储里那几个 xlsx 会
-- 变成孤儿，等 export_job 的保留期清理跑过去即可——这里不去动它，因为删对象
-- 是不可逆的，而降级本身应当可以再升回来。

DELETE FROM export_job WHERE kind = 'college';

ALTER TABLE export_job DROP CONSTRAINT export_job_kind_check;

ALTER TABLE export_job ADD CONSTRAINT export_job_kind_check
    CHECK (kind IN ('summary', 'detail', 'archive'));
