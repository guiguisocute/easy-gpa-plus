-- Class knowledge is a shared static resource. Visibility is no longer a
-- per-document setting: every existing and future document is class-visible.
UPDATE knowledge_document
   SET visibility='class',
       published_at=COALESCE(published_at,created_at),
       updated_at=now()
 WHERE deleted_at IS NULL
   AND (visibility<>'class' OR published_at IS NULL);

ALTER TABLE knowledge_document
    ALTER COLUMN visibility SET DEFAULT 'class';
