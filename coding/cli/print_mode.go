package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/handleryield"
	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"golang.org/x/term"
)

// ─── Print Mode ───────────────────────────────────────────────────────────────

// errPrintModeHandled signals that runPrintMode already wrote the error to
// stderr in the upstream format (no "error: " prefix). The caller should
// os.Exit(1) without re-printing.
var errPrintModeHandled = fmt.Errorf("print mode: error already written")

// signalExitError reports that print mode stopped because the process received
// a termination signal rather than because the run failed.
//
// Upstream's print mode disposes its runtime and then exits 128+signum
// (`process.exit(signal === "SIGHUP" ? 129 : 143)`), and leaves SIGINT to
// Node's default handler, which terminates by signal. Callers therefore distinguish "a
// timeout or supervisor killed the run" from "the run failed" by exit code, so
// pig must report the same codes and must not print an internal cancellation
// error. The handler disposes the runtime as upstream's does and exits; a signal
// that is not a syscall.Signal returns this error to the caller instead.
type signalExitError struct {
	signal syscall.Signal
}

func (e *signalExitError) Error() string {
	return fmt.Sprintf("terminated by signal %s", e.signal)
}

func (e *signalExitError) ExitCode() int {
	return 128 + int(e.signal)
}

// receivedTerminationSignal records the termination signal that stopped this
// process, if any. It is set by the process-wide SIGTERM handler in main and by
// print mode's own handler, so a signal arriving before print mode starts (for
// example while extensions are still loading) reports the same exit code as one
// arriving mid-run.
var receivedTerminationSignal atomic.Int32

// printModeRuntime is what main resolved for the print-mode session: the
// runtime services and extensions, and the options the session starts with.
type printModeRuntime struct {
	Services   *coding.AgentSessionServices
	Extensions []extension.Extension
	Bridge     *subprocess.UIBridge
	// Host is the extension host Bridge belongs to, if any.
	Host        *subprocess.Host
	Runtime     *extension.ExtensionRuntime
	Session     coding.SessionStartOptions
	ResumePath  string
	SessionName string
	// ToolRegistryAllowed and ToolRegistryExcluded bound the tool registry
	// pi.getAllTools() reports (see toolRegistryFilters).
	ToolRegistryAllowed  map[string]struct{}
	ToolRegistryExcluded map[string]struct{}
	// Rebuild constructs these inputs again for a replacement Session's destination cwd, as Pi's createRuntime does on every replacement. Without it a replacement fails.
	Rebuild func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (printModeRuntime, error)
	// Invalidate makes the extension host reject later calls of a replaced Session's extension processes.
	Invalidate func(message string)
	// Release retires the extension host and services of a replaced Session.
	Release func(reason string)
}

// startOptions returns the options the first Session starts with.
func (h printModeRuntime) startOptions() coding.SessionStartOptions {
	start := h.Session
	if start.SessionManager == nil {
		start.ResumePath = h.ResumePath
	}
	return start
}

func (h printModeRuntime) inputs() cliSessionInputs {
	return cliSessionInputs{Services: h.Services, Extensions: h.Extensions, Start: h.Session, Host: h.Host, Bridge: h.Bridge, Runtime: h.Runtime, Invalidate: h.Invalidate, Release: h.Release}
}

// printModeOptions mirrors upstream PrintModeOptions (print-mode.ts).
type printModeOptions struct {
	// Mode is "text" for the final response only, "json" for all events.
	Mode string
	// Messages are sent one by one after the initial message.
	Messages []string
	// InitialMessage is the first message to send (may contain @file content).
	InitialMessage string
	// InitialImages are attached to the initial message.
	InitialImages []ai.ImageContent

	// stdout and stderr default to the process's raw stdout and stderr.
	stdout io.Writer
	stderr io.Writer
	// convertEvent defaults to rpcAgentEvent, upstream's toJsonEvent.
	convertEvent func(agent.AgentEvent) ([]any, error)
	// toolRegistryAllowed and toolRegistryExcluded bound the tool registry pi.getAllTools() reports (see toolRegistryFilters); startPrintMode takes them from the command line.
	toolRegistryAllowed  map[string]struct{}
	toolRegistryExcluded map[string]struct{}
}

// startPrintMode creates the runtime print mode runs in from what main resolved (upstream main.ts createAgentSessionRuntime) and runs print mode
// in it (runPrintMode(runtimeHost, options)); the runtime is disposed by runPrintMode, the services factory here.
func startPrintMode(ctx context.Context, host printModeRuntime, opts printModeOptions) (int, error) {
	factory := newCLISessionFactory(ctx, host.inputs(), struct{}{}, func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (cliSessionInputs, struct{}, error) {
		if host.Rebuild == nil {
			return cliSessionInputs{}, struct{}{}, errors.New("print mode cannot rebuild a runtime for a replacement Session")
		}
		next, err := host.Rebuild(ctx, options)
		if err != nil {
			return cliSessionInputs{}, struct{}{}, err
		}
		return next.inputs(), struct{}{}, nil
	})
	defer factory.Close()
	manager, err := coding.SessionManagerFor(host.Services, host.startOptions())
	if err != nil {
		return 1, fmt.Errorf("construct session: %w", err)
	}
	// upstream: main.ts:707-714 names a new Session on its manager before createAgentSessionRuntime records the start model and thinking level (sdk.ts:453-458); the initial name emits no name-change notification.
	if host.SessionName != "" {
		if _, err := manager.AppendSessionInfo(host.SessionName); err != nil {
			return 1, fmt.Errorf("set session name: %w", err)
		}
	}
	rt, err := coding.CreateAgentSessionRuntime(ctx, factory.Factory(), coding.CreateAgentSessionRuntimeOptions{
		CWD: host.Services.CWD(), AgentDir: host.Services.AgentDir(), SessionManager: manager,
	})
	if err != nil {
		return 1, fmt.Errorf("construct session: %w", err)
	}
	opts.toolRegistryAllowed, opts.toolRegistryExcluded = host.ToolRegistryAllowed, host.ToolRegistryExcluded
	return runPrintMode(ctx, rt, opts)
}

// runPrintMode runs print (single-shot) mode: it sends the prompts and writes
// the result. Mirrors upstream modes/print-mode.ts, which serves both
// `pi -p "prompt"` (text: the final response only) and `pi --mode json
// "prompt"` (every session event as one JSON line, after the session header)
// from one function, so both modes build the same session from the same
// options. Normal completion drains output; a termination signal disposes the
// runtime without waiting for stdout and exits with 128+signum.
func runPrintMode(ctx context.Context, rt *coding.Runtime, opts printModeOptions) (exitCode int, err error) {
	// print-mode.ts:35,158,161: the run's exit code is 0, or 1 when the run failed or stopped on an error; err says what failed for a caller that has not reported it.
	defer func() {
		if err != nil {
			exitCode = 1
		}
	}()
	return 0, runPrintModeIn(ctx, rt, opts)
}

func runPrintModeIn(ctx context.Context, rt *coding.Runtime, opts printModeOptions) (err error) {
	// Take over os.Stdout in non-TTY contexts so any stdout
	// writes from tools / extensions / agent infra are redirected to
	// stderr instead of corrupting the print-mode output framing.
	if opts.stdout == nil {
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			if err := codingagent.TakeOverStdout(); err == nil {
				defer codingagent.RestoreStdout()
			}
		}
		opts.stdout = codingagent.RawStdoutWriter()
	}
	if opts.stderr == nil {
		opts.stderr = os.Stderr
	}
	if opts.convertEvent == nil {
		opts.convertEvent = rpcAgentEvent
	}
	// Pi owns SIGTERM and SIGHUP but leaves SIGINT to the process default action.
	// A signal-terminated process is distinguishable from numeric exit 130.
	// The process context owns extension processes (printHost). Cancelling the run's own context stops prompting, but extensions still receive session_shutdown afterwards.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// convertMu guards convertErr, the JSON writer's detach functions, and outputDetached, which a termination signal sets when it detaches them.
	var convertMu sync.Mutex
	var convertErr error
	var detachOutput []func()
	var outputDetached bool
	intCh := make(chan os.Signal, 1)
	termSignals := []os.Signal{syscall.SIGTERM}
	if runtime.GOOS != "windows" {
		termSignals = append(termSignals, syscall.SIGHUP)
	}
	// disposeFromSignal is print-mode.ts:58-61 disposeRuntime on a termination signal: the JSON writer unsubscribes first,
	// then runtimeHost.dispose emits session_shutdown and disposes the Session, which disconnects from the Agent before the
	// aborted run settles (agent-session.ts:1393-1414). Main's signal handler runs it before it cancels the root context
	// that owns the extension processes; this mode's handler runs it when main's has not.
	//
	// Pi runs that disposal on its one event loop: a run that is in flight goes on only when a session_shutdown handler
	// awaits a timer or I/O and the loop turns (a killed tool's exit is seen then, and its next turn starts). So the
	// signal holds the Agent before anything is killed, and the hold ends when a handler yields or the disposal ends.
	// A run that finishes meanwhile resolves Pi's prompt, which prints its result before the disposal goes on; the
	// disposal therefore waits for that result (resultMu) before it disposes the Session.
	var disposeOnce sync.Once
	var disposed atomic.Bool
	var resultMu sync.Mutex
	var releaseHoldMu sync.Mutex
	releaseHold := func() {}
	setTerminationPrelude(func() {
		release := rt.Session().HoldAgentEvents()
		releaseHoldMu.Lock()
		releaseHold = release
		releaseHoldMu.Unlock()
	})
	defer setTerminationPrelude(nil)
	release := func() {
		releaseHoldMu.Lock()
		fn := releaseHold
		releaseHoldMu.Unlock()
		fn()
	}
	disposeFromSignal := func() {
		disposeOnce.Do(func() {
			convertMu.Lock()
			detaches := detachOutput
			detachOutput = nil
			outputDetached = true
			convertMu.Unlock()
			for _, detach := range detaches {
				detach()
			}
			//portlint:allow ctxbackground Pi's SIGTERM/SIGHUP handler awaits runtimeHost.dispose() with no signal (print-mode.ts:57-60); this signal hook has no caller context
			rt.EmitQuitShutdownContext(handleryield.With(context.Background(), release))
			// Pi exits a microtask after dispose; the aborted run's later extension errors (a turn_end boundary without a persisted entry) are never printed.
			resultMu.Lock()
			disposed.Store(true)
			resultMu.Unlock()
			rt.Session().Dispose()
			release()
		})
	}
	setTerminationShutdownHook(disposeFromSignal)
	defer setTerminationShutdownHook(nil)
	signal.Notify(intCh, termSignals...)
	defer signal.Stop(intCh)
	go func() {
		select {
		case sig := <-intCh:
			sysSig, ok := noteTerminationSignal(sig)
			tools.KillTrackedDetachedChildren()
			disposeFromSignal()
			// Then process.exit(128+signum): the aborted run's remaining work (tool hooks, turn boundaries, extension errors) never runs. Extension processes end with the process, as the RPC signal exit ends them.
			if ok {
				if extensionHost, _ := rpcExtensionHost(rt.Session()); extensionHost != nil {
					extensionHost.TerminateProcesses()
				}
				exitProcess(128 + int(sysSig))
			}
			cancel()
		case <-ctx.Done():
		}
	}()
	defer func() {
		// A termination signal outranks whatever error the cancelled run
		// surfaced, so the caller reports the signal's exit code instead of a
		// generic failure.
		if sig := receivedTerminationSignal.Load(); sig != 0 {
			err = &signalExitError{signal: syscall.Signal(sig)}
		}
	}()

	extensionMode := extension.ModePrint
	if opts.Mode == "json" {
		extensionMode = extension.ModeJSON
	}
	// Runtime.Close emits session_shutdown for the current Session, closes it, and retires the host as upstream disposeRuntime does.
	defer func() { _ = rt.Close() }()
	sess := rt.Session()
	current := func() *coding.Session { return rt.Session() }

	// Line 1 of JSON output is always the session header, matching the
	// session JSONL layout and upstream print-mode.ts, which writes
	// getHeader() before binding extensions.
	// JSON output goes through output-guard's ordered raw-stdout tail (writeRawStdout); a write failure exits 1 as the tail's catch does.
	var stdoutFailed atomic.Bool
	jsonOut := newStdoutQueue(opts.stdout, func(error) {
		stdoutFailed.Store(true)
		cancel()
	})
	// The writer goroutine ends once the tail is empty; a termination signal does not wait for it.
	defer jsonOut.Close()
	if opts.Mode == "json" {
		if hdr := sess.Inner().GetHeader(); hdr.ID != "" {
			writeJSONLine(jsonOut, hdr)
		}
	}

	// Pi's subscriber converts, serializes and writes before Session persistence (print-mode.ts:108-111). Listeners are the only sinks: nothing reads Events, so the Session discards them, acknowledges delivery barriers itself, and executes each event on its producer's goroutine instead of handing it to an Events consumer. A conversion or serialization failure fails the run, as upstream's throwing toJsonEvent rejects prompt(). One subscription runs per Session.
	subscribe := func(session *coding.Session) {
		session.DiscardEvents()
		if opts.Mode == "json" {
			unsubscribe := session.Subscribe(func(event agent.AgentEvent) {
				convertMu.Lock()
				failed := convertErr != nil
				convertMu.Unlock()
				if failed {
					return
				}
				frames, eventErr := opts.convertEvent(event)
				var lines [][]byte
				if eventErr == nil {
					for _, frame := range frames {
						var line []byte
						line, eventErr = rpcclient.SerializeJsonLine(frame)
						if eventErr != nil {
							break
						}
						lines = append(lines, line)
					}
				}
				for _, line := range lines {
					_, _ = jsonOut.Write(line)
				}
				if eventErr != nil {
					convertMu.Lock()
					convertErr = fmt.Errorf("convert session event: %w", eventErr)
					convertMu.Unlock()
					cancel()
				}
			})
			// print-mode.ts:113-118 subscribes an Agent listener after the Session's own, awaiting every pending stdout write before the next Agent event.
			backpressure := subscribeStdoutBackpressure(session.Agent(), func() { jsonOut.Wait(ctx) })
			convertMu.Lock()
			detached := outputDetached
			if !detached {
				detachOutput = append(detachOutput, unsubscribe, backpressure)
			}
			convertMu.Unlock()
			if detached {
				unsubscribe()
				backpressure()
			}
		}
	}

	// currentCommands is the command catalog of the current Session. Prompts read it at the moment they are sent.
	var currentCommands atomic.Pointer[headlessCommandCatalog]
	var detachModelRegistry, stopRunSignal func()
	var publishedCommands atomic.Pointer[headlessCommandCatalog]
	// bind wires the Session to this mode as upstream rebindSession does: extension bindings, command actions, then the event subscription. It emits session_start for the Session's own reason, which is startup for the first Session.
	// A reload binds the same Session to a replacement runner: the Session has reloaded its resources and rebuilt its system prompt, and the event subscription stays (agent-session.ts:3575-3625).
	bind := func(ctx context.Context, session *coding.Session, event extension.SessionStartEvent, reloading bool) error {
		services := session.Services()
		_, bridge := rpcExtensionHost(session)
		if detachModelRegistry != nil {
			detachModelRegistry()
		}
		if stopRunSignal != nil {
			stopRunSignal()
		}
		detachModelRegistry = wireSubprocessModelRegistry(bridge, session, services)
		stopRunSignal = watchSessionRunSignal(bridge, session)
		bindSessionReadActions(bridge, current, session.CWD(), session.Inner().GetSessionDir())
		var emitEntryAppended func(RPCEntryAppendedEvent)
		if opts.Mode == "json" {
			emitEntryAppended = func(event RPCEntryAppendedEvent) { writeJSONLine(jsonOut, event) }
		}
		bindSessionAppendEntry(bridge, current, emitEntryAppended)
		runner := session.ExtensionRunner()
		if runner != nil {
			runner.AddErrorListener(printExtensionErrorListener(disposedWriter{w: opts.stderr, disposed: &disposed}))
			// Upstream print mode binds the session to its extensions
			// (session.bindExtensions), so sendUserMessage, isIdle, abort,
			// hasPendingMessages, and waitForIdle reach this session.
			runner.SetUIContext(nil, extensionMode)
			runner.BindCommandActions(rt.ExtensionCommandActions(session))
		}
		flagValues := services.ExtensionFlagValues()
		bindSessionExtensionActions(runner, bridge, current, extension.ContextActions{
			ModelRegistry: services.Registry(),
			// upstream: agent-session.ts:3440 isProjectTrusted reads the Session's settings manager.
			IsProjectTrusted: services.SettingsManager().IsProjectTrusted,
			GetFlagValue:     func(name string) any { return flagValues[name] },
		}, nil, rt.ExtensionCommandActions)
		commands := headlessCommandCatalog{
			runner: runner, mode: string(extensionMode),
			promptTemplates: session.PromptTemplates(), skills: session.ResourceLoader().GetSkills().Skills,
			cwd: session.CWD(), agentDir: services.AgentDir(),
			// Print mode binds no UI; ctx.ui.notify does nothing.
			notify: func(string, string) {},
		}
		// Extension host calls read the published copy of the catalog; this
		// goroutine alone changes commands.
		publishedCommands.Store(new(commands))
		if bridge != nil {
			bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
				return codingagent.ExtensionToolInfos(runner, opts.toolRegistryAllowed, opts.toolRegistryExcluded)
			})
			bridge.SetHostAction("getCommands", func() []subprocess.CommandInfo {
				return publishedCommands.Load().slashCatalog().SubprocessCommands()
			})
			// A state push encodes the catalog only when the published catalog or the runner's commands changed.
			var commandsVersion codingagent.CommandCatalogVersion
			bridge.SetHostAction("getCommandsVersion", func() uint64 {
				catalog := publishedCommands.Load()
				return commandsVersion.Version(catalog, catalog.slashCatalog().Runner)
			})
		}
		if !reloading {
			// The system prompt names the docs bundle, and a tool call is how the model reads it.
			session.Agent().AddBeforeToolCallHook(writeDocsBundleBeforeToolCall)
			subscribe(session)
		}
		// Drive the extension session lifecycle so extensions that initialize on
		// session_start (and clean up on session_shutdown) run in print mode too,
		// not only interactive. The Session emits session_start, extends its resource loader with what resources_discover returns and rebuilds its system prompt from the loader (agent-session.ts bindExtensions).
		if err := session.EmitSessionStartTransition(event.Reason, event.PreviousSessionFile); err != nil {
			return err
		}
		commands.promptTemplates = session.PromptTemplates()
		commands.skills = session.ResourceLoader().GetSkills().Skills
		publishedCommands.Store(new(commands))
		currentCommands.Store(&commands)
		return nil
	}
	rt.SetRebindSession(func(ctx context.Context, session *coding.Session) error {
		return bind(ctx, session, session.StartEvent(), false)
	})
	// print-mode.ts:97-99: ctx.reload() runs session.reload() and then rebinds the Session's extensions.
	rt.SetReload(func(ctx context.Context, session *coding.Session) error {
		previous := session.ExtensionRunner()
		if err := session.Reload(ctx); err != nil {
			// The old runner went stale after session_shutdown; its extension processes reject later calls with its message too.
			if extensionHost := session.ExtensionHost(); extensionHost != nil && previous != nil && session.ExtensionRunner() == previous {
				extensionHost.Invalidate(previous.StaleMessage())
			}
			return err
		}
		return bind(ctx, session, extension.SessionStartEvent{Type: codingagent.EventSessionStart, Reason: "reload"}, true)
	})
	defer func() {
		if detachModelRegistry != nil {
			detachModelRegistry()
		}
		if stopRunSignal != nil {
			stopRunSignal()
		}
	}()

	// A failed run is reported like upstream's catch, console.error(error.message):
	// the error's own text on stderr, exit 1. A conversion failure is the
	// cause of the cancellation the prompt then reports, so it wins. A
	// termination signal stays quiet and reports its exit code instead.
	var runErr error
	defer func() {
		// Pi's finally awaits disposeRuntime, which does nothing once a signal handler has started it (print-mode.ts:42-48,
		// 162-167); the process then lives until that disposal settles and the handler exits. Join the signal's disposal
		// first, so closing the runtime cannot retire the extensions under its in-flight session_shutdown.
		if receivedTerminationSignal.Load() != 0 {
			disposeFromSignal()
		}
		// Ordered teardown: shutdown hooks run while the Session is still open,
		// then closing it ends the Session's event executor.
		_ = rt.Close()
		// Upstream's signal handler disposes the runtime and calls process.exit
		// without flushRawStdout. The process owns any blocked stdout write on
		// this path; waiting for it would make a full client pipe prevent exit.
		// Ordinary cancellation still drains output.
		convertMu.Lock()
		for _, detach := range detachOutput {
			detach()
		}
		if convertErr != nil {
			runErr = convertErr
		}
		convertMu.Unlock()
		// A normal run flushes the raw-stdout tail (flushRawStdout); a termination signal exits without waiting on a blocked reader.
		if receivedTerminationSignal.Load() == 0 {
			jsonOut.Wait(context.Background())
		}
		if stdoutFailed.Load() {
			err = errPrintModeHandled
			return
		}
		if runErr != nil && receivedTerminationSignal.Load() == 0 {
			_, _ = fmt.Fprintln(opts.stderr, jsstring.StreamText(runErr.Error()))
			err = errPrintModeHandled
		}
	}()

	if err := bind(ctx, sess, sess.StartEvent(), false); err != nil {
		runErr = err
		return nil // reported by the teardown above
	}

	// A termination signal ends the run where it is: upstream's handler
	// disposes the runtime and exits, so no later prompt starts.
	send := func(message string, images []ai.ImageContent) {
		if runErr != nil || ctx.Err() != nil {
			return
		}
		_, runErr = sendPrintPrompt(ctx, current(), *currentCommands.Load(), message, images)
	}
	if opts.InitialMessage != "" {
		send(opts.InitialMessage, opts.InitialImages)
	}
	for _, message := range opts.Messages {
		send(message, nil)
	}
	// A run that a signal's disposal cut short never resolves Pi's prompt; one that ended first prints its result before
	// the disposal goes on.
	resultMu.Lock()
	defer resultMu.Unlock()
	if disposed.Load() {
		return nil
	}
	// Upstream prompt() resolves only after every agent event's extension
	// handlers have run and every event was written; Send returns while the
	// session may still be dispatching the run's tail. Wait for that before
	// the deferred session_shutdown and host shutdown, or those handlers run
	// against a closed extension connection and JSON output is cut short.
	if err := current().FlushEvents(ctx); err != nil && runErr == nil && ctx.Err() == nil {
		runErr = fmt.Errorf("flush session events: %w", err)
	}
	if runErr != nil {
		return nil // reported by the teardown above
	}
	if opts.Mode != "text" {
		return nil
	}
	return writePrintModeResult(current().Messages(), opts.stdout, opts.stderr)
}

// sendPrintPrompt mirrors AgentSession.prompt for print and JSON mode's input
// boundary: an extension command runs in place of the prompt
// (_tryExecuteExtensionCommand), otherwise input handlers run once and skill
// commands and prompt templates expand the transformed text before it enters
// the Session. RPC owns the equivalent dispatch in its command loop.
func sendPrintPrompt(ctx context.Context, sess *coding.Session, commands headlessCommandCatalog, text string, images []ai.ImageContent) (handled bool, err error) {
	if name, args, ok := commands.extensionCommand(text); ok {
		// A failing handler is reported through the extension error
		// listener and still counts as handled, as upstream emitError does.
		commands.executeCommand(ctx, name, args)
		return true, nil
	}
	text, images, handled, err = sess.RunInputHandlers(ctx, text, images, extension.InputSourceUser, "")
	if err != nil || handled {
		return handled, err
	}
	// An absent startup selection becomes Pi's Agent DEFAULT_MODEL. Its unknown provider fails credential preflight, not the lower-level SendContent model guard; commands and handled input above do not need credentials.
	if model := sess.Model(); model == nil || model.Provider == nil && model.ProviderMeta.ProviderID == "unknown" {
		return false, errors.New(codingagent.FormatNoAPIKeyFoundMessage("unknown"))
	}
	// AgentSession.prompt (agent-session.ts:2033-2052) rejects a prompt whose provider has no usable auth before it reaches the provider.
	if err := sess.ValidatePromptModelAuth(ctx); err != nil {
		return false, err
	}
	_, err = sess.SendContent(ctx, coding.BuildUserContent(commands.expandPrompt(text), images))
	return false, err
}

// writePrintModeResult writes text mode's result: the session's last message,
// when it is an assistant message. Mirrors upstream print-mode.ts, which reads
// session.state.messages at the end rather than the messages a prompt
// produced, so compaction and retries during the run cannot hide the answer,
// and earlier assistant messages of the run (tool-use narration) are not
// printed. An error or aborted message goes to stderr and fails the run, and so does a message cut off at the output token limit once its text is written.
func writePrintModeResult(messages []agent.AgentMessage, stdout, stderr io.Writer) error {
	if len(messages) == 0 {
		return nil
	}
	last := messages[len(messages)-1].Assistant
	if last == nil {
		return nil
	}
	if last.StopReason == ai.StopReasonError || last.StopReason == ai.StopReasonAborted {
		errMsg := last.ErrorMessage
		if errMsg == "" {
			errMsg = "Request " + string(last.StopReason)
		}
		// console.error and writeRawStdout send a lone surrogate, which PiG strings carry as WTF-8, as U+FFFD.
		_, _ = fmt.Fprintln(stderr, jsstring.StreamText(errMsg))
		return errPrintModeHandled
	}
	for _, block := range last.Content {
		if text, ok := block.(ai.TextContent); ok {
			_, _ = io.WriteString(stdout, jsstring.StreamText(text.Text+"\n"))
		}
	}
	// A response cut off at the output token limit keeps its partial text on stdout, warns and fails the run (print-mode.ts: stopReason === "length").
	if last.StopReason == ai.StopReasonLength {
		_, _ = fmt.Fprintln(stderr, "Response was truncated: the model reached its output token limit.")
		return errPrintModeHandled
	}
	return nil
}

// printExtensionErrorListener writes each extension error to w. Mirrors
// upstream print-mode.ts onError: console.error(`Extension error
// (${err.extensionPath}): ${err.error}`), in both text and json modes.
// disposedWriter drops writes once a termination signal has disposed the runtime: upstream has exited by then.
type disposedWriter struct {
	w        io.Writer
	disposed *atomic.Bool
}

func (d disposedWriter) Write(p []byte) (int, error) {
	if d.disposed.Load() {
		return len(p), nil
	}
	return d.w.Write(p)
}

func printExtensionErrorListener(w io.Writer) extension.ErrorListener {
	var mu sync.Mutex
	return func(err *extension.ExtensionError) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = io.WriteString(w, jsstring.StreamText(fmt.Sprintf("Extension error (%s): %s\n", err.ExtensionPath, err.Error)))
	}
}
