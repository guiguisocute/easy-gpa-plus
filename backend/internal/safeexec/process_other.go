//go:build !linux

package safeexec

import (
	"os"
	"os/exec"
)

func configureCommand(_ *exec.Cmd) {}

func killProcessTree(process *os.Process) error {
	return process.Kill()
}
