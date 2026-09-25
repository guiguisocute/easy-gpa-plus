-- Keep a tamper-evident reference to each logical model request without
-- storing image Base64, local paths or filenames in the audit tables.

ALTER TABLE ai_batch
    ADD COLUMN request_hash TEXT,
    ADD COLUMN repair_request_hash TEXT,
    ADD CONSTRAINT ai_batch_request_hash_format
        CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT ai_batch_repair_request_hash_format
        CHECK (repair_request_hash IS NULL OR repair_request_hash ~ '^[0-9a-f]{64}$');

ALTER TABLE ai_item
    ADD COLUMN request_hash TEXT,
    ADD COLUMN repair_request_hash TEXT,
    ADD CONSTRAINT ai_item_request_hash_format
        CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT ai_item_repair_request_hash_format
        CHECK (repair_request_hash IS NULL OR repair_request_hash ~ '^[0-9a-f]{64}$');
