package api

import (
	"crypto/sha256"
	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/scorecard"
	"easygpa/backend/internal/settle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"time"
)

type myScorecardItem struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	FiledCategory     string          `json:"filedCategory"`
	FiledItemKey      string          `json:"filedItemKey"`
	Category          string          `json:"category"`
	ItemKey           string          `json:"itemKey"`
	Claim             json.RawMessage `json:"claim"`
	RequestedScore    *float64        `json:"requestedScore"`
	Score             float64         `json:"score"`
	Note              string          `json:"note"`
	Source            string          `json:"source"`
	SubmittedAt       *time.Time      `json:"submittedAt"`
	RuleSnapshot      json.RawMessage `json:"ruleSnapshot"`
	FiledRuleSnapshot json.RawMessage `json:"filedRuleSnapshot"`
	Evidence          json.RawMessage `json:"evidence"`
	NoteEvidence      json.RawMessage `json:"noteEvidence"`
	Reviews           []anonReview    `json:"reviews"`
	ForceRejection    json.RawMessage `json:"forceRejection"`
	ForcedScore       json.RawMessage `json:"forcedScore"`
}

type anonReview struct {
	Decision string    `json:"decision"`
	Score    *float64  `json:"score"`
	Reason   string    `json:"reason"`
	At       time.Time `json:"at"`
}

// 快照里的 details.categories 是 scheme.CategoryBreakdown 的原始序列化：大写
// 键、Points 毫分整数。这里必须用 scheme.Points 字段接住再 Float64()，直接用
// float64 会把分数放大一千倍。
type categoryBreakdownSnapshot struct {
	ItemTotal    scheme.Points
	BaseTotal    scheme.Points
	PenaltyTotal scheme.Points
	BeforeCap    scheme.Points
	Total        scheme.Points
}

type myScorecardSnapshot struct {
	SchemeVersion  string             `json:"schemeVersion"`
	InputHash      string             `json:"inputHash"`
	CapturedAt     time.Time          `json:"capturedAt"`
	CategoryScores map[string]float64 `json:"categoryScores"`
	Details        struct {
		Items      []myScorecardItem                    `json:"items"`
		BaseItems  json.RawMessage                      `json:"baseItems"`
		Categories map[string]categoryBreakdownSnapshot `json:"categories"`
	} `json:"details"`
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func liveScorecardRevision(card any) (string, error) {
	raw, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func loadLiveScorecard(c *gin.Context, tx pgx.Tx, current storedScheme, studentID int64) (gin.H, string, error) {
	snapshots, err := scorecard.Build(c.Request.Context(), tx, studentID, current.ID, current.Config, time.Now())
	if err != nil {
		return nil, "", err
	}
	if len(snapshots) != 1 {
		return nil, "", pgx.ErrNoRows
	}
	raw, err := json.Marshal(snapshots[0])
	if err != nil {
		return nil, "", err
	}
	card, err := buildMyScorecard(c, tx, studentID, current, raw)
	if err != nil {
		return nil, "", err
	}
	rows, err := tx.Query(c.Request.Context(), `
 SELECT 'submission',id::text,title,status FROM submission
  WHERE student_id=$1 AND status IN ('pending','consensus','arbitrating','appealing')
 UNION ALL
 SELECT 'appeal',a.id::text,COALESCE(s.title,'基础分 / 扣分申诉'),a.status
  FROM appeal a LEFT JOIN submission s ON a.target_type='submission' AND s.id=a.target_id
  WHERE a.student_id=$1 AND a.kind='student_appeal' AND a.status IN ('filed','reviewing','escalated')
 UNION ALL
 SELECT 'objection',id::text,'计分异议',status FROM objection
  WHERE student_id=$1 AND status='submitted'
 ORDER BY 1,2
 `, studentID)
	if err != nil {
		return nil, "", err
	}
	issues := make([]gin.H, 0)
	for rows.Next() {
		var kind, id, title, status string
		if err := rows.Scan(&kind, &id, &title, &status); err != nil {
			rows.Close()
			return nil, "", err
		}
		issues = append(issues, gin.H{"kind": kind, "id": id, "title": title, "status": status})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	card["issues"] = issues
	revision, err := liveScorecardRevision(card)
	return card, revision, err
}

func (s *Server) myScorecard(c *gin.Context) {
	actor, tx := mustActor(c), mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if notFound(c, err, "当前方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	card, revision, err := loadLiveScorecard(c, tx, current, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var confirmedAt *time.Time
	err = tx.QueryRow(c.Request.Context(), `SELECT confirmed_at FROM score_acknowledgement
  WHERE student_id=$1 AND scheme_id=$2 AND revision=$3`, actor.UserID, current.ID, revision).Scan(&confirmedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	var everConfirmed bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM score_acknowledgement WHERE student_id=$1 AND scheme_id=$2)`, actor.UserID, current.ID).Scan(&everConfirmed); err != nil {
		writeServiceError(c, err)
		return
	}
	state := "confirmable"
	if confirmedAt != nil {
		state = "confirmed"
	}
	c.JSON(http.StatusOK, gin.H{
		"state": state, "revision": revision, "updatedAt": time.Now().UTC(), "scorecard": card,
		"confirmation": gin.H{"confirmed": confirmedAt != nil, "confirmedAt": confirmedAt, "recheck": everConfirmed && confirmedAt == nil},
	})
}

func (s *Server) confirmMyResult(c *gin.Context) {
	var input struct {
		Revision string `json:"revision"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Revision) != 64 {
		writeError(c, http.StatusBadRequest, "invalid_request", "请先刷新当前成绩后再确认", nil)
		return
	}
	actor, tx := mustActor(c), mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if notFound(c, err, "当前方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	_, revision, err := loadLiveScorecard(c, tx, current, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if input.Revision != revision {
		writeError(c, http.StatusConflict, "scorecard_changed", "成绩或处理进度已更新，请刷新核对后再确认", nil)
		return
	}
	var id int64
	var confirmedAt time.Time
	err = tx.QueryRow(c.Request.Context(), `INSERT INTO score_acknowledgement(class_id,student_id,scheme_id,revision)
  VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING id,confirmed_at`, actor.ClassID, actor.UserID, current.ID, revision).Scan(&id, &confirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(c.Request.Context(), `SELECT id,confirmed_at FROM score_acknowledgement WHERE student_id=$1 AND scheme_id=$2 AND revision=$3`, actor.UserID, current.ID, revision).Scan(&id, &confirmedAt)
	} else if err == nil {
		err = appendAudit(c, tx, "score.acknowledged", "score_acknowledgement", strconv.FormatInt(id, 10), nil, gin.H{"revision": revision}, nil)
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"confirmed": true, "confirmedAt": confirmedAt, "revision": revision})
}

func buildMyScorecard(c *gin.Context, tx pgx.Tx, studentID int64, current storedScheme, snapshotRaw []byte) (gin.H, error) {
	var snap myScorecardSnapshot
	if err := json.Unmarshal(snapshotRaw, &snap); err != nil {
		return nil, err
	}
	categories := make([]gin.H, 0, len(current.Config.Categories))
	for _, category := range current.Config.Categories {
		if category.Key == "major" {
			continue
		}
		breakdown := snap.Details.Categories[category.Key]
		categories = append(categories, gin.H{
			"key": category.Key, "name": category.Name,
			"itemTotal": breakdown.ItemTotal.Float64(), "baseTotal": breakdown.BaseTotal.Float64(),
			"penaltyTotal": breakdown.PenaltyTotal.Float64(), "beforeCap": breakdown.BeforeCap.Float64(),
			"total":    breakdown.Total.Float64(),
			"maxTotal": category.MaxTotal, "weight": current.Config.Weights[category.Key],
		})
	}
	var gpaScore *float64
	err := tx.QueryRow(c.Request.Context(), `
		SELECT score::float8 FROM student_gpa WHERE student_id=$1 AND scheme_id=$2
	`, studentID, current.ID).Scan(&gpaScore)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// 预估总分复用 settle.Compute：同一个函数就是同一个口径，名次丢弃。GPA 未
	// 导入时不给估算值，不用 0 分凑合成一个错的总分。
	var estimatedTotal *float64
	if gpaScore != nil {
		scores := make(map[string]scheme.Points, len(snap.CategoryScores)+1)
		for key, value := range snap.CategoryScores {
			scores[key] = scheme.NewPoints(value)
		}
		scores["major"] = scheme.NewPoints(*gpaScore)
		var sid, name string
		if err := tx.QueryRow(c.Request.Context(), `SELECT sid,name FROM app_user WHERE id=$1`, studentID).Scan(&sid, &name); err != nil {
			return nil, err
		}
		snapshots, err := settle.Compute(
			[]settle.StudentInput{{UserID: studentID, SID: sid, Name: name, CategoryScores: scores}},
			current.Config.Weights, current.Config.HonorRoll,
		)
		if err != nil {
			return nil, err
		}
		estimatedTotal = &snapshots[0].TotalScore
	}
	var baseRows []map[string]json.RawMessage
	if err := json.Unmarshal(snap.Details.BaseItems, &baseRows); err != nil {
		return nil, err
	}
	for _, row := range baseRows {
		delete(row, "appeals")
	}
	baseItems, err := json.Marshal(baseRows)
	if err != nil {
		return nil, err
	}
	if baseItems == nil {
		baseItems = json.RawMessage("[]")
	}
	return gin.H{
		"schemeVersion": snap.SchemeVersion,
		"categories":    categories, "items": snap.Details.Items, "baseItems": baseItems,
		"gpa":            gin.H{"score": gpaScore, "imported": gpaScore != nil},
		"estimatedTotal": estimatedTotal,
		"estimateNote":   "按当前认定分项与专业成绩计算；未定分材料不计入，正式名次与评优以班级结算为准",
	}, nil
}
