DROP TRIGGER IF EXISTS app_user_clear_ineligible_deputy ON app_user;
DROP FUNCTION IF EXISTS clear_ineligible_class_deputy();
DROP INDEX IF EXISTS app_user_one_deputy_per_class;
ALTER TABLE app_user DROP CONSTRAINT IF EXISTS app_user_deputy_eligible;
ALTER TABLE app_user DROP COLUMN IF EXISTS is_deputy;
