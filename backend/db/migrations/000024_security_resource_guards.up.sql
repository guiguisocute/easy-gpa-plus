-- Make quota checks cheap and enforce export deduplication below the HTTP layer.

UPDATE ops_config
   SET value=value||'{"passwordHashConcurrency":4,"apiRateLimitPerMinute":180,"apiRateLimitBurst":60,"authLoginPerMinute":10,"authRefreshPerMinute":60,"authRegisterPerHour":20,"authForgotPerHour":5,"authResetPerHour":10,"evidencePresignPerHour":120,"aiPresignPerHour":300,"agentPresignPerHour":120,"knowledgePresignPerHour":120,"aiBatchActionsPerHour":10,"agentMessagesPerMinute":20,"exportRequestsPerHour":10,"knowledgeReprocessPerHour":30}'::jsonb,
       updated_at=now()
 WHERE key='flags';

UPDATE ops_config
   SET value=value||'{"materialActiveBatches":3}'::jsonb,updated_at=now()
 WHERE key='ai';

ALTER TABLE ai_batch ADD COLUMN queued_at TIMESTAMPTZ;

-- Batches that reached a worker have consumed model resources even if the user
-- later canceled them. Preserve that fact when upgrading existing databases.
UPDATE ai_batch
   SET queued_at=COALESCE(started_at,created_at)
 WHERE status IN ('queued','processing','review','complete','failed')
    OR (status='canceled' AND started_at IS NOT NULL);

CREATE INDEX ai_batch_student_daily
    ON ai_batch (class_id,student_id,queued_at DESC)
    WHERE queued_at IS NOT NULL;

CREATE INDEX agent_message_actor_daily
    ON agent_message (class_id,actor_id,created_at DESC)
    WHERE role='user';

CREATE INDEX export_job_product_history
    ON export_job (class_id,run_id,kind,created_at DESC)
    WHERE status IN ('queued','running','complete');

-- Older deployments may already contain duplicate active jobs. Keep the
-- oldest job runnable and make the redundant rows terminal before adding the
-- invariant.
WITH ranked AS (
    SELECT id,row_number() OVER (
        PARTITION BY class_id,run_id,kind ORDER BY created_at,id
    ) AS position
      FROM export_job
     WHERE status IN ('queued','running')
)
UPDATE export_job j
   SET status='failed',finished_at=now(),
       error_message='superseded by an earlier identical export request'
  FROM ranked r
 WHERE j.id=r.id AND r.position>1;

CREATE UNIQUE INDEX export_job_one_active_product
    ON export_job (class_id,run_id,kind)
    WHERE status IN ('queued','running');
