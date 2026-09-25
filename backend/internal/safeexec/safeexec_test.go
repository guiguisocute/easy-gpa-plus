package safeexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestRunBoundsOutput(t *testing.T) {
	name, args := outputCommand(t, "1234567890")
	output, err := run(context.Background(), name, args, Options{MaxOutput: 5})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("error = %v, want output limit", err)
	}
	if string(output) != "12345" {
		t.Fatalf("output = %q", output)
	}
}

func TestRunScrubsParentSecrets(t *testing.T) {
	t.Setenv("EASYGPA_SAFEEXEC_SECRET", "must-not-leak")
	var name string
	var args []string
	if runtime.GOOS == "windows" {
		name, args = "cmd", []string{"/d", "/c", "set"}
	} else {
		name, args = "env", nil
	}
	output, err := run(context.Background(), name, args, Options{MaxOutput: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "EASYGPA_SAFEEXEC_SECRET") {
		t.Fatalf("parent secret leaked to subprocess: %s", output)
	}
}

func TestRunRejectsCommandsOutsideFixedToolset(t *testing.T) {
	for _, name := range []string{"sh", "bash", "cmd", "powershell", "env", "curl", "unknown"} {
		if _, err := Run(context.Background(), name, nil, Options{MaxOutput: 1024}); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("Run(%q) error = %v, want allowlist rejection", name, err)
		}
	}
}

func outputCommand(t *testing.T, value string) (string, []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/d", "/c", "<nul set /p =" + value}
	}
	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("printf is unavailable")
	}
	return "printf", []string{"%s", value}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
