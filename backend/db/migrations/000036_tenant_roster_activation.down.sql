-- Roster activation is operational state and is intentionally not rewritten
-- during rollback. The previous direct class update path resumes after the
-- application is rolled back.

DROP FUNCTION IF EXISTS ops_update_tenant(BIGINT, TEXT, BOOLEAN, BOOLEAN);
