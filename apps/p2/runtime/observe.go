package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Ports apps/p2/src/runtime/observe.ts: an observer that loads configuration whenever watched files may have changed.

// retryInvalid is how long a load that reported half-written content waits before it is read once more.
const retryInvalid = 200 * time.Millisecond

// ObservedFiles says where to watch: an environment and the paths in it (computed when watching starts, for example from
// a remote home).
type ObservedFiles struct {
	Env     durableenv.ExecutionEnv
	Targets func(ctx context.Context) ([]durableenv.WatchTarget, error)
}

// ObserverLoad reads the files. It reports errors itself and keeps its last valid state; it returns true to be read once
// more shortly (content that may be half-written, an editor's save caught half-way).
type ObserverLoad func(ctx context.Context, retrying bool) (retry bool)

// Observer loads whenever watched files may have changed. Watching is established before each load starts, so a change
// made during a load is never missed: every change, overflow (lost events, reconnect) or explicit Reload bumps a
// generation, and a load that finishes behind the current generation runs again. While watching is impossible (an error,
// or offline), the fallback interval drives loads and new watch attempts. At most one load runs at a time.
type Observer struct {
	files    []ObservedFiles
	load     ObserverLoad
	fallback time.Duration
	ctx      context.Context
	cancel   context.CancelFunc

	mu         sync.Mutex
	generation int
	loaded     int
	running    bool
	closed     bool
	watchers   []durableenv.FileWatcher
	watching   chan struct{} // closed when the watch attempt in progress ends; nil when none
	fallbackAt *time.Timer
	retryAt    *time.Timer
	idle       []chan struct{}
	loop       sync.WaitGroup
}

// NewObserver is an observer of files that calls load; Start begins watching.
func NewObserver(files []ObservedFiles, load ObserverLoad, fallback time.Duration) *Observer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Observer{files: files, load: load, fallback: fallback, ctx: ctx, cancel: cancel}
}

// Start watches, then loads; it returns after the first load. A cancelled ctx stops the wait, not the observer.
func (o *Observer) Start(ctx context.Context) error {
	o.watch()
	return o.Reload(ctx)
}

// Reload loads again now and returns once loads have caught up with every change so far.
func (o *Observer) Reload(ctx context.Context) error {
	caughtUp := o.reload()
	if caughtUp == nil {
		return nil
	}
	select {
	case <-caughtUp:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// reload bumps the generation and starts the loop; the result closes when loads have caught up (nil: closed).
func (o *Observer) reload() <-chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.generation++
	caughtUp := make(chan struct{})
	o.idle = append(o.idle, caughtUp)
	o.kickLocked(false)
	return caughtUp
}

// Close stops watching and waits for the running load; waiting Reloads return.
func (o *Observer) Close() {
	o.mu.Lock()
	o.closed = true
	o.cancel()
	stopTimer(o.fallbackAt)
	stopTimer(o.retryAt)
	watching := o.watching
	o.mu.Unlock()
	if watching != nil {
		<-watching
	}
	o.mu.Lock()
	watchers := o.watchers
	o.watchers = nil
	o.mu.Unlock()
	stopWatchers(watchers)
	o.loop.Wait()
	o.mu.Lock()
	o.releaseIdleLocked()
	o.mu.Unlock()
}

func stopTimer(timer *time.Timer) {
	if timer != nil {
		timer.Stop()
	}
}

func stopWatchers(watchers []durableenv.FileWatcher) {
	for _, watcher := range watchers {
		_ = watcher.Stop(context.Background())
	}
}

func (o *Observer) releaseIdleLocked() {
	for _, caughtUp := range o.idle {
		close(caughtUp)
	}
	o.idle = nil
}

// watch establishes the watchers (once at a time); a failure schedules the fallback.
func (o *Observer) watch() {
	o.mu.Lock()
	if o.watching != nil {
		watching := o.watching
		o.mu.Unlock()
		<-watching
		return
	}
	done := make(chan struct{})
	o.watching = done
	o.mu.Unlock()
	watchers, err := o.watchAll()
	o.mu.Lock()
	if err == nil && o.closed {
		err = errors.New("closed")
	}
	if err == nil {
		o.watchers = watchers
	}
	o.watching = nil
	o.mu.Unlock()
	if err != nil {
		stopWatchers(watchers)
		o.scheduleFallback()
	}
	close(done)
}

func (o *Observer) watchAll() ([]durableenv.FileWatcher, error) {
	var watchers []durableenv.FileWatcher
	for _, files := range o.files {
		targets, err := files.Targets(o.ctx)
		if err != nil {
			return watchers, err
		}
		watcher, err := files.Env.Watch(o.ctx, targets, o.changed)
		if err != nil {
			return watchers, err
		}
		watchers = append(watchers, watcher)
	}
	return watchers, nil
}

// scheduleFallback loads now and then and tries to watch again, while no watcher runs.
func (o *Observer) scheduleFallback() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	stopTimer(o.fallbackAt)
	o.fallbackAt = time.AfterFunc(o.fallback, func() {
		o.watch()
		o.reload()
	})
}

// changed is the watchers' callback.
func (o *Observer) changed(change durableenv.FileChange) {
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		return
	}
	if _, failed := change.(durableenv.FileChangeFailed); failed {
		// A watcher stopped; the others may still run. Watch everything again later and load meanwhile.
		o.mu.Lock()
		watchers := o.watchers
		o.watchers = nil
		o.mu.Unlock()
		go stopWatchers(watchers)
		o.scheduleFallback()
	}
	o.reload()
}

// kickLocked starts the loop unless it runs.
func (o *Observer) kickLocked(retrying bool) {
	if o.running || o.closed {
		return
	}
	o.running = true
	o.loop.Add(1)
	go o.run(retrying)
}

func (o *Observer) run(again bool) {
	defer o.loop.Done()
	for {
		o.mu.Lock()
		if o.loaded >= o.generation || o.closed {
			o.running = false
			if o.loaded >= o.generation {
				o.releaseIdleLocked()
			}
			o.mu.Unlock()
			return
		}
		generation := o.generation
		o.mu.Unlock()
		retry := o.load(o.ctx, again)
		again = false
		o.mu.Lock()
		o.loaded = generation
		if retry && !o.closed {
			stopTimer(o.retryAt)
			o.retryAt = time.AfterFunc(retryInvalid, func() {
				o.mu.Lock()
				o.generation++
				o.kickLocked(true)
				o.mu.Unlock()
			})
		}
		o.mu.Unlock()
	}
}
