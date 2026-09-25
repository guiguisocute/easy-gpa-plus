-- The review SLA reminder reuses maintenance_run for its once-per-day
-- idempotency key, but the table's CHECK still only allowed the original
-- window_reminder job, so every tick aborted the whole window-task
-- transaction and rolled back the scorecard audit rounds generated with it.
ALTER TABLE maintenance_run DROP CONSTRAINT IF EXISTS maintenance_run_job_name_check;
ALTER TABLE maintenance_run ADD CONSTRAINT maintenance_run_job_name_check
    CHECK (job_name IN ('window_reminder','review_sla_overdue'));
