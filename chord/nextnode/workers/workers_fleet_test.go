package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/test/node/workers/support.ts: a server routing `Session` to forked fixture workers, the
// fixture plugins copied into a temporary root, each address's start data (its behaviour) chosen by the test.

var background = chordctx.Background()

// fixtureServer is the server's own kind: `hostKind("server")`.
var fixtureServer = mustFixture(contracts.NewHostKind("server"))

// fleetOptions are the options of fleet; the zero value is the defaults of support.ts.
type fleetOptions struct {
	// idleMs is how long a worker idles before it is stopped (60 s by default).
	idleMs    int
	heartbeat *host.HeartbeatOptions
	start     func(address contracts.Address, ctx context.Context) (delta.JsonValue, error)
	principal func() (data.Principal, error)
	authorize func() (bool, error)
	// onAuthorize runs for every hold the route authorizes, inside the server's section (the route's `authorize` runs there).
	onAuthorize func(address contracts.Address)
	// env is added to the workers' environment.
	env map[string]string
	// entry is the workers' program: the fixture's by default.
	entry string
	// baseDir is the directory the worker base's copy is written into (WorkersOptions.BaseDir).
	baseDir string
}

// collector collects values from callbacks on other goroutines.
type collector[T any] struct {
	mu    sync.Mutex
	items []T
}

func (c *collector[T]) add(item T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, item)
}

func (c *collector[T]) all() []T {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]T(nil), c.items...)
}

// fleet is a server with workers; closed after the test.
type fleet struct {
	t       testing.TB
	server  host.Host
	workers *Workers
	plugins *plugins.Plugins
	// root is the copy of the fixture plugins.
	root       string
	errors     *collector[WorkersError]
	hostErrors *collector[host.HostError]
	// starts is the addresses `start` ran for, in order.
	starts *collector[string]
	// output is the workers' stdout lines, each with its address key.
	output *collector[string]
	// forgotten is the address keys WorkersOptions.Forgotten ran for, in order.
	forgotten *collector[string]
	// behaviours is each address's behaviour (its start data); none: a plain worker.
	behaviours *behaviours
	clock      *switchClock
}

type behaviours struct {
	mu   sync.Mutex
	byID map[string]workerBehaviour
}

func (b *behaviours) set(id string, behaviour workerBehaviour) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.byID[id] = behaviour
}

func (b *behaviours) delete(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.byID, id)
}

func (b *behaviours) get(id string) workerBehaviour {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.byID[id]
}

// newFleet starts a server routing `Session` to fixture workers; options override the workers' defaults.
func newFleet(t testing.TB, options fleetOptions) *fleet {
	t.Helper()
	root := t.TempDir()
	f := &fleet{
		t: t, root: root, errors: &collector[WorkersError]{}, hostErrors: &collector[host.HostError]{}, starts: &collector[string]{},
		output: &collector[string]{}, forgotten: &collector[string]{}, behaviours: &behaviours{byID: map[string]workerBehaviour{}}, clock: newSwitchClock(),
	}
	f.plugins = newPlugins(t, root)
	serverAddress := fixtureServer.Address(nil)
	serverOptions := host.HostOptions{Address: &serverAddress, Principal: delta.JsonObjectOf("kind", "server"), OnError: f.hostErrors.add}
	if options.heartbeat != nil {
		serverOptions.Heartbeat = options.heartbeat
	}
	server, err := host.CreateHost(serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	f.server = server
	idle := 60_000
	if options.idleMs != 0 {
		idle = options.idleMs
	}
	entry := options.entry
	if entry == "" {
		entry = fixtureEntry(t)
	}
	workers, err := CreateWorkers(server, fixtureSession, WorkersOptions{
		Kind:    "worker",
		Entry:   entry,
		BaseDir: options.baseDir,
		Env:     withEnv(fixtureEnvironment("worker"), options.env),
		Authorize: func(_ data.Principal, address contracts.Address) (bool, error) {
			if options.onAuthorize != nil {
				options.onAuthorize(address)
			}
			if options.authorize != nil {
				return options.authorize()
			}
			return true, nil
		},
		Principal: func(address contracts.Address) (data.Principal, error) {
			if options.principal != nil {
				return options.principal()
			}
			id, _ := address.Params.Value("id").(string)
			return delta.JsonObjectOf("kind", "worker", "id", id), nil
		},
		Start: func(address contracts.Address, ctx context.Context) (delta.JsonValue, error) {
			f.starts.add(address.Key)
			if options.start != nil {
				return options.start(address, ctx)
			}
			id, _ := address.Params.Value("id").(string)
			return f.behaviours.get(id).json(), nil
		},
		Lock:    func(address contracts.Address) (string, error) { return "chord-test:" + root + ":" + address.Key, nil },
		IdleMs:  func() time.Duration { return time.Duration(idle) * time.Millisecond },
		Plugins: f.plugins,
		OnOutput: func(address contracts.Address, stream string, chunk []byte) {
			if stream == "stdout" {
				f.output.add(address.Key + " " + string(trimSpace(chunk)))
			}
		},
		OnError:   f.errors.add,
		Forgotten: func(address contracts.Address) { f.forgotten.add(address.Key) },
		clock:     f.clock,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	f.workers = workers
	loadHost(t, server)
	// The worker kind's set made now (discovery, builds): the watcher marks edits from then on.
	if _, err := f.plugins.Scripts("worker", background); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = workers.Close(background)
		_ = server.Close(background)
	})
	return f
}

func trimSpace(chunk []byte) []byte {
	start, end := 0, len(chunk)
	for start < end && (chunk[start] == ' ' || chunk[start] == '\n' || chunk[start] == '\r' || chunk[start] == '\t') {
		start++
	}
	for end > start && (chunk[end-1] == ' ' || chunk[end-1] == '\n' || chunk[end-1] == '\r' || chunk[end-1] == '\t') {
		end--
	}
	return chunk[start:end]
}

func (f *fleet) hold(typ *contracts.Contract, id string) *host.Reference {
	f.t.Helper()
	reference, err := f.server.Hold(background, typ, delta.JsonObjectOf("id", id), host.GetOptions{})
	if err != nil {
		f.t.Fatal(err)
	}
	return reference
}

func (f *fleet) get(typ *contracts.Contract, id string) *host.Reference {
	f.t.Helper()
	reference, err := f.server.Get(background, typ, delta.JsonObjectOf("id", id), host.GetOptions{})
	if err != nil {
		f.t.Fatalf("get %s %s: %v", typ.Name, id, err)
	}
	return reference
}

// session is `Session({ id })`.
func session(id string) contracts.Address {
	return fixtureSession.Address(delta.JsonObjectOf("id", id))
}

// stateOf is the state entry of id: nil when none.
func (f *fleet) stateOf(id string) *WorkerState {
	object, _ := f.workers.State().Value().(*delta.JsonObject)
	entry, ok := object.Value("session|" + id).(*delta.JsonObject)
	if !ok {
		return nil
	}
	state := &WorkerState{Status: WorkerStatus(entry.Value("status").(string))}
	if pid, ok := entry.Value("pid").(float64); ok {
		state.Pid = int(pid)
	}
	if failure, ok := entry.Value("error").(*delta.JsonObject); ok {
		state.Error.Message, _ = failure.Value("message").(string)
		state.Error.Code, _ = failure.Value("code").(string)
	}
	return state
}

// stateText is JSON.stringify(workers.state.value).
func (f *fleet) stateText() string {
	text, _ := delta.JsonObjectOf("v", f.workers.State().Value()).MarshalJSON()
	return string(text)
}

// kinds are the kinds of the errors the workers reported, as Pi spells them.
func (f *fleet) kinds() []string {
	var kinds []string
	for _, each := range f.errors.all() {
		kinds = append(kinds, errorKind(each))
	}
	return kinds
}

func errorKind(err WorkersError) string {
	switch err.(type) {
	case WorkerFailedError:
		return "worker_failed"
	case AuthorizeFailedError:
		return "authorize_failed"
	case WorkerExitedError:
		return "worker_exited"
	case WorkerLinkLostError:
		return "worker_link_lost"
	default:
		return fmt.Sprintf("%T", err)
	}
}

// firstFailure is the first `worker_failed` the workers reported.
func (f *fleet) firstFailure() (WorkerFailedError, bool) {
	for _, each := range f.errors.all() {
		if failed, ok := each.(WorkerFailedError); ok {
			return failed, true
		}
	}
	return WorkerFailedError{}, false
}

// lastFailure is the last `worker_failed` the workers reported.
func (f *fleet) lastFailure() (WorkerFailedError, bool) {
	all := f.errors.all()
	for _, a := range slices.Backward(all) {
		if failed, ok := a.(WorkerFailedError); ok {
			return failed, true
		}
	}
	return WorkerFailedError{}, false
}

// label reads `notes.label.value`.
func labelOf(t testing.TB, reference *host.Reference) string {
	t.Helper()
	value, err := reference.Member("label").Value()
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	text, _ := value.(string)
	return text
}

// refKind is `ref.state.value.kind`.
func refKind(reference *host.Reference) host.RefStateKind { return reference.State().Value().Kind }

// fakeClock is vitest's fake timers for setTimeout and clearTimeout (and nothing else: Date is the real one).
type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTimer
}

type fakeTimer struct {
	clock *fakeClock
	due   time.Duration
	job   func()
	done  bool
}

func (c *fakeClock) afterFunc(d time.Duration, job func()) host.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{clock: c, due: c.now + d, job: job}
	c.timers = append(c.timers, timer)
	return timer
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	pending := !t.done
	t.done = true
	return pending
}

// advance is `vi.advanceTimersByTimeAsync(d)`: the timers due within d run in order of their due time.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	limit := c.now + d
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, timer := range c.timers {
			if !timer.done && timer.due <= limit && (next == nil || timer.due < next.due) {
				next = timer
			}
		}
		if next == nil {
			c.now = limit
			c.mu.Unlock()
			return
		}
		next.done = true
		if next.due > c.now {
			c.now = next.due
		}
		c.mu.Unlock()
		next.job()
	}
}

// switchClock is the real clock until a test fakes the timers (`vi.useFakeTimers`), and again after it restores them.
type switchClock struct {
	mu   sync.Mutex
	fake *fakeClock
	// real is the host's own loop: its timers drop their job when stopped. A stopped time.Timer stays in the runtime's timer
	// heap until the heap is cleaned, and its callback would keep what the job captured (an address, a child) reachable until
	// then: the cause of the intermittent "referent was not collected" of the 100-address test.
	real *host.Loop
}

func newSwitchClock() *switchClock { return &switchClock{real: host.NewLoop()} }

func (c *switchClock) Now() int64 { return c.real.Now() }

func (c *switchClock) AfterFunc(d time.Duration, job func()) host.Timer {
	c.mu.Lock()
	fake := c.fake
	c.mu.Unlock()
	if fake != nil {
		return fake.afterFunc(d, job)
	}
	return c.real.AfterFunc(d, job)
}

// useFakeTimers is `vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] })`; the returned func restores the real ones.
func (c *switchClock) useFakeTimers() (*fakeClock, func()) {
	fake := &fakeClock{}
	c.mu.Lock()
	c.fake = fake
	c.mu.Unlock()
	return fake, func() {
		c.mu.Lock()
		c.fake = nil
		c.mu.Unlock()
	}
}

// newPlugins makes the fleet's plugins component over a copy of the fixture plugin in root: Pi's own plugin of the workers
// tests (testdata/plugins/notes: its package, its worker facet serving `Notes` at the worker's address, and the label file the
// rollout tests edit), built for real; the workers evaluate it in their Node hosts.
func newPlugins(t testing.TB, root string) *plugins.Plugins {
	t.Helper()
	if err := os.CopyFS(filepath.Join(root, "notes"), os.DirFS(filepath.Join("testdata", "plugins", "notes"))); err != nil {
		t.Fatal(err)
	}
	component := plugins.CreatePlugins(plugins.PluginsOptions{
		Build: build.BuildFacet, Roots: []model.PluginRoot{{Path: root}},
		Kinds:   []plugins.KindEntry{{Name: "worker", Definition: plugins.KindDefinition{Platform: model.PlatformNode, HostPackages: []string{"@earendil-works/chord"}}}},
		OnError: func(plugins.PluginsError) {},
	}, background)
	t.Cleanup(component.Close)
	return component
}

// writeLabel writes label into the copy's notes plugin (`label.ts`). The file is renamed into place from outside the watched
// tree: a plain write is a truncate event and a write event, which fsnotify delivers on another goroutine (Node's fs.watch
// events of one writeFile arrive in one turn), so the second could land after a build began and mark the plugin dirty again.
func writeLabel(root, label string) error {
	text, err := json.Marshal(label)
	if err != nil {
		return err
	}
	staged, err := os.CreateTemp(filepath.Dir(root), "label-*.ts")
	if err != nil {
		return err
	}
	_, err = staged.WriteString("export const label = " + string(text) + ";\n")
	if closeErr := staged.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(staged.Name())
		return err
	}
	return os.Rename(staged.Name(), filepath.Join(root, "notes", "label.ts"))
}

// label writes label into the copy's notes plugin.
func (f *fleet) label(label string) {
	f.t.Helper()
	if err := writeLabel(f.root, label); err != nil {
		f.t.Fatal(err)
	}
}

// dirty is `plugins.state.value.dirty`.
func (f *fleet) dirty() []string { return f.plugins.Marks().Dirty }

// build is `plugins.build(plugins.state.value.dirty, context)`.
func (f *fleet) build(names []string) *plugins.Built {
	f.t.Helper()
	built, err := f.plugins.Build(names, background)
	if err != nil {
		f.t.Fatal(err)
	}
	return built
}

// childStarted reports whether the child of session `id` has told `started` (a look into the component, under its loop).
func (f *fleet) childStarted(id string) bool {
	component := f.workers
	var started bool
	component.loop.Run(func() {
		if record := component.addresses["session|"+id]; record != nil && record.child != nil {
			started = record.child.started.isSettled()
		}
	})
	return started
}

// watchStatuses records the status of session `id` in each frame of the workers' state, as it changes.
func (f *fleet) watchStatuses(id string) *collector[WorkerStatus] {
	statuses := &collector[WorkerStatus]{}
	watch, err := f.workers.State().Watch(background)
	if err != nil {
		f.t.Fatal(err)
	}
	var last WorkerStatus
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		object, _ := call.Value.(*delta.JsonObject)
		entry, ok := object.Value("session|" + id).(*delta.JsonObject)
		if !ok {
			return nil
		}
		if status := WorkerStatus(entry.Value("status").(string)); status != last {
			last = status
			statuses.add(status)
		}
		return nil
	})
	f.t.Cleanup(func() { _, _ = watch.Stop() })
	return statuses
}

// reloadOutstanding reports whether a reload of session `id`'s child is out and unanswered (a look into the component).
func (f *fleet) reloadOutstanding(id string) bool {
	component := f.workers
	var outstanding bool
	component.loop.Run(func() {
		if record := component.addresses["session|"+id]; record != nil && record.child != nil {
			outstanding = record.child.reloaded != nil
		}
	})
	return outstanding
}

// withEnv is env with extra added.
func withEnv(env, extra map[string]string) map[string]string {
	maps.Copy(env, extra)
	return env
}
