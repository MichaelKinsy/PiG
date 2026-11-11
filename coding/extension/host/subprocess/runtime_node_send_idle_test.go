//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's sendCustomMessage with triggerTurn marks the Session busy (_startRun sets _isAgentRunActive before its first await), and
// Pi's one event loop runs nothing else until the handler awaits, so ctx.isIdle() on the handler's next statements is false in every
// extension even when the run it started would already have ended. The host runs that run concurrently with the Node process: the
// host below answers idle at every read, as one whose run ended while the read was in flight does, so only the host's hold
// (Host.syncRuns) makes a read false. A synchronous call that Pi does not await (setSessionName) keeps the hold; after the handler's
// first await a read is live again, because the Node runtime reports the microtask checkpoint (NotifyHandlerYielded). With the sender
// in a process of its own, the handler stops in a host call and another extension's read in its own process is busy too: the hold
// is the Session's, not the sender's.
func TestNodeIsIdleStaysBusyThroughTheSynchronousRunAfterATriggeringSendMessage(t *testing.T) {
	for _, isolation := range []string{"isolated", ""} {
		name := isolation
		if name == "" {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) {
			testNodeIsIdleStaysBusyThroughTheSynchronousRunAfterATriggeringSendMessage(t, isolation)
		})
	}
}

func testNodeIsIdleStaysBusyThroughTheSynchronousRunAfterATriggeringSendMessage(t *testing.T, isolation string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "send-idle.mjs")
	script := `
export default function(pi) {
  pi.on("session_start", async (_event, ctx) => {
    pi.sendMessage({ customType: "note", content: "go", display: true }, { triggerTurn: true });
    const sync = ctx.isIdle();
    pi.setSessionName("barrier");
    const stillSync = ctx.isIdle();
    await Promise.resolve();
    const later = ctx.isIdle();
    pi.setSessionName(JSON.stringify({ sync, stillSync, later }));
  });
}
`
	if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	neighborSource := filepath.Join(root, "neighbor.mjs")
	neighborScript := `export default function(pi) { pi.on("agent_start", (_event, ctx) => { pi.setSessionName("neighbor:" + ctx.isIdle()); }); }`
	if err := os.WriteFile(neighborSource, []byte(neighborScript), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.Context(t)
	atBarrier, release, names := make(chan struct{}), make(chan struct{}), make(chan string, 4)
	h := newTestHost(t)
	h.SetMode("print")
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("isIdle", func() bool { return true })
	bridge.SetHostAction("sendMessage", func(extension.CustomMessageRef, SendMessageOptions) error { return nil })
	bridge.SetHostAction("setSessionName", func(name string) error {
		if name == "barrier" {
			close(atBarrier)
			<-release
			return nil
		}
		names <- name
		return nil
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")
	var releaseOnce sync.Once
	releaseHolder := func() { releaseOnce.Do(func() { close(release) }) }
	// A failing assertion must not leave the handler blocked in its host call, which Shutdown would wait for.
	defer releaseHolder()
	configs := []ExtConfig{
		{Name: "send-idle", Source: source, Enabled: true, Isolation: isolation},
		{Name: "neighbor", Source: neighborSource, Enabled: true},
	}
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("LoadAll = %d loaded, %v; want %d", len(loaded), errs, len(configs))
	}
	exts := map[string]extension.Extension{}
	for _, candidate := range loaded {
		exts[candidate.Name] = candidate
	}
	handled := make(chan error, 1)
	go func() {
		_, err := exts["send-idle"].Handlers["session_start"][0](map[string]any{"type": "session_start"}, ctx)
		handled <- err
	}()
	select {
	case <-atBarrier:
	case <-ctx.Done():
		t.Fatal("the handler never reached its synchronous host call")
	}
	if isolation != "" {
		// The sender's thread waits in the host call, in a process of its own: this extension's read runs now, in its process.
		if _, err := exts["neighbor"].Handlers["agent_start"][0](map[string]any{"type": "agent_start"}, ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-names:
			if got != "neighbor:false" {
				t.Fatalf("another extension read %q while the sender's handler had not yielded, want neighbor:false", got)
			}
		case <-ctx.Done():
			t.Fatal("the neighbor never reported its read")
		}
	}
	releaseHolder()
	var got struct {
		Sync      bool `json:"sync"`
		StillSync bool `json:"stillSync"`
		Later     bool `json:"later"`
	}
	select {
	case name := <-names:
		if err := json.Unmarshal([]byte(name), &got); err != nil {
			t.Fatalf("the handler reported %q: %v", name, err)
		}
	case <-ctx.Done():
		t.Fatal("the handler never reported its reads")
	}
	if err := <-handled; err != nil {
		t.Fatal(err)
	}
	if got.Sync || got.StillSync {
		t.Fatalf("ctx.isIdle() right after a triggering pi.sendMessage = %+v, want false before the handler awaits: the run it started cannot have ended before the handler's next statement", got)
	}
	if !got.Later {
		t.Fatal("ctx.isIdle() after the handler's first await = false, want the live host's answer (true)")
	}
}
