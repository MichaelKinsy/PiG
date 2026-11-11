package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/src/node/workers/workers.ts. The workers component (slice 10 §3, §4; decision "Slice 10's
// points"): a worker process per address of one host kind, started on demand by the route it owns, stopped when idle,
// never restarted by itself (R2): a crash, a `fatal` or a failed start leaves the address failed until `Retry` or a
// rollout. A running worker's lost link is a stop its holders start a new worker after, once a minute (decision "A lost
// worker link is recovered from"). Per address, a child's events are recorded on that child as they happen;
// starts and stops run one after another.
//
// Forced Go differences. The component's state is Pi's single JavaScript thread: a `Loop` of its own runs every synchronous
// part of a Pi function as one section (`loop.Run`), and the microtask `queueMicrotask` is `loop.Queue`. An awaited Promise
// is a blocking call that enters the loop for each synchronous part; a Promise stored and awaited by several parties
// (`started`, `ready`, an address's start, a reload's reply) is a once-settled `outcome` (workers_outcome.go). The
// per-address queue (`routed.queue`, `queue.then(operation)`) is a chain of `done` channels, one owned goroutine per
// operation, joined by Close (a start the route awaits releases the next operation in the section that applies its result,
// as Pi's route continuation runs before it: parking.Applied, at `resolve`). `setTimeout` is `clock.AfterFunc` (the loop's own
// unless a test gives one), `setInterval` a
// timer its callback re-arms, `Date.now()` is `clock.Now()` in milliseconds. A thrown error is a returned one: `authorize`,
// `principal`, `start`, `lock` and the plugins' `Scripts` return errors where Pi's throw, and the component keeps each
// inside the boundary Pi's try/catch draws. `Workers<K>`'s kind parameter is type-level: addresses are contracts.Address.
// `idleMs` is a time.Duration. `Close` waits for the goroutines the component owns, a start's call into the app's `start`
// excepted: an app hook that ignores its context and never returns is the app's, abandoned when Close aborts the wait.

// workersEnvironment is the workers' environment: Env, with NODE_COMPILE_CACHE from CompileCache unless Env sets it
// (`compileCache === undefined ? env : { NODE_COMPILE_CACHE: compileCache, ...env }`).
func workersEnvironment(options WorkersOptions) map[string]string {
	if options.CompileCache == "" {
		return options.Env
	}
	environment := map[string]string{"NODE_COMPILE_CACHE": options.CompileCache}
	maps.Copy(environment, options.Env)
	return environment
}

// WorkersOptions is what CreateWorkers takes. Kind, Entry, ExecArgv and Env are bootstrap; the functions are called per use.
type WorkersOptions struct {
	// Kind is the facet kind its workers load ("worker"), shipped by the plugins component.
	Kind string
	// Entry is the worker's executable (it calls WorkerMain), ExecArgv its arguments and Env its environment.
	Entry    string
	ExecArgv []string
	Env      map[string]string
	// BaseDir is the private directory the worker base's copy of Entry is written into at the first fork: the component's own while
	// it is open (emptied first, removed at Close, never touched after it, so a later start may own it then); none: a new temporary
	// directory. One that stays the same between starts is the kit's (`cache/workers/<kind>/`).
	BaseDir string
	// CompileCache is Node's compile cache for the workers (`NODE_COMPILE_CACHE` in their environment unless Env sets it): none, the
	// server's environment decides. A Go worker is no Node module; the variable reaches the Node processes it starts (its plugins'
	// host), which inherit its environment (Pi's `compileCache`: forks load the base and its packages from compiled code).
	CompileCache string
	// Authorize is the route's `authorize`: whether principal may reach address.
	Authorize func(principal data.Principal, address contracts.Address) (bool, error)
	// Principal is who the worker at address is (stamped on its link).
	Principal func(address contracts.Address) (data.Principal, error)
	// Start is the worker's start data (JSON), before each start; abortable (`Close`), an error fails the start.
	Start func(address contracts.Address, ctx context.Context) (delta.JsonValue, error)
	// Lock is the lock one live worker of address holds (across processes).
	Lock func(address contracts.Address) (string, error)
	// IdleMs is how long a worker idles before it is stopped: 5 minutes by default.
	IdleMs  func() time.Duration
	Plugins *plugins.Plugins
	// OnOutput is a worker's output; without it, mirrored to this process's own.
	OnOutput func(address contracts.Address, stream string, chunk []byte)
	OnError  func(err WorkersError)
	// Forgotten is called, on the component's loop (it must not block), once an address has no worker, no operation and no failure
	// left: the app's per-address bookkeeping made at Start ends there. Pi has no such hook (its app keeps the entries it made at start for
	// the process's life); this is a Go addition that changes nothing observable.
	Forgotten func(address contracts.Address)

	// clock is the clock of the component's timers; the loop's own by default. Tests give one with virtual timers.
	clock clock
}

// clock is what the component asks of time: `Date.now()` and `setTimeout`. *host.Loop is one.
type clock interface {
	Now() int64
	AfterFunc(d time.Duration, job func()) host.Timer
}

// WorkerStatus is where a worker stands.
type WorkerStatus string

const (
	WorkerStarting WorkerStatus = "starting"
	WorkerRunning  WorkerStatus = "running"
	WorkerStopping WorkerStatus = "stopping"
	WorkerFailed   WorkerStatus = "failed"
)

// WorkerState is one address's worker (R5): running or on its way, or failed (until `Retry` or a rollout) with a public
// error: a fixed message (`worker failed to start`, `worker exited (SIGSEGV)`) or the child's redacted `fatal`. Pid and
// StartedAt are set for a worker that lives, Error and At for a failed one.
type WorkerState struct {
	Status    WorkerStatus
	Pid       int
	StartedAt int64
	Error     CrossedError
	At        int64
}

// WorkersError is what the component reports (and the state's watch listeners that threw): one of WorkerFailedError,
// AuthorizeFailedError, WorkerExitedError, WorkerLinkLostError and BatchWorkersError.
type WorkersError interface{ workersError() }

// WorkerFailedError (`worker_failed`): Error is the full error (a start's throw, a crash, the child's `fatal`), for the app's
// logs, never public.
type WorkerFailedError struct {
	Address    string
	Error      error
	StderrTail string
}

// AuthorizeFailedError (`authorize_failed`): the app's `authorize` failed: the hold is refused (`forbidden`), the error stays
// private.
type AuthorizeFailedError struct {
	Address string
	Error   error
}

// WorkerExitedError (`worker_exited`): an exit nobody asked for.
type WorkerExitedError struct {
	Address string
	Code    *int
	Signal  *string
}

// WorkerLinkLostError (`worker_link_lost`): a running worker's link was lost (the heartbeat, a protocol error): it was stopped,
// its holders start anew.
type WorkerLinkLostError struct {
	Address string
	Error   error
}

// BatchWorkersError is a `BatchError` of the state's batch.
type BatchWorkersError struct{ state.BatchError }

func (WorkerFailedError) workersError()    {}
func (AuthorizeFailedError) workersError() {}
func (WorkerExitedError) workersError()    {}
func (WorkerLinkLostError) workersError()  {}
func (BatchWorkersError) workersError()    {}

// WorkersBuilt is how a worker kind's base build ended: built (the file it was built from with its bytes' hash), or failed. A
// Go worker's base is a private copy of its own executable, which holds its code already (no bundle: see the worker base note in
// doc.go); its build checks that Entry is a program that can be started and copies it.
type WorkersBuilt struct {
	// Kind says how it ended: WorkersBuiltBuilt (Inputs) or WorkersBuiltFailed (Err), Pi's `kind`. Empty when a close ended the wait
	// for the build (awaitBuild): it did not end.
	Kind WorkersBuiltKind
	// Inputs is the file the base was built from with the hash of the bytes copied: the app's base code for its workers, an edit
	// of it outdates the base. Nil when it failed.
	Inputs model.BaseInputs
	// Err is why it did not build.
	Err error
}

// WorkersBuiltKind is the `kind` of how a build ended.
type WorkersBuiltKind string

// The kinds of how a build ended.
const (
	WorkersBuiltBuilt  WorkersBuiltKind = "built"
	WorkersBuiltFailed WorkersBuiltKind = "failed"
)

// Build copies the worker's entry into the base's private directory and records its bytes, once (a start waits for it, and starts
// it when nothing did); Pi's `build` bundles the worker base in memory and writes it at the first fork. A program cannot be held in
// memory: the copy is the base, and what an edit of the entry afterwards does is what Pi's edit of a bundled source does, nothing
// to a worker. It returns how it ended: every start fails while it failed.
//
// The copy is shared by every waiter and owned by no caller: a caller's wait ends with the caller, a start's with the component's
// close (`start`), and `Close` does not wait for it, as Pi's does not wait for the bundle's build. What the copy finishes after the
// close is dropped: it is written beside the base's directory and becomes the base only while the component is open (commitBase),
// so a close never leaves it holding a directory the kit's next start owns (the kit releases the data directory's lock after the
// close).
func (c *Workers) Build() WorkersBuilt {
	c.startBuild()
	<-c.buildDone
	return *c.built.Load()
}

// startBuild starts the build when nothing did.
func (c *Workers) startBuild() {
	c.buildOnce.Do(func() {
		//portlint:allow golifetime the build is shared by every waiter and ends with the copy of the entry, which cannot be cancelled (a read of a program on a slow or stalled file system); Close does not wait for it and what it finishes after the close is dropped, as upstream's
		go func() { //portlint:allow asyncorder Pi's `#built()` is one shared Promise: its ordering point is buildDone, closed once the outcome is stored, which Build and awaitBuild wait on
			built := WorkersBuilt{}
			// A component that closed copies nothing; a named directory's staged leftovers of an earlier process go first.
			found, staged, err := c.stageBase("")
			if err == nil {
				err = c.commitBase(staged)
			}
			if err != nil {
				built.Kind, built.Err = WorkersBuiltFailed, err
			} else {
				c.entryPath = found
				built.Kind, built.Inputs = WorkersBuiltBuilt, model.BaseInputs{found: staged.hash}
			}
			c.built.Store(&built)
			close(c.buildDone)
		}()
	})
}

// Built is how the build ended, once it did (nil before).
func (c *Workers) Built() *WorkersBuilt { return c.built.Load() }

// awaitBuild waits for the base's build, started when nothing did, as long as the component is open (Pi's
// `awaitWithContext(this.#built(), this.#closingContext)` in a start): false when the close ended the wait.
func (c *Workers) awaitBuild() (WorkersBuilt, bool) {
	c.startBuild()
	select {
	case <-c.buildDone:
		// Pi's closing re-check after the await: both ready at once is a close.
		return *c.built.Load(), !c.isClosing()
	case <-c.closingCtx.Done():
		return WorkersBuilt{}, false
	}
}

// stagedBase is a copy of the entry written and not yet the component's base: the directory holding it, the copy in it, and the
// hash of the bytes written.
type stagedBase struct {
	dir, copied, hash string
}

// stageBase copies the entry (resolved from Options.Entry, or the resolved path given) into a new private directory: beside
// Options.BaseDir when there is one (the same file system: the commit is a rename), else a new temporary directory, which is the
// base's own. It takes no lock and touches nothing that is not its own new directory. It returns the resolved entry.
func (c *Workers) stageBase(resolved string) (found string, staged stagedBase, err error) {
	if c.isClosing() {
		return "", stagedBase{}, closed()
	}
	found = resolved
	if found == "" {
		if found, err = exec.LookPath(c.options.Entry); err == nil {
			found, err = filepath.Abs(found)
		}
		if err != nil {
			return "", stagedBase{}, fmt.Errorf("workers: the worker entry cannot be started: %w", err)
		}
	}
	if resolved == "" {
		removeStaleStages(c.options.BaseDir)
	}
	staged, err = stageWorkerBase(found, c.options.BaseDir)
	if err != nil {
		return "", stagedBase{}, fmt.Errorf("workers: the worker base cannot be made from %s: %w", found, err)
	}
	return found, staged, nil
}

// commitBase makes staged the component's base, while the component is open: in the named directory, emptied first (the files of
// an earlier build go), or the temporary one it was written in. A component that closed drops it: nothing but staged's own
// directory is touched then.
func (c *Workers) commitBase(staged stagedBase) error {
	c.baseMu.Lock()
	defer c.baseMu.Unlock()
	if c.isClosing() {
		_ = os.RemoveAll(staged.dir)
		return closed()
	}
	dir, copied := staged.dir, staged.copied
	if named := c.options.BaseDir; named != "" {
		dir, copied = named, filepath.Join(named, filepath.Base(staged.copied))
		if err := os.RemoveAll(named); err != nil {
			_ = os.RemoveAll(staged.dir)
			return fmt.Errorf("workers: the worker base cannot be made in %s: %w", named, err)
		}
		if err := os.Rename(staged.dir, named); err != nil {
			_ = os.RemoveAll(staged.dir)
			return fmt.Errorf("workers: the worker base cannot be made in %s: %w", named, err)
		}
	} else if c.baseDir != "" && c.baseDir != dir {
		// The copy this one replaces (a cleaner of temporary files removed it): its directory goes too.
		_ = os.RemoveAll(c.baseDir)
	}
	c.baseDir, c.baseCopy = dir, copied
	return nil
}

// baseFile is the file every worker is forked from: the base's copy, made again from the entry when it is gone (a cleaner of
// temporary files) if the entry has the bytes the base was built with. A component that closed gives none.
func (c *Workers) baseFile() (string, error) {
	c.baseMu.Lock()
	copied := c.baseCopy
	c.baseMu.Unlock()
	if copied != "" {
		if _, err := os.Stat(copied); err == nil {
			return copied, nil
		}
	}
	built := c.built.Load()
	_, staged, err := c.stageBase(c.entryPath)
	if err != nil {
		return "", err
	}
	if want := built.Inputs[c.entryPath]; staged.hash != want {
		_ = os.RemoveAll(staged.dir)
		c.removeBase()
		return "", fmt.Errorf("workers: the worker base is gone and %s changed since it was built", c.entryPath)
	}
	if err := c.commitBase(staged); err != nil {
		return "", err
	}
	c.baseMu.Lock()
	defer c.baseMu.Unlock()
	return c.baseCopy, nil
}

// removeBase removes the directory this component wrote its base copy into, once it wrote one.
func (c *Workers) removeBase() {
	c.baseMu.Lock()
	defer c.baseMu.Unlock()
	if c.baseDir != "" {
		_ = os.RemoveAll(c.baseDir)
	}
	c.baseDir, c.baseCopy = "", ""
}

// stagePrefix is the name a staged directory beside base starts with: `.<base>+build-`. `+` is the separator no kind name has (names
// are `[a-z0-9@/._~-]`, contracts.CheckName), so another kind's staging directory (`-` can be in a name: kind `a-build-x` beside
// kind `a`) never looks like a leftover of this one.
func stagePrefix(base string) string { return "." + filepath.Base(base) + "+build-" }

// removeStaleStages removes the staged directories (`.<base>+build-*`) an earlier start left beside the named base directory: a
// build that outlived its component's close and the process ended before it dropped its copy. The first build of a component does
// it, once the app holds the data directory's lock (nothing else writes there). Nothing without a named directory.
func removeStaleStages(base string) {
	if base == "" {
		return
	}
	// The directory's entries, not a glob: the path can hold `[`, `*` and `?`.
	parent, prefix := filepath.Dir(base), stagePrefix(base)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, each := range entries {
		if strings.HasPrefix(each.Name(), prefix) {
			_ = os.RemoveAll(filepath.Join(parent, each.Name()))
		}
	}
}

// stageWorkerBase copies the executable at path into a new private directory (mode 0700, the copy 0700): beside base when it is
// not empty, else in a new temporary one. The program is opened first: a read that waits leaves nothing on disk.
func stageWorkerBase(path, base string) (staged stagedBase, err error) {
	source, err := os.Open(path)
	if err != nil {
		return stagedBase{}, err
	}
	defer func() { _ = source.Close() }()
	var dir string
	if base == "" {
		dir, err = os.MkdirTemp("", "chord-workers-base-")
	} else if parent := filepath.Dir(base); true {
		if err = os.MkdirAll(parent, 0o700); err == nil {
			dir, err = os.MkdirTemp(parent, stagePrefix(base))
		}
	}
	if err != nil {
		return stagedBase{}, err
	}
	copied := filepath.Join(dir, "worker"+filepath.Ext(path))
	target, err := os.OpenFile(copied, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	var hash string
	if err == nil {
		sum := sha256.New()
		_, err = io.Copy(io.MultiWriter(target, sum), source)
		if closeErr := target.Close(); err == nil {
			err = closeErr
		}
		hash = hex.EncodeToString(sum.Sum(nil))
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return stagedBase{}, err
	}
	return stagedBase{dir: dir, copied: copied, hash: hash}, nil
}

// CreateWorkers creates the workers component on h: it routes kind itself (`host.Route`, before `Load`). Its worker base is not
// built yet (Build).
func CreateWorkers(h host.Host, kind *contracts.HostKind, options WorkersOptions, ctx context.Context) (*Workers, error) {
	component := newWorkers(options, ctx)
	route, err := h.Route(kind, host.RouteOptions{
		Authorize: func(principal data.Principal, address contracts.Address) (bool, error) {
			// An error would reach the holder as the hold's failure: the app's error stays private.
			admitted, err := options.Authorize(principal, address)
			if err != nil {
				options.OnError(AuthorizeFailedError{Address: address.Key, Error: err})
				return false, nil
			}
			return admitted, nil
		},
		Resolve: component.resolve,
	}, ctx)
	if err != nil {
		return nil, err
	}
	component.route = route
	return component, nil
}

// failure is what an address keeps until `Retry` or a rollout: what the public sees (a fixed message, or the child's
// redacted `fatal`; decision "Slice 10 fix round, worker errors in public") and when.
type failure struct {
	err CrossedError
	at  int64
}

// cause is why a child fails when it exits, recorded as it happens: a start that failed, a `fatal`, a lost link. err is the
// full error (`OnError` only); public is what holders and `State` get (a fixed one from the exit when nil).
type cause struct {
	err    error
	public *CrossedError
	// killed: the component ended the child itself (a failed start): its exit is no `worker_exited`.
	killed bool
	// lost: a running child's link was lost: its address may recover (recovers) instead of failing.
	lost bool
}

// ended is how a child's reloads end once it is gone: stopped cleanly (a stop's exit) or with an error.
type ended struct {
	stopped bool
	err     error
}

// child is one worker process of an address, with what its events recorded.
type child struct {
	process *WorkerProcess
	status  WorkerStatus
	// stopping: a stop was asked for: its exit is no crash.
	stopping bool
	cause    *cause
	// started: the app's host listens on its link.
	started *outcome[struct{}]
	// ready: its load report; rejected when it exits before.
	ready *outcome[CrossedReport]
	// idle: it is idle (from `ready`), since idleSince (unix milliseconds); not while busy or before `ready`.
	idle      bool
	idleSince int64
	// idleStop: an idle stop is queued (checked again when it runs).
	idleStop bool
	// shipped is the set its start message or its last reload that applied without failures carried.
	shipped string
	// reloading is closed when its outstanding reload settled; reloads queue behind it, one outstanding at a time.
	reloading chan struct{}
	// reloaded is the waiter of its outstanding reload.
	reloaded func(reply FromWorker)
	// ended is set once its link is gone or it exited: every reload queued behind settles with it.
	ended *ended
	// grace is the timer of a stop or of a lost link.
	grace host.Timer
	// lock is the lock its start message named; cleared after its exit when it was killed holding it.
	lock string
}

// routed is one address: its operations in a queue, its child, its failure. Kept while it has any of them.
type routed struct {
	address contracts.Address
	// queue is closed when the last queued operation (and its bookkeeping) is done.
	queue   chan struct{}
	pending int
	child   *child
	failure *failure
	// starting is a start queued and not settled.
	starting *outcome[host.ResolvedLink]
	// recovered is when it last recovered from a lost link (recovers); nil: never.
	recovered *int64
}

const (
	// recoveryWindow is how long after a recovered lost link another one fails the address: 1 minute.
	recoveryWindow = 60 * time.Second
	// startTimeout is what a worker may take until `started` (fork, lock, connect): 60 s.
	startTimeout = 60 * time.Second
	// grace is how long a stopping worker (or one whose link is gone) gets before its tree is killed: 5 s.
	grace = 5 * time.Second
	// idleCheck is how often idle workers are looked for: every second.
	idleCheck = time.Second
	// defaultIdle is how long a worker idles before it is stopped unless `IdleMs` says otherwise: 5 minutes.
	defaultIdle = 5 * time.Minute
)

// startFailed is the public error of a start that failed.
var startFailed = CrossedError{Message: "worker failed to start", Code: "unavailable"}

// Workers is the workers component.
type Workers struct {
	// route is set by CreateWorkers once the route exists.
	route   host.RouteHandle
	state   *workersState
	options WorkersOptions
	// environment is the workers' environment: Env, with NODE_COMPILE_CACHE from CompileCache unless Env sets it (Pi's `#environment`).
	environment map[string]string
	loop        *host.Loop
	clock       clock

	// addresses is Pi's `Map<string, Routed>`, in insertion order; guarded by the loop.
	addresses map[string]*routed
	order     []string
	// closingCtx is aborted at `Close`: starts waiting in the app's `start` or for `started` end.
	closingCtx context.Context
	closing    func(cause error)
	// routing: whether the route's context closes it already (its first `resolve` attached that).
	routing   bool
	idleTimer host.Timer
	tasks     taskGroup
	// built is how the build ended, once it did; buildOnce runs it; entryPath is the entry it resolved.
	// buildDone closes when it did.
	built     atomic.Pointer[WorkersBuilt]
	buildOnce sync.Once
	buildDone chan struct{}
	entryPath string
	// baseMu guards the base copy: baseDir is the directory this component wrote it into, baseCopy the copy ("" before the
	// Build and after the removal).
	baseMu   sync.Mutex
	baseDir  string
	baseCopy string
}

func newWorkers(options WorkersOptions, ctx context.Context) *Workers {
	// One spelling of the named directory: its staging directory is made beside it (`Dir`, `Base`) and the commit replaces it.
	if options.BaseDir != "" {
		options.BaseDir = filepath.Clean(options.BaseDir)
	}
	c := &Workers{options: options, environment: workersEnvironment(options), loop: host.NewLoop(), addresses: map[string]*routed{}, buildDone: make(chan struct{})}
	c.clock = options.clock
	if c.clock == nil {
		c.clock = c.loop
	}
	c.closingCtx, c.closing = chordctx.WithCancel(chordctx.Background())
	batch := state.NewBatch(func(err state.BatchError) { options.OnError(BatchWorkersError{err}) }, nil).
		UseMicrotasks(c.loop.Queue).UseSection(c.loop.Run, c.loop.InSection)
	c.state = newWorkersState(c, batch)
	c.closeWith(ctx)
	return c
}

// State is per address key its WorkerState; no entry when stopped or never started. A JSON watchable.
func (c *Workers) State() chord.Watchable[delta.JsonValue] { return host.Expose(c.state) }

// Inputs are the files its worker base was built from, with their bytes' hashes: the app's base code for its workers (Pi's
// `inputs`, `base.inputs` of the base `createWorkers` built before it returned). Go builds the base lazily (Build): it is built
// here when nothing did, and none when the build failed (Pi's `createWorkers` rejects then).
func (c *Workers) Inputs() model.BaseInputs { return c.Build().Inputs }

// isClosing reports whether `Close` was called.
func (c *Workers) isClosing() bool { return c.closingCtx.Err() != nil }

// timer is `setTimeout`: job runs as a section of the component's loop.
func (c *Workers) timer(d time.Duration, job func()) host.Timer {
	return c.clock.AfterFunc(d, func() { c.loop.Run(job) })
}

// task runs fn as a goroutine the component owns; Close joins it.
func (c *Workers) task(fn func()) {
	c.tasks.add()
	//portlint:allow golifetime the component's own tasks end with their work and Close joins them
	go func() { //portlint:allow asyncorder workers.ts:233 `void started.then(...)` and :562 `void this.#enqueue(...)` start owned unawaited work; operations are chained by their done channels, not by this hand-off
		defer c.tasks.done()
		fn()
	}()
}

// resolve is the route's `resolve`: a failed address returns at once its (public) failure, nothing forks; otherwise a
// start, after the previous child's exit.
func (c *Workers) resolve(address contracts.Address, routing context.Context) (host.ResolvedLink, error) {
	var started *outcome[host.ResolvedLink]
	var record *routed
	var immediate error
	c.loop.Run(func() {
		if !c.routing {
			c.routing = true
			c.closeWith(routing)
		}
		record = c.routedOf(address)
		if record.failure != nil {
			immediate = uncrossed(record.failure.err)
			return
		}
		if c.isClosing() {
			immediate = closed()
			return
		}
		// The start has settled when its address's bookkeeping is done, before whoever awaits it resumes (the first reaction
		// Pi registers on it); the next operation is released once the route applied the result (the await's continuation, which
		// Pi runs before it).
		started = enqueue(c, record, func() (host.ResolvedLink, error) { return c.start(record, routing) }, func() { c.settled(record, started) }, parking.AppliedOf(routing))
		record.starting = started
	})
	if immediate != nil {
		return host.ResolvedLink{}, immediate
	}
	// The synchronous part is done: what follows waits for a worker, not for a section.
	chordctx.Suspend(routing)
	return started.wait()
}

// Stop suspends address while it has holders (`RouteHandle.Suspend`), then stops its worker.
func (c *Workers) Stop(address contracts.Address, ctx context.Context) error {
	c.route.Suspend(address)
	var stopped *outcome[struct{}]
	c.loop.Run(func() {
		record := c.addresses[address.Key]
		if record == nil {
			return
		}
		stopped = c.enqueue(record, func() (struct{}, error) { return struct{}{}, c.stop(record) })
	})
	if stopped == nil {
		return nil
	}
	_, err := stopped.await(ctx)
	return err
}

// Retry clears address's failure and suspension: it starts again now if held (`RouteHandle.Retry`).
func (c *Workers) Retry(address contracts.Address, _ context.Context) error {
	c.loop.Run(func() {
		if record := c.addresses[address.Key]; record != nil {
			record.failure = nil
			c.state.refresh()
			c.forget(record)
		}
	})
	c.route.Retry(address)
	return nil
}

// Step is a rollout step shipping the kind's current set to every worker, one result per worker (§4).
func (c *Workers) Step(name string) plugins.RolloutStep {
	return plugins.RolloutStep{Name: name, Kinds: []string{c.options.Kind}, Reload: func(_ *plugins.Built, applied map[string][]plugins.PluginScript, ctx context.Context) (plugins.StepResult, error) {
		hosts, err := c.Rollout(applied[c.options.Kind], ctx)
		return plugins.StepResult{Hosts: hosts}, err
	}}
}

// Close cancels starts, stops every worker; new starts reject `released/closed`.
func (c *Workers) Close(ctx context.Context) error {
	if err := c.close(ctx); err != nil {
		return err
	}
	// Shutdown draining: the component's own goroutines end with their work, as far as ctx lets Close wait for them.
	return c.tasks.wait(ctx)
}

func (c *Workers) close(ctx context.Context) error {
	var stops []*outcome[struct{}]
	var records []*routed
	c.loop.Run(func() {
		if !c.isClosing() {
			c.closing(closed())
		}
		if c.idleTimer != nil {
			c.idleTimer.Stop()
			c.idleTimer = nil
		}
		for _, key := range slices.Clone(c.order) {
			record := c.addresses[key]
			records = append(records, record)
			stops = append(stops, c.enqueue(record, func() (struct{}, error) { return struct{}{}, c.stop(record) }))
		}
	})
	// Each stop, then what its exit queued (the clear of a killed child's lock file).
	exited := func(ctx context.Context) error {
		for index, stop := range stops {
			if _, err := stop.await(ctx); err != nil {
				return err
			}
			var queue chan struct{}
			c.loop.Run(func() { queue = records[index].queue })
			select {
			case <-queue:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		return nil
	}
	// The base's copy stays until every child exited or was killed (a stopping worker may still need it), also when ctx ends the
	// wait first: nothing forks once closing, and only the directory this component wrote goes. The kit releases the data
	// directory's lock only after this close resolved.
	err := exited(ctx)
	if err == nil {
		c.removeBase()
		return nil
	}
	c.task(func() {
		_ = exited(chordctx.Background())
		c.removeBase()
	})
	return err
}

// closeWith: ctx's abort closes the component (the route's is aborted when the host closes).
func (c *Workers) closeWith(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	context.AfterFunc(ctx, func() {
		c.task(func() { _ = c.close(chordctx.Background()) })
	})
}

// routedOf is the record of address, made by its first use.
func (c *Workers) routedOf(address contracts.Address) *routed {
	record := c.addresses[address.Key]
	if record == nil {
		queue := make(chan struct{})
		close(queue)
		record = &routed{address: address, queue: queue}
		c.addresses[address.Key] = record
		c.order = append(c.order, address.Key)
	}
	return record
}

// enqueue runs operation after the address's earlier ones. Call it in a section.
func (c *Workers) enqueue(record *routed, operation func() (struct{}, error)) *outcome[struct{}] {
	return enqueue(c, record, operation, nil, nil)
}

// enqueue is Workers.enqueue for any result; after, if not nil, runs in the section that ends the operation's bookkeeping.
// applied, if not nil, is the route's hand-off for the result of an operation the route awaits: the next operation is released
// by the section that applies it, as Pi runs the route's `await` continuation before the next operation of the queue (the
// reaction on the promise the bookkeeping reaction settles); a route that applies nothing has no such continuation to wait for.
func enqueue[T any](c *Workers, record *routed, operation func() (T, error), after func(), applied *parking.Applied) *outcome[T] {
	record.pending++
	previous := record.queue
	next := make(chan struct{})
	record.queue = next
	run := newOutcome[T]()
	c.task(func() {
		<-previous
		value, err := operation()
		// The address's bookkeeping first, then whoever awaits the operation, then the next operation.
		c.loop.Run(func() {
			c.done(record)
			if after != nil {
				after()
			}
		})
		run.settle(value, err)
		if applied != nil {
			applied.Then(func() { close(next) })
			return
		}
		close(next)
	})
	return run
}

// done: an operation ended.
func (c *Workers) done(record *routed) {
	record.pending--
	c.forget(record)
}

// settled: a start settled: no longer starting.
func (c *Workers) settled(record *routed, started *outcome[host.ResolvedLink]) {
	if record.starting == started {
		record.starting = nil
	}
}

// forget forgets an address with no operation, no child and no failure.
func (c *Workers) forget(record *routed) {
	if record.pending == 0 && record.child == nil && record.failure == nil {
		delete(c.addresses, record.address.Key)
		c.order = slices.DeleteFunc(c.order, func(key string) bool { return key == record.address.Key })
		if c.options.Forgotten != nil {
			c.options.Forgotten(record.address)
		}
	}
}

// start is a start: the app's start data, the fork, the start message with the kind's current set, then `started` within
// the start timer (fork, lock and connect only). Its failure fails the address; `Close` ends it at any of its waits (an
// app's `Start` that ignores its context too), a forked child is stopped.
func (c *Workers) start(record *routed, routing context.Context) (host.ResolvedLink, error) {
	address := record.address
	// After the previous child's exit (its link may close first): a crash recorded meanwhile fails this start.
	var exited <-chan struct{}
	c.loop.Run(func() {
		if record.child != nil {
			exited = record.child.process.Exited()
		}
	})
	if exited != nil {
		<-exited
	}
	var crashed *failure
	var closing bool
	c.loop.Run(func() { crashed, closing = record.failure, c.isClosing() })
	if crashed != nil {
		return host.ResolvedLink{}, uncrossed(crashed.err)
	}
	if closing {
		return host.ResolvedLink{}, closed()
	}
	// The kind's worker base: a start builds it when nothing did, and fails while it failed.
	built, open := c.awaitBuild()
	if !open {
		return host.ResolvedLink{}, closed()
	}
	if built.Err != nil {
		return host.ResolvedLink{}, c.failLocked(record, cause{err: built.Err, public: &startFailed}, "")
	}
	// The start's context ends with the route's or with Close, and its registrations on both end with the start.
	own, release := chordctx.WithCancel(routing)
	defer release(nil)
	ctx := chordctx.WithAbortSignal(own, c.closingCtx)
	work := make(chan chordctx.Settled[delta.JsonValue], 1)
	// The app's start may ignore ctx and never return: it is not joined, its outcome is dropped once Close aborts the wait.
	//portlint:allow golifetime a call into the app's start hook, which may never return; the wait is bounded by closingCtx
	go func() {
		data, err := c.options.Start(address, ctx)
		work <- chordctx.Settled[delta.JsonValue]{Value: data, Err: err}
	}()
	data, err := chordctx.Await(c.closingCtx, work)
	if err != nil {
		if c.isClosing() {
			return host.ResolvedLink{}, closed()
		}
		return host.ResolvedLink{}, c.failLocked(record, cause{err: err, public: &startFailed}, "")
	}
	if c.isClosing() {
		return host.ResolvedLink{}, closed()
	}
	entry, err := c.baseFile()
	if err != nil {
		if c.isClosing() {
			return host.ResolvedLink{}, closed()
		}
		return host.ResolvedLink{}, c.failLocked(record, cause{err: err, public: &startFailed}, "")
	}
	var forked *child
	var forkErr error
	c.loop.Run(func() { forked, forkErr = c.fork(record, entry) })
	if forkErr != nil {
		return host.ResolvedLink{}, c.failLocked(record, cause{err: forkErr, public: &startFailed}, "")
	}
	spawned := forked
	timer := c.timer(startTimeout, func() {
		err := fmt.Errorf("worker %s: did not start within %d s", address.Key, int(startTimeout/time.Second))
		if spawned.cause == nil {
			spawned.cause = &cause{err: err, killed: true}
		}
		spawned.process.Kill()
	})
	defer c.loop.Run(func() { timer.Stop() })
	// Every app hook of the start runs in here: what one fails with stays private (`worker_failed`), its holders and
	// `State` get the fixed public message (Astra's recheck R3: `principal` threw through to a guest).
	link, err := c.startChild(record, spawned, data, ctx)
	if err == nil {
		return link, nil
	}
	// Closing is a stop (no failure); anything else fails the start.
	c.loop.Run(func() {
		if c.isClosing() {
			spawned.stopping = true
		} else if spawned.cause == nil {
			spawned.cause = &cause{err: err, public: &startFailed, killed: true}
		}
		spawned.process.Kill()
	})
	<-spawned.process.Exited()
	var final *failure
	c.loop.Run(func() { final = record.failure })
	if c.isClosing() {
		return host.ResolvedLink{}, closed()
	}
	if final != nil {
		return host.ResolvedLink{}, uncrossed(final.err)
	}
	return host.ResolvedLink{}, uncrossed(startFailed)
}

// startChild is the app hooks and the waits of a start after the fork: the shipped scripts, the lock, the start message,
// `started`, the principal.
func (c *Workers) startChild(record *routed, spawned *child, data delta.JsonValue, ctx context.Context) (host.ResolvedLink, error) {
	address := record.address
	scripts, err := c.options.Plugins.Scripts(c.options.Kind, ctx)
	if err != nil {
		return host.ResolvedLink{}, err
	}
	lock, err := c.options.Lock(address)
	if err != nil {
		return host.ResolvedLink{}, err
	}
	// A worker killed without its exit hooks (SIGKILL), maybe by an earlier server, left its socket file, which fails its
	// successors closed. The component is the one clearer of its addresses' locks, in the address's queue: one clear at a
	// time (decision "Stale lock files are cleared by the starter"). A live holder (an old worker still exiting) is kept and
	// waited for.
	if _, err := ClearStaleLock(lock, ctx); err != nil {
		return host.ResolvedLink{}, err
	}
	c.loop.Run(func() {
		spawned.shipped = keyOf(scripts)
		spawned.lock = lock
		spawned.process.Send(ToWorker{Kind: ToWorkerStart, Address: address, Start: data, Scripts: scripts, Lock: lock})
	})
	if _, err := spawned.started.await(c.closingCtx); err != nil {
		return host.ResolvedLink{}, err
	}
	principal, err := c.options.Principal(address)
	if err != nil {
		return host.ResolvedLink{}, err
	}
	return host.ResolvedLink{Link: spawned.process.Link, Principal: principal, Trusted: true}, nil
}

// fork forks the address's worker and records its events on it as they happen. It runs in a section.
func (c *Workers) fork(record *routed, entry string) (*child, error) {
	address := record.address
	var spawned *child
	options := ForkOptions{Entry: entry, ExecArgv: c.options.ExecArgv, Env: c.environment}
	if output := c.options.OnOutput; output != nil {
		options.OnOutput = func(stream string, chunk []byte) { output(address, stream, chunk) }
	}
	process, err := NewWorkerProcess(options, ProcessEvents{
		Control:    func(message FromWorker) { c.loop.Run(func() { c.control(record, spawned, message) }) },
		LinkLost:   func() { c.loop.Run(func() { c.linkLost(spawned) }) },
		LinkClosed: func() { c.loop.Run(func() { c.linkClosed(spawned) }) },
		Exited:     func(exit Exit) { c.loop.Run(func() { c.exited(record, spawned, exit) }) },
	})
	if err != nil {
		return nil, err
	}
	reloading := make(chan struct{})
	close(reloading)
	spawned = &child{
		process:   process,
		status:    WorkerStarting,
		started:   newOutcome[struct{}](),
		ready:     newOutcome[CrossedReport](),
		reloading: reloading,
	}
	record.child = spawned
	c.state.refresh()
	c.watchIdle()
	return spawned, nil
}

// control is a control message of child.
func (c *Workers) control(record *routed, child *child, message FromWorker) {
	switch message.Kind {
	case FromWorkerStarted:
		child.started.resolve(struct{}{})
	case FromWorkerReady:
		child.status = WorkerRunning
		child.idle = message.Idle
		if message.Idle {
			child.idleSince = c.clock.Now()
		}
		child.ready.resolve(*message.Report)
		if record.child == child {
			c.state.refresh()
		}
	case FromWorkerIdle:
		if message.Idle {
			if !child.idle {
				child.idle, child.idleSince = true, c.clock.Now()
			}
		} else {
			child.idle = false
		}
	case FromWorkerFatal:
		// Redacted by the child's `Redact`: public as it is.
		if child.cause == nil {
			public := *message.Error
			child.cause = &cause{err: uncrossed(public), public: &public}
		}
		child.started.reject(uncrossed(*message.Error))
		child.process.Kill()
	case FromWorkerReloaded:
		if child.reloaded != nil {
			child.reloaded(message)
		}
	}
}

// linkLost: this side closed child's link (the heartbeat, a protocol error): its child is stopped, its exit a lost link.
func (c *Workers) linkLost(child *child) {
	c.ended(child)
	if child.stopping {
		return
	}
	if child.cause == nil {
		child.cause = lostLink(child)
	}
	c.shutdown(child)
	c.state.refresh()
}

// linkClosed: the child's side closed its link (it is exiting, or it closed its IPC and lives on): its reloads end; one
// that does not exit within the grace is killed, its exit a lost link.
func (c *Workers) linkClosed(child *child) {
	c.ended(child)
	if child.stopping || child.grace != nil {
		return
	}
	lost := lostLink(child)
	child.grace = c.timer(grace, func() {
		if child.cause == nil {
			child.cause = lost
		}
		child.process.Kill()
	})
}

// exited: child exited: a stop's exit forgets it, as does a lost link the address recovers from (`worker_link_lost`: its
// holders' route resolves again, a start after this exit); any other is a crash that fails the address (`worker_exited` and
// `worker_failed` once, the full error and stderr tail for the app's logs only), except once the component closes.
func (c *Workers) exited(record *routed, child *child, exit Exit) {
	if child.grace != nil {
		child.grace.Stop()
	}
	clean := child.stopping && child.cause == nil
	why := child.cause
	if why == nil {
		why = &cause{err: exitError(record.address, exit, child)}
	}
	public := publicExit(exit, child)
	if why.public != nil {
		public = *why.public
	}
	child.started.reject(uncrossed(public))
	child.ready.reject(uncrossed(public))
	c.ended(child)
	// A child a signal ended (SIGKILL) ran no exit hooks: its socket file would stay until the address's next start. Cleared
	// in the queue, like a start's clear; a failure here is the next start's to report (it clears again).
	if lock := child.lock; lock != "" && exit.Signal != nil {
		c.enqueue(record, func() (struct{}, error) {
			_, _ = ClearStaleLock(lock, chordctx.Background())
			return struct{}{}, nil
		})
	}
	if record.child != child {
		return
	}
	record.child = nil
	if !clean && !c.isClosing() {
		if why.lost && c.recovers(record) {
			c.options.OnError(WorkerLinkLostError{Address: record.address.Key, Error: why.err})
		} else {
			// A failed start's kill is the component's own act (Opus's second recheck): only `worker_failed`.
			if !why.killed {
				c.options.OnError(WorkerExitedError{Address: record.address.Key, Code: exit.Code, Signal: exit.Signal})
			}
			c.fail(record, cause{err: why.err, public: &public, killed: why.killed}, child.process.StderrTail())
		}
	}
	c.state.refresh()
	c.forget(record)
}

// recovers is whether record recovers from its running child's lost link, recorded when it does: not within a minute of the
// last recovery, so a worker that loses every link fails instead of restarting in a loop. It runs in a section.
func (c *Workers) recovers(record *routed) bool {
	now := c.clock.Now()
	if record.recovered != nil && time.Duration(now-*record.recovered)*time.Millisecond < recoveryWindow {
		return false
	}
	record.recovered = &now
	return true
}

// ended: child's link is gone or it exited (the first of these): its outstanding reload and every one queued behind it
// settle, `stopped` when a stop asked for it, else failed.
func (c *Workers) ended(child *child) {
	if child.ended != nil {
		return
	}
	if child.stopping && child.cause == nil {
		child.ended = &ended{stopped: true}
	} else {
		child.ended = &ended{err: data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "worker: its link is gone"}, nil)}
	}
	if child.reloaded != nil {
		child.reloaded(FromWorker{Kind: FromWorkerReloaded, Error: &CrossedError{Message: "worker: its link is gone"}})
	}
}

// fail fails record with why (`worker_failed` once, with the full error and stderrTail). It runs in a section.
func (c *Workers) fail(record *routed, why cause, stderrTail string) {
	record.failure = &failure{err: *why.public, at: c.clock.Now()}
	c.options.OnError(WorkerFailedError{Address: record.address.Key, Error: why.err, StderrTail: stderrTail})
	c.state.refresh()
}

// failLocked is fail from outside a section; it returns the public error a start fails with.
func (c *Workers) failLocked(record *routed, why cause, stderrTail string) error {
	c.loop.Run(func() { c.fail(record, why, stderrTail) })
	return uncrossed(*why.public)
}

// stop is a stop: `shutdown`, the grace, then the tree killed; it returns at its exit.
func (c *Workers) stop(record *routed) error {
	var exited <-chan struct{}
	c.loop.Run(func() {
		child := record.child
		if child == nil {
			return
		}
		child.stopping = true
		c.shutdown(child)
		c.state.refresh()
		exited = child.process.Exited()
	})
	if exited != nil {
		<-exited
	}
	return nil
}

// shutdown asks child to shut down and kills its tree after the grace.
func (c *Workers) shutdown(child *child) {
	child.status = WorkerStopping
	child.process.Send(ToWorker{Kind: ToWorkerShutdown})
	if child.grace != nil {
		child.grace.Stop()
	}
	child.grace = c.timer(grace, func() { child.process.Kill() })
}

// watchIdle starts the idle check (every second) while any worker exists. It runs in a section.
func (c *Workers) watchIdle() {
	if c.idleTimer != nil {
		return
	}
	var arm func()
	arm = func() {
		c.idleTimer = c.timer(idleCheck, func() {
			c.idleTimer = nil
			if c.checkIdle() {
				arm()
			}
		})
	}
	arm()
}

// checkIdle queues a stop for every worker idle since `IdleMs`; it reports whether any worker is left.
func (c *Workers) checkIdle() bool {
	idleFor := c.idleFor()
	now := c.clock.Now()
	workers := 0
	for _, key := range slices.Clone(c.order) {
		record := c.addresses[key]
		child := record.child
		if child == nil {
			continue
		}
		workers++
		if child.idleStop || child.status != WorkerRunning {
			continue
		}
		if !child.idle || time.Duration(now-child.idleSince)*time.Millisecond < idleFor {
			continue
		}
		child.idleStop = true
		enqueue(c, record, func() (struct{}, error) { return struct{}{}, c.idleStop(record, child) }, nil, nil)
	}
	return workers > 0
}

// idleStop is an idle stop when it runs: only if child is still the address's and still idle (a hold may have come since).
func (c *Workers) idleStop(record *routed, child *child) error {
	var skip bool
	c.loop.Run(func() {
		child.idleStop = false
		if record.child != child || !child.idle {
			skip = true
			return
		}
		if time.Duration(c.clock.Now()-child.idleSince)*time.Millisecond < c.idleFor() {
			skip = true
		}
	})
	if skip {
		return nil
	}
	return c.stop(record)
}

// idleFor is how long a worker idles before it is stopped.
func (c *Workers) idleFor() time.Duration {
	if c.options.IdleMs != nil {
		return c.options.IdleMs()
	}
	return defaultIdle
}

// Rollout is the rollout step (§4), for an app that ships the set itself (without a plugins component, p2's compiled-in
// plugins): scripts is the set as this rollout applied it (Pi reads `plugins.current(kind)` in the
// same synchronous section as the rollout's ship; the component hands it over); the
// failures recorded before it cleared and the held ones retried (listed once ready or failed); the set shipped to every
// worker whose last applied set differs, in parallel. ctx's abort ends only this wait: the entries still waiting are listed
// with its error.
func (c *Workers) Rollout(scripts []plugins.PluginScript, ctx context.Context) ([]plugins.RolloutHost, error) {
	key := keyOf(scripts)
	type entry struct {
		host  string
		entry *outcome[plugins.RolloutHost]
	}
	var entries []entry
	// The failures recorded before this rollout clear; the route is called outside the section, as a resolve it starts enters
	// the component's loop itself.
	var failed []*routed
	c.loop.Run(func() {
		for _, name := range slices.Clone(c.order) {
			record := c.addresses[name]
			if record == nil || record.failure == nil {
				continue
			}
			record.failure = nil
			c.state.refresh()
			failed = append(failed, record)
		}
	})
	retried := make([]bool, len(failed))
	for index, record := range failed {
		retried[index] = c.route.Retry(record.address)
	}
	c.loop.Run(func() {
		for index, record := range failed {
			if started := record.starting; retried[index] && started != nil {
				entries = append(entries, entry{record.address.Key, c.retried(record, started)})
			} else {
				c.forget(record)
			}
		}
		for _, name := range c.order {
			record := c.addresses[name]
			child := record.child
			if child == nil || child.status == WorkerStopping || child.shipped == key {
				continue
			}
			entries = append(entries, entry{record.address.Key, c.reload(record.address.Key, child, scripts, key)})
		}
	})
	hosts := make([]plugins.RolloutHost, len(entries))
	for index, each := range entries {
		hosts[index] = bounded(each.host, each.entry, ctx)
	}
	return hosts, nil
}

// retried is a retried address's entry: its start's load report once ready, or its error.
func (c *Workers) retried(record *routed, started *outcome[host.ResolvedLink]) *outcome[plugins.RolloutHost] {
	entry := newOutcome[plugins.RolloutHost]()
	hostName := record.address.Key
	c.task(func() {
		if _, err := started.wait(); err != nil {
			entry.resolve(plugins.RolloutHost{Kind: plugins.RolloutErrored, Host: hostName, Error: err})
			return
		}
		var ready *outcome[CrossedReport]
		c.loop.Run(func() {
			if record.child != nil {
				ready = record.child.ready
			}
		})
		if ready == nil {
			entry.resolve(plugins.RolloutHost{Kind: plugins.RolloutErrored, Host: hostName, Error: errors.New("worker: exited")})
			return
		}
		report, err := ready.wait()
		if err != nil {
			entry.resolve(plugins.RolloutHost{Kind: plugins.RolloutErrored, Host: hostName, Error: err})
			return
		}
		entry.resolve(plugins.RolloutHost{Kind: plugins.RolloutReported, Host: hostName, Report: uncrossedReport(report)})
	})
	return entry
}

// reload ships scripts to child after its outstanding reload; it settles on `reloaded`, its link's loss or its exit. Only a
// reload that applied without failures marks the set shipped (a failed plugin is shipped again next time). It runs in a
// section.
func (c *Workers) reload(hostName string, child *child, scripts []plugins.PluginScript, key string) *outcome[plugins.RolloutHost] {
	reload := newOutcome[plugins.RolloutHost]()
	previous := child.reloading
	next := make(chan struct{})
	child.reloading = next
	c.task(func() {
		<-previous
		c.loop.Run(func() {
			settle := func(ended *ended) {
				if ended.stopped {
					reload.resolve(plugins.RolloutHost{Kind: plugins.RolloutStopped, Host: hostName})
				} else {
					reload.resolve(plugins.RolloutHost{Kind: plugins.RolloutErrored, Host: hostName, Error: ended.err})
				}
			}
			if child.ended != nil {
				settle(child.ended)
				return
			}
			child.reloaded = func(reply FromWorker) {
				child.reloaded = nil
				switch {
				case reply.Report != nil:
					if len(reply.Report.Failed) == 0 {
						child.shipped = key
					}
					reload.resolve(plugins.RolloutHost{Kind: plugins.RolloutReported, Host: hostName, Report: uncrossedReport(*reply.Report)})
				case child.ended != nil:
					settle(child.ended)
				default:
					reload.resolve(plugins.RolloutHost{Kind: plugins.RolloutErrored, Host: hostName, Error: uncrossed(*reply.Error)})
				}
			}
			child.process.Send(ToWorker{Kind: ToWorkerReload, Scripts: scripts})
		})
		<-reload.done
		close(next)
	})
	return reload
}

// closed is what new starts reject with once the component closes.
func closed() error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: "workers: closed"}, nil)
}

// linkLostPublic is the public error of a running worker's lost link that fails its address (a second one within the window).
var linkLostPublic = CrossedError{Message: "worker lost its link", Code: "unavailable"}

// lostLink is the cause of child's lost link (the heartbeat, a protocol error, its own close of its link) while it lives:
// after `ready`, one its address may recover from; before it, a failed start.
func lostLink(child *child) *cause {
	err := errors.New("worker: link lost")
	if child.status == WorkerRunning {
		public := linkLostPublic
		return &cause{err: err, public: &public, lost: true}
	}
	return &cause{err: err}
}

// exitError is the full error of an exit nobody asked for (the app's logs).
func exitError(address contracts.Address, exit Exit, child *child) error {
	how := fmt.Sprintf("code %s", codeText(exit.Code))
	if exit.Signal != nil {
		how = "signal " + *exit.Signal
	}
	when := "exited"
	if child.status == WorkerStarting {
		when = "exited before it was ready"
	}
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: fmt.Sprintf("worker %s: %s (%s)", address.Key, when, how)}, nil)
}

// codeText is a JavaScript number's text, or `null`.
func codeText(code *int) string {
	if code == nil {
		return "null"
	}
	return fmt.Sprint(*code)
}

// publicExit is the public error of an exit: a start that failed, or the exit's code or signal.
func publicExit(exit Exit, child *child) CrossedError {
	if child.status == WorkerStarting {
		return startFailed
	}
	how := "code " + codeText(exit.Code)
	if exit.Signal != nil {
		how = *exit.Signal
	}
	return CrossedError{Message: fmt.Sprintf("worker exited (%s)", how), Code: "unavailable"}
}

// keyOf is a set's identity: its plugins and digests.
func keyOf(scripts []plugins.PluginScript) string {
	keys := make([]string, len(scripts))
	for index, script := range scripts {
		keys[index] = scriptKey(script)
	}
	slices.Sort(keys)
	return strings.Join(keys, "\n")
}

// bounded is entry, or host listed with the abort's error once ctx aborts first (the entry goes on).
func bounded(hostName string, entry *outcome[plugins.RolloutHost], ctx context.Context) plugins.RolloutHost {
	result, err := entry.await(ctx)
	if err != nil {
		return plugins.RolloutHost{Kind: plugins.RolloutErrored, Host: hostName, Error: err}
	}
	return result
}

// workersState is the workers' state: a JSON watchable of every address's WorkerState, published once per microtask turn.
type workersState struct {
	state.Source
	component *Workers
	value     delta.JsonValue
	scheduled bool
}

var _ state.SourceImpl = (*workersState)(nil)

func newWorkersState(component *Workers, batch *state.Batch) *workersState {
	s := &workersState{component: component}
	s.Init(batch, s)
	return s
}

// Value is the state as JSON: plain statuses, numbers and crossed errors.
func (s *workersState) Value() (delta.JsonValue, error) {
	var value delta.JsonValue
	s.component.loop.Run(func() {
		if s.value == nil {
			s.value = stateOf(s.component)
		}
		value = s.value
	})
	return value, nil
}

// Deliver gives the watches the state as it is now, as one frame from whatever they had.
func (s *workersState) Deliver(ctx context.Context) error {
	value, _ := s.Value()
	s.Publish(value, []delta.Op{{"r", value}}, false, ctx)
	return nil
}

// refresh: something changed: published with the turn's other changes while someone watches or derives from it.
func (s *workersState) refresh() {
	s.value = nil
	if s.scheduled || (!s.Watched() && s.DependentSet().Len() == 0) {
		return
	}
	s.scheduled = true
	s.component.loop.Queue(func() {
		s.scheduled = false
		// The adoption returns no error, and Within returns only an adoption's.
		_ = s.Batch().Within(func() error {
			s.Changed()
			return nil
		}, chordctx.Background())
	})
}

// stateOf is every address's state: its child's, or its failure; none for an address with neither.
func stateOf(component *Workers) delta.JsonValue {
	result := delta.NewJsonObject(len(component.order))
	for _, key := range component.order {
		record := component.addresses[key]
		switch {
		case record.failure != nil:
			result.Set(key, delta.JsonObjectOf("status", string(WorkerFailed), "error", record.failure.err.json(), "at", float64(record.failure.at)))
		case record.child != nil:
			process := record.child.process
			result.Set(key, delta.JsonObjectOf("status", string(record.child.status), "pid", float64(process.Pid), "startedAt", float64(process.StartedAt)))
		}
	}
	return result
}

// scriptKey is one script's identity: its plugin and its digest.
func scriptKey(script plugins.PluginScript) string {
	digest := "failed"
	if script.Built.Kind == model.BuiltFacetScript {
		digest = script.Built.Digest
	}
	return script.Plugin.Name + "@" + digest
}
