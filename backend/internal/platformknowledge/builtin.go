package platformknowledge

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed manifest.json content/*.md
var builtinFiles embed.FS

type ManifestEntry struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	File     string   `json:"file"`
	Roles    []string `json:"roles"`
	Views    []string `json:"views"`
	Keywords []string `json:"keywords"`
	Order    int      `json:"order"`
}

func Manifest() ([]ManifestEntry, error) {
	raw, err := builtinFiles.ReadFile("manifest.json")
	if err != nil {
		return nil, err
	}
	var entries []ManifestEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, item := range entries {
		if item.Key == "" || item.Title == "" || item.File == "" || seen[item.Key] || len(item.Roles) == 0 {
			return nil, errors.New("platform knowledge manifest is invalid")
		}
		seen[item.Key] = true
	}
	return entries, nil
}

func Sync(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("platform knowledge sync requires ops database")
	}
	manifest, err := Manifest()
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('easygpa.platform_knowledge.builtin'))`); err != nil {
		return err
	}
	active := make([]string, 0, len(manifest))
	for _, item := range manifest {
		content, err := builtinFiles.ReadFile(item.File)
		if err != nil {
			return fmt.Errorf("read builtin %s: %w", item.Key, err)
		}
		digest := sha256.Sum256(content)
		hash := hex.EncodeToString(digest[:])
		var id int64
		var previousHash *string
		lookupErr := tx.QueryRow(ctx, `
			SELECT id,content_hash FROM platform_knowledge_document WHERE builtin_key=$1
		`, item.Key).Scan(&id, &previousHash)
		if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
			return lookupErr
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO platform_knowledge_document
			    (source_kind,builtin_key,filename,display_name,allowed_roles,page_tags,keywords,sort_order,
			     status,enabled,searchable,extractor,entries_count,content_version,content_hash,published_at)
			VALUES ('builtin',$1,$2,$3,$4,$5,$6,$7,'ready',true,true,'builtin/markdown',1,$8,$9,now())
			ON CONFLICT (builtin_key) WHERE builtin_key IS NOT NULL DO UPDATE SET
			    filename=EXCLUDED.filename,display_name=EXCLUDED.display_name,page_tags=EXCLUDED.page_tags,
			    keywords=EXCLUDED.keywords,sort_order=EXCLUDED.sort_order,status='ready',searchable=true,
			    extractor='builtin/markdown',entries_count=1,content_version=EXCLUDED.content_version,
			    content_hash=EXCLUDED.content_hash,warning=NULL,error=NULL,deleted_at=NULL,updated_at=now()
			RETURNING id
		`, item.Key, strings.TrimPrefix(item.File, "content/"), item.Title, item.Roles, item.Views, item.Keywords, item.Order, hash[:12], hash).Scan(&id)
		if err != nil {
			return err
		}
		if previousHash == nil || *previousHash != hash {
			if _, err := tx.Exec(ctx, `DELETE FROM platform_knowledge_entry WHERE document_id=$1`, id); err != nil {
				return err
			}
			lines := 0
			if len(content) > 0 {
				lines = strings.Count(string(content), "\n") + 1
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO platform_knowledge_entry
				    (document_id,kind,locator,plain_text,line_count,char_count,metadata,content_hash)
				VALUES ($1,'text','{"startLine":1}'::jsonb,$2,$3,$4,'{"builtin":true}'::jsonb,$5)
			`, id, string(content), lines, utf8.RuneCount(content), hash); err != nil {
				return err
			}
		}
		active = append(active, item.Key)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE platform_knowledge_document SET enabled=false,status='retired',searchable=false,updated_at=now()
		 WHERE source_kind='builtin' AND NOT (builtin_key=ANY($1))
	`, active); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ReadBuiltin(ctx context.Context, tx pgx.Tx, documentID int64) (string, error) {
	var content string
	err := tx.QueryRow(ctx, `
		SELECT e.plain_text FROM platform_knowledge_document d
		JOIN platform_knowledge_entry e ON e.document_id=d.id
		WHERE d.id=$1 AND d.source_kind='builtin' AND d.deleted_at IS NULL ORDER BY e.id LIMIT 1
	`, documentID).Scan(&content)
	return content, err
}
