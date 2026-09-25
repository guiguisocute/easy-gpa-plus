UPDATE ops_config
   SET value = value - 'exportConcurrency',
       updated_at = now()
 WHERE key = 'flags';
