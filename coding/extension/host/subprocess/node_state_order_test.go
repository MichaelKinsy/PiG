package subprocess

import (
	"encoding/json"
	"os"
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
//   - nestedHanded, nestedQueued: the setter's event starts inside the setter while the loop holds a request (handed to it, or taken from its queue and not
//     started), as the host waits for it to start before it answers the setter, and the request the loop held still runs after the setter returns.
//   - staleHandedOff: the push the loop read is not applied again by a later synchronous call of a handler that pushes no state (the loop clears handedOff).
//   - replaced: a log is replaced by a page with a lower count that holds the whole log, the page the host sends when a cursor is out of range
//     (Session.RawEntriesPage), and the following pages extend it. A guard against lower-count pages would break that replacement, and Pi's own log has no stale page to guard against.
//   - beforeReady: a state the host sends before the ready is applied, not dropped, and the load goes on; a handler reads it after a ready that carries no state.
//
// The probe orders its steps by what each observed (a gate a step opens for the next), and no timer's length decides one. A process the host starves of CPU runs every
// timer late, and a fixed wait inverted then: limbo reported a handler b that had not read yet, under load, in 7 of 960 runs of this test at 16 in parallel. The second
// run blocks the probe's event loop for 100 ms before every timer callback (testdata/node-timer-stall.mjs), which inverted the old waits every time.
func TestNodeAppliesHostStateInFrameOrder(t *testing.T) {
	nodeCellRequireNode(t)
	runtime, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	stallPath, err := filepath.Abs("testdata/node-timer-stall.mjs")
	if err != nil {
		t.Fatal(err)
	}
	// --import takes a module specifier, not a platform path: on Windows Node reads D:\... as URL scheme "d:".
	stallURL, err := nodeFileURL(stallPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []struct {
		name string
		args []string
		env  []string
	}{
		{"plain", nil, nil},
		{"every_timer_callback_late", []string{"--import", stallURL}, []string{"PIG_TIMER_STALL_MS=100"}},
	} {
		t.Run(run.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "node", append(run.args, "testdata/node-state-order-probe.mjs", runtime)...)
			cmd.Env = append(os.Environ(), run.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("probe: %v\n%s", err, out)
			}
			checkNodeStateOrder(t, out)
		})
	}
}

func checkNodeStateOrder(t *testing.T, out []byte) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	want := map[string]any{
		"limbo":          map[string]any{"name": "renamed", "ids": "e1,e2,e3,e4,e5", "host": "renamed e1,e2,e3,e4,e5"},
		"limboNoLoaded":  map[string]any{"name": "renamed", "ids": "e1,e2,e3,e4,e5", "host": "renamed e1,e2,e3,e4,e5"},
		"nested":         map[string]any{"afterWrite": "nested-name e1,e2,e3", "inEvent": "nested-name e1,e2,e3"},
		"replaced":       "e1,e2,e3 n1,n2 n1,n2,n3",
		"staleHandedOff": "e1,e2,e3",
		"nestedHanded":   "started ev | setter returned | c ran",
		"nestedQueued":   "started ev | setter returned | c ran",
		"beforeReady":    "named before ready",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state order\n got %s\nwant %v", out, want)
	}
}
