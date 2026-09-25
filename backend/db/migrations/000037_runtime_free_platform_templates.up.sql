-- Platform templates are reusable scoring rules, not snapshots of one
-- class's academic-year runtime. Existing rows were imported as full schemes;
-- remove the class-specific envelope while retaining the scoring tree.
UPDATE platform_template
   SET config = config - ARRAY['version', 'window', 'capabilities', 'honorRoll']::text[];

-- Pending/reviewed share snapshots follow the same canonical shape so an
-- approval cannot reintroduce runtime fields into the template library.
UPDATE template_share_request
   SET config = config - ARRAY['version', 'window', 'capabilities', 'honorRoll']::text[];

COMMENT ON COLUMN platform_template.config IS
    'Reusable scoring template: schemeName, weights and categories only; class runtime is applied when copied.';
