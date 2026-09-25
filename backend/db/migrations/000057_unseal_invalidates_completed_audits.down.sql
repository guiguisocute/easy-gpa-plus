-- Data-only correctness repair. Rolling back code must not resurrect an
-- obsolete review/confirmation or erase its audit trail. No schema to undo.
SELECT 1;
