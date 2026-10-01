package main

// Ports packages/coding-agent/src/main.ts createRuntime and createAgentSessionRuntime as every mode uses them.

import (
	"context"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// cliSessionInputs is what one Session of a mode needs from its cwd-bound build: the Services and extension set the Session runs on, the options it starts with, and how to retire the build's resources after the Session is replaced.
type cliSessionInputs struct {
	Services   *coding.Services
	Extensions []extension.Extension
	Start      coding.SessionStartOptions
	// Prepare finishes Start once the extension runner exists, for modes whose prompt or tool set depends on the runner's registrations.
	Prepare func(runner *inproc.Runner, start *coding.SessionStartOptions)
	// Host is the build's extension host, if it has one. The factory tracks the hosts that are alive so a mode can end input on them or kill them at exit.
	Host *subprocess.Host
	// Invalidate makes the build's extension host reject every later call of the replaced Session's extension processes, which outlive the replacement while a command handler runs.
	Invalidate func(message string)
	// Release retires the build's extension host and services. It runs after the replaced Session shut down and its command handlers returned.
	Release func(reason string)
}

// cliRebuild constructs the inputs and mode state of a replacement Session for its destination cwd.
type cliRebuild[S any] func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (cliSessionInputs, S, error)

// cliSessionFactory is Pi's createRuntime closure for one process and mode. The first Session takes the inputs startup already built. Every replacement calls the rebuild function, which recreates the destination cwd's services, resources and extension host, as Pi's factory does.
type cliSessionFactory[S any] struct {
	ctx     context.Context
	rebuild cliRebuild[S]

	mu           sync.Mutex
	hosts        map[*subprocess.Host]struct{}
	inputEnded   bool
	holding      bool
	initial      *cliSessionInputs
	initialState S
	states       map[*coding.Session]S

	retirement cliRetirement
}

func newCLISessionFactory[S any](ctx context.Context, initial cliSessionInputs, initialState S, rebuild cliRebuild[S]) *cliSessionFactory[S] {
	return &cliSessionFactory[S]{
		ctx: ctx, rebuild: rebuild, initial: &initial, initialState: initialState,
		states:     make(map[*coding.Session]S),
		hosts:      make(map[*subprocess.Host]struct{}),
		retirement: newCLIRetirement(ctx),
	}
}

// Factory returns the factory every replacement reuses.
func (f *cliSessionFactory[S]) Factory() coding.CreateAgentSessionRuntimeFactory { return f.create }

// StateFor returns the mode state of a live Session.
func (f *cliSessionFactory[S]) StateFor(session *coding.Session) (S, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.states[session]
	return state, ok
}

func (f *cliSessionFactory[S]) create(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (coding.CreateAgentSessionRuntimeResult, error) {
	f.mu.Lock()
	inputs, state := f.initial, f.initialState
	f.initial = nil
	f.mu.Unlock()
	if inputs == nil {
		if f.rebuild == nil {
			return coding.CreateAgentSessionRuntimeResult{}, errors.New("cli: this mode cannot rebuild a runtime for a replacement Session")
		}
		// The replacement's extension processes and background work live as long as the process, not as long as the call that requested the replacement.
		// pig divergence (D70): the replacement Session's extensions run in processes started for it; Pi runs the factories again in the same Node process.
		rebuilt, rebuiltState, err := f.rebuild(f.ctx, options)
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		inputs, state = &rebuilt, rebuiltState
	}
	if inputs.Host != nil {
		f.mu.Lock()
		f.hosts[inputs.Host] = struct{}{}
		ended := f.inputEnded
		f.mu.Unlock()
		if ended {
			inputs.Host.EndInput()
		}
	}
	release := func(reason string) {
		f.mu.Lock()
		holding := f.holding
		f.mu.Unlock()
		if holding {
			// pig divergence (D70): the process is exiting and a quit session_shutdown may still be pending on this host; Pi's handler stays pending until exit, so the host keeps running and TerminateProcesses stops it.
			return
		}
		if inputs.Release != nil {
			inputs.Release(reason)
		}
		if inputs.Host != nil {
			f.mu.Lock()
			delete(f.hosts, inputs.Host)
			f.mu.Unlock()
		}
	}
	var hostRuntime *extension.ExtensionRuntime
	if inputs.Host != nil {
		hostRuntime = inputs.Host.Runtime()
	}
	low, err := coding.NewRuntime(coding.RuntimeOptions{Services: inputs.Services, NewExtensions: inputs.Extensions, ExtensionRuntime: hostRuntime, AbortContext: f.ctx})
	if err != nil {
		release("construct runtime failed")
		return coding.CreateAgentSessionRuntimeResult{}, err
	}
	start := inputs.Start
	if inputs.Prepare != nil {
		inputs.Prepare(low.NewExtensionRunner(), &start)
	}
	start.SessionManager = options.SessionManager
	start.ResumePath = ""
	session, err := low.New(start)
	if err != nil {
		_ = low.Close()
		release("construct session failed")
		return coding.CreateAgentSessionRuntimeResult{}, err
	}
	f.mu.Lock()
	f.states[session] = state
	f.mu.Unlock()
	return coding.CreateAgentSessionRuntimeResult{
		Session:  session,
		Services: inputs.Services,
		Dispose: func(reason string) {
			_ = low.Close()
			f.mu.Lock()
			delete(f.states, session)
			f.mu.Unlock()
			// pig divergence (D30): the host rejects the replaced Session's later calls with Pi's stale message, but an SDK-local getter or a fire-and-forget call in the extension process does not throw.
			if message := session.ExtensionRunner().StaleMessage(); message != "" && inputs.Invalidate != nil {
				inputs.Invalidate(message)
			}
			f.retirement.retire(session.ExtensionRunner(), func() { release(reason) })
		},
	}, nil
}

// liveHosts returns the extension hosts that have not been retired: the current Session's and those still finishing a command.
func (f *cliSessionFactory[S]) liveHosts() []*subprocess.Host {
	f.mu.Lock()
	defer f.mu.Unlock()
	hosts := make([]*subprocess.Host, 0, len(f.hosts))
	for host := range f.hosts {
		hosts = append(hosts, host)
	}
	return hosts
}

// EndInput tells every live extension host, and each host that a later replacement Session registers, that no further input can arrive.
func (f *cliSessionFactory[S]) EndInput() {
	f.mu.Lock()
	f.inputEnded = true
	f.mu.Unlock()
	for _, host := range f.liveHosts() {
		host.EndInput()
	}
}

// HoldRetirements keeps every host that a later retirement would release running, and skips its Services close, until TerminateProcesses. Call it before the quit session_shutdown is emitted, and only on a path that ends in TerminateProcesses: RPC's non-exit return from the shutdown never reaches a held retirement, because commands are joined before the Runtime closes.
func (f *cliSessionFactory[S]) HoldRetirements() {
	f.mu.Lock()
	f.holding = true
	f.mu.Unlock()
}

// TerminateProcesses kills the extension processes of every live host. Only a process about to exit calls it.
func (f *cliSessionFactory[S]) TerminateProcesses() {
	for _, host := range f.liveHosts() {
		host.TerminateProcesses()
	}
}

// Close ends pending retirements and waits for them. Call it after the Runtime closes.
func (f *cliSessionFactory[S]) Close() { f.retirement.close() }

// cliRetirement retires the resources of replaced Sessions. The host process of a replaced Session stays alive while a command handler of that Session is still running, because a handler that requested the replacement still awaits its result. An owned goroutine waits for the handler; close joins it.
type cliRetirement struct {
	ctx  context.Context
	stop context.CancelFunc
	wg   *sync.WaitGroup
}

func newCLIRetirement(parent context.Context) cliRetirement {
	ctx, stop := context.WithCancel(context.WithoutCancel(parent))
	return cliRetirement{ctx: ctx, stop: stop, wg: new(sync.WaitGroup)}
}

// pig divergence (D70): the replaced Session's extension processes stop here, once their running command handlers return, instead of surviving into the replacement as they do in Pi's single Node process.
func (r cliRetirement) retire(runner *inproc.Runner, release func()) {
	if runner == nil || runner.ActiveCommands() == 0 {
		release()
		return
	}
	r.wg.Go(func() {
		_ = runner.WaitForCommands(r.ctx)

		release()
	})
}

func (r cliRetirement) close() {
	r.stop()
	r.wg.Wait()
}
