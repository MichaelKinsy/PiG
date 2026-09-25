package main

import (
	"context"
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
	"github.com/MichaelKinsy/PiG/internal/codingagent"

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
// Node's default handler, which exits 130. Callers therefore distinguish "a
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
	Services    *coding.Services
	Extensions  []extension.Extension
	Bridge      *subprocess.UIBridge
	Session     coding.SessionStartOptions
	ResumePath  string
	SessionName string
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
}

// runPrintMode runs print (single-shot) mode: it sends the prompts and writes
// the result. Mirrors upstream modes/print-mode.ts, which serves both
// `pi -p "prompt"` (text: the final response only) and `pi --mode json
// "prompt"` (every session event as one JSON line, after the session header)
// from one function, so both modes build the same session from the same
// options. Normal completion drains output; signal termination disposes the
// runtime without waiting for stdout, then the caller exits with 128+signum.
func runPrintMode(ctx context.Context, host printModeRuntime, opts printModeOptions) (err error) {
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
	// Print mode owns SIGINT: there's no interactive editor to deliver
	// Ctrl+C as a byte, so the signal is the only abort path. SIGTERM and
	// SIGHUP are owned here too so the exit code reports the signal:
	// upstream registers SIGTERM (plus SIGHUP off win32) and exits
	// 128+signum after disposing its runtime.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	intCh := make(chan os.Signal, 1)
	termSignals := []os.Signal{syscall.SIGINT, syscall.SIGTERM}
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

	rt, err := coding.NewRuntime(coding.RuntimeOptions{
		Services:      host.Services,
		NewExtensions: host.Extensions,
		AbortContext:  ctx,
	})
	if err != nil {
		return fmt.Errorf("construct runtime: %w", err)
	}
	defer func() { _ = rt.Close() }()
	if runner := rt.NewExtensionRunner(); runner != nil {
		runner.AddErrorListener(printExtensionErrorListener(opts.stderr))
	}

	var sess *coding.Session
	if host.ResumePath != "" {
		sess, err = rt.Open(host.ResumePath, host.Session)
	} else {
		sess, err = rt.New(host.Session)
	}
	if err != nil {
		return fmt.Errorf("construct session: %w", err)
	}
	defer func() { _ = sess.Close() }()
	detachModelRegistry := wireSubprocessModelRegistry(host.Bridge, sess, host.Services)
	defer detachModelRegistry()
	// Upstream print mode binds the session to its extensions
	// (session.bindExtensions), so sendUserMessage, isIdle, abort,
	// hasPendingMessages, and waitForIdle reach this session.
	extensionMode := extension.ModePrint
	if opts.Mode == "json" {
		extensionMode = extension.ModeJSON
	}
	bindSessionExtensionActions(rt.NewExtensionRunner(), host.Bridge, func() *coding.Session { return sess }, extension.ContextActions{
		ModelRegistry:    host.Services.Registry(),
		IsProjectTrusted: host.Services.SettingsManager().IsProjectTrusted,
		Mode:             extensionMode,
	})

	// Persist --name to session_info so the display name survives resume.
	// Mirrors upstream sessionManager.appendSessionInfo(name) (main.ts:580),
	// which runs when the session is created, before any mode starts.
	if host.SessionName != "" {
		if err := sess.SetSessionName(host.SessionName); err != nil {
			return fmt.Errorf("set session name: %w", err)
		}
	}

	// Line 1 of JSON output is always the session header, matching the
	// session JSONL layout and upstream print-mode.ts, which writes
	// getHeader() before binding extensions.
	if opts.Mode == "json" {
		if hdr := sess.Inner().Header(); hdr.ID != "" {
			writeJSONLine(opts.stdout, hdr)
		}
	}

	// Subscribe before prompting so no event of the run is missed. Every
	// event is consumed even when it is not written: forwardAgentEvents pushes
	// every agent event into the channel, and a consumer that stops reading
	// blocks the agent's emit and deadlocks the run. A JSON conversion failure
	// fails the run, as upstream's throwing toJsonEvent rejects prompt(), but
	// the loop keeps draining until Close ends the channel.
	var convertErr error
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for ev := range sess.Events() {
			if coding.AcknowledgeEvent(ev) || opts.Mode != "json" || convertErr != nil {
				continue
			}
			converted, err := opts.convertEvent(ev)
			if err != nil {
				convertErr = fmt.Errorf("convert session event: %w", err)
				cancel()
				continue
			}
			for _, out := range converted {
				writeJSONLine(opts.stdout, out)
			}
		}
	}()
	// Ordered teardown: shutdown hooks run while the session is still open,
	// then closing it ends the event channel and the consumer exits. A failed
	// run is reported like upstream's catch, console.error(error.message):
	// the error's own text on stderr, exit 1. A conversion failure is the
	// cause of the cancellation the prompt then reports, so it wins. A
	// termination signal stays quiet and reports its exit code instead.
	var runErr error
	defer func() {
		sess.EmitSessionShutdown("quit")
		_ = sess.Close()
		// Upstream's signal handler disposes the runtime and calls process.exit
		// without flushRawStdout. The process owns any blocked stdout write on
		// this path; waiting for it would make a full client pipe prevent exit.
		// Ordinary cancellation still drains output. Read convertErr only after
		// the consumer has joined, never while it may still be converting.
		select {
		case <-eventsDone:
		case <-ctx.Done():
			if receivedTerminationSignal.Load() != 0 {
				return
			}
			<-eventsDone
		}
		if convertErr != nil {
			runErr = convertErr
		}
		if runErr != nil && receivedTerminationSignal.Load() == 0 {
			_, _ = fmt.Fprintln(opts.stderr, runErr.Error())
			err = errPrintModeHandled
		}
	}()

	// Drive the extension session lifecycle so extensions that initialize on
	// session_start (and clean up on session_shutdown) run in print mode too,
	// not only interactive.
	sess.EmitSessionStart("startup")

	if opts.InitialMessage != "" {
		_, runErr = sendPrintPrompt(ctx, sess, opts.InitialMessage, opts.InitialImages)
	}
	for _, message := range opts.Messages {
		if runErr != nil {
			break
		}
		_, runErr = sendPrintPrompt(ctx, sess, message, nil)
	}
	// Upstream prompt() resolves only after every agent event's extension
	// handlers have run and every event was written; Send returns while the
	// session may still be dispatching the run's tail. Wait for that before
	// the deferred session_shutdown and host shutdown, or those handlers run
	// against a closed extension connection and JSON output is cut short.
	if err := sess.FlushEvents(ctx); err != nil && runErr == nil && ctx.Err() == nil {
		runErr = fmt.Errorf("flush session events: %w", err)
	}
	if runErr != nil {
		return nil // reported by the teardown above
	}
	if opts.Mode != "text" {
		return nil
	}
	return writePrintModeResult(sess.Messages(), opts.stdout, opts.stderr)
}

// sendPrintPrompt mirrors AgentSession.prompt for print and JSON mode's input
// boundary. Input handlers run once, before the transformed text and images
// enter the Session; RPC owns the equivalent dispatch in its command loop.
func sendPrintPrompt(ctx context.Context, sess *coding.Session, text string, images []ai.ImageContent) (handled bool, err error) {
	text, images, handled, err = sess.RunInputHandlers(ctx, text, images, extension.InputSourceUser, "")
	if err != nil || handled {
		return handled, err
	}
	_, err = sess.SendContent(ctx, coding.BuildUserContent(text, images))
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
