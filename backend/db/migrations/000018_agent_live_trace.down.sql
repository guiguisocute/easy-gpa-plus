ALTER TABLE agent_message DROP COLUMN IF EXISTS final_thought;

-- 回滚前先把只在新版出现的中间态收敛掉，否则旧 CHECK 建不起来。
UPDATE agent_tool_call SET status = 'canceled' WHERE status = 'running';

ALTER TABLE agent_tool_call DROP CONSTRAINT agent_tool_call_status_check;
ALTER TABLE agent_tool_call ADD CONSTRAINT agent_tool_call_status_check
    CHECK (status IN ('complete', 'failed', 'canceled'));

ALTER TABLE agent_tool_call DROP COLUMN IF EXISTS thought;
