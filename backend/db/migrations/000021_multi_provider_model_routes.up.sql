-- Multiple OpenAI Chat Completions compatible providers. Business code chooses
-- a stable purpose; ops binds that purpose to a provider and model.

CREATE TABLE ai_provider (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL UNIQUE,
    base_url         TEXT NOT NULL,
    auth_type        TEXT NOT NULL DEFAULT 'bearer' CHECK (auth_type IN ('bearer')),
    api_key          TEXT NOT NULL DEFAULT '',
    timeout_seconds  INTEGER NOT NULL DEFAULT 90 CHECK (timeout_seconds BETWEEN 5 AND 600),
    max_retries      INTEGER NOT NULL DEFAULT 0 CHECK (max_retries BETWEEN 0 AND 5),
    capabilities     JSONB NOT NULL DEFAULT '{"json":true,"stream":true,"vision":false,"models":false}'::jsonb,
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    revision         BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ai_model_route (
    purpose          TEXT PRIMARY KEY CHECK (purpose IN (
        'material.vision','material.compose','knowledge.ocr','agent.text','agent.vision'
    )),
    provider_id      UUID NOT NULL REFERENCES ai_provider(id) ON DELETE RESTRICT,
    model            TEXT NOT NULL,
    parameters       JSONB NOT NULL DEFAULT '{}'::jsonb,
    revision         BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ai_model_route_provider ON ai_model_route(provider_id);

-- Preserve deterministic routing for jobs already accepted by the API. Keys
-- are deliberately absent from snapshots and are resolved at execution time.
ALTER TABLE ai_batch
    ADD COLUMN vision_route JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN text_route   JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE knowledge_blob
    ADD COLUMN ocr_route JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Migrate a database-backed legacy connection. Environment-only deployments
-- continue through the application fallback until Ops explicitly saves routes.
INSERT INTO ai_provider (
    id,name,base_url,api_key,timeout_seconds,max_retries,capabilities,enabled
)
SELECT '00000000-0000-0000-0000-000000000001'::uuid,
       'Legacy default',
       value->>'baseUrl',
       value->>'apiKey',
       90,
       0,
       '{"json":true,"stream":true,"vision":true,"models":false}'::jsonb,
       true
  FROM ops_config
 WHERE key='ai'
   AND COALESCE(value->>'baseUrl','') <> ''
   AND COALESCE(value->>'apiKey','') <> ''
ON CONFLICT (id) DO NOTHING;

INSERT INTO ai_model_route (purpose,provider_id,model)
SELECT purpose,'00000000-0000-0000-0000-000000000001'::uuid,model
  FROM (
    SELECT 'material.vision'::text AS purpose,value->>'visionModel' AS model FROM ops_config WHERE key='ai'
    UNION ALL
    SELECT 'knowledge.ocr',value->>'visionModel' FROM ops_config WHERE key='ai'
    UNION ALL
    SELECT 'agent.vision',value->>'visionModel' FROM ops_config WHERE key='ai'
    UNION ALL
    SELECT 'material.compose',value->>'textModel' FROM ops_config WHERE key='ai'
    UNION ALL
    SELECT 'agent.text',COALESCE(NULLIF(value->>'agentModel',''),value->>'textModel') FROM ops_config WHERE key='ai'
  ) routes
 WHERE COALESCE(model,'') <> ''
   AND EXISTS (SELECT 1 FROM ai_provider WHERE id='00000000-0000-0000-0000-000000000001'::uuid)
ON CONFLICT (purpose) DO NOTHING;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easygpa_ops') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON ai_provider, ai_model_route TO easygpa_ops;
    END IF;
END
$$;
