package harness

// Ports packages/durable/test/harness-idle-scopes.test.ts (Pi main 35dfa757): idle checks without per-commit scans. A task change
// settles only the conversations above it (idle waiters and kept context ranges), expiry runs oldest first from an ordered
// index, and wake() reads only sleeping tasks while work is active. Every case checks the incremental idle state against a full
// recompute (TaskScheduler.idleState, scheduler_idle.go).

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
	"github.com/MichaelKinsy/PiG/schema"
)

// idleGates are the gates and read signals of the tasks of one test, by name.
type idleGates struct {
	mu    sync.Mutex
	gates map[string]*deferredGate
	reads map[string]*deferredGate
}

func (g *idleGates) gate(name string) *deferredGate { return g.named(&g.gates, name) }
func (g *idleGates) read(name string) *deferredGate { return g.named(&g.reads, name) }

func (g *idleGates) named(table *map[string]*deferredGate, name string) *deferredGate {
	g.mu.Lock()
	defer g.mu.Unlock()
	if *table == nil {
		*table = map[string]*deferredGate{}
	}
	if (*table)[name] == nil {
		(*table)[name] = deferred()
	}
	return (*table)[name]
}

func (g *idleGates) resolveAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, gate := range g.gates {
		gate.resolve()
	}
}

// idleWork is the task "name" or "name:conversation": it reads the context of the conversation, if one is named, signals the read,
// waits for its gate, and completes. Aborted, it ends aborted.
func idleWork(gates *idleGates) durable.AnyTask {
	return tkOneStep("test.idle.work", func(ctx context.Context, task stepRecord, runtime stepRuntime) error {
		name, read, _ := strings.Cut(task.Input.(string), ":")
		if read != "" {
			id, _ := strconv.ParseInt(read, 10, 64)
			if _, err := runtime.Context(ctx, durable.ConversationId(id), nil); err != nil {
				return err
			}
			gates.read(name).resolve()
		}
		if err := gates.gate(name).wait(ctx); err != nil {
			return err
		}
		return tkComplete(ctx, runtime)
	}).Erased()
}

var idleWake = durable.DefineSignal(durable.SignalDefinition[struct{}]{Name: "test.idle.wake", Version: 1, Schema: schema.Obj(schema.Properties{})})

type idleParkerInput struct {
	Read int64 `json:"read"`
}

type (
	idleParkerRuntime = durable.TaskRuntime[idleParkerInput, stepState, durable.JsonValue, any]
	idleParkerRecord  = durable.RunningTask[idleParkerInput, stepState, durable.JsonValue]
)

// idleParker reads the context of conversation `read`, then parks on `idleWake` until a delivery, and completes.
var idleParker = durable.DefineTask(durable.TaskDefinition[idleParkerInput, stepState, durable.JsonValue, any]{
	Name:    "test.idle.parker",
	Version: 1,
	Initial: func(idleParkerInput) stepState { return stepState{Phase: "run"} },
	Phases: map[string]durable.PhaseHandler[idleParkerInput, stepState, durable.JsonValue, any]{
		"run": func(ctx context.Context, task idleParkerRecord, runtime idleParkerRuntime) error {
			if _, err := runtime.Context(ctx, durable.ConversationId(task.Input.Read), nil); err != nil {
				return err
			}
			return runtime.Commit(ctx, func(durable.Tx, idleParkerRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "woke"}, Signals: []durable.SignalKey{idleWake.Key("go")}}, nil
			})
		},
		"woke": func(ctx context.Context, _ idleParkerRecord, runtime idleParkerRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, idleParkerRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return completed[stepState, durable.JsonValue](nil), nil
			})
		},
	},
	Abort: func(ctx context.Context, _ idleParkerRecord, runtime idleParkerRuntime) error {
		return runtime.Commit(ctx, func(durable.Tx, idleParkerRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
			return abortedWith[stepState, durable.JsonValue]("test"), nil
		})
	},
})

type idleEnvOptions struct {
	clock     *testClock
	retention func() float64
	// timers keeps the background timers; by default they are declined, as Cloudflare Workers do.
	timers bool
	store  durable.Storage
	gates  *idleGates
	// paused leaves the Harness without Resume: its tasks stay pending.
	paused bool
}

type idleEnv struct {
	t         *testing.T
	harness   Harness
	root      Conversation
	scheduler *TaskScheduler
	scan      *scanCount
	gates     *idleGates
	work      durable.AnyTask
}

// openIdle opens a resumed Harness running idleWork, over MemoryStorage counting the entry rows each scan returns, with the
// clock's time, the retention the option returns (10 minutes by default), and background timers declined unless asked.
func openIdle(t *testing.T, options idleEnvOptions) *idleEnv {
	t.Helper()
	env := &idleEnv{t: t, scan: &scanCount{}, gates: options.gates}
	if env.gates == nil {
		env.gates = &idleGates{}
	}
	env.work = idleWork(env.gates)
	retention := options.retention
	if retention == nil {
		retention = func() float64 { return 600_000 }
	}
	store := options.store
	if store == nil {
		store = storage.NewMemoryStorage()
	}
	tk := tkOptions{
		storage:  countingStorage{Storage: store, scan: env.scan},
		settings: func() *HarnessSettings { return &HarnessSettings{ContextRetentionMs: new(retention())} },
	}
	if options.clock != nil {
		tk.now = options.clock.Now
	}
	if !options.timers {
		tk.setTimer = func(callback func(), delayMs float64, background bool) func() {
			if background {
				return nil
			}
			timer := time.AfterFunc(time.Duration(delayMs*float64(time.Millisecond)), callback)
			return func() { timer.Stop() }
		}
	}
	opened := tkOpenRoot(t, []durable.AnyTask{env.work, idleParker.Erased()}, tk)
	env.harness, env.root = opened.harness, opened.root
	env.scheduler = opened.harness.(*harnessImpl).tasks
	if !options.paused {
		env.harness.Resume()
	}
	return env
}

// conversation is a new ownerless conversation with entries entries.
func (env *idleEnv) conversation(entries int) Conversation {
	env.t.Helper()
	created, err := env.harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
	if err != nil {
		env.t.Fatal(err)
	}
	env.addEntries(created, entries)
	return created
}

func (env *idleEnv) addEntries(conversation Conversation, entries int) {
	env.t.Helper()
	for range entries {
		if _, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "note"})
		}); err != nil {
			env.t.Fatal(err)
		}
	}
}

// start starts idleWork named name in into, reading conversation read when it is not 0, and waits for its read.
func (env *idleEnv) start(name string, into Conversation, read durable.ConversationId, background ...bool) durable.TaskId {
	env.t.Helper()
	input := name
	if read != 0 {
		input = fmt.Sprintf("%s:%d", name, read)
	}
	id, err := durable.Commit(testContext, into, func(tx durable.Tx) (durable.TaskId, error) {
		return tx.CreateTaskErased(env.work, input, durable.TaskOptions{Ownership: conversationOwned, Background: len(background) > 0 && background[0]})
	})
	if err != nil {
		env.t.Fatal(err)
	}
	if read != 0 {
		env.waitGate(env.gates.read(name))
	}
	return id
}

func (env *idleEnv) waitGate(gate *deferredGate) {
	env.t.Helper()
	if err := gate.wait(testContext); err != nil {
		env.t.Fatal(err)
	}
}

// end opens the gate of name and waits for its task.
func (env *idleEnv) end(name string, id durable.TaskId) {
	env.t.Helper()
	env.gates.gate(name).resolve()
	if _, err := env.harness.WaitForTask(testContext, id); err != nil {
		env.t.Fatal(err)
	}
}

// rowsOf is the entry rows a read of read from a new task in into scans.
func (env *idleEnv) rowsOf(name string, into Conversation, read durable.ConversationId) int64 {
	env.t.Helper()
	before := env.scan.rows.Load()
	id := env.start(name, into, read)
	rows := env.scan.rows.Load() - before
	env.end(name, id)
	return rows
}

// quiet waits until the scheduler passes the last changes started have settled.
func (env *idleEnv) quiet() {
	env.t.Helper()
	awaitSchedulerSettled(env.t, env.scheduler)
}

// awaitSchedulerSettled waits until no scheduler pass is scheduled or underway (reconcile, sweep, drain, wake), then until every
// goroutine has run up to a wait, so what the passes started has been done.
func awaitSchedulerSettled(t *testing.T, scheduler *TaskScheduler) {
	t.Helper()
	pollUntil(t, func() bool {
		scheduler.mu.Lock()
		defer scheduler.mu.Unlock()
		return !scheduler.reconcileScheduled && scheduler.reconcilingWork == 0 && scheduler.reconciles == 0 &&
			!scheduler.sweepScheduled && !scheduler.wakeScheduled && !scheduler.draining
	})
	awaitQuiescent(t)
}

func (env *idleEnv) close() {
	env.t.Helper()
	env.gates.resolveAll()
	mustClose(env.t, env.harness)
}

func (env *idleEnv) expectState(want SchedulerIdleState, fields string) {
	env.t.Helper()
	got := env.scheduler.idleState()
	check := func(field string, got, want int) {
		if strings.Contains(fields, field) && got != want {
			env.t.Errorf("%s = %d, want %d (state %+v)", field, got, want, env.scheduler.idleState())
		}
	}
	check("contexts", got.Contexts, want.Contexts)
	check("idleContexts", got.IdleContexts, want.IdleContexts)
	check("idleMismatches", got.IdleMismatches, want.IdleMismatches)
	check("fullIdleChecks", got.FullIdleChecks, want.FullIdleChecks)
}

// settledAfterFlush reports whether wait settled once every goroutine has run up to a wait: a wait that its task changes
// allow to settle has settled by then, and one that is still held is parked.
func settledAfterFlush(t *testing.T, wait func() error) bool {
	t.Helper()
	done := make(chan struct{})
	go func() { _ = wait(); close(done) }()
	awaitQuiescent(t)
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func TestIdleWaitersByScope(t *testing.T) {
	t.Run("resolve through owner chains, owned conversations and ended tasks between, ignoring background work", func(t *testing.T) {
		env := openIdle(t, idleEnvOptions{})
		type tree struct {
			p, b, a, d, g durable.TaskId
			c             durable.ConversationId
		}
		built, err := durable.Commit(testContext, env.root, func(tx durable.Tx) (tree, error) {
			var out tree
			var err error
			create := func(name string, options durable.TaskOptions) (durable.TaskId, error) {
				return tx.CreateTaskErased(env.work, name, options)
			}
			if out.p, err = create("p", durable.TaskOptions{Ownership: conversationOwned}); err != nil {
				return out, err
			}
			if out.b, err = create("b", durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: out.p}}); err != nil {
				return out, err
			}
			if out.a, err = create("a", durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: out.b}}); err != nil {
				return out, err
			}
			c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: out.a}})
			if err != nil {
				return out, err
			}
			out.c = c.Id
			if out.d, err = create("d", durable.TaskOptions{Ownership: conversationOwned, ConversationId: &out.c}); err != nil {
				return out, err
			}
			out.g, err = create("g", durable.TaskOptions{Ownership: conversationOwned, ConversationId: &out.c, Background: true})
			return out, err
		})
		if err != nil {
			t.Fatal(err)
		}
		c, err := env.harness.Conversation(testContext, built.c)
		if err != nil || c == nil {
			t.Fatal(err)
		}
		idleC := func() error { return c.WaitForIdle(testContext) }
		idleRoot := func() error { return env.root.WaitForIdle(testContext) }
		idleAll := func() error { return env.harness.WaitForIdle(testContext) }
		type waitResult struct{ done chan struct{} }
		start := func(wait func() error) *waitResult {
			result := &waitResult{done: make(chan struct{})}
			go func() { _ = wait(); close(result.done) }()
			return result
		}
		isSettled := func(result *waitResult) bool {
			awaitQuiescent(t)
			select {
			case <-result.done:
				return true
			default:
				return false
			}
		}
		// A wait that must have settled is waited for: the goroutines it ends on may not have run when awaitQuiescent returns under -race, so a
		// poll of done after the barrier misses a settlement that is still on its way. awaitQuiescent stays for the checks that a wait did not settle.
		mustSettle := func(result *waitResult) bool {
			select {
			case <-result.done:
				return true
			case <-time.After(quiescenceTimeout):
				return false
			}
		}
		waitC, waitRoot, waitAll := start(idleC), start(idleRoot), start(idleAll)
		// The owners A and B hold for D: live, though their handlers returned.
		env.gates.gate("a").resolve()
		env.gates.gate("b").resolve()
		pollUntil(t, func() bool { return tkTaskRecord(t, env.harness, built.b).State.Status == durable.TaskCompleting })
		if isSettled(waitC) || isSettled(waitRoot) || isSettled(waitAll) {
			t.Fatal("a wait settled while D holds A and B")
		}
		// D ends: C is idle (G is background); A and B finalize; P still holds the root.
		env.gates.gate("d").resolve()
		if !mustSettle(waitC) {
			t.Fatal("the wait on C did not settle when D ended")
		}
		if _, err := env.harness.WaitForTask(testContext, built.b); err != nil {
			t.Fatal(err)
		}
		if isSettled(waitRoot) || isSettled(waitAll) {
			t.Fatal("a wait on the root settled while P is live")
		}
		env.gates.gate("p").resolve()
		if !mustSettle(waitRoot) || !mustSettle(waitAll) {
			t.Fatal("the root and the Harness did not settle when P ended")
		}
		if status := tkTaskRecord(t, env.harness, built.g).State.Status; status == durable.TaskTerminal {
			t.Fatal("the background task G ended")
		}
		env.close()
	})

	t.Run("count work whose chain is not loaded yet as inside, until the open reconcile loads it", func(t *testing.T) {
		store := storage.NewMemoryStorage()
		gates := &idleGates{}
		first := openIdle(t, idleEnvOptions{store: store, gates: gates, paused: true})
		type tree struct {
			p, d     durable.TaskId
			c, other durable.ConversationId
		}
		built, err := durable.Commit(testContext, first.root, func(tx durable.Tx) (tree, error) {
			var out tree
			var err error
			if out.p, err = tx.CreateTaskErased(first.work, "p", durable.TaskOptions{Ownership: conversationOwned}); err != nil {
				return out, err
			}
			c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: out.p}})
			if err != nil {
				return out, err
			}
			out.c = c.Id
			if out.d, err = tx.CreateTaskErased(first.work, "d", durable.TaskOptions{Ownership: conversationOwned, ConversationId: &out.c}); err != nil {
				return out, err
			}
			other, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownerless})
			out.other = other.Id
			return out, err
		})
		if err != nil {
			t.Fatal(err)
		}
		// P and D stay live in storage, as a crash leaves them; the first Harness never ran them and is not used again
		// (closing a Harness closes its MemoryStorage here, where Pi reopens a ControlledStorage).
		// Reopened, no chain is loaded: P and D count as inside every scope until the reconcile pass loads theirs.
		second := &idleEnv{t: t, gates: gates, scan: &scanCount{}}
		second.work = idleWork(gates)
		reopened := tkOpenRoot(t, []durable.AnyTask{second.work}, tkOptions{storage: store})
		second.harness, second.root = reopened.harness, reopened.root
		other, err := second.harness.Conversation(testContext, built.other)
		if err != nil || other == nil {
			t.Fatal(err)
		}
		if err := other.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		rootWait := make(chan struct{})
		go func() { _ = second.root.WaitForIdle(testContext); close(rootWait) }()
		awaitQuiescent(t)
		select {
		case <-rootWait:
			t.Fatal("the root is idle while P and D are live")
		default:
		}
		second.harness.Resume()
		gates.gate("d").resolve()
		if _, err := second.harness.WaitForTask(testContext, built.d); err != nil {
			t.Fatal(err)
		}
		awaitQuiescent(t)
		select {
		case <-rootWait:
			t.Fatal("the root is idle while P is live")
		default:
		}
		gates.gate("p").resolve()
		select {
		case <-rootWait:
		case <-time.After(10 * time.Second):
			t.Fatal("the root did not become idle")
		}
		gates.resolveAll()
		mustClose(t, second.harness)
	})

	t.Run("resolve a waiter on an unrelated conversation without settling the busy one", func(t *testing.T) {
		env := openIdle(t, idleEnvOptions{})
		busy, other := env.conversation(0), env.conversation(0)
		held := env.start("held", busy, 0)
		quick := env.start("quick", other, 0)
		idleBusy := func() bool { return settledAfterFlush(t, func() error { return busy.WaitForIdle(testContext) }) }
		idleOther := make(chan struct{})
		go func() { _ = other.WaitForIdle(testContext); close(idleOther) }()
		env.end("quick", quick)
		select {
		case <-idleOther:
		case <-time.After(10 * time.Second):
			t.Fatal("the wait on the unrelated conversation did not settle")
		}
		if idleBusy() {
			t.Fatal("the busy conversation settled")
		}
		env.end("held", held)
		if !idleBusy() {
			t.Fatal("the conversation did not settle when its task ended")
		}
		env.close()
	})

	// scheduler.ts: a commit's task changes are settled at its publication, over the scopes above the tasks that ended
	// (addScopesAbove), not at the reconcile pass after it.
	t.Run("resolve a waiter at the publication of the change that ended the last task below, before the pass after it", func(t *testing.T) {
		env := openIdle(t, idleEnvOptions{})
		busy := env.conversation(0)
		held := env.start("held", busy, 0)
		waiting := make(chan struct{})
		go func() { _ = busy.WaitForIdle(testContext); close(waiting) }()
		awaitQuiescent(t)
		// The task's end queues behind one line job, and a second one is admitted behind that end: the pass its publication
		// schedules takes its place after the second, so only the publication's own check can resolve the waiter meanwhile.
		firstEntered, releaseFirst := holdLine(t, env.harness)
		if err := firstEntered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		env.gates.gate("held").resolve()
		awaitQuiescent(t)
		secondAdmitted, secondEntered, releaseSecond := deferred(), deferred(), deferred()
		second := goCall(func() error {
			_, err := env.harness.Commit(lineadmission.With(testContext, secondAdmitted.resolve), func(durable.Tx) (any, error) {
				secondEntered.resolve()
				return nil, releaseSecond.wait(t.Context())
			})
			return err
		})
		if err := secondAdmitted.wait(testContext); err != nil {
			t.Fatal(err)
		}
		releaseFirst()
		if err := secondEntered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		awaitQuiescent(t)
		select {
		case <-waiting:
		default:
			t.Error("the waiter was not resolved while a job held the line after the task's end was published")
		}
		releaseSecond.resolve()
		if err := second.wait(t); err != nil {
			t.Fatal(err)
		}
		if _, err := env.harness.WaitForTask(testContext, held); err != nil {
			t.Fatal(err)
		}
		env.close()
	})
}

func TestIdleKeptContextRanges(t *testing.T) {
	t.Run("count a conversation whose live work is all parked as inactive, and as busy once it runs again", func(t *testing.T) {
		env := openIdle(t, idleEnvOptions{})
		x := env.conversation(2)
		parker, err := durable.Commit(testContext, x, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, idleParker, idleParkerInput{Read: int64(x.Id())}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: new(x.Id())})
		})
		if err != nil {
			t.Fatal(err)
		}
		pollUntil(t, func() bool { return tkTaskRecord(t, env.harness, parker).State.Status == durable.TaskSleeping })
		env.quiet()
		// Not idle, as a wait for idleness says, but inactive: its context ages like an idle one's.
		if settledAfterFlush(t, func() error { return x.WaitForIdle(testContext) }) {
			t.Fatal("a wait for idleness settled while a task sleeps")
		}
		env.expectState(SchedulerIdleState{Contexts: 1, IdleContexts: 1, IdleMismatches: 0}, "contexts idleContexts idleMismatches")
		running := env.start("running", x, 0)
		env.quiet()
		env.expectState(SchedulerIdleState{Contexts: 1, IdleContexts: 0, IdleMismatches: 0}, "contexts idleContexts idleMismatches")
		env.end("running", running)
		if result, err := Signal(testContext, env.harness, parker, idleWake, "go", struct{}{}); err != nil || result != durable.SignalDelivered {
			t.Fatalf("delivery: %s %v", result, err)
		}
		if _, err := env.harness.WaitForTask(testContext, parker); err != nil {
			t.Fatal(err)
		}
		env.close()
	})

	t.Run("settles a run end whose sweep drops the chain without a check of every scope", func(t *testing.T) {
		env := openIdle(t, idleEnvOptions{})
		type tree struct {
			p, t durable.TaskId
			c    durable.ConversationId
		}
		built, err := durable.Commit(testContext, env.root, func(tx durable.Tx) (tree, error) {
			var out tree
			var err error
			if out.p, err = tx.CreateTaskErased(env.work, "p", durable.TaskOptions{Ownership: conversationOwned}); err != nil {
				return out, err
			}
			if out.t, err = tx.CreateTaskErased(env.work, "t", durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: out.p}}); err != nil {
				return out, err
			}
			c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: out.t}})
			if err != nil {
				return out, err
			}
			out.c = c.Id
			// A context range keeps entries: an empty conversation has none to keep.
			_, err = tx.AppendEntry(out.c, durable.EntryDraft{Kind: "note"})
			return out, err
		})
		if err != nil {
			t.Fatal(err)
		}
		c, err := env.harness.Conversation(testContext, built.c)
		if err != nil || c == nil {
			t.Fatal(err)
		}
		d := env.start("d", c, built.c)
		env.gates.gate("t").resolve()
		pollUntil(t, func() bool { return tkTaskRecord(t, env.harness, built.t).State.Status == durable.TaskCompleting })
		env.quiet()
		before := env.scheduler.idleState()
		env.expectState(SchedulerIdleState{Contexts: 1, IdleContexts: 0, IdleMismatches: 0}, "contexts idleContexts idleMismatches")
		// D's end makes C idle, T finalizes, and the sweep forgets C's chain: none of it checks every scope.
		env.gates.gate("d").resolve()
		if _, err := env.harness.WaitForTask(testContext, d); err != nil {
			t.Fatal(err)
		}
		if _, err := env.harness.WaitForTask(testContext, built.t); err != nil {
			t.Fatal(err)
		}
		env.quiet()
		want := before
		want.IdleContexts = 1
		if got := env.scheduler.idleState(); got != want {
			t.Fatalf("idle state %+v, want %+v", got, want)
		}
		env.gates.gate("p").resolve()
		if _, err := env.harness.WaitForTask(testContext, built.p); err != nil {
			t.Fatal(err)
		}
		env.close()
	})

	t.Run("drops a range past its retention when a task change makes it busy again, with background timers declined", func(t *testing.T) {
		clock := &testClock{now: 1_000}
		env := openIdle(t, idleEnvOptions{clock: clock, retention: func() float64 { return 1_000 }})
		x, z := env.conversation(2), env.conversation(0)
		env.end("reader", env.start("reader", z, x.Id()))
		env.expectState(SchedulerIdleState{Contexts: 1, IdleContexts: 1}, "contexts idleContexts idleMismatches")
		clock.advance(1_500)
		// No timer expired it; the task change that makes X busy checks expiry first.
		busy := env.start("busy", x, 0)
		env.quiet()
		env.expectState(SchedulerIdleState{Contexts: 0, IdleContexts: 0}, "contexts idleContexts idleMismatches")
		env.end("busy", busy)
		env.close()
	})

	t.Run("drops a range past its retention at a reconcile pass with nothing to do, with background timers declined", func(t *testing.T) {
		clock := &testClock{now: 1_000}
		env := openIdle(t, idleEnvOptions{clock: clock, retention: func() float64 { return 1_000 }})
		x, z := env.conversation(2), env.conversation(0)
		env.end("reader", env.start("reader", z, x.Id()))
		held := env.start("held", z, 0)
		// Held runs: no task changes from here.
		env.quiet()
		env.expectState(SchedulerIdleState{Contexts: 1, IdleContexts: 1}, "contexts idleContexts")
		clock.advance(1_500)
		// A wait on busy Z asks for a reconcile pass, which has no work and changes no task: it checks expiry.
		idle := make(chan struct{})
		go func() { _ = z.WaitForIdle(testContext); close(idle) }()
		env.quiet()
		env.expectState(SchedulerIdleState{Contexts: 0, IdleMismatches: 0}, "contexts idleMismatches")
		env.end("held", held)
		select {
		case <-idle:
		case <-time.After(10 * time.Second):
			t.Fatal("the wait did not settle")
		}
		env.close()
	})

	t.Run("expires in idle order when the clock goes back", func(t *testing.T) {
		clock := &testClock{now: 1_000}
		env := openIdle(t, idleEnvOptions{clock: clock, retention: func() float64 { return 1_000 }})
		a, b, z := env.conversation(3), env.conversation(5), env.conversation(0)
		env.end("read a", env.start("read a", z, a.Id()))
		clock.advance(-500)
		env.end("read b", env.start("read b", z, b.Id()))
		env.expectState(SchedulerIdleState{Contexts: 2, IdleContexts: 2, IdleMismatches: 0}, "contexts idleContexts idleMismatches")
		// B went idle at 500 and expires first, though it went idle last.
		clock.advance(1_100)
		env.end("unrelated", env.start("unrelated", z, 0))
		env.expectState(SchedulerIdleState{Contexts: 1, IdleMismatches: 0}, "contexts idleMismatches")
		if rows := env.rowsOf("again b", z, b.Id()); rows != 5 {
			t.Errorf("a read of B scanned %d rows, want 5", rows)
		}
		if rows := env.rowsOf("again a", z, a.Id()); rows != 0 {
			t.Errorf("a read of A scanned %d rows, want 0", rows)
		}
		env.close()
	})

	t.Run("drops every idle range when retention becomes zero, also one that went idle after the clock's now", func(t *testing.T) {
		clock := &testClock{now: 2_000}
		retention := 1_000.0
		var mu sync.Mutex
		env := openIdle(t, idleEnvOptions{clock: clock, retention: func() float64 { mu.Lock(); defer mu.Unlock(); return retention }})
		a, z := env.conversation(2), env.conversation(0)
		env.end("read", env.start("read", z, a.Id()))
		env.expectState(SchedulerIdleState{Contexts: 1, IdleContexts: 1}, "contexts idleContexts")
		// The clock goes back below A's idle start; then retention 0 and an unrelated task change.
		clock.advance(-1_000)
		mu.Lock()
		retention = 0
		mu.Unlock()
		env.end("unrelated", env.start("unrelated", z, 0))
		env.expectState(SchedulerIdleState{Contexts: 0, IdleMismatches: 0}, "contexts idleMismatches")
		env.close()
	})

	t.Run("keeps an idle range's expiry when another task reads it again", func(t *testing.T) {
		clock := &testClock{now: 1_000}
		env := openIdle(t, idleEnvOptions{clock: clock, retention: func() float64 { return 1_000 }})
		a, b, z := env.conversation(3), env.conversation(5), env.conversation(0)
		env.end("read a", env.start("read a", z, a.Id()))
		clock.advance(500)
		env.end("read b", env.start("read b", z, b.Id()))
		clock.advance(300)
		if rows := env.rowsOf("again a", z, a.Id()); rows != 0 {
			t.Errorf("a second read of A scanned %d rows, want 0", rows)
		}
		clock.advance(300)
		env.end("unrelated", env.start("unrelated", z, 0))
		env.expectState(SchedulerIdleState{Contexts: 1, IdleMismatches: 0}, "contexts idleMismatches")
		if rows := env.rowsOf("late b", z, b.Id()); rows != 0 {
			t.Errorf("a read of B scanned %d rows, want 0", rows)
		}
		env.close()
	})

	t.Run("drops a range from the expiry order when an entry commit finds it expired", func(t *testing.T) {
		clock := &testClock{now: 1_000}
		env := openIdle(t, idleEnvOptions{clock: clock, retention: func() float64 { return 1_000 }})
		a, z := env.conversation(2), env.conversation(0)
		env.end("read", env.start("read", z, a.Id()))
		clock.advance(1_500)
		// An entry-only commit settles no idleness: the range is dropped where it would be extended.
		env.addEntries(a, 1)
		env.expectState(SchedulerIdleState{Contexts: 0, IdleContexts: 0, IdleMismatches: 0}, "contexts idleContexts idleMismatches")
		env.close()
	})

	t.Run("drops a range from the expiry order when an entry commit cannot extend it", func(t *testing.T) {
		env := openIdle(t, idleEnvOptions{})
		a, z := env.conversation(0), env.conversation(0)
		appendEntry := func(draft durable.EntryDraft) durable.EntryRecord {
			record, err := durable.Commit(testContext, a, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(a.Id(), draft)
			})
			if err != nil {
				t.Fatal(err)
			}
			return record
		}
		first := appendEntry(durable.EntryDraft{Kind: "note"})
		second := appendEntry(durable.EntryDraft{Kind: "note"})
		appendEntry(durable.EntryDraft{Kind: "summary", Head: &second.Id})
		env.end("read", env.start("read", z, a.Id()))
		env.expectState(SchedulerIdleState{Contexts: 1, IdleContexts: 1, IdleMismatches: 0}, "contexts idleContexts idleMismatches")
		// A marker whose head is older than the range's: the range cannot be extended and is dropped.
		appendEntry(durable.EntryDraft{Kind: "summary", Head: &first.Id})
		env.expectState(SchedulerIdleState{Contexts: 0, IdleContexts: 0, IdleMismatches: 0}, "contexts idleContexts idleMismatches")
		env.close()
	})

	for _, test := range []struct {
		name    string
		changed float64
		kept    int
	}{{"shorter", 400, 0}, {"longer", 10_000, 1}, {"zero", 0, 0}} {
		t.Run("applies a "+test.name+" retention to idle ranges at an unrelated commit", func(t *testing.T) {
			clock := &testClock{now: 1_000}
			retention := 1_000.0
			var mu sync.Mutex
			env := openIdle(t, idleEnvOptions{clock: clock, retention: func() float64 { mu.Lock(); defer mu.Unlock(); return retention }})
			a, z := env.conversation(2), env.conversation(0)
			env.end("read", env.start("read", z, a.Id()))
			clock.advance(500)
			mu.Lock()
			retention = test.changed
			mu.Unlock()
			env.end("unrelated", env.start("unrelated", z, 0))
			env.expectState(SchedulerIdleState{Contexts: test.kept, IdleMismatches: 0}, "contexts idleMismatches")
			env.close()
		})
	}

	t.Run("drops a range that goes idle with zero retention", func(t *testing.T) {
		env := openIdle(t, idleEnvOptions{retention: func() float64 { return 0 }})
		a := env.conversation(2)
		own := env.start("own", a, a.Id())
		env.quiet()
		env.expectState(SchedulerIdleState{Contexts: 1, IdleContexts: 0, IdleMismatches: 0}, "contexts idleContexts idleMismatches")
		env.end("own", own)
		env.quiet()
		env.expectState(SchedulerIdleState{Contexts: 0, IdleMismatches: 0}, "contexts idleMismatches")
		env.close()
	})
}

// idleSeeded is Pi's `seeded` (harness-idle-scopes.test.ts), bit for bit: the state is a float64 and the step is computed in
// IEEE doubles, which is inexact from the second draw on (the product is above 2^53), and math.Mod is `%` on doubles. The product is
// rounded on its own, as the conversion says, so no fused multiply-add changes the result.
func idleSeeded(seed float64) func() float64 {
	state := seed
	return func() float64 {
		state = math.Mod(float64(state*1_103_515_245)+12_345, 2_147_483_648)
		return state / 2_147_483_648
	}
}

// The draws of Pi's generator for seed 42, taken from running it (node 24).
func TestIdleSeededIsPisGenerator(t *testing.T) {
	next := idleSeeded(42)
	for position, want := range []float64{0.5823075897060335, 0.5198186635971069, 0.9149397611618042, 0.6987155675888062, 0.753081202507019, 0.6894334554672241} {
		if got := next(); got != want {
			t.Fatalf("draw %d = %v, want %v", position, got, want)
		}
	}
}

func TestIncrementalIdleStateEqualsAFullRecomputeAfterEveryStepOfRandomNestedWork(t *testing.T) {
	next := idleSeeded(42)
	pick := func(size int) int { return int(math.Floor(next() * float64(size))) }
	env := openIdle(t, idleEnvOptions{})
	// A context range keeps entries: every conversation gets one.
	env.addEntries(env.root, 1)
	conversations := []Conversation{env.root}
	type liveTask struct {
		id           durable.TaskId
		conversation Conversation
	}
	live := map[string]liveTask{}
	// The live tasks in the order they were added: Pi picks from a Map's entries.
	var names []string
	forget := func(name string) {
		delete(live, name)
		names = slices.DeleteFunc(names, func(each string) bool { return each == name })
	}
	count := 0
	for step := range 200 {
		action := next()
		name := fmt.Sprintf("t%d", count)
		count++
		switch {
		case action < 0.1:
			conversations = append(conversations, env.conversation(1))
		case action < 0.45 || len(live) == 0:
			// A task in any conversation, also one whose owner chain ended and was swept; it may read a context.
			into := conversations[pick(len(conversations))]
			var read durable.ConversationId
			if next() < 0.6 {
				read = conversations[pick(len(conversations))].Id()
			}
			background := next() < 0.15
			live[name] = liveTask{id: env.start(name, into, read, background), conversation: into}
			names = append(names, name)
		case action < 0.6:
			// Work a live task owns: a child task, or an owned conversation with a task that reads it.
			ownerName := names[pick(len(names))]
			owner := live[ownerName]
			if state := tkTaskRecord(t, env.harness, owner.id).State.Status; state == durable.TaskTerminal || state == durable.TaskCompleting {
				forget(ownerName)
				continue
			}
			asConversation := next() < 0.5
			type made struct {
				id           durable.TaskId
				conversation durable.ConversationId
			}
			created, err := durable.Commit(testContext, owner.conversation, func(tx durable.Tx) (made, error) {
				ownedByOwner := durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: owner.id}
				if !asConversation {
					id, err := tx.CreateTaskErased(env.work, name, durable.TaskOptions{Ownership: ownedByOwner})
					return made{id: id}, err
				}
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner.id}})
				if err != nil {
					return made{}, err
				}
				if _, err := tx.AppendEntry(child.Id, durable.EntryDraft{Kind: "note"}); err != nil {
					return made{}, err
				}
				id, err := tx.CreateTaskErased(env.work, fmt.Sprintf("%s:%d", name, child.Id), durable.TaskOptions{Ownership: conversationOwned, ConversationId: &child.Id})
				return made{id: id, conversation: child.Id}, err
			})
			if err != nil {
				t.Fatal(err)
			}
			intoConversation := owner.conversation
			if asConversation {
				env.waitGate(env.gates.read(name))
				if handle, err := env.harness.Conversation(testContext, created.conversation); err != nil || handle == nil {
					t.Fatal(err)
				} else {
					conversations = append(conversations, handle)
					intoConversation = handle
				}
			}
			live[name] = liveTask{id: created.id, conversation: intoConversation}
			names = append(names, name)
		default:
			ended := names[pick(len(names))]
			forget(ended)
			env.gates.gate(ended).resolve()
		}
		env.quiet()
		if state := env.scheduler.idleState(); state.IdleMismatches != 0 {
			t.Fatalf("step %d: %d mismatches with a full recompute: %+v", step, state.IdleMismatches, state)
		}
	}
	state := env.scheduler.idleState()
	if state.Contexts <= 5 || state.IdleContexts == 0 || state.IdleContexts >= state.Contexts {
		t.Errorf("idle state %+v: want more than 5 kept ranges, some idle and some busy", state)
	}
	// Every task here is created in a conversation whose chain was swept, so it is live with its chain not loaded until its
	// reservation. Pi loads the chain in the creating commit; 1.4.0+b3a29f11 does not, and settles such a task's scopes once its
	// chain is loaded, so no idle check covers every scope.
	if state.FullIdleChecks != 0 {
		t.Errorf("%d idle checks covered every scope, want 0", state.FullIdleChecks)
	}
	env.close()
}

func TestWakeReadsSleepingTasksAndScansLiveOnesOnlyWhileNothingRuns(t *testing.T) {
	const kind = "test.idle.work"
	seed := func(t *testing.T, store durable.Storage, conversation durable.ConversationId, records ...durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) {
		writes := []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: conversation}}}
		for _, record := range records {
			writes = append(writes, durable.TaskWrite{Value: record})
		}
		commitWrites(t, store, writes...)
	}
	checkpoint := durable.JsonValue(map[string]any{"phase": "run"})
	record := func(conversation durable.ConversationId, id durable.TaskId, state durable.TaskState[durable.JsonValue, durable.JsonValue], mutate ...func(*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue])) durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
		out := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{Id: id, ConversationId: conversation, Kind: kind, Version: 1, Input: fmt.Sprintf("seeded%d", id), State: state}
		for _, change := range mutate {
			change(&out)
		}
		return out
	}
	sleeping := func(until float64) durable.TaskState[durable.JsonValue, durable.JsonValue] {
		return durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &checkpoint, Until: &until}
	}
	pending := durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint}
	abortMarked := func(out *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) {
		out.AbortRequested = true
	}
	at := func(value float64) *float64 { return &value }
	for _, test := range []struct {
		name  string
		build func(conversation durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
		want  Wake
	}{
		{"a sleeping task", func(c durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
			return []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{record(c, 10, sleeping(5_000))}
		}, Wake{At: at(5_000)}},
		{"an abort-marked sleeping task, which runs its handler", func(c durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
			return []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{record(c, 10, sleeping(5_000)), record(c, 11, sleeping(3_000), abortMarked)}
		}, Wake{Active: true, At: at(5_000)}},
		{"a sleeping task that is due", func(c durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
			return []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{record(c, 11, sleeping(500))}
		}, Wake{Active: true}},
		{"a task waiting on a live one", func(c durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
			waiting := durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskWaiting, Checkpoint: &checkpoint, On: []durable.TaskId{10}, Policy: durable.JoinAllSettled}
			return []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{record(c, 10, sleeping(5_000)), record(c, 11, waiting)}
		}, Wake{At: at(5_000)}},
		{"a completing task holding for a sleeping child", func(c durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
			result := durable.JsonValue(nil)
			completing := durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskCompleting, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}
			owner := durable.TaskId(11)
			return []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
				record(c, 11, completing),
				record(c, 10, sleeping(5_000), func(out *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) {
					out.Owner = &owner
				}),
			}
		}, Wake{At: at(5_000)}},
		{"a task no definition can take", func(c durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
			return []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
				record(c, 10, sleeping(5_000)),
				record(c, 11, pending, func(out *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) {
					out.Kind = "test.idle.missing"
				}),
			}
		}, Wake{At: at(5_000)}},
		{"a pending task", func(c durable.ConversationId) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
			return []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{record(c, 10, sleeping(5_000)), record(c, 11, pending)}
		}, Wake{Active: true, At: at(5_000)}},
	} {
		t.Run("with "+test.name, func(t *testing.T) {
			store := storage.NewMemoryStorage()
			conversation := durable.ConversationId(1)
			seed(t, store, conversation, test.build(conversation)...)
			gates := &idleGates{}
			clock := &testClock{now: 1_000}
			opened := tkOpenRoot(t, []durable.AnyTask{idleWork(gates)}, tkOptions{storage: store, now: clock.Now})
			scheduler := opened.harness.(*harnessImpl).tasks
			awaitSchedulerSettled(t, scheduler)
			got := scheduler.Wake()
			if got.Active != test.want.Active || (got.At == nil) != (test.want.At == nil) || (got.At != nil && *got.At != *test.want.At) {
				t.Errorf("wake = %+v, want %+v", got, test.want)
			}
			mustClose(t, opened.harness)
		})
	}

	t.Run("with an invocation running: active, and the earliest sleep still found", func(t *testing.T) {
		store := storage.NewMemoryStorage()
		conversation := durable.ConversationId(1)
		seed(t, store, conversation, record(conversation, 10, sleeping(5_000)))
		gates := &idleGates{}
		clock := &testClock{now: 1_000}
		work := idleWork(gates)
		opened := tkOpenRoot(t, []durable.AnyTask{work}, tkOptions{storage: store, now: clock.Now})
		opened.harness.Resume()
		id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return tx.CreateTaskErased(work, "running", durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		scheduler := opened.harness.(*harnessImpl).tasks
		pollUntil(t, func() bool { return scheduler.invocationCount() == 1 })
		if got := scheduler.Wake(); !got.Active || got.At == nil || *got.At != 5_000 {
			t.Errorf("wake = %+v, want active with at 5000", got)
		}
		gates.gate("running").resolve()
		if _, err := opened.harness.WaitForTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		awaitSchedulerSettled(t, scheduler)
		if got := scheduler.Wake(); got.Active || got.At == nil || *got.At != 5_000 {
			t.Errorf("wake = %+v, want inactive with at 5000", got)
		}
		mustClose(t, opened.harness)
	})
}
