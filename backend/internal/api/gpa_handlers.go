package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

const maxGPAUploadBytes = 10 << 20

type gpaImportRow struct {
	SID    string  `json:"sid"`
	Name   string  `json:"name,omitempty"`
	Score  float64 `json:"score"`
	Source int     `json:"-"`
}

func parseGPATable(table [][]string) ([]gpaImportRow, error) {
	rows := make([]gpaImportRow, 0, len(table))
	seen := make(map[string]bool)
	for index, columns := range table {
		for len(columns) > 0 && strings.TrimSpace(columns[len(columns)-1]) == "" {
			columns = columns[:len(columns)-1]
		}
		if len(columns) == 0 {
			continue
		}
		columns[0] = strings.TrimPrefix(strings.TrimSpace(columns[0]), "\ufeff")
		if index == 0 && isGPAHeader(columns) {
			continue
		}
		if len(columns) != 2 && len(columns) != 3 {
			return nil, fmt.Errorf("第 %d 行应为 学号,均分 或 学号,姓名,均分", index+1)
		}
		row := gpaImportRow{SID: strings.TrimSpace(columns[0]), Source: index + 1}
		scoreText := strings.TrimSpace(columns[len(columns)-1])
		if len(columns) == 3 {
			row.Name = strings.TrimSpace(columns[1])
		}
		if row.SID == "" {
			return nil, fmt.Errorf("第 %d 行学号为空", index+1)
		}
		if seen[row.SID] {
			return nil, fmt.Errorf("第 %d 行学号 %s 重复", index+1, row.SID)
		}
		seen[row.SID] = true
		score, err := strconv.ParseFloat(scoreText, 64)
		if err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 100 {
			return nil, fmt.Errorf("第 %d 行均分必须在 0—100 之间", index+1)
		}
		row.Score = math.Round(score*1000) / 1000
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("没有可导入的数据行")
	}
	if len(rows) > 5000 {
		return nil, errors.New("一次最多导入 5000 行")
	}
	return rows, nil
}

func isGPAHeader(columns []string) bool {
	first := strings.ToLower(strings.TrimSpace(columns[0]))
	last := strings.ToLower(strings.TrimSpace(columns[len(columns)-1]))
	return (strings.Contains(first, "学号") || first == "sid" || first == "student_id") &&
		(strings.Contains(last, "均分") || last == "score" || last == "gpa")
}

func parseCSVRows(reader io.Reader) ([][]string, error) {
	parser := csv.NewReader(reader)
	parser.FieldsPerRecord = -1
	parser.TrimLeadingSpace = true
	return parser.ReadAll()
}

func (s *Server) importGPA(c *gin.Context, imported []gpaImportRow, source string) {
	tx := mustTx(c)
	actor := mustActor(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// GPA import is always available to class admins, independently of the
	// material window. The shared lockdown middleware still enforces read-only.
	type userRow struct {
		ID   int64
		SID  string
		Name string
	}
	users := make(map[string]userRow)
	rows, err := tx.Query(c.Request.Context(), `SELECT id,sid,name FROM app_user WHERE status='active' ORDER BY sid`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for rows.Next() {
		var user userRow
		if err := rows.Scan(&user.ID, &user.SID, &user.Name); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		users[user.SID] = user
	}
	rows.Close()
	unknown := make([]string, 0)
	mismatched := make([]gin.H, 0)
	for _, row := range imported {
		user, ok := users[row.SID]
		if !ok {
			unknown = append(unknown, row.SID)
			continue
		}
		if row.Name != "" && row.Name != user.Name {
			mismatched = append(mismatched, gin.H{"sid": row.SID, "expected": user.Name, "actual": row.Name, "row": row.Source})
		}
	}
	if len(unknown) > 0 || len(mismatched) > 0 {
		writeError(c, http.StatusUnprocessableEntity, "gpa_roster_mismatch", "导入名单与本班账号不一致", gin.H{"unknownSids": unknown, "nameMismatches": mismatched})
		return
	}
	var previous int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM student_gpa WHERE scheme_id=$1`, current.ID).Scan(&previous); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM student_gpa WHERE scheme_id=$1`, current.ID); err != nil {
		writeServiceError(c, err)
		return
	}
	var batch string
	if err := tx.QueryRow(c.Request.Context(), `SELECT gen_random_uuid()::text`).Scan(&batch); err != nil {
		writeServiceError(c, err)
		return
	}
	for _, row := range imported {
		user := users[row.SID]
		details, _ := json.Marshal(map[string]any{"source": source, "row": row.Source, "providedName": row.Name})
		if _, err := tx.Exec(c.Request.Context(), `
			INSERT INTO student_gpa (class_id,student_id,scheme_id,score,details,import_batch,imported_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, actor.ClassID, user.ID, current.ID, row.Score, details, batch, actor.UserID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	missing := make([]gin.H, 0)
	importedSet := make(map[string]bool, len(imported))
	for _, row := range imported {
		importedSet[row.SID] = true
	}
	for _, user := range users {
		if !importedSet[user.SID] {
			missing = append(missing, gin.H{"sid": user.SID, "name": user.Name})
		}
	}
	if err := appendAudit(c, tx, "gpa.imported", "student_gpa", batch, map[string]any{"count": previous}, map[string]any{"count": len(imported), "batch": batch}, map[string]any{"source": source, "missing": len(missing)}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateLatestSettlement(c.Request.Context(), tx, "GPA import replaced"); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"batch": batch, "imported": len(imported), "totalStudents": len(users), "missing": missing, "complete": len(missing) == 0})
}

func (s *Server) pasteGPA(c *gin.Context) {
	var input struct {
		Text string         `json:"text"`
		Rows []gpaImportRow `json:"rows"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "粘贴导入参数不正确", nil)
		return
	}
	rows := input.Rows
	if len(rows) == 0 {
		table, err := parseCSVRows(strings.NewReader(input.Text))
		if err != nil {
			writeError(c, http.StatusUnprocessableEntity, "gpa_parse_failed", "无法解析粘贴内容", err.Error())
			return
		}
		rows, err = parseGPATable(table)
		if err != nil {
			writeError(c, http.StatusUnprocessableEntity, "gpa_parse_failed", err.Error(), nil)
			return
		}
	} else {
		seen := make(map[string]bool)
		for index := range rows {
			rows[index].SID = strings.TrimSpace(rows[index].SID)
			rows[index].Name = strings.TrimSpace(rows[index].Name)
			rows[index].Source = index + 1
			if rows[index].SID == "" || seen[rows[index].SID] || math.IsNaN(rows[index].Score) || math.IsInf(rows[index].Score, 0) || rows[index].Score < 0 || rows[index].Score > 100 {
				writeError(c, http.StatusUnprocessableEntity, "gpa_parse_failed", "结构化导入行不正确或有重复学号", gin.H{"row": index + 1})
				return
			}
			seen[rows[index].SID] = true
			rows[index].Score = math.Round(rows[index].Score*1000) / 1000
		}
	}
	s.importGPA(c, rows, "paste")
}

func (s *Server) importGPAFile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxGPAUploadBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_file", "请选择不超过 10 MB 的 CSV 或 XLSX 文件", nil)
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	var table [][]string
	switch ext {
	case ".csv":
		table, err = parseCSVRows(file)
	case ".xlsx":
		var workbook *excelize.File
		workbook, err = excelize.OpenReader(file)
		if err == nil {
			defer workbook.Close()
			sheets := workbook.GetSheetList()
			if len(sheets) == 0 {
				err = errors.New("工作簿没有工作表")
			} else {
				table, err = workbook.GetRows(sheets[0])
			}
		}
	default:
		writeError(c, http.StatusUnsupportedMediaType, "unsupported_file", "只支持 CSV 或 XLSX 文件", nil)
		return
	}
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "gpa_parse_failed", "无法解析成绩文件", err.Error())
		return
	}
	imported, err := parseGPATable(table)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "gpa_parse_failed", err.Error(), nil)
		return
	}
	s.importGPA(c, imported, strings.TrimPrefix(ext, "."))
}

func (s *Server) adminGPA(c *gin.Context) {
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT u.id,u.sid,u.name,g.score::float8,g.import_batch::text,g.updated_at
		  FROM app_user u LEFT JOIN student_gpa g ON g.student_id=u.id AND g.scheme_id=$1
		 WHERE u.status='active' ORDER BY u.sid
	`, current.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	items := make([]gin.H, 0)
	importedCount := 0
	var sum float64
	var latest *time.Time
	for rows.Next() {
		var userID int64
		var sid, name string
		var score *float64
		var batch *string
		var updatedAt *time.Time
		if err := rows.Scan(&userID, &sid, &name, &score, &batch, &updatedAt); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		item := gin.H{"userId": strconv.FormatInt(userID, 10), "sid": sid, "name": name, "score": score, "batch": batch, "updatedAt": updatedAt}
		items = append(items, item)
		if score != nil {
			importedCount++
			sum += *score
			if updatedAt != nil && (latest == nil || updatedAt.After(*latest)) {
				value := *updatedAt
				latest = &value
			}
		}
	}
	rows.Close()
	var average *float64
	if importedCount > 0 {
		value := math.Round(sum/float64(importedCount)*1000) / 1000
		average = &value
	}
	if err := appendAudit(c, tx, "gpa.read", "student_gpa", "", nil, nil, map[string]any{"imported": importedCount}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items, "imported": importedCount, "total": len(items), "complete": importedCount == len(items) && len(items) > 0,
		"average": average, "latestAt": latest, "weights": current.Config.Weights, "honorTopPercent": current.Config.HonorRoll.TopPercent,
	})
}
