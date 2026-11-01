//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's sendCustomMessage with triggerTurn marks the Session busy (_startRun sets _isAgentRunActive before its first await), and
// Pi's one event loop runs nothing else until the handler awaits, so ctx.isIdle() on the handler's next statement is false
// even when the run it started would already have ended. The host runs that run concurrently with the Node process: the host
// below answers idle at every read, as one whose run ended while the read was in flight does. After the handler's first await
// a read is live again (Pi's loop may have run the run's continuations by then).
func TestNodeIsIdleStaysBusyThroughTheSynchronousRunAfterATriggeringSendMessage(t *testing.T) {
	for _, isolation := range []string{"isolated", ""} {
		name := isolation
		if name == "" {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) { testNodeIsIdleStaysBusyThroughTheSynchronousRunAfterATriggeringSendMessage(t, isolation) })
	}
}

func testNodeIsIdleStaysBusyThroughTheSynchronousRunAfterATriggeringSendMessage(t *testing.T, isolation string) {
	t.Helper()
	root := t.TempDir()
	result := filepath.Join(root, "result.json")
	source := filepath.Join(root, "send-idle.mjs")
	script := fmt.Sprintf(`
import { writeFileSync } from "node:fs";
export default function(pi) {
  pi.on("session_start", async (_event, ctx) => {
    pi.sendMessage({ customType: "note", content: "go", display: true }, { triggerTurn: true });
    const sync = ctx.isIdle();
    await Promise.resolve();
    const later = ctx.isIdle();
    writeFileSync(%q, JSON.stringify({ sync, later }));
  });
}
`, filepath.ToSlash(result))
	if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newTestHost(t)
	h.SetMode("print")
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("isIdle", func() bool { return true })
	bridge.SetHostAction("sendMessage", func(extension.CustomMessageRef, SendMessageOptions) error { return nil })
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")
	configs := []ExtConfig{{Name: "send-idle", Source: source, Enabled: true, Isolation: isolation}}
	if isolation == "" {
		neighbor := filepath.Join(root, "neighbor.mjs")
		if err := os.WriteFile(neighbor, []byte(`export default function(pi) { pi.on("session_start", () => {}); }`), 0o600); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: "neighbor", Source: neighbor, Enabled: true})
	}
	loaded, errs := h.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("LoadAll = %d loaded, %v; want %d", len(loaded), errs, len(configs))
	}
	var ext extension.Extension
	for _, candidate := range loaded {
		if candidate.Name == "send-idle" {
			ext = candidate
		}
	}
	if ext.Name != "send-idle" {
		t.Fatal("send-idle extension was not loaded")
	}
	if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start"}, t.Context()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sync  bool `json:"sync"`
		Later bool `json:"later"`
	}
	pollUntil(t, 15*time.Second, "the handler did not record its reads", func() bool {
		data, err := os.ReadFile(result)
		return err == nil && json.Unmarshal(data, &got) == nil
	})
	if got.Sync {
		t.Fatal("ctx.isIdle() right after a triggering pi.sendMessage = true, want false: the run it started cannot have ended before the handler's next statement")
	}
	if !got.Later {
		t.Fatal("ctx.isIdle() after the handler's first await = false, want the live host's answer (true)")
	}
}
