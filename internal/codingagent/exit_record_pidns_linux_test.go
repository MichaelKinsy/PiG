package codingagent

import (
	"os"
	"testing"
	"time"
)

// A session in another PID namespace on the same host shares the host name; its pid still means nothing here.
func TestReconcileLeavesAMarkerOfAnotherPIDNamespaceAlone(t *testing.T) {
	hostname, _ := os.Hostname()
	ns := pidNamespace()
	if ns == "" {
		t.Skip("no PID namespace link")
	}
	pid := deadPID(t)
	agentDir := t.TempDir()
	marker := writeTestMarker(t, agentDir, pid, map[string]any{"pid": pid, "ppid": 1, "release": "9.9.9", "cwd": "/work", "started": "2026-01-01T00:00:00Z", "host": hostname, "pidns": ns + "-other"})
	ReconcileSessionMarkers(agentDir, time.Now())
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker of another PID namespace was removed: %v", err)
	}
	if text := exitLogText(agentDir); text != "" {
		t.Fatalf("exit.log = %q", text)
	}
}
