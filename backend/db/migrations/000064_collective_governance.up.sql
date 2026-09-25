-- Opt-in participation is separate from registration, material submission and
-- the immutable class roster. Existing classes retain centralized operation.
CREATE TABLE class_governance (
 class_id BIGINT PRIMARY KEY REFERENCES class(id),
 mode TEXT NOT NULL DEFAULT 'centralized' CHECK(mode IN ('centralized','enrolling','collective')),
 version BIGINT NOT NULL DEFAULT 1,
 enrollment_open_at TIMESTAMPTZ,
 enrollment_close_at TIMESTAMPTZ,
 profile TEXT CHECK(profile IN ('compact','standard')),
 activated_at TIMESTAMPTZ,
 created_by BIGINT NOT NULL,
 FOREIGN KEY(class_id,created_by) REFERENCES app_user(class_id,id)
);
CREATE TABLE governance_member (
 class_id BIGINT NOT NULL REFERENCES class(id),
 user_id BIGINT NOT NULL,
 joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 left_at TIMESTAMPTZ,
 reviewer BOOLEAN NOT NULL DEFAULT false,
 PRIMARY KEY(class_id,user_id),
 FOREIGN KEY(class_id,user_id) REFERENCES app_user(class_id,id)
);
CREATE TABLE governance_proposal (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 class_id BIGINT NOT NULL REFERENCES class(id),
 author_id BIGINT NOT NULL,
 request_id UUID NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('activate','ordinary','protected','bonus','review','appeal')),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 120),
 body TEXT NOT NULL CHECK(length(body) BETWEEN 4 AND 10000),
 action TEXT NOT NULL,
 payload JSONB NOT NULL DEFAULT '{}',
 target_id BIGINT,
 subject_id BIGINT,
 parent_id BIGINT,
 governance_version BIGINT NOT NULL,
 state_hash TEXT NOT NULL,
 roster_hash TEXT NOT NULL,
 roster_count INTEGER NOT NULL,
 electorate_count INTEGER NOT NULL,
 required_yes INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'discussion' CHECK(status IN ('discussion','voting','passed','rejected','applied','blocked','stale','deliberating')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 opens_at TIMESTAMPTZ NOT NULL,
 closes_at TIMESTAMPTZ NOT NULL,
 decided_at TIMESTAMPTZ,
 applied_at TIMESTAMPTZ,
 result JSONB,
 review_round INTEGER NOT NULL DEFAULT 1,
 next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(class_id,id), UNIQUE(class_id,author_id,request_id),
 FOREIGN KEY(class_id,author_id) REFERENCES app_user(class_id,id),
 FOREIGN KEY(class_id,subject_id) REFERENCES app_user(class_id,id),
 FOREIGN KEY(class_id,parent_id) REFERENCES governance_proposal(class_id,id),
 CHECK(closes_at>opens_at),CHECK(required_yes>0),CHECK(electorate_count>0)
);
CREATE UNIQUE INDEX governance_open_case ON governance_proposal(class_id,action,target_id)
 WHERE kind IN ('review','appeal') AND status IN ('discussion','voting','deliberating','passed','blocked');
CREATE TABLE governance_voter (
 class_id BIGINT NOT NULL,
 proposal_id BIGINT NOT NULL,
 user_id BIGINT NOT NULL,
 choice TEXT CHECK(choice IN ('yes','no','abstain')),
 opinion JSONB,
 candidate TEXT,
 reason TEXT,
 cast_at TIMESTAMPTZ,
 locked BOOLEAN NOT NULL DEFAULT false,
 active BOOLEAN NOT NULL DEFAULT true,
 replacement_count INTEGER NOT NULL DEFAULT 0,
 assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(proposal_id,user_id),
 FOREIGN KEY(class_id,proposal_id) REFERENCES governance_proposal(class_id,id),
 FOREIGN KEY(class_id,user_id) REFERENCES app_user(class_id,id)
);
CREATE TABLE governance_event (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 class_id BIGINT NOT NULL REFERENCES class(id),
 proposal_id BIGINT,
 actor_id BIGINT,
 action TEXT NOT NULL,
 detail JSONB NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(class_id,proposal_id) REFERENCES governance_proposal(class_id,id),
 FOREIGN KEY(class_id,actor_id) REFERENCES app_user(class_id,id)
);
CREATE INDEX governance_pending ON governance_proposal(class_id,status,closes_at);
ALTER TABLE submission DROP CONSTRAINT submission_source_check;
ALTER TABLE submission ADD CONSTRAINT submission_source_check CHECK(source IN ('manual','ai','admin_grant','collective_grant'));
ALTER TABLE submission DROP CONSTRAINT submission_bonus_grant_source_check;
ALTER TABLE submission ADD CONSTRAINT submission_bonus_grant_source_check
 CHECK ((source IN ('admin_grant','collective_grant')) = (bonus_grant_batch_id IS NOT NULL));
CREATE TABLE governance_comment (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 class_id BIGINT NOT NULL REFERENCES class(id),
 proposal_id BIGINT NOT NULL,
 actor_id BIGINT NOT NULL,
 body TEXT NOT NULL CHECK(length(body) BETWEEN 4 AND 5000),
 kind TEXT NOT NULL DEFAULT 'discussion' CHECK(kind IN ('discussion','evidence')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(class_id,proposal_id) REFERENCES governance_proposal(class_id,id),
 FOREIGN KEY(class_id,actor_id) REFERENCES app_user(class_id,id)
);
CREATE INDEX governance_seats ON governance_voter(class_id,user_id,active);
DO $$ DECLARE tab TEXT; BEGIN
 FOREACH tab IN ARRAY ARRAY['class_governance','governance_member','governance_proposal','governance_voter','governance_event','governance_comment'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',tab);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',tab);
  EXECUTE format('CREATE POLICY tenant_isolation ON %I USING(class_id=app_current_class_id()) WITH CHECK(class_id=app_current_class_id())',tab);
  IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
   EXECUTE format('GRANT SELECT,INSERT,UPDATE ON %I TO easygpa_app',tab);
  END IF;
 END LOOP;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
  GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO easygpa_app;
 END IF;
END $$;

-- The operations connection discovers due IDs without gaining general access
-- to private opinions. Execution still uses the application tenant transaction.
CREATE INDEX governance_due ON governance_proposal(next_check_at,id)
 WHERE status IN ('discussion','voting','deliberating','passed','blocked');
CREATE FUNCTION governance_due_proposals()
RETURNS TABLE(id BIGINT,class_id BIGINT,author_id BIGINT)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public,pg_temp AS $$
 SELECT p.id,p.class_id,p.author_id FROM governance_proposal p
 JOIN class c ON c.id=p.class_id
 WHERE NOT c.archived AND p.next_check_at<=now()
 AND ((p.status IN ('discussion','voting','deliberating') AND p.closes_at<=now())
   OR (p.status='passed' AND p.decided_at<=now()-interval '24 hours')
   OR (p.status='blocked' AND p.result IS NULL))
 ORDER BY p.next_check_at,p.id LIMIT 25
$$;
REVOKE ALL ON FUNCTION governance_due_proposals() FROM PUBLIC;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='easygpa_ops') THEN
  GRANT EXECUTE ON FUNCTION governance_due_proposals() TO easygpa_ops;
 END IF;
END $$;
