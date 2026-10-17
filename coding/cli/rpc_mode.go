// Headless JSONL command and event loop.
//
// The command set and wire shapes follow the pinned Pi rpc-mode.ts and
// rpc-types.ts. Extension UI dialogs use extension_ui_request and
// extension_ui_response records on the same stream.

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/handleryield"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type rpcTaskGroup struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

func (g *rpcTaskGroup) Go(task func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Go(task)
	return true
}

func (g *rpcTaskGroup) CloseAndWait() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.wg.Wait()
}

// rpcCommandReplacement marks a context that carries a replacement requested by an RPC command.
type rpcCommandReplacement struct{}

// rpcPendingReplacement is one replacement an RPC command requested: release starts it before its input batch drained, settled closes when it finished.
type rpcPendingReplacement struct {
	release chan struct{}
	settled chan struct{}
	// prechecked closes once the replacement's precheck ran or was skipped: Pi checks a fork against the requesting Session while the batch dispatches, before any replacement of the batch starts.
	prechecked chan struct{}
}

// rpcModeResources is the startup runtime main hands to RPC mode, as upstream main.ts passes runRpcMode the runtime whose extensions loaded before model resolution.
type rpcModeResources struct {
	// Builder and Build are the process-fixed inputs and the first Session's cwd-bound build.
	Builder *cliRuntimeBuilder
	Build   *cliBuild
	// SessionManager is the startup Session log, or nil to start a new one.
	SessionManager *coding.SessionManager
	ResumePath     string
}

type headlessCommandRunner interface {
	Commands() []extension.ResolvedCommand
	Command(string) (extension.ResolvedCommand, bool)
	ExecuteCommand(context.Context, string, string) bool
	EmitError(*extension.ExtensionError)
}

// headlessCommandCatalog routes the prompts of print, JSON and RPC mode as
// upstream AgentSession.prompt does: an extension command runs instead of
// prompting, and other text has skill commands and prompt templates expanded.
// It also lists the commands upstream getCommands and get_commands report.
type headlessCommandCatalog struct {
	runner headlessCommandRunner
	// mode is the command context's mode: "print", "json" or "rpc".
	mode            string
	notify          func(message, kind string)
	promptTemplates []codingagent.PromptTemplate
	skills          []*codingagent.SkillDef
	cwd             string
	agentDir        string
	sourceInfo      map[string]codingagent.ResourceSourceInfo
}

// commands lists the catalog as upstream get_commands does.
func (c headlessCommandCatalog) commands() []RPCSlashCommand {
	return c.slashCatalog().Commands()
}

// slashCatalog is the shared getCommands catalog over this command set.
func (c headlessCommandCatalog) slashCatalog() codingagent.SlashCommandCatalog {
	catalog := codingagent.SlashCommandCatalog{
		PromptTemplates: c.promptTemplates, Skills: c.skills,
		CWD: c.cwd, AgentDir: c.agentDir, SourceInfo: c.sourceInfo,
	}
	if c.runner != nil {
		catalog.Runner = c.runner
	}
	return catalog
}

func (c headlessCommandCatalog) extensionCommand(message string) (string, string, bool) {
	if !strings.HasPrefix(message, "/") {
		return "", "", false
	}
	requestedNameAndArgs := message[1:]
	requestedName, args, found := strings.Cut(requestedNameAndArgs, " ")
	if !found {
		args = ""
	}
	if c.runner == nil {
		return "", "", false
	}
	for _, command := range c.runner.Commands() {
		if strings.TrimPrefix(command.InvocationName, "/") == requestedName {
			return command.InvocationName, args, true
		}
	}
	return "", "", false
}

func (c headlessCommandCatalog) expandPrompt(message string) string {
	if expanded, ok, err := codingagent.ExpandSkillCommand(message, c.skills); ok {
		message = expanded
	} else if err != nil && c.runner != nil {
		c.runner.EmitError(err)
	}
	if expanded, ok := codingagent.ExpandPromptTemplate(message, c.promptTemplates); ok {
		message = expanded
	}
	return message
}

// executeCommand runs a command extensionCommand resolved; the built-in
// llama.cpp extension's /llama runs the llama host with this mode's command
// context.
func (c headlessCommandCatalog) executeCommand(ctx context.Context, name, args string) bool {
	return c.runner.ExecuteCommand(ctx, name, args)
}

func (c headlessCommandCatalog) sourceInfoForPath(path, kind string) RPCSourceInfo {
	return c.slashCatalog().SourceInfoForPath(path, kind)
}

func rpcGetCommandsResponse(id rpcRequestID, catalog headlessCommandCatalog) RPCResponse {
	return rpcSuccess(id, "get_commands", RPCGetCommandsData{Commands: catalog.commands()})
}

func rpcExtensionConfigs(configs []subprocess.ExtConfig, cwd, agentDir string, sourceInfo map[string]codingagent.ResourceSourceInfo) []subprocess.ExtConfig {
	out := append([]subprocess.ExtConfig(nil), configs...)
	catalog := headlessCommandCatalog{cwd: cwd, agentDir: agentDir, sourceInfo: sourceInfo}
	for i := range out {
		if out[i].SourceInfo != (extension.SourceInfo{}) {
			// Already stamped where it was collected (a -e extension).
			continue
		}
		path := out[i].Source
		if path == "" {
			path = out[i].Path
		}
		if path == "" {
			path = "builtin:" + out[i].Name
		}
		info := catalog.sourceInfoForPath(path, "extensions")
		if strings.HasPrefix(path, "builtin:") {
			info.Source = "builtin"
			info.BaseDir = ""
		}
		out[i].SourceInfo = info
	}
	return out
}

func rpcResolvedSkills(skills []*codingagent.SkillDef, activePiglet *piglet.Piglet) []*codingagent.SkillDef {
	out := make([]*codingagent.SkillDef, len(skills))
	for i, skill := range skills {
		copy := *skill
		if copy.FilePath == "" && activePiglet != nil {
			// pig additive (D18): the synthesized path identifies the inline skill's manifest, not a file to reload as Markdown.
			copy.SourceInfo.Source = "inline"
			copy.FilePath = activePiglet.SourcePath()
			if copy.FilePath == "" {
				copy.FilePath = "builtin:piglet"
			}
			copy.BaseDir = filepath.Dir(copy.FilePath)
		}
		out[i] = &copy
	}
	return out
}

// runRPCMode owns the `--mode rpc` runtime. EOF and termination triggers dispose and exit the process; startup failures and caller cancellation return to main. Protocol records bypass startup's stdout redirection.
func runRPCMode(ctx context.Context, flags Args, activePiglet *piglet.Piglet, resources rpcModeResources) (exitCode int) {
	defer func() {
		if sig := receivedTerminationSignal.Load(); sig != 0 {
			exitCode = 128 + int(sig)
		}
	}()
	// The process context owns extension processes. Cancelling the mode's own context stops admission, but extensions still receive session_shutdown afterwards.
	processCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var writeMu sync.Mutex
	// shutdownRequested is an extension's ctx.shutdown() (rpc-mode.ts
	// shutdownHandler). Upstream checks it after each handled command and at
	// agent_settled.
	var shutdownRequested atomic.Bool
	// stdoutClosed is set when shutdown exits: upstream's process.exit
	// follows its stdout flush, so the cleanup before the exit writes nothing.
	stdoutClosed := atomic.Bool{}
	// RPC owns raw stdout; incidental startup/extension output follows the process-wide guard to stderr. Records are serialized in the caller and written through output-guard's ordered raw-stdout tail, so no caller blocks on a stopped reader.
	var stdoutFailed atomic.Bool
	rawStdout := newStdoutQueue(codingagent.RawStdoutWriter(), func(error) {
		stdoutFailed.Store(true)
		cancel()
	})
	// The writer goroutine ends once the tail is empty; a termination signal does not wait for it.
	defer rawStdout.Close()
	defer func() {
		// A normal exit flushes the tail; a termination signal exits without waiting on a blocked reader.
		if receivedTerminationSignal.Load() == 0 {
			rawStdout.Wait(context.Background())
		}
		if stdoutFailed.Load() && exitCode == 0 {
			exitCode = 1
		}
	}()
	writeImmediate := func(v any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if stdoutClosed.Load() {
			return
		}
		writeJSONLine(rawStdout, v)
	}
	responses := &rpcResponseTurn{write: writeImmediate}
	writeRPC := func(v any) {
		switch v.(type) {
		case RPCResponse, rpcNullResponse, rpcUnknownCommandResponse:
			responses.complete(v)
		default:
			writeImmediate(v)
		}
	}
	rpcUI := newRPCUIContext(writeRPC)
	defer rpcUI.Close()
	// Upstream shutdown() unsubscribes the stdout forwarder of Session events
	// before it disposes the runtime (rpc-mode.ts shutdown).

	// ── Runtime ────────────────────────────────────────────────────────────
	// The builder is Pi's createRuntime closure: the first Session and every
	// replacement Session are built from the same process-fixed inputs, and
	// every replacement rebuilds the destination cwd's services, resources
	// and extension host.
	builder, initial := resources.Builder, resources.Build
	// RPC refreshes dynamic model catalogs (Radius, llama.cpp) in the background, as
	// upstream main.ts does for RPC mode unless offline (15 s timeout).
	if codingagent.ModelNetworkEnabled() {
		registry := initial.Services.Registry()
		registry.StartModelTask(ctx, func(taskContext context.Context) {
			refreshCtx, cancelRefresh := context.WithTimeout(taskContext, 15*time.Second)
			defer cancelRefresh()
			registry.RefreshCatalogs(refreshCtx, codingagent.CatalogRefreshOptions{AllowNetwork: true})
		})
	}
	flags = initial.Flags

	sessionDir, err := resolveSessionDir(flags.SessionDir, initial.Services.SettingsManager())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: session: %v\n", err)
		return 1
	}
	var promptWG sync.WaitGroup
	var bashWG sync.WaitGroup
	var commandWG sync.WaitGroup
	host := &rpcHost{
		ctx: ctx, cancel: cancel, flags: flags,
		ui: rpcUI, writeRPC: writeRPC, responses: responses,
		promptWG: &promptWG, tasks: &rpcTaskGroup{},
		conversionErr:   make(chan error, 1),
		requestShutdown: func() { shutdownRequested.Store(true) },
		stdoutWait:      func(ctx context.Context) { rawStdout.Wait(ctx) },
	}
	inputs := builder.rpcInputs(processCtx, initial, &rpcStartup{
		Manager: resources.SessionManager, ResumePath: resources.ResumePath, SessionDir: sessionDir,
	})
	factory := newCLISessionFactory(processCtx, inputs, struct{}{}, func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (cliSessionInputs, struct{}, error) {
		build, err := builder.rebuild(ctx, options)
		if err != nil {
			return cliSessionInputs{}, struct{}{}, err
		}
		return builder.rpcInputs(processCtx, build, nil), struct{}{}, nil
	})
	defer factory.Close()
	manager, err := coding.SessionManagerFor(initial.Services, inputs.Start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: session: %v\n", err)
		return 1
	}
	// upstream: main.ts:707-714 names a new Session on its manager before createAgentSessionRuntime records the start model and thinking level (sdk.ts:453-458). The name is initial metadata: no session_info_changed reaches the client.
	if flags.Name != "" {
		if _, err := manager.AppendSessionInfo(flags.Name); err != nil {
			fmt.Fprintf(os.Stderr, "pig --rpc: set session name: %v\n", err)
			return 1
		}
	}
	rt, err := coding.CreateAgentSessionRuntime(ctx, factory.Factory(), coding.CreateAgentSessionRuntimeOptions{
		CWD: initial.CWD, AgentDir: initial.Services.AgentDir(), SessionManager: manager,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: session: %v\n", err)
		return 1
	}
	host.rt = rt
	runRpcMode(withRPCModeOptions(ctx, rpcModeOptions{
		exitCode: &exitCode, host: host, cancel: cancel, shutdownRequested: &shutdownRequested, stdoutClosed: &stdoutClosed,
		rawStdout: rawStdout, responses: responses, writeRPC: writeRPC, writeImmediate: writeImmediate, rpcUI: rpcUI,
		promptWG: &promptWG, bashWG: &bashWG, commandWG: &commandWG,
	}), rt)
	return exitCode
}

// rpcModeOptions are what runRPCMode's startup hands runRpcMode besides the runtime: the host that serves commands and the stdout framing
// the server writes through.
type rpcModeOptions struct {
	// exitCode receives the process exit code the server ends with.
	exitCode          *int
	host              *rpcHost
	cancel            context.CancelFunc
	shutdownRequested *atomic.Bool
	stdoutClosed      *atomic.Bool
	rawStdout         *stdoutQueue
	responses         *rpcResponseTurn
	writeRPC          func(any)
	writeImmediate    func(any)
	rpcUI             *rpcUIContext
	promptWG          *sync.WaitGroup
	bashWG            *sync.WaitGroup
	commandWG         *sync.WaitGroup
}

type rpcModeOptionsKey struct{}

// withRPCModeOptions puts the stdout framing and command host runRpcMode serves through on ctx; Pi keeps them as rpc-mode.ts module state.
func withRPCModeOptions(ctx context.Context, options rpcModeOptions) context.Context {
	return context.WithValue(ctx, rpcModeOptionsKey{}, options)
}

// runRpcMode is rpc-mode.ts runRpcMode(runtimeHost): it serves the RPC commands of the runtime until stdin ends or a shutdown is requested,
// then disposes the runtime. The runtime is built by the caller, as Pi's createRuntime closure builds it for main; the stdout framing and
// command host come from ctx ([withRPCModeOptions]), where Pi reads module state.
// Pi's promise never resolves, because shutdown ends the process with process.exit(code); the code lands in the options' exit slot for
// the process-exiting caller.
func runRpcMode(ctx context.Context, rt *coding.Runtime) {
	options, ok := ctx.Value(rpcModeOptionsKey{}).(rpcModeOptions)
	if !ok {
		fmt.Fprintln(os.Stderr, "pig --rpc: no RPC mode state on the context")
		return
	}
	*options.exitCode = serveRPCMode(ctx, rt, options)
}

// serveRPCMode serves the commands of rt and returns the process exit code.
func serveRPCMode(ctx context.Context, rt *coding.Runtime, options rpcModeOptions) (exitCode int) {
	host, cancel := options.host, options.cancel
	shutdownRequested, stdoutClosed, rawStdout := options.shutdownRequested, options.stdoutClosed, options.rawStdout
	responses, writeRPC, writeImmediate, rpcUI := options.responses, options.writeRPC, options.writeImmediate, options.rpcUI
	promptWG, bashWG, commandWG := options.promptWG, options.bashWG, options.commandWG
	// Runtime.Close closes the current Session and retires its host. session_shutdown for quit has been emitted by then, by the shutdown below or by Close itself.
	defer func() { _ = rt.Close() }()
	sess := func() *coding.Session { return rt.Session() }
	services := func() *coding.AgentSessionServices { return rt.Services() }
	// setModelResolved is the end of the chain of set_model auth checks: the next set_model resolves after it.
	initialResolved := make(chan struct{})
	close(initialResolved)
	var setModelResolved <-chan struct{} = initialResolved
	// pig divergence (D147): the compactions of one input batch run one after another.
	var compactResolved <-chan struct{} = initialResolved
	runner := func() *inproc.Runner { return rt.Session().ExtensionRunner() }
	commandCatalog := func() headlessCommandCatalog { return host.admission.Load().catalog }

	// Upstream shutdown() runs once, on stdin end, ctx.shutdown(), or a
	// termination signal (rpc-mode.ts:728-744). It removes the signal
	// handlers, stops writing Session events, and awaits runtimeHost.dispose():
	// session_shutdown, then AgentSession.dispose, while the Session still runs.
	// It then flushes stdout, unless the signal is SIGTERM, and exits without
	// joining the remaining work: dialogs stdin can no longer answer stay
	// pending, and their commands never respond.
	// The extension host of any Session reaches the process controls of every host the process started.
	terminateProcesses := func() {
		if extensionHost, _ := rpcExtensionHost(sess()); extensionHost != nil {
			extensionHost.TerminateProcesses()
		}
	}
	exitRPC := func(code int) {
		stdoutClosed.Store(true)
		if code != 128+int(syscall.SIGTERM) {
			// Upstream flushes stdout before process.exit unless the signal is SIGTERM; the queued tail is that pending output.
			rawStdout.Wait(context.Background())
		}
		terminateProcesses()
		// PIG_PROFILE files are written here because os.Exit skips main's deferred stop; unset, this is a no-op.
		stopProfiles()
		// Upstream process.exit does not wait for providers or suspended extension callbacks.
		os.Exit(code)
	}
	forceSignal := func(sig os.Signal) {
		terminateProcesses()
		dieBySignal(sig)
	}
	var forced atomic.Pointer[forcedTermination]
	defer func() { forced.Load().Stop() }()
	var shutdown *rpcShutdown
	shutdownInvoked := make(chan struct{})
	// A termination signal holds the run from the moment it arrives, before anything is killed: Pi's handler kills
	// tracked children and awaits the disposal on its one event loop (rpc-mode.ts:366-376,726-744), so a run in flight
	// goes on only once a session_shutdown handler awaits a timer or I/O, or after the Session is disposed.
	var holdMu sync.Mutex
	releaseHold := func() {}
	setTerminationPrelude(func() {
		release := sess().HoldAgentEvents()
		holdMu.Lock()
		releaseHold = release
		holdMu.Unlock()
	})
	defer setTerminationPrelude(nil)
	release := func() {
		holdMu.Lock()
		fn := releaseHold
		holdMu.Unlock()
		fn()
	}
	//portlint:allow ctxmapping Pi's shutdown awaits runtimeHost.dispose() with no signal (rpc-mode.ts:726-744), and it runs after the mode's ctx ends, so it cannot derive from it
	shutdownContext := handleryield.With(invocation.WithAcknowledgment(context.Background(), func() { close(shutdownInvoked) }), release)
	shutdown = newRPCShutdown(
		func() { forced.Store(watchForcedTermination(shutdown.forceDie)) },
		func() {
			// Upstream writes each Session event when it is published, so the
			// events already published reach stdout before it unsubscribes.
			if host.forwarding.Load() > 0 {
				_ = sess().FlushEvents(context.Background())
			}
			host.detached.Store(true)
		},
		func() {
			if extensionHost, _ := rpcExtensionHost(sess()); extensionHost != nil {
				extensionHost.HoldRetirements()
			}
			rt.EmitQuitShutdownContext(shutdownContext)
			invocation.Acknowledge(shutdownContext)
			sess().Dispose()
			release()
		},
		func() {
			select {
			case <-rpcUI.PendingRequest():
				return
			case <-responses.idle():
			}
			if commandJoin := host.commandJoin.Load(); commandJoin != nil {
				// pig additive (D19): the host applies Pi's stdin-end window to runtimes that report none; a Node runtime reports each command's own window.
				if extensionHost, bridge := rpcExtensionHost(sess()); bridge != nil {
					extensionHost.FlushCommands()
				}
				select {
				case <-commandJoin.idle():
					commandJoin.close()
				case <-rpcUI.PendingRequest():
					return
				}
			}
			// flushRawStdout lets the aborted run's in-process work (its
			// turn_end boundaries, agent_settled and the errors they report)
			// finish before the process exits. Work waiting on a dialog never
			// finishes there.
			settleCtx, stopSettle := context.WithCancel(context.Background())
			settleWatcherDone := make(chan struct{})
			defer func() { stopSettle(); <-settleWatcherDone }()
			go func() {
				defer close(settleWatcherDone)
				select {
				case <-rpcUI.PendingRequest():
					stopSettle()
				case <-settleCtx.Done():
				}
			}()
			_ = sess().WaitForIdle(settleCtx)
		},
		exitRPC,
		forceSignal,
	)
	host.drain = shutdown.drained
	// pig additive (D19): the host waits in-process for the commands Pi answers before it reads stdin's end, because their runtimes are other processes whose frames race the shutdown's own writes.
	shutdown.settle = func() bool {
		// Pi reads stdin's end in an event-loop iteration of its own: after the settle tail that a published agent_end started, and after the response of every command that settles in the microtasks and check phase of its line's iteration (rpc-mode.ts:355-360,802-805).
		host.settle.wait(ctx, rpcUI.PendingRequest, shutdown.Started())
		if commandJoin := host.commandJoin.Load(); commandJoin != nil {
			select {
			case <-commandJoin.windowIdle():
			case <-rpcUI.PendingRequest():
			case <-shutdown.Started():
			case <-ctx.Done():
			}
		}
		return ctx.Err() == nil
	}
	defer shutdown.finish()
	// checkRequestedShutdown is rpc-mode.ts checkShutdownRequested. It runs on
	// the command loop and the event forwarder, so shutdown runs apart from
	// them.
	var shutdownTasks sync.WaitGroup
	defer shutdownTasks.Wait()
	scheduleRequestedShutdown := func() bool {
		if !shutdownRequested.Load() {
			return false
		}
		shutdownTasks.Go(shutdown.requested)
		return true
	}
	checkRequestedShutdown := func() {
		if scheduleRequestedShutdown() {
			<-shutdownInvoked
		}
	}
	host.afterSettled = func() { scheduleRequestedShutdown() }
	host.commandDone = checkRequestedShutdown
	setTerminationShutdownHook(func() { shutdown.signal(syscall.Signal(receivedTerminationSignal.Load())) })
	defer setTerminationShutdownHook(nil)

	rt.SetRebindSession(func(ctx context.Context, session *coding.Session) error {
		return host.bind(ctx, session, session.StartEvent(), false)
	})
	// rpc-mode.ts:341-343: ctx.reload() runs session.reload() and then rebinds the Session's extensions.
	rt.SetReload(func(ctx context.Context, session *coding.Session) error {
		previous := session.ExtensionRunner()
		if err := session.Reload(ctx); err != nil {
			// The old runner went stale after session_shutdown; its extension processes reject later calls with its message too.
			if extensionHost := session.ExtensionHost(); extensionHost != nil && previous != nil && session.ExtensionRunner() == previous {
				extensionHost.Invalidate(previous.StaleMessage())
			}
			return err
		}
		return host.bind(ctx, session, extension.SessionStartEvent{Type: codingagent.EventSessionStart, Reason: "reload"}, true)
	})
	first := rt.Session()
	if err := host.bind(ctx, first, first.StartEvent(), false); err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: %v\n", err)
		return 1
	}

	// Preflight is not an agent run. Admission marks the Session streaming only after before_agent_start and image normalization finish.
	streaming := func() bool { return sess().IsStreaming() }

	handleCycleModel := func(id rpcRequestID, currentSession *coding.Session, turn *rpcResponseTurn) {
		result, complete, err := currentSession.BeginModelCycle(ctx, "forward")
		if err != nil {
			turn.complete(rpcError(id, "cycle_model", err.Error()))
			return
		}
		if result == nil {
			turn.after(func() { writeRPC(rpcSuccessNull(id, "cycle_model")) })
			return
		}
		if err := currentSession.FlushEvents(ctx); err != nil {
			turn.complete(rpcError(id, "cycle_model", err.Error()))
			return
		}
		publish := func() {
			writeRPC(rpcSuccess(id, "cycle_model", RPCModelCycleResult{
				Model: rpcModelValue(result.Model), ThinkingLevel: currentSession.ThinkingLevel(), IsScoped: result.IsScoped,
			}))
		}
		if complete == nil {
			turn.after(publish)
		} else {
			commandWG.Go(func() { complete(); turn.after(publish) })
		}
	}

	var inputTurn *rpcResponseTurn
	waitPrompt := func() {
		promptWG.Wait()
		_ = sess().WaitForIdle(context.Background())
		_ = sess().FlushEvents(ctx)
	}
	// settlePrompt is upstream `await session.abort()`: release the admitted input turn while joining work so queued Session reactions can complete before the response.
	settlePrompt := func() {
		sess().RequestAbort()
		currentTurn := inputTurn
		if currentTurn != nil {
			inputTurn = nil
			currentTurn.end()
			defer func() {
				currentTurn.begin()
				inputTurn = currentTurn
			}()
		}
		waitPrompt()
	}
	settleSessionWork := func() {
		commandWG.Wait()
		settlePrompt()
		sess().AbortBash()
		bashWG.Wait()
	}
	// compactionCancels holds the cancel function of every compact command that has not answered yet, by command sequence number.
	var compactionMu sync.Mutex
	var compactionSeq uint64
	// pig divergence (D148): Pi answers the first of two compacts that a replacement aborts with a TypeError from its shared abort controller; PiG answers each compact with the abort error, in request order.
	compactionCancels := map[uint64]context.CancelFunc{}
	// headCompactionStarts holds, for each compact command that did not wait for an earlier one, a channel closed once its compaction_start event was emitted or it ended without one. Guarded by compactionMu.
	var headCompactionStarts []chan struct{}
	cancelCompactions := func() {
		compactionMu.Lock()
		defer compactionMu.Unlock()
		for _, cancel := range compactionCancels {
			cancel()
		}
	}
	// settleReplacedWork joins the same work for a replacement that runs awaited off the input loop, which owns the admitted input turn.
	settleReplacedWork := func() {
		// Pi's teardown disposes the outgoing Session, which aborts its compaction (agent-session.ts:1393-1399); it does not wait for it. A compaction that has not claimed the Session yet is cancelled through its context.
		cancelCompactions()
		commandWG.Wait()
		waitAfterAbort := func() {
			sess().RequestAbort()
			waitPrompt()
		}
		waitAfterAbort()
		sess().AbortBash()
		bashWG.Wait()
	}
	// A replacement that an RPC command requests drains the mode's own work once the before hooks approved it. A replacement that an extension requests does not: that extension's handler is itself part of the work.
	rt.SetBeforeSessionReplacement(func(ctx context.Context) error {
		if ctx.Value(rpcCommandReplacement{}) != nil {
			settleReplacedWork()
		}
		return nil
	})
	replacementCtx := context.WithValue(ctx, rpcCommandReplacement{}, true)
	// awaitReplacement is upstream `await runtime.newSession()` and its siblings (rpc-mode.ts:808-810 dispatches every line at once): the replacement starts after the input batch that requested it, so a later command of that batch answers first from the old Session, and the response is written when the replacement and its second rebind finish.
	// pig divergence (D146): Pi exits at stdin end while a replacement still awaits createRuntime and never answers it. Stdin end here waits for each replacement and writes its response.
	var replacementWG sync.WaitGroup
	var replacementMu sync.Mutex
	var startedReplacements []*rpcPendingReplacement
	// pendingSetModels holds one channel per set_model whose auth check or apply has not finished; a replacement of the batch starts after them, because Pi's `await checkAuth` resumes in the microtasks of its batch while the replacement starts after it.
	var pendingSetModels []chan struct{}
	// Replacements chain like set_model's checks (rpcAwaitInOrder): each starts after the previous one applied and answers after it, so the last one requested decides the final Session. Pi 1.1.0 answers switch_session, new_session, new_session, get_state as 4,1,2,3.
	initialReplacementResolved := make(chan struct{})
	close(initialReplacementResolved)
	var replacementResolved <-chan struct{} = initialReplacementResolved
	// precheck, when set, is the check Pi's fork makes against the Session that was current when it was requested. It answers while an earlier replacement of the batch still builds its Session, and only then.
	// awaitHeadCompactionStarts returns once every compaction that began without waiting for another one emitted compaction_start, so a replacement's error follows it as in Pi.
	awaitHeadCompactionStarts := func() {
		compactionMu.Lock()
		starting := slices.Clone(headCompactionStarts)
		compactionMu.Unlock()
		for _, started := range starting {
			select {
			case <-started:
			case <-ctx.Done():
			}
		}
	}
	awaitReplacement := func(admission *rpcAdmission, turn *rpcResponseTurn, id rpcRequestID, command string, precheck func() error, replace func(context.Context) (any, bool, error)) {
		pending := &rpcPendingReplacement{release: make(chan struct{}), settled: make(chan struct{}), prechecked: make(chan struct{})}
		replacementMu.Lock()
		startedReplacements = slices.DeleteFunc(startedReplacements, func(earlier *rpcPendingReplacement) bool {
			select {
			case <-earlier.settled:
				return true
			default:
				return false
			}
		})
		startedReplacements = append(startedReplacements, pending)
		replacementMu.Unlock()
		batchDrained := turn.idle()
		replacementWG.Add(1)
		previous := replacementResolved
		awaitPrechecks := func() {
			replacementMu.Lock()
			waiting := slices.Clone(startedReplacements)
			replacementMu.Unlock()
			for _, other := range waiting {
				select {
				case <-other.prechecked:
				case <-ctx.Done():
					return
				}
			}
		}
		awaitSetModels := func() {
			replacementMu.Lock()
			pendingSetModels = slices.DeleteFunc(pendingSetModels, func(done chan struct{}) bool {
				select {
				case <-done:
					return true
				default:
					return false
				}
			})
			waiting := slices.Clone(pendingSetModels)
			replacementMu.Unlock()
			for _, done := range waiting {
				select {
				case <-done:
				case <-ctx.Done():
				}
			}
		}
		var applied *rpcPromise[any]
		applied, replacementResolved = rpcAwaitInOrder(admission, previous, func() (any, error) {
			defer close(pending.settled)
			var markPrechecked sync.Once
			defer markPrechecked.Do(func() { close(pending.prechecked) })
			select {
			case <-batchDrained:
			case <-pending.release:
			case <-ctx.Done():
			}
			awaitSetModels()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if precheck != nil {
				select {
				case <-previous:
				default:
					if err := precheck(); err != nil {
						awaitHeadCompactionStarts()
						return nil, rpcEarlyError{err}
					}
				}
			}
			markPrechecked.Do(func() { close(pending.prechecked) })
			awaitPrechecks()
			select {
			case <-previous:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			data, cancelled, err := replace(replacementCtx)
			if err != nil {
				awaitHeadCompactionStarts()
			}
			if err == nil && !cancelled {
				err = host.rebindAgain(ctx)
			}
			return data, err
		})
		applied.then(func(data any, err error) {
			defer replacementWG.Done()
			if err != nil {
				writeRPC(rpcError(id, command, err.Error()))
				return
			}
			writeRPC(rpcSuccess(id, command, data))
		})
	}
	// awaitReplacementDecisions returns once batchDispatched is closed and every replacement requested by then has settled, or once ctx is done: the teardown of an approved replacement cancels ctx through cancelCompactions.
	awaitReplacementDecisions := func(batchDispatched <-chan struct{}, ctx context.Context) {
		select {
		case <-batchDispatched:
		case <-ctx.Done():
			return
		}
		replacementMu.Lock()
		pending := slices.Clone(startedReplacements)
		replacementMu.Unlock()
		for _, replacement := range pending {
			select {
			case <-replacement.settled:
			case <-ctx.Done():
				return
			}
		}
	}
	// finishReplacements runs every requested replacement to completion before the command loop reads further. An extension command of the same batch runs on the replacement Session: Pi reaches the replacement's session_shutdown before such a command's handler (comb-audit row 58, rpc_shutdown_replacement_test.go).
	finishReplacements := func() {
		replacementMu.Lock()
		waiting := startedReplacements
		startedReplacements = nil
		replacementMu.Unlock()
		for _, pending := range waiting {
			close(pending.release)
			select {
			case <-pending.settled:
			case <-rpcUI.PendingRequest():
			case <-ctx.Done():
			}
		}
	}

	// ── Command loop ───────────────────────────────────────────────────────

	inputLines := make(chan [][]byte, 64)
	inputDone := make(chan error, 1)
	// loopIdle is closed when the command loop has dispatched every line read
	// before stdin ended.
	loopIdle := make(chan struct{})
	go func() {
		err := rpcclient.ReadJSONLBatches(os.Stdin, func(lines [][]byte) bool {
			select {
			case inputLines <- lines:
				return true
			case <-ctx.Done():
				return false
			}
		})
		close(inputLines)
		if extensionHost, _ := rpcExtensionHost(sess()); extensionHost != nil {
			extensionHost.EndInput()
		}
		if ctx.Err() != nil {
			inputDone <- nil
			return
		}
		// Upstream stdin end runs shutdown() once every line was dispatched,
		// while a command may still wait on a dialog stdin can no longer
		// answer.
		select {
		case <-loopIdle:
		case <-rpcUI.PendingRequest():
		case <-shutdown.Started():
		case <-ctx.Done():
		}
		// pig divergence (D146): a replacement requested before stdin ended finishes and answers first.
		replacementsDone := make(chan struct{})
		go func() { replacementWG.Wait(); close(replacementsDone) }()
		select {
		case <-replacementsDone:
		case <-rpcUI.PendingRequest():
		case <-shutdown.Started():
		case <-ctx.Done():
		}
		if ctx.Err() == nil {
			shutdown.inputEnd()
		}
		inputDone <- err
	}()

	var inputBatch [][]byte
	commandChecked := true
commandLoop:
	for {
		if len(inputBatch) == 0 && inputTurn != nil {
			inputTurn.end()
			inputTurn = nil
		}
		// Upstream checks for ctx.shutdown() once after each handled command.
		if !commandChecked {
			checkRequestedShutdown()
			commandChecked = true
		}
		if ctx.Err() != nil {
			break commandLoop
		}
		if len(inputBatch) == 0 {
			select {
			case <-ctx.Done():
				break commandLoop
			case next, ok := <-inputLines:
				if !ok {
					close(loopIdle)
					break commandLoop
				}
				inputBatch = next
			}
			// Pi reads a stdin chunk in an iteration of its own, after any microtask tail that agent_end started.
			host.settle.wait(ctx, rpcUI.PendingRequest, shutdown.Started())
			responses.begin()
			inputTurn = responses
		}
		line := inputBatch[0]
		inputBatch = inputBatch[1:]
		turn := inputTurn
		// A replacement installs a new admission for its Session; commands read the current one.
		admission := host.admission.Load()
		env, parseErr := parseRPCCommand(line)
		if parseErr != nil {
			// Upstream uses rpcError(undefined, "parse", message): mirrors
			// rpc-mode.ts:handleInputLine catch block. Translate Go json
			// error wording to upstream's JS SyntaxError wording so the wire
			// shape matches byte-for-byte across binaries.
			writeImmediate(rpcParseError(line, parseErr))
			continue
		}

		if env.Type == "extension_ui_response" {
			rpcUI.HandleResponse(env.Raw)
			continue
		}

		commandChecked = false
		var adaptMessage string
		var adaptFailed bool
		if env, adaptMessage, adaptFailed = rpcJSAdapt(env); adaptFailed {
			writeRPC(rpcError(env.ID, string(env.Type), adaptMessage))
			continue
		}
		switch env.Type {
		// ── prompt ──────────────────────────────────────────────────────
		case RPCCommandPrompt:
			cmd, err := decodeRPCCommand[RPCPromptCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "prompt", err.Error()))
				continue
			}
			if name, args, ok := commandCatalog().extensionCommand(cmd.Message); ok {
				finishReplacements()
				admission = host.admission.Load()
				admission.command(env.ID, name, args)
				commandChecked = true
				continue
			}
			admission.prompt(env.ID, cmd)

		// ── abort ────────────────────────────────────────────────────────
		case RPCCommandAbort:
			// upstream: agent-session.ts abort ends with waitForIdle, which returns at once when no agent run or compaction is active at that point; it does not join a run that a later command in the batch starts.
			idle := sess().IsIdle()
			sess().RequestAbort()
			rpcAwaitWork(admission, !idle, func() (struct{}, error) {
				if !idle {
					waitPrompt()
				}
				return struct{}{}, nil
			}).then(func(_ struct{}, err error) {
				if err != nil {
					writeRPC(rpcError(env.ID, "abort", err.Error()))
				} else {
					writeRPC(rpcSuccess(env.ID, "abort", nil))
				}
			})

		case RPCCommandClearQueue:
			steering, followUp := sess().ClearQueue()
			// Flush the queue_update this emits through the wire before the
			// response, as Pi's clearQueue() emits it synchronously before
			// rpc-mode's success() call runs (steer/follow_up flush the same
			// way for the same reason).
			if err := sess().FlushEvents(ctx); err != nil {
				writeRPC(rpcError(env.ID, "clear_queue", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "clear_queue", RPCClearQueueData{Steering: steering, FollowUp: followUp}))

		case RPCCommandNewSession:
			cmd, err := decodeRPCCommand[RPCNewSessionCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "new_session", err.Error()))
				continue
			}
			var options *extension.NewSessionOptions
			if cmd.ParentSession != "" {
				options = &extension.NewSessionOptions{ParentSession: cmd.ParentSession}
			}
			awaitReplacement(admission, turn, env.ID, "new_session", nil, func(ctx context.Context) (any, bool, error) {
				result, err := rt.NewSession(ctx, options)
				return RPCCancelledResult{Cancelled: result.Cancelled}, result.Cancelled, err
			})

		// ── get_extensions ─────────────────────────────────────────────
		// pig additive (D101): Pi lists loaded extensions only in-process, through session.extensionRunner.getExtensionPaths() (runner.ts:625).
		case "get_extensions":
			paths := []string{}
			if r := runner(); r != nil {
				paths = r.ExtensionPaths()
			}
			writeRPC(rpcSuccess(env.ID, "get_extensions", RPCGetExtensionsData{Paths: paths}))

		// ── get_commands ───────────────────────────────────────────────
		case RPCCommandGetCommands:
			writeRPC(rpcGetCommandsResponse(env.ID, commandCatalog()))

		// ── get_state ────────────────────────────────────────────────────
		case RPCCommandGetState:
			state := RPCSessionState{
				Model:                 rpcModelValue(sess().Model()),
				ThinkingLevel:         sess().ThinkingLevel(),
				IsStreaming:           streaming(),
				IsCompacting:          sess().IsCompacting(),
				SteeringMode:          string(sess().SteeringMode()),
				FollowUpMode:          string(sess().FollowUpMode()),
				SessionFile:           sess().Path(),
				SessionID:             sess().ID(),
				SessionName:           sess().SessionName(),
				AutoCompactionEnabled: sess().AutoCompactionEnabled(),
				MessageCount:          len(sess().Messages()),
				PendingMessageCount:   sess().PendingMessageCount(),
			}
			writeRPC(rpcSuccess(env.ID, "get_state", state))

		// ── set_model ────────────────────────────────────────────────────
		case RPCCommandSetModel:
			cmd, err := decodeRPCCommand[RPCSetModelCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "set_model", err.Error()))
				continue
			}
			models := rpcAvailableModels(services(), sess().Model())
			var newModel *ai.Model
			for _, candidate := range models {
				if candidate.ProviderMeta.ProviderID == cmd.Provider && candidate.ID == cmd.ModelID {
					newModel = candidate
					break
				}
			}
			if newModel == nil {
				writeRPC(rpcError(env.ID, "set_model", fmt.Sprintf("Model not found: %s/%s", cmd.Provider, cmd.ModelID)))
				continue
			}
			// Port of rpc-mode.ts:470-478: `await session.setModel(model)` suspends on `await modelRuntime.checkAuth(provider)` (agent-session.ts:2477-2480) before it applies the model, so commands in the same input batch run first.
			// The suspended set_models resume in the order they arrived, as Pi's continuations do, so the last one applied is the last one sent.
			currentSession, commandID := sess(), env.ID
			var checked *rpcPromise[struct{}]
			checked, setModelResolved = rpcAwaitInOrder(admission, setModelResolved, func() (struct{}, error) {
				return struct{}{}, currentSession.CheckModelAuth(newModel)
			})
			applied := make(chan struct{})
			replacementMu.Lock()
			pendingSetModels = append(pendingSetModels, applied)
			replacementMu.Unlock()
			checked.then(func(_ struct{}, err error) {
				if err != nil {
					writeRPC(rpcError(commandID, "set_model", err.Error()))
					close(applied)
					return
				}
				complete, err := currentSession.BeginModelChange(ctx, newModel, extension.ModelSelectSourceUser)
				if err == nil {
					err = currentSession.FlushEvents(ctx)
				}
				if err != nil {
					writeRPC(rpcError(commandID, "set_model", err.Error()))
					close(applied)
					return
				}
				publish := func() { writeRPC(rpcSuccess(commandID, "set_model", rpcModelValue(newModel))) }
				if complete == nil {
					publish()
					close(applied)
					return
				}
				commandWG.Go(func() { complete(); close(applied); turn.after(publish) })
			})

		case RPCCommandCycleModel:
			handleCycleModel(env.ID, sess(), turn)

		// ── compact ─────────────────────────────────────────────────────
		case RPCCommandCompact:
			cmd, err := decodeRPCCommand[RPCCompactCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "compact", err.Error()))
				continue
			}
			commandID := env.ID
			customInstructions := cmd.CustomInstructions
			currentSession := sess()
			batchDispatched := turn.idle()
			// Pi's compact() awaits abort() before it creates its abort controller, so a compact of the same batch aborts nothing of an earlier one. Compactions chain: each starts after the previous one settled.
			commandWG.Add(1)
			previous := compactResolved
			compactCtx, cancelCompact := context.WithCancel(ctx)
			started := make(chan struct{})
			var markStarted sync.Once
			compactionMu.Lock()
			compactionSeq++
			compactKey := compactionSeq
			compactionCancels[compactKey] = cancelCompact
			select {
			case <-previous:
				headCompactionStarts = slices.DeleteFunc(headCompactionStarts, func(earlier chan struct{}) bool {
					select {
					case <-earlier:
						return true
					default:
						return false
					}
				})
				headCompactionStarts = append(headCompactionStarts, started)
			default:
			}
			compactionMu.Unlock()
			var compacted *rpcPromise[any]
			compacted, compactResolved = rpcAwaitInOrder(admission, previous, func() (any, error) {
				defer markStarted.Do(func() { close(started) })
				defer func() {
					compactionMu.Lock()
					delete(compactionCancels, compactKey)
					compactionMu.Unlock()
					cancelCompact()
				}()
				select {
				case <-previous:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				// The input loop owns its response turn; this worker only aborts and joins Session work.
				currentSession.RequestAbort()
				waitPrompt()
				// Pi's compaction first awaits I/O after compaction_start (session_before_compact or getAuth), while a replacement reaches its teardown without any, so a replacement requested in the same batch, or still pending from an earlier one, aborts the compaction there. The compaction waits for each such replacement to tear down or to give up.
				yield := func(compaction context.Context) {
					markStarted.Do(func() { close(started) })
					awaitReplacementDecisions(batchDispatched, compaction)
				}
				result, err := currentSession.Compact(coding.WithCompactionYield(compactCtx, yield), customInstructions)
				if flushErr := currentSession.FlushEvents(ctx); err == nil {
					err = flushErr
				}
				if err != nil {
					return nil, err
				}
				return rpcCompactionResult(result), nil
			})
			compacted.then(func(data any, err error) {
				defer commandWG.Done()
				if err != nil {
					writeRPC(rpcError(commandID, "compact", err.Error()))
					return
				}
				writeRPC(rpcSuccess(commandID, "compact", data))
			})

		// ── set_auto_compaction ──────────────────────────────────────────
		case RPCCommandSetAutoCompaction:
			cmd, err := decodeRPCCommand[RPCSetAutoCompactionCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "set_auto_compaction", err.Error()))
				continue
			}
			sess().SetAutoCompactionEnabled(cmd.Enabled)
			writeRPC(rpcSuccess(env.ID, "set_auto_compaction", nil))

		// ── get_available_models ─────────────────────────────────────────
		case RPCCommandGetAvailableModels:
			models := rpcAvailableModels(services(), sess().Model())
			writeRPC(rpcSuccess(env.ID, "get_available_models", map[string]any{
				"models": rpcModelList(models),
			}))

		// ── set_thinking_level ─────────────────────────────────────────
		case RPCCommandSetThinkingLevel:
			cmd, err := decodeRPCCommand[RPCSetThinkingLevelCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "set_thinking_level", err.Error()))
				continue
			}
			if err := sess().SetThinkingLevel(ai.ModelThinkingLevel(cmd.Level)); err != nil {
				writeRPC(rpcError(env.ID, "set_thinking_level", err.Error()))
				continue
			}
			if err := sess().FlushEvents(ctx); err != nil {
				writeRPC(rpcError(env.ID, "set_thinking_level", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_thinking_level", nil))

		// ── cycle_thinking_level ─────────────────────────────────────────────
		case RPCCommandCycleThinkingLevel:
			levels := sess().AvailableThinkingLevels()
			if len(levels) <= 1 {
				writeRPC(rpcSuccessNull(env.ID, "cycle_thinking_level"))
				continue
			}
			current := sess().ThinkingLevel()
			currentIndex := max(slices.Index(levels, current), 0)
			nextLevel := levels[(currentIndex+1)%len(levels)]
			if err := sess().SetThinkingLevel(nextLevel); err != nil {
				writeRPC(rpcError(env.ID, "cycle_thinking_level", err.Error()))
				continue
			}
			if err := sess().FlushEvents(ctx); err != nil {
				writeRPC(rpcError(env.ID, "cycle_thinking_level", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "cycle_thinking_level", map[string]string{
				"level": string(nextLevel),
			}))

		case RPCCommandGetAvailableThinkingLevels:
			levels := sess().AvailableThinkingLevels()
			writeRPC(rpcSuccess(env.ID, "get_available_thinking_levels", map[string]any{"levels": levels}))

		// ── set_auto_retry ────────────────────────────────────────────────
		case RPCCommandSetAutoRetry:
			cmd, err := decodeRPCCommand[RPCSetAutoRetryCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "set_auto_retry", err.Error()))
				continue
			}
			sess().SetAutoRetryEnabled(cmd.Enabled)
			writeRPC(rpcSuccess(env.ID, "set_auto_retry", nil))

		case RPCCommandAbortRetry:
			sess().AbortRetry()
			writeRPC(rpcSuccess(env.ID, "abort_retry", nil))

		// ── steer ────────────────────────────────────────────────────────────
		case RPCCommandSteer:
			cmd, err := decodeRPCCommand[RPCSteerCommand](env)
			if err != nil {
				turn.complete(rpcError(env.ID, "steer", err.Error()))
				continue
			}
			admission.queue(env.ID, "steer", cmd.Message, rpcImages(cmd.Images))

		// ── follow_up ─────────────────────────────────────────────────────────
		case RPCCommandFollowUp:
			cmd, err := decodeRPCCommand[RPCFollowUpCommand](env)
			if err != nil {
				turn.complete(rpcError(env.ID, "follow_up", err.Error()))
				continue
			}
			admission.queue(env.ID, "follow_up", cmd.Message, rpcImages(cmd.Images))

		// ── bash ────────────────────────────────────────────────────────────────
		case RPCCommandBash:
			cmd, err := decodeRPCCommand[RPCBashCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "bash", err.Error()))
				continue
			}
			// Run bash in a goroutine (may block for a long time).
			bashWG.Add(1)
			go func(id rpcRequestID, command string, excludeFromContext *bool) {
				defer bashWG.Done()
				// Upstream rpc-mode awaits emitUserBash first: a result override is
				// recorded and returned without running the command, and a failed
				// handler fails the request (#9068).
				override, operations, err := rpcUserBashOverride(ctx, runner(), command, excludeFromContext != nil && *excludeFromContext, sess().CWD())
				if err != nil {
					turn.complete(rpcError(id, "bash", err.Error()))
					return
				}
				if override != nil {
					if err := sess().RecordBashResult(command, *override, excludeFromContext); err != nil {
						turn.complete(rpcError(id, "bash", err.Error()))
						return
					}
					turn.complete(rpcSuccess(id, "bash", RPCBashResult(*override)))
					return
				}
				result, err := sess().ExecuteBash(ctx, command, nil, &coding.ExecuteBashOptions{ExcludeFromContext: excludeFromContext, Operations: operations, ID: rpcBashUpdateID(id)})
				flushErr := sess().FlushEvents(ctx)
				if err != nil {
					turn.complete(rpcError(id, "bash", err.Error()))
					return
				}
				if flushErr != nil {
					turn.complete(rpcError(id, "bash", flushErr.Error()))
					return
				}
				turn.complete(rpcSuccess(id, "bash", RPCBashResult{
					Output:         result.Output,
					ExitCode:       result.ExitCode,
					Cancelled:      result.Cancelled,
					Truncated:      result.Truncated,
					FullOutputPath: result.FullOutputPath,
				}))
			}(env.ID, cmd.Command, cmd.ExcludeFromContext)

		// ── abort_bash ────────────────────────────────────────────────────────
		case RPCCommandAbortBash:
			sess().AbortBash()
			writeRPC(rpcSuccess(env.ID, "abort_bash", nil))

		// ── set_session_name ─────────────────────────────────────────────────
		case RPCCommandSetSessionName:
			cmd, err := decodeRPCCommand[RPCSetSessionNameCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "set_session_name", err.Error()))
				continue
			}
			name := widthx.JSTrim(cmd.Name)
			if name == "" {
				writeRPC(rpcError(env.ID, "set_session_name", "Session name cannot be empty"))
				continue
			}
			if err := sess().SetSessionName(name); err != nil {
				writeRPC(rpcError(env.ID, "set_session_name", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_session_name", nil))

		// ── get_session_stats ────────────────────────────────────────────────
		case RPCCommandGetSessionStats:
			writeRPC(rpcSuccess(env.ID, "get_session_stats", sess().GetSessionStats()))

		case RPCCommandExportHtml:
			cmd, err := decodeRPCCommand[RPCExportHTMLCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "export_html", err.Error()))
				continue
			}
			// upstream rpc-mode.ts:601 session.exportToHtml(outputPath).
			// exportToHtml is async: its response follows a later command of the same input batch.
			currentSession, commandID := sess(), env.ID
			rpcAwaitWork(admission, false, func() (string, error) {
				return currentSession.ExportToHTML(cmd.OutputPath)
			}).then(func(outputPath string, err error) {
				if err != nil {
					writeRPC(rpcError(commandID, "export_html", err.Error()))
					return
				}
				writeRPC(rpcSuccess(commandID, "export_html", map[string]string{"path": outputPath}))
			})

		case RPCCommandSwitchSession:
			cmd, err := decodeRPCCommand[RPCSwitchSessionCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "switch_session", err.Error()))
				continue
			}
			if cmd.SessionPath == "" {
				writeRPC(rpcError(env.ID, "switch_session", "EISDIR: illegal operation on a directory, read"))
				continue
			}
			awaitReplacement(admission, turn, env.ID, "switch_session", func() error { return rt.ValidateSwitchSession(cmd.SessionPath) }, func(ctx context.Context) (any, bool, error) {
				result, err := rt.SwitchSession(ctx, cmd.SessionPath)
				return RPCCancelledResult{Cancelled: result.Cancelled}, result.Cancelled, err
			})

		case RPCCommandFork:
			cmd, err := decodeRPCCommand[RPCForkCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "fork", err.Error()))
				continue
			}
			if cmd.EntryID == "" {
				writeRPC(rpcError(env.ID, "fork", "Invalid entry ID for forking"))
				continue
			}
			currentSession := sess()
			awaitReplacement(admission, turn, env.ID, "fork", func() error { return currentSession.ValidateFork(cmd.EntryID, "before") }, func(ctx context.Context) (any, bool, error) {
				result, err := rt.Fork(coding.WithForkSession(ctx, currentSession), cmd.EntryID, nil)
				text := ""
				if result.SelectedText != nil {
					text = *result.SelectedText
				}
				return RPCForkResult{Text: text, Cancelled: result.Cancelled}, result.Cancelled, err
			})

		case RPCCommandClone:
			leaf := sess().LeafID()
			if leaf == nil {
				writeRPC(rpcError(env.ID, "clone", "Cannot clone session: no current entry selected"))
				continue
			}
			currentSession := sess()
			awaitReplacement(admission, turn, env.ID, "clone", func() error { return currentSession.ValidateFork(*leaf, "at") }, func(ctx context.Context) (any, bool, error) {
				result, err := rt.Fork(coding.WithForkSession(ctx, currentSession), *leaf, &extension.ForkOptions{Position: "at"})
				return RPCCancelledResult{Cancelled: result.Cancelled}, result.Cancelled, err
			})

		// ── get_messages ────────────────────────────────────────────────────────
		case RPCCommandGetMessages:
			data, err := rpcGetMessagesData(sess().Messages())
			if err != nil {
				writeRPC(rpcError(env.ID, "get_messages", err.Error()))
				break
			}
			writeRPC(rpcSuccess(env.ID, "get_messages", data))

		// ── get_last_assistant_text ───────────────────────────────────────────
		case RPCCommandGetLastAssistantText:
			// Upstream: success(id, "get_last_assistant_text", { text })
			// When getLastAssistantText() returns undefined, JS serializes
			// {text: undefined} as {} (keys with undefined values are omitted).
			// Match by omitting the key when nil.
			if text := sess().LastAssistantText(); text != nil {
				writeRPC(rpcSuccess(env.ID, "get_last_assistant_text", map[string]any{
					"text": *text,
				}))
			} else {
				writeRPC(rpcSuccess(env.ID, "get_last_assistant_text", map[string]any{}))
			}

		// ── get_fork_messages ─────────────────────────────────────────────────
		case RPCCommandGetForkMessages:
			writeRPC(rpcSuccess(env.ID, "get_fork_messages", map[string]any{
				"messages": sess().UserMessagesForForking(),
			}))

		// ── get_entries ───────────────────────────────────────────────────────
		case RPCCommandGetEntries:
			cmd, err := decodeRPCCommand[RPCGetEntriesCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "get_entries", err.Error()))
				continue
			}
			entries, err := rpcEntriesSince(codingagent.HandleReads{Session: sess().Inner()}.GetEntries(), cmd.Since)
			if err != nil {
				writeRPC(rpcError(env.ID, "get_entries", err.Error()))
				continue
			}
			records, err := sessionentry.ReadRaws(entries)
			if err != nil {
				writeRPC(rpcError(env.ID, "get_entries", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "get_entries", rpcEntriesData{
				Entries: records,
				LeafID:  sess().LeafID(),
			}))

		// ── get_tree ──────────────────────────────────────────────────────────
		case RPCCommandGetTree:
			tree, err := rpcTree(&codingagent.SessionTreeNode{Children: codingagent.HandleReads{Session: sess().Inner()}.GetTree()})
			if err != nil {
				writeRPC(rpcError(env.ID, "get_tree", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "get_tree", rpcTreeData{
				Tree:   tree,
				LeafID: sess().LeafID(),
			}))

		// ── set_steering_mode / set_follow_up_mode ─────────────────────────
		// Persist to settings so the value survives sessions.
		case RPCCommandSetSteeringMode:
			cmd, err := decodeRPCCommand[RPCSetSteeringModeCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "set_steering_mode", err.Error()))
				continue
			}
			if err := sess().SetSteeringMode(agent.QueueMode(cmd.Mode)); err != nil {
				writeRPC(rpcError(env.ID, "set_steering_mode", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_steering_mode", nil))

		case RPCCommandSetFollowUpMode:
			cmd, err := decodeRPCCommand[RPCSetFollowUpModeCommand](env)
			if err != nil {
				writeRPC(rpcError(env.ID, "set_follow_up_mode", err.Error()))
				continue
			}
			if err := sess().SetFollowUpMode(agent.QueueMode(cmd.Mode)); err != nil {
				writeRPC(rpcError(env.ID, "set_follow_up_mode", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_follow_up_mode", nil))

		default:
			writeRPC(rpcUnknownCommand(env))
		}
	}

	if inputTurn != nil {
		inputTurn.end()
		inputTurn = nil
	}

	// Detach input on cancellation. The stdin reader belongs to the process, not the Session; process exit releases a pending OS read just as Pi pauses stdin before exiting.
	// The reader reports stdin end only after the runtime was disposed.
	var scanErr error
	select {
	case scanErr = <-inputDone:
	case <-ctx.Done():
		select {
		case scanErr = <-inputDone:
		default:
		}
	}
	if scanErr != nil && !errors.Is(scanErr, io.EOF) {
		writeRPC(RPCErrorEvent{Type: "error", Message: fmt.Sprintf("stdin read error: %v", scanErr)})
	}

	// Stop admitting or blocking on extension work before joining commands.
	// A model-select handler can be waiting for an RPC dialog while its command
	// is in commandWG, so waiting first would deadlock on EOF.
	cancel()
	rpcUI.Close()
	host.tasks.CloseAndWait()
	responses.begin()
	responses.end()
	settleSessionWork()

	// Runtime.Close closes the current Session, which closes its events channel, and retires its host.
	_ = rt.Close()
	host.closeHost()
	host.forwarders.Wait()
	select {
	case <-host.conversionErr:
		return 1
	default:
	}

	return 0
}

type rpcEntriesData struct {
	Entries []json.RawMessage `json:"entries"`
	LeafID  *string           `json:"leafId"`
}

type rpcTreeData struct {
	Tree   []rpcSessionTreeNode `json:"tree"`
	LeafID *string              `json:"leafId"`
}

// rpcSessionTreeNode is the JSON shape returned by upstream get_tree.
// Pig's internal tree uses exported Go fields and a synthetic root; RPC returns
// lower-case keys and only real root entries.
type rpcSessionTreeNode struct {
	Entry          json.RawMessage      `json:"entry"`
	Children       []rpcSessionTreeNode `json:"children"`
	Label          string               `json:"label,omitempty"`
	LabelTimestamp string               `json:"labelTimestamp,omitempty"`
}

// rpcAvailableModels is the model runtime's availability snapshot, which is what Pi's get_available_models, set_model and cycle_model read (rpc-mode.ts:473,490-493; agent-session.ts getAvailableSnapshot). Only the PIG_TEST_FAUX provider, which has no catalog entry, joins it. With nothing available Pi answers [].
// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts:get_available_models.
func rpcAvailableModels(services *coding.AgentSessionServices, current *ai.Model) []*ai.Model {
	models := services.ModelRuntime().GetAvailableSnapshot()
	if current == nil || current.ID == "" || os.Getenv("PIG_TEST_FAUX") != "1" {
		return models
	}
	provider := current.ProviderMeta.ProviderID
	if provider == "" && current.Provider != nil {
		provider = current.Provider.ID()
	}
	if provider != "test-faux" || slices.ContainsFunc(models, func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == provider && model.ID == current.ID }) {
		return models
	}
	return append(models, current)
}

func rpcModelList(models []*ai.Model) []*RPCModel {
	out := make([]*RPCModel, len(models))
	for i, model := range models {
		out[i] = rpcModelValue(model)
	}
	return out
}

func rpcEntriesSince(entries []codingagent.SessionEntry, since string) ([]codingagent.SessionEntry, error) {
	if since == "" {
		return entries, nil
	}
	for i, entry := range entries {
		if entry.Base().ID == since {
			return entries[i+1:], nil
		}
	}
	return nil, fmt.Errorf("Entry not found: %s", since)
}

// rpcTree is the get_tree shape of root's children, with each entry's record read once; an entry whose record changed on disk fails it.
func rpcTree(root *codingagent.SessionTreeNode) ([]rpcSessionTreeNode, error) {
	if root == nil {
		return nil, nil
	}
	var entries []codingagent.SessionEntry
	var collect func(nodes []*codingagent.SessionTreeNode)
	collect = func(nodes []*codingagent.SessionTreeNode) {
		for _, node := range nodes {
			entries = append(entries, node.Entry)
			collect(node.Children)
		}
	}
	collect(root.Children)
	records, err := sessionentry.ReadRaws(entries)
	if err != nil {
		return nil, err
	}
	next := 0
	var build func(nodes []*codingagent.SessionTreeNode) []rpcSessionTreeNode
	build = func(nodes []*codingagent.SessionTreeNode) []rpcSessionTreeNode {
		out := make([]rpcSessionTreeNode, len(nodes))
		for i, node := range nodes {
			out[i] = rpcSessionTreeNode{Entry: records[next], Label: node.Label, LabelTimestamp: node.LabelTimestamp}
			next++
			out[i].Children = build(node.Children)
		}
		return out
	}
	return build(root.Children), nil
}

// rpcUserBashOverride dispatches user_bash for an RPC bash command. It returns
// the extension's replacement result, or the operations to run the command
// through (nil for local execution), or the handler failure that must fail
// the request.
func rpcUserBashOverride(ctx context.Context, runner *inproc.Runner, command string, excludeFromContext bool, cwd string) (*coding.BashResult, extension.BashOperations, error) {
	if runner == nil || !runner.HasHandlers("user_bash") {
		return nil, nil, nil
	}
	eventResult, err := runner.EmitUserBash(ctx, extension.UserBashEvent{
		Type: "user_bash", Command: command, ExcludeFromContext: excludeFromContext, Cwd: cwd,
	})
	if err != nil || eventResult == nil {
		return nil, nil, err
	}
	if eventResult.Result == nil {
		return nil, eventResult.Operations, nil
	}
	encoded, err := json.Marshal(eventResult.Result)
	if err != nil {
		return nil, nil, err
	}
	var result coding.BashResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, nil, err
	}
	return &result, nil, nil
}
