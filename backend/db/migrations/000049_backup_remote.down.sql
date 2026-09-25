-- 保留已经产生的远程任务记录；存在记录时拒绝降级，不能为回滚删除审计历史。
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM backup_job WHERE kind = 'remote_push') THEN
        RAISE EXCEPTION 'remote backup jobs exist; refusing destructive downgrade';
    END IF;
END $$;
ALTER TABLE backup_job DROP CONSTRAINT backup_job_kind_check;
ALTER TABLE backup_job ADD CONSTRAINT backup_job_kind_check
    CHECK (kind IN ('backup', 'restore_drill'));
DELETE FROM ops_config WHERE key = 'backup_remote';
