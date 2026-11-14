//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Hosts that each hold a process whose output pipe never closes delay the exit of pig for ONE bound in all, not one bound per host, and the dropped-output line comes once with the total count.
func TestTerminateProcessesOfSeveralHostsSharesOneBound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sleep")
	}
	collect := swapStd(t)
	const bound = 600 * time.Millisecond
	const hostCount = 3
	var hosts []*Host
	for i := range hostCount {
		h := NewHost(t.TempDir())
		h.waitDelay = bound
		t.Cleanup(func() { h.Shutdown("test complete") })
		source := filepath.Join(t.TempDir(), "holder.mjs")
		body := `import { spawn } from "node:child_process";
export default function(pi) {
  spawn("sleep", ["6"], { detached: true, stdio: ["ignore", "inherit", "inherit"] }).unref();
  setInterval(() => {}, 60000);
}`
		if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Load(testbudget.Context(t), ExtConfig{Name: "holder", Source: source, Enabled: true}); err != nil {
			t.Fatalf("host %d: %v", i, err)
		}
		hosts = append(hosts, h)
	}
	start := time.Now()
	for _, h := range hosts {
		h.TerminateProcesses()
	}
	elapsed := time.Since(start)
	if elapsed > 2*bound {
		t.Errorf("ending %d hosts took %v, want about one bound of %v", hostCount, elapsed, bound)
	}
	_, stderr := collect()
	if got := strings.Count(stderr, "did not finish ending"); got != 1 {
		t.Errorf("dropped-output lines = %d, want 1; stderr = %q", got, stderr)
	}
	if want := "pig: 3 extension process(es) did not finish ending within 600ms;"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}
