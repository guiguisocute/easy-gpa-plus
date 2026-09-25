INSERT INTO ops_config (key, value)
VALUES (
    'ai',
    '{"baseUrl":"","apiKey":"","textModel":"","visionModel":""}'::jsonb
)
ON CONFLICT (key) DO NOTHING;

UPDATE ops_config
   SET value = jsonb_set(value, '{aiEnabled}', COALESCE(value->'aiEnabled', 'false'::jsonb), true),
       updated_at = now()
 WHERE key = 'flags';
