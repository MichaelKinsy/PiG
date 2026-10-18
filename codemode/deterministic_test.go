// pi: packages/codemode/src/runtime/vm.ts

package codemode_test

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/codemode"
)

// Ports packages/codemode/test/deterministic.test.ts (Pi 1.2.0) with its scripts and expectations: Sandbox.Start runs a
// script deterministically with the caller answering its calls, so a durable host can replay it after a restart. Every test
// runs on the worker backend and the remote backend, whose transport clones every message through JSON as Workers RPC
// would. Go mechanics: the abort signal is a context, JSON text stands for `unknown`, and `queueMicrotask` is an ordered
// answerer goroutine (a call's answer is delivered after OnCall returns, in call order).

const deterministicBudget = 50_000

// startedAt is STARTED_AT = Date.UTC(2026, 0, 15, 10, 0, 0).
var startedAt = float64(time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC).UnixMilli())

// delayedRemote serves in process and clones every message, as clonedRemote does; with delayFirst it holds the reply to the
// first result back, so later results reach the VM before it.
type delayedRemote struct{ delayFirst bool }

func (r delayedRemote) Execute(ctx context.Context, request codemode.RemoteRequest, exchange codemode.RemoteExchange) error {
	codemode.ServeRemote(ctx, jsonClone(request), func(ctx context.Context, messages []codemode.RemoteMessage, reply func(*codemode.RemoteReply, error)) {
		exchange(ctx, jsonClone(messages), func(answer *codemode.RemoteReply, err error) {
			if answer != nil {
				cloned := jsonClone(*answer)
				answer = &cloned
			}
			if r.delayFirst && answer != nil && answer.Type == codemode.MessageResult && answer.Seq != nil && *answer.Seq == 1 {
				time.AfterFunc(30*time.Millisecond, func() { reply(answer, err) })
				return
			}
			reply(answer, err)
		})
	}, codemode.ServeOptions{MaxInterruptBudget: deterministicBudget})
	return nil
}

var deterministicBackends = []struct {
	name string
	open func(t *testing.T, options codemode.SandboxOptions) *codemode.Sandbox
}{
	{"worker", func(t *testing.T, options codemode.SandboxOptions) *codemode.Sandbox {
		sandbox, err := codemode.NewSandbox(options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sandbox.Close() })
		return sandbox
	}},
	{"remote", func(t *testing.T, options codemode.SandboxOptions) *codemode.Sandbox {
		return remoteDeterministic(t, options, false)
	}},
}

func remoteDeterministic(t *testing.T, options codemode.SandboxOptions, delayFirst bool) *codemode.Sandbox {
	t.Helper()
	sandbox, err := codemode.NewRemoteSandbox(codemode.RemoteSandboxOptions{MemoryLimitBytes: options.MemoryLimitBytes,
		Remote: func() (codemode.Remote, error) { return delayedRemote{delayFirst}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	return sandbox.Sandbox
}

// recorded is a started run and what it reported.
type recorded struct {
	run *codemode.Run

	mu          sync.Mutex
	calls       []codemode.HostCall
	idles       [][2]int
	events      []string
	fingerprint string
}

func (r *recorded) callCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }
func (r *recorded) idleList() [][2]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]int(nil), r.idles...)
}

func (r *recorded) call(i int) codemode.HostCall { r.mu.Lock(); defer r.mu.Unlock(); return r.calls[i] }

// startOptions adjusts a start: the options of the run, the context that stands for its abort signal, and how the host
// answers a call other than `wait`.
type startOptions struct {
	codemode.RunOptions
	ctx    context.Context
	answer func(codemode.HostCall) codemode.Outcome
}

// startRun starts code with tools `echo` and `wait`. Every call but `wait` is answered at once, by default with its
// argument, as result number 1, 2, ... with the clock at startedAt + 1000 * seq; `wait` is left for the test to answer.
func startRun(t *testing.T, sandbox *codemode.Sandbox, code string, mutate ...func(*startOptions)) *recorded {
	t.Helper()
	timeout := 20_000.0
	options := startOptions{RunOptions: codemode.RunOptions{StartedAt: startedAt, Seed: 1, Tools: []codemode.DeclaredTool{{Name: "echo"}, {Name: "wait"}}, TimeoutMs: &timeout}}
	for _, m := range mutate {
		m(&options)
	}
	answer := options.answer
	if answer == nil {
		answer = func(call codemode.HostCall) codemode.Outcome { return codemode.Outcome{OK: true, Value: call.Args} }
	}
	r := &recorded{}
	queue := make(chan codemode.HostCall, 1024)
	// Pi answers in a microtask, after start() has returned the run; ready stands for that.
	ready := make(chan struct{})
	seq := 0
	var runs sync.WaitGroup
	runs.Go(func() {
		<-ready
		for call := range queue {
			seq++
			_ = r.run.Deliver(seq, startedAt+1000*float64(seq), call.ID, answer(call))
		}
	})
	run := options.RunOptions
	run.OnStarted = func(fingerprint string) {
		r.mu.Lock()
		r.fingerprint = fingerprint
		r.events = append(r.events, "started")
		r.mu.Unlock()
	}
	run.OnCall = func(call codemode.HostCall) {
		r.mu.Lock()
		r.calls = append(r.calls, call)
		r.events = append(r.events, "call "+itoa(call.ID))
		r.mu.Unlock()
		if call.Name != "wait" {
			queue <- call
		}
	}
	run.OnIdle = func(seq, issued int) {
		r.mu.Lock()
		r.idles = append(r.idles, [2]int{seq, issued})
		r.events = append(r.events, "idle "+itoa(seq))
		r.mu.Unlock()
	}
	ctx := options.ctx
	if ctx == nil {
		ctx = t.Context()
	}
	started, err := sandbox.Start(ctx, code, run)
	if err != nil {
		close(ready)
		close(queue)
		t.Fatal(err)
	}
	r.run = started
	close(ready)
	t.Cleanup(func() {
		started.Result()
		close(queue)
		runs.Wait()
	})
	return r
}

func waitUntil(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("Condition was not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func numbers(t *testing.T, raw json.RawMessage) []float64 {
	t.Helper()
	var out []float64
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return out
}

func valueJSON(r codemode.Result) string {
	if !r.OK {
		return "<failed: " + r.Error.Message + ">"
	}
	return string(r.Value)
}

// The first Math.random() with seed 1, and the first two with seed 0.
const pinnedFirstRandom = 0.34227450605335275

var pinnedSeedZero = []float64{0.6711372530266764, 0.7258461452833667}

func forEachBackend(t *testing.T, test func(t *testing.T, name string, open func(codemode.SandboxOptions) *codemode.Sandbox)) {
	for _, backend := range deterministicBackends {
		t.Run(backend.name, func(t *testing.T) {
			test(t, backend.name, func(options codemode.SandboxOptions) *codemode.Sandbox { return backend.open(t, options) })
		})
	}
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:108 describe(`deterministic runs (${backend})`) "makes the same calls and output for the same start, seed, and results"
func TestDeterministicRunsMakeTheSameCallsAndOutputForTheSameStartSeedAndResults(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		code := `
				const values = [Date.now(), performance.now(), Math.random(), Math.random(), new Date().toISOString()];
				const first = await tools.echo(values);
				text(JSON.stringify([Date.now(), performance.now(), Math.random()]));
				return await tools.echo(first.concat(Math.random()));
			`
		sandbox := open(codemode.SandboxOptions{})
		one, two := startRun(t, sandbox, code), startRun(t, sandbox, code)
		a, b := one.run.Result(), two.run.Result()
		// Call durations are wall-clock time; everything else is the same.
		timeless := func(r codemode.Result) any {
			var calls [][2]string
			for _, c := range r.Calls {
				calls = append(calls, [2]string{c.Name, string(c.Status)})
			}
			return []any{r.OK, string(r.Value), r.Error, r.Output, r.During, calls}
		}
		if !reflect.DeepEqual(timeless(a), timeless(b)) {
			t.Fatalf("two runs differ:\n%v\n%v", timeless(a), timeless(b))
		}
		if !a.OK {
			t.Fatalf("result = %+v (%+v)", a, a.Error)
		}
		if !reflect.DeepEqual(one.calls, two.calls) {
			t.Fatalf("calls differ:\n%v\n%v", one.calls, two.calls)
		}
		// The clock: startedAt, then the time of the result being processed; performance.now() counts from startedAt.
		first := one.call(0).Args
		values := numbers(t, mustPrefix(first, 4))
		now, elapsed, random := values[0], values[1], values[2]
		if now != startedAt || elapsed != 0 {
			t.Fatalf("clock = %v, %v", now, elapsed)
		}
		var all []any
		_ = json.Unmarshal(first, &all)
		if all[4] != "2026-01-15T10:00:00.000Z" {
			t.Fatalf("new Date().toISOString() = %v", all[4])
		}
		var printed []float64
		_ = json.Unmarshal([]byte(a.Output[0].Text), &printed)
		if !reflect.DeepEqual(printed[:2], []float64{startedAt + 1000, 1000}) {
			t.Fatalf("printed clock = %v", printed[:2])
		}
		// Pinned: a change here changes what replayed scripts see, so it must change the fingerprint too.
		if random != pinnedFirstRandom {
			t.Fatalf("first random = %v, want %v", random, pinnedFirstRandom)
		}

		// The seed decides the random values, not the start time.
		later := startRun(t, sandbox, code, func(o *startOptions) { o.StartedAt = startedAt + 5000 })
		reseeded := startRun(t, sandbox, code, func(o *startOptions) { o.Seed = 2 })
		later.run.Result()
		reseeded.run.Result()
		if got := numbers(t, mustPrefix(later.call(0).Args, 4))[2]; got != random {
			t.Fatalf("a later start changed the random value: %v", got)
		}
		if got := numbers(t, mustPrefix(reseeded.call(0).Args, 4))[2]; got == random {
			t.Fatalf("a new seed gave the same random value %v", got)
		}
		// Seed 0 has its own values: QuickJS turns a zero state into 1, which must not make it seed 1's.
		zero := startRun(t, sandbox, "return [Math.random(), Math.random()]", func(o *startOptions) { o.Seed = 0 })
		zeroResult := zero.run.Result()
		if !zeroResult.OK {
			t.Fatalf("seed 0: %+v", zeroResult.Error)
		}
		got := numbers(t, zeroResult.Value)
		if !reflect.DeepEqual(got, pinnedSeedZero) {
			t.Fatalf("seed 0 values = %v, want %v", got, pinnedSeedZero)
		}
		if slicesContains(got, random) {
			t.Fatalf("seed 0 gave seed 1's value")
		}
	})
}

func slicesContains(list []float64, v float64) bool {
	return slices.Contains(list, v)
}

// mustPrefix is the first n elements of a JSON array of mixed values as a JSON array of numbers (the ISO string element is
// left out), so the numbers can be read without a typed decode of the whole list.
func mustPrefix(raw json.RawMessage, n int) json.RawMessage {
	var all []json.RawMessage
	_ = json.Unmarshal(raw, &all)
	out, _ := json.Marshal(all[:n])
	return out
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:148 describe(`deterministic runs (${backend})`) "runs in UTC whatever the host's timezone, and without WeakRef or FinalizationRegistry"
func TestDeterministicRunsRunInUTCWhateverTheHostsTimezoneAndWithoutWeakRef(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	saved := time.Local
	time.Local = kolkata
	t.Cleanup(func() { time.Local = saved })
	forEachBackend(t, func(t *testing.T, name string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		code := "return [new Date(0).getHours(), new Date(0).getTimezoneOffset(), typeof WeakRef, typeof FinalizationRegistry]"
		sandbox := open(codemode.SandboxOptions{})
		result := startRun(t, sandbox, code).run.Result()
		if valueJSON(result) != `[0,0,"undefined","undefined"]` {
			t.Fatalf("start = %s", valueJSON(result))
		}
		// Execute is unchanged: the host's timezone and every intrinsic. (The remote server of this process shares the host's zone.)
		executed, err := sandbox.Execute(t.Context(), code, codemode.ExecuteOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if valueJSON(executed) != `[5,-330,"function","function"]` {
			t.Fatalf("%s execute = %s", name, valueJSON(executed))
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:159 describe(`deterministic runs (${backend})`) "applies results in seq order, whatever order they are delivered in"
func TestDeterministicRunsApplyResultsInSeqOrderWhateverOrderTheyAreDeliveredIn(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, `const order = [];
				await Promise.all([tools.wait("a").then(() => order.push("a")), tools.wait("b").then(() => order.push("b"))]);
				return order;`)
		waitUntil(t, func() bool { return r.callCount() == 2 && len(r.idleList()) == 1 })
		a, b := r.call(0), r.call(1)
		// b is result 1 and a result 2, but a arrives first.
		mustDeliver(t, r.run, 2, startedAt+2000, a.ID)
		mustDeliver(t, r.run, 1, startedAt+1000, b.ID)
		if got := valueJSON(r.run.Result()); got != `["b","a"]` {
			t.Fatalf("order = %s", got)
		}
		// No idle after the last result: the script finished in that drain, and the host stopped the VM.
		if got := r.idleList(); !reflect.DeepEqual(got, [][2]int{{0, 2}, {1, 2}}) {
			t.Fatalf("idles = %v", got)
		}
	})
}

func mustDeliver(t *testing.T, run *codemode.Run, seq int, now float64, id int) {
	t.Helper()
	if err := run.Deliver(seq, now, id, codemode.Outcome{OK: true, Value: json.RawMessage("null")}); err != nil {
		t.Fatal(err)
	}
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:180 describe(`deterministic runs (${backend})`) "reports the fingerprint before any call and the calls issued after each result"
func TestDeterministicRunsReportTheFingerprintBeforeAnyCallAndTheCallsIssuedAfterEachResult(t *testing.T) {
	forEachBackend(t, func(t *testing.T, name string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{MemoryLimitBytes: 64 * 1024 * 1024})
		r := startRun(t, sandbox, "await tools.echo(1); await Promise.all([tools.echo(2), tools.echo(3)]);")
		if !r.run.Result().OK {
			t.Fatal("run failed")
		}
		r.mu.Lock()
		events := append([]string(nil), r.events...)
		r.mu.Unlock()
		if got := events[:3]; !reflect.DeepEqual(got, []string{"started", "call 1", "idle 0"}) {
			t.Fatalf("events = %v", events)
		}
		if got := r.idleList(); !reflect.DeepEqual(got, [][2]int{{0, 1}, {1, 3}, {2, 3}}) {
			t.Fatalf("idles = %v", got)
		}
		for i, want := range []struct {
			id   int
			args string
		}{{1, "1"}, {2, "2"}, {3, "3"}} {
			if c := r.call(i); c.ID != want.id || c.Target != "tool" || c.Name != "echo" || string(c.Args) != want.args {
				t.Fatalf("call %d = %+v", i, c)
			}
		}
		fingerprint := r.fingerprint
		if !regexp.MustCompile(`^quickjs-wasi \S+, quickjs \S+, prelude [0-9a-f]{8}, deterministic \d+, stack \d+, heap 67108864, budget \S+$`).MatchString(fingerprint) {
			t.Fatalf("fingerprint = %q", fingerprint)
		}
		if name == "remote" {
			if !strings.Contains(fingerprint, "budget 50000") {
				t.Fatalf("fingerprint = %q", fingerprint)
			}
		} else if !strings.Contains(fingerprint, "budget none") {
			t.Fatalf("fingerprint = %q", fingerprint)
		}
		same := startRun(t, sandbox, "return 1")
		other := startRun(t, open(codemode.SandboxOptions{MemoryLimitBytes: 32 * 1024 * 1024}), "return 1")
		same.run.Result()
		other.run.Result()
		if same.fingerprint != fingerprint || other.fingerprint == fingerprint {
			t.Fatalf("fingerprints: same %q, other %q, first %q", same.fingerprint, other.fingerprint, fingerprint)
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:209 describe(`deterministic runs (${backend})`) "numbers calls to globals with tools, and answers errors as rejections"
func TestDeterministicRunsNumberCallsToGlobalsWithToolsAndAnswerErrorsAsRejections(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, `const found = await search("x", 2);
				try { await tools.echo("fail"); } catch (error) { text(error.message); }
				return found;`, func(o *startOptions) {
			o.Globals = []codemode.DeclaredGlobal{{Name: "search", Spread: true}}
			o.answer = func(call codemode.HostCall) codemode.Outcome {
				if string(call.Args) == `"fail"` {
					return codemode.Outcome{Message: "no"}
				}
				if call.Name == "search" {
					return codemode.Outcome{OK: true, Value: json.RawMessage(`["found"]`)}
				}
				return codemode.Outcome{OK: true, Value: call.Args}
			}
		})
		result := r.run.Result()
		if c := r.call(0); c.ID != 1 || c.Target != "global" || c.Name != "search" || string(c.Args) != `["x",2]` {
			t.Fatalf("call 1 = %+v", c)
		}
		if valueJSON(result) != `["found"]` || len(result.Output) != 1 || result.Output[0].Type != codemode.OutputItemText || result.Output[0].Text != "no" {
			t.Fatalf("result = %+v", result)
		}
		if c := r.call(1); c.ID != 2 || c.Target != "tool" || c.Name != "echo" {
			t.Fatalf("call 2 = %+v", c)
		}
		// Calls to globals count as issued calls too.
		if got := r.idleList()[:2]; !reflect.DeepEqual(got, [][2]int{{0, 1}, {1, 2}}) {
			t.Fatalf("idles = %v", got)
		}
		if len(result.Calls) != 1 || result.Calls[0].Name != "echo" || result.Calls[0].Status != codemode.CallError {
			t.Fatalf("calls = %+v", result.Calls)
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:236 describe(`deterministic runs (${backend})`) "keeps the clock monotonic and rejects a clock value that is not finite"
func TestDeterministicRunsKeepTheClockMonotonicAndRejectAClockValueThatIsNotFinite(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, "const before = Date.now(); await tools.wait(1); return [before, Date.now(), performance.now()]")
		waitUntil(t, func() bool { return r.callCount() == 1 })
		if err := r.run.Deliver(1, math.NaN(), 1, codemode.Outcome{OK: true, Value: json.RawMessage("null")}); err == nil || !strings.Contains(err.Error(), "not a finite number") {
			t.Fatalf("NaN clock: %v", err)
		}
		// A resumed host whose clock is behind the run's start.
		mustDeliver(t, r.run, 1, startedAt-2000, 1)
		want := "[" + jsNumber(startedAt) + "," + jsNumber(startedAt) + ",0]"
		if got := valueJSON(r.run.Result()); got != want {
			t.Fatalf("value = %s, want %s", got, want)
		}
	})
}

func jsNumber(f float64) string { b, _ := json.Marshal(f); return string(b) }

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:249 describe(`deterministic runs (${backend})`) "reports the result the VM was processing when the script ended"
func TestDeterministicRunsReportTheResultTheVMWasProcessingWhenTheScriptEnded(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		returned := startRun(t, sandbox, "await tools.echo(1); return await tools.echo(2)")
		thrown := startRun(t, sandbox, "await tools.echo(1); await Promise.all([tools.echo(2), tools.echo(3)]); throw new Error('x')")
		first := startRun(t, sandbox, "return 1")
		if r := returned.run.Result(); !r.OK || string(r.Value) != "2" || r.During == nil || *r.During != (codemode.During{Seq: 2, Issued: 2}) {
			t.Fatalf("returned = %+v", r)
		}
		if r := thrown.run.Result(); r.OK || r.During == nil || *r.During != (codemode.During{Seq: 3, Issued: 3}) {
			t.Fatalf("thrown = %+v", r)
		}
		if r := first.run.Result(); !r.OK || r.During == nil || *r.During != (codemode.During{}) {
			t.Fatalf("first = %+v", r)
		}
		executed, err := sandbox.Execute(t.Context(), "return 1", codemode.ExecuteOptions{})
		if err != nil || executed.During != nil {
			t.Fatalf("execute has no during: %+v %v", executed.During, err)
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:263 describe(`deterministic runs (${backend})`) "leaves a call waiting when its answer cannot be serialized"
func TestDeterministicRunsLeaveACallWaitingWhenItsAnswerCannotBeSerialized(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, "return await tools.wait(1)")
		waitUntil(t, func() bool { return r.callCount() == 1 })
		// 1n has no JSON form: here an answer that is not JSON.
		if err := r.run.Deliver(1, startedAt, 1, codemode.Outcome{OK: true, Value: json.RawMessage("1n")}); err == nil {
			t.Fatal("an answer JSON cannot hold was accepted")
		}
		if err := r.run.Deliver(1, startedAt, 1, codemode.Outcome{Message: "x", Properties: json.RawMessage(`{"big":1n}`)}); err == nil {
			t.Fatal("properties JSON cannot hold were accepted")
		}
		if err := r.run.Deliver(1, startedAt, 1, codemode.Outcome{OK: true, Value: json.RawMessage(`"fine"`)}); err != nil {
			t.Fatal(err)
		}
		if got := valueJSON(r.run.Result()); got != `"fine"` {
			t.Fatalf("value = %s", got)
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:275 describe(`deterministic runs (${backend})`) "rejects with an Error carrying the answer's properties"
func TestDeterministicRunsRejectWithAnErrorCarryingTheAnswersProperties(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, "try { await tools.echo(1); } catch (e) { return [e instanceof Error, e.message, e.name, e.structuredOutput]; }", func(o *startOptions) {
			o.answer = func(codemode.HostCall) codemode.Outcome {
				return codemode.Outcome{Message: "denied", Properties: json.RawMessage(`{"structuredOutput":{"code":7},"name":"Ignored","message":"ignored"}`)}
			}
		})
		if got := valueJSON(r.run.Result()); got != `[true,"denied","Error",{"code":7}]` {
			t.Fatalf("value = %s", got)
		}
	})
}

// deliver() throws "not an object" for null and an array (execution.ts deliver), the call still waits, and start() refuses
// tools whose script names collide.
// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:291 describe(`deterministic runs (${backend})`) "rejects rejection properties that are not an object, and tools sharing a script name"
func TestDeterministicRunsRejectRejectionPropertiesThatAreNotAnObjectAndToolsSharingAScriptName(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, "return await tools.wait(1)")
		waitUntil(t, func() bool { return r.callCount() == 1 })
		for _, properties := range []string{`null`, `[1]`} {
			err := r.run.Deliver(1, startedAt, 1, codemode.Outcome{Message: "x", Properties: json.RawMessage(properties)})
			if err == nil || !strings.Contains(err.Error(), "not an object") {
				t.Fatalf("properties %s: err = %v, want one saying they are not an object", properties, err)
			}
		}
		if err := r.run.Deliver(1, startedAt, 1, codemode.Outcome{OK: true, Value: json.RawMessage(`1`)}); err != nil {
			t.Fatal(err)
		}
		if got := valueJSON(r.run.Result()); got != `1` {
			t.Fatalf("value = %s", got)
		}
		_, err := sandbox.Start(context.Background(), "", codemode.RunOptions{StartedAt: startedAt, Seed: 1,
			Tools: []codemode.DeclaredTool{{Name: "a-b"}, {Name: "a_b"}}, OnCall: func(codemode.HostCall) {}})
		if err == nil || !strings.Contains(err.Error(), "script names must be unique") {
			t.Fatalf("start with tools sharing a script name: err = %v", err)
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:312 describe(`deterministic runs (${backend})`) "uses the script names the run passes"
func TestDeterministicRunsUseTheScriptNamesTheRunPasses(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, "return [typeof tools.renamed, Object.keys(tools), ALL_TOOLS.map((tool) => tool.name)]", func(o *startOptions) {
			o.Tools = []codemode.DeclaredTool{{Name: "my-tool", JSName: "renamed"}}
		})
		if got := valueJSON(r.run.Result()); got != `["function",["renamed","my-tool"],["renamed"]]` {
			t.Fatalf("value = %s", got)
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:327 describe(`deterministic runs (${backend})`) "ends with an error the script cannot catch when the caller aborts"
func TestDeterministicRunsEndWithAnErrorTheScriptCannotCatchWhenTheCallerAborts(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		r := startRun(t, sandbox, `try { await tools.wait(1); } catch { text("caught"); }
				text("after");`, func(o *startOptions) { o.ctx = ctx })
		waitUntil(t, func() bool { return r.callCount() == 1 })
		cancel(&abortReason{"diverged"})
		result := r.run.Result()
		if result.OK || result.Error.Kind != codemode.ErrorAborted || result.Error.Message != "diverged" || len(result.Output) != 0 {
			t.Fatalf("result = %+v (%+v)", result, result.Error)
		}
		if err := r.run.Deliver(1, startedAt, 1, codemode.Outcome{OK: true, Value: json.RawMessage("null")}); err != nil {
			t.Fatalf("an answer after the run ended: %v", err)
		}
	})
}

type abortReason struct{ text string }

func (a *abortReason) Error() string { return a.text }

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:343 describe(`deterministic runs (${backend})`) "is finished once it has its outcome"
func TestDeterministicRunsAreFinishedOnceTheyHaveTheirOutcome(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, "await tools.wait(1); return 2")
		waitUntil(t, func() bool { return r.callCount() == 1 })
		if r.run.Finished() {
			t.Fatal("finished before the call was answered")
		}
		mustDeliver(t, r.run, 1, startedAt, 1)
		r.run.Result()
		if !r.run.Finished() {
			t.Fatal("not finished after the result")
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:353 describe(`deterministic runs (${backend})`) "rejects a call that is not waiting and a result number used twice"
func TestDeterministicRunsRejectACallThatIsNotWaitingAndAResultNumberUsedTwice(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		r := startRun(t, sandbox, "await Promise.all([tools.wait(1), tools.wait(2)])")
		waitUntil(t, func() bool { return r.callCount() == 2 })
		null := codemode.Outcome{OK: true, Value: json.RawMessage("null")}
		if err := r.run.Deliver(1, startedAt, 99, null); err == nil || !strings.Contains(err.Error(), "Call 99 is not waiting") {
			t.Fatalf("unknown call: %v", err)
		}
		mustDeliver(t, r.run, 1, startedAt, 1)
		for _, seq := range []int{1, 0} {
			if err := r.run.Deliver(seq, startedAt, 2, null); err == nil || !strings.Contains(err.Error(), "used once") {
				t.Fatalf("seq %d: %v", seq, err)
			}
		}
		mustDeliver(t, r.run, 2, startedAt, 2)
		if !r.run.Result().OK {
			t.Fatal("run failed")
		}
	})
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:369 describe("deterministic runs (remote transport)") "applies results in seq order when the transport reorders replies sent in order"
func TestDeterministicRunsApplyResultsInSeqOrderWhenTheTransportReordersRepliesSentInOrder(t *testing.T) {
	sandbox := remoteDeterministic(t, codemode.SandboxOptions{}, true)
	r := startRun(t, sandbox, `const order = [];
			await Promise.all([tools.wait("a").then(() => order.push("a")), tools.wait("b").then(() => order.push("b"))]);
			return order;`)
	waitUntil(t, func() bool { return r.callCount() == 2 })
	a, b := r.call(0), r.call(1)
	mustDeliver(t, r.run, 1, startedAt+1000, a.ID)
	mustDeliver(t, r.run, 2, startedAt+2000, b.ID)
	if got := valueJSON(r.run.Result()); got != `["a","b"]` {
		t.Fatalf("order = %s", got)
	}
}

// Pi 1.2.0 packages/codemode/test/deterministic.test.ts:386 describe("execute() is not deterministic") "reads the real clock and real randomness"
func TestExecuteIsNotDeterministic(t *testing.T) {
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	before := float64(time.Now().UnixMilli())
	first, _ := sandbox.Execute(t.Context(), "return [Date.now(), Math.random()]", codemode.ExecuteOptions{})
	second, _ := sandbox.Execute(t.Context(), "return [Date.now(), Math.random()]", codemode.ExecuteOptions{})
	got := numbers(t, first.Value)
	if got[0] < before || got[0] > float64(time.Now().UnixMilli()) {
		t.Fatalf("Date.now() = %v, not the real clock", got[0])
	}
	if slicesContains(numbers(t, second.Value), got[1]) {
		t.Fatalf("Math.random() repeated: %v", got[1])
	}
}

// vm.ts (VmSession.deliver): a result number used twice, or one already applied, crashes the VM with the sequence message.
// Execution.deliver never sends one, so this arrives over the wire from a client that does.
func TestAVMThatIsToldTheSameResultNumberTwiceCrashes(t *testing.T) {
	var mu sync.Mutex
	var crash string
	done := make(chan struct{})
	seq := 5
	zero := 0.0
	payload := "null"
	request := codemode.RemoteRequest{Data: codemode.VmStartData{Code: "await Promise.all([tools.wait(1), tools.wait(2)])", Tools: []codemode.VmStartTool{{Name: "wait", JSName: "wait"}},
		Globals: []codemode.VmStartGlobal{}, Deterministic: &codemode.VmDeterministic{StartedAt: startedAt, Seed: 1}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go codemode.ServeRemote(ctx, request, func(_ context.Context, messages []codemode.RemoteMessage, reply func(*codemode.RemoteReply, error)) {
		last := messages[len(messages)-1]
		switch last.Type {
		case codemode.MessageCall:
			reply(&codemode.RemoteReply{HostToWorkerMessage: codemode.HostToWorkerMessage{Type: codemode.MessageResult, ID: last.ID, Ok: true, Payload: &payload, Seq: &seq, Now: &zero}}, nil)
		case codemode.MessageCrash:
			mu.Lock()
			crash = last.Message
			mu.Unlock()
			reply(nil, nil)
			close(done)
		default:
			reply(nil, nil)
		}
	}, codemode.ServeOptions{MaxInterruptBudget: deterministicBudget})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the VM was not crashed by a result number used twice")
	}
	mu.Lock()
	defer mu.Unlock()
	if crash != "Result 2 has sequence number 5, expected 1 or later, once" {
		t.Fatalf("crash = %q", crash)
	}
}
