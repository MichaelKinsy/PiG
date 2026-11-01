package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
)

// pig additive (D201): connectionFlushTimeout bounds how long a connection's exit waits for its pending records to reach a peer that may have stopped reading.
const connectionFlushTimeout = 5 * time.Second

// flushConnectionTail waits for a connection's pending records, then gives up: the exit that follows closes the connection, which unblocks a writer stuck on a peer that stopped reading. pig additive (D201).
func flushConnectionTail(tail *stdoutQueue) {
	//portlint:allow ctxbackground the exit path of a connection has no caller context; connectionFlushTimeout bounds the wait
	ctx, cancel := context.WithTimeout(context.Background(), connectionFlushTimeout)
	defer cancel()
	tail.Wait(ctx)
}

// listenControls is the set of live connection sessions' signal controls; the server's termination hooks fan out to it. pig additive (D201).
type listenControls struct {
	mu       sync.Mutex
	next     int
	live     map[int]rpcSessionControls
	stopping bool
	// late joins the shutdowns of sessions that registered after the fan-out began.
	late sync.WaitGroup
}

// add registers a session's controls and returns the unregister. A session that registers after the fan-out began is shut down at once.
func (c *listenControls) add(controls rpcSessionControls) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.next
	c.next++
	if c.live == nil {
		c.live = map[int]rpcSessionControls{}
	}
	c.live[id] = controls
	if c.stopping {
		c.late.Go(func() {
			controls.prelude()
			controls.shutdown(syscall.Signal(receivedTerminationSignal.Load()))
		})
	}
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.live, id)
	}
}

func (c *listenControls) snapshot(stop bool) []rpcSessionControls {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stop {
		c.stopping = true
	}
	out := make([]rpcSessionControls, 0, len(c.live))
	for _, controls := range c.live {
		out = append(out, controls)
	}
	return out
}

// each runs fn over every live session's controls concurrently and waits.
func (c *listenControls) each(stop bool, fn func(rpcSessionControls)) {
	var wg sync.WaitGroup
	for _, controls := range c.snapshot(stop) {
		wg.Go(func() { fn(controls) })
	}
	wg.Wait()
}

// listenHosts is the set of extension Hosts of live connections. pig additive (D201).
type listenHosts struct {
	mu sync.Mutex
	m  map[*subprocess.Host]struct{}
}

// add tracks the build's Host and returns the function that forgets it.
func (h *listenHosts) add(build *cliBuild) (remove func()) {
	if build == nil || build.Host == nil {
		return func() {}
	}
	host := build.Host
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m == nil {
		h.m = map[*subprocess.Host]struct{}{}
	}
	h.m[host] = struct{}{}
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.m, host)
	}
}

func (h *listenHosts) len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.m)
}

// terminate kills the extension processes of every live Host.
func (h *listenHosts) terminate() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for host := range h.m {
		host.TerminateProcesses()
	}
}

// runRPCListen serves one RPC session per connection accepted on the Unix socket at flags.Listen until a termination signal, and returns the process exit code. pig additive (D201).
func runRPCListen(ctx context.Context, flags Args, activePiglet *piglet.Piglet, resources rpcModeResources) (exitCode int) {
	path := flags.Listen
	if _, err := os.Lstat(path); err == nil { //perf:ok cold: one stat per server start
		fmt.Fprintf(os.Stderr, "pig --listen: %s exists\n", path)
		return 1
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Err != nil {
			err = opErr.Err
		}
		fmt.Fprintf(os.Stderr, "pig --listen: listen %s: %v\n", path, err)
		return 1
	}
	defer func() { _ = os.Remove(path) }()
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		fmt.Fprintf(os.Stderr, "pig --listen: chmod %s: %v\n", path, err)
		return 1
	}

	builder := resources.Builder
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	controls := &listenControls{}
	hosts := &listenHosts{}
	// forced carries the termination signal that arrives while the first signal's fan-out is still running.
	forced := make(chan syscall.Signal, 1)
	// The startup build's Host stays tracked until the first connection's session ends, or until exit if none connects.
	hosts.add(resources.Build)

	// Both hooks fan out to every live session at once, and the shutdown one stops accepting first.
	setTerminationPrelude(func() { controls.each(false, func(c rpcSessionControls) { c.prelude() }) })
	setTerminationShutdownHook(func() {
		_ = listener.Close()
		sig := syscall.Signal(receivedTerminationSignal.Load())
		// pig additive (D201): as in process mode, a further termination signal ends the wait for a session whose dispose hangs.
		second := make(chan os.Signal, 1)
		signal.Notify(second, terminationSignals()...)
		defer signal.Stop(second)
		fanout := make(chan struct{})
		go func() {
			defer close(fanout)
			controls.each(true, func(c rpcSessionControls) { c.shutdown(sig) })
		}()
		select {
		case <-fanout:
		case s := <-second:
			forced <- s.(syscall.Signal)
		}
	})
	defer setTerminationPrelude(nil)
	defer setTerminationShutdownHook(nil)

	buildMu := builder.buildMu
	if buildMu == nil {
		buildMu = &sync.Mutex{}
		builder.buildMu = buildMu
	}
	startupBuildTaken := false
	cwd := resources.Build.CWD

	var conns sync.WaitGroup
	var acceptFailed error
	conns.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
					acceptFailed = err
					// pig additive (D201): a failed accept ends the server, and its sessions shut down first.
					fmt.Fprintf(os.Stderr, "pig --listen: accept: %v\n", err)
					controls.each(true, func(c rpcSessionControls) { c.shutdown(syscall.SIGTERM) })
					stop()
				}
				return
			}
			conns.Go(func() {
				defer func() { _ = conn.Close() }()
				buildMu.Lock()
				if ctx.Err() != nil {
					buildMu.Unlock()
					return
				}
				var build *cliBuild
				var buildErr error
				connResources := rpcModeResources{Builder: builder}
				if !startupBuildTaken {
					startupBuildTaken = true
					build = resources.Build
					connResources.SessionManager, connResources.ResumePath = resources.SessionManager, resources.ResumePath
				} else {
					build, buildErr = builder.listenBuild(ctx, cwd)
				}
				buildMu.Unlock()
				if buildErr != nil {
					fmt.Fprintf(os.Stderr, "pig --listen: build: %v\n", buildErr)
					return
				}
				remove := hosts.add(build)
				defer remove()
				if ctx.Err() != nil {
					_ = builder.discardBuild(build, "quit", nil)
					return
				}
				connResources.Build = build
				connResources.Transport = &rpcTransport{
					in: conn, out: conn,
					exit:     func(int) { _ = conn.Close() },
					register: controls.add,
				}
				code := runRPCMode(ctx, flags, activePiglet, connResources)
				slog.Debug("rpc connection ended", "exit", code)
			})
		}
	})

	var forcedSig syscall.Signal
	select {
	case <-ctx.Done():
	case forcedSig = <-forced:
	}
	_ = listener.Close()
	if forcedSig == 0 {
		allDone := make(chan struct{})
		go func() { conns.Wait(); controls.late.Wait(); close(allDone) }()
		select {
		case <-allDone:
		case forcedSig = <-forced:
		}
	}
	if forcedSig != 0 {
		// pig additive (D201): a second termination signal abandons sessions whose dispose hangs.
		if builder.cellRegistry != nil {
			builder.cellRegistry.Close()
		}
		hosts.terminate()
		return 128 + int(forcedSig)
	}
	// Shared cells end with the server, then any process a Host still holds is terminated, as exitRPC does at the end of a process-owned session.
	if builder.cellRegistry != nil {
		builder.cellRegistry.Close()
	}
	hosts.terminate()
	if acceptFailed != nil {
		return 1
	}
	if sig := receivedTerminationSignal.Load(); sig != 0 {
		return 128 + int(sig)
	}
	return 0
}
