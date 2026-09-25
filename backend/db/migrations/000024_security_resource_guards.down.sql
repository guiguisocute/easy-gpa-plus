DROP INDEX IF EXISTS export_job_one_active_product;
DROP INDEX IF EXISTS export_job_product_history;
DROP INDEX IF EXISTS agent_message_actor_daily;
DROP INDEX IF EXISTS ai_batch_student_daily;
ALTER TABLE ai_batch DROP COLUMN IF EXISTS queued_at;

UPDATE ops_config
   SET value=value-ARRAY[
       'passwordHashConcurrency','apiRateLimitPerMinute','apiRateLimitBurst','authLoginPerMinute',
       'authRefreshPerMinute','authRegisterPerHour','authForgotPerHour',
       'authResetPerHour','evidencePresignPerHour','aiPresignPerHour',
       'agentPresignPerHour','knowledgePresignPerHour','aiBatchActionsPerHour',
       'agentMessagesPerMinute','exportRequestsPerHour','knowledgeReprocessPerHour'
   ]::text[],updated_at=now()
 WHERE key='flags';

UPDATE ops_config SET value=value-'materialActiveBatches',updated_at=now() WHERE key='ai';
