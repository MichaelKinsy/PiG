package subprocess

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi's old extension instance keeps running while its command awaits `ctx.reload()`, because everything is one process (agent-session.ts:3575-3625). A generation with a command running is therefore stopped when that command returns, not when the reload commits.
func TestCommitStagedKeepsAGenerationRunningWhileItsCommandRuns(t *testing.T) {
	h := NewHost(t.TempDir())
	var mu sync.Mutex
	var unregistered []string
	stopped := make(chan string, 4)
	h.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(name string) {
		mu.Lock()
		unregistered = append(unregistered, name)
		mu.Unlock()
		stopped <- name
	})
	busy := &managedExt{config: ExtConfig{Name: "busy"}, providerNames: []string{"old-busy"}}
	idle := &managedExt{config: ExtConfig{Name: "idle"}, providerNames: []string{"old-idle"}}
	h.exts["busy"], h.exts["idle"] = busy, idle
	busy.commands.begin()

	h.commitStaged([]stagedManagedExt{
		{name: "busy", me: &managedExt{config: ExtConfig{Name: "busy"}}},
		{name: "idle", me: &managedExt{config: ExtConfig{Name: "idle"}}},
	}, nil, "reload replaced")
	if got := <-stopped; got != "old-idle" {
		t.Fatalf("first stopped generation = %q, want the idle one", got)
	}
	select {
	case got := <-stopped:
		t.Fatalf("%q stopped while its command was still running", got)
	case <-time.After(50 * time.Millisecond):
	}
	if !busy.shuttingDown.Load() {
		t.Fatal("the replaced generation must stop taking new work at once")
	}
	busy.commands.end()
	select {
	case got := <-stopped:
		if got != "old-busy" {
			t.Fatalf("stopped generation = %q, want old-busy", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the generation was not stopped after its command returned")
	}
	h.Shutdown("test complete")
}

// Shutdown does not wait for a command that never returns: it stops the generations a command deferred, and a command that returns afterwards stops nothing twice.
func TestShutdownStopsGenerationsADeferredRetirementStillHolds(t *testing.T) {
	h := NewHost(t.TempDir())
	stops := 0
	var mu sync.Mutex
	h.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(string) {
		mu.Lock()
		stops++
		mu.Unlock()
	})
	old := &managedExt{config: ExtConfig{Name: "busy"}, providerNames: []string{"old-busy"}}
	h.exts["busy"] = old
	old.commands.begin()
	h.commitStaged([]stagedManagedExt{{name: "busy", me: &managedExt{config: ExtConfig{Name: "busy"}}}}, nil, "reload replaced")
	h.Shutdown("test complete")
	old.commands.end()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if stops != 1 {
		t.Fatalf("provider unregistered %d times, want exactly once", stops)
	}
}

func TestCommandGateRunsItsCallbackOnTheLastReturn(t *testing.T) {
	var gate commandGate
	ran := 0
	if gate.afterIdle(func() { ran++ }) {
		t.Fatal("an idle gate must report that the caller acts itself")
	}
	gate.begin()
	gate.begin()
	if !gate.afterIdle(func() { ran++ }) {
		t.Fatal("a busy gate must take the callback")
	}
	gate.end()
	if ran != 0 {
		t.Fatalf("callback ran after the first of two commands returned")
	}
	gate.end()
	if ran != 1 {
		t.Fatalf("callback ran %d times after the last command returned, want 1", ran)
	}
}

// Shutdown runs the pending deferred stops before it takes transitionMu, so a reload that holds transitionMu can still commit afterwards. A generation it replaces while a command runs must stop at once: no later task runs a stop registered after Shutdown collected them.
func TestRetirementAfterShutdownCollectedThePendingStopsStopsAtOnce(t *testing.T) {
	h := NewHost(t.TempDir())
	stopped := make(chan string, 1)
	h.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(name string) { stopped <- name })
	old := &managedExt{config: ExtConfig{Name: "busy"}, providerNames: []string{"old-busy"}}
	h.exts["busy"] = old
	old.commands.begin()
	h.finishRetirements()
	h.commitStaged([]stagedManagedExt{{name: "busy", me: &managedExt{config: ExtConfig{Name: "busy"}}}}, nil, "reload replaced")
	old.commands.end()
	select {
	case got := <-stopped:
		if got != "old-busy" {
			t.Fatalf("stopped generation = %q, want old-busy", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a generation replaced after Shutdown collected the pending stops was never stopped")
	}
	h.Shutdown("test complete")
}

// After `await ctx.reload()` the old instance's captured pi and ctx are stale (runner.ts invalidate, agent-session.ts:3580): every call throws Pi's stale message. A replaced generation whose command still runs therefore gets that error for each host call. It must neither act on the live Session nor go unanswered: a dropped synchronous call (a Node generation the reload superseded) blocks the command forever, and with it the deferred stop and the mode's exit.
func TestReplacedGenerationCallsFailWithThePiStaleMessage(t *testing.T) {
	for _, node := range []bool{false, true} {
		name := "isolated"
		if node {
			name = "node"
		}
		t.Run(name, func(t *testing.T) {
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			bridge := NewUIBridge(func() {})
			bridge.SetActions(&HostCallbacks{GetActiveTools: func() []string { return []string{"live"} }})
			h.SetUIBridge(bridge)
			hostEnd, peer := net.Pipe()
			defer func() { _ = peer.Close() }()
			conn := NewConn("busy", hostEnd)
			conn.Start(t.Context())
			defer func() { _ = conn.Close("test done") }()
			old := withConn(&managedExt{config: ExtConfig{Name: "busy"}, host: h}, conn)
			if node {
				old.packedProcess = &packedProcessState{node: true, key: "cell", generation: 1}
				h.packedCellGeneration = map[string]int{"cell": 2}
			}
			h.exts["busy"] = old
			old.commands.begin()
			defer old.commands.end()
			h.commitStaged([]stagedManagedExt{{name: "busy", me: &managedExt{config: ExtConfig{Name: "busy"}}}}, nil, "reload replaced")

			go h.runCall(old, conn, "c1", &CallPayload{Method: "getActiveTools"}, nil)
			env := readFramed(t, peer)
			if env.Type != MsgCallResult || env.ID != "c1" || env.CallResult == nil || env.CallResult.Error == nil {
				t.Fatalf("result = %+v, want an error result for c1", env)
			}
			if got, want := env.CallResult.Error.Message, (&inproc.StaleError{}).Error(); got != want {
				t.Fatalf("error = %q, want Pi's stale message %q", got, want)
			}
			// The deferred stop sends its shutdown frame over the pipe when the command ends.
			go func() { _, _ = io.Copy(io.Discard, peer) }()
		})
	}
}
