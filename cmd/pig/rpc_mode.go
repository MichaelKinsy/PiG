// Headless JSONL command and event loop.
//
// The command set and wire shapes follow the pinned Pi rpc-mode.ts and
// rpc-types.ts. Extension UI dialogs use extension_ui_request and
// extension_ui_response records on the same stream.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
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
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
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
	mode string
	// llama runs the built-in llama.cpp extension's /llama command, which the
	// runner lists with the other extension commands; notify carries its
	// ctx.ui.notify calls to the client.
	llama           *llama.Host
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

func (c headlessCommandCatalog) routePrompt(ctx context.Context, message string) (string, bool) {
	if name, args, ok := c.extensionCommand(message); ok {
		return "", c.executeCommand(ctx, name, args)
	}
	return c.expandPrompt(message), false
}

// executeCommand runs a command extensionCommand resolved; the built-in
// llama.cpp extension's /llama runs the llama host with this mode's command
// context.
func (c headlessCommandCatalog) executeCommand(ctx context.Context, name, args string) bool {
	if command, found := c.runner.Command(name); found && c.llama != nil && codingagent.IsLlamaCommand(command) {
		_ = c.llama.HandleCommand(llama.CommandContext{Ctx: ctx, Mode: c.mode, Notify: c.notify})
		return true
	}
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
		if out[i].SourceInfo != nil {
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
		if copy.Path == "" && activePiglet != nil {
			// pig additive (D18): the synthesized path identifies the inline skill's manifest, not a file to reload as Markdown.
			copy.SourceInfo.Source = "inline"
			copy.Path = activePiglet.SourcePath()
			if copy.Path == "" {
				copy.Path = "builtin:piglet"
			}
			copy.Dir = filepath.Dir(copy.Path)
		}
		out[i] = &copy
	}
	return out
}

// runRPCMode owns the `--mode rpc` runtime. EOF and termination triggers dispose and exit the process; startup failures and caller cancellation return to main. Protocol records bypass startup's stdout redirection.
func runRPCMode(ctx context.Context, flags CLIFlags, activePiglet *piglet.Piglet, resources rpcModeResources) (exitCode int) {
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
	// RPC refreshes dynamic model catalogs (Radius) in the background, as
	// upstream main.ts does for RPC mode unless offline (15 s timeout).
	refreshCatalogsInBackground(ctx, initial.Llama)
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
		ctx: ctx, cancel: cancel, flags: flags, piglet: activePiglet,
		ui: rpcUI, writeRPC: writeRPC, responses: responses,
		promptWG: &promptWG, tasks: &rpcTaskGroup{},
		conversionErr:   make(chan error, 1),
		requestShutdown: func() { shutdownRequested.Store(true) },
		stdoutWait:      func(ctx context.Context) { rawStdout.Wait(ctx) },
	}
	inputs, initialState := builder.rpcInputs(initial, &rpcStartup{
		Manager: resources.SessionManager, ResumePath: resources.ResumePath, SessionName: flags.Name, SessionDir: sessionDir,
	})
	host.factory = newCLISessionFactory(processCtx, inputs, initialState, func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (cliSessionInputs, *rpcSessionState, error) {
		build, err := builder.rebuild(ctx, options)
		if err != nil {
			return cliSessionInputs{}, nil, err
		}
		next, state := builder.rpcInputs(build, nil)
		return next, state, nil
	})
	defer host.factory.Close()
	manager, err := coding.SessionManagerFor(initial.Services, inputs.Start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: session: %v\n", err)
		return 1
	}
	rt, err := coding.CreateAgentSessionRuntime(ctx, host.factory.Factory(), coding.CreateAgentSessionRuntimeOptions{
		CWD: initial.CWD, AgentDir: initial.Services.AgentDir(), SessionManager: manager,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: session: %v\n", err)
		return 1
	}
	host.rt = rt
	// Runtime.Close closes the current Session and retires its host. session_shutdown for quit has been emitted by then, by the shutdown below or by Close itself.
	defer func() { _ = rt.Close() }()
	sess := func() *coding.Session { return rt.Session() }
	services := func() *coding.Services { return rt.Services() }
	runner := func() *inproc.Runner { return rt.Session().ExtensionRunner() }
	commandCatalog := func() headlessCommandCatalog { return host.admission.Load().catalog }

	// Upstream shutdown() runs once, on stdin end, ctx.shutdown(), or a
	// termination signal (rpc-mode.ts:728-744). It removes the signal
	// handlers, stops writing Session events, and awaits runtimeHost.dispose():
	// session_shutdown, then AgentSession.dispose, while the Session still runs.
	// It then flushes stdout, unless the signal is SIGTERM, and exits without
	// joining the remaining work: dialogs stdin can no longer answer stay
	// pending, and their commands never respond.
	terminateProcesses := func() { host.factory.TerminateProcesses() }
	exitRPC := func(code int) {
		stdoutClosed.Store(true)
		if code != 128+int(syscall.SIGTERM) {
			// Upstream flushes stdout before process.exit unless the signal is SIGTERM; the queued tail is that pending output.
			rawStdout.Wait(context.Background())
		}
		terminateProcesses()
		// Upstream process.exit does not wait for providers or suspended extension callbacks.
		os.Exit(code)
	}
	host.drain = func() { exitRPC(0) }
	forceSignal := func(sig os.Signal) {
		terminateProcesses()
		dieBySignal(sig)
	}
	var forced atomic.Pointer[forcedTermination]
	defer func() { forced.Load().Stop() }()
	shutdownInvoked := make(chan struct{})
	shutdownContext := invocation.WithAcknowledgment(context.Background(), func() { close(shutdownInvoked) })
	shutdown := newRPCShutdown(
		func() { forced.Store(watchForcedTermination(forceSignal)) },
		func() {
			// Upstream writes each Session event when it is published, so the
			// events already published reach stdout before it unsubscribes.
			if host.forwarding.Load() > 0 {
				_ = sess().FlushEvents(context.Background())
			}
			host.detached.Store(true)
		},
		func() {
			host.factory.HoldRetirements()
			rt.EmitQuitShutdownContext(shutdownContext)
			invocation.Acknowledge(shutdownContext)
			sess().Dispose()
		},
		func() {
			select {
			case <-rpcUI.PendingRequest():
				return
			case <-responses.idle():
			}
			if commandJoin := host.commandJoin.Load(); commandJoin != nil {
				// pig additive (D19): the host applies Pi's stdin-end window to runtimes that report none; a Node runtime reports each command's own window.
				if st, ok := host.state(sess()); ok && st.Build.Host != nil {
					st.Build.Host.FlushCommands()
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
		st, ok := host.state(session)
		if !ok || st.Reload == nil {
			return errors.New("rpc: this Session cannot reload")
		}
		return st.Reload(ctx, host.ctx, session, func(ctx context.Context, event extension.SessionStartEvent) error {
			return host.bind(ctx, session, event, true)
		})
	})
	first := rt.Session()
	if err := host.bind(ctx, first, first.StartEvent(), false); err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: %v\n", err)
		return 1
	}

	// Preflight is not an agent run. Admission marks the Session streaming only after before_agent_start and image normalization finish.
	streaming := func() bool { return sess().IsStreaming() }

	handleCycleModel := func(id rpcRequestID, currentSession *coding.Session, turn *rpcResponseTurn) {
		models := rpcAvailableModels(services(), currentSession.Model())
		scoped := rpcScopedModels(models, flags.Models)
		if len(scoped) <= 1 {
			turn.after(func() { writeRPC(rpcSuccessNull(id, "cycle_model")) })
			return
		}
		currentIndex := -1
		for i, candidate := range scoped {
			if ai.ModelsAreEqual(candidate, currentSession.Model()) {
				currentIndex = i
				break
			}
		}
		if currentIndex < 0 {
			currentIndex = 0
		}
		next := scoped[(currentIndex+1)%len(scoped)]
		complete, err := currentSession.BeginModelChange(ctx, next, extension.ModelSelectSourceCycle)
		if err != nil {
			turn.complete(rpcError(id, "cycle_model", err.Error()))
			return
		}
		if err := currentSession.FlushEvents(ctx); err != nil {
			turn.complete(rpcError(id, "cycle_model", err.Error()))
			return
		}
		publish := func() {
			writeRPC(rpcSuccess(id, "cycle_model", RPCModelCycleResult{
				Model: rpcModelValue(next), ThinkingLevel: currentSession.ThinkingLevel(), IsScoped: len(flags.Models) > 0,
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
	// A replacement that an RPC command requests drains the mode's own work once the before hooks approved it. A replacement that an extension requests does not: that extension's handler is itself part of the work.
	rt.SetBeforeSessionReplacement(func(ctx context.Context) error {
		if ctx.Value(rpcCommandReplacement{}) != nil {
			settleSessionWork()
		}
		return nil
	})
	replacementCtx := context.WithValue(ctx, rpcCommandReplacement{}, true)

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
		host.factory.EndInput()
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
		switch env.Type {
		// ── prompt ──────────────────────────────────────────────────────
		case "prompt":
			var cmd RPCPromptCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "prompt", err.Error()))
				continue
			}
			if name, args, ok := commandCatalog().extensionCommand(cmd.Message); ok {
				admission.command(env.ID, name, args)
				commandChecked = true
				continue
			}
			admission.prompt(env.ID, cmd)

		// ── abort ────────────────────────────────────────────────────────
		case "abort":
			sess().RequestAbort()
			rpcAwaitWork(admission, true, func() (struct{}, error) {
				waitPrompt()
				return struct{}{}, nil
			}).then(func(_ struct{}, err error) {
				if err != nil {
					writeRPC(rpcError(env.ID, "abort", err.Error()))
				} else {
					writeRPC(rpcSuccess(env.ID, "abort", nil))
				}
			})

		case "clear_queue":
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

		case "new_session":
			var cmd RPCNewSessionCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "new_session", err.Error()))
				continue
			}
			var options *extension.NewSessionOptions
			if cmd.ParentSession != "" {
				options = &extension.NewSessionOptions{ParentSession: cmd.ParentSession}
			}
			result, err := rt.NewSession(replacementCtx, options)
			if err == nil && !result.Cancelled {
				err = host.rebindAgain(ctx)
			}
			if err != nil {
				writeRPC(rpcError(env.ID, "new_session", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "new_session", RPCCancelledResult{Cancelled: result.Cancelled}))

		// ── get_commands ───────────────────────────────────────────────
		case "get_commands":
			writeRPC(rpcGetCommandsResponse(env.ID, commandCatalog()))

		// ── get_state ────────────────────────────────────────────────────
		case "get_state":
			compactionEnabled := services().SettingsManager().GetCompactionEnabled()
			state := RPCSessionState{
				Model:                 rpcModelValue(sess().Model()),
				ThinkingLevel:         string(sess().ThinkingLevel()),
				IsStreaming:           streaming(),
				IsCompacting:          sess().IsCompacting(),
				SteeringMode:          string(sess().Agent().SteeringMode()),
				FollowUpMode:          string(sess().Agent().FollowUpMode()),
				SessionFile:           sess().Path(),
				SessionID:             sess().ID(),
				SessionName:           sess().SessionName(),
				AutoCompactionEnabled: compactionEnabled,
				MessageCount:          len(sess().Messages()),
				PendingMessageCount:   sess().PendingMessageCount(),
			}
			writeRPC(rpcSuccess(env.ID, "get_state", state))

		// ── set_model ────────────────────────────────────────────────────
		case "set_model":
			var cmd RPCSetModelCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
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
			if err := sess().SetModel(newModel); err != nil {
				writeRPC(rpcError(env.ID, "set_model", err.Error()))
				continue
			}
			if err := sess().FlushEvents(ctx); err != nil {
				writeRPC(rpcError(env.ID, "set_model", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_model", rpcModelValue(newModel)))

		case "cycle_model":
			handleCycleModel(env.ID, sess(), turn)

		// ── compact ─────────────────────────────────────────────────────
		case "compact":
			var cmd RPCCompactCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "compact", err.Error()))
				continue
			}
			commandID := env.ID
			customInstructions := cmd.CustomInstructions
			currentSession := sess()
			commandWG.Go(func() {
				// The input loop owns its response turn; this worker only aborts and joins Session work.
				currentSession.RequestAbort()
				waitPrompt()
				result, err := currentSession.CompactResult(ctx, customInstructions)
				if flushErr := currentSession.FlushEvents(ctx); err == nil {
					err = flushErr
				}
				if err != nil {
					writeRPC(rpcError(commandID, "compact", err.Error()))
					return
				}
				writeRPC(rpcSuccess(commandID, "compact", rpcCompactionResult(result)))
			})

		// ── set_auto_compaction ──────────────────────────────────────────
		case "set_auto_compaction":
			var cmd RPCSetAutoCompactionCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_auto_compaction", err.Error()))
				continue
			}
			if settingsMgr := services().SettingsManager(); settingsMgr != nil {
				if err := settingsMgr.UpdateGlobal(func(s *codingagent.Settings) {
					if s.Compaction == nil {
						s.Compaction = &codingagent.CompactionSettingsJSON{}
					}
					s.Compaction.Enabled = &cmd.Enabled
				}); err != nil {
					writeRPC(rpcError(env.ID, "set_auto_compaction", err.Error()))
					continue
				}
			}
			writeRPC(rpcSuccess(env.ID, "set_auto_compaction", nil))

		// ── get_available_models ─────────────────────────────────────────
		case "get_available_models":
			models := rpcAvailableModels(services(), sess().Model())
			writeRPC(rpcSuccess(env.ID, "get_available_models", map[string]any{
				"models": rpcModelList(models),
			}))

		// ── set_thinking_level ─────────────────────────────────────────
		case "set_thinking_level":
			var cmd RPCSetThinkingLevelCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_thinking_level", err.Error()))
				continue
			}
			if err := sess().SetThinkingLevel(ai.ThinkingLevel(cmd.Level)); err != nil {
				writeRPC(rpcError(env.ID, "set_thinking_level", err.Error()))
				continue
			}
			if err := sess().FlushEvents(ctx); err != nil {
				writeRPC(rpcError(env.ID, "set_thinking_level", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_thinking_level", nil))

		// ── cycle_thinking_level ─────────────────────────────────────────────
		case "cycle_thinking_level":
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

		case "get_available_thinking_levels":
			levels := sess().AvailableThinkingLevels()
			writeRPC(rpcSuccess(env.ID, "get_available_thinking_levels", map[string]any{"levels": levels}))

		// ── set_auto_retry ────────────────────────────────────────────────
		case "set_auto_retry":
			var cmd RPCSetAutoRetryCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_auto_retry", err.Error()))
				continue
			}
			if err := sess().SetAutoRetryEnabled(cmd.Enabled); err != nil {
				writeRPC(rpcError(env.ID, "set_auto_retry", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_auto_retry", nil))

		case "abort_retry":
			sess().AbortRetry()
			writeRPC(rpcSuccess(env.ID, "abort_retry", nil))

		// ── steer ────────────────────────────────────────────────────────────
		case "steer":
			var cmd RPCSteerCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				turn.complete(rpcError(env.ID, "steer", err.Error()))
				continue
			}
			admission.queue(env.ID, "steer", cmd.Message, rpcImages(cmd.Images))

		// ── follow_up ─────────────────────────────────────────────────────────
		case "follow_up":
			var cmd RPCFollowUpCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				turn.complete(rpcError(env.ID, "follow_up", err.Error()))
				continue
			}
			admission.queue(env.ID, "follow_up", cmd.Message, rpcImages(cmd.Images))

		// ── bash ────────────────────────────────────────────────────────────────
		case "bash":
			var cmd RPCBashCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "bash", err.Error()))
				continue
			}
			// Run bash in a goroutine (may block for a long time).
			bashWG.Add(1)
			go func(id rpcRequestID, command string, excludeFromContext bool) {
				defer bashWG.Done()
				// Upstream rpc-mode awaits emitUserBash first: a result override is
				// recorded and returned without running the command, and a failed
				// handler fails the request (#9068).
				override, operations, err := rpcUserBashOverride(ctx, runner(), command, excludeFromContext, sess().CWD())
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
				result, err := sess().ExecuteBashWithOperations(ctx, command, excludeFromContext, nil, operations, rpcBashUpdateID(id))
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
		case "abort_bash":
			sess().AbortBash()
			writeRPC(rpcSuccess(env.ID, "abort_bash", nil))

		// ── set_session_name ─────────────────────────────────────────────────
		case "set_session_name":
			var cmd RPCSetSessionNameCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
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
		case "get_session_stats":
			writeRPC(rpcSuccess(env.ID, "get_session_stats", sess().GetSessionStats()))

		case "export_html":
			var cmd RPCExportHTMLCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "export_html", err.Error()))
				continue
			}
			var registered []extension.RegisteredTool
			if runner != nil {
				registered = runner().Tools()
			}
			// upstream rpc-mode.ts:601 exportToHtml passes agent.state: the transcript's system prompt and the active tools.
			state := codingagent.NewShareState(codingagent.AgentStateSystemPrompt(sess().Agent().MessagesSnapshot()), sess().Tools())
			outputPath, err := codingagent.ExportSessionToHTML(sess().Path(), cmd.OutputPath, registered, services().CWD(), state)
			if err != nil {
				writeRPC(rpcError(env.ID, "export_html", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "export_html", map[string]string{"path": outputPath}))

		case "switch_session":
			var cmd RPCSwitchSessionCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "switch_session", err.Error()))
				continue
			}
			if cmd.SessionPath == "" {
				writeRPC(rpcError(env.ID, "switch_session", "EISDIR: illegal operation on a directory, read"))
				continue
			}
			result, err := rt.SwitchSession(replacementCtx, cmd.SessionPath)
			if err == nil && !result.Cancelled {
				err = host.rebindAgain(ctx)
			}
			if err != nil {
				writeRPC(rpcError(env.ID, "switch_session", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "switch_session", RPCCancelledResult{Cancelled: result.Cancelled}))

		case "fork":
			var cmd RPCForkCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "fork", err.Error()))
				continue
			}
			if cmd.EntryID == "" {
				writeRPC(rpcError(env.ID, "fork", "Invalid entry ID for forking"))
				continue
			}
			result, err := rt.Fork(replacementCtx, cmd.EntryID, nil)
			if err == nil && !result.Cancelled {
				err = host.rebindAgain(ctx)
			}
			if err != nil {
				writeRPC(rpcError(env.ID, "fork", err.Error()))
				continue
			}
			text := ""
			if result.SelectedText != nil {
				text = *result.SelectedText
			}
			writeRPC(rpcSuccess(env.ID, "fork", RPCForkResult{Text: text, Cancelled: result.Cancelled}))

		case "clone":
			leaf := sess().LeafID()
			if leaf == nil {
				writeRPC(rpcError(env.ID, "clone", "Cannot clone session: no current entry selected"))
				continue
			}
			result, err := rt.Fork(replacementCtx, *leaf, &extension.ForkOptions{Position: "at"})
			if err == nil && !result.Cancelled {
				err = host.rebindAgain(ctx)
			}
			if err != nil {
				writeRPC(rpcError(env.ID, "clone", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "clone", RPCCancelledResult{Cancelled: result.Cancelled}))

		// ── get_messages ────────────────────────────────────────────────────────
		case "get_messages":
			writeRPC(rpcSuccess(env.ID, "get_messages", map[string]any{
				"messages": sess().Messages(),
			}))

		// ── get_last_assistant_text ───────────────────────────────────────────
		case "get_last_assistant_text":
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
		case "get_fork_messages":
			writeRPC(rpcSuccess(env.ID, "get_fork_messages", map[string]any{
				"messages": sess().UserMessagesForForking(),
			}))

		// ── get_entries ───────────────────────────────────────────────────────
		case "get_entries":
			var cmd RPCGetEntriesCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "get_entries", err.Error()))
				continue
			}
			entries, err := rpcEntriesSince(sess().Entries(), cmd.Since)
			if err != nil {
				writeRPC(rpcError(env.ID, "get_entries", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "get_entries", rpcEntriesData{
				Entries: entries,
				LeafID:  sess().LeafID(),
			}))

		// ── get_tree ──────────────────────────────────────────────────────────
		case "get_tree":
			writeRPC(rpcSuccess(env.ID, "get_tree", rpcTreeData{
				Tree:   rpcTree(sess().Tree()),
				LeafID: sess().LeafID(),
			}))

		// ── set_steering_mode / set_follow_up_mode ─────────────────────────
		// Persist to settings so the value survives sessions.
		case "set_steering_mode":
			var cmd RPCSetSteeringModeCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_steering_mode", err.Error()))
				continue
			}
			if err := sess().SetSteeringMode(agent.QueueMode(cmd.Mode)); err != nil {
				writeRPC(rpcError(env.ID, "set_steering_mode", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_steering_mode", nil))

		case "set_follow_up_mode":
			var cmd RPCSetFollowUpModeCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
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
	Entries []codingagent.SessionEntry `json:"entries"`
	LeafID  *string                    `json:"leafId"`
}

type rpcTreeData struct {
	Tree   []rpcSessionTreeNode `json:"tree"`
	LeafID *string              `json:"leafId"`
}

// rpcSessionTreeNode is the JSON shape returned by upstream get_tree.
// Pig's internal tree uses exported Go fields and a synthetic root; RPC returns
// lower-case keys and only real root entries.
type rpcSessionTreeNode struct {
	Entry          codingagent.SessionEntry `json:"entry"`
	Children       []rpcSessionTreeNode     `json:"children"`
	Label          string                   `json:"label,omitempty"`
	LabelTimestamp string                   `json:"labelTimestamp,omitempty"`
}

type rpcEventRunner interface {
	Emit(context.Context, any) (any, error)
}

func rpcBeforeSessionSwitch(ctx context.Context, runner rpcEventRunner, reason, target string) (bool, error) {
	if runner == nil {
		return false, nil
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeSwitchEvent{
		Type:              codingagent.EventSessionBeforeSwitch,
		Reason:            reason,
		TargetSessionFile: target,
	})
	if err != nil {
		return false, err
	}
	return rpcResultCancelled(result), nil
}

func rpcBeforeSessionFork(ctx context.Context, runner rpcEventRunner, entryID, position string) (bool, error) {
	if runner == nil {
		return false, nil
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeForkEvent{
		Type:     codingagent.EventSessionBeforeFork,
		EntryID:  entryID,
		Position: position,
	})
	if err != nil {
		return false, err
	}
	return rpcResultCancelled(result), nil
}

func rpcResultCancelled(result any) bool {
	switch value := result.(type) {
	case extension.SessionBeforeSwitchResult:
		return value.Cancel
	case *extension.SessionBeforeSwitchResult:
		return value != nil && value.Cancel
	case extension.SessionBeforeForkResult:
		return value.Cancel
	case *extension.SessionBeforeForkResult:
		return value != nil && value.Cancel
	}
	data, err := json.Marshal(result)
	if err != nil {
		return false
	}
	var value struct {
		Cancel bool `json:"cancel"`
	}
	return json.Unmarshal(data, &value) == nil && value.Cancel
}

// rpcAvailableModels is the model runtime's availability snapshot, which is what Pi's get_available_models, set_model and cycle_model read (rpc-mode.ts:473,490-493; agent-session.ts getAvailableSnapshot). Only the PIG_TEST_FAUX provider, which has no catalog entry, joins it. With nothing available Pi answers [].
// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts:get_available_models.
func rpcAvailableModels(services *coding.Services, current *ai.Model) []*ai.Model {
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

func rpcScopedModels(models []*ai.Model, patterns []string) []*ai.Model {
	if len(patterns) == 0 {
		return models
	}
	var scoped []*ai.Model
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		pattern = rpcModelPatternWithoutThinking(pattern)
		for _, model := range models {
			provider := model.ProviderMeta.ProviderID
			if provider == "" && model.Provider != nil {
				provider = model.Provider.ID()
			}
			fullID := provider + "/" + model.ID
			matched := strings.EqualFold(pattern, fullID) || strings.EqualFold(pattern, model.ID)
			if !matched && strings.ContainsAny(pattern, "*?[") {
				fullMatch, fullErr := path.Match(strings.ToLower(pattern), strings.ToLower(fullID))
				idMatch, idErr := path.Match(strings.ToLower(pattern), strings.ToLower(model.ID))
				matched = (fullErr == nil && fullMatch) || (idErr == nil && idMatch)
			}
			if !matched {
				continue
			}
			key := provider + "\x00" + model.ID
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			scoped = append(scoped, model)
		}
	}
	return scoped
}

func rpcModelPatternWithoutThinking(pattern string) string {
	index := strings.LastIndex(pattern, ":")
	if index < 0 {
		return pattern
	}
	switch pattern[index+1:] {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return pattern[:index]
	default:
		return pattern
	}
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
		if entry.Base.ID == since {
			return entries[i+1:], nil
		}
	}
	return nil, fmt.Errorf("Entry not found: %s", since)
}

func rpcTree(root *codingagent.SessionTreeNode) []rpcSessionTreeNode {
	if root == nil {
		return nil
	}
	out := make([]rpcSessionTreeNode, len(root.Children))
	for i, child := range root.Children {
		out[i] = rpcTreeNode(child)
	}
	return out
}

func rpcTreeNode(node *codingagent.SessionTreeNode) rpcSessionTreeNode {
	out := rpcSessionTreeNode{
		Entry:          node.Entry,
		Children:       make([]rpcSessionTreeNode, len(node.Children)),
		Label:          node.Label,
		LabelTimestamp: node.LabelTimestamp,
	}
	for i, child := range node.Children {
		out.Children[i] = rpcTreeNode(child)
	}
	return out
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
