package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

type boundaryNotifyUI struct{ extension.UIContext }

// upstream: packages/coding-agent/src/core/agent-session.ts:2075-2084 records abort during before-settle without cancelling the awaited hook. Its result is still committed.
func TestUpstreamSessionBoundariesNodeAbort(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	source, err := filepath.Abs(filepath.Join("..", "test", "parity", "testdata", "session-boundary-abort-extension.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("aborted boundary Session complete") })
	started := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&boundaryNotifyUI{extension.NoopUIContext})
	bridge.SetNotifyFunc(func(message, _ string) {
		if message == "boundary hook started" {
			markStarted()
		}
	})
	host.SetUIBridge(bridge)
	loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "session-boundary-abort-extension", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	h := newBoundaryHarness(t, harnessOptions{extension: *loaded}, boundaryReply("first", ai.StopReasonStop, 0), boundaryReply("must not run", ai.StopReasonStop, 0))
	diagnostics := []string{}
	h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) { diagnostics = append(diagnostics, err.Error) })
	prompt := make(chan error, 1)
	go func() { err := h.session.Prompt(t.Context(), "start", nil); prompt <- err }()
	<-started
	h.session.RequestAbort()
	abort := make(chan error, 1)
	go func() { abort <- h.session.Abort(t.Context()) }()
	if err := h.session.Prompt(t.Context(), "/release-boundary", nil); err != nil {
		t.Error(err)
	}
	if err := <-prompt; err != nil {
		t.Error(err)
	}
	if err := <-abort; err != nil {
		t.Error(err)
	}
	if err := h.session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	entry := boundaryEntry(t, h, "custom_message", "committed-after-abort")
	if entry["content"] != "runnable draft after abort" || entry["display"] != false || h.provider.callCount() != 1 || len(diagnostics) != 0 {
		t.Fatalf("calls=%d entry=%v diagnostics=%v", h.provider.callCount(), entry, diagnostics)
	}
	if os.Getenv("PIG_BOUNDARY_PROBE") == "1" {
		fmt.Println("SESSION_BOUNDARY_BRIDGE " + boundaryJSON(t, []any{h.provider.callCount(), diagnostics, entry["content"], entry["display"]}))
	}
}

// The same factory runs inside published Pi and the production Node subprocess. A failed first handler's mutated draft must reach the second preview, persistence and the next Provider request.
func TestUpstreamSessionBoundariesNode(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	source, err := filepath.Abs(filepath.Join("..", "test", "parity", "testdata", "session-boundary-extension.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("boundary Session complete") })
	loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "session-boundary-extension", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	requests := []string{}
	h := newBoundaryHarness(t, harnessOptions{extension: *loaded}, boundaryReply("first", ai.StopReasonStop, 0), boundaryCapture(t, &requests, "second"))
	diagnostics := []string{}
	h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) { diagnostics = append(diagnostics, err.Error) })
	boundaryPrompt(t, h, "start")
	if h.provider.callCount() != 2 || len(requests) != 1 || !strings.Contains(requests[0], "context from real extension") {
		t.Fatalf("requests=%v calls=%d", requests, h.provider.callCount())
	}
	if !reflect.DeepEqual(diagnostics, []string{"retained boundary error"}) {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	entry := boundaryEntry(t, h, "custom_message", "bridge-boundary")
	if entry["content"] != "context from real extension" || entry["display"] != false || !reflect.DeepEqual(entry["details"], map[string]any{"source": "boundary-probe"}) {
		t.Fatalf("entry=%v", entry)
	}
	if os.Getenv("PIG_BOUNDARY_PROBE") == "1" {
		raw, err := json.Marshal([]any{h.provider.callCount(), diagnostics, entry["content"], entry["display"], entry["details"]})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("SESSION_BOUNDARY_BRIDGE " + string(raw))
	}
}
