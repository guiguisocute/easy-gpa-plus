UPDATE ops_config
   SET value = value - 'agentToolScanMb', updated_at = now()
 WHERE key = 'ai';
