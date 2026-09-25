package api

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
)

func governanceLockdownExempt(c *gin.Context) (bool, error) {
	path := c.FullPath()
	if path == "/api/v1/governance/settle" {
		return true, nil
	}
	if path != "/api/v1/governance/proposals" && path != "/api/v1/governance/proposals/:id/vote" && path != "/api/v1/governance/proposals/:id/close" && path != "/api/v1/governance/proposals/:id/comments" && path != "/api/v1/governance/membership" {
		return false, nil
	}
	g, err := loadGovernance(c.Request.Context(), mustTx(c), mustActor(c).ClassID)
	if err != nil || g.Mode != "collective" {
		return false, err
	}
	if path == "/api/v1/governance/membership" {
		return true, nil
	}
	if path == "/api/v1/governance/proposals" {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1024*1024+1))
		if err != nil {
			return false, err
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		var in governanceProposalInput
		if len(raw) > 1024*1024 || json.Unmarshal(raw, &in) != nil {
			return false, nil
		}
		return (in.Action == "timeline" && in.Kind == "protected") || (in.Action == "export" && in.Kind == "ordinary"), nil
	}
	var allowed bool
	err = mustTx(c).QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM governance_proposal WHERE id=$1 AND action IN ('timeline','export'))`, c.Param("id")).Scan(&allowed)
	return allowed, err
}
