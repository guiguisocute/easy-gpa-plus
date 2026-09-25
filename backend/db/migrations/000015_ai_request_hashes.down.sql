ALTER TABLE ai_item
    DROP CONSTRAINT IF EXISTS ai_item_repair_request_hash_format,
    DROP CONSTRAINT IF EXISTS ai_item_request_hash_format,
    DROP COLUMN IF EXISTS repair_request_hash,
    DROP COLUMN IF EXISTS request_hash;

ALTER TABLE ai_batch
    DROP CONSTRAINT IF EXISTS ai_batch_repair_request_hash_format,
    DROP CONSTRAINT IF EXISTS ai_batch_request_hash_format,
    DROP COLUMN IF EXISTS repair_request_hash,
    DROP COLUMN IF EXISTS request_hash;
