//go:build !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi loader.ts:256-271 removes a subscription only from future dispatch snapshots. The runner's already selected callback still runs; a subscription added during dispatch starts with the next event.
func TestNodeEventSubscriptionChangesApplyToNextSnapshot(t *testing.T) {
	nodeCellRequireNode(t)
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			dir := t.TempDir()
			entry, trace := filepath.Join(dir, "subscriptions.mjs"), filepath.Join(dir, "trace")
			source := fmt.Sprintf(`import { appendFileSync } from "node:fs";
export default function(pi) {
  let added = false;
  pi.on("session_start", () => {
    appendFileSync(%q, "first\n");
    off();
    if (!added) {
      added = true;
      pi.on("session_start", () => appendFileSync(%q, "third\n"));
    }
  });
  const off = pi.on("session_start", () => appendFileSync(%q, "second\n"));
}`, trace, trace, trace)
			if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "subscriptions", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v, %v", loaded, errs)
			}
			runner := inproc.NewRunner(loaded, dir)
			for range 2 {
				if _, err := runner.Emit(t.Context(), extension.SessionStartEvent{Type: "session_start"}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "first\nsecond\nfirst\nthird\n" {
				t.Fatalf("subscription changes altered the wrong snapshot: %q", got)
			}
		})
	}
}
