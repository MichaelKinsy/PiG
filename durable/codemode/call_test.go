package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// countHeldStorage is memory storage that holds every commit recording an acknowledged call count until released.
type countHeldStorage struct {
	*storage.MemoryStorage
	released *gate
	mu       sync.Mutex
	held     int
}

func (s *countHeldStorage) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	if text := fmt.Sprintf("%+v", writes); strings.Contains(text, "firstIssued") || strings.Contains(text, "issuedAfter") {
		s.mu.Lock()
		s.held++
		s.mu.Unlock()
		if err := s.released.wait(ctx); err != nil {
			return 0, err
		}
	}
	return s.MemoryStorage.Commit(ctx, writes, adopt)
}

func (s *countHeldStorage) heldCommits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held
}

// codemode.ts: "A call is launched only once the count of calls issued in the drain that made it is committed"
// (launchCovered: `call.id <= this.durableIssued`). While the commit of the script's first count is held, its call is
// made but not launched; once it lands, the call runs and the script ends.
func TestACallLaunchesOnlyOnceItsIssuedCountIsCommitted(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	f := codemodeChat(t, Global{Name: "first", Execute: func(context.Context, json.RawMessage, durable.ToolExecutionApi) (json.RawMessage, error) {
		mu.Lock()
		seen = append(seen, "first")
		mu.Unlock()
		return json.RawMessage("1"), nil
	}})
	f.holds.block.Store(false)
	held := &countHeldStorage{MemoryStorage: storage.NewMemoryStorage(), released: newGate()}
	f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`return await first();`), done()})
	opened, root := f.setup.open(t, held)
	submission := submitGo(t, root)
	// Not eventually(): starting the VM can take longer than its 2 s under -race.
	for deadline := time.Now().Add(30 * time.Second); held.heldCommits() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no count commit was made")
		}
	}
	// The fingerprint commit lands meanwhile; only the count is held.
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	early := slices.Clone(seen)
	mu.Unlock()
	if len(early) != 0 {
		t.Fatalf("first ran before its issued count was committed: %v", early)
	}
	held.released.resolve()
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	expectText(t, must2(resultText(t, root)), false, "1", false)
	mustClose(t, opened)
}

func must2(text string, isError bool) string {
	if isError {
		return "error: " + text
	}
	return text
}

// codemode.ts checkFingerprint: the first run records the VM's fingerprint in the start data, before any call is
// admitted, so a rerun can refuse a different VM. (The upstream test sets the field itself, so it does not prove the first
// run records it.)
func TestTheFirstRunRecordsTheVMsFingerprint(t *testing.T) {
	f := codemodeChat(t)
	var recorded *string
	opened, root := f.restart(t, `await tools.hold({}); return "after";`, func(opened harness.Harness, task durable.TaskId) {
		run, err := durable.Snapshot[runState](testContext, opened, RunDoc, task)
		if err != nil {
			t.Fatal(err)
		}
		if run == nil || run.Start == nil {
			t.Fatal("no start data")
		}
		recorded = run.Start.Fingerprint
	})
	if recorded == nil || !strings.HasPrefix(*recorded, "quickjs-wasi ") {
		t.Fatalf("recorded fingerprint = %v, want the VM's", recorded)
	}
	text, isError := resultText(t, root)
	expectText(t, text, isError, "after", false)
	mustClose(t, opened)
}

// codemode.ts execute: the run gets `timeoutMs: remaining` (deadline - now), so a script's own `timeout_ms` limits its
// run, and not the sandbox's default of 300 s.
func TestAScriptsTimeoutLimitsItsRun(t *testing.T) {
	f := codemodeChat(t)
	f.setup.faux.SetResponses([]ai.FauxResponseStep{script(`// @options: {"timeout_ms": 200}
		while (true) {}`), done()})
	opened, root := f.setup.open(t, openSqlite(t, t.TempDir()+"/session.sqlite"))
	ctx, cancel := context.WithTimeout(testContext, 60*time.Second)
	defer cancel()
	if _, err := submitGo(t, root).Wait(ctx); err != nil {
		t.Fatalf("the script did not end at its timeout: %v", err)
	}
	text, isError := resultText(t, root)
	if !isError || !strings.Contains(text, "Script timed out: Execution timed out") {
		t.Fatalf("result = %q (error %v), want a timeout", text, isError)
	}
	mustClose(t, opened)
}

// codemode.ts sameJson: structural JSON equality where key order does not matter; undefined equals only undefined.
func TestSameJSONIgnoresKeyOrder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{`{"a":1,"b":{"c":[1,{"d":2,"e":3}]}}`, `{"b":{"c":[1,{"e":3,"d":2}]},"a":1}`, true},
		{`{"a":1}`, `{"a":1,"b":2}`, false},
		{`[1,2]`, `[2,1]`, false},
		{`1`, `1.0`, true},
		{`null`, ``, false},
		{``, ``, true},
	} {
		if got := sameJSON(json.RawMessage(c.a), json.RawMessage(c.b)); got != c.want {
			t.Errorf("sameJSON(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// codemode.ts cut: `text.length > max ? text.slice(0, max - 3) + "..." : text`, in UTF-16 units.
func TestCutCountsUTF16Units(t *testing.T) {
	if got := cut(strings.Repeat("é", 200), 200); got != strings.Repeat("é", 200) {
		t.Fatalf("200 two-byte characters were cut: %q", got)
	}
	if got := cut(strings.Repeat("é", 201), 200); got != strings.Repeat("é", 197)+"..." {
		t.Fatalf("cut = %q", got)
	}
	// A character outside the BMP is two units.
	if got := cut(strings.Repeat("😀", 100), 200); got != strings.Repeat("😀", 100) {
		t.Fatalf("100 astral characters were cut: %q", got)
	}
	if got := cut(strings.Repeat("😀", 101), 200); got != strings.Repeat("😀", 98)+"\xed\xa0\xbd..." {
		t.Fatalf("cut = %q", got)
	}
}

// entryApi is a tool execution API whose ExecuteTool records the order calls enter it, then holds each call until ctx
// ends, as a running tool does.
type entryApi struct {
	durable.ToolExecutionApi
	mu      sync.Mutex
	entered []string
}

func (a *entryApi) ExecuteTool(ctx context.Context, name string, _ map[string]any, _ ...durable.NestedCallOptions) (durable.NestedToolExecutionResult, error) {
	a.mu.Lock()
	a.entered = append(a.entered, name)
	a.mu.Unlock()
	<-ctx.Done()
	return durable.NestedToolExecutionResult{}, context.Cause(ctx)
}

func (a *entryApi) enteredCalls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.entered)
}

// onePProcessor makes the Go scheduler run the goroutine started last first, as its runnext slot does under load: the
// interleaving in which a call started later overtakes one started before it.
func onePProcessor(t *testing.T) {
	t.Helper()
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
}

func entryCall(t *testing.T) (*call, *entryApi) {
	t.Helper()
	api := &entryApi{}
	ctx, cancel := context.WithCancel(testContext)
	c := newCall(ToolOptions{}, api, ctx)
	c.work = ctx
	t.Cleanup(func() {
		cancel()
		c.spawned.Wait()
	})
	return c, api
}

func toolCall(id int, name string) cm.HostCall {
	return cm.HostCall{ID: id, Target: "tool", Name: name}
}

// codemode.ts launch: `this.callTool(call)` runs synchronously up to its first await, which is inside
// `api.executeTool`, so a call is in executeTool before the next call the script made is launched, and before the
// script can end and abort it. Here each call runs on its own goroutine, which the Go scheduler may start after a later
// call's (a script that ends first aborts the call before its tool ran): launch returns once the call entered.
func TestALaunchedCallEntersBeforeTheNextOneIsLaunched(t *testing.T) {
	onePProcessor(t)
	c, api := entryCall(t)
	c.released, c.durableIssued = true, 2
	c.launch(toolCall(1, "hold"))
	if got := api.enteredCalls(); !slices.Equal(got, []string{"hold"}) {
		t.Fatalf("after launching hold, the calls that entered are %v, want [hold]", got)
	}
	c.launch(toolCall(2, "echo"))
	if got := api.enteredCalls(); !slices.Equal(got, []string{"hold", "echo"}) {
		t.Fatalf("after launching echo, the calls that entered are %v, want [hold echo]", got)
	}
}

// codemode.ts onCall, replay: `this.gate.then(() => this.callTool(call))` enters the call in the turn it runs, as launch does.
func TestAReplayedCallEntersBeforeTheNextOneIsStarted(t *testing.T) {
	onePProcessor(t)
	c, api := entryCall(t)
	c.gateOpen, c.replaying = true, true
	c.replayed = []journalEntry{{ID: 1}, {ID: 2}}
	c.replayIndex = map[int]int{1: 0, 2: 1}
	c.onCall(toolCall(1, "hold"))
	c.onCall(toolCall(2, "echo"))
	c.events.next()()
	if got := api.enteredCalls(); !slices.Equal(got, []string{"hold"}) {
		t.Fatalf("after replaying hold, the calls that entered are %v, want [hold]", got)
	}
	c.events.next()()
	if got := api.enteredCalls(); !slices.Equal(got, []string{"hold", "echo"}) {
		t.Fatalf("after replaying echo, the calls that entered are %v, want [hold echo]", got)
	}
}

// commitApi is a tool execution API whose commits record that they ran and store nothing.
type commitApi struct {
	durable.ToolExecutionApi
	commits atomic.Int32
}

func (a *commitApi) Commit(context.Context, func(durable.Tx) (any, error), ...durable.CommitOptions) (any, error) {
	a.commits.Add(1)
	return nil, nil
}

func decidingCall(t *testing.T) (*call, *commitApi) {
	t.Helper()
	api := &commitApi{}
	c := newCall(ToolOptions{}, api, testContext)
	c.controller, c.abort = context.WithCancelCause(testContext)
	t.Cleanup(func() {
		c.abort(nil)
		c.spawned.Wait()
	})
	return c, api
}

// settle waits for what the call spawned, then handles the events it posted.
func settle(c *call) {
	c.spawned.Wait()
	for {
		c.events.mu.Lock()
		pending := len(c.events.items)
		c.events.mu.Unlock()
		if pending == 0 {
			return
		}
		c.events.next()()
	}
}

// codemode.ts:538-542 (fail) and diverge's `ended`/`failure` guard: a call ends with the first divergence or failure that decides it; what follows
// decides nothing, so no outcome is stored for it and the script's end is the first one's.
func TestOnlyTheFirstDivergenceOrFailureOfACallDecidesItsEnd(t *testing.T) {
	t.Run("a divergence after a divergence", func(t *testing.T) {
		c, api := decidingCall(t)
		c.diverge("first")
		c.diverge("second")
		settle(c)
		if got := c.ended.Output[0].Text; !strings.Contains(got, "first") || strings.Contains(got, "second") {
			t.Fatalf("the call ended with %q, want its first divergence", got)
		}
		if got := api.commits.Load(); got != 1 {
			t.Fatalf("%d outcomes were stored, want 1", got)
		}
	})
	t.Run("a divergence after a failure", func(t *testing.T) {
		c, api := decidingCall(t)
		failure := errors.New("failure")
		c.fail(failure)
		c.diverge("late")
		settle(c)
		if c.ended != nil || !errors.Is(c.failure, failure) || api.commits.Load() != 0 {
			t.Fatalf("a divergence after a failure ended the call with %v (failure %v, %d outcomes stored)", c.ended, c.failure, api.commits.Load())
		}
	})
	t.Run("a failure after a failure", func(t *testing.T) {
		c, _ := decidingCall(t)
		first := errors.New("first")
		c.fail(first)
		c.fail(errors.New("second"))
		if !errors.Is(c.failure, first) || !errors.Is(context.Cause(c.controller), first) {
			t.Fatalf("the call failed with %v and stopped the run with %v, want its first failure", c.failure, context.Cause(c.controller))
		}
	})
	t.Run("both stop the call before the run has its outcome", func(t *testing.T) {
		for name, decide := range map[string]func(c *call){
			"a divergence": func(c *call) { c.diverge("x") },
			"a failure":    func(c *call) { c.fail(errors.New("x")) },
		} {
			c, _ := decidingCall(t)
			if c.stopped() || c.decided() {
				t.Fatalf("%s: a call that decided nothing is stopped", name)
			}
			decide(c)
			if !c.stopped() || !c.decided() {
				t.Fatalf("%s: the call that decided is not stopped (stopped %v, decided %v)", name, c.stopped(), c.decided())
			}
			settle(c)
		}
	})
}

// codemode.ts:477 and :671-687 (callTool :822-843): the script's end reaches the call after the calls it made were issued and their nested
// calls settled (a replayed call finishes its row before it resolves the result the VM waits for), so the end waits for the
// replayed calls the VM issued; a call that stops meanwhile (failure, divergence) ends with its own outcome, not the script's.
func TestAScriptsEndWaitsForTheReplayedCallsItIssued(t *testing.T) {
	waiting := func(t *testing.T) *call {
		c, _ := decidingCall(t)
		c.replayCalls = 1
		c.onResult(cm.Result{OK: true})
		if c.finished || c.pendingResult == nil {
			t.Fatalf("the script's end was handled while a replayed call was settling (finished %v)", c.finished)
		}
		return c
	}
	t.Run("it waits for every one of them", func(t *testing.T) {
		c, _ := decidingCall(t)
		c.replayCalls = 2
		c.onResult(cm.Result{OK: true})
		c.replayCalls--
		c.resumeResult(false)
		if c.finished || c.pendingResult == nil {
			t.Fatalf("the script's end was handled while a replayed call was still settling (finished %v)", c.finished)
		}
	})
	t.Run("a failure ends the call", func(t *testing.T) {
		c := waiting(t)
		failure := errors.New("failure")
		c.fail(failure)
		if !c.finished || !errors.Is(c.outErr, failure) {
			t.Fatalf("after a failure the call finished %v with %v, want its failure", c.finished, c.outErr)
		}
	})
	t.Run("a divergence ends the call once its outcome is stored", func(t *testing.T) {
		c := waiting(t)
		c.diverge("diverged")
		if c.finished {
			t.Fatal("the call ended before the divergence's outcome was stored")
		}
		settle(c)
		if !c.finished || c.outErr != nil || len(c.out.Output) != 1 || !strings.Contains(c.out.Output[0].(ai.TextContent).Text, "diverged") {
			t.Fatalf("after a divergence the call finished %v with %+v, %v, want its divergence", c.finished, c.out, c.outErr)
		}
	})
}
