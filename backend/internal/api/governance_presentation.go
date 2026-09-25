package api

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"strings"
	"time"
)

func governancePresentation(c *gin.Context, p governanceProposal, eligible bool) (gin.H, error) {
	out := gin.H{"summary": "", "evidence": []any{}}
	var data struct {
		UploadID *jsonID        `json:"uploadId"`
		Rows     []gpaImportRow `json:"rows"`
		Kind     string         `json:"kind"`
	}
	if err := json.Unmarshal(p.Payload, &data); err != nil {
		return out, err
	}
	if data.UploadID != nil && (eligible || p.Author == mustActor(c).UserID) {
		var files []byte
		if err := mustTx(c).QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'name',filename,'mediaType',media_type,'sizeBytes',size_bytes,'sha256',sha256,'status',status,'uploadedAt',created_at) ORDER BY id),'[]') FROM evidence WHERE bonus_upload_id=$1 AND status='ready'`, int64(*data.UploadID)).Scan(&files); err != nil {
			return out, err
		}
		out["evidence"] = json.RawMessage(files)
	}
	switch p.Action {
	case "bonus":
		var in bonusGrantInput
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return out, err
		}
		ids := make([]int64, len(in.StudentIDs))
		for i, id := range in.StudentIDs {
			ids[i] = int64(id)
		}
		var names string
		if err := mustTx(c).QueryRow(c.Request.Context(), `SELECT COALESCE(string_agg(name,'、' ORDER BY sid),'') FROM app_user WHERE id=ANY($1::bigint[])`, ids).Scan(&names); err != nil {
			return out, err
		}
		summary := fmt.Sprintf("受益成员（%d 人）：%s\n评分方案：%s", len(ids), names, in.SchemeVersion)
		current, err := loadCurrentScheme(c.Request.Context(), mustTx(c))
		if err != nil {
			return out, err
		}
		prepared, err := prepareBonusGrant(current, in, time.Now())
		if err == nil && prepared.Requested != nil {
			summary += fmt.Sprintf("\n小项：%s · 每人 %.3f 分", prepared.Item.Name, *prepared.Requested)
		}
		out["summary"] = summary
	case "gpa":
		out["summary"] = fmt.Sprintf("依据正式原件核验全班 %d 人专业素质分；不以投票偏好修改成绩。", len(data.Rows))
		if eligible || p.Author == mustActor(c).UserID {
			var lines []string
			for _, row := range data.Rows {
				lines = append(lines, fmt.Sprintf("%s %s · %.3f", row.SID, row.Name, row.Score))
			}
			out["summary"] = out["summary"].(string) + "\n" + strings.Join(lines, "\n")
		}
	case "timeline":
		var in struct {
			Open     time.Time  `json:"open"`
			Close    time.Time  `json:"close"`
			Lockdown *time.Time `json:"lockdown"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return out, err
		}
		out["summary"] = fmt.Sprintf("拟开放：%s\n拟截止：%s\n封锁时间：%v", in.Open.Format(time.RFC3339), in.Close.Format(time.RFC3339), in.Lockdown)
	case "export":
		out["summary"] = "导出类型：" + map[string]string{"summary": "全班汇总", "detail": "逐人明细", "archive": "佐证归档", "college": "学院报表"}[data.Kind] + "。仅提案发起人可领取本次授权文件。"
	}
	return out, nil
}
func (s *Server) governanceExport(c *gin.Context) {
	var allowed bool
	if err := mustTx(c).QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM governance_proposal WHERE action='export' AND status='applied' AND author_id=$1 AND result->>'jobId'=$2)`, mustActor(c).UserID, c.Param("job")).Scan(&allowed); err != nil {
		writeServiceError(c, err)
		return
	}
	if !allowed {
		writeError(c, 403, "export_grant_required", "没有授予你这个导出文件的领取权限", nil)
		return
	}
	s.exportJob(c)
}
