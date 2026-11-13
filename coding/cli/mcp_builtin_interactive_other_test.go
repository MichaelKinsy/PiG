//go:build !linux

package cli

import (
	"os/exec"
	"strings"
	"testing"
)

func driveInteractivePrompts(t *testing.T, _ *exec.Cmd, _ []string, _ func() int, _ int, _ *strings.Builder) {
	t.Helper()
	t.Skip("the interactive MCP test drives a Linux pseudo-terminal")
}

// interactiveStep is one line typed into an interactive session; see the Linux driver.
type interactiveStep struct {
	input   string
	before  func()
	done    func() bool
	pending func() string
	shows   string
}

func driveInteractiveSteps(t *testing.T, _ *exec.Cmd, _ []interactiveStep, _ *strings.Builder) {
	t.Helper()
	t.Skip("the interactive tests drive a Linux pseudo-terminal")
}
