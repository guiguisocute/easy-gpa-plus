-- 回滚很干净，因为 up 没有改过任何一行 scheme.config：老的已发布方案里那三个
-- 运行期键从来没被删过，降级后旧代码照样读得到它们。
--
-- 会丢的是 up 之后才产生的东西：全系统封锁时间、奖学金档位配置，以及 settlement
-- 上算好的档位。旧结构里没有它们的落脚点，这是降级的固有代价，不是疏漏。

ALTER TABLE settlement DROP COLUMN IF EXISTS award_tier;

DROP TABLE IF EXISTS class_timeline;
