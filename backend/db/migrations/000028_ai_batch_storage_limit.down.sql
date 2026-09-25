UPDATE ops_config
   SET value=value-'materialMaxBatchMb',updated_at=now()
 WHERE key='ai';
