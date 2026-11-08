package subprocess

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// The Node runtime applies the session state the host pushes in the order the host sent it, whichever frame the receive loop holds when a handler's synchronous
// host call runs. The probe drives the real Runtime class over a fake transport (testdata/node-state-order-probe.mjs): a synchronous wait delivers every
// frame available, so the pump of one call can deliver a request and the push of another writer behind it while the loop waits.
//
//   - limbo: the loop took a queued push with next() and has not yet read it (the promise it awaits is resolved) when a request handler that began first,
//     here every handler of an extension that registers pi.onLoaded, resumes and writes. The write's push was applied before the older push, and the
//     replicated log ended e1,e2,e3,e5,e4 under the name of the older writer.
//   - nested: the session_info_changed handler that starts in a write after an await reads the state the write's push carries (Pi runs it inside the call).
//   - replaced: a log is replaced by a page with a lower count, the shape the host sends when a cursor is out of range (Session.RawEntriesPage), and the
//     following pages extend it. A guard against lower-count pages would break that replacement, and Pi's own log has no stale page to guard against.
func TestNodeAppliesHostStateInFrameOrder(t *testing.T) {
	nodeCellRequireNode(t)
	runtime, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "node", "testdata/node-state-order-probe.mjs", runtime).CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	want := map[string]any{
		"limbo":         map[string]any{"name": "renamed", "ids": "e1,e2,e3,e4,e5", "host": "renamed e1,e2,e3,e4,e5"},
		"limboNoLoaded": map[string]any{"name": "renamed", "ids": "e1,e2,e3,e4,e5", "host": "renamed e1,e2,e3,e4,e5"},
		"nested":        map[string]any{"afterWrite": "nested-name e1,e2,e3", "inEvent": "nested-name e1,e2,e3"},
		"replaced":      "e1,e2,e3 n1 n1,n2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state order\n got %s\nwant %v", out, want)
	}
}
