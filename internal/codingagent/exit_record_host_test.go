package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// deadPID is the pid of a process that has ended, so processAlive reports it dead here.
func deadPID(t *testing.T) int {
	t.Helper()
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	return dead.Process.Pid
}

func writeTestMarker(t *testing.T, agentDir string, pid int, header map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(CrashOutputDir(agentDir), 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(CrashOutputDir(agentDir), fmt.Sprintf("%d.log", pid))
	if err := os.WriteFile(marker, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return marker
}

// An agent directory on a shared home or a container bind mount holds markers of sessions on other hosts. Their pids are
// not processes of this host, so a pid that is dead here says nothing: the marker stays, and no exit line is written.
func TestReconcileLeavesAMarkerOfAnotherHostAlone(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	pid := deadPID(t)
	for _, tc := range []struct {
		name       string
		header     map[string]any
		reconciled bool
	}{
		{"another host", map[string]any{"pid": pid, "ppid": 1, "release": "9.9.9", "cwd": "/work", "started": "2026-01-01T00:00:00Z", "host": "node-17-" + hostname}, false},
		{"this host", map[string]any{"pid": pid, "ppid": 1, "release": "9.9.9", "cwd": "/work", "started": "2026-01-01T00:00:00Z", "host": hostname}, true},
		// A marker written before the header named a host is judged by its pid, as it was.
		{"no host", map[string]any{"pid": pid, "ppid": 1, "release": "9.9.9", "cwd": "/work", "started": "2026-01-01T00:00:00Z"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			marker := writeTestMarker(t, agentDir, pid, tc.header)
			ReconcileSessionMarkers(agentDir, time.Now())
			_, statErr := os.Stat(marker)
			text := exitLogText(agentDir)
			if tc.reconciled {
				if !os.IsNotExist(statErr) || !strings.Contains(text, "ended without recording an exit") {
					t.Fatalf("marker stat = %v, exit.log = %q; want the marker reconciled", statErr, text)
				}
				return
			}
			if statErr != nil || text != "" {
				t.Fatalf("marker stat = %v, exit.log = %q; want the foreign marker kept and no line", statErr, text)
			}
			if entries, _ := filepath.Glob(marker + ".claimed-*"); len(entries) != 0 {
				t.Fatalf("a foreign marker was claimed: %v", entries)
			}
		})
	}
}
