package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	durablestore "github.com/MichaelKinsy/PiG/durable/storage/store"
	"github.com/MichaelKinsy/PiG/env"
)

// Ports apps/p2/src/runtime/worker.ts: locals, registries and natives a session worker host provides to plugins (Chord
// v2), and the worker's core facet.
//
// Forced Go differences. A thrown error is a returned one; an async function is a blocking call with a context; Node's
// `AbortController` is a context; a getter that may throw is a method returning an error. The Harness's models and
// settings proxies are functions read at each use (`HarnessOptions.ModelsFunc`, `Settings`).

// WorkerInit is a worker's start data (JSON, from the server's `start`): `session` from the sessions plugin, more keys from
// others.
type WorkerInit struct {
	// App is the p2 app directory (`apps/p2`): its sources, docs and vendored files, for facets that start or describe them.
	App        string
	DataDir    string
	Fake       bool
	Generation int
	// Values are the contributions of the plugins by key, `session` (a SessionInit as JSON) among them.
	Values *delta.JsonObject
}

// JSON is the start data as the server sends it.
func (init WorkerInit) JSON() *delta.JsonObject {
	return delta.JsonObjectOf("app", init.App, "dataDir", init.DataDir, "fake", init.Fake, "generation", float64(init.Generation), "values", init.Values)
}

// ParseWorkerInit reads the start data a worker got.
func ParseWorkerInit(value delta.JsonValue) (WorkerInit, error) {
	object, _ := value.(*delta.JsonObject)
	if object == nil {
		return WorkerInit{}, errors.New("p2 worker: no start data")
	}
	values, _ := object.Value("values").(*delta.JsonObject)
	generation, _ := object.Value("generation").(float64)
	init := WorkerInit{Generation: int(generation), Values: values}
	init.App, _ = object.Value("app").(string)
	init.DataDir, _ = object.Value("dataDir").(string)
	init.Fake, _ = object.Value("fake").(bool)
	if values == nil || values.Value("session") == nil {
		return WorkerInit{}, errors.New("p2 worker: the start data has no session")
	}
	return init, nil
}

// Session is the session this worker runs (`values.session`).
func (init WorkerInit) Session() (SessionInit, error) {
	return ParseSessionInit(init.Values.Value("session"))
}

// Location is where the session works now. A worker starts at its spawn location (SessionInit) and follows
// `Sessions.relocate` in place (HarnessParts.Relocate): plugins read Value at each use instead of the spawn parameters,
// and subscribe to reopen what they keep for a location (prompt files, workspace watches). Listeners run once the move is
// committed: the Harness's environments and the conversations' agent cwds are already at the new location.
//
// During a move the Harness's environments switch before the location is committed, so Value may still name the old
// location while HarnessParts.Environments already resolves on the new machine. Decide what depends on the machine from the
// environment itself (IsLocalEnvironment), and key what is kept for a location by Version: it changes when a move is
// committed, so anything opened before (also during the switch) is older and opened again.
type SessionLocation struct {
	mu        sync.Mutex
	value     Location
	version   int
	listeners map[int]func(Location)
	next      int
}

// NewSessionLocation is the location a worker starts at.
func NewSessionLocation(value Location) *SessionLocation {
	return &SessionLocation{value: value, listeners: map[int]func(Location){}}
}

// Value is where the session works now.
func (l *SessionLocation) Value() Location {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.value
}

// Version counts committed moves.
func (l *SessionLocation) Version() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.version
}

// Subscribe calls listener with each committed move; it returns the unsubscribe.
func (l *SessionLocation) Subscribe(listener func(Location)) (unsubscribe func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	id := l.next
	l.listeners[id] = listener
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.listeners, id)
	}
}

// Next is the next move, or ctx's error when it ends first.
func (l *SessionLocation) Next(ctx context.Context) (Location, error) {
	moved := make(chan Location, 1)
	stop := l.Subscribe(func(location Location) {
		select {
		case moved <- location:
		default:
		}
	})
	defer stop()
	select {
	case location := <-moved:
		return location, nil
	case <-ctx.Done():
		return Location{}, context.Cause(ctx)
	}
}

// Set commits a move (the Harness, after switching its environments and the conversations).
func (l *SessionLocation) Set(next Location) {
	l.mu.Lock()
	l.value = next
	l.version++
	listeners := make([]func(Location), 0, len(l.listeners))
	for _, id := range slices.Sorted(maps.Keys(l.listeners)) {
		listeners = append(listeners, l.listeners[id])
	}
	l.mu.Unlock()
	for _, listener := range listeners {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					fmt.Fprintln(os.Stderr, "p2 worker: a location listener failed:", recovered)
				}
			}()
			listener(next)
		}()
	}
}

// IsLocalEnvironment is whether execution environment e runs on this machine (not an SSH environment): p2's private data
// directory is denied there. Decided from the environment, not from SessionLocation.Value, which lags behind the
// environments during a move.
func IsLocalEnvironment(e durableenv.ExecutionEnv) bool {
	_, remote := e.(*env.RemoteExecutionEnv)
	return !remote
}

// SpawnLocation is the spawn location of session.
func SpawnLocation(session SessionInit) Location { return Location{Cwd: session.Cwd, Env: session.Env} }

// InputGate waits before input is submitted to a conversation (all entries, in order). A plugin whose part of the prompt
// loads asynchronously registers one, so the first message of a fresh worker never runs without it. Example: the prompt
// plugin waits for its first load of AGENTS.md and skills.
type InputGate func(ctx host.Ctx) error

// InputGates is the registry of input gates.
var InputGates = NewKeyedRegistry[InputGate]("p2.input-gates")

// ConversationConfiguredHook lets plugins synchronize metadata after a browser changes a conversation's agent
// configuration.
type ConversationConfiguredHook func(ctx host.Ctx, conversation harness.Conversation) error

// ConversationConfigured is the registry of those hooks.
var ConversationConfigured = NewKeyedRegistry[ConversationConfiguredHook]("p2.conversation-configured")

// ReadOnlyCheck marks conversations read-only for people: a reason (non-empty) refuses `Chat`'s send, dequeue and
// configure (Stop stays allowed). Example: the subagents plugin's one-shot agents, which belong to the script waiting for
// their answer.
type ReadOnlyCheck func(ctx host.Ctx, conversation int) (reason string, err error)

// ReadOnlyConversations is the registry of read-only checks.
var ReadOnlyConversations = NewKeyedRegistry[ReadOnlyCheck]("p2.read-only-conversations")

// HarnessConfigEntry is model access and run settings for the Harness (the models plugin contributes key "default").
type HarnessConfigEntry struct {
	Models   func() durable.Models
	Settings func() *harness.HarnessSettings
}

// HarnessConfig is the registry of Harness configurations.
var HarnessConfig = NewKeyedRegistry[HarnessConfigEntry]("p2.harness-config")

// ConversationPlacement is where the conversations besides the main one work (the subagents plugin, key "subagents"). A
// conversation either follows the session (subagents spawned without a cwd, one-shot agents) or has its own location,
// environment and cwd, fixed at spawn (a subagent spawned with a cwd: on the caller's environment). A contributor must not
// need the Harness (it would wait for itself): it gets the opening Durable Harness, or a reader of its documents, as an
// argument. Contributed by a facet (no runtime state), so it is there when the Harness opens.
type ConversationPlacement interface {
	// Place commits what an older version left unplaced (a subagent's own cwd without its environment: the environment it
	// was spawned on), then lists the conversations that follow the session and the own locations in use. The opening
	// Harness calls it, and every relocation, and moves the followers whose agent cwd differs to the session's cwd with the
	// root, in one commit, before it is live: nothing can run or resume in a stale cwd first.
	Place(ctx context.Context, harness harness.Harness, session Location, values *delta.JsonObject) (followers []durable.ConversationId, own []Location, err error)
	// Own is a conversation's own location, or nil: it follows the session.
	Own(ctx context.Context, read durable.DocumentReader, id durable.ConversationId) (*Location, error)
}

// ConversationLocations is the registry of conversation placements.
var ConversationLocations = NewKeyedRegistry[ConversationPlacement]("p2.conversation-locations")

// Machine is one machine's connection: environments at its directories (At), and Close ends the connection.
type Machine interface {
	At(cwd string) durableenv.ExecutionEnv
	Close()
}

// HarnessEnvironmentEntry provides execution environments (key "remote"); a remote location cannot use the local default.
type HarnessEnvironmentEntry struct {
	// Open is the machine of the remote environment name. Connecting may wait for its first operation.
	Open func(ctx context.Context, name string) (Machine, error)
	// Describe is where environment name's tools run, for the prompt's `cwd` section, e.g. `the SSH host gpu-box` (nil: no
	// description; ok false: none for this one).
	Describe func(ctx context.Context, name string) (text string, ok bool, err error)
	// Sandbox is whether environment name is a sandbox, decided by its configuration's type (never its name):
	// conversations working there cannot leave it. Cheap: read on every confinement check.
	Sandbox func(name string) bool
}

// HarnessEnvironments is the registry of environment providers.
var HarnessEnvironments = NewKeyedRegistry[HarnessEnvironmentEntry]("p2.harness-environments")

// RelocationCheck refuses a move to `to` (from the session's current location), checked under the fence before anything
// moves. A message refuses. Conversations with their own location never refuse: they stay where they are.
type RelocationCheck func(ctx context.Context, harness harness.Harness, from, to Location) (refusal string, err error)

// RelocationChecks is the registry of relocation checks.
var RelocationChecks = NewKeyedRegistry[RelocationCheck]("p2.relocation-checks")

// Liveness is what keeps the worker process alive besides held references (Chord's own count): its Durable tasks, calls
// under way (Call), viewers plugins count themselves, and the relocation fence. A JSON watchable of those counts
// (`workerMain`'s `busy`): the worker is idle when every count is zero.
type Liveness struct {
	mu         sync.Mutex
	relocating bool
	// fenceHeld is whether the fence's active call is counted: Unfence lifts the admission fence at once, Unfreeze also
	// releases the count.
	fenceHeld bool
	stopping  bool
	counts     map[string]int
	watches    map[*livenessWatch]struct{}
}

// NewLiveness is a Liveness with every count zero.
func NewLiveness() *Liveness {
	return &Liveness{counts: map[string]int{"activeCalls": 0, "viewers": 0, "durableTasks": 0}, watches: map[*livenessWatch]struct{}{}}
}

func (l *Liveness) object() *delta.JsonObject {
	object := delta.NewJsonObject(len(livenessKinds))
	for _, kind := range livenessKinds {
		object.Set(kind, float64(l.counts[kind]))
	}
	return object
}

// livenessKinds are the counts, in the order the value lists them: the worker's own, then its host's (`host.Activity`).
var livenessKinds = []string{"activeCalls", "viewers", "durableTasks", "references", "calls", "jobs", "lingering"}

// FollowActivity counts what keeps h busy (the references held on it, its calls, jobs and lingering objects, Chord's own
// keep-alive counts) as part of the worker's: the worker is idle when those are zero too.
func (l *Liveness) FollowActivity(h host.Host) error {
	watch, err := h.Activity().Watch(chordctx.Background())
	if err != nil {
		return err
	}
	apply := func(value delta.JsonValue) {
		counts, _ := value.(*delta.JsonObject)
		if counts == nil {
			return
		}
		for _, kind := range []string{"references", "calls", "jobs", "lingering"} {
			count, _ := counts.Value(kind).(float64)
			l.set(kind, int(count))
		}
	}
	apply(watch.Value())
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			apply(call.Value)
		}
		return nil
	})
	return nil
}

// Value is the counts.
func (l *Liveness) Value() (delta.JsonValue, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.object(), nil
}

// Counts is each count now.
func (l *Liveness) Counts() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.counts)
}

// Watch is a watch of the counts (only `WorkerMain` watches): it gets the newest counts at each change.
func (l *Liveness) Watch(context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	watch := &livenessWatch{liveness: l, closed: make(chan struct{}), initial: l.object()}
	l.watches[watch] = struct{}{}
	return watch, nil
}

type livenessWatch struct {
	liveness *Liveness
	initial  *delta.JsonObject
	closed   chan struct{}
	once     sync.Once
	mu       sync.Mutex
	listener func(ctx context.Context, call chord.WatchCall[delta.JsonValue]) error
	last     *delta.JsonObject
}

func (w *livenessWatch) Value() delta.JsonValue {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last != nil {
		return w.last
	}
	return w.initial
}

func (w *livenessWatch) Start(listener func(ctx context.Context, call chord.WatchCall[delta.JsonValue]) error) {
	w.mu.Lock()
	w.listener = listener
	w.mu.Unlock()
}

func (w *livenessWatch) notify(counts *delta.JsonObject) {
	w.mu.Lock()
	listener := w.listener
	w.last = counts
	w.mu.Unlock()
	if listener != nil {
		_ = listener(chordctx.Background(), chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: counts, Ops: []delta.Op{{"r", counts}}})
	}
}

func (w *livenessWatch) Stop() (chord.WatchEnd, error) {
	w.once.Do(func() {
		w.liveness.mu.Lock()
		delete(w.liveness.watches, w)
		w.liveness.mu.Unlock()
		close(w.closed)
	})
	return chord.WatchEnd{Kind: chord.WatchStopped}, nil
}

func (w *livenessWatch) Closed() <-chan struct{} { return w.closed }

func (w *livenessWatch) End() chord.WatchEnd { return chord.WatchEnd{Kind: chord.WatchStopped} }

// set sets a count and tells the watches (outside the lock).
func (l *Liveness) set(kind string, count int) {
	l.mu.Lock()
	l.counts[kind] = count
	counts := l.object()
	watches := slices.Collect(maps.Keys(l.watches))
	l.mu.Unlock()
	for _, watch := range watches {
		watch.notify(counts)
	}
}

func (l *Liveness) add(kind string, by int) {
	l.mu.Lock()
	count := l.counts[kind] + by
	l.mu.Unlock()
	l.set(kind, count)
}

// AddActiveCalls counts calls under way.
func (l *Liveness) AddActiveCalls(by int) { l.add("activeCalls", by) }

// AddViewers counts viewers plugins count themselves.
func (l *Liveness) AddViewers(by int) { l.add("viewers", by) }

// SetDurableTasks sets the Durable tasks' count.
func (l *Liveness) SetDurableTasks(count int) {
	l.mu.Lock()
	same := l.counts["durableTasks"] == count
	l.mu.Unlock()
	if !same {
		l.set("durableTasks", count)
	}
}

// Stopping is whether the worker process is shutting down (set before its host closes). Disposal code tells a stop, after
// which Durable resumes interrupted work in the next worker, from a plugin removed by a reload, whose work must end now.
func (l *Liveness) Stopping() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopping
}

// Stop marks the process as shutting down.
func (l *Liveness) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopping = true
}

// Relocating is whether admissions are fenced for a relocation (`WorkerBoundary.prepare` accepted, not yet moved or
// cancelled).
func (l *Liveness) Relocating() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.relocating
}

// Freeze fences new admissions before checking Durable state; a failed check releases the fence. The fence counts as an
// active call, so the worker cannot stop idle between the server's prepare and its relocate.
func (l *Liveness) Freeze() bool {
	l.mu.Lock()
	if l.relocating || l.counts["activeCalls"] > 0 {
		l.mu.Unlock()
		return false
	}
	l.relocating = true
	l.fenceHeld = true
	l.mu.Unlock()
	l.AddActiveCalls(1)
	return true
}

// Unfence lifts the admission fence only: calls are admitted again, the worker still counts the fence as an active call
// (Unfreeze releases it). It is a flag flip, so a relocation may do it inside the commit that makes its move visible.
func (l *Liveness) Unfence() {
	l.mu.Lock()
	l.relocating = false
	l.mu.Unlock()
}

// Unfreeze lifts the fence and releases its active call.
func (l *Liveness) Unfreeze() {
	l.mu.Lock()
	l.relocating = false
	held := l.fenceHeld
	l.fenceHeld = false
	l.mu.Unlock()
	if held {
		l.AddActiveCalls(-1)
	}
}

// ErrRelocating is what a call made during a move fails with.
var ErrRelocating = errors.New("Working directory is changing; wait before sending another message")

// Call runs fn as one active call.
func (l *Liveness) Call(fn func() error) error {
	if l.Relocating() {
		return ErrRelocating
	}
	l.AddActiveCalls(1)
	defer l.AddActiveCalls(-1)
	return fn()
}

// WorkerParts is the worker process: its start data, liveness counters, and where the session works now (read Location).
type WorkerParts struct {
	Init     WorkerInit
	Liveness *Liveness
	Location *SessionLocation
	// Root runs fn in a root of the worker's own (HOST) under ctx: for code outside Chord calls (a Durable tool or section
	// passes its Durable context), or a method acting as the worker (chordctx.WithoutAbortSignal(ctx)).
	Root func(fn func(ctx host.Ctx) (any, error), ctx context.Context) (any, error)
	// CopyFilesTo copies this session's files (the kit's `Files`: its index and blobs) into the directory of the new session
	// `session`, which must not exist yet: a fork, whose worker waits while it copies. The session goes on meanwhile.
	CopyFilesTo func(ctx context.Context, session string) error
}

// WorkerProcess is the registry of the worker process's natives, contributed by the core facet from the start: read the
// value with WorkerOf (`.Init.DataDir`, `.Liveness.Call(...)`, `.Location.Value()`). Read Location, not Init's session, for
// the cwd.
var WorkerProcess = singleNative[WorkerParts]("p2.worker-process")

// HarnessParts is the session's Harness and its environments, opened by the core (HarnessHost), read at each use.
type HarnessParts struct {
	// Durable is the session's Harness.
	Durable func() (harness.Harness, error)
	// Environments are every conversation's location and environment (also supplied to Durable): the session's for the main
	// conversation and its followers, a subagent's own (its environment and cwd, e.g. a git worktree) otherwise. Resolve at
	// each use: a relocation changes the session's.
	Environments func() (ExecutionEnvironments, error)
	// Relocate moves the session to location in place (the server's `WorkerBoundary.relocate`, while admissions are fenced
	// and nothing runs): opens the destination's environment, switches the session's location, commits the root and its
	// followers to the new cwd, commits the SessionLocation (its listeners reopen what they keep), then retires the
	// environments nothing uses any more. unfence, when set, lifts the admission fence inside the commit that makes the
	// move visible (the SessionLocation is committed before it, so what is admitted from then on works at the new
	// location), as Pi's fence is lifted before a client can observe the commit; nil when no fence is up.
	Relocate func(ctx context.Context, location Location, unfence func()) error
}

// HarnessNative is the registry of the session's Harness (the core's contribution once it is open).
var HarnessNative = singleNative[HarnessParts]("p2.harness")

// HarnessHost is the core's host-lifetime object that opens the Harness (an exclusive store: one per worker). A factory
// that needs the Harness at activation declares it: HarnessOf(self) parks the attempt until the Harness is open.
var HarnessHost = mustContract(contracts.Local("p2.harness-host", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))

// HarnessOf is the session's Harness for a factory: declares HarnessHost (so the attempt waits for it) and reads it at each
// use.
func HarnessOf(self host.Self) func() (*HarnessParts, error) {
	self.Get(HarnessHost, delta.NewJsonObject(0))
	return nativeOf[HarnessParts](self.Registry(HarnessNative), "The session's Harness is not open")
}

// WorkerOf is the worker process's natives for a factory (the core contributes them before anything activates).
func WorkerOf(self host.Self) func() (*WorkerParts, error) {
	return nativeOf[WorkerParts](self.Registry(WorkerProcess), "No worker process (the core facet contributes it)")
}

// ServeWorkerRegistries serves the base keyed registries of a worker (the core facet; test hosts without one).
func ServeWorkerRegistries(facet host.FacetHost) error {
	for _, registry := range []interface{ Serve(host.FacetHost) error }{
		Extensions, ExtensionSwitches, InputGates, ConversationConfigured, ReadOnlyConversations, HarnessConfig,
		HarnessEnvironments, RelocationChecks, ConversationLocations,
	} {
		if err := registry.Serve(facet); err != nil {
			return err
		}
	}
	return nil
}

// ExecutionEnvironments are the execution environments of a session's conversations, each at its conversation's location.
type ExecutionEnvironments interface {
	// Session is where the session works now: its main conversation and the conversations following it.
	Session() Location
	// Locate is where conversation id works: its own location (a subagent spawned with a cwd), else the session's. read
	// reads its documents (a tool's or a prompt section's reader; nil: the Harness).
	Locate(ctx context.Context, id durable.ConversationId, read durable.DocumentReader) (Location, error)
	// At is the environment at location: one per directory, on one connection per machine, opened at its first use.
	At(ctx context.Context, location Location) (durableenv.ExecutionEnv, error)
	// Sandbox is env itself when it is a sandbox (see HarnessEnvironmentEntry.Sandbox), else "" (this device never is).
	Sandbox(ctx context.Context, env *string) (string, error)
}

// ConversationConfinement is the sandbox conversation id is confined to: the environment it works in, when that is a
// sandbox ("" otherwise). Decided from its current location at each call, so it holds per conversation (a subagent placed
// in a sandbox is confined, its main agent elsewhere is not) and follows moves.
func ConversationConfinement(ctx context.Context, environments ExecutionEnvironments, id durable.ConversationId, read durable.DocumentReader) (string, error) {
	location, err := environments.Locate(ctx, id, read)
	if err != nil {
		return "", err
	}
	return environments.Sandbox(ctx, location.Env)
}

// ConversationEnvironment is where conversation id works and its environment there.
func ConversationEnvironment(ctx context.Context, environments ExecutionEnvironments, id durable.ConversationId, read durable.DocumentReader) (Location, durableenv.ExecutionEnv, error) {
	location, err := environments.Locate(ctx, id, read)
	if err != nil {
		return Location{}, nil, err
	}
	executionEnv, err := environments.At(ctx, location)
	return location, executionEnv, err
}

type machineEntry struct {
	ready   chan struct{}
	machine Machine
	err     error
	envs    map[string]durableenv.ExecutionEnv
}

// execution is the session's execution environments: one machine (connection) per environment name, opened lazily and
// shared by every conversation working there, with one environment per directory. A relocation switches the session's
// location (the conversations following it locate there from then on), then retires what nothing uses any more: the old
// cwd's environment, the old machine unless a conversation with its own location works on it. Conversations with their own
// location keep their environments.
type execution struct {
	open    func(ctx context.Context, env *string) (Machine, error)
	sandbox func(ctx context.Context, env string) (bool, error)
	own     func(ctx context.Context, read durable.DocumentReader, id durable.ConversationId) (*Location, error)

	mu       sync.Mutex
	session  Location
	machines map[string]*machineEntry
	closing  context.Context
	close    func(cause error)
}

var errEnvironmentsClosed = errors.New("The session's environments are closed")

func newExecution(session Location, open func(context.Context, *string) (Machine, error), sandbox func(context.Context, string) (bool, error), own func(context.Context, durable.DocumentReader, durable.ConversationId) (*Location, error)) *execution {
	e := &execution{session: session, open: open, sandbox: sandbox, own: own, machines: map[string]*machineEntry{}}
	e.closing, e.close = chordctx.WithCancel(chordctx.Background())
	return e
}

func (e *execution) Sandbox(ctx context.Context, env *string) (string, error) {
	if env == nil {
		return "", nil
	}
	sandboxed, err := e.sandbox(ctx, *env)
	if err != nil || !sandboxed {
		return "", err
	}
	return *env, nil
}

func (e *execution) Session() Location {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.session
}

func (e *execution) Locate(ctx context.Context, id durable.ConversationId, read durable.DocumentReader) (Location, error) {
	session := e.Session()
	if id == durable.ROOT_CONVERSATION_ID {
		return session, nil
	}
	own, err := e.own(ctx, read, id)
	if err != nil || own == nil {
		return session, err
	}
	return *own, nil
}

func (e *execution) At(ctx context.Context, location Location) (durableenv.ExecutionEnv, error) {
	for {
		if e.closing.Err() != nil {
			return nil, errEnvironmentsClosed
		}
		key := ""
		if location.Env != nil {
			key = *location.Env
		}
		e.mu.Lock()
		entry := e.machines[key]
		if entry == nil {
			entry = &machineEntry{ready: make(chan struct{}), envs: map[string]durableenv.ExecutionEnv{}}
			e.machines[key] = entry
			go func() {
				// A panic of the open is its failure, as a rejected promise is.
				defer func() {
					if recovered := recover(); recovered != nil {
						entry.err = fmt.Errorf("opening the environment: %v", recovered)
						e.mu.Lock()
						if e.machines[key] == entry {
							delete(e.machines, key)
						}
						e.mu.Unlock()
						close(entry.ready)
					}
				}()
				entry.machine, entry.err = e.open(e.closing, location.Env)
				// A failed open is not kept: the next use tries again.
				if entry.err != nil {
					e.mu.Lock()
					if e.machines[key] == entry {
						delete(e.machines, key)
					}
					e.mu.Unlock()
				}
				close(entry.ready)
			}()
		}
		e.mu.Unlock()
		select {
		case <-entry.ready:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
		if entry.err != nil {
			return nil, entry.err
		}
		e.mu.Lock()
		// Retired while opening: open it again (or refuse, once closed).
		if e.machines[key] != entry {
			e.mu.Unlock()
			continue
		}
		executionEnv := entry.envs[location.Cwd]
		if executionEnv == nil {
			executionEnv = entry.machine.At(location.Cwd)
			entry.envs[location.Cwd] = executionEnv
		}
		e.mu.Unlock()
		return executionEnv, nil
	}
}

// switchTo moves the session to next; the old environments stay until retire.
func (e *execution) switchTo(next Location) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.session = next
}

// retire ends the environments outside used (their running commands killed) and closes the machines left without any.
// Nothing runs meanwhile: the worker is fenced.
func (e *execution) retire(used []Location) {
	var ending []durableenv.ExecutionEnv
	var closing []*machineEntry
	e.mu.Lock()
	for key, entry := range e.machines {
		var kept []Location
		for _, location := range used {
			name := ""
			if location.Env != nil {
				name = *location.Env
			}
			if name == key {
				kept = append(kept, location)
			}
		}
		for cwd, executionEnv := range entry.envs {
			if !slices.ContainsFunc(kept, func(location Location) bool { return location.Cwd == cwd }) {
				delete(entry.envs, cwd)
				ending = append(ending, executionEnv)
			}
		}
		if len(kept) == 0 {
			delete(e.machines, key)
			closing = append(closing, entry)
		}
	}
	e.mu.Unlock()
	var group sync.WaitGroup
	for _, executionEnv := range ending {
		group.Go(func() { _ = executionEnv.Cleanup(chordctx.Background()) })
	}
	group.Wait()
	for _, entry := range closing {
		<-entry.ready
		if entry.err == nil {
			entry.machine.Close()
		}
	}
}

func (e *execution) Close() {
	e.close(errEnvironmentsClosed)
	e.retire(nil)
}

// localMachine is this machine: LocalExecutionEnv where pi-env ships a daemon for it, so the file index lists through it
// (ListFiles), else NodeExecutionEnv. The daemon starts at the first listing; Close stops it.
func localMachine() Machine {
	daemon, err := env.LocalDaemon()
	if err != nil {
		daemon = ""
	}
	if _, statErr := os.Stat(daemon); daemon == "" || statErr != nil {
		return nodeMachine{}
	}
	return &daemonMachine{connection: env.NewConnection(env.ConnectionOptions{Command: []string{daemon}})}
}

type nodeMachine struct{}

func (nodeMachine) At(cwd string) durableenv.ExecutionEnv {
	return node.NewNodeExecutionEnv(node.NodeExecutionEnvOptions{Cwd: cwd, ShellPath: os.Getenv("SHELL")})
}

func (nodeMachine) Close() {}

type daemonMachine struct{ connection *env.Connection }

func (m *daemonMachine) At(cwd string) durableenv.ExecutionEnv {
	return env.NewLocalExecutionEnv(env.LocalExecutionEnvOptions{
		NodeExecutionEnvOptions: node.NodeExecutionEnvOptions{Cwd: cwd, ShellPath: os.Getenv("SHELL")},
		Connection:              m.connection,
	})
}

func (m *daemonMachine) Close() { m.connection.Close() }

// followSession moves the top-level conversations (the root and its `/tree` branches) and the conversations following the
// session to session.Cwd in one commit (those already there stay untouched), after the placement provider committed what
// an older version left unplaced. Children copied the session's cwd when created, so a move leaves them behind; this runs at
// every start (sessions moved before subagents tracked their own cwd still hold the old path) and at every in-place
// relocation. Nothing resolves against this process's cwd. It returns the own locations of the other conversations.
//
// moved, when set (a relocation), runs before the commit, and unfence inside it, once its writes are made (with no commit
// to make, moved then unfence run in a row).
func followSession(ctx context.Context, place func(ctx context.Context, h harness.Harness, session Location) ([]durable.ConversationId, []Location, error), h harness.Harness, session Location, moved, unfence func()) ([]Location, error) {
	var followers []durable.ConversationId
	var own []Location
	if place != nil {
		var err error
		if followers, own, err = place(ctx, h, session); err != nil {
			return nil, err
		}
	}
	top, err := TopLevelConversations(ctx, h)
	if err != nil {
		return nil, err
	}
	var stale []durable.ConversationId
	for _, id := range append(top, followers...) {
		conversation, err := h.Conversation(ctx, id)
		if err != nil {
			return nil, err
		}
		if conversation == nil {
			continue
		}
		agent, err := conversation.Agent(ctx)
		if err != nil {
			return nil, err
		}
		if agent.Cwd != nil && *agent.Cwd != session.Cwd && !slices.Contains(stale, id) {
			stale = append(stale, id)
		}
	}
	if moved != nil {
		moved()
	}
	if len(stale) > 0 {
		if _, err := h.Commit(ctx, func(tx durable.Tx) (any, error) {
			for _, id := range stale {
				if err := harness.Configure(tx, id, harness.AgentChange{Cwd: harness.SetTo(session.Cwd)}); err != nil {
					return nil, err
				}
			}
			if unfence != nil {
				unfence()
			}
			return nil, nil
		}); err != nil {
			return nil, err
		}
	} else if unfence != nil {
		unfence()
	}
	return own, nil
}

// whenPresent waits for entry key of a keyed registry; it fails when ctx ends first.
func whenPresent[V any](ctx context.Context, source *registries.RegistryValueSource, key string) (V, error) {
	present := make(chan V, 1)
	stop := Follow[any](chord.NativeWatchable[any](source), func(value any) {
		if table, ok := value.(*KeyedTable[V]); ok {
			if entry, ok := table.Get(key); ok {
				select {
				case present <- entry:
				default:
				}
			}
		}
	})
	defer stop()
	select {
	case entry := <-present:
		return entry, nil
	case <-ctx.Done():
		var zero V
		return zero, context.Cause(ctx)
	}
}

// openHarness opens the session's Harness in the HarnessHost object: the execution environments first (the session's
// machine opens now: a start fails visibly where it cannot), then Durable once the models plugin contributed its
// configuration, with the root conversation created from the session's model and cwd. Scheduling starts with `Resume()`
// once the worker's plugins are loaded (the worker entry). It returns the contribution: the natives, read at each use.
func openHarness(self host.StoreSelf, init WorkerInit, session SessionInit, location *SessionLocation, store chordstore.Store) *HarnessParts {
	config := self.Registry(HarnessConfig.Registry)
	environments := self.Registry(HarnessEnvironments.Registry)
	placements := self.Registry(ConversationLocations.Registry)
	extensions := self.Registry(Extensions.Registry)
	switches := self.Registry(ExtensionSwitches.Registry)
	configured := func() (HarnessConfigEntry, bool) {
		table, _ := config.Value().(*KeyedTable[HarnessConfigEntry])
		return table.Get("default")
	}
	// Model access is looked up at each use: the models plugin may reload or replace its configuration.
	models := func() durable.Models {
		entry, ok := configured()
		if !ok {
			panic("No model configuration installed (models plugin)")
		}
		return entry.Models()
	}
	settings := func() *harness.HarnessSettings {
		if entry, ok := configured(); ok {
			return entry.Settings()
		}
		return nil
	}
	// A machine: this one, or a remote environment's from the plugin providing them.
	open := func(ctx context.Context, name *string) (Machine, error) {
		if name == nil {
			return localMachine(), nil
		}
		remote, err := whenPresent[HarnessEnvironmentEntry](ctx, environments, "remote")
		if err != nil {
			return nil, err
		}
		return remote.Open(ctx, *name)
	}
	// Whether a remote environment is a sandbox, once the plugin providing them is there.
	sandbox := func(ctx context.Context, name string) (bool, error) {
		remote, err := whenPresent[HarnessEnvironmentEntry](ctx, environments, "remote")
		if err != nil {
			return false, err
		}
		return remote.Sandbox(name), nil
	}
	place := func(ctx context.Context, h harness.Harness, at Location) ([]durable.ConversationId, []Location, error) {
		table, _ := placements.Value().(*KeyedTable[ConversationPlacement])
		placement, ok := table.Get("subagents")
		if !ok {
			return nil, nil, nil
		}
		return placement.Place(ctx, h, at, init.Values)
	}
	var readerMu sync.Mutex
	var reader durable.DocumentReader
	executionNative := self.Open(
		func(ctx context.Context) (any, error) {
			created := newExecution(location.Value(), open, sandbox, func(ctx context.Context, read durable.DocumentReader, id durable.ConversationId) (*Location, error) {
				readerMu.Lock()
				documents := read
				if documents == nil {
					documents = reader
				}
				readerMu.Unlock()
				if documents == nil {
					return nil, errors.New("The session's Harness is not open")
				}
				table, _ := placements.Value().(*KeyedTable[ConversationPlacement])
				placement, ok := table.Get("subagents")
				if !ok {
					return nil, nil
				}
				return placement.Own(ctx, documents, id)
			})
			if _, err := created.At(ctx, location.Value()); err != nil {
				created.Close()
				return nil, err
			}
			return created, nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*execution).Close()
			return nil
		},
	)
	durableNative := self.Open(
		func(ctx context.Context) (any, error) {
			// Generation needs model access: open once the models plugin has contributed it.
			if _, err := whenPresent[HarnessConfigEntry](ctx, config, "default"); err != nil {
				return nil, err
			}
			// Durable's records in the session's store, beside the kit's `Files`.
			storage, err := DurableStorage(ctx, store)
			if err != nil {
				return nil, err
			}
			executionValue, err := executionNative.Value()
			if err != nil {
				return nil, err
			}
			executions := executionValue.(*execution)
			h, err := harness.OpenHarness(ctx, storage, harness.HarnessOptions{
				ModelsFunc: models,
				Registry:   DurableRegistry(extensions, switches),
				Settings:   settings,
				// The conversation's location, not its agent cwd alone: a subagent's own may be on another machine.
				Env: func(ctx context.Context, target harness.EnvTarget) (durableenv.ExecutionEnv, error) {
					at, err := executions.Locate(ctx, target.ConversationId, target.Read)
					if err != nil {
						return nil, err
					}
					return executions.At(ctx, at)
				},
				OnReport: func(report durable.Report) { fmt.Fprintf(os.Stderr, "durable: %s: %v\n", report.Kind, report.Error) },
			})
			if err != nil {
				return nil, err
			}
			readerMu.Lock()
			reader = h
			readerMu.Unlock()
			root, err := h.Root(ctx, &harness.RootOptions{Agent: &harness.AgentChange{
				Model:         harness.SetTo(session.Model),
				Cwd:           harness.SetTo(location.Value().Cwd),
				ThinkingLevel: harness.SetTo(session.ThinkingLevel),
			}})
			if err == nil {
				// Before the Harness is live: nothing can run or resume in a stale cwd first.
				_, err = followSession(ctx, place, h, location.Value(), nil, nil)
			}
			var agent durable.Agent
			if err == nil {
				agent, err = root.Agent(ctx)
			}
			// As Pi Mobile: a root model no longer in the configuration fails the start visibly (the session's worker error,
			// with Retry), not on the first turn.
			if err == nil && (agent.Model == nil || models().GetModel(agent.Model.Provider, agent.Model.ModelId) == nil) {
				err = errors.New("Unknown model")
			}
			if err != nil {
				_ = h.Close(context.WithoutCancel(ctx))
				return nil, err
			}
			return h, nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			_ = value.(harness.Harness).Close(chordctx.Background())
			return nil
		},
	)
	return &HarnessParts{
		Durable: func() (harness.Harness, error) {
			value, err := durableNative.Value()
			if err != nil {
				return nil, err
			}
			return value.(harness.Harness), nil
		},
		Environments: func() (ExecutionEnvironments, error) {
			value, err := executionNative.Value()
			if err != nil {
				return nil, err
			}
			return value.(*execution), nil
		},
		Relocate: func(ctx context.Context, next Location, unfence func()) error {
			if SameLocation(next, location.Value()) {
				return nil
			}
			durableValue, err := durableNative.Value()
			if err != nil {
				return err
			}
			executionValue, err := executionNative.Value()
			if err != nil {
				return err
			}
			executions := executionValue.(*execution)
			// The destination first: a machine that cannot be opened refuses before anything moves. Then the session's
			// location, the agent cwds and the SessionLocation (its listeners reopen on the new environments); what nothing
			// uses any more goes last. A failure leaves the worker fenced: the server restarts it from the catalog.
			if _, err := executions.At(ctx, next); err != nil {
				return err
			}
			executions.switchTo(next)
			// The SessionLocation is committed before the agent cwds' commit, which lifts the fence: what is admitted after it
			// reads the new location. A failure leaves the worker fenced or moved, and the server restarts it from the catalog.
			own, err := followSession(ctx, place, durableValue.(harness.Harness), next, func() { location.Set(next) }, unfence)
			if err != nil {
				return err
			}
			executions.retire(append([]Location{next}, own...))
			return nil
		},
	}
}

// ResolveDirectory is target as a location of an existing directory, or why it is not one (a non-empty problem): on the
// caller's environment its path is relative to from.Cwd, on another one relative to that environment's home (`~` expands on
// both). Resolved through the worker's environments, so another machine is opened (and kept until nothing uses it).
func ResolveDirectory(ctx context.Context, environments ExecutionEnvironments, target LocationInput, from Location) (location Location, problem string) {
	same := sameEnv(target.Env, from.Env)
	typed := strings.TrimSpace(target.Path)
	path := typed
	switch {
	case typed == "" && same:
		path = from.Cwd
	case typed == "":
		path = "~"
	case !same && !IsRooted(typed):
		path = "~/" + typed
	}
	cwd := "/"
	if same {
		cwd = from.Cwd
	}
	executionEnv, err := environments.At(ctx, Location{Env: target.Env, Cwd: cwd})
	if err != nil {
		return Location{}, err.Error()
	}
	absolute, err := executionEnv.AbsolutePath(ctx, path)
	if err != nil {
		return Location{}, "not an existing directory"
	}
	canonical, err := executionEnv.CanonicalPath(ctx, absolute)
	if err != nil {
		return Location{}, "not an existing directory"
	}
	info, err := executionEnv.FileInfo(ctx, canonical)
	if err != nil || info.Kind != durableenv.FileKindDirectory {
		return Location{}, "not an existing directory"
	}
	return Location{Env: target.Env, Cwd: absolute}, ""
}

// DurableOwner is the owner name of a session's Durable records in its store: its tables and document namespaces are
// claimed under it.
const DurableOwner = "p2.durable"

// DurableStorage is Durable's storage in the session store: StoreStorage under p2's owner name, beside the kit's `Files`
// (tables `p2.durable:*` and `chord.app.files:*` in one store).
func DurableStorage(ctx context.Context, store chordstore.Store) (durable.Storage, error) {
	return durablestore.Open(ctx, store, DurableOwner)
}

// WorkerCore is the worker's core facet: its registries, its natives, the Harness and the server's lifecycle calls.
func WorkerCore(init WorkerInit, session SessionInit, liveness *Liveness, location *SessionLocation, store chordstore.Store, root func(fn func(ctx host.Ctx) (any, error), ctx context.Context) (any, error)) host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
		if err := ServeWorkerRegistries(facet); err != nil {
			return err
		}
		if err := serveNative[WorkerParts](facet, WorkerProcess); err != nil {
			return err
		}
		own, err := SessionDirectory(init.DataDir, session.ID)
		if err != nil {
			return err
		}
		copyFilesTo := func(ctx context.Context, other string) error {
			target, err := SessionDirectory(init.DataDir, other)
			if err != nil {
				return err
			}
			return app.CopyFiles(app.FilesSource{Store: store, Files: HostFiles(own)}, target, ctx)
		}
		if err := self.Contribute(WorkerProcess, &WorkerParts{Init: init, Liveness: liveness, Location: location, Root: root, CopyFilesTo: copyFilesTo}); err != nil {
			return err
		}
		if err := serveNative[HarnessParts](facet, HarnessNative); err != nil {
			return err
		}
		if err := facet.Serve(HarnessHost, host.LocalFactory(func(_ *delta.JsonObject, objectSelf host.Self) (contracts.Implementation, error) {
			storeSelf, ok := objectSelf.(host.StoreSelf)
			if !ok {
				return contracts.Implementation{}, errors.New("p2.harness-host: no store self")
			}
			storeSelf.Contribute(HarnessNative, openHarness(storeSelf, init, session, location, store))
			return contracts.Implementation{}, nil
		}), host.ServeOptions{}); err != nil {
			return err
		}
		return facet.Serve(WorkerBoundary, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, objectSelf host.Self) (contracts.Implementation, error) {
			who, err := FromChord(principal)
			if _, isServer := who.(ServerPrincipal); err != nil || !isServer {
				return contracts.Implementation{}, Forbidden("server lifecycle only")
			}
			harnessOf := HarnessOf(objectSelf)
			checks := objectSelf.Registry(RelocationChecks.Registry)
			return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"prepare": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					frozen := liveness.Freeze()
					// Pi's async handler runs up to its first await inside the section that admits the call: the freeze. The
					// inspection waits for the Durable session's line, whose commits publish in sections of this host's loop,
					// so a handler that waits for it without suspending holds the loop the publication needs.
					chordctx.Suspend(ctx)
					result, err := prepareRelocation(ctx, args, frozen, harnessOf, checks, location)
					if err != nil || !result.accepted {
						if frozen {
							liveness.Unfreeze()
						}
					}
					if err != nil {
						return nil, err
					}
					return result.json(), nil
				},
				"cancel": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					liveness.Unfreeze()
					return nil, nil
				},
				"relocate": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if !liveness.Relocating() {
						return nil, errors.New("Relocation was not prepared")
					}
					next, err := ParseLocationJSON(args)
					if err != nil {
						return nil, err
					}
					parts, err := harnessOf()
					if err != nil {
						return nil, err
					}
					// The relocation commits to the Durable session (the conversations' agent cwds) and awaits the commit's
					// publication, a section of this host's loop: suspend, or the section holds the loop that publication
					// waits for (the first await of Pi's async handler).
					chordctx.Suspend(ctx)
					if err := parts.Relocate(ctx, next, liveness.Unfence); err != nil {
						return nil, err
					}
					liveness.Unfreeze()
					return nil, nil
				},
			}}}, nil
		}), host.ServeOptions{})
	})
}

type preparation struct {
	accepted bool
	tasks    int
	busy     []int
	refusal  string
}

func (p preparation) json() *delta.JsonObject {
	busy := make([]any, len(p.busy))
	for i, id := range p.busy {
		busy[i] = float64(id)
	}
	object := delta.JsonObjectOf("accepted", p.accepted, "tasks", float64(p.tasks), "busy", busy)
	if p.refusal != "" {
		object.Set("refusal", p.refusal)
	}
	return object
}

func prepareRelocation(ctx context.Context, args delta.JsonValue, frozen bool, harnessOf func() (*HarnessParts, error), checks *registries.RegistryValueSource, location *SessionLocation) (preparation, error) {
	parts, err := harnessOf()
	if err != nil {
		return preparation{}, err
	}
	h, err := parts.Durable()
	if err != nil {
		return preparation{}, err
	}
	inspection, err := h.Inspect(ctx)
	if err != nil {
		return preparation{}, err
	}
	seen := map[int]bool{}
	var busy []int
	for _, task := range inspection.Tasks {
		if !task.Record.Background && !seen[int(task.Record.ConversationId)] {
			seen[int(task.Record.ConversationId)] = true
			busy = append(busy, int(task.Record.ConversationId))
		}
	}
	slices.Sort(busy)
	tasks := len(inspection.Tasks) + len(inspection.Submissions)
	var refusal string
	// Under the fence: nothing can change what the checks read (no task runs, nothing is admitted).
	if object, _ := args.(*delta.JsonObject); frozen && tasks == 0 && object != nil {
		to, err := ParseLocationJSON(object.Value("to"))
		if err != nil {
			return preparation{}, err
		}
		table, _ := checks.Value().(*KeyedTable[RelocationCheck])
		for _, key := range table.Keys() {
			check, _ := table.Get(key)
			if refusal, err = check(ctx, h, location.Value(), to); err != nil {
				return preparation{}, err
			}
			if refusal != "" {
				break
			}
		}
	}
	return preparation{accepted: frozen && tasks == 0 && refusal == "", tasks: tasks, busy: busy, refusal: refusal}, nil
}

// ParseLocationJSON reads a Location from its JSON (`{ cwd, env }`, env a name or null).
func ParseLocationJSON(value delta.JsonValue) (Location, error) {
	object, _ := value.(*delta.JsonObject)
	if object == nil {
		return Location{}, errors.New("a location is an object")
	}
	cwd, ok := object.Value("cwd").(string)
	if !ok {
		return Location{}, errors.New("a location has a cwd")
	}
	location := Location{Cwd: cwd}
	if name, ok := object.Value("env").(string); ok {
		location.Env = &name
	}
	return location, nil
}

// ParseSessionInit reads a SessionInit from its JSON.
func ParseSessionInit(value delta.JsonValue) (SessionInit, error) {
	object, _ := value.(*delta.JsonObject)
	if object == nil {
		return SessionInit{}, errors.New("p2 worker: the session start data is not an object")
	}
	init := SessionInit{}
	init.ID, _ = object.Value("id").(string)
	init.Cwd, _ = object.Value("cwd").(string)
	if name, ok := object.Value("env").(string); ok {
		init.Env = &name
	}
	if model, _ := object.Value("model").(*delta.JsonObject); model != nil {
		init.Model.Provider, _ = model.Value("provider").(string)
		init.Model.ModelId, _ = model.Value("modelId").(string)
	}
	if level, ok := object.Value("thinkingLevel").(string); ok {
		init.ThinkingLevel = ai.ModelThinkingLevel(level)
	}
	if resume, ok := object.Value("resume").(bool); ok {
		init.Resume = &resume
	}
	if init.ID == "" || init.Cwd == "" {
		return SessionInit{}, errors.New("p2 worker: the session start data lacks an id or a cwd")
	}
	return init, nil
}

// ServeWorkerNatives serves the natives registries of a worker host: the worker process's and the Harness's (the core
// facet does this itself; test hosts without one).
func ServeWorkerNatives(facet host.FacetHost) error {
	if err := serveNative[WorkerParts](facet, WorkerProcess); err != nil {
		return err
	}
	return serveNative[HarnessParts](facet, HarnessNative)
}

// ContributeWorkerProcess contributes the worker process's natives from a facet (test hosts).
func ContributeWorkerProcess(self host.FacetSelf, parts *WorkerParts) error {
	return self.Contribute(WorkerProcess, parts)
}

// ContributeHarness contributes the session's Harness natives from a facet (test hosts).
func ContributeHarness(self host.FacetSelf, parts *HarnessParts) error {
	return self.Contribute(HarnessNative, parts)
}
