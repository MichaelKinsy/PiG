package subprocess

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Upstream's context getters read the live Session at every call (runner.ts:906, loader.ts:411-414); the host replicates the state to each runtime and used to push it only before a request, after a setModel or appendEntry call and after a status change. A change the host makes with no call and no event to an extension reaches every connected runtime through UIBridge.HostStateChanged, and a runtime that already holds the state is sent nothing.
func TestHostStateChangedPushesStateTheHostChangedAndNothingElse(t *testing.T) {
	rig := newPushRig(t)
	rig.push(t)
	rig.nextState(t)

	rig.prompt.Store("changed by the host")
	rig.bridge.HostStateChanged()
	state := rig.nextState(t)
	if got := string(state["systemPrompt"]); got != `"changed by the host"` {
		t.Fatalf("pushed system prompt = %s", got)
	}
	for _, key := range []string{"commands", "theme", "keybindings", "terminalCapabilities"} {
		if _, present := state[key]; present {
			t.Fatalf("a push of one changed value carried %s", key)
		}
	}

	rig.bridge.HostStateChanged()
	rig.expectNoFrame(t, "a host state change report for state the runtime already holds")
}

// The owner reports a change from the goroutine that made it, which is the UI loop for a model switch, so the report never waits for the push: not for the snapshot, not for the runtime's connection. Changes reported while a push runs coalesce into one more push of the newest state.
func TestHostStateChangedNeverBlocksItsCallerAndCoalescesABurst(t *testing.T) {
	rig := newPushRig(t)
	rig.push(t)
	rig.nextState(t)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	rig.bridge.SetHostAction("getSystemPrompt", func() string {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return rig.prompt.Load().(string)
	})
	rig.prompt.Store("first change")
	reported := make(chan struct{})
	go func() {
		rig.bridge.HostStateChanged()
		close(reported)
	}()
	select {
	case <-reported:
	case <-time.After(5 * time.Second):
		t.Fatal("the report waited for the push")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the push never read the host's state")
	}
	// The push is inside the snapshot now: a burst of further changes returns at once, and none starts another push beside it.
	rig.prompt.Store("last change")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			rig.bridge.HostStateChanged()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a report waited for the push in flight")
	}
	close(release)
	if got := string(rig.nextState(t)["systemPrompt"]); got != `"last change"` {
		t.Fatalf("the push after the burst carried %s, want the newest state", got)
	}
	rig.expectNoFrame(t, "a burst of reports of one change")
}

// A runtime that stops reading delays only its own push: the connection write blocks, and every other runtime still receives the state at once.
func TestHostStateChangedStuckRuntimeDelaysOnlyItself(t *testing.T) {
	rig := newPushRig(t)
	rig.push(t)
	rig.nextState(t)

	// Nothing reads the extension's side of this pipe, so a write to it never completes.
	stuckConn, _ := startedPipeConn(t, "stuck")
	stuck := withConn(&managedExt{config: ExtConfig{Name: "stuck"}, host: rig.h}, stuckConn)
	rig.h.mu.Lock()
	rig.h.exts["stuck"] = stuck
	rig.h.mu.Unlock()

	for _, prompt := range []string{"one", "two", "three"} {
		rig.prompt.Store(prompt)
		rig.bridge.HostStateChanged()
		if got := string(rig.nextState(t)["systemPrompt"]); got != `"`+prompt+`"` {
			t.Fatalf("the healthy runtime received %s, want %q", got, prompt)
		}
	}
}

// Upstream's getMcpServers reads the runtime's registry live (loader.ts:495-497), and an extension with no mcp_servers_change handler gets no event when another extension registers or removes a server. The registry's own change reports the new list to every connected runtime, and a runtime that holds the list is sent nothing.
func TestMcpServerRegistryChangePushesTheListToEveryRuntime(t *testing.T) {
	rig := newPushRig(t)
	rig.push(t)
	rig.nextState(t)
	names := func(state map[string]json.RawMessage) string {
		var servers []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(state["mcpServers"], &servers); err != nil {
			t.Fatalf("mcpServers %s: %v", state["mcpServers"], err)
		}
		listed := make([]string, len(servers))
		for i, server := range servers {
			listed[i] = server.Name
		}
		return strings.Join(listed, ",")
	}

	runtime := rig.h.Runtime()
	if err := runtime.RegisterMcpServer("other-extension", "pushed", json.RawMessage(`{"type":"http","url":"https://mcp.example.com/pushed"}`)); err != nil {
		t.Fatal(err)
	}
	if got := names(rig.nextState(t)); got != "pushed" {
		t.Fatalf("the push after a registration listed %q, want pushed", got)
	}
	rig.bridge.HostStateChanged()
	rig.expectNoFrame(t, "a report for a list the runtime already holds")

	runtime.UnregisterMcpServer("other-extension", "pushed")
	if got := names(rig.nextState(t)); got != "" {
		t.Fatalf("the push after the removal listed %q, want none", got)
	}
}

// The registry's report ends with the Host: a registration after Shutdown pushes nothing and leaves no observer on a runtime another Host may share.
func TestMcpServerRegistryChangeAfterShutdownPushesNothing(t *testing.T) {
	rig := newPushRig(t)
	rig.push(t)
	rig.nextState(t)
	runtime := rig.h.Runtime()
	rig.h.Shutdown("done")
	if err := runtime.RegisterMcpServer("other-extension", "late", json.RawMessage(`{"type":"http","url":"https://mcp.example.com/late"}`)); err != nil {
		t.Fatal(err)
	}
	rig.expectNoFrame(t, "a registration after Shutdown")
}

// A theme the owner selects with no extension call reaches the runtime as one lean push of the palette.
func TestHostStateChangedPushesTheThemeAlone(t *testing.T) {
	rig := newPushRig(t)
	rig.push(t)
	rig.nextState(t)
	rig.theme.Store("light")
	rig.bridge.HostStateChanged()
	state := rig.nextState(t)
	var theme struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(state["theme"], &theme); err != nil || theme.Name != "light" {
		t.Fatalf("pushed theme = %s (%v), want light", state["theme"], err)
	}
	if _, present := state["commands"]; present {
		t.Fatal("a theme push carried the command catalog")
	}
}

// A report after Shutdown has joined the host's tasks starts nothing.
func TestHostStateChangedAfterShutdownStartsNothing(t *testing.T) {
	rig := newPushRig(t)
	rig.h.Shutdown("done")
	rig.prompt.Store("late")
	rig.bridge.HostStateChanged()
	rig.expectNoFrame(t, "a report after Shutdown")
}

// While a fan-out is queued a report is one atomic swap, with no allocation and no lock beyond the bridge's read lock: the UI loop pays nothing for a change that is already announced.
func TestHostStateChangedWhileAFanOutIsQueuedDoesNotAllocate(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)
	h.hostStatePushQueued.Store(true)
	if allocs := testing.AllocsPerRun(1000, bridge.HostStateChanged); allocs != 0 {
		t.Fatalf("a report while a fan-out is queued allocated %v times", allocs)
	}
}

// BenchmarkHostStateChanged is the cost the UI loop pays for a report: the first of a burst queues the fan-out, and the rest return at the atomic swap. The fan-out itself runs on the Host's task.
func BenchmarkHostStateChanged(b *testing.B) {
	h := NewHost(b.TempDir())
	b.Cleanup(func() { h.Shutdown("benchmark done") })
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)
	b.ReportAllocs()
	for b.Loop() {
		bridge.HostStateChanged()
	}
}
