DROP TRIGGER prevent_force_rejected_report ON report;
DROP TRIGGER prevent_force_rejected_classification ON classification_suggestion;
DROP TRIGGER prevent_force_rejected_objection ON objection;
DROP TRIGGER prevent_force_rejected_appeal ON appeal;
DROP FUNCTION prevent_force_rejected_workflow();
DROP TRIGGER protect_submission_force_rejection ON submission;
DROP FUNCTION protect_submission_force_rejection();
ALTER TABLE submission DROP CONSTRAINT submission_force_rejection_score_check;
ALTER TABLE submission DROP COLUMN force_rejection;
