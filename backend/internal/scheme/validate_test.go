package scheme

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func timeline() (Window, Capabilities, HonorRoll) {
	return DefaultWindow(), DefaultCapabilities(), DefaultHonorRoll()
}

func TestValidateTimelineAcceptsTheDefaults(t *testing.T) {
	if err := ValidateTimeline(timeline()); err != nil {
		t.Fatalf("system defaults must validate: %v", err)
	}
}

func TestValidateTimelineRejectsLockdownBeforeClose(t *testing.T) {
	window, capabilities, honorRoll := timeline()
	early := window.Close.Add(-time.Second)
	window.Lockdown = &early
	if err := ValidateTimeline(window, capabilities, honorRoll); err == nil {
		t.Fatal("lockdown before the sealing deadline was accepted")
	}

	// 相等是允许的：封存即封死，只是不留复议尾巴。
	same := window.Close
	window.Lockdown = &same
	if err := ValidateTimeline(window, capabilities, honorRoll); err != nil {
		t.Fatalf("lockdown at the sealing deadline must be allowed: %v", err)
	}
}

func TestValidateTimelineChecksAwardTiers(t *testing.T) {
	cases := []struct {
		name   string
		awards []Award
		ok     bool
	}{
		{"默认三档", DefaultAwards(), true},
		{"一档都不设", []Award{}, true},
		{"名称为空", []Award{{Name: "  ", TopPercent: 5}}, false},
		{"档位重名", []Award{{Name: "一等", TopPercent: 5}, {Name: "一等", TopPercent: 10}}, false},
		{"单档为负", []Award{{Name: "一等", TopPercent: -1}}, false},
		{"单档超过 100", []Award{{Name: "一等", TopPercent: 101}}, false},
		{"累计正好 100", []Award{{Name: "一等", TopPercent: 40}, {Name: "二等", TopPercent: 60}}, true},
		{"累计超过全班", []Award{{Name: "一等", TopPercent: 60}, {Name: "二等", TopPercent: 60}}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			window, capabilities, honorRoll := timeline()
			honorRoll.Awards = testCase.awards
			err := ValidateTimeline(window, capabilities, honorRoll)
			if testCase.ok && err != nil {
				t.Fatalf("expected acceptance, got %v", err)
			}
			if !testCase.ok && err == nil {
				t.Fatal("expected rejection")
			}
		})
	}

	window, capabilities, honorRoll := timeline()
	honorRoll.Awards = make([]Award, MaxAwards+1)
	for i := range honorRoll.Awards {
		honorRoll.Awards[i] = Award{Name: string(rune('A' + i)), TopPercent: 0}
	}
	if err := ValidateTimeline(window, capabilities, honorRoll); err == nil {
		t.Fatalf("more than %d tiers was accepted", MaxAwards)
	}
}

// 窗口字段不再序列化进 scheme.config——这是"解绑"在类型层面的落点。
// 少了这条，某个 marshal 又会把一份过期的窗口写回方案行里。
func TestConfigDoesNotSerializeTheRuntimeEnvelope(t *testing.T) {
	raw, err := json.Marshal(DefaultSelfReportConfig("v1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"window"`, `"capabilities"`, `"honorRoll"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("scheme config still serializes %s", key)
		}
	}
	for _, key := range []string{`"weights"`, `"categories"`, `"schemeName"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("scheme config lost %s", key)
		}
	}
}
