-- A downgrade must not erase ballots or silently restore single-person authority.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM submission WHERE source='collective_grant')
 OR EXISTS(SELECT 1 FROM governance_proposal)
 OR EXISTS(SELECT 1 FROM class_governance WHERE mode<>'centralized') THEN
  RAISE EXCEPTION 'collective governance history requires migration 64';
 END IF;
END $$;
DROP FUNCTION governance_due_proposals();
ALTER TABLE submission DROP CONSTRAINT submission_source_check;
ALTER TABLE submission ADD CONSTRAINT submission_source_check CHECK(source IN ('manual','ai','admin_grant'));
ALTER TABLE submission DROP CONSTRAINT submission_bonus_grant_source_check;
ALTER TABLE submission ADD CONSTRAINT submission_bonus_grant_source_check
 CHECK ((source='admin_grant') = (bonus_grant_batch_id IS NOT NULL));
DROP TABLE governance_comment;
DROP TABLE governance_event;
DROP TABLE governance_voter;
DROP TABLE governance_proposal;
DROP TABLE governance_member;
DROP TABLE class_governance;
