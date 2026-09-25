package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Class knowledge is shared with the whole class as soon as upload completes.
// Content extraction is optional and never controls raw-file availability.
func (s *Server) classResources(c *gin.Context) {
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT d.id,d.filename,d.logical_path,d.display_name,b.media_type,b.size_bytes,d.updated_at,COALESCE(d.published_at,d.created_at)
		  FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id
		 WHERE d.deleted_at IS NULL AND b.deleted_at IS NULL
		   AND d.status<>'uploading'
		 ORDER BY d.updated_at DESC,d.id DESC
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, size int64
		var filename, logicalPath, displayName, mediaType string
		var updated, published time.Time
		if err := rows.Scan(&id, &filename, &logicalPath, &displayName, &mediaType, &size, &updated, &published); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{
			"id": strconv.FormatInt(id, 10), "filename": filename, "logicalPath": logicalPath,
			"displayName": displayName, "mediaType": mediaType, "sizeBytes": size,
			"updatedAt": updated, "publishedAt": published,
		})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "class_resource.list", "knowledge_document", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) classResourceURL(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var objectKey, filename string
	err := tx.QueryRow(c.Request.Context(), `
		SELECT b.object_key,d.filename
		  FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id
		 WHERE d.id=$1 AND d.deleted_at IS NULL AND b.deleted_at IS NULL
		   AND d.status<>'uploading'
	`, id).Scan(&objectKey, &filename)
	if notFound(c, err, "班级资料") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	link, err := s.deps.Objects.PresignGet(c.Request.Context(), objectKey, 10*time.Minute, filename, false)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "class_resource.url_issued", "knowledge_document", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": link.String(), "filename": filename, "expiresIn": 600})
}
