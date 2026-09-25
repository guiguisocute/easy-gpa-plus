-- Align the two-stage appeal implementation with the frontend contract and
-- give reviewer objections their own draft/submission/decision lifecycle.

ALTER TABLE appeal DROP CONSTRAINT IF EXISTS appeal_status_check;
UPDATE appeal SET status='reviewing' WHERE status='assigned';
ALTER TABLE appeal ADD CONSTRAINT appeal_status_check
    CHECK (status IN ('filed', 'reviewing', 'resolved', 'escalated', 'final'));

ALTER TABLE appeal
    ADD COLUMN previous_appeal_id BIGINT,
    ADD CONSTRAINT appeal_previous_round_fk
        FOREIGN KEY (class_id, previous_appeal_id) REFERENCES appeal(class_id, id);

ALTER TABLE appeal_reviewer
    DROP CONSTRAINT IF EXISTS appeal_reviewer_check,
    ADD COLUMN decision TEXT,
    ADD CONSTRAINT appeal_reviewer_decision_check CHECK (
        (decision IS NULL AND score IS NULL AND reason IS NULL AND spent_seconds IS NULL AND decided_at IS NULL)
        OR
        (decision IN ('uphold','adjust') AND score IS NOT NULL AND reason IS NOT NULL AND spent_seconds IS NOT NULL AND decided_at IS NOT NULL)
    );

DROP INDEX IF EXISTS appeal_one_active_reviewer_objection;

CREATE TABLE objection (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        BIGINT NOT NULL REFERENCES class(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('base', 'penalty', 'submission')),
    student_id      BIGINT NOT NULL,
    proposer_id     BIGINT NOT NULL,
    scheme_id       BIGINT NOT NULL,
    category_key    TEXT NOT NULL,
    item_key        TEXT NOT NULL,
    target_id       BIGINT,
    current_score   NUMERIC(10,3),
    proposed_score  NUMERIC(10,3) NOT NULL,
    quantity        NUMERIC(10,3),
    basis           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft','submitted','applied','adjusted','dismissed','withdrawn')),
    batch_id        UUID,
    decided_by      BIGINT,
    decided_score   NUMERIC(10,3),
    decision_reason TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at    TIMESTAMPTZ,
    decided_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (class_id, id),
    FOREIGN KEY (class_id, student_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, proposer_id) REFERENCES app_user(class_id, id),
    FOREIGN KEY (class_id, scheme_id) REFERENCES scheme(class_id, id),
    FOREIGN KEY (class_id, decided_by) REFERENCES app_user(class_id, id),
    CHECK (proposer_id <> student_id),
    CHECK ((status IN ('draft','submitted','withdrawn') AND decided_by IS NULL AND decided_score IS NULL AND decision_reason IS NULL AND decided_at IS NULL)
        OR (status IN ('applied','adjusted','dismissed') AND decided_by IS NOT NULL AND decision_reason IS NOT NULL AND decided_at IS NOT NULL)),
    CHECK ((kind='penalty' AND quantity IS NOT NULL) OR (kind<>'penalty' AND quantity IS NULL))
);

CREATE INDEX objection_by_proposer ON objection (class_id, proposer_id, updated_at DESC, id DESC);
CREATE INDEX objection_admin_queue ON objection (class_id, status, submitted_at, id)
    WHERE status='submitted';
CREATE INDEX objection_target ON objection (class_id, kind, target_id);
CREATE UNIQUE INDEX objection_one_open_target
    ON objection (class_id, kind, student_id, scheme_id, category_key, item_key, COALESCE(target_id, 0))
    WHERE status IN ('draft','submitted');

ALTER TABLE objection ENABLE ROW LEVEL SECURITY;
ALTER TABLE objection FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON objection
    USING (class_id = app_current_class_id())
    WITH CHECK (class_id = app_current_class_id());

-- Preserve objections created by the short-lived pre-contract implementation.
INSERT INTO objection
    (class_id,kind,student_id,proposer_id,scheme_id,category_key,item_key,target_id,
     current_score,proposed_score,basis,status,decided_by,decided_score,decision_reason,
     created_at,submitted_at,decided_at,updated_at)
SELECT a.class_id,
       CASE a.target_type WHEN 'submission' THEN 'submission'
            WHEN 'base_score' THEN 'base' ELSE 'penalty' END,
       a.student_id,a.filed_by,
       COALESCE(s.scheme_id,b.scheme_id),
       COALESCE(s.category_key,b.category_key),
       COALESCE(s.item_key,b.item_key),a.target_id,
       a.original_score,a.proposed_score,COALESCE(a.proposal_reason,a.reason),
       CASE WHEN a.status='final' AND a.resolution_score IS NOT DISTINCT FROM a.proposed_score THEN 'applied'
            WHEN a.status='final' THEN 'adjusted' ELSE 'submitted' END,
       CASE WHEN a.status='final' THEN COALESCE(a.handler_id,a.filed_by) END,
       CASE WHEN a.status='final' THEN a.resolution_score END,
       CASE WHEN a.status='final' THEN COALESCE(a.resolution_reason,'历史终裁') END,
       a.created_at,a.created_at,a.resolved_at,a.updated_at
  FROM appeal a
  LEFT JOIN submission s ON a.target_type='submission' AND s.id=a.target_id
  LEFT JOIN base_score b ON a.target_type IN ('base_score','penalty_score') AND b.id=a.target_id
 WHERE a.kind='reviewer_objection'
   AND COALESCE(s.scheme_id,b.scheme_id) IS NOT NULL
ON CONFLICT DO NOTHING;

-- The data now lives in objection. Keep the short-lived appeal rows as an
-- immutable historical trace, but never leave them blocking settlement.
UPDATE appeal
   SET status='final',resolved_at=COALESCE(resolved_at,now()),updated_at=now(),
       resolution_reason=COALESCE(resolution_reason,'已迁移至扣分与异议终裁台')
 WHERE kind='reviewer_objection' AND status<>'final';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON objection TO easygpa_app;
        GRANT USAGE, SELECT ON SEQUENCE objection_id_seq TO easygpa_app;
    END IF;
END
$$;
