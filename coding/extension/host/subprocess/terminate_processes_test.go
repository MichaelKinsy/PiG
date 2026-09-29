package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Forced RPC termination cannot leave the Node cell's timers alive after the host exits. Kill does not depend on a cooperative session_shutdown handler, in isolated or packed placements.
func TestTerminateProcessesStopsNodeRuntimes(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(map[bool]string{false: "isolated", true: "packed"}[packed], func(t *testing.T) {
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			var configs []ExtConfig
			for _, name := range []string{"one", "two"} {
				source := filepath.Join(t.TempDir(), name+".mjs")
				if err := os.WriteFile(source, []byte(`export default function(pi) { setInterval(() => {}, 60000); pi.on("session_shutdown", async () => new Promise(() => {})); }`), 0o600); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, ExtConfig{Name: name, Source: source, Enabled: true})
			}
			if packed {
				if _, errs := h.LoadAll(testbudget.Context(t), configs); len(errs) != 0 {
					t.Fatal(errs)
				}
			} else {
				for _, config := range configs {
					if _, err := h.Load(testbudget.Context(t), config); err != nil {
						t.Fatal(err)
					}
				}
			}
			h.mu.Lock()
			var stopped []<-chan struct{}
			for _, me := range h.exts {
				if me.packedProcess != nil {
					stopped = append(stopped, me.packedProcess.startWait())
				} else {
					stopped = append(stopped, me.exitedCh)
				}
			}
			h.mu.Unlock()
			if len(stopped) != len(configs) {
				t.Fatalf("loaded %d runtimes for %d sources", len(stopped), len(configs))
			}
			h.TerminateProcesses()
			ctx := testbudget.Context(t)
			for _, done := range stopped {
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("runtime survived forced termination")
				}
			}
		})
	}
}
