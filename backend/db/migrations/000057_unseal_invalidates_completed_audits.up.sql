-- Older unseal requests left completed rounds reusable. A round created
-- before a later unseal cannot attest to the student's resealed materials.
-- Preserve all snapshots, assignments and confirmations as historical facts.
WITH stale AS (
    UPDATE scorecard_audit_batch b
       SET status='stale',stale_at=now(),stale_reason='修复解封后遗留终审',
           invalidated_at=now(),invalidated_reason='修复解封后遗留终审'
     WHERE b.student_id IS NOT NULL
       AND b.status IN ('generating','blocked','open','resolving','complete')
       AND EXISTS (
           SELECT 1 FROM seal s
            WHERE s.class_id=b.class_id AND s.student_id=b.student_id
              AND s.unsealed_at>b.created_at
       )
     RETURNING b.class_id,b.id,b.student_id
), logged AS (
    INSERT INTO audit_log (class_id,actor_role,action,resource_type,resource_id,metadata)
    SELECT class_id,'system','scorecard_audit.stale','scorecard_audit_batch',id::text,
           jsonb_build_object('studentId',student_id,'reason','修复解封后遗留终审','migration',57)
      FROM stale
), notified AS (
    INSERT INTO outbox_event (class_id,type,payload)
    SELECT class_id,'scorecard_audit.stale',
           jsonb_build_object('batchId',id::text,'studentId',student_id,'reason','修复解封后遗留终审')
      FROM stale
)
INSERT INTO settlement_invalidation (class_id,run_id,reason)
SELECT affected.class_id,latest.id,'修复解封后遗留终审，须重新终审并确认结果'
  FROM (SELECT DISTINCT class_id FROM stale) affected
  CROSS JOIN LATERAL (
      SELECT r.id FROM settlement_run r
       WHERE r.class_id=affected.class_id AND r.status='complete'
       ORDER BY r.created_at DESC,r.id DESC LIMIT 1
  ) latest
 WHERE NOT EXISTS (
     SELECT 1 FROM settlement_invalidation i
      WHERE i.class_id=affected.class_id AND i.run_id=latest.id
 );
