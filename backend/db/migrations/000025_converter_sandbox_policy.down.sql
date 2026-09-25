UPDATE ops_config
   SET value = value - 'nativeToolsEnabled', updated_at = now()
 WHERE key = 'flags';

UPDATE ops_config
   SET value = (value - 'knowledgeMaxExtractedTextMb' - 'knowledgeConverterTimeoutSeconds') || '{"knowledgeMaxArchiveMb":500}'::jsonb,
       updated_at = now()
 WHERE key = 'lifecycle';
