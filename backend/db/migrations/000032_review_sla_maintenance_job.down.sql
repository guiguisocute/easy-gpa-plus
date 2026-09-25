-- Drop the SLA bookkeeping rows first: the narrower CHECK cannot be restored
-- while rows carrying the newer job name are still present.
DELETE FROM maintenance_run WHERE job_name = 'review_sla_overdue';
ALTER TABLE maintenance_run DROP CONSTRAINT IF EXISTS maintenance_run_job_name_check;
ALTER TABLE maintenance_run ADD CONSTRAINT maintenance_run_job_name_check
    CHECK (job_name IN ('window_reminder'));
