-- 让 Agent 的处理过程在运行期间就能被前端读到。
--
-- thought 是给用户看的一句进度说明（“先查一下学院细则怎么算志愿服务”），
-- 不是模型的原始推理转储；worker 在每一步开始时就写入，前端轮询即可逐步回显。
-- status 增加 running，是因为工具行现在先于工具执行落库。
ALTER TABLE agent_tool_call ADD COLUMN thought TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_tool_call DROP CONSTRAINT agent_tool_call_status_check;
ALTER TABLE agent_tool_call ADD CONSTRAINT agent_tool_call_status_check
    CHECK (status IN ('running', 'complete', 'failed', 'canceled'));

-- 最终回答之前的那一句说明没有对应的工具行，单独存在消息上。
ALTER TABLE agent_message ADD COLUMN final_thought TEXT NOT NULL DEFAULT '';
