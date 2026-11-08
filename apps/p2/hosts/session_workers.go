package hosts

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/workers"
)

// Ports apps/p2/src/hosts/session-workers.ts: the session workers on Chord's workers component: one worker process per
// session, started on the first hold, stopped when idle, its failure kept until a retry. On top: worker generations (in
// the worker principal and start data), logs and owner notices, restarts after a crash (as v1), and the lifecycle steps of
// relocation and cloning, serialized per session.
//
// Forced Go differences. A step that waits for the component is a blocking call with a context; the per-session chain of
// promises is a queue of channels (serial). The workers component's plugins are empty: p2's plugins are compiled in
// (D473), so no script set is built and no Node process starts.

// SessionPrefix is the log source prefix of a session's worker.
const SessionPrefix = "session:"

// workerKind is the facet kind of the session workers' plugins.
const workerKind = "worker"

// Crash restarts: how many p2 makes of a session's worker within crashWindow; the next crash waits for Retry.
const (
	crashRestarts = 5
	crashWindow   = 60 * time.Second
)

// SessionWorkersOptions is what NewSessionWorkers takes.
type SessionWorkersOptions struct {
	Host    host.Host
	App     string
	DataDir string
	Fake    bool
	// Entry is the worker's executable and ExecArgv its arguments (the p2 command's `worker` subcommand).
	Entry    string
	ExecArgv []string
	Env      map[string]string
	// IdleMs is how long a worker with no references, calls or Durable tasks stays: 5 minutes by default.
	Idle time.Duration
	// MoveTimeout is how long a running worker may take to follow a relocation before it is restarted instead (60 s by
	// default).
	MoveTimeout time.Duration
	// Values is the start data's values (`WorkerInit.values`) for a session; an error refuses the start.
	Values func(session string, ctx context.Context) (*delta.JsonObject, error)
}

// SessionWorkers are the session workers (p2runtime.SessionWorkerControl).
type SessionWorkers struct {
	options SessionWorkersOptions
	plugins *plugins.Plugins
	workers *workers.Workers

	mu          sync.Mutex
	generations map[string]int
	generation  int
	listeners   map[int]func(p2runtime.HostEvent)
	listenerID  int
	observers   map[int]host.FollowObserver[any]
	observerID  int
	// states are the workers' states as of the last publication (`workerStates`), by session, in the order first seen.
	states       map[string]p2runtime.WorkerState
	stateOrder   []string
	writers      map[string]*p2runtime.LogWriter
	reasons      map[string]string
	exits        map[string]string
	crashRestart map[string][]time.Time
	// last is the status of each session's worker at the last publication, to emit changes only.
	last    map[string]string
	lastPid map[string]int
	chains  map[string]chan struct{}
	stop    func()
	done    chan struct{}
}

var _ p2runtime.SessionWorkerControl = (*SessionWorkers)(nil)

// NewSessionWorkers creates Chord's workers component for the sessions (the route of the session host kind), then follows
// it.
func NewSessionWorkers(options SessionWorkersOptions, ctx context.Context) (*SessionWorkers, error) {
	w := &SessionWorkers{
		options: options, generations: map[string]int{}, listeners: map[int]func(p2runtime.HostEvent){}, observers: map[int]host.FollowObserver[any]{},
		states: map[string]p2runtime.WorkerState{}, writers: map[string]*p2runtime.LogWriter{}, reasons: map[string]string{},
		exits: map[string]string{}, crashRestart: map[string][]time.Time{}, last: map[string]string{}, lastPid: map[string]int{}, chains: map[string]chan struct{}{},
		done: make(chan struct{}),
	}
	p2runtime.ProcessLogs.Redactor.AddDirectory(options.DataDir)
	for key, value := range options.Env {
		if isSecretName(key) {
			p2runtime.ProcessLogs.Redactor.AddSecret(value)
		}
	}
	dataDir, err := filepath.EvalSymlinks(options.DataDir)
	if err != nil {
		return nil, err
	}
	idle := options.Idle
	if idle == 0 {
		idle = 5 * time.Minute
	}
	// No roots: the set of scripts is empty and nothing is built or started (the workers' plugins are compiled in).
	w.plugins = plugins.CreatePlugins(plugins.PluginsOptions{Kinds: []plugins.KindDefinition{{Name: workerKind}}}, ctx)
	w.workers, err = workers.CreateWorkers(options.Host, p2runtime.SessionKind, workers.WorkersOptions{
		Kind:     workerKind,
		Entry:    options.Entry,
		ExecArgv: options.ExecArgv,
		Env:      options.Env,
		// Owners reach every session, the server's own code too; guests only the sessions their grant names.
		Authorize: func(principal data.Principal, address contracts.Address) (bool, error) {
			who, err := p2runtime.FromChord(principal)
			if err != nil {
				return false, nil
			}
			if _, ok := who.(p2runtime.ServerPrincipal); ok {
				return true, nil
			}
			return p2runtime.CanView(who, sessionOf(address)), nil
		},
		Principal: func(address contracts.Address) (data.Principal, error) {
			session := sessionOf(address)
			return p2runtime.ToChord(p2runtime.WorkerPrincipal{Session: session, Generation: w.Generation(session)}), nil
		},
		Start: func(address contracts.Address, ctx context.Context) (delta.JsonValue, error) {
			session := sessionOf(address)
			w.mu.Lock()
			w.generation++
			generation := w.generation
			w.generations[session] = generation
			w.mu.Unlock()
			p2runtime.ProcessLogs.Append(SessionPrefix+session, p2runtime.LogEvent, fmt.Sprintf("Starting worker generation %d", generation))
			values, err := options.Values(session, ctx)
			if err != nil {
				return nil, err
			}
			init := p2runtime.WorkerInit{App: options.App, DataDir: options.DataDir, Fake: options.Fake, Generation: generation, Values: values}
			return init.JSON(), nil
		},
		Lock: func(address contracts.Address) (string, error) {
			return "p2:" + dataDir + ":worker:" + sessionOf(address), nil
		},
		IdleMs:  func() time.Duration { return idle },
		Plugins: w.plugins,
		OnOutput: func(address contracts.Address, stream string, chunk []byte) {
			_, _ = w.writer(sessionOf(address), stream).Write(chunk)
		},
		OnError: w.failed,
	}, ctx)
	if err != nil {
		w.plugins.Close()
		return nil, err
	}
	if err := w.follow(); err != nil {
		_ = w.Close(ctx)
		return nil, err
	}
	return w, nil
}

func isSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, part := range []string{"key", "token", "secret", "password", "cookie", "auth"} {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// sessionOf is the session an address names.
func sessionOf(address contracts.Address) string {
	id, _ := address.Params.Value("id").(string)
	return id
}

func addressOf(session string) contracts.Address {
	return p2runtime.SessionKind.Address(delta.JsonObjectOf("id", session))
}

// Workers is Chord's workers component for the sessions.
func (w *SessionWorkers) Workers() *workers.Workers { return w.workers }

// Generation is the generation of the session's latest worker (0: never started).
func (w *SessionWorkers) Generation(session string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.generations[session]
}

func (w *SessionWorkers) stateObject() *delta.JsonObject {
	value, _ := w.workers.State().Value()
	object, _ := value.(*delta.JsonObject)
	return object
}

// statusOf is the status of the session's worker in the component's state: "" when it has none.
func (w *SessionWorkers) statusOf(session string) string {
	return statusIn(w.stateObject(), session)
}

func statusIn(states *delta.JsonObject, session string) string {
	if states == nil {
		return ""
	}
	entry, _ := states.Value("session|" + session).(*delta.JsonObject)
	if entry == nil {
		return ""
	}
	status, _ := entry.Value("status").(string)
	return status
}

// Running is the sessions whose worker is running now.
func (w *SessionWorkers) Running() []string {
	states := w.stateObject()
	var running []string
	if states == nil {
		return running
	}
	for _, key := range states.Keys() {
		if session, ok := strings.CutPrefix(key, "session|"); ok && statusIn(states, session) == "running" {
			running = append(running, session)
		}
	}
	return running
}

// Subscribe calls listener with every change of a worker's state.
func (w *SessionWorkers) Subscribe(listener func(p2runtime.HostEvent)) func() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.listenerID++
	id := w.listenerID
	w.listeners[id] = listener
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.listeners, id)
	}
}

// Observe follows the workers' states with a synchronous observer (`Hosts.workers`).
func (w *SessionWorkers) Observe(observer host.FollowObserver[any], _ context.Context) (*host.FollowAttachment[delta.JsonValue], error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.observerID++
	id := w.observerID
	w.observers[id] = observer
	return &host.FollowAttachment[delta.JsonValue]{Value: w.stateJSONLocked(), Detach: func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.observers, id)
	}}, nil
}

func (w *SessionWorkers) stateJSONLocked() *delta.JsonObject {
	object := delta.NewJsonObject(len(w.states))
	for _, session := range w.stateOrder {
		object.Set(session, w.states[session].JSON())
	}
	return object
}

// serial runs step after the session's earlier lifecycle steps; ctx ends the wait for them, not a step under way.
func (w *SessionWorkers) serial(ctx context.Context, session string, step func() error) error {
	w.mu.Lock()
	previous := w.chains[session]
	done := make(chan struct{})
	w.chains[session] = done
	w.mu.Unlock()
	defer func() {
		close(done)
		w.mu.Lock()
		if w.chains[session] == done {
			delete(w.chains, session)
		}
		w.mu.Unlock()
	}()
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return step()
}

// until blocks until pred holds for the workers' states (read now, then at each change), or ctx ends.
func (w *SessionWorkers) until(ctx context.Context, pred func(states *delta.JsonObject) bool) error {
	if pred(w.stateObject()) {
		return nil
	}
	watch, err := w.workers.State().Watch(ctx)
	if err != nil {
		return err
	}
	defer func() { _, _ = watch.Stop() }()
	changed := make(chan struct{}, 1)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			select {
			case changed <- struct{}{}:
			default:
			}
		}
		return nil
	})
	if pred(watch.Value().(*delta.JsonObject)) {
		return nil
	}
	for {
		select {
		case <-changed:
			if pred(w.stateObject()) {
				return nil
			}
		case <-watch.Closed():
			return errors.New("the workers' state ended")
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// Relocate moves a session, serialized with starts, stops and pauses.
func (w *SessionWorkers) Relocate(ctx context.Context, session string, steps p2runtime.RelocationSteps) error {
	source := SessionPrefix + session
	return w.serial(ctx, session, func() error {
		status := w.statusOf(session)
		if status == "starting" || status == "stopping" {
			return errors.New("Background work is still running (for example a message waiting for a subagent's answer): wait for it or stop the subagent before changing directory")
		}
		// Not running: the next start reads the moved catalog; holders arriving meanwhile wait (suspended).
		if status != "running" {
			return w.suspended(session, steps.Update)
		}
		refusal, err := steps.Prepare(ctx)
		if err != nil {
			if w.statusOf(session) != "running" {
				return w.suspended(session, steps.Update)
			}
			// The fence may be up (the failure came after it): never leave a worker that refuses everything.
			p2runtime.ProcessLogs.Append(source, p2runtime.LogEvent, fmt.Sprintf("Preparing a move failed (%s); stopping the worker", err))
			_ = w.stopped(session)
			return err
		}
		if refusal != "" {
			return errors.New(refusal)
		}
		if err := steps.Update(ctx); err != nil {
			if w.statusOf(session) == "running" {
				if failure := steps.Cancel(ctx); failure != nil {
					p2runtime.ProcessLogs.Append(source, p2runtime.LogEvent, fmt.Sprintf("Lifting the move's fence failed (%s); stopping the worker", failure))
					_ = w.stopped(session)
				}
			}
			return err
		}
		if w.statusOf(session) != "running" {
			return nil
		}
		// Bounded like a start: a worker that does not follow in time, or exits meanwhile, is restarted below.
		timeout := w.options.MoveTimeout
		if timeout == 0 {
			timeout = 60 * time.Second
		}
		if err := w.whileRunning(ctx, session, timeout, steps.Move); err != nil {
			p2runtime.ProcessLogs.Append(source, p2runtime.LogEvent, fmt.Sprintf("The worker could not follow the move (%s); restarting it", err))
			// The catalog is moved: the next start (now, if held) reads it; a failed start is the worker's error.
			return w.suspended(session, func(context.Context) error { return nil })
		}
		return nil
	})
}

// Pause runs work while the session's worker is stopped.
func (w *SessionWorkers) Pause(ctx context.Context, session string, prepare func(ctx context.Context) (string, error), work func(ctx context.Context) error, doing string) error {
	return w.serial(ctx, session, func() error {
		status := w.statusOf(session)
		if status == "starting" || status == "stopping" {
			return fmt.Errorf("Background work is still running (for example a message waiting for a subagent's answer): wait for it or stop the subagent before %s", doing)
		}
		if status == "running" {
			refusal, err := prepare(ctx)
			if err != nil {
				if w.statusOf(session) == "running" {
					p2runtime.ProcessLogs.Append(SessionPrefix+session, p2runtime.LogEvent, fmt.Sprintf("Preparing to pause failed (%s); stopping the worker", err))
					_ = w.suspended(session, func(context.Context) error { return nil })
					return err
				}
			}
			if refusal != "" {
				return errors.New(refusal)
			}
		}
		return w.suspended(session, work)
	})
}

// Retry stops the previous instance, clears the failure and starts the worker again, resolving with the start's outcome.
func (w *SessionWorkers) Retry(ctx context.Context, session string) (p2runtime.WorkerState, error) {
	if err := w.Resume(ctx, session); err != nil {
		return p2runtime.WorkerState{}, err
	}
	// A hold of the server's own starts the worker when nothing else holds it; its outcome is the answer.
	boundary, err := w.options.Host.Hold(ctx, p2runtime.WorkerBoundary, delta.JsonObjectOf("session", session), host.GetOptions{})
	if err != nil {
		return p2runtime.WorkerState{}, err
	}
	defer boundary.Release()
	if err := w.until(ctx, func(states *delta.JsonObject) bool {
		status := statusIn(states, session)
		return status == "running" || status == "failed"
	}); err != nil {
		return p2runtime.WorkerState{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if state, ok := w.states[session]; ok {
		return state, nil
	}
	return p2runtime.WorkerState{Running: false, Generation: w.generations[session]}, nil
}

// Resume clears the session's failure or suspension (a restore).
func (w *SessionWorkers) Resume(ctx context.Context, session string) error {
	w.mu.Lock()
	delete(w.crashRestart, session)
	w.mu.Unlock()
	return w.serial(ctx, session, func() error {
		return w.suspended(session, func(context.Context) error {
			source := SessionPrefix + session
			dismissNotices(source)
			p2runtime.ProcessLogs.Append(source, p2runtime.LogEvent, "Worker retry requested")
			return nil
		})
	})
}

func dismissNotices(source string) {
	for _, notice := range p2runtime.ProcessLogs.Notices() {
		if notice.Source == source {
			p2runtime.ProcessLogs.DismissNotice(notice.ID)
		}
	}
}

// Stop stops the session's worker and clears its failure: the next hold (one waiting now too) starts it again.
func (w *SessionWorkers) Stop(ctx context.Context, session string) error {
	return w.serial(ctx, session, func() error {
		return w.suspended(session, func(context.Context) error {
			dismissNotices(SessionPrefix + session)
			return nil
		})
	})
}

// Suspend stops an archived session's worker for good: its holders wait suspended instead of starting a worker the
// archived session refuses. Retry (restore) ends the suspension; so does the release of its last holder.
func (w *SessionWorkers) Suspend(ctx context.Context, session string) error {
	w.mu.Lock()
	delete(w.crashRestart, session)
	w.mu.Unlock()
	return w.serial(ctx, session, func() error {
		if err := w.workers.Stop(addressOf(session), chordctx.Background()); err != nil {
			return err
		}
		dismissNotices(SessionPrefix + session)
		return nil
	})
}

// Close stops every worker; their exits are no news to subscribers (the server's objects close next).
func (w *SessionWorkers) Close(ctx context.Context) error {
	w.mu.Lock()
	w.listeners = map[int]func(p2runtime.HostEvent){}
	stop := w.stop
	w.stop = nil
	w.mu.Unlock()
	if stop != nil {
		stop()
	}
	err := w.workers.Close(ctx)
	w.plugins.Close()
	return err
}

// suspended runs work while the session's worker is stopped and its holders wait (suspended); then starts it if held and
// waits for that start's outcome (running or failed), as callers read the worker's state next.
func (w *SessionWorkers) suspended(session string, work func(ctx context.Context) error) error {
	address := addressOf(session)
	if err := w.workers.Stop(address, chordctx.Background()); err != nil {
		return err
	}
	err := work(chordctx.Background())
	if retry := w.workers.Retry(address, chordctx.Background()); retry != nil && err == nil {
		err = retry
	}
	if started := w.started(session); started != nil && err == nil {
		err = started
	}
	return err
}

// stopped stops the session's worker; its holders start a new one (not waited for: the caller reports the stop).
func (w *SessionWorkers) stopped(session string) error {
	address := addressOf(session)
	if err := w.workers.Stop(address, chordctx.Background()); err != nil {
		return err
	}
	return w.workers.Retry(address, chordctx.Background())
}

// whileRunning runs step, failing as soon as the session's worker is no longer running (it exited while the step waits on
// it) or after timeout.
func (w *SessionWorkers) whileRunning(ctx context.Context, session string, timeout time.Duration, step func(ctx context.Context) error) error {
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- step(stepCtx) }()
	exited := make(chan error, 1)
	go func() {
		exited <- w.until(stepCtx, func(states *delta.JsonObject) bool { return statusIn(states, session) != "running" })
	}()
	select {
	case err := <-result:
		cancel()
		<-exited
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("Moving the worker took longer than %s", timeout)
		}
		return err
	case err := <-exited:
		if err != nil {
			// The wait itself ended (the timeout, or ctx).
			cancel()
			stepErr := <-result
			if errors.Is(stepErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("Moving the worker took longer than %s", timeout)
			}
			return stepErr
		}
		cancel()
		<-result
		return errors.New("The worker exited")
	}
}

// started blocks until a start of the session's worker the retry began (its holders') is running or failed.
func (w *SessionWorkers) started(session string) error {
	// The route resolves its held addresses asynchronously: the start shows within a few turns, or never (unheld).
	deadline := time.Now().Add(50 * time.Millisecond)
	for w.statusOf(session) == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.statusOf(session) != "starting" {
		return nil
	}
	return w.until(chordctx.Background(), func(states *delta.JsonObject) bool { return statusIn(states, session) != "starting" })
}

func (w *SessionWorkers) writer(session, stream string) *p2runtime.LogWriter {
	source := SessionPrefix + session
	key := session + ":" + stream
	w.mu.Lock()
	defer w.mu.Unlock()
	writer := w.writers[key]
	if writer == nil {
		if stream == "stdout" {
			writer = p2runtime.NewLogWriter(source, p2runtime.LogStdout, nil, nil)
		} else {
			writer = p2runtime.NewLogWriter(source, p2runtime.LogStderr, nil, func(line string) {
				if strings.HasPrefix(line, "p2 worker:") && !strings.Contains(line, "aborted:") {
					p2runtime.ProcessLogs.Notice(source, line)
				}
			})
		}
		w.writers[key] = writer
	}
	return writer
}

// failed is a worker's start, crash or failure: an owner notice (redacted), with the stderr tail as Pi Mobile; a crash is
// "Worker exited (signal): tail" in the notice and the status error too, as in v1.
func (w *SessionWorkers) failed(report workers.WorkersError) {
	redact := p2runtime.ProcessLogs.Redactor.Redact
	switch typed := report.(type) {
	case workers.WorkerExitedError:
		// Chord reports the exit right before its `worker_failed`.
		session := strings.TrimPrefix(typed.Address, "session|")
		reason := "Worker exited ("
		switch {
		case typed.Signal != nil:
			reason += *typed.Signal
		case typed.Code != nil:
			reason += fmt.Sprint(*typed.Code)
		}
		w.mu.Lock()
		w.exits[session] = reason + ")"
		w.mu.Unlock()
		w.crashed(session)
	case workers.WorkerFailedError:
		session := strings.TrimPrefix(typed.Address, "session|")
		w.mu.Lock()
		exit, exited := w.exits[session]
		delete(w.exits, session)
		w.mu.Unlock()
		reason := exit
		if !exited {
			reason = typed.Error.Error()
		}
		notice := reason
		if tail := strings.TrimSpace(typed.StderrTail); tail != "" {
			notice += ": " + tail
		}
		notice = redact(notice)
		w.mu.Lock()
		if exited {
			w.reasons[session] = notice
		} else {
			w.reasons[session] = redact(reason)
		}
		w.mu.Unlock()
		p2runtime.ProcessLogs.Notice(SessionPrefix+session, notice)
	default:
		fmt.Printf("p2 workers: %v\n", report)
	}
}

// crashed is a running worker that exited unasked (SIGKILL, OOM): as in v1 its holders start a new one, up to
// crashRestarts such restarts within crashWindow; a crash past that stays failed until Retry (no crash loop). A worker that
// crashes while starting stays failed too.
func (w *SessionWorkers) crashed(session string) {
	w.mu.Lock()
	if w.last["session|"+session] != "running" {
		w.mu.Unlock()
		return
	}
	now := time.Now()
	recent := slices.DeleteFunc(slices.Clone(w.crashRestart[session]), func(at time.Time) bool { return now.Sub(at) >= crashWindow })
	source := SessionPrefix + session
	if len(recent) >= crashRestarts {
		w.crashRestart[session] = recent
		w.mu.Unlock()
		p2runtime.ProcessLogs.Append(source, p2runtime.LogEvent, fmt.Sprintf("Worker crashed %d times within a minute; waiting for Retry", len(recent)+1))
		return
	}
	w.crashRestart[session] = append(recent, now)
	w.mu.Unlock()
	p2runtime.ProcessLogs.Append(source, p2runtime.LogEvent, "Worker crashed; restarting it for its holders")
	// After the session's running steps (one may have restarted it already: then nothing is left to do). A stop first: a
	// holder's start already waiting on this exit fails with the crash, and a bare retry while that start runs would be
	// lost; the stop queues behind it, the retry after the stop starts the holders' worker.
	//portlint:allow golifetime one queued step of the session's serial chain, which ends with the host
	go func() {
		_ = w.serial(chordctx.Background(), session, func() error {
			if w.statusOf(session) == "failed" {
				return w.stopped(session)
			}
			return nil
		})
	}()
}

// follow publishes every change of the workers' states, as HostEvents and to the observers of `Hosts.workers`.
func (w *SessionWorkers) follow() error {
	watch, err := w.workers.State().Watch(chordctx.Background())
	if err != nil {
		return err
	}
	w.publish(watch.Value().(*delta.JsonObject))
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			if states, ok := call.Value.(*delta.JsonObject); ok {
				w.publish(states)
			}
		}
		return nil
	})
	w.stop = func() { _, _ = watch.Stop() }
	return nil
}

func (w *SessionWorkers) publish(states *delta.JsonObject) {
	redact := p2runtime.ProcessLogs.Redactor.Redact
	w.mu.Lock()
	seen := map[string]bool{}
	var keys []string
	for key := range w.last {
		seen[key] = true
		keys = append(keys, key)
	}
	for _, key := range states.Keys() {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	var events []p2runtime.HostEvent
	for _, key := range keys {
		entry, _ := states.Value(key).(*delta.JsonObject)
		status := ""
		if entry != nil {
			status, _ = entry.Value("status").(string)
		}
		pid := -1
		if entry != nil {
			if value, ok := entry.Value("pid").(float64); ok {
				pid = int(value)
			}
		}
		if previous, ok := w.last[key]; ok && previous == status && w.lastPid[key] == pid {
			continue
		}
		session := strings.TrimPrefix(key, "session|")
		generation := w.generations[session]
		event := p2runtime.HostEvent{Session: session, WorkerState: p2runtime.WorkerState{Generation: generation}}
		switch {
		case entry == nil:
			delete(w.last, key)
			delete(w.lastPid, key)
		case status == "failed":
			message := ""
			if failure, ok := entry.Value("error").(*delta.JsonObject); ok {
				message, _ = failure.Value("message").(string)
			}
			reason, ok := w.reasons[session]
			if !ok {
				reason = redact(message)
			}
			event.Error = &reason
			w.last[key], w.lastPid[key] = status, pid
		default:
			event.Running = status == "running"
			if pid >= 0 {
				event.Pid = &pid
			}
			w.last[key], w.lastPid[key] = status, pid
		}
		events = append(events, event)
		if _, known := w.states[event.Session]; !known {
			w.stateOrder = append(w.stateOrder, event.Session)
		}
		w.states[event.Session] = event.WorkerState
	}
	if len(events) == 0 {
		w.mu.Unlock()
		return
	}
	listeners := make([]func(p2runtime.HostEvent), 0, len(w.listeners))
	for _, id := range sortedIDs(w.listeners) {
		listeners = append(listeners, w.listeners[id])
	}
	value := w.stateJSONLocked()
	observers := make([]host.FollowObserver[any], 0, len(w.observers))
	for _, id := range sortedIDs(w.observers) {
		observers = append(observers, w.observers[id])
	}
	w.mu.Unlock()
	for _, event := range events {
		for _, listener := range listeners {
			listener(event)
		}
	}
	for _, observer := range observers {
		observer.Advance(chord.WatchCall[any]{Kind: chord.WatchCallValue, Value: value, Ops: []delta.Op{{"r", value}}}, chordctx.Background())
	}
}

func sortedIDs[V any](m map[int]V) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
