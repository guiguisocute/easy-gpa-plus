package agentcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrResource = errors.New("当前事项不存在或你已无权读取，请返回原页面刷新")
var ErrStale = errors.New("当前事项或材料已经变化，请刷新后重新提问")

// Draft contains only the unsaved fields of the current adjudication form.
// It is user input, never authoritative business state or an instruction.
type Draft struct {
	Reason   string `json:"reason"`
	Score    string `json:"score"`
	Category string `json:"category"`
	ItemKey  string `json:"itemKey"`
}

type ReasonDraft struct {
	Context Context `json:"context"`
	Reason  string  `json:"reason"`
}

type Evidence struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	Status    string `json:"status"`
	ObjectKey string `json:"-"`
}

type Snapshot struct {
	Context  Context         `json:"context"`
	Facts    json.RawMessage `json:"facts"`
	Rules    json.RawMessage `json:"rules"`
	Evidence []Evidence      `json:"evidence"`
	CanDraft bool            `json:"canDraft"`
}

func ResolveInput(role string, input Context) (Context, error) {
	resolved, err := Resolve(role, input.View, input.ResourceID)
	if err != nil {
		return Context{}, err
	}
	if input.ResourceID == "" && input.ResourceKind == "" {
		return resolved, nil
	}
	if (input.View != "admSubs" && input.View != "revDeputy") ||
		(input.ResourceKind != "submission" && input.ResourceKind != "appeal") {
		return Context{}, ErrResource
	}
	id, err := strconv.ParseInt(input.ResourceID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != input.ResourceID {
		return Context{}, ErrResource
	}
	if input.EvidenceID != "" {
		eid, e := strconv.ParseInt(input.EvidenceID, 10, 64)
		if e != nil || eid <= 0 {
			return Context{}, ErrResource
		}
	}
	if input.Draft != nil && (len(input.Draft.Reason) > 30000 || len(input.Draft.Score) > 32 || len(input.Draft.Category) > 100 || len(input.Draft.ItemKey) > 200) {
		return Context{}, errors.New("当前表单内容过长")
	}
	resolved.ResourceKind, resolved.Revision, resolved.EvidenceID, resolved.Draft = input.ResourceKind, input.Revision, input.EvidenceID, input.Draft
	return resolved, nil
}

// Load uses a tenant transaction and the live actor, not client-supplied roles.
// Initially only adjudication pages register business resources. In particular,
// review/student page names cannot be used to reveal peer reviews through Agent.
func Load(ctx context.Context, tx pgx.Tx, classID, userID int64, input Context) (Snapshot, error) {
	var role string
	var deputy bool
	err := tx.QueryRow(ctx, `SELECT u.role,u.is_deputy FROM app_user u JOIN class c ON c.id=u.class_id
		WHERE u.id=$1 AND u.class_id=$2 AND u.status='active' AND NOT c.archived`, userID, classID).Scan(&role, &deputy)
	if err != nil {
		return Snapshot{}, ErrResource
	}
	var collective bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM class_governance WHERE class_id=$1 AND mode='collective')`, classID).Scan(&collective); err != nil {
		return Snapshot{}, ErrResource
	}
	if collective {
		role = "student"
		deputy = false
	}
	resolved, err := ResolveInput(role, input)
	if err != nil || resolved.ResourceID == "" {
		return Snapshot{}, ErrResource
	}
	if resolved.View == "revDeputy" && (role != "group" || !deputy) {
		return Snapshot{}, ErrResource
	}
	var result Snapshot
	result.Context = resolved
	var studentID, submissionID int64
	var studentRole, student, title, status string
	if resolved.ResourceKind == "submission" {
		err = tx.QueryRow(ctx, `SELECT s.student_id,u.role,u.name,s.title,s.status,s.id,
			jsonb_build_object('id',s.id::text,'student',u.name,'studentId',u.sid,'title',s.title,
			'claim',s.claim,'requestedScore',s.requested_score,'finalScore',s.final_score,'note',s.markdown_note,
			'category',s.category_key,'itemKey',s.item_key,'filedCategory',s.filed_category_key,'filedItemKey',s.filed_item_key,
			'status',s.status,'submittedAt',s.submitted_at,'updatedAt',s.updated_at,'forceRejection',s.force_rejection),
			jsonb_build_object('filed',s.filed_rule_snapshot,'effective',s.rule_snapshot)
			FROM submission s JOIN app_user u ON u.id=s.student_id AND u.class_id=s.class_id
			WHERE s.id=$1 AND s.class_id=$2`, resolved.ResourceID, classID).Scan(&studentID, &studentRole, &student, &title, &status, &submissionID, &result.Facts, &result.Rules)
	} else {
		err = tx.QueryRow(ctx, `SELECT a.student_id,u.role,u.name,COALESCE(s.title,a.original_item_key,a.target_type),a.status,COALESCE(s.id,0),
			jsonb_build_object('id',a.id::text,'student',u.name,'studentId',u.sid,'targetType',a.target_type,'targetId',a.target_id::text,
			'title',COALESCE(s.title,a.original_item_key,a.target_type),'reason',a.reason,'kind',a.kind,'round',a.round,
			'originalScore',a.original_score,'proposedScore',a.proposed_score,'proposalReason',a.proposal_reason,
			'originalCategory',a.original_category_key,'originalItemKey',a.original_item_key,
			'proposedCategory',a.proposed_category_key,'proposedItemKey',a.proposed_item_key,
			'resolutionScore',a.resolution_score,'resolutionReason',a.resolution_reason,'status',a.status,'updatedAt',a.updated_at,
			'originalSubmission',CASE WHEN s.id IS NOT NULL THEN jsonb_build_object('claim',s.claim,'requestedScore',s.requested_score,
			'note',s.markdown_note,'status',s.status,'updatedAt',s.updated_at,'finalScore',s.final_score) ELSE NULL END,
			'basis',b.basis,'fullScore',b.full_score,'currentScore',COALESCE(s.final_score,b.score)),
			jsonb_build_object('filed',s.filed_rule_snapshot,'effective',s.rule_snapshot,'appealOriginal',a.original_rule_snapshot)
			FROM appeal a JOIN app_user u ON u.id=a.student_id AND u.class_id=a.class_id
			LEFT JOIN submission s ON a.target_type='submission' AND s.id=a.target_id AND s.class_id=a.class_id
			LEFT JOIN base_score b ON a.target_type IN ('base_score','penalty_score') AND b.id=a.target_id AND b.class_id=a.class_id
			WHERE a.id=$1 AND a.class_id=$2`, resolved.ResourceID, classID).Scan(&studentID, &studentRole, &student, &title, &status, &submissionID, &result.Facts, &result.Rules)
	}
	if err != nil {
		return Snapshot{}, ErrResource
	}
	if !CanReadAdjudication(role, deputy, resolved.View, studentRole, resolved.ResourceKind, status) {
		return Snapshot{}, ErrResource
	}
	var facts map[string]any
	if err = json.Unmarshal(result.Facts, &facts); err != nil {
		return Snapshot{}, err
	}
	if submissionID > 0 {
		var reviews json.RawMessage
		err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',r.id::text,'reviewer',u.name,
			'decision',r.decision,'score',r.score,'reason',r.reason,'at',r.created_at) ORDER BY r.id),'[]'::jsonb)
			FROM review r JOIN app_user u ON u.id=r.reviewer_id WHERE r.submission_id=$1 AND r.superseded_at IS NULL`, submissionID).Scan(&reviews)
		if err != nil {
			return Snapshot{}, err
		}
		facts["originalReviews"] = reviews
	}
	if resolved.ResourceKind == "appeal" {
		var reviews json.RawMessage
		err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('reviewer',u.name,'decidedAt',ar.decided_at,
			'decision',ar.decision,'score',ar.score,'reason',ar.reason,'category',ar.category_key,'itemKey',ar.item_key) ORDER BY ar.position),'[]'::jsonb)
			FROM appeal_reviewer ar JOIN app_user u ON u.id=ar.reviewer_id WHERE ar.appeal_id=$1`, resolved.ResourceID).Scan(&reviews)
		if err != nil {
			return Snapshot{}, err
		}
		facts["rereviews"] = reviews
	}
	result.Facts, _ = json.Marshal(facts)
	rows, err := tx.Query(ctx, `SELECT id::text,filename,media_type,size_bytes,COALESCE(sha256,''),status,object_key FROM evidence
		WHERE class_id=$1 AND ((submission_id=$2 AND $2>0) OR ($3='appeal' AND appeal_id=$4)) ORDER BY id`, classID, submissionID, resolved.ResourceKind, resolved.ResourceID)
	if err != nil {
		return Snapshot{}, err
	}
	result.Evidence = []Evidence{}
	for rows.Next() {
		var file Evidence
		if err = rows.Scan(&file.ID, &file.Filename, &file.MediaType, &file.SizeBytes, &file.SHA256, &file.Status, &file.ObjectKey); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		result.Evidence = append(result.Evidence, file)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Snapshot{}, err
	}
	if resolved.EvidenceID != "" {
		found := false
		for _, file := range result.Evidence {
			found = found || file.ID == resolved.EvidenceID
		}
		if !found {
			return Snapshot{}, ErrResource
		}
	}
	var lockdown *time.Time
	err = tx.QueryRow(ctx, `SELECT lockdown_at FROM class_timeline WHERE class_id=$1`, classID).Scan(&lockdown)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, err
	}
	result.CanDraft = studentID != userID && (lockdown == nil || time.Now().Before(*lockdown)) &&
		((resolved.ResourceKind == "submission" && status == "arbitrating") || (resolved.ResourceKind == "appeal" && (status == "escalated" || status == "reviewing")))
	result.Context.ResourceLabel = student + " · #" + resolved.ResourceID + " · " + title
	manifest, _ := json.Marshal(result.Evidence)
	hash := sha256.New()
	hash.Write(result.Facts)
	hash.Write(result.Rules)
	hash.Write(manifest)
	result.Context.Revision = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func CanReadAdjudication(role string, deputy bool, view, subjectRole, kind, status string) bool {
	if view == "admSubs" {
		return role == "class_admin"
	}
	if view != "revDeputy" || role != "group" || !deputy || subjectRole != "class_admin" {
		return false
	}
	if kind == "appeal" {
		return status != "draft"
	}
	return status == "arbitrating" || status == "scored" || status == "locked" || status == "appealing"
}

func (s Snapshot) CheckRevision(expected string) error {
	if expected != "" && expected != s.Context.Revision {
		return ErrStale
	}
	return nil
}

// Fresh metadata must not silently rebind a question to business changes the
// person has not seen in the form yet.
func (s Snapshot) CheckObservedAt(expected string) error {
	if expected == "" {
		return nil
	}
	var facts struct {
		UpdatedAt time.Time `json:"updatedAt"`
	}
	observed, err := time.Parse(time.RFC3339Nano, expected)
	if err != nil || json.Unmarshal(s.Facts, &facts) != nil || !observed.Equal(facts.UpdatedAt) {
		return ErrStale
	}
	return nil
}

func (c Context) DocumentID() string {
	return "business-" + c.View + "-" + c.ResourceKind + "-" + c.ResourceID
}

func ParseDocumentID(id string) (Context, error) {
	parts := strings.Split(id, "-")
	if len(parts) != 4 || parts[0] != "business" {
		return Context{}, ErrResource
	}
	return Context{View: parts[1], ResourceKind: parts[2], ResourceID: parts[3]}, nil
}
