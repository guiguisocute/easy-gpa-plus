package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"easygpa/backend/internal/opsconfig"
)

const (
	TemplateAppealPending      = "appeal_pending"
	TemplateAppealReceived     = "appeal_received"
	TemplateAppealResolved     = "appeal_resolved"
	TemplateFinalReviewReady   = "final_review_ready"
	TemplateMailTest           = "mail_test"
	TemplatePasswordReset      = "password_reset"
	TemplateReviewDecided      = "review_decided"
	TemplateResultConfirmed    = "result_confirmed"
	TemplateRulingNotice       = "ruling_notice"
	TemplateScorecardTask      = "scorecard_audit_task"
	TemplateSealConfirmed      = "seal_confirmed"
	TemplateSettlementDone     = "settlement_done"
	TemplateTaskNew            = "task_new"
	TemplateVerificationCode   = "verification_code"
	TemplateWindowReminder     = "window_reminder"
	TemplateNotificationAlert  = "notification_alert"
	TemplateNotificationDigest = "notification_digest"
)

const (
	// Raw templates are also uploaded to Tencent Cloud SES for review. Keep the
	// production origin literal in those files: SES rejects attributes such as
	// href="{{url}}" where a variable supplies the entire URL. At runtime the
	// literal origin is replaced with PUBLIC_URL so local and staging mail still
	// points at the environment that sent it.
	mailTemplatePublicOrigin = "https://gpa.example.org"
)

var requiredTemplates = opsconfig.AllMailTemplates()

var simpleTemplateField = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_]*)\s*\}\}`)
var variableURLAttribute = regexp.MustCompile(`(?i)\b(?:href|src)\s*=\s*["']([^"']*\{\{\s*[A-Za-z][A-Za-z0-9_]*\s*\}\}[^"']*)["']`)

type TemplateConfig struct {
	Dir     string
	BaseURL string
}

// TemplateMailer decorates a delivery provider with the repository's HTML
// templates. Callers still provide a useful plain-text body for clients that
// do not render HTML.
type TemplateMailer struct {
	next      Mailer
	templates map[string]*template.Template
	fields    map[string][]string
}

func NewTemplateMailer(next Mailer, cfg TemplateConfig) (*TemplateMailer, error) {
	if next == nil {
		return nil, errors.New("template mailer provider is required")
	}
	dir, err := resolveTemplateDir(cfg.Dir)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	loaded := make(map[string]*template.Template, len(requiredTemplates))
	fields := make(map[string][]string, len(requiredTemplates))
	for _, name := range requiredTemplates {
		path := filepath.Join(dir, name+".html")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read mail template %s: %w", name, err)
		}
		source := string(raw)
		seenFields := make(map[string]bool)
		for _, match := range simpleTemplateField.FindAllStringSubmatch(source, -1) {
			if !seenFields[match[1]] {
				fields[name] = append(fields[name], match[1])
				seenFields[match[1]] = true
			}
		}
		for _, match := range variableURLAttribute.FindAllStringSubmatch(source, -1) {
			if !strings.HasPrefix(match[1], mailTemplatePublicOrigin) {
				return nil, fmt.Errorf("mail template %s has a variable URL without the literal production origin: %s", name, match[0])
			}
		}
		if baseURL != "" {
			source = strings.ReplaceAll(source, mailTemplatePublicOrigin, baseURL)
		}
		// The design assets use Mustache-style {{name}} placeholders. Translate
		// that intentionally small subset to html/template's {{.name}} syntax so
		// values receive contextual HTML escaping.
		source = simpleTemplateField.ReplaceAllString(source, "{{.$1}}")
		parsed, err := template.New(name).Option("missingkey=zero").Parse(source)
		if err != nil {
			return nil, fmt.Errorf("parse mail template %s: %w", name, err)
		}
		loaded[name] = parsed
	}

	mailer := &TemplateMailer{next: next, templates: loaded, fields: fields}
	return mailer, nil
}

func (m *TemplateMailer) Send(ctx context.Context, msg Message) (string, error) {
	if msg.Template == "" {
		return m.next.Send(ctx, msg)
	}
	tmpl, ok := m.templates[msg.Template]
	if !ok {
		return "", fmt.Errorf("unknown mail template %q", msg.Template)
	}
	data := defaultTemplateData()
	for key, value := range msg.Data {
		data[key] = value
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return "", fmt.Errorf("render mail template %s: %w", msg.Template, err)
	}
	msg.HTML = rendered.String()
	// SES receives only the variables that actually occur in this approved
	// template. Missing optional values use the same visible fallback as the
	// local renderer, while unrelated variables are not sent to Tencent Cloud.
	providerData := make(map[string]any, len(m.fields[msg.Template]))
	for _, key := range m.fields[msg.Template] {
		providerData[key] = data[key]
	}
	msg.Data = providerData
	return m.next.Send(ctx, msg)
}

func (m *TemplateMailer) Ready(ctx context.Context) error {
	checker, ok := m.next.(interface{ Ready(context.Context) error })
	if !ok {
		return nil
	}
	return checker.Ready(ctx)
}

func resolveTemplateDir(configured string) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		info, err := os.Stat(configured)
		if err != nil || !info.IsDir() {
			if err == nil {
				err = errors.New("not a directory")
			}
			return "", fmt.Errorf("MAIL_TEMPLATE_DIR %q is unavailable: %w", configured, err)
		}
		return configured, nil
	}
	for _, candidate := range []string{
		"asset/mailtemplate",
		"../asset/mailtemplate",
		"../../../asset/mailtemplate",
		"mailtemplate",
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("mail templates not found; set MAIL_TEMPLATE_DIR")
}

func defaultTemplateData() map[string]any {
	data := make(map[string]any)
	for _, key := range []string{
		"name", "class_name", "filed_at", "reason_excerpt",
		"student_name", "student_sid", "target_type_label", "title", "reason", "result_label",
		"score_after", "score_before", "basis", "category_name", "decision_label", "score",
		"ruling_type_label", "decided_count", "sealed_at", "source_label", "submitted_count",
		"class_rank", "class_size", "honor_label", "scheme_name", "scheme_version", "score_gpa",
		"score_health", "score_moral", "score_practice", "settled_at", "total_score", "pending_count", "new_count",
		"code", "expire_minutes", "email", "provider", "sent_at", "days_left", "window_close",
		"event_label", "summary",
		"review_heading", "review_message", "score_label", "action_note",
		"ruling_heading", "ruling_message",
		"task_kind", "task_message", "assignment_id", "assigned_at", "due_at",
		"student_name", "audience_label", "completed_at", "confirmation_deadline", "confirmation_guidance",
		"confirmation_source", "confirmed_at",
	} {
		data[key] = "—"
	}
	data["reset_token"] = ""
	return data
}
