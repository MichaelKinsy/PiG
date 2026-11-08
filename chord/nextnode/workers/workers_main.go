package workers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/src/next-node/workers/main.ts. The worker's side (slice 10 §3): `WorkerMain` reads the start
// message, takes the address's lock, makes the link to the server (`started` goes out when the app's host listens on it),
// runs the app's function with a plugin runtime over the shipped scripts, reports `ready` and each idle flip, applies
// reloads, and on `shutdown`, the parent's disconnect, `SIGTERM` or `SIGINT` aborts its context, runs the app's closers in
// reverse order, then closes its NodeHost, and exits.
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
	Plugins *WorkerPlugins
	// OnClose registers a closer: closers run in reverse order at shutdown.
	OnClose func(closer func(ctx context.Context) error)
}

// WorkerLoad is what WorkerPlugins.Load takes besides the host.
//
// The plugins of a worker run in a Node subprocess (docs/plan/chord-plugins-node-evaluator.md): Pi's worker host loads them in
// process, here the worker's Go host is the relay the server's holds pass through, and the host that serves the worker's
// address, the app's core facets and the plugins' is the NodeHost, the child of the worker's Go host. Load makes it, routes
// the worker's address kind to its link on the worker's Go host (`Route`, before `Load` of that host: the app's host has
// no address of its own, which the route would not reach) and loads the Go host.
type WorkerLoad struct {
	Kind string
	// Core is the app's own facets that are JavaScript: modules whose default export is the facet, loaded into the NodeHost
	// before the plugins (they import Pi's chord by its package name, `@earendil-works/chord`).
	Core []plugins.CoreFacet
	// GoCore are the app's own facets that are Go: they load on the worker's Go host, and the local types the NodeHost's
	// facets use are named by Bridged.
	GoCore []host.HostFacet
	// Bridged are the Go core facets whose local types the NodeHost's core facets and plugins use (plugins.BridgedFacet): the
	// NodeHost loads a proxy facet of each, in the same closure as the plugins. A BridgedFacet without a Host is the worker's.
	Bridged []plugins.BridgedFacet
	// HostKind is the kind of the worker's address, the one the server routes to this worker.
	HostKind *contracts.HostKind
	// Principal is who the NodeHost acts as, and how the worker's Go host stamps it; Peer is who the worker's Go host is for
	// the NodeHost (the server, whose holds it relays).
	Principal data.Principal
	Peer      data.Principal
	// Store is the NodeHost's store (RuntimeLoad.Store): the worker's own file, where the plugins' persisted documents and tables live;
	// CloseTimeout and ReadyTimeout are the NodeHost's (RuntimeLoad's). Nil: none, the host's defaults.
	Store        *plugins.StoreOptions
	CloseTimeout *time.Duration
	ReadyTimeout *time.Duration
	// ReloadDrainTimeout is the NodeHost's `reloadDrainTimeout` (RuntimeLoad's). Nil: the host's default.
	ReloadDrainTimeout *time.Duration
}

// BusyWatchable is a JSON watchable of numbers: a reference's member, or a source.
type BusyWatchable interface {
	Value() (delta.JsonValue, error)
	Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error)
}

// WorkerResult is what the app's function returns: extra keep-alive counts (busy while any is nonzero).
type WorkerResult struct {
	Busy BusyWatchable
	// Held is the number of holds the app's own code keeps on the NodeHost's objects (a reference it reads Busy through): the
	// NodeHost counts them as references like the server's, and they do not keep the worker busy.
	Held int
}

// WorkerMainOptions is what WorkerMain takes besides the app's function.
type WorkerMainOptions struct {
	// HostPackages is the host packages by specifier: the module files the NodeHost imports once and the plugins share.
	HostPackages plugins.HostPackages
	// Node is how the worker's Node subprocess is started (its output goes to this process's own unless OnOutput says
	// otherwise, as a worker's does).
	Node plugins.NodeOptions
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
	plugins.AdoptSocketOwner()
	newWorker(port, options).run(fn)
	select {}
}

type loadedHost struct {
	host    host.Host
	node    *plugins.NodeHost
	runtime *plugins.PluginRuntime
}

type worker struct {
	port    processPort
	options WorkerMainOptions
	context context.Context
	abort   func(cause error)
	started chan ToWorker

	mu sync.Mutex
	// closers are the app's (WorkerStart.OnClose); nodes are the NodeHosts WorkerPlugins.Load started, which shutdown closes
	// after every closer of the app, whenever each was registered.
	closers []func(ctx context.Context) error
	nodes   []*plugins.NodeHost
	lock    *ProcessLockHandle
	// reloads arrived, applied one after another once fn returned.
	reloads   [][]plugins.PluginScript
	loaded    *loadedHost
	ready     bool
	reloading bool
	// reloadApplying, when set (a test's seam; nil in the process of an app), is called, without the lock, each time a reload
	// was taken off the queue and is about to be applied.
	reloadApplying func()
	busy           BusyWatchable
	// activity is the keep-alive counts of the NodeHost, as it last reported them (NodeOptions.OnActivity).
	activity host.ActivityCounts
	// held is the app's own holds on the NodeHost's objects (WorkerResult.Held).
	held int
	// idle is the last idle state sent; sent says whether one was.
	idle     bool
	idleSent bool
	stopping bool
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
	port.OnDisconnect(w.shutdown)
	port.start()
	// `process.once(signal)` for each: a signal handled stops only its own listener, so the second of one takes the default
	// action and the other still reaches the shutdown (a no-op by then).
	for _, each := range []os.Signal{syscall.SIGTERM, syscall.SIGINT} {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, each)
		//portlint:allow golifetime the process ends through exitProcess, which is what the worker waits for
		go func() { //portlint:allow asyncorder the handler is main.ts:98 process.once(signal, () => void this.#shutdown()), a signal event with no ordering against other work
			// A throw in a handler is Pi's `unhandledRejection`: fatal (main.ts:100).
			defer func() {
				if recovered := recover(); recovered != nil {
					w.fatal(panicError{value: recovered})
				}
			}()
			<-signals
			signal.Stop(signals)
			w.shutdown()
		}()
	}
	return w
}

// run takes the lock, makes the parent link, runs fn, then reports ready and applies reloads.
func (w *worker) run(fn func(start WorkerStart, ctx context.Context) (*WorkerResult, error)) {
	//portlint:allow golifetime the process ends through exitProcess, which is what the worker waits for
	go func() { //portlint:allow asyncorder main.ts:106 `void this.#run(fn).catch(...)` starts the run unawaited, as here
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
	node := w.options.Node
	reportsActivity := node.OnActivity
	node.OnActivity = func(counts host.ActivityCounts) {
		w.noteActivity(counts)
		if reportsActivity != nil {
			reportsActivity(counts)
		}
	}
	runtime := plugins.NewPluginRuntime(node)
	var report host.LoadReport
	loading := &WorkerPlugins{worker: w, message: message, runtime: runtime, report: &report}
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
		w.held = result.Held
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
	go w.applyReloads()
	return nil
}

// WorkerPlugins is the worker's plugins: the shipped scripts of its kind, loaded on its host; the server's reloads applied
// to it.
type WorkerPlugins struct {
	worker  *worker
	message ToWorker
	runtime *plugins.PluginRuntime
	report  *host.LoadReport
}

// nodeHeartbeat is the heartbeat of the NodeHost's link: its host closes a link silent for it, and the link is attached
// (routed) when the first hold arrives, which may be long after the load. The link is a local socket whose end dies with its
// process, so the largest timer a Node host takes stands for none.
var nodeHeartbeat = host.HeartbeatOptions{Ping: new(maxTimer), Close: new(maxTimer)}

const maxTimer = time.Duration(1<<31-1) * time.Millisecond

// Load loads the app's Go core facets (options.GoCore) on h (a host without an address, not loaded yet), routes the worker's
// address kind to the link of the NodeHost on h (`Route`, before `Load` of h), starts the NodeHost of the worker's address with
// the start message's scripts of Kind, the JavaScript Core and a proxy for each of options.Bridged (the Go core facets whose local types
// the NodeHost's facets use; a BridgedFacet without a Host is h's), and returns the NodeHost's report. The Go host is loaded
// first: the proxies ask for their Go objects as they activate, inside the NodeHost's own load. When the NodeHost ends before
// the worker is told to stop, the worker ends with it: it exits with its exit code, or sends `fatal` with the exception it
// died of. Its close is the worker's last: shutdown closes it after the app's closers, those registered after
// Load too, so the app's host closes the link to the server before the NodeHost ends and fails a reload in flight (Pi's host
// closer closes the link before its reload rejects).
func (p *WorkerPlugins) Load(h host.Host, options WorkerLoad, ctx context.Context) (host.LoadReport, error) {
	address := p.message.Address
	ready := make(chan struct{})
	var nodeLink atomic.Pointer[plugins.NodeHost]
	if _, err := h.Route(options.HostKind, host.RouteOptions{
		// The server authorized the hold before it reached this worker.
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(_ contracts.Address, resolving context.Context) (host.ResolvedLink, error) {
			// The host resolves a route in a section it lends to this function: waiting for the NodeHost, which needs the host's
			// loop (its proxies get the Go core facets' objects), parks first.
			parking.Park(resolving)
			select {
			case <-ready:
			case <-resolving.Done():
				return host.ResolvedLink{}, context.Cause(resolving)
			}
			return host.ResolvedLink{Link: nodeLink.Load().Link(), Principal: options.Principal, Trusted: true}, nil
		},
	}, ctx); err != nil {
		return host.LoadReport{}, err
	}
	if _, err := h.Load(options.GoCore, ctx); err != nil {
		return host.LoadReport{}, err
	}
	bridged := slices.Clone(options.Bridged)
	for i := range bridged {
		if bridged[i].Host == nil {
			bridged[i].Host = h
		}
	}
	node, report, err := p.runtime.Load(plugins.RuntimeLoad{
		Kind: options.Kind, Core: options.Core, Scripts: p.message.Scripts, Modules: p.worker.options.HostPackages,
		Address: &address, Principal: options.Principal, Peer: options.Peer, Heartbeat: &nodeHeartbeat, Bridged: bridged,
		Store: options.Store, CloseTimeout: options.CloseTimeout, ReadyTimeout: options.ReadyTimeout, ReloadDrainTimeout: options.ReloadDrainTimeout,
	}, ctx)
	if err != nil {
		// A *plugins.PluginError comes with the loaded host (Pi's `host` is the caller's to stop): the worker ends instead.
		if node != nil {
			_ = node.Close(context.WithoutCancel(ctx))
		}
		return host.LoadReport{}, err
	}
	nodeLink.Store(node)
	close(ready)
	p.worker.mu.Lock()
	p.worker.loaded = &loadedHost{host: h, node: node, runtime: p.runtime}
	p.worker.nodes = append(p.worker.nodes, node)
	p.worker.mu.Unlock()
	go func() { //portlint:allow asyncorder pig runs the plugins in a Node subprocess (NodeHost) where Pi loads them in-process (main.ts:116-123 runtime.load), so there is no Pi hand-off to order; the watcher only observes the child exit
		<-node.Exited()
		p.worker.watchNode(node)
	}()
	*p.report = report
	return report, nil
}

// watchNode ends the worker with its NodeHost, once it has exited: a process that ended before the worker was told to stop is the worker's own
// crash (its exit code), and an exception it died of is the worker's `fatal`.
func (w *worker) watchNode(node *plugins.NodeHost) {
	if w.isStopping() {
		return
	}
	exit, ok := errors.AsType[*plugins.NodeExit](node.Err())
	if !ok {
		exitProcess(1)
	}
	if exit.Fatal != nil {
		w.fatal(exit.Fatal)
		return
	}
	code := 1
	if exitErr, ok := errors.AsType[*exec.ExitError](exit.Err); ok && exitErr.ExitCode() > 0 {
		code = exitErr.ExitCode()
	}
	exitProcess(code)
}

// noteActivity is the NodeHost's keep-alive counts arriving: an idle flip is reported.
func (w *worker) noteActivity(counts host.ActivityCounts) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.activity = counts
	w.flipLocked()
}

// follow follows the app's busy: each idle flip is reported. The NodeHost's activity is followed by noteActivity.
func (w *worker) follow() error {
	w.mu.Lock()
	busy := w.busy
	w.mu.Unlock()
	if busy != nil {
		watch, err := busy.Watch(w.context)
		if err != nil {
			return err
		}
		w.flipOn(watch)
	}
	return nil
}

// flipOn reports the idle state each time watch delivers a frame.
func (w *worker) flipOn(watch chord.WatchHandle[delta.JsonValue]) {
	watch.Start(func(_ context.Context, _ chord.WatchCall[delta.JsonValue]) error {
		w.flip()
		return nil
	})
}

// isIdleLocked reports whether every count is zero: the host's references, jobs and lingering objects, the app's busy, no
// reload.
func (w *worker) isIdleLocked() bool {
	if w.reloading {
		return false
	}
	if w.activity.References-w.held+w.activity.Jobs+w.activity.Lingering > 0 {
		return false
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
		w.shutdown()
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
			go w.applyReloads() //portlint:allow asyncorder main.ts:195 `if (this.#ready) void this.#applyReloads()` starts it unawaited, serialized by the reloading flag
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
		applying := w.reloadApplying
		w.mu.Unlock()
		if applying != nil {
			applying()
		}
		if loaded == nil {
			failure := CrossedError{Message: "worker: plugins.load was never called"}
			w.send(FromWorker{Kind: FromWorkerReloaded, Error: &failure})
			continue
		}
		var report host.ReloadReport
		err := w.safely(func() error {
			var reloadErr error
			report, reloadErr = loaded.runtime.Reload(loaded.node, scripts, w.context)
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

// shutdown stops the worker: `stopping` and the abort take effect before the next message is handled (as Pi's `#shutdown`
// runs up to its first await), then the closers run in reverse order, the NodeHosts' after them, and the process exits
// (forced after 3 s). The closers run on a goroutine of their own: they may wait for frames this call's caller (the
// channel's reader) delivers.
func (w *worker) shutdown() {
	w.mu.Lock()
	if w.stopping {
		w.mu.Unlock()
		return
	}
	w.stopping = true
	closers := slices.Clone(w.closers)
	nodes := slices.Clone(w.nodes)
	w.mu.Unlock()
	w.abort(errShuttingDown)
	time.AfterFunc(forcedExit, func() { exitProcess(0) })
	slices.Reverse(closers)
	slices.Reverse(nodes)
	for _, node := range nodes {
		closers = append(closers, func(ctx context.Context) error { return node.Close(ctx) })
	}
	//portlint:allow golifetime the closers end with the process, which exitProcess ends
	go func() { //portlint:allow asyncorder main.ts:189 and :98 call `void this.#shutdown()` unawaited; the closers run in that async function (main.ts:226-)
		// An OnError that throws rejects `#shutdown`, which nothing awaits: Pi's `unhandledRejection`, fatal (main.ts:100).
		defer func() {
			if recovered := recover(); recovered != nil {
				w.fatal(panicError{value: recovered})
			}
		}()
		for _, closer := range closers {
			if err := w.safely(func() error { return closer(chordctx.Background()) }); err != nil {
				w.options.OnError(err)
			}
		}
		exitProcess(0)
	}()
}

// fatal sends `fatal` with the redacted error, then exits.
func (w *worker) fatal(err any) {
	w.options.OnError(err)
	var redacted CrossedError
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				// Redact itself failed: only the fixed message crosses; the app's log gets what failed.
				w.options.OnError(panicError{value: recovered})
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
