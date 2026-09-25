DROP INDEX IF EXISTS evidence_created_by_daily;

UPDATE ops_config
   SET value=value-'evidenceDailyMb',updated_at=now()
 WHERE key='flags';
