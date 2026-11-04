package coding

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// graphSignalWire is what the extension's getSignal function answers for a run state: nothing while no run is active, else whether the signal is aborted.
type graphSignalWire int

const (
	graphNoRun graphSignalWire = iota
	graphLiveRun
	graphAbortedRun
)

// graphSignalScope is a connection whose extension answers getSignal with the run state the test sets, and counts how many times the host asked for each state.
type graphSignalScope struct {
	mcpFakeScope
	state atomic.Int32
	reads [3]atomic.Int64
	// stormCap ends a storm: past this many reads of an aborted run the extension answers "no run", so a binding that makes every read renumber the run still lets the test finish.
	stormCap int64
	// abortedRead receives one value per read of an aborted run, so a test waits for a count of reads, not for a time.
	abortedRead chan struct{}
}

func newGraphSignalScope(stormCap int64) *graphSignalScope {
	s := &graphSignalScope{stormCap: stormCap, abortedRead: make(chan struct{}, stormCap+8)}
	s.serve = func(string, []json.RawMessage) (any, error) {
		state := graphSignalWire(s.state.Load())
		n := s.reads[state].Add(1)
		if state == graphAbortedRun {
			select {
			case s.abortedRead <- struct{}{}:
			default:
			}
			if n > s.stormCap {
				return nil, nil
			}
		}
		switch state {
		case graphLiveRun:
			return map[string]any{"aborted": false}, nil
		case graphAbortedRun:
			return map[string]any{"aborted": true}, nil
		}
		return nil, nil
	}
	return s
}

// Pi's `agent.signal` is `activeRun?.abortController.signal` (agent.ts:345-347) and `abort()` aborts that controller (agent.ts:350-352): one signal object from the start of a run, aborted in place, until the run ends and the next one begins. The binding of the extension's getSignal answers the state of the run on the wire, so it must give the host the same context object for as long as the state is the same run: one live context, which the abort cancels, and none while no run is active.
func TestGraphGetSignalIsOneObjectForARunAndItsAbort(t *testing.T) {
	t.Parallel()
	scope := newGraphSignalScope(1 << 20)
	g := &GraphExtensionRunner{scope: scope}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal
	read := func(state graphSignalWire) context.Context {
		scope.state.Store(int32(state))
		return get()
	}

	if got := read(graphNoRun); got != nil {
		t.Fatalf("no run: signal = %v, want nil", got)
	}
	first := read(graphLiveRun)
	if first == nil || first.Err() != nil {
		t.Fatalf("live run: signal = %v, want a live context", first)
	}
	if again := read(graphLiveRun); again != first {
		t.Fatal("a second read of the same live run returned another context")
	}
	if aborted := read(graphAbortedRun); aborted != first || aborted.Err() == nil {
		t.Fatalf("abort: signal = %v (same object %v), want the run's context, cancelled", aborted, aborted == first)
	}
	for range 3 {
		if again := read(graphAbortedRun); again != first {
			t.Fatal("a read of the aborted run returned another context")
		}
	}
	if got := read(graphNoRun); got != nil {
		t.Fatalf("run ended: signal = %v, want nil", got)
	}
	second := read(graphLiveRun)
	if second == nil || second == first || second.Err() != nil {
		t.Fatalf("next run: signal = %v (first run's %v), want a new live context", second, second == first)
	}
	if got := read(graphAbortedRun); got != second || got.Err() == nil {
		t.Fatalf("abort of the next run: signal = %v, want that run's context, cancelled", got)
	}
	// A run whose first read is already aborted is one context, cancelled.
	if got := read(graphNoRun); got != nil {
		t.Fatalf("run ended: signal = %v, want nil", got)
	}
	third := read(graphAbortedRun)
	if third == nil || third == first || third == second || third.Err() == nil {
		t.Fatalf("run first seen aborted: signal = %v, want a new cancelled context", third)
	}
	if again := read(graphAbortedRun); again != third {
		t.Fatal("a second read of the run first seen aborted returned another context")
	}
	// A run that follows an aborted one with no read of "no run" between is a new run, as the cancelled context cannot hold it.
	next := read(graphLiveRun)
	if next == third || next == nil || next.Err() != nil {
		t.Fatalf("live run after an aborted one: signal = %v, want a new live context", next)
	}
}

// The extension's getSignal runs on the extension's own thread, so two reads of the run can be in flight together, and the answers come back in either order. The tracker applies each answer to the one context of the run, so it must apply them in the order the reads were made: an "aborted" answer that lands before an older "live" one must not leave the live one to start a run of its own, a live context that nothing will abort.
// The test holds the first read's answer (live) inside the extension, makes a second read (aborted) and lets it finish if it can, then releases the first. With the answers applied in read order the second read waits for the first, and both reads return the run's one context, cancelled.
func TestGraphGetSignalAppliesAnswersInReadOrder(t *testing.T) {
	t.Parallel()
	scope := &graphSignalScope{}
	g := &GraphExtensionRunner{scope: scope}
	var (
		calls    atomic.Int32
		secondIn atomic.Bool
		firstIn  = make(chan struct{})
		release  = make(chan struct{})
	)
	scope.serve = func(string, []json.RawMessage) (any, error) {
		if calls.Add(1) == 1 {
			close(firstIn)
			<-release
			return map[string]any{"aborted": false}, nil
		}
		secondIn.Store(true)
		return map[string]any{"aborted": true}, nil
	}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal

	var first, second context.Context
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(firstDone); first = get() }()
	<-firstIn
	go func() { defer close(secondDone); second = get() }()
	// Wait until the second read has finished, or is held behind the first. Nothing but a read holds the tracker's lock. A tracker that holds it across the extension's answer holds it for the first read, so TryLock fails before the second read has reached the extension; one that does not lets the second read reach the extension (secondIn, set before it takes the lock) and finish.
	for {
		select {
		case <-secondDone:
		default:
			if g.signal.mu.TryLock() {
				g.signal.mu.Unlock()
			} else if !secondIn.Load() {
				break
			}
			runtime.Gosched()
			continue
		}
		break
	}
	close(release)
	<-firstDone
	<-secondDone
	if first == nil || first != second || first.Err() == nil {
		t.Fatalf("reads = %v and %v (same object %v, cancelled %v), want the one context of the run, cancelled", first, second, first == second, first != nil && first.Err() != nil)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("extension answered %d times, want 2", n)
	}
}

// A Host asks the getter again after every sync that sees a new run, and a run it sees aborted is watched with context.AfterFunc, which fires at once for a context that is already cancelled. A getter that answers an aborted run with a new context on every read therefore reads, renumbers the run and reads again without end (91k getter reads a second, one RPC to the extension each). The reads of a session-graph client's aborted run are bounded by a count, not by time: the sync that sees the abort and the watcher's sync read it, then one more read for the request the test makes afterwards; a storm goes on reading while the request is served.
func TestGraphAbortedRunIsReadAgainByNoSync(t *testing.T) {
	t.Parallel()
	const stormCap, bound = 512, 6
	scope := newGraphSignalScope(stormCap)
	g := &GraphExtensionRunner{scope: scope}
	getSignal := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal

	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetHostAction("getSignal", getSignal)
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	ext := sdk.New("graph-abort")
	ext.Tool("probe", "answers", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		return sdk.ToolResult{Content: "ok"}, nil
	})
	loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "graph-abort", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}

	scope.state.Store(int32(graphLiveRun))
	bridge.RunSignalChanged()
	scope.state.Store(int32(graphAbortedRun))
	bridge.RunSignalChanged()
	// The sync that sees the abort reads the getter, and so does the watcher that the abort of the run fires.
	for range 2 {
		<-scope.abortedRead
	}
	// A request is served after its runtime is synced, which is one more read. A storm reads on while it is served.
	tool := loaded.Tools["probe"].Definition
	if _, err := tool.Execute(t.Context(), "tc-1", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	host.Shutdown("test done")
	got := scope.reads[graphAbortedRun].Load()
	t.Logf("the aborted run was read %d times", got)
	if got > bound {
		t.Fatalf("the aborted run was read %d times (a storm ends at %d); want at most %d", got, stormCap+1, bound)
	}
}
