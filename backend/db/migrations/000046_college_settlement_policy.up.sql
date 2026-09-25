-- 旧快照保留，但不再作为新口径的有效结算；否则「重新结算」会直接复用旧结果。
-- 不自动重算或发送通知，由班管在导出中心重新结算。
INSERT INTO settlement_invalidation (class_id,run_id,reason)
SELECT r.class_id,r.id,'college_policy_2026: independent rounded quotas and all-category honor'
  FROM settlement_run r
 WHERE r.status='complete'
   AND NOT EXISTS (SELECT 1 FROM settlement_invalidation i WHERE i.run_id=r.id);
