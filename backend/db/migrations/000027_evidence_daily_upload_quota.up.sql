-- Bound storage reservations for ordinary evidence uploads. The byte budget
-- is checked while holding the same per-user advisory quota lock used by
-- Agent attachments, so concurrent presigns cannot oversubscribe it.
UPDATE ops_config
   SET value=value||'{"evidenceDailyMb":1024}'::jsonb,updated_at=now()
 WHERE key='flags' AND NOT (value ? 'evidenceDailyMb');

CREATE INDEX evidence_created_by_daily
    ON evidence (class_id, created_by, created_at DESC);
