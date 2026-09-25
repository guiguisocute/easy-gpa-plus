-- Existing installations keep operator-chosen limits. Only lift the original
-- 20 MB platform default so new 50 MB scheme rules can be published.
ALTER TABLE evidence ADD COLUMN IF NOT EXISTS object_etag TEXT;

UPDATE ops_config
   SET value = jsonb_set(value, '{uploadMaxMb}', '50'::jsonb),
       updated_at = now()
 WHERE key = 'flags'
   AND COALESCE((value->>'uploadMaxMb')::integer, 20) = 20;
