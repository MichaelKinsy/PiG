//go:build !darwin

package testenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFakePgrep(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pgrep")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pgrep: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestLiveSessionProcessesReportsPgrepDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake pgrep shell script requires a POSIX shell")
	}
	fakePath := writeFakePgrep(t, `#!/bin/sh
if [ "$1" = "-V" ]; then
	echo "pgrep from fake-procps 9.9.9"
	exit 0
fi
echo "pgrep: invalid session id syntax" >&2
exit 2
`)
	_, err := LiveSessionProcesses(8290)
	if err == nil {
		t.Fatal("LiveSessionProcesses returned nil error for exit status 2, want error")
	}
	msg := err.Error()
	for _, want := range []string{
		"exit status 2",
		"pgrep: invalid session id syntax",
		fakePath,
		"pgrep from fake-procps 9.9.9",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}

func TestLiveSessionProcessesExitOneIsNoProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake pgrep shell script requires a POSIX shell")
	}
	writeFakePgrep(t, `#!/bin/sh
exit 1
`)
	pids, err := LiveSessionProcesses(8290)
	if err != nil {
		t.Fatalf("LiveSessionProcesses returned error for exit status 1: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("LiveSessionProcesses returned %v for exit status 1, want empty", pids)
	}
}
