//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Crash attribution needs the runtime stack, not just factory stderr. Node's host-owned copy writer must retain its log file until process output drains.
func TestNodeRuntimeStderrLogOutlivesRegistration(t *testing.T) {
	nodeCellRequireNode(t)
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			entry := filepath.Join(t.TempDir(), "logging.mjs")
			if err := os.WriteFile(entry, []byte(`export default function(pi){pi.registerCommand("log",{handler:async()=>{console.error("runtime-stack-marker");}});}`), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "logging", Source: entry, Isolation: isolation, Enabled: true}})
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			if err := loaded[0].Commands["log"].Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			path := h.exts["logging"].stderrLogPath
			h.mu.Unlock()
			pollUntil(t, 2*time.Second, "runtime stderr was not retained for crash attribution", func() bool {
				data, err := os.ReadFile(path)
				return err == nil && strings.Contains(string(data), "runtime-stack-marker")
			})
		})
	}
}
