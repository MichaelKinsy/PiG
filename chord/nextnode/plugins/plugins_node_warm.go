package plugins

import (
	"context"
	"errors"
	"sync"
)

// No Pi file corresponds to this one: Pi's plugins run in the app's process and have no start to hide. A Node host's start
// (about 0.6 s to `started` before the compile cache, about 0.3 s after, BenchmarkNodeStart) is hidden from `Load` by a spare
// process the runtime starts ahead of it.

// errRuntimeClosed is what a Load or a Warm after PluginRuntime.Close returns.
var errRuntimeClosed = errors.New("plugins: the plugin runtime is closed")

// spare is a process the runtime started ahead of a Load: up to `started`, with Pi's modules imported and nothing loaded. done
// is closed once the start settled, with node or err.
type spare struct {
	done chan struct{}
	node *NodeHost
	err  error
}

// warmState is what a PluginRuntime keeps for its spare process. mu guards spare and closed.
type warmState struct {
	mu     sync.Mutex
	spare  *spare
	closed bool
	// ctx ends the starts the runtime made on its own (KeepWarm) when the runtime closes; tasks joins them.
	ctx    context.Context
	cancel context.CancelFunc
	tasks  sync.WaitGroup
}

func newWarmState() *warmState {
	//portlint:allow ctxbackground the runtime owns the spares it starts on its own (KeepWarm): no caller's context bounds them, and Close cancels and joins them
	ctx, cancel := context.WithCancel(context.Background())
	return &warmState{ctx: ctx, cancel: cancel}
}

// Warm starts a spare process, if the runtime has none, and returns once it is up to `started` with Pi's modules imported
// (NodeOptions.ReadyTimeout bounds the start, ctx ends it). The next Load takes it, so the start is off that Load's critical
// path; a Load that finds the spare still starting waits for it, which is no longer than starting its own. There is at most
// one spare: Warm with one (or one starting) waits for it and returns nil. A failed start returns its error and leaves no
// spare. The spare is the runtime's until a Load takes it: Close closes it. A Warm after Close returns an error.
// pig additive (D223): a spare process started ahead of a Load.
func (r *PluginRuntime) Warm(ctx context.Context) error {
	r.warm.mu.Lock()
	if r.warm.closed {
		r.warm.mu.Unlock()
		return errRuntimeClosed
	}
	if s := r.warm.spare; s != nil {
		r.warm.mu.Unlock()
		select {
		case <-s.done:
			return s.err
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	s := &spare{done: make(chan struct{})}
	r.warm.spare = s
	r.warm.mu.Unlock()

	s.node, s.err = startNode(r.node, ctx)
	if s.err != nil {
		r.warm.mu.Lock()
		if r.warm.spare == s {
			r.warm.spare = nil
		}
		r.warm.mu.Unlock()
	}
	close(s.done)
	return s.err
}

// start is the process a Load runs in: the spare when there is one that is up (waiting for one still starting), else a process
// started now. With NodeOptions.KeepWarm the next spare starts in the background once this call has taken its process. It
// returns errRuntimeClosed after Close. A Load whose ctx ends while it waits for a spare returns the cause and leaves the
// spare for the next Load.
func (r *PluginRuntime) start(ctx context.Context) (*NodeHost, error) {
	r.warm.mu.Lock()
	closed, s := r.warm.closed, r.warm.spare
	r.warm.mu.Unlock()
	if closed {
		return nil, errRuntimeClosed
	}
	if r.node.KeepWarm {
		defer r.keepWarm()
	}
	if s != nil {
		select {
		case <-s.done:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
		if r.claim(s) && s.err == nil {
			if !exited(s.node) {
				return s.node, nil
			}
			s.node.stop()
		}
	}
	return startNode(r.node, ctx)
}

// claim takes s as the process of one Load: it is the runtime's spare, and the runtime is open. The first Load to claim it
// gets it; the others start their own.
func (r *PluginRuntime) claim(s *spare) bool {
	r.warm.mu.Lock()
	defer r.warm.mu.Unlock()
	if r.warm.closed || r.warm.spare != s {
		return false
	}
	r.warm.spare = nil
	return true
}

// exited reports whether the process of node ended.
func exited(node *NodeHost) bool {
	select {
	case <-node.exited:
		return true
	default:
		return false
	}
}

// goTask runs fn as a task Close joins, and reports false, running nothing, once Close has begun.
func (r *PluginRuntime) goTask(fn func()) bool {
	r.warm.mu.Lock()
	defer r.warm.mu.Unlock()
	if r.warm.closed {
		return false
	}
	r.warm.tasks.Go(fn)
	return true
}

// keepWarm starts the next spare as an owned background task of the runtime: Close cancels it and joins it. A start that
// fails is reported to NodeOptions.OnError as a `warm_failed` kind; the next Load starts its own process and reports its own
// failure to its caller.
func (r *PluginRuntime) keepWarm() {
	r.goTask(func() {
		err := r.Warm(r.warm.ctx)
		if err != nil && r.warm.ctx.Err() == nil && !errors.Is(err, errRuntimeClosed) && r.node.OnError != nil {
			r.node.OnError(NodeHostError{Kind: "warm_failed", Err: &NodeError{Name: "Error", Message: err.Error()}})
		}
	})
}

// Close closes the runtime's spare process, and ends and joins the starts it made on its own. The hosts that Loads returned
// are their callers': Close does not close them. It waits for the spare's process to end and returns the error of closing it; when ctx ends first it returns the cause, and
// the process, a start still under way included, is killed as soon as it is up. After Close, Load and Warm return an error. It is safe to call more than once.
func (r *PluginRuntime) Close(ctx context.Context) error {
	r.warm.mu.Lock()
	r.warm.closed = true
	s := r.warm.spare
	r.warm.spare = nil
	r.warm.mu.Unlock()
	r.warm.cancel()
	r.warm.tasks.Wait()
	if s == nil {
		return nil
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		// A Warm the caller started is still starting this process, and no one else owns it: it is stopped as soon as it is up.
		//portlint:allow golifetime ends when the start settles, which NodeOptions.ReadyTimeout and the Warm's own ctx bound
		go func() {
			<-s.done
			if s.err == nil {
				s.node.stop()
			}
		}()
		return context.Cause(ctx)
	}
	if s.err != nil {
		return nil
	}
	return s.node.Close(ctx)
}
