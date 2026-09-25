-- 回退举报佐证。附件行随列一起删：它们的 owner 只有 report_id 一个，
-- 列没了就成了无主行，而 created_by 又是空的，回填不出一个上传人来。

DROP FUNCTION IF EXISTS report_evidence_is_mine(BIGINT,BIGINT,BIGINT);

DELETE FROM evidence WHERE report_id IS NOT NULL;

ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_anonymous_only_for_reports_check;
ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_report_note_only_check;
ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_class_id_report_id_fkey;

DROP INDEX IF EXISTS evidence_by_report;

ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_one_owner_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_one_owner_check
    CHECK ((submission_id IS NOT NULL)::integer
         + (appeal_id IS NOT NULL)::integer
         + (objection_id IS NOT NULL)::integer = 1);

ALTER TABLE evidence DROP COLUMN IF EXISTS report_id;
ALTER TABLE evidence ALTER COLUMN created_by SET NOT NULL;
