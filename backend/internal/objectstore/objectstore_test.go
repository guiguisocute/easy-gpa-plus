package objectstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCORSRulesKeepOneOriginPerRule(t *testing.T) {
	t.Parallel()

	rules := corsRules([]string{
		" http://localhost:5173 ",
		"https://app.example.com",
		"http://localhost:5173",
		"",
	})

	if len(rules) != 2 {
		t.Fatalf("expected 2 unique rules, got %d", len(rules))
	}
	if got, want := rules[0].AllowedOrigin, []string{"http://localhost:5173"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first rule origins = %#v, want %#v", got, want)
	}
	if got, want := rules[1].AllowedOrigin, []string{"https://app.example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second rule origins = %#v, want %#v", got, want)
	}
	for _, rule := range rules {
		if len(rule.AllowedOrigin) != 1 {
			t.Fatalf("each Garage CORS rule must expose exactly one origin, got %#v", rule.AllowedOrigin)
		}
		if got, want := rule.AllowedMethod, []string{"GET", "POST", "HEAD"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("allowed methods = %#v, want %#v", got, want)
		}
	}
}

func TestPresignUploadBindsExactObjectSize(t *testing.T) {
	client, err := New(Config{
		Endpoint: "127.0.0.1:3900", PublicEndpoint: "storage.example.test", Region: "garage",
		AccessKey: "test-access-key", SecretKey: "test-secret-key", Bucket: "easygpa", PublicUseSSL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := client.PresignUpload(context.Background(), "class-1/evidence.pdf", "application/pdf", 1234, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if upload.URL.Scheme != "https" || upload.URL.Host != "storage.example.test" {
		t.Fatalf("upload URL = %s", upload.URL)
	}
	raw, err := base64.StdEncoding.DecodeString(upload.Fields["policy"])
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Conditions []any `json:"conditions"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	foundExactSize := false
	for _, condition := range policy.Conditions {
		items, ok := condition.([]any)
		if ok && len(items) == 3 && items[0] == "content-length-range" && items[1] == float64(1234) && items[2] == float64(1234) {
			foundExactSize = true
		}
	}
	if !foundExactSize {
		t.Fatalf("POST policy does not bind exact size: %s", raw)
	}
	if upload.Fields["Content-Type"] != "application/pdf" {
		t.Fatalf("Content-Type field = %q", upload.Fields["Content-Type"])
	}
}
