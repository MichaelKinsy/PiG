//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi's loader constructs throwing action slots until loading finishes and the runner binds them. Admitting A before C must not make A's host actions available to C's factory through the shared bus.
func TestNodeAdmissionDoesNotBindActionsBeforeFactoriesFinish(t *testing.T) {
	nodeCellRequireNode(t)
	dir := t.TempDir()
	a, c := filepath.Join(dir, "a.mjs"), filepath.Join(dir, "c.mjs")
	if err := os.WriteFile(a, []byte(`export default function(pi){pi.events.on("loading", state=>{try{pi.getActiveTools();state.bound=true;}catch{state.bound=false;}});}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c, []byte(`export default function(pi){const state={};pi.events.emit("loading",state);if(state.bound!==false)throw new Error("actions bound before factory transaction completed");}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	if _, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "a", Source: a, Enabled: true}, {Name: "c", Source: c, Enabled: true}}); len(errs) > 0 {
		t.Fatal(errs)
	}
}
