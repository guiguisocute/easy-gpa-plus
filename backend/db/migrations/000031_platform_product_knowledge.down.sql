DROP FUNCTION IF EXISTS enqueue_platform_knowledge_document(BIGINT);
DROP TABLE IF EXISTS platform_knowledge_entry;
DROP TABLE IF EXISTS platform_knowledge_document;
DROP TABLE IF EXISTS platform_knowledge_blob;
ALTER TABLE outbox_event DROP CONSTRAINT IF EXISTS outbox_event_platform_scope;
DELETE FROM outbox_event WHERE class_id IS NULL;
ALTER TABLE outbox_event ALTER COLUMN class_id SET NOT NULL;
