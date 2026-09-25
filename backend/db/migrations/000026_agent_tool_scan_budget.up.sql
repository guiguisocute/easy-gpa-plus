-- Bound the amount of extracted text an Agent message may scan through tools.
UPDATE ops_config
   SET value = value || '{"agentToolScanMb":64}'::jsonb, updated_at = now()
 WHERE key = 'ai';
