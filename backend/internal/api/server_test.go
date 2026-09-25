package api

import "testing"

// 浏览器按字面串比对 Origin，localhost 与 127.0.0.1 不互相等价；开发时两种
// 写法都会真实出现，少放一个就是直传附件预检 403。
func TestCORSOriginsCoverBothLocalhostSpellings(t *testing.T) {
	origins := corsOrigins("https://easygpa.example.com")
	for _, required := range []string{
		"http://localhost:5173", "http://127.0.0.1:5173",
		"http://localhost:35173", "http://127.0.0.1:35173",
		"http://localhost:45173", "http://127.0.0.1:45173",
		"https://easygpa.example.com",
	} {
		if !containsOrigin(origins, required) {
			t.Fatalf("origins %v missing %q", origins, required)
		}
	}
}

func TestCORSOriginsKeepPublicURLOriginOnlyAndDeduplicate(t *testing.T) {
	origins := corsOrigins("http://127.0.0.1:5173/some/path")
	if containsOrigin(origins, "http://127.0.0.1:5173/some/path") {
		t.Fatalf("path leaked into origin list: %v", origins)
	}
	seen := 0
	for _, origin := range origins {
		if origin == "http://127.0.0.1:5173" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("expected one entry for the dev origin, got %d in %v", seen, origins)
	}
	if len(corsOrigins("")) != 6 {
		t.Fatalf("empty PUBLIC_URL should keep only the six local origins: %v", corsOrigins(""))
	}
}

func containsOrigin(origins []string, value string) bool {
	for _, origin := range origins {
		if origin == value {
			return true
		}
	}
	return false
}
