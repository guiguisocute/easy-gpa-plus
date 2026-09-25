-- A rollback restores only the legacy default. It deliberately keeps files
-- already published to the class visible instead of silently hiding them.
ALTER TABLE knowledge_document
    ALTER COLUMN visibility SET DEFAULT 'admin';
