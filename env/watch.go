package env

// Ports packages/env/src/watch.ts

import (
	"context"
	"sync"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// RemoteWatchOptions is how the daemon watches, like NodeExecutionEnv's NodeWatchOptions.
type RemoteWatchOptions struct {
	// Mode forces a mode; by default Windows and file systems that do not report remote changes poll.
	Mode durableenv.WatchMode
	// PollIntervalMs is the interval between snapshots in polling mode; nil is the daemon's default 2000 ms.
	PollIntervalMs *int
	// MaxDirectories is the most directories one watcher covers; nil is the daemon's default 10,000. A set value is sent
	// as given, as Pi spreads its options into the watch request.
	MaxDirectories *int
}

const (
	reconnectFirst = 1 * time.Second
	reconnectMax   = 30 * time.Second
)

// RemoteWatcher is a watcher running in the daemon (Durable's NodeFileWatcher next to the files). When the connection
// is lost, it is opened again once the connection can be started again, and reports overflow: changes in between were
// not seen.
type RemoteWatcher struct {
	connection *Connection
	targets    []durableenv.WatchTarget
	onChange   func(durableenv.WatchChange)
	options    RemoteWatchOptions

	mu      sync.Mutex
	mode    durableenv.WatchMode
	closed  bool
	cancel  context.CancelFunc
	request chan struct{}
	wake    chan struct{}
	// reconnecting joins the reconnect loops.
	reconnecting sync.WaitGroup
}

var _ durableenv.FileWatcher = (*RemoteWatcher)(nil)

// stubgen:omit NewRemoteWatcher
// stubgen:omit Open
// OpenRemoteWatcher is upstream's RemoteWatcher.open: the constructor is private upstream, and Go has no static methods.
// It watches targets (resolved paths); it fails like NodeFileWatcher.open if coverage cannot be
// established.
func OpenRemoteWatcher(ctx context.Context, connection *Connection, targets []durableenv.WatchTarget, onChange func(durableenv.WatchChange), options RemoteWatchOptions) (*RemoteWatcher, error) {
	watcher := &RemoteWatcher{connection: connection, targets: targets, onChange: onChange, options: options, mode: durableenv.WatchNative}
	if err := watcher.start(ctx); err != nil {
		return nil, err
	}
	return watcher, nil
}

// Mode is how the watcher learns of changes.
func (w *RemoteWatcher) Mode() durableenv.WatchMode {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.mode
}

// Close stops watching; no onChange call starts after it returns. It is idempotent.
func (w *RemoteWatcher) Close(context.Context) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	wake, cancel, request := w.wake, w.cancel, w.request
	w.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	if cancel != nil {
		cancel()
	}
	if request != nil {
		<-request
	}
	w.reconnecting.Wait()
	return nil
}

func (w *RemoteWatcher) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (w *RemoteWatcher) stop() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

// deliver calls onChange unless closed; a throwing callback must not stop watching.
func (w *RemoteWatcher) deliver(change durableenv.WatchChange) {
	if w.isClosed() {
		return
	}
	defer func() { _ = recover() }()
	w.onChange(change)
}

// start opens the daemon's watcher; it returns once it reports coverage.
func (w *RemoteWatcher) start(ctx context.Context) error {
	targets := make([]any, len(w.targets))
	for index, target := range w.targets {
		exclude := Json{"hidden": false, "names": []any{}}
		if target.Exclude != nil {
			names := make([]any, len(target.Exclude.Names))
			for nameIndex, name := range target.Exclude.Names {
				names[nameIndex] = name
			}
			exclude = Json{"hidden": target.Exclude.Hidden, "names": names}
		}
		targets[index] = Json{"path": target.Path, "recursive": target.Recursive, "exclude": exclude}
	}
	request := Json{"targets": targets}
	if w.options.Mode != "" {
		request["mode"] = string(w.options.Mode)
	}
	if w.options.PollIntervalMs != nil {
		request["pollIntervalMs"] = *w.options.PollIntervalMs
	}
	if w.options.MaxDirectories != nil {
		request["maxDirectories"] = *w.options.MaxDirectories
	}
	requestContext, cancel := context.WithCancel(context.Background())
	stopForwarding := context.AfterFunc(ctx, cancel)
	ready := make(chan struct{})
	var readyOnce sync.Once
	var isReady bool
	var readyMu sync.Mutex
	done := make(chan struct{})
	w.mu.Lock()
	w.cancel = cancel
	w.request = done
	closedMeanwhile := w.closed
	w.mu.Unlock()
	if closedMeanwhile {
		cancel()
	}
	call := w.connection.Start(requestContext, "watch", request, RequestOptions{OnEvent: func(event Json, _ []byte) {
		switch event["kind"] {
		case "ready":
			readyMu.Lock()
			isReady = true
			readyMu.Unlock()
			w.mu.Lock()
			w.mode = durableenv.WatchNative
			if event["mode"] == "polling" {
				w.mode = durableenv.WatchPolling
			}
			w.mu.Unlock()
			readyOnce.Do(func() { close(ready) })
		case "change":
			if event["mode"] == "polling" {
				w.mu.Lock()
				w.mode = durableenv.WatchPolling
				w.mu.Unlock()
			}
			if event["overflow"] == true {
				w.deliver(durableenv.WatchChangeOverflow{})
				return
			}
			var paths []string
			listed, _ := event["paths"].([]any)
			for _, item := range listed {
				if path, ok := item.(string); ok {
					paths = append(paths, path)
				}
			}
			w.deliver(durableenv.WatchChangePaths{Paths: paths})
		case "error":
			code := durableenv.FileErrorInvalid
			if event["code"] == "permission_denied" {
				code = durableenv.FileErrorPermissionDenied
			}
			w.deliver(durableenv.WatchChangeError{Error: durableenv.NewFileError(code, templateString(event, "message"), "", nil)})
			w.stop()
		}
	}})
	failed := make(chan error, 1)
	go func() {
		defer close(done)
		defer stopForwarding()
		_, err := call.Wait()
		if err == nil {
			return
		}
		readyMu.Lock()
		established := isReady
		readyMu.Unlock()
		if !established {
			failed <- err
			return
		}
		if w.isClosed() {
			return
		}
		if IsConnectionLost(err) {
			w.reconnecting.Go(w.reconnect)
			return
		}
		w.deliver(durableenv.WatchChangeError{Error: toFileError(err, "")})
		w.stop()
	}()
	select {
	case <-ready:
		// The context only aborts the opening; the watcher lives until Close, as upstream's open takes no signal.
		stopForwarding()
		return nil
	case err := <-failed:
		cancel()
		return err
	}
}

func (w *RemoteWatcher) reconnect() {
	for delay := reconnectFirst; !w.isClosed(); delay = min(delay*2, reconnectMax) {
		wake := make(chan struct{}, 1)
		w.mu.Lock()
		w.wake = wake
		closed := w.closed
		w.mu.Unlock()
		if closed {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-wake:
			timer.Stop()
		}
		w.mu.Lock()
		w.wake = nil
		w.mu.Unlock()
		if w.isClosed() {
			return
		}
		err := w.start(context.Background())
		if err == nil {
			// Changes while disconnected were not seen.
			w.deliver(durableenv.WatchChangeOverflow{})
			return
		}
		if IsConnectionLost(err) {
			continue
		}
		w.deliver(durableenv.WatchChangeError{Error: toFileError(err, "")})
		w.stop()
		return
	}
}
