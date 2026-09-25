UPDATE ops_config
   SET value=value||'{"materialMaxBatchMb":256}'::jsonb,updated_at=now()
 WHERE key='ai' AND NOT (value ? 'materialMaxBatchMb');
