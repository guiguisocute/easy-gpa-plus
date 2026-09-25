package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/exportjob"
)

func TestCollegeExportCacheRejectsPriorScoreFormat(t *testing.T) {
	admin := settlementAdminPool(t)
	fx := seedSettlementClass(t, admin, true)
	app := settlementAppPool(t, os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL"))
	run := persistForcedSettlement(t, app, fx)
	oldKey := fmt.Sprintf("class-%d/exports/old-college.zip", fx.ClassID)
	var oldID string
	if err := admin.QueryRow(t.Context(), `
		INSERT INTO export_job(class_id,run_id,kind,status,requested_by,object_key,expires_at)
		VALUES($1,$2,'college','complete',$3,$4,now()+interval '1 day') RETURNING id::text
	`, fx.ClassID, run.RunID, fx.AdminID, oldKey).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		JobID        string `json:"jobId"`
		Status       string `json:"status"`
		Deduplicated bool   `json:"deduplicated"`
	}
	request := func() result {
		t.Helper()
		response := serveClassHTTP(t, app, settlementActor(fx), func(router *gin.Engine) {
			router.POST("/export", (&Server{}).requestExport)
		}, http.MethodPost, "/export", `{"kind":"college"}`)
		if response.Code != http.StatusAccepted {
			t.Fatalf("export status=%d body=%s", response.Code, response.Body.String())
		}
		var body result
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	fresh := request()
	if fresh.JobID == "" || fresh.JobID == oldID || fresh.Deduplicated || fresh.Status != "queued" {
		t.Fatalf("old score format reused: %#v", fresh)
	}
	if queued := request(); queued.JobID != fresh.JobID || !queued.Deduplicated {
		t.Fatalf("queued report duplicated: %#v", queued)
	}
	newKey := fmt.Sprintf("class-%d/exports/%s%s", fx.ClassID, fresh.JobID, exportjob.CollegeArchiveSuffix)
	if _, err := admin.Exec(t.Context(), `
		UPDATE export_job SET status='complete',object_key=$2,expires_at=now()+interval '1 day' WHERE id=$1::uuid
	`, fresh.JobID, newKey); err != nil {
		t.Fatal(err)
	}
	if ready := request(); ready.JobID != fresh.JobID || !ready.Deduplicated || ready.Status != "complete" {
		t.Fatalf("current report not reused: %#v", ready)
	}
	if n := countClassRows(t, admin, `SELECT count(*) FROM export_job WHERE id=$1::uuid AND status='complete' AND object_key=$2`, oldID, oldKey); n != 1 {
		t.Fatal("historical report record changed")
	}
	assertClassTrail(t, admin, fx.ClassID, "export.requested", "export.requested", fresh.JobID, 1)
}
