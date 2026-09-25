UPDATE ops_config
   SET value = jsonb_set(value, '{uploadMaxMb}', '20'::jsonb),
       updated_at = now()
 WHERE key = 'flags'
   AND COALESCE((value->>'uploadMaxMb')::integer, 50) = 50;

ALTER TABLE evidence DROP COLUMN IF EXISTS object_etag;
