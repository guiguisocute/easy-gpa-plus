ALTER TABLE agent_action DROP CONSTRAINT agent_action_kind_check;
ALTER TABLE agent_action ADD CONSTRAINT agent_action_kind_check
    CHECK (kind IN ('submission_draft','review_draft','scheme_draft','export_draft','arbitration_reason_draft'));
CREATE INDEX agent_message_resource_history ON agent_message
    (conversation_id, (request_context->>'view'), (request_context->>'resourceKind'), (request_context->>'resourceId'), id)
    WHERE status='complete';
