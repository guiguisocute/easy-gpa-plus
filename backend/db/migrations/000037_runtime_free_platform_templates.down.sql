-- Recreate a valid legacy full-scheme envelope for application rollback.
-- The original class-specific values were intentionally discarded because
-- they never belonged to a platform template; conservative defaults are used.
UPDATE platform_template
   SET config = jsonb_build_object(
       'version', 'template',
       'window', jsonb_build_object('open', '2000-01-01T00:00:00Z', 'close', '9999-12-31T23:59:59Z'),
       'capabilities', jsonb_build_object(
           'submit', true, 'edit', true, 'appeal', true, 'review', true, 'arbitrate', true,
           'gpa', jsonb_build_object('on', false, 'close', '9999-12-31T23:59:59Z'),
           'export', jsonb_build_object('on', false, 'gate', 'settlement')
       ),
       'honorRoll', jsonb_build_object('topPercent', 30)
   ) || config;

UPDATE template_share_request
   SET config = jsonb_build_object(
       'version', 'template',
       'window', jsonb_build_object('open', '2000-01-01T00:00:00Z', 'close', '9999-12-31T23:59:59Z'),
       'capabilities', jsonb_build_object(
           'submit', true, 'edit', true, 'appeal', true, 'review', true, 'arbitrate', true,
           'gpa', jsonb_build_object('on', false, 'close', '9999-12-31T23:59:59Z'),
           'export', jsonb_build_object('on', false, 'gate', 'settlement')
       ),
       'honorRoll', jsonb_build_object('topPercent', 30)
   ) || config;

COMMENT ON COLUMN platform_template.config IS NULL;
