-- 放开 export_job.kind，容纳第四个导出产物「学院报表」。
--
-- 学院每年下发 5 份附件，班委要把结算结果按附件的列头填进去。现有三个产物里
-- 只有汇总表沾边，而且写的是各大项的原始分（0–100），附件要的是按权重折算后
-- 保留两位小数的分——中间隔着一次逐格换算，抄一遍就是抄一遍错的机会。
--
-- college 是纯渲染产物：读的还是同一份结算快照，不新增任何存储、不改任何算分
-- 逻辑，只是把同一批数把附件的样子排一遍。前三个产物一个都不动，佐证归档包
-- 仍然独立生成、独立下载——学院要的"5 份附件打包成一个文件夹"和佐证材料是
-- 两件事，不能互相顶替。

ALTER TABLE export_job DROP CONSTRAINT export_job_kind_check;

ALTER TABLE export_job ADD CONSTRAINT export_job_kind_check
    CHECK (kind IN ('summary', 'detail', 'archive', 'college'));

COMMENT ON COLUMN export_job.kind IS
    'summary=汇总表 detail=逐人明细 archive=佐证归档包 college=学院报表（按学院附件列头排版）';
