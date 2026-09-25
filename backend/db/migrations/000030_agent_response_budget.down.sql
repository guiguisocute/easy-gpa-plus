UPDATE ops_config
   SET value=value-'agentMaxAnswerKb',updated_at=now()
 WHERE key='ai';
