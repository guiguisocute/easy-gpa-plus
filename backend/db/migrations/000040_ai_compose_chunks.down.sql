DROP TABLE IF EXISTS ai_compose_chunk;
ALTER TABLE ai_batch DROP COLUMN IF EXISTS compose_stop_requested;
ALTER TABLE ai_batch DROP COLUMN IF EXISTS compose_thinking;
