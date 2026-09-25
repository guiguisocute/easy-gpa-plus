-- Give operators an emergency switch and bounded parser settings. Existing
-- rows retain their other policy fields and receive safe defaults.
UPDATE ops_config
   SET value = value || '{"nativeToolsEnabled":true}'::jsonb, updated_at = now()
 WHERE key = 'flags';

UPDATE ops_config
   SET value = value || '{"knowledgeMaxArchiveMb":256,"knowledgeMaxExtractedTextMb":32,"knowledgeConverterTimeoutSeconds":60}'::jsonb,
       updated_at = now()
 WHERE key = 'lifecycle';
