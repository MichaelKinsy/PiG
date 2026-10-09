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
// error. Recording the signal and returning normally, rather than exiting from
// the handler, keeps the deferred runtime teardown that upstream performs
// before its exit.
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
	// UnknownFlags carries extension CLI values into the bound runtime.
	UnknownFlags map[string]any
	// Commands carries the prompt templates, skills, resource provenance and
	// built-in llama.cpp command of the session. runPrintMode binds it to
	// the session's extension runner.
	Commands headlessCommandCatalog
	// ToolRegistryAllowed and ToolRegistryExcluded bound the tool registry
	// pi.getAllTools() reports (see toolRegistryFilters).
	ToolRegistryAllowed  map[string]struct{}
	ToolRegistryExcluded map[string]struct{}
	// SystemPromptSections rebuilds the session's system prompt with the
	// skills extensions discovered (upstream _rebuildSystemPrompt).
	SystemPromptSections func(skills []*codingagent.SkillDef) ai.OrderedSections
	// SystemPromptResources reports the resource-loader state behind the rebuilt prompt.
	SystemPromptResources func(skills []*codingagent.SkillDef) *coding.SystemPromptResources
	// Rebuild constructs these inputs again for a replacement Session's destination cwd, as Pi's createRuntime does on every replacement. Without it a replacement fails.
	Rebuild func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (printModeRuntime, error)
	// Invalidate makes the extension host reject later calls of a replaced Session's extension processes.
	Invalidate func(message string)
	// Release retires the extension host and services of a replaced Session.
	Release func(reason string)
	// Resources builds the command catalog from the build's resources, which a reload replaces.
	Resources func() headlessCommandCatalog
	// Reload is AgentSession.reload for this build's Session; rebind binds the replacement runner to the mode. Without it ctx.reload() fails.
	Reload headlessReload
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

// printSessionState is the mode state of one print-mode Session.
type printSessionState struct {
	Services              *coding.AgentSessionServices
	Bridge                *subprocess.UIBridge
	Session               coding.SessionStartOptions
	UnknownFlags          map[string]any
	Commands              headlessCommandCatalog
	ToolRegistryAllowed   map[string]struct{}
	ToolRegistryExcluded  map[string]struct{}
	SystemPromptSections  func(skills []*codingagent.SkillDef) ai.OrderedSections
	SystemPromptResources func(skills []*codingagent.SkillDef) *coding.SystemPromptResources
	Resources             func() headlessCommandCatalog
	Reload                headlessReload
}

func (h printModeRuntime) state() printSessionState {
	return printSessionState{
		Services: h.Services, Bridge: h.Bridge, Session: h.Session, UnknownFlags: h.UnknownFlags, Commands: h.Commands,
		ToolRegistryAllowed: h.ToolRegistryAllowed, ToolRegistryExcluded: h.ToolRegistryExcluded,
		SystemPromptSections: h.SystemPromptSections, SystemPromptResources: h.SystemPromptResources,
		Resources: h.Resources, Reload: h.Reload,
	}
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
	// stateFor returns the mode state of a live Session of the runtime; sessionName names the first one. startPrintMode supplies both.
	stateFor    func(*coding.Session) (printSessionState, bool)
	sessionName string
}

// startPrintMode creates the runtime print mode runs in from what main resolved (upstream main.ts createAgentSessionRuntime) and runs print mode
// in it (runPrintMode(runtimeHost, options)); the runtime is disposed by runPrintMode, the services factory here.
func startPrintMode(ctx context.Context, host printModeRuntime, opts printModeOptions) (int, error) {
	factory := newCLISessionFactory(ctx, host.inputs(), host.state(), func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (cliSessionInputs, printSessionState, error) {
		if host.Rebuild == nil {
			return cliSessionInputs{}, printSessionState{}, errors.New("print mode cannot rebuild a runtime for a replacement Session")
		}
		next, err := host.Rebuild(ctx, options)
		if err != nil {
			return cliSessionInputs{}, printSessionState{}, err
		}
		return next.inputs(), next.state(), nil
	})
	defer factory.Close()
	manager, err := coding.SessionManagerFor(host.Services, host.startOptions())
	if err != nil {
		return 1, fmt.Errorf("construct session: %w", err)
	}
	rt, err := coding.CreateAgentSessionRuntime(ctx, factory.Factory(), coding.CreateAgentSessionRuntimeOptions{
		CWD: host.Services.CWD(), AgentDir: host.Services.AgentDir(), SessionManager: manager,
	})
	if err != nil {
		return 1, fmt.Errorf("construct session: %w", err)
	}
	opts.stateFor, opts.sessionName = factory.StateFor, host.SessionName
	return runPrintMode(ctx, rt, opts)
}

// runPrintMode runs print (single-shot) mode: it sends the prompts and writes
// the result. Mirrors upstream modes/print-mode.ts, which serves both
// `pi -p "prompt"` (text: the final response only) and `pi --mode json
// "prompt"` (every session event as one JSON line, after the session header)
// from one function, so both modes build the same session from the same
// options. Normal completion drains output; signal termination disposes the
// runtime without waiting for stdout, then the caller exits with 128+signum.
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
	// The process context owns extension processes. Cancelling the run's own context stops prompting, but extensions still receive session_shutdown afterwards.
	processCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	intCh := make(chan os.Signal, 1)
	termSignals := []os.Signal{syscall.SIGTERM}
	if runtime.GOOS != "windows" {
		termSignals = append(termSignals, syscall.SIGHUP)
	}
	signal.Notify(intCh, termSignals...)
	defer signal.Stop(intCh)
	go func() {
		select {
		case sig := <-intCh:
			if sysSig, ok := sig.(syscall.Signal); ok {
				receivedTerminationSignal.Store(int32(sysSig))
			}
			tools.KillTrackedDetachedChildren()
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

	// Initial naming appends metadata without a runtime name-change notification.
	if opts.sessionName != "" {
		if _, err := sess.Inner().AppendSessionInfo(opts.sessionName); err != nil {
			return fmt.Errorf("set session name: %w", err)
		}
	}

	// Line 1 of JSON output is always the session header, matching the
	// session JSONL layout and upstream print-mode.ts, which writes
	// getHeader() before binding extensions.
	// JSON output goes through output-guard's ordered raw-stdout tail (writeRawStdout); a write failure exits 1 as the tail's catch does.
	var stdoutFailed atomic.Bool
	jsonOut := newStdoutQueue(opts.stdout, func(error) {
		stdoutFailed.Store(true)
		cancel()
	})
	if opts.Mode == "json" {
		if hdr := sess.Inner().GetHeader(); hdr.ID != "" {
			writeJSONLine(jsonOut, hdr)
		}
	}

	// Pi's subscriber converts, serializes and writes before Session persistence (print-mode.ts:108-111). The Events consumer only drains and acknowledges delivery barriers, so a consumer that stops reading cannot block the agent's emit. A conversion or serialization failure fails the run, as upstream's throwing toJsonEvent rejects prompt(). One subscription and one consumer run per Session; a replaced Session's channel closes with the Session.
	var convertMu sync.Mutex
	var convertErr error
	var consumers sync.WaitGroup
	var detachOutput []func()
	subscribe := func(session *coding.Session) {
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
			detachOutput = append(detachOutput, unsubscribe, backpressure)
			convertMu.Unlock()
		}
		consumers.Go(func() {
			for event := range session.Events() {
				coding.AcknowledgeEvent(event)
			}
		})
	}

	// state is the mode state of the current Session. Prompts read it at the moment they are sent.
	var state atomic.Pointer[printSessionState]
	var detachModelRegistry, stopRunSignal func()
	var publishedCommands atomic.Pointer[headlessCommandCatalog]
	// bind wires the Session to this mode as upstream rebindSession does: extension bindings, command actions, then the event subscription. It emits session_start for the Session's own reason, which is startup for the first Session.
	// A reload binds the same Session to a replacement runner: the resources are the reloaded ones, the event subscription stays, and the system prompt is rebuilt before session_start (agent-session.ts:3575-3625).
	bind := func(ctx context.Context, session *coding.Session, event extension.SessionStartEvent, reloading bool) error {
		st, ok := opts.stateFor(session)
		if !ok {
			return errors.New("print mode: replacement Session has no mode state")
		}
		if detachModelRegistry != nil {
			detachModelRegistry()
		}
		if stopRunSignal != nil {
			stopRunSignal()
		}
		detachModelRegistry = wireSubprocessModelRegistry(st.Bridge, session, st.Services)
		stopRunSignal = watchSessionRunSignal(st.Bridge, session)
		bindSessionReadActions(st.Bridge, current, st.Services.CWD(), session.Inner().GetSessionDir())
		bindSessionAppendEntry(st.Bridge, current)
		runner := session.ExtensionRunner()
		if runner != nil {
			runner.AddErrorListener(printExtensionErrorListener(opts.stderr))
			// Upstream print mode binds the session to its extensions
			// (session.bindExtensions), so sendUserMessage, isIdle, abort,
			// hasPendingMessages, and waitForIdle reach this session.
			runner.SetUIContext(nil, extensionMode)
			runner.BindCommandActions(rt.ExtensionCommandActions(session))
		}
		bindSessionExtensionActions(runner, st.Bridge, current, extension.ContextActions{
			ModelRegistry:    st.Services.Registry(),
			IsProjectTrusted: st.Services.SettingsManager().IsProjectTrusted,
			GetFlagValue:     func(name string) any { return st.UnknownFlags[name] },
		}, rt.ExtensionCommandActions)
		commands := st.Commands
		if reloading {
			commands = st.Resources()
		}
		commands.runner = runner
		commands.mode = string(extensionMode)
		if commands.notify == nil {
			// Print mode binds no UI; ctx.ui.notify does nothing.
			commands.notify = func(string, string) {}
		}
		// Extension host calls read the published copy of the catalog; this
		// goroutine alone changes commands.
		publishedCommands.Store(new(commands))
		if st.Bridge != nil {
			st.Bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
				return codingagent.ExtensionToolInfos(runner, st.ToolRegistryAllowed, st.ToolRegistryExcluded)
			})
			st.Bridge.SetHostAction("getCommands", func() []subprocess.CommandInfo {
				return publishedCommands.Load().slashCatalog().SubprocessCommands()
			})
		}
		if !reloading {
			subscribe(session)
		} else if st.SystemPromptSections != nil {
			session.SetSystemPromptSections(st.SystemPromptSections(commands.skills))
			if st.SystemPromptResources != nil {
				session.SetSystemPromptResources(*st.SystemPromptResources(commands.skills))
			}
		}
		// Drive the extension session lifecycle so extensions that initialize on
		// session_start (and clean up on session_shutdown) run in print mode too,
		// not only interactive.
		session.EmitSessionStartTransition(event.Reason, event.PreviousSessionFile)
		skillsChanged, resourceErr := commands.extendFromExtensions(ctx, runner, codingagent.ResourcesDiscoverReason(event.Reason))
		if resourceErr != nil {
			return resourceErr
		}
		if skillsChanged && st.SystemPromptSections != nil {
			session.SetSystemPromptSections(st.SystemPromptSections(commands.skills))
			if st.SystemPromptResources != nil {
				session.SetSystemPromptResources(*st.SystemPromptResources(commands.skills))
			}
		}
		publishedCommands.Store(new(commands))
		st.Commands = commands
		state.Store(&st)
		return nil
	}
	rt.SetRebindSession(func(ctx context.Context, session *coding.Session) error {
		return bind(ctx, session, session.StartEvent(), false)
	})
	// print-mode.ts:97-99: ctx.reload() runs session.reload() and then rebinds the Session's extensions.
	rt.SetReload(func(ctx context.Context, session *coding.Session) error {
		st, ok := opts.stateFor(session)
		if !ok || st.Reload == nil {
			return errors.New("print mode: this Session cannot reload")
		}
		return st.Reload(ctx, processCtx, session, func(ctx context.Context, event extension.SessionStartEvent) error {
			return bind(ctx, session, event, true)
		})
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
		// Ordered teardown: shutdown hooks run while the Session is still open,
		// then closing it ends the event channel and the consumer exits.
		_ = rt.Close()
		// Upstream's signal handler disposes the runtime and calls process.exit
		// without flushRawStdout. The process owns any blocked stdout write on
		// this path; waiting for it would make a full client pipe prevent exit.
		// Ordinary cancellation still drains output. Read convertErr only after
		// the consumers have joined, never while one may still be converting.
		joined := make(chan struct{})
		go func() { consumers.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-ctx.Done():
			if receivedTerminationSignal.Load() != 0 {
				return
			}
			<-joined
		}
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
			_, _ = fmt.Fprintln(opts.stderr, runErr.Error())
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
		st := state.Load()
		_, runErr = sendPrintPrompt(ctx, current(), st.Commands, message, images)
	}
	if opts.InitialMessage != "" {
		send(opts.InitialMessage, opts.InitialImages)
	}
	for _, message := range opts.Messages {
		send(message, nil)
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
	_, err = sess.SendContent(ctx, coding.BuildUserContent(commands.expandPrompt(text), images))
	return false, err
}

// writePrintModeResult writes text mode's result: the session's last message,
// when it is an assistant message. Mirrors upstream print-mode.ts, which reads
// session.state.messages at the end rather than the messages a prompt
// produced, so compaction and retries during the run cannot hide the answer,
// and earlier assistant messages of the run (tool-use narration) are not
// printed. An error or aborted message goes to stderr and fails the run.
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
		_, _ = fmt.Fprintln(stderr, errMsg)
		return errPrintModeHandled
	}
	for _, block := range last.Content {
		if text, ok := block.(ai.TextContent); ok {
			_, _ = io.WriteString(stdout, text.Text+"\n")
		}
	}
	return nil
}

// printExtensionErrorListener writes each extension error to w. Mirrors
// upstream print-mode.ts onError: console.error(`Extension error
// (${err.extensionPath}): ${err.error}`), in both text and json modes.
func printExtensionErrorListener(w io.Writer) extension.ErrorListener {
	var mu sync.Mutex
	return func(err *extension.ExtensionError) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintf(w, "Extension error (%s): %s\n", err.ExtensionPath, err.Error)
	}
}
