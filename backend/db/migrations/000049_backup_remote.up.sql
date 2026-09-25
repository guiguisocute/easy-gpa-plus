-- 远程备份仓库（异地副本）。桶地址、桶名这些非机密项在运维页上配；
-- AccessKey/SecretKey 由 API 用 BACKUP_REMOTE_SECRET_KEY 加密成 enc:v1 后写进来，
-- 这里播的是空值，不含任何凭据。
ALTER TABLE backup_job DROP CONSTRAINT backup_job_kind_check;
ALTER TABLE backup_job ADD CONSTRAINT backup_job_kind_check
    CHECK (kind IN ('backup', 'restore_drill', 'remote_push'));

INSERT INTO ops_config (key, value)
VALUES (
    'backup_remote',
    '{"enabled":false,"endpoint":"","bucket":"","region":"","prefix":"","useSsl":true,"pathStyle":false,"retentionDays":90}'::jsonb
)
ON CONFLICT (key) DO NOTHING;
