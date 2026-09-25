-- Preserve generated drafts and audit history; rollback must not silently erase
-- or relabel them as another action kind.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM agent_action WHERE kind='arbitration_reason_draft') THEN
        RAISE EXCEPTION 'Cannot roll back agent page context while reason drafts exist';
    END IF;
END $$;
DROP INDEX agent_message_resource_history;
ALTER TABLE agent_action DROP CONSTRAINT agent_action_kind_check;
ALTER TABLE agent_action ADD CONSTRAINT agent_action_kind_check
    CHECK (kind IN ('submission_draft','review_draft','scheme_draft','export_draft'));
