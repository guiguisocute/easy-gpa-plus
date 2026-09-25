-- 举报的佐证附件。
--
-- 举报人本来只能写一段话，复核人也就只有那段话可依据。允许附文件之后，
-- "宿管的登记表""通报原件的照片"这类东西能直接跟着举报走，判起来才有据。
--
-- 挂在 evidence 上而不是新开一张表：配额、大小校验、内容嗅探、对象存储回收
-- 都长在这张表上，另起炉灶要把这四样各抄一遍，抄漏一样就是一个洞。
-- 000013 给 objection 加列时走的也是这条路。
--
-- 唯一要动的老约束是 created_by。举报是匿名的：报了什么可以留档，谁报的不能。
-- 所以举报附件这一类**不记上传人**，并且把这件事写成约束——
--   report_id IS NULL  ⇔  created_by IS NOT NULL
-- 反过来读就是：除了举报附件，其余每一份都必须记上传人；举报附件一律不许记。
-- 这样即使以后有人顺手在插入时带上 actor.UserID，数据库会直接拒绝，
-- 而不是悄悄把举报人写进去。

ALTER TABLE evidence ADD COLUMN report_id BIGINT;

ALTER TABLE evidence
    ADD CONSTRAINT evidence_class_id_report_id_fkey
        FOREIGN KEY (class_id, report_id) REFERENCES report(class_id, id) ON DELETE CASCADE;

ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_one_owner_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_one_owner_check
    CHECK ((submission_id IS NOT NULL)::integer
         + (appeal_id IS NOT NULL)::integer
         + (objection_id IS NOT NULL)::integer
         + (report_id IS NOT NULL)::integer = 1);

-- 举报附件只走 note 这一类：claim 是学生给自己申报材料用的，语义对不上。
ALTER TABLE evidence ADD CONSTRAINT evidence_report_note_only_check
    CHECK (report_id IS NULL OR kind = 'note');

ALTER TABLE evidence ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE evidence ADD CONSTRAINT evidence_anonymous_only_for_reports_check
    CHECK ((report_id IS NULL) = (created_by IS NOT NULL));

CREATE INDEX evidence_by_report
    ON evidence (class_id, report_id) WHERE report_id IS NOT NULL;

-- 「这份附件是不是挂在我提的举报上」。和 000041 的三个函数同一个形状：
-- 必须先给出举报人才回答得了，反过来查不出某份附件是谁传的。
CREATE FUNCTION report_evidence_is_mine(p_class_id BIGINT, p_reporter_id BIGINT, p_report_id BIGINT)
RETURNS BOOLEAN
LANGUAGE sql SECURITY DEFINER STABLE
SET search_path = public
AS $$
    SELECT EXISTS (
        SELECT 1 FROM report_reporter
         WHERE class_id=p_class_id AND reporter_id=p_reporter_id AND report_id=p_report_id
    )
$$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='easygpa_app') THEN
        GRANT EXECUTE ON FUNCTION report_evidence_is_mine(BIGINT,BIGINT,BIGINT) TO easygpa_app;
    END IF;
END
$$;
