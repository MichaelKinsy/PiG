//go:build !windows

package testenv

import (
	"os/exec"
	"testing"
)

// needsInterpreter is false: the kernel runs a script by its #! line.
const needsInterpreter = false

func posixTool(t testing.TB, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not on PATH", name)
	}
	return path
}
