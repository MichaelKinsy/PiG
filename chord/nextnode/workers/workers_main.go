package workers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/src/next-node/workers/main.ts. The worker's side (slice 10 §3): `WorkerMain` reads the start
// message, takes the address's lock, makes the link to the server (`started` goes out when the app's host listens on it),
// runs the app's function with a plugin runtime over the shipped scripts, reports `ready` and each idle flip, applies
// reloads, and on `shutdown`, the parent's disconnect, `SIGTERM` or `SIGINT` aborts its context, runs the app's closers in
// reverse order and exits.
//
// Forced Go differences. `workerMain` returns at once in Node, whose event loop keeps the process alive until it exits;
// `WorkerMain` blocks, because a Go program ends when its main returns, and the process ends through exitProcess like
// Node's `process.exit`. Node's single thread is a mutex over the worker's state: every message, signal and watch listener
// enters through it, and sending a control message never blocks (workers_channel.go), so one section never waits on
// another. `uncaughtException` and `unhandledRejection` are a panic of the worker's own goroutines (`fn`, the reload loop),
// recovered into `fatal`; a panic in a goroutine the app started ends the process with Go's own exit status and trace, which
// the server reports as a crash (`worker_exited`, `worker_failed`). `fn` and the closers return an error where Pi's reject.

// WorkerStart is what the app's function gets.
type WorkerStart struct {
	Address contracts.Address
	// Start is the start data the server's `start(address)` gave.
	Start delta.JsonValue
	// Parent is the link to the server: connect it before anything slow (`started` goes out then).
	Parent  link.Link
	Plugins WorkerPlugins
	// OnClose registers a closer: closers run in reverse order at shutdown.
	OnClose func(closer func(ctx context.Context) error)
}

// WorkerLoad is what WorkerPlugins.Load takes besides the host.
type WorkerLoad struct {
	Kind string
	// Core is the app's own facets.
	Core []host.HostFacet
}

// WorkerPlugins is the worker's plugins: the shipped scripts of its kind, loaded on its host; the server's reloads applied
// to it.
type WorkerPlugins interface {
	// Load loads h with the start message's scripts of Kind and Core.
	Load(h host.Host, options WorkerLoad, ctx context.Context) (host.LoadReport, error)
}

// BusyWatchable is a JSON watchable of numbers: a reference's member, or a source.
type BusyWatchable interface {
	Value() (delta.JsonValue, error)
	Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error)
}

// WorkerResult is what the app's function returns: extra keep-alive counts (busy while any is nonzero).
type WorkerResult struct {
	Busy BusyWatchable
}

// WorkerMainOptions is what WorkerMain takes besides the app's function.
type WorkerMainOptions struct {
	// HostPackages is the host packages by specifier: this process's own instances (functions cannot cross IPC).
	HostPackages plugins.HostPackages
	// Evaluator turns a shipped script into a facet: the plugin runtime's evaluation seam.
	Evaluator plugins.Evaluator
	// Redact is what crosses as `fatal` for an error (never the original message unless this returns it).
	Redact  func(err any) CrossedError
	OnError func(err any)
}

// forcedExit is how long a shutdown's closers get before the worker exits anyway: 3 s.
const forcedExit = 3 * time.Second

// WorkerMain runs this process as a worker: fn once the start message arrived and the lock is taken. An error from fn (or
// a panic in it) sends `fatal` (redacted) and exits. It does not return.
func WorkerMain(fn func(start WorkerStart, ctx context.Context) (*WorkerResult, error), options WorkerMainOptions) {
	serve(openProcessChannel(), fn, options)
}

// processPort is the worker's end of its IPC channel: the link port, and control messages beside the frames.
type processPort interface {
	link.IpcPort
	// start begins reading, once the worker listens.
	start()
	// SendControl sends a control message (the JSON text of its envelope); false when the channel is gone.
	SendControl(payload []byte) bool
	// sendControlThen is SendControl with then run once it is out (or dropped with the channel).
	sendControlThen(payload []byte, then func())
}

// serve runs the worker on port; it does not return.
func serve(port processPort, fn func(start WorkerStart, ctx context.Context) (*WorkerResult, error), options WorkerMainOptions) {
	newWorker(port, options).run(fn)
	select {}
}

type loadedHost struct {
	host    host.Host
	runtime plugins.PluginRuntime
}

type worker struct {
	port    processPort
	options WorkerMainOptions
	context context.Context
	abort   func(cause error)
	started chan ToWorker

	mu      sync.Mutex
	closers []func(ctx context.Context) error
	lock    ProcessLockHandle
	// reloads arrived, applied one after another once fn returned.
	reloads   [][]plugins.PluginScript
	loaded    *loadedHost
	ready     bool
	reloading bool
	busy      BusyWatchable
	// idle is the last idle state sent; sent says whether one was.
	idle     bool
	idleSent bool
	stopping bool
	// tasks are the goroutines the worker owns (the reload loop, the watches' listeners).
	tasks sync.WaitGroup
	// start is the one start message.
	startOnce sync.Once
}

func newWorker(port processPort, options WorkerMainOptions) *worker {
	w := &worker{port: port, options: options, started: make(chan ToWorker, 1)}
	w.context, w.abort = chordctx.WithCancel(chordctx.Background())
	port.OnMessage(func(message any) {
		if control, ok := controlOf[ToWorker](message); ok {
			w.message(control)
		}
	})
	port.OnDisconnect(func() { go w.shutdown() })
	port.start()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	//portlint:allow golifetime the process ends through exitProcess, which is what the worker waits for
	go func() {
		<-signals
		// `process.once`: the second signal takes the default action.
		signal.Stop(signals)
		w.shutdown()
	}()
	return w
}

// run takes the lock, makes the parent link, runs fn, then reports ready and applies reloads.
func (w *worker) run(fn func(start WorkerStart, ctx context.Context) (*WorkerResult, error)) {
	//portlint:allow golifetime the process ends through exitProcess, which is what the worker waits for
	go func() {
		// A throw while shutting down is the shutdown's own cancellation (its ctx aborted): no fatal, the closers run.
		err := w.safely(func() error { return w.runStart(fn) })
		if err != nil && !w.isStopping() {
			w.fatal(err)
		}
	}()
}

// safely runs work, returning its panic as an error: the worker's `uncaughtException`.
func (w *worker) safely(work func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
			} else {
				err = panicError{value: recovered}
			}
		}
	}()
	return work()
}

type panicError struct{ value any }

func (e panicError) Error() string { return fmt.Sprint("panic: ", e.value) }

// errShuttingDown is the reason the worker's context aborts with at shutdown.
var errShuttingDown = errors.New("worker: shutting down")

func (w *worker) isStopping() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopping
}

func (w *worker) runStart(fn func(start WorkerStart, ctx context.Context) (*WorkerResult, error)) error {
	var message ToWorker
	select {
	case message = <-w.started:
	case <-w.context.Done():
		return context.Cause(w.context)
	}
	lock, err := ProcessLock(message.Lock, w.context)
	if err != nil {
		return err
	}
	w.mu.Lock()
	// Held until the process exits.
	w.lock = lock
	w.mu.Unlock()
	parent := &parentLink{inner: link.IpcLink(w.port), started: func() { w.send(FromWorker{Kind: FromWorkerStarted}) }}
	runtime := plugins.NewPluginRuntime(w.options.Evaluator)
	var report host.LoadReport
	loading := loadingPlugins{worker: w, message: message, runtime: runtime, report: &report}
	start := WorkerStart{
		Address: message.Address,
		Start:   message.Start,
		Parent:  parent,
		Plugins: loading,
		OnClose: func(closer func(ctx context.Context) error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.closers = append(w.closers, closer)
		},
	}
	result, err := fn(start, w.context)
	if err != nil {
		return err
	}
	if w.isStopping() {
		return nil
	}
	w.mu.Lock()
	if result != nil {
		w.busy = result.Busy
	}
	w.mu.Unlock()
	if err := w.follow(); err != nil {
		return err
	}
	failed := map[string]bool{}
	for _, each := range report.Failed {
		failed[each.Plugin] = true
	}
	added := []string{}
	for _, script := range message.Scripts {
		if !failed[script.Plugin.Name] {
			added = append(added, script.Plugin.Name)
		}
	}
	loaded := CrossedReport{Plugins: ReloadedPlugins{Added: added, Changed: []string{}, Removed: []string{}}, Failed: crossedFailures(report.Failed), Objects: []CrossedObject{}}
	w.mu.Lock()
	w.ready = true
	w.idle, w.idleSent = w.isIdleLocked(), true
	w.sendLocked(FromWorker{Kind: FromWorkerReady, Idle: w.idle, Report: &loaded})
	w.mu.Unlock()
	//portlint:allow golifetime the reload loop returns when the reloads are applied or the worker stops; shutdown exits the process
	w.tasks.Go(func() {
		w.applyReloads()
	})
	return nil
}

// loadingPlugins is the plugins the app's function gets: it records the host and the load report.
type loadingPlugins struct {
	worker  *worker
	message ToWorker
	runtime plugins.PluginRuntime
	report  *host.LoadReport
}

func (p loadingPlugins) Load(h host.Host, options WorkerLoad, ctx context.Context) (host.LoadReport, error) {
	p.worker.mu.Lock()
	p.worker.loaded = &loadedHost{host: h, runtime: p.runtime}
	p.worker.mu.Unlock()
	report, err := p.runtime.Load(h, plugins.RuntimeLoad{Kind: options.Kind, Core: options.Core, Scripts: p.message.Scripts, Modules: p.worker.options.HostPackages}, ctx)
	if err != nil {
		return host.LoadReport{}, err
	}
	*p.report = report
	return report, nil
}

// follow follows the host's activity and the app's busy: each idle flip is reported.
func (w *worker) follow() error {
	var sources []BusyWatchable
	w.mu.Lock()
	if w.loaded != nil {
		sources = append(sources, w.loaded.host.Activity())
	}
	if w.busy != nil {
		sources = append(sources, w.busy)
	}
	w.mu.Unlock()
	for _, source := range sources {
		watch, err := source.Watch(w.context)
		if err != nil {
			return err
		}
		watch.Start(func(_ context.Context, _ chord.WatchCall[delta.JsonValue]) error {
			w.flip()
			return nil
		})
	}
	return nil
}

// isIdleLocked reports whether every count is zero: the host's references, jobs and lingering objects, the app's busy, no
// reload.
func (w *worker) isIdleLocked() bool {
	if w.reloading {
		return false
	}
	if w.loaded != nil {
		if value, err := w.loaded.host.Activity().Value(); err == nil {
			if activity, ok := value.(*delta.JsonObject); ok {
				if number(activity.Value("references"))+number(activity.Value("jobs"))+number(activity.Value("lingering")) > 0 {
					return false
				}
			}
		}
	}
	if w.busy != nil {
		if value, err := w.busy.Value(); err == nil {
			if counts, ok := value.(*delta.JsonObject); ok {
				for _, key := range counts.Keys() {
					if number(counts.Value(key)) != 0 {
						return false
					}
				}
			}
		}
	}
	return true
}

func number(value any) float64 {
	count, _ := value.(float64)
	return count
}

// flip reports the idle state when it flipped (after ready).
func (w *worker) flip() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flipLocked()
}

func (w *worker) flipLocked() {
	if !w.ready {
		return
	}
	idle := w.isIdleLocked()
	if w.idleSent && idle == w.idle {
		return
	}
	w.idle, w.idleSent = idle, true
	w.sendLocked(FromWorker{Kind: FromWorkerIdle, Idle: idle})
}

// message is a control message from the server.
func (w *worker) message(message ToWorker) {
	switch message.Kind {
	case ToWorkerStart:
		w.startOnce.Do(func() { w.started <- message })
	case ToWorkerShutdown:
		go w.shutdown()
	case ToWorkerReload:
		w.mu.Lock()
		// A worker shutting down applies no reload (its exit answers it).
		if w.stopping {
			w.mu.Unlock()
			return
		}
		w.reloads = append(w.reloads, message.Scripts)
		ready := w.ready
		w.mu.Unlock()
		if ready {
			//portlint:allow golifetime the reload loop returns when the reloads are applied or the worker stops; shutdown exits the process
			w.tasks.Go(func() {
				w.applyReloads()
			})
		}
	}
}

// applyReloads applies the reloads that arrived, one after another, each answered `reloaded`.
func (w *worker) applyReloads() {
	w.mu.Lock()
	if w.reloading || len(w.reloads) == 0 {
		w.mu.Unlock()
		return
	}
	loaded := w.loaded
	w.reloading = true
	w.flipLocked()
	w.mu.Unlock()
	for {
		w.mu.Lock()
		if w.stopping || len(w.reloads) == 0 {
			// The loop ended with the reloads as they were: one that arrives now finds reloading clear.
			w.reloading = false
			w.flipLocked()
			w.mu.Unlock()
			return
		}
		scripts := w.reloads[0]
		w.reloads = w.reloads[1:]
		w.mu.Unlock()
		if loaded == nil {
			failure := CrossedError{Message: "worker: plugins.load was never called"}
			w.send(FromWorker{Kind: FromWorkerReloaded, Error: &failure})
			continue
		}
		var report host.ReloadReport
		err := w.safely(func() error {
			var reloadErr error
			report, reloadErr = loaded.runtime.Reload(loaded.host, scripts, w.context)
			return reloadErr
		})
		if err != nil {
			failure := crossed(err)
			w.send(FromWorker{Kind: FromWorkerReloaded, Error: &failure})
			continue
		}
		crossedReport := crossedReport(report)
		w.send(FromWorker{Kind: FromWorkerReloaded, Report: &crossedReport})
	}
}

// shutdown aborts the context, runs the closers in reverse order and exits (forced after 3 s).
func (w *worker) shutdown() {
	w.mu.Lock()
	if w.stopping {
		w.mu.Unlock()
		return
	}
	w.stopping = true
	closers := slices.Clone(w.closers)
	w.mu.Unlock()
	w.abort(errShuttingDown)
	time.AfterFunc(forcedExit, func() { exitProcess(0) })
	slices.Reverse(closers)
	for _, closer := range closers {
		if err := w.safely(func() error { return closer(chordctx.Background()) }); err != nil {
			w.options.OnError(err)
		}
	}
	exitProcess(0)
}

// fatal sends `fatal` with the redacted error, then exits.
func (w *worker) fatal(err any) {
	w.options.OnError(err)
	var redacted CrossedError
	func() {
		defer func() {
			if recover() != nil {
				redacted = CrossedError{Message: "worker: fatal error"}
			}
		}()
		redacted = w.options.Redact(err)
	}()
	if !w.port.Connected() {
		exitProcess(1)
	}
	payload, marshalErr := envelope(FromWorker{Kind: FromWorkerFatal, Error: &redacted})
	if marshalErr != nil {
		exitProcess(1)
	}
	// The message is out (or dropped with the channel) when the callback runs.
	w.port.sendControlThen(payload, func() { exitProcess(1) })
}

// send sends a control message; dropped when the server is gone.
func (w *worker) send(message FromWorker) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sendLocked(message)
}

func (w *worker) sendLocked(message FromWorker) {
	if !w.port.Connected() {
		return
	}
	if payload, err := envelope(message); err == nil {
		w.port.SendControl(payload)
	}
}

// parentLink is the link to the server as the app gets it: `started` goes out when the app's host sets its listener
// (`Connect`).
type parentLink struct {
	inner link.Link

	mu      sync.Mutex
	started func()
}

var _ link.Link = (*parentLink)(nil)

func (l *parentLink) Send(message []byte) { l.inner.Send(message) }

func (l *parentLink) OnMessage(listener func(message []byte)) {
	l.inner.OnMessage(listener)
	l.mu.Lock()
	started := l.started
	l.started = nil
	l.mu.Unlock()
	if started != nil {
		started()
	}
}

func (l *parentLink) Close() { l.inner.Close() }

func (l *parentLink) OnClose(listener func()) { l.inner.OnClose(listener) }
