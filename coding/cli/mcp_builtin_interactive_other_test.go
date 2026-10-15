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
