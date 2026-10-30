//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Pi's runner.ts:createContext reads the Session's current isIdle value on every
// call. A socket callback can run after agent_end without another subscribed
// event updating the extension's state, as pi-intercom's inbound handler does.
func TestNodeBackgroundIsIdleReadsTheHostLive(t *testing.T) {
	for _, isolation := range []string{"isolated", ""} {
		name := isolation
		if name == "" {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) { testNodeBackgroundIsIdleReadsTheHostLive(t, isolation) })
	}
}

func testNodeBackgroundIsIdleReadsTheHostLive(t *testing.T, isolation string) {
	t.Helper()
	root := t.TempDir()
	ready := filepath.Join(root, "ready.json")
	source := filepath.Join(root, "idle.mjs")
	script := fmt.Sprintf(`
import net from "node:net";
import { writeFileSync } from "node:fs";
export default function(pi) {
  let server;
  pi.on("session_start", (_event, ctx) => {
    server = net.createServer(socket => {
      socket.end(JSON.stringify({idle: ctx.isIdle()}) + "\n");
    });
    server.listen(0, "127.0.0.1", () => writeFileSync(%q, JSON.stringify(server.address())));
  });
  pi.on("agent_end", () => {});
  pi.on("session_shutdown", () => { server?.close(); });
}
`, filepath.ToSlash(ready))
	if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	var idle atomic.Bool
	idle.Store(true)
	h := newTestHost(t)
	h.SetMode("tui")
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("isIdle", idle.Load)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")
	configs := []ExtConfig{{Name: "background-idle", Source: source, Enabled: true, Isolation: isolation}}
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
	ext := loaded[0]
	for _, candidate := range loaded {
		if candidate.Name == "background-idle" {
			ext = candidate
			break
		}
	}
	if ext.Name != "background-idle" {
		t.Fatal("background-idle extension was not loaded")
	}
	if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start"}, t.Context()); err != nil {
		t.Fatal(err)
	}
	var address struct {
		Port int `json:"port"`
	}
	pollUntil(t, 15*time.Second, "socket callback did not start", func() bool {
		data, err := os.ReadFile(ready)
		return err == nil && json.Unmarshal(data, &address) == nil && address.Port != 0
	})
	probe := func(want bool) {
		t.Helper()
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(address.Port)), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var result struct {
			Idle bool `json:"idle"`
		}
		if err := json.NewDecoder(conn).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.Idle != want {
			t.Fatalf("background ctx.isIdle() = %v, live host = %v", result.Idle, want)
		}
	}
	probe(true)
	idle.Store(false)
	probe(false)
	if _, err := ext.Handlers["agent_end"][0](map[string]any{"type": "agent_end"}, t.Context()); err != nil {
		t.Fatal(err)
	}
	probe(false)
	// Settlement changes the Host without a request to this extension: it does
	// not subscribe to agent_settled, and no state broadcast precedes the I/O.
	idle.Store(true)
	probe(true)
	// A later busy transition must not be hidden by a cached idle snapshot either.
	idle.Store(false)
	probe(false)
}
