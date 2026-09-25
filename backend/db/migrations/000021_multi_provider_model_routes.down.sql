ALTER TABLE knowledge_blob DROP COLUMN IF EXISTS ocr_route;
ALTER TABLE ai_batch DROP COLUMN IF EXISTS text_route;
ALTER TABLE ai_batch DROP COLUMN IF EXISTS vision_route;
DROP TABLE IF EXISTS ai_model_route;
DROP TABLE IF EXISTS ai_provider;
