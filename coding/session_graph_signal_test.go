package coding

import (
	"context"
	"errors"
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

// graphTokenAnswer is one answer of an extension whose getSignal tells its runs apart (the Run token of the wire).
type graphTokenAnswer func() (any, error)

// graphTokenScope is a connection whose extension answers getSignal with the answer the test sets, and counts how many times the host asked.
type graphTokenScope struct {
	mcpFakeScope
	answer atomic.Value
	reads  atomic.Int64
}

func newGraphTokenScope() *graphTokenScope {
	s := &graphTokenScope{}
	s.serve = func(string, []json.RawMessage) (any, error) {
		s.reads.Add(1)
		return s.answer.Load().(graphTokenAnswer)()
	}
	return s
}

func (s *graphTokenScope) reply(answer any) {
	s.answer.Store(graphTokenAnswer(func() (any, error) { return answer, nil }))
}

// tokenSignal is the answer of an SDK that tells runs apart: run is the token of the signal the client's function returned, endedAborted whether the signal of the last answer had aborted.
func tokenSignal(run uint64, aborted, endedAborted bool) map[string]any {
	answer := map[string]any{"aborted": aborted, "run": run}
	if endedAborted {
		answer["endedAborted"] = true
	}
	return answer
}

// Pi gives every run its own signal (agent.ts:518-527): the run that follows one has another signal object, and a handler that kept the first's never sees the second's abort. An answer with a Run token tells the host which run it reads, so a token other than the held one is a new context, and the same token is one context however often it is read.
func TestGraphGetSignalTokenedRuns(t *testing.T) {
	t.Parallel()
	scope := newGraphTokenScope()
	g := &GraphExtensionRunner{scope: scope}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal
	read := func(answer any) context.Context {
		scope.reply(answer)
		return get()
	}

	first := read(tokenSignal(1, false, false))
	if first == nil || first.Err() != nil {
		t.Fatalf("run 1: signal = %v, want a live context", first)
	}
	for i := range 1000 {
		if again := read(tokenSignal(1, false, false)); again != first {
			t.Fatalf("read %d of run 1 returned another context", i)
		}
	}
	// Run 1 ends live and run 2 begins with no read of "no run" between them: another token is another context, and run 1's context is left alone.
	second := read(tokenSignal(2, false, false))
	if second == nil || second == first || second.Err() != nil {
		t.Fatalf("run 2: signal = %v (run 1's %v), want a new live context", second, second == first)
	}
	if first.Err() != nil {
		t.Fatal("the new run cancelled the run that ended live")
	}
	for i := range 1000 {
		if again := read(tokenSignal(2, false, false)); again != second {
			t.Fatalf("read %d of run 2 returned another context", i)
		}
	}
	// The abort of run 2 cancels run 2's context and no other.
	if aborted := read(tokenSignal(2, true, false)); aborted != second || aborted.Err() == nil {
		t.Fatalf("abort of run 2: signal = %v (same object %v), want run 2's context, cancelled", aborted, aborted == second)
	}
	if first.Err() != nil {
		t.Fatal("the abort of run 2 reached run 1's context")
	}
	for range 3 {
		if again := read(tokenSignal(2, true, false)); again != second {
			t.Fatal("a read of the aborted run 2 returned another context")
		}
	}
	// A run after an aborted one has its own token, so a live answer is a new run.
	third := read(tokenSignal(3, false, false))
	if third == nil || third == second || third == first || third.Err() != nil {
		t.Fatalf("run 3: signal = %v, want a new live context", third)
	}
	// "No run" ends the run; the same token after it is a new run, as a client that returns undefined between runs has none to hold.
	if got := read(nil); got != nil {
		t.Fatalf("no run: signal = %v, want nil", got)
	}
	if again := read(tokenSignal(3, false, false)); again == nil || again == third {
		t.Fatalf("a run after no run: signal = %v (run 3's %v), want a new live context", again, again == third)
	}
}

// A run that was aborted and replaced before the host read the abort (the client's signal for run 1 aborted, then run 2 began with no read between) is told by endedAborted: the context the host holds for run 1 is cancelled first, as the signal a handler kept is aborted in Pi, and run 2 has a live context of its own.
func TestGraphGetSignalEndedAbortedCancelsTheOldRun(t *testing.T) {
	t.Parallel()
	scope := newGraphTokenScope()
	g := &GraphExtensionRunner{scope: scope}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal
	read := func(answer any) context.Context {
		scope.reply(answer)
		return get()
	}

	first := read(tokenSignal(1, false, false))
	second := read(tokenSignal(2, false, true))
	if first.Err() == nil {
		t.Fatal("run 1 ended aborted and its context is live")
	}
	if second == nil || second == first || second.Err() != nil {
		t.Fatalf("run 2: signal = %v (run 1's %v), want a new live context", second, second == first)
	}
	// endedAborted is about the run that ended: a later read of run 2 with it set again changes nothing.
	if again := read(tokenSignal(2, false, true)); again != second || again.Err() != nil {
		t.Fatalf("a second read of run 2 = %v, want its live context", again)
	}
	// Run 2 aborts, and run 3 begins live with no read between: run 2's context is cancelled, run 3's is live.
	third := read(tokenSignal(3, false, true))
	if second.Err() == nil || third == second || third == nil || third.Err() != nil {
		t.Fatalf("run 3: signal = %v, run 2 cancelled %v", third, second.Err() != nil)
	}
}

// The extension's getSignal can fail between two runs (the client's function raises): the host keeps the run it holds, as it does for an answer it cannot decode, and the next answer is read against that run's token.
func TestGraphGetSignalErrorBetweenTokenedRuns(t *testing.T) {
	t.Parallel()
	scope := newGraphTokenScope()
	g := &GraphExtensionRunner{scope: scope}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal
	read := func(answer any) context.Context {
		scope.reply(answer)
		return get()
	}

	first := read(tokenSignal(1, false, false))
	scope.answer.Store(graphTokenAnswer(func() (any, error) { return nil, errors.New("getSignal raised") }))
	if got := get(); got != nil {
		t.Fatalf("a failed read: signal = %v, want nil", got)
	}
	if first.Err() != nil {
		t.Fatal("a failed read cancelled the held run")
	}
	if again := read(tokenSignal(1, false, false)); again != first {
		t.Fatal("run 1 after a failed read is another context")
	}
	scope.answer.Store(graphTokenAnswer(func() (any, error) { return nil, errors.New("getSignal raised") }))
	_ = get()
	second := read(tokenSignal(2, false, false))
	if second == nil || second == first || second.Err() != nil {
		t.Fatalf("run 2 after a failed read: signal = %v (run 1's %v), want a new live context", second, second == first)
	}
}

// A run member that is not a positive integer is an answer the host cannot read: the read fails and the held run is kept, as for any answer that does not decode.
func TestGraphGetSignalMalformedRunIsAReadError(t *testing.T) {
	t.Parallel()
	scope := newGraphTokenScope()
	g := &GraphExtensionRunner{scope: scope}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal
	scope.reply(tokenSignal(1, false, false))
	first := get()
	for _, run := range []any{"1", -1, 1.5, 1e30, true, []any{1}, map[string]any{}} {
		scope.reply(map[string]any{"aborted": true, "run": run, "endedAborted": true})
		if got := get(); got != nil {
			t.Errorf("run %#v: signal = %v, want nil (a read error)", run, got)
		}
		if first.Err() != nil {
			t.Fatalf("run %#v: the malformed answer cancelled the held run", run)
		}
	}
	scope.reply(tokenSignal(1, false, false))
	if again := get(); again != first {
		t.Fatal("run 1 after malformed answers is another context")
	}
}

// A Run of zero is no token: a Run is positive, and the host decodes it into a plain uint64, so an explicit zero reads as an answer without one. The old rule applies (a live answer keeps the held run, an aborted one cancels it, a live one after that is a new run) and the token the host holds is not touched.
func TestGraphGetSignalZeroRunIsNoToken(t *testing.T) {
	t.Parallel()
	scope := newGraphTokenScope()
	g := &GraphExtensionRunner{scope: scope}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal
	scope.reply(map[string]any{"aborted": false, "run": 0})
	first := get()
	scope.reply(map[string]any{"aborted": false, "run": 0})
	if again := get(); first == nil || again != first {
		t.Fatalf("two live answers with run 0: %v then %v, want one context (the old rule)", first, again)
	}
	scope.reply(map[string]any{"aborted": true, "run": 0, "endedAborted": true})
	if aborted := get(); aborted != first || first.Err() == nil {
		t.Fatalf("an aborted answer with run 0: %v, want the held context, cancelled", aborted)
	}
	scope.reply(tokenSignal(1, false, false))
	next := get()
	if next == nil || next == first || next.Err() != nil {
		t.Fatalf("token 1 after the aborted run 0: %v, want a new live context", next)
	}
	scope.reply(map[string]any{"aborted": false, "run": 0})
	if zero := get(); zero != next {
		t.Fatal("an answer with run 0 after token 1 changed the run")
	}
	scope.reply(tokenSignal(1, false, false))
	if again := get(); again != next {
		t.Fatal("token 1 after an answer with run 0 is another context: the zero replaced the held token")
	}
}

// graphReleasedSignalState is graphSignalState as the released host decodes the answer of getSignal: an SDK with a Run token must still be read by it.
type graphReleasedSignalState struct {
	Aborted bool `json:"aborted"`
}

func TestGraphSignalAnswerWithRunDecodesInAReleasedHost(t *testing.T) {
	t.Parallel()
	var got graphReleasedSignalState
	if err := json.Unmarshal([]byte(`{"aborted":true,"run":7,"endedAborted":true}`), &got); err != nil || !got.Aborted {
		t.Fatalf("decode = %+v, %v; want aborted and no error", got, err)
	}
}

// The tracker applies the answers in the order the reads were made, with tokens too: run 1's answer is held inside the extension, the read of run 2 follows it and cannot finish first, so the run the host holds at the end is run 2's, and a later read of run 2 returns the context the second read returned.
func TestGraphGetSignalAppliesTokenedAnswersInReadOrder(t *testing.T) {
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
		switch calls.Add(1) {
		case 1:
			close(firstIn)
			<-release
			return tokenSignal(1, false, false), nil
		case 2:
			secondIn.Store(true)
			return tokenSignal(2, false, false), nil
		}
		return tokenSignal(2, false, false), nil
	}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal

	var first, second context.Context
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(firstDone); first = get() }()
	<-firstIn
	go func() { defer close(secondDone); second = get() }()
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
	if first == nil || second == nil || first == second {
		t.Fatalf("reads = %v and %v, want the contexts of runs 1 and 2", first, second)
	}
	if again := get(); again != second {
		t.Fatalf("a later read of run 2 = %v (second read's %v), want the context the second read returned", again, again == second)
	}
}

// graphUnstableScope is a connection whose extension returns another signal object on every call (a derived signal): every answer has a token no answer had before. Once aborted, every answer is an aborted one. stormCap ends a storm: past that many reads of an aborted run the extension answers "no run", so a host that reads without end still lets the test finish with the count of what it read.
type graphUnstableScope struct {
	mcpFakeScope
	aborted     atomic.Bool
	tokens      atomic.Uint64
	reads       atomic.Int64
	liveRead    chan struct{}
	abortedRead chan struct{}
	abortedN    atomic.Int64
	stormCap    int64
}

func newGraphUnstableScope(stormCap int64) *graphUnstableScope {
	s := &graphUnstableScope{
		stormCap:    stormCap,
		liveRead:    make(chan struct{}, 8),
		abortedRead: make(chan struct{}, stormCap+8),
	}
	s.serve = func(string, []json.RawMessage) (any, error) {
		s.reads.Add(1)
		if !s.aborted.Load() {
			select {
			case s.liveRead <- struct{}{}:
			default:
			}
			return tokenSignal(s.tokens.Add(1), false, false), nil
		}
		n := s.abortedN.Add(1)
		select {
		case s.abortedRead <- struct{}{}:
		default:
		}
		if n > s.stormCap {
			return nil, nil
		}
		return tokenSignal(s.tokens.Add(1), true, true), nil
	}
	return s
}

// A client whose getSignal returns a new signal object on every call has a new token on every read. Its run is aborted: a context made for every aborted read would be cancelled at birth, the watcher (context.AfterFunc fires at once for a cancelled context) would sync, read and make another, without end. The bound: a read makes at most one context, a token other than the held one makes none while the held context is cancelled and the answer is aborted, so the abort costs at most four reads: the sync that sees it, the watcher of the context it made (it fires at once for a cancelled one), the watcher of the context it replaced (the run ended aborted, so the replaced context is cancelled; the watcher is stopped, but may already have started) and the sync of the request that follows. Every request after it costs the one read of its own sync.
// The test counts the client's getSignal calls: the abort, then N requests.
func TestGraphUnstableSignalIsReadOncePerRequest(t *testing.T) {
	t.Parallel()
	const stormCap, requests, abortBound = 512, 20, 4
	scope := newGraphUnstableScope(stormCap)
	g := &GraphExtensionRunner{scope: scope}
	getSignal := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal

	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetHostAction("getSignal", getSignal)
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	ext := sdk.New("graph-unstable")
	ext.Tool("probe", "answers", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		return sdk.ToolResult{Content: "ok"}, nil
	})
	loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "graph-unstable", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	tool := loaded.Tools["probe"].Definition
	execute := func() {
		t.Helper()
		if _, err := tool.Execute(t.Context(), "tc", json.RawMessage(`{}`), nil); err != nil {
			t.Fatal(err)
		}
	}

	bridge.RunSignalChanged()
	execute()
	// Wait for both live reads (RunSignalChanged and execute) before aborting, so the live RunSignalChanged task cannot land after the abort.
	for range 2 {
		<-scope.liveRead
	}
	scope.aborted.Store(true)
	bridge.RunSignalChanged()
	// The abort fires three async reads: the RunSignalChanged sync, the watcher of the cancelled live context (cancelled inside graphSignal.read before runSignals stops it), and the watcher of the new cancelled context. Wait for all three before the settle request and snapshot, so none can land inside the request loop.
	for range 3 {
		<-scope.abortedRead
	}
	execute()
	// The reads so far are the abort's: all three async abort reads and the settle request have completed, so each request after this snapshot is one read.
	abortReads, before := scope.abortedN.Load(), scope.reads.Load()
	for range requests {
		execute()
	}
	perRequest := scope.reads.Load() - before
	host.Shutdown("test done")
	t.Logf("getSignal was called %d times for the abort and %d times for %d requests", abortReads, perRequest, requests)
	if abortReads > abortBound {
		t.Fatalf("the abort of the run was read %d times before the requests (a storm ends at %d); want at most %d", abortReads, stormCap+1, abortBound)
	}
	if perRequest > requests {
		t.Fatalf("%d requests read getSignal %d times (a storm ends at %d); want at most one read per request", requests, perRequest, stormCap+1)
	}
}

// The price of the bound on an unstable client: two runs that are both aborted when first read, with no live read between them, are one context (Pi has two aborted signals, so a handler that kept the first one sees `ctx.signal !== held` there; here it is `===`). This pins that corner of D235.
func TestGraphTwoRunsAbortedAtBirthShareOneContext(t *testing.T) {
	t.Parallel()
	scope := newGraphTokenScope()
	g := &GraphExtensionRunner{scope: scope}
	get := g.contextActions(callbackIDs{"getSignal": "sig"}).GetSignal
	scope.reply(tokenSignal(1, true, false))
	first := get()
	scope.reply(tokenSignal(2, true, true))
	second := get()
	if first == nil || first.Err() == nil || second != first {
		t.Fatalf("two runs aborted at birth: %v then %v, want one cancelled context", first, second)
	}
	// A live run after them is a new run.
	scope.reply(tokenSignal(3, false, true))
	if third := get(); third == first || third == nil || third.Err() != nil {
		t.Fatalf("a live run after them: %v, want a new live context", third)
	}
}

// read runs under Host.pushState (//pig:hotpath) for every request, with its lock held: the token compare and the keep of a held run allocate nothing, whether the answer has a token or not. (A new run makes a context, as it always did.)
func TestGraphSignalReadOfAHeldRunAllocatesNothing(t *testing.T) {
	var s graphSignal
	for name, state := range map[string]*graphSignalState{
		"no token": {}, "token": {Run: 1}, "token, aborted": {Aborted: true, Run: 1, EndedAborted: true},
	} {
		ask := func() (*graphSignalState, error) { return state, nil }
		held := s.read(ask)
		if n := testing.AllocsPerRun(100, func() {
			if s.read(ask) != held {
				t.Fatalf("%s: a read of the held run returned another context", name)
			}
		}); n != 0 {
			t.Errorf("%s: a read of the held run allocates %v times, want 0", name, n)
		}
		s = graphSignal{}
	}
}
