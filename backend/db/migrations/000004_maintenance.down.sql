DROP TABLE IF EXISTS maintenance_run;

UPDATE export_job
   SET status='failed',
       error_message=COALESCE(error_message,'export expired before migration rollback')
 WHERE status='expired';

ALTER TABLE export_job DROP CONSTRAINT export_job_status_check;
ALTER TABLE export_job
    ADD CONSTRAINT export_job_status_check
    CHECK (status IN ('queued', 'running', 'complete', 'failed'));
