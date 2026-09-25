-- Only an authenticated user's explicit preference can select this mode.
-- Keep all existing preferences, opt-outs and platform activation unchanged.
ALTER TABLE mail_notification DROP CONSTRAINT mail_notification_mode_check;
ALTER TABLE mail_notification ADD CONSTRAINT mail_notification_mode_check
    CHECK (mode IN ('digest','immediate','frequent'));
