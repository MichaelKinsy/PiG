package subprocess

import (
	"net"
	"slices"
	"testing"
	"time"
)

// reload_started and runtime_input_end drive waits only a Node runtime has: its in-place wait for the host's next step after a print-mode prompt (runtime.mjs waitsForHostStep) and its command-suspension and loop-drain reports after stdin's end (runtime.mjs releaseCommandSuspensions). The Go, Python and Rust SDKs have neither, and the host applies stdin's end to their commands itself (FlushCommands), so the host sends both only to a Node runtime.
func TestNodeOnlyNotificationsReachOnlyNodeRuntimes(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	peers := map[string]*Conn{}
	for _, runtime := range []struct {
		name string
		node bool
	}{{"node", true}, {"native", false}} {
		hostEnd, peerEnd := net.Pipe()
		t.Cleanup(func() { _ = hostEnd.Close(); _ = peerEnd.Close() })
		conn := NewConn(runtime.name, hostEnd)
		conn.Start(t.Context())
		peer := NewConn(runtime.name+"-peer", peerEnd)
		peer.Start(t.Context())
		peers[runtime.name] = peer
		me := withConn(&managedExt{config: ExtConfig{Name: runtime.name}, host: h, nodeRealm: runtime.node}, conn)
		h.mu.Lock()
		h.exts[runtime.name] = me
		h.mu.Unlock()
	}
	h.notifyReloadStarted()
	h.EndInput()
	h.mu.Lock()
	conns := map[string]*Conn{"node": h.exts["node"].connection(), "native": h.exts["native"].connection()}
	h.mu.Unlock()
	want := map[string][]string{
		"node":   {NotifyReloadStarted, "runtime_input_end", "sentinel"},
		"native": {"sentinel"},
	}
	for name, conn := range conns {
		if err := conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "sentinel"}}); err != nil {
			t.Fatal(err)
		}
		var got []string
		for !slices.Contains(got, "sentinel") {
			select {
			case env := <-peers[name].Incoming():
				if env.Type == MsgNotify && env.Notify != nil {
					got = append(got, env.Notify.Method)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s runtime: no sentinel after %v", name, got)
			}
		}
		if !slices.Equal(got, want[name]) {
			t.Errorf("%s runtime received %v, want %v", name, got, want[name])
		}
	}
}

// event_loop_turn asks a Node process for one turn of its own event loop before a tool's result (runtime.mjs); the Go, Python and Rust SDKs have no such loop and neither dispatch on nor name the request, so the host offers Extension.EventLoopTurn, and with it the request, only for a Node runtime.
func TestEventLoopTurnIsOfferedOnlyForNodeRuntimes(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	for _, runtime := range []struct {
		name string
		node bool
	}{{"node", true}, {"native", false}} {
		ext := h.buildExtension(&managedExt{config: ExtConfig{Name: runtime.name}, host: h, nodeRuntime: runtime.node}, &RegisterPayload{})
		if got := ext.EventLoopTurn != nil; got != runtime.node {
			t.Errorf("%s runtime: EventLoopTurn set = %t, want %t", runtime.name, got, runtime.node)
		}
	}
}
