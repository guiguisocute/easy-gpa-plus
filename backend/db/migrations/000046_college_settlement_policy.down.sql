-- 只撤销本次迁移加的标记，保留其他业务原因导致的失效记录。
DELETE FROM settlement_invalidation
 WHERE reason='college_policy_2026: independent rounded quotas and all-category honor';
