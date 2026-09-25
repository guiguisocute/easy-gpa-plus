package notify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type captureMailer struct {
	message Message
}

func (m *captureMailer) Send(_ context.Context, message Message) (string, error) {
	m.message = message
	return "captured", nil
}

func TestTemplateMailerRendersRepositoryTemplate(t *testing.T) {
	next := &captureMailer{}
	mailer, err := NewTemplateMailer(next, TemplateConfig{
		Dir:     filepath.Join("..", "..", "..", "asset", "mailtemplate"),
		BaseURL: "https://easygpa.example/",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mailer.Send(context.Background(), Message{
		To: "student@example.com", Subject: "验证码", Text: "验证码：123456",
		Template: TemplateVerificationCode,
		Data:     map[string]any{"name": "<学生>", "code": "123456", "expire_minutes": 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.message.HTML, "&lt;学生&gt;") || !strings.Contains(next.message.HTML, "123456") {
		t.Fatalf("rendered HTML does not contain escaped template data")
	}
	if strings.Contains(next.message.HTML, "{{") || strings.Contains(next.message.HTML, "cid:brand-mark") {
		t.Fatalf("rendered HTML contains an unresolved placeholder")
	}
	if !strings.Contains(next.message.HTML, "https://easygpa.example") {
		t.Fatalf("rendered HTML does not contain the configured public URL")
	}
	if len(next.message.Data) != 3 {
		t.Fatalf("SES template data contains %d fields, want exactly 3: %#v", len(next.message.Data), next.message.Data)
	}
	for _, key := range []string{"name", "code", "expire_minutes"} {
		if _, ok := next.message.Data[key]; !ok {
			t.Errorf("SES template data is missing %q", key)
		}
	}
	if _, ok := next.message.Data["class_name"]; ok {
		t.Fatal("SES template data leaked a field that is not declared by the selected template")
	}
}

func TestTemplateMailerRejectsUnknownTemplate(t *testing.T) {
	mailer, err := NewTemplateMailer(&captureMailer{}, TemplateConfig{
		Dir: filepath.Join("..", "..", "..", "asset", "mailtemplate"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mailer.Send(context.Background(), Message{Template: "missing"}); err == nil {
		t.Fatal("Send() accepted an unknown template")
	}
}

func TestRepositoryTemplatesAreTencentReviewSafe(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "asset", "mailtemplate")
	files, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(requiredTemplates) {
		t.Fatalf("template files = %d, required template registry = %d", len(files), len(requiredTemplates))
	}
	for _, name := range requiredTemplates {
		raw, err := os.ReadFile(filepath.Join(dir, name+".html"))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		source := string(raw)
		for _, match := range variableURLAttribute.FindAllStringSubmatch(source, -1) {
			if !strings.HasPrefix(match[1], mailTemplatePublicOrigin) {
				t.Errorf("%s contains a variable URL without a literal production origin: %s", name, match[0])
			}
		}
		if strings.Contains(source, "cid:brand-mark") {
			t.Errorf("%s contains a raw CID image that Tencent Cloud cannot resolve during template upload", name)
		}
	}
}

func TestPasswordResetTemplateKeepsOriginOutsideTokenVariable(t *testing.T) {
	next := &captureMailer{}
	mailer, err := NewTemplateMailer(next, TemplateConfig{
		Dir:     filepath.Join("..", "..", "..", "asset", "mailtemplate"),
		BaseURL: "http://localhost:5173/",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mailer.Send(context.Background(), Message{
		To: "student@example.com", Subject: "重置密码", Text: "重置密码",
		Template: TemplatePasswordReset,
		Data:     map[string]any{"reset_token": "abc123", "expire_minutes": 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "http://localhost:5173/?reset_token=abc123"
	if !strings.Contains(next.message.HTML, want) {
		t.Fatalf("rendered password reset HTML does not contain %q", want)
	}
	if strings.Contains(next.message.HTML, mailTemplatePublicOrigin) {
		t.Fatal("runtime template did not replace the review-time production origin")
	}
}

func TestEveryRequiredTemplateRendersWithoutPlaceholderLeak(t *testing.T) {
	next := &captureMailer{}
	mailer, err := NewTemplateMailer(next, TemplateConfig{
		Dir:     filepath.Join("..", "..", "..", "asset", "mailtemplate"),
		BaseURL: "https://runtime.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredTemplates {
		_, err := mailer.Send(context.Background(), Message{
			To: "student@example.com", Subject: name, Text: name, Template: name,
		})
		if err != nil {
			t.Errorf("render %s: %v", name, err)
			continue
		}
		if strings.Contains(next.message.HTML, "{{") || strings.Contains(next.message.HTML, "<no value>") {
			t.Errorf("%s leaked an unresolved placeholder", name)
		}
		if strings.Contains(next.message.HTML, mailTemplatePublicOrigin) {
			t.Errorf("%s did not replace the review-time production origin", name)
		}
	}
}
