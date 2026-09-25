UPDATE ops_config
   SET value = value || '{"exportConcurrency":1}'::jsonb,
       updated_at = now()
 WHERE key = 'flags' AND NOT (value ? 'exportConcurrency');
