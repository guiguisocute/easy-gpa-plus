-- Keep model-controlled text and draft-action payloads from amplifying into
-- unbounded database and API response storage. Code retains a 512 KiB hard
-- ceiling; Ops can lower the effective per-answer budget at runtime.
UPDATE ops_config
   SET value=value||'{"agentMaxAnswerKb":128}'::jsonb,updated_at=now()
 WHERE key='ai' AND NOT (value ? 'agentMaxAnswerKb');
