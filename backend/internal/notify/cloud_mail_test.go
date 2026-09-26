package notify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type mailRoundTripper func(*http.Request) (*http.Response, error)

func (f mailRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Keep production endpoint selection in the test while routing its requests
// exclusively to a local HTTP fixture. No cloud mail is sent by these tests.
func localMailHTTP(t *testing.T, client *http.Client, endpoint string, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	local, _ := url.Parse(server.URL)
	transport := server.Client().Transport
	client.Transport = mailRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != endpoint {
			return nil, fmt.Errorf("unexpected mail endpoint: %s", request.URL.Redacted())
		}
		copy := request.Clone(request.Context())
		copy.URL.Scheme, copy.URL.Host = local.Scheme, local.Host
		return transport.RoundTrip(copy)
	})
}

func TestAliyunSignatureMatchesOfficialPOSTVector(t *testing.T) {
	// Alibaba's published example includes characters requiring double encoding.
	// https://www.alibabacloud.com/help/en/direct-mail/signature
	raw := "AccessKeyId=testid&AccountName=<a%b'>&Action=SingleSendMail&AddressType=1&Format=XML&HtmlBody=4&RegionId=cn-hangzhou&ReplyToAddress=true&SignatureMethod=HMAC-SHA1&SignatureNonce=c1b2c332-4cfb-4a0f-b8cc-ebe622aa0a5c&SignatureVersion=1.0&Subject=3&TagName=2&Timestamp=2016-10-20T06:27:56Z&ToAddress=1@test.com&Version=2015-11-23"
	parameters := url.Values{}
	for _, pair := range strings.Split(raw, "&") {
		name, value, _ := strings.Cut(pair, "=")
		parameters.Set(name, value)
	}
	if got := aliyunMailSignature(http.MethodPost, parameters, "testsecret"); got != "llJfXJjBW3OacrVgxxsITgYaYm0=" {
		t.Fatalf("signature = %q", got)
	}
	if got := aliyunPercentEncode("中文 +*~"); got != "%E4%B8%AD%E6%96%87%20%2B%2A~" {
		t.Fatalf("encoding = %q", got)
	}
}

func TestAliyunMailUsesSignedPOSTAndLocalTemplate(t *testing.T) {
	mailer, err := NewAliyunMailer(AliyunMailConfig{SenderConfig: SenderConfig{From: "sender@example.org", FromName: "测试", ReplyTo: "reply@example.org"}, Region: "cn-hangzhou", AccessKeyID: "fixture-id", AccessKeySecret: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	mailer.now = func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }
	mailer.nonce = func() string { return "fixture-nonce" }
	localMailHTTP(t, mailer.client, "https://dm.aliyuncs.com/", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Form.Get("AccountName") != "sender@example.org" || r.Form.Get("TextBody") != "纯文本" || r.Form.Get("HtmlBody") != "<p>HTML</p>" || r.Form.Get("ReplyAddress") != "reply@example.org" || r.Form.Get("Signature") != aliyunMailSignature(http.MethodPost, r.Form, "fixture-secret") {
			t.Error("Aliyun request fields or signature were incorrect")
		}
		_, _ = io.WriteString(w, `{"EnvId":"fixture-receipt"}`)
	})
	id, err := mailer.Send(t.Context(), Message{To: "student@example.org", Subject: "测试", Text: "纯文本", HTML: "<p>HTML</p>", Template: TemplateMailTest})
	if err != nil || id != "aliyun:fixture-receipt" {
		t.Fatalf("send = %q, %v", id, err)
	}
}

func TestResendMailUsesBearerLocalTemplateAndStableEventKey(t *testing.T) {
	mailer, err := NewResendMailer(ResendConfig{SenderConfig: SenderConfig{From: "auth@example.org", FromName: "Auth", ReplyTo: "reply@example.org", NotificationsEnabled: true, NotificationFrom: "notice@notify.example.org", NotificationFromName: "通知"}, APIKey: "re_fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var previousKey string
	localMailHTTP(t, mailer.client, resendEmailEndpoint, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			HTML    string   `json:"html"`
			Text    string   `json:"text"`
			ReplyTo string   `json:"reply_to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		key := r.Header.Get("Idempotency-Key")
		if key == "" || previousKey != "" && key != previousKey {
			t.Error("event idempotency key was absent or unstable")
		}
		previousKey = key
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer re_fixture" || body.From != "通知 <notice@notify.example.org>" || len(body.To) != 1 || body.To[0] != "student@example.org" || body.HTML != "<p>通知</p>" || body.Text != "通知" || body.ReplyTo != "reply@example.org" {
			t.Error("Resend request fields were incorrect")
		}
		_, _ = io.WriteString(w, `{"id":"fixture-receipt"}`)
	})
	for i := 0; i < 2; i++ {
		id, err := mailer.Send(t.Context(), Message{To: "student@example.org", Subject: "通知", Text: "通知", HTML: "<p>通知</p>", EventID: "fixture-event", Template: TemplateNotificationAlert})
		if err != nil || id != "resend:fixture-receipt" {
			t.Fatalf("send = %q, %v", id, err)
		}
	}
}

func TestCloudMailFailureIsSafeAndDoesNotFollowRedirects(t *testing.T) {
	for _, provider := range []string{"aliyun", "resend"} {
		for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusTemporaryRedirect} {
			t.Run(fmt.Sprintf("%s/%d", provider, status), func(t *testing.T) {
				var sender Mailer
				var client *http.Client
				var endpoint string
				if provider == "aliyun" {
					m, err := NewAliyunMailer(AliyunMailConfig{SenderConfig: SenderConfig{From: "sender@example.org"}, Region: "cn-hangzhou", AccessKeyID: "fixture", AccessKeySecret: "fixture-secret"})
					if err != nil {
						t.Fatal(err)
					}
					sender, client, endpoint = m, m.client, aliyunMailEndpoints[m.cfg.Region]
				} else {
					m, err := NewResendMailer(ResendConfig{SenderConfig: SenderConfig{From: "sender@example.org"}, APIKey: "re_fixture"})
					if err != nil {
						t.Fatal(err)
					}
					sender, client, endpoint = m, m.client, resendEmailEndpoint
				}
				requests := 0
				localMailHTTP(t, client, endpoint, func(w http.ResponseWriter, r *http.Request) {
					requests++
					w.Header().Set("Location", "https://example.org/steal")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"Code":"Rejected","name":"rejected","Message":"recipient@example.org secret-body","message":"recipient@example.org secret-body"}`)
				})
				_, err := sender.Send(t.Context(), Message{To: "student@example.org", Subject: "test", Text: "test", Template: TemplateMailTest})
				if err == nil || strings.Contains(err.Error(), "recipient@example.org") || strings.Contains(err.Error(), "secret-body") {
					t.Fatalf("unsafe send error: %v", err)
				}
				if requests != 1 || definitelyNotSubmitted(err) != (status < 500 && status >= 400) || isSESFrequencyLimit(err) != (status == http.StatusTooManyRequests) {
					t.Fatalf("incorrect outcome classification: calls=%d err=%v", requests, err)
				}
			})
		}
	}
}
