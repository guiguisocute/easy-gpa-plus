// Package safeexec runs fixed local tools with bounded output and a scrubbed
// environment. It is intended for parsers that process untrusted files.
package safeexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var ErrOutputLimit = errors.New("subprocess output exceeded the configured limit")

var allowedExecutables = map[string]struct{}{
	"antiword": {}, "catppt": {}, "xls2csv": {},
	"pdfinfo": {}, "pdftoppm": {}, "pdftotext": {},
	"createdb": {}, "dropdb": {}, "pg_dump": {}, "pg_restore": {},
}

type Options struct {
	Dir       string
	ExtraEnv  []string
	MaxOutput int64
}

func Run(ctx context.Context, name string, args []string, options Options) ([]byte, error) {
	if _, allowed := allowedExecutables[name]; !allowed {
		return nil, fmt.Errorf("subprocess %q is not allowed", name)
	}
	return run(ctx, name, args, options)
}

func run(ctx context.Context, name string, args []string, options Options) ([]byte, error) {
	if options.MaxOutput <= 0 {
		return nil, errors.New("subprocess output limit must be positive")
	}
	executable, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = cleanEnvironment(options.ExtraEnv)
	command.Dir = options.Dir
	command.WaitDelay = 2 * time.Second
	configureCommand(command)
	command.Cancel = func() error { return killCommand(command) }

	output := &limitedBuffer{maximum: options.MaxOutput}
	command.Stdout = output
	command.Stderr = output
	runErr := command.Run()
	if output.exceeded() {
		runErr = errors.Join(runErr, ErrOutputLimit)
	}
	return output.bytes(), runErr
}

func cleanEnvironment(extra []string) []string {
	result := make([]string, 0, 8+len(extra))
	for _, key := range []string{"PATH", "LANG", "LC_ALL", "TZ", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok && !strings.ContainsRune(value, '\x00') {
			result = append(result, key+"="+value)
		}
	}
	for _, item := range extra {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" || strings.ContainsAny(key, "\x00=") || strings.ContainsRune(value, '\x00') {
			continue
		}
		result = append(result, key+"="+value)
	}
	return result
}

type limitedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	maximum   int64
	truncated bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(value)
	remaining := b.maximum - int64(b.buffer.Len())
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if int64(len(value)) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(value)
	return original, nil
}

func (b *limitedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *limitedBuffer) exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func killCommand(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return os.ErrProcessDone
	}
	if err := killProcessTree(command.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill subprocess: %w", err)
	}
	return nil
}
