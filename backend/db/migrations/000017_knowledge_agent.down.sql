UPDATE ops_config
   SET value = value - 'agentModel' - 'agentMaxSteps' - 'agentTimeoutSeconds'
                     - 'agentToolResultKb' - 'agentDailyMessages'
                     - 'knowledgeMaxFilesPerClass' - 'knowledgeMaxStorageMbPerClass',
       updated_at = now()
 WHERE key = 'ai';

UPDATE ops_config
   SET value = value - 'knowledgeEnabled' - 'agentActionsEnabled' - 'knowledgeEgressEnabled',
       updated_at = now()
 WHERE key = 'flags';

DROP TABLE IF EXISTS agent_action;
DROP TABLE IF EXISTS agent_tool_call;
DROP TABLE IF EXISTS agent_message;
DROP TABLE IF EXISTS agent_conversation;
DROP TABLE IF EXISTS knowledge_entry;
DROP TABLE IF EXISTS knowledge_document;
DROP TABLE IF EXISTS knowledge_blob;
DROP TABLE IF EXISTS knowledge_policy;
