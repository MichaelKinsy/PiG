package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// startRPCShutdownFixture starts RPC mode with testdata/rpc-shutdown.mjs and
// returns the process and the file the extension records its events in.
// rpcShutdownSecondExtension in extraEnv also loads rpc-shutdown-second.mjs.
const rpcShutdownSecondExtension = "RPC_SHUTDOWN_SECOND=1"

func startRPCShutdownFixture(t *testing.T, extraEnv ...string) (*rpcProcess, string) {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	report := filepath.Join(t.TempDir(), "report.txt")
	env := append([]string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "RPC_SHUTDOWN_REPORT=" + report}, extraEnv...)
	args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", fixture}
	if slices.Contains(extraEnv, rpcShutdownSecondExtension) {
		second, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown-second.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "-e", second)
	}
	return startRPCProcessAt(t, t.TempDir(), env, args...), report
}

// startPiRPCShutdownFixture starts the pinned Pi 0.87.1 in RPC mode with the
// same fixture and Pi's test-faux provider.
func startPiRPCShutdownFixture(t *testing.T, extraEnv ...string) (*rpcProcess, string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	piRoot := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != "0.87.1" {
		t.Fatalf("Pi version = %q, %v", pkg.Version, err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	report := filepath.Join(t.TempDir(), "report.txt")
	env := append([]string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "RPC_SHUTDOWN_REPORT=" + report}, extraEnv...)
	args := []string{filepath.Join(piRoot, "dist", "cli.js"), "--mode", "rpc", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1",
		"-e", filepath.Join(root, "test", "parity", "testdata", "test-faux-provider.ts"), "-e", filepath.Join(root, "cmd", "pig", "testdata", "rpc-shutdown.mjs")}
	if slices.Contains(extraEnv, rpcShutdownSecondExtension) {
		args = append(args, "-e", filepath.Join(root, "cmd", "pig", "testdata", "rpc-shutdown-second.mjs"))
	}
	return startJSONLProcessAt(t, t.TempDir(), env, node, args...), report
}

// rpcShutdownImplementations start the fixture under pig and under the pinned
// Pi, so a test can compare their observable output.
var rpcShutdownImplementations = []struct {
	name  string
	start func(t *testing.T, extraEnv ...string) (*rpcProcess, string)
}{
	{"pig", startRPCShutdownFixture},
	{"pi", startPiRPCShutdownFixture},
}

// startJSONLProcessAt starts command with a JSONL stdout reader, as
// startRPCProcessAt does for the pig binary.
func startJSONLProcessAt(t *testing.T, cwd string, env []string, command string, args ...string) *rpcProcess {
	t.Helper()
	cmd := exec.Command(command, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &rpcProcess{t: t, cmd: cmd, stdin: stdin, stderr: &lockedBuffer{}, records: make(chan rpcRecord, 64), budget: testbudget.Wait(t)}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p.scanOutput(stdout)
	t.Cleanup(func() {
		close(p.stopOutput)
		_ = stdout.Close()
		<-p.outputDone
		if p.exited {
			return
		}
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return p
}

// startInFlightPrompt sends a prompt whose extension tool stays in flight and
// returns once the tool has reported its update.
func startInFlightPrompt(p *rpcProcess) {
	p.t.Helper()
	p.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": "Run: extension echo hello"})
	p.await("tool update", func(r rpcRecord) bool { return r["type"] == "tool_execution_update" })
}

// awaitReady waits until the process answers get_state, which RPC mode reads
// only after its extensions loaded.
func awaitReady(p *rpcProcess) {
	p.t.Helper()
	p.sendJSON(map[string]any{"id": "ready", "type": "get_state"})
	p.await("ready", func(r rpcRecord) bool { return isSuccessResponse(r, "ready") })
}

func readRPCShutdownReport(t *testing.T, report string) []string {
	t.Helper()
	data, err := os.ReadFile(report)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// drainRPCOutput returns every stdout record until stdout closes. Request IDs
// of extension UI requests are random and are dropped.
func drainRPCOutput(p *rpcProcess) []rpcRecord {
	p.t.Helper()
	var records []rpcRecord
	timer := time.NewTimer(p.budget)
	defer timer.Stop()
	for {
		select {
		case r, ok := <-p.records:
			if !ok {
				return records
			}
			if r["type"] == "extension_ui_request" {
				delete(r, "id")
			}
			records = append(records, r)
		case <-timer.C:
			p.t.Fatalf("RPC output did not close\n%s", p.stderr.String())
		}
	}
}

// endInputMidPrompt ends stdin while the fixture's tool is in flight and
// returns every stdout record written after that, and the extension's events.
func endInputMidPrompt(p *rpcProcess, report string) ([]rpcRecord, []string) {
	p.t.Helper()
	startInFlightPrompt(p)
	p.closeInput()
	afterEOF := drainRPCOutput(p)
	p.waitForExit("with a prompt in flight")
	return afterEOF, readRPCShutdownReport(p.t, report)
}

var rpcShutdownStartedNotify = rpcRecord{"type": "extension_ui_request", "method": "notify", "message": "session_shutdown started", "notifyType": "info"}

// Pi 0.87.1 when stdin ends while a tool call is in flight:
//
//  1. rpc-mode.ts:804-807 runs shutdown(): 733-737 remove the signal handlers
//     and unsubscribe the stdout forwarder of Session events, 738 awaits
//     runtimeHost.dispose().
//  2. agent-session-runtime.ts:406-413 emits session_shutdown, then calls
//     AgentSession.dispose (agent-session.ts:1170-1191): agent.abort(),
//     runner.invalidate() (it guards ctx, not dispatch) and
//     _disconnectFromAgent(), which unsubscribes _handleAgentEvent. The
//     aborted run's message, tool and agent_end events therefore reach
//     neither extensions nor persistence nor stdout.
//  3. The aborted tool resolves. The Agent's finishTurn hook
//     (_installAgentBoundaryHooks, agent-session.ts:674-683) is not a
//     subscription, so agent-loop.ts:285 still dispatches the turn_end
//     boundary: the tool-call assistant message was persisted before stdin
//     ended, so turn_end reaches the extension.
//  4. The tool batch did not terminate, so the loop streams the next
//     response with the aborted signal and finishes that aborted turn
//     (agent-loop.ts:244-252). Its message_end never reached the
//     disconnected _handleAgentEvent, so _dispatchTurnEndBoundary cannot
//     resolve its entry and emits "turn_end could not resolve the persisted
//     assistant entry ID" (agent-session.ts:642-650), which rpc-mode.ts:348
//     writes to stdout as extension_error.
//  5. The run then settles: _emitAgentSettled (agent-session.ts:870-876)
//     emits agent_settled through the extension runner; its listeners were
//     cleared. shutdown() awaits flushRawStdout and exits 0.
var (
	rpcMidPromptInputEndExtensionEvents = []string{
		"agent_start", "turn_start",
		"message_start", "message_end", // system
		"message_start", "message_end", // user
		"message_start", "message_end", // tool-call assistant
		"tool_execution_start", "tool_running",
		"session_shutdown", "turn_end", "agent_settled",
	}
	// The fixture's session_shutdown handler notifies first.
	rpcMidPromptInputEndStdout = []rpcRecord{
		rpcShutdownStartedNotify,
		{"type": "extension_error", "extensionPath": "<boundary>", "event": "turn_end", "error": "turn_end could not resolve the persisted assistant entry ID"},
	}
)

// After stdin ends mid-prompt, pig writes only the session_shutdown
// notification and the turn_end boundary's extension_error to stdout, and
// delivers session_shutdown, the tool-call turn's turn_end and agent_settled
// to the extension, in Pi's order.
func TestRPCInputEndMidPromptDeliversPiShutdownSequence(t *testing.T) {
	p, report := startRPCShutdownFixture(t)
	afterEOF, events := endInputMidPrompt(p, report)
	if !reflect.DeepEqual(afterEOF, rpcMidPromptInputEndStdout) {
		t.Fatalf("stdout after stdin ended = %v, want %v", afterEOF, rpcMidPromptInputEndStdout)
	}
	if !slices.Equal(events, rpcMidPromptInputEndExtensionEvents) {
		t.Fatalf("extension events = %v, want %v", events, rpcMidPromptInputEndExtensionEvents)
	}
}

// The pinned Pi produces the sequence the previous test pins, from the same
// fixture.
func TestRPCInputEndMidPromptSequenceComparedWithPi(t *testing.T) {
	p, report := startPiRPCShutdownFixture(t)
	afterEOF, events := endInputMidPrompt(p, report)
	if !reflect.DeepEqual(afterEOF, rpcMidPromptInputEndStdout) || !slices.Equal(events, rpcMidPromptInputEndExtensionEvents) {
		t.Fatalf("Pi stdout after stdin ended = %v, extension events = %v; want %v, %v", afterEOF, events, rpcMidPromptInputEndStdout, rpcMidPromptInputEndExtensionEvents)
	}
}

// Pi reads stdin's end one event-loop iteration after the line that starts a
// command, and then shutdown() exits within microtasks and one tick
// (rpc-mode.ts:728-744, output-guard.ts:105-108). A command answers only if it
// settles inside that window: microtasks, ticks and the check phase of the
// line's own iteration (measured with Pi 0.87.1: sync, micro, nextTick and
// immediate answer 10 of 10; a nested setImmediate, fs I/O, a child process, a
// timer and a dialog never do; a single fs.stat is not stable in Pi). A command that settles while a
// session_shutdown handler that awaits I/O keeps Pi alive does answer. Each row
// runs with the fixture's default session_shutdown handler, which sends a
// notification, and without it. setTimeout(0) is racy in Pi (about 3 runs in
// 10), so it has no row.
func TestRPCInputEndAfterExtensionCommandComparedWithPi(t *testing.T) {
	cases := []struct {
		command  string
		response bool
		dialog   bool
		// afterExit marks a command that runs after Pi's exit. Pi records nothing
		// for it. Pig's runtime is a separate process that its host stops after the
		// exit decision, so the continuation may record its event before then.
		// pig divergence (D85): the afterExit tolerance is D85's first difference; the Pi rows stay strict.
		afterExit bool
	}{
		{command: "micro", response: true},
		{command: "sync", response: true},
		{command: "nexttick", response: true},
		{command: "immediate", response: true},
		{command: "microimmediate", response: true},
		{command: "awaits5immediate", response: true},
		{command: "immediate2", afterExit: true},
		{command: "timer", afterExit: true},
		{command: "fspromises", afterExit: true},
		{command: "fscallback", afterExit: true},
		{command: "exec", afterExit: true},
		{command: "execloop", afterExit: true},
		{command: "ask", dialog: true},
	}
	starts := append([]struct {
		name  string
		start func(t *testing.T, extraEnv ...string) (*rpcProcess, string)
	}{}, rpcShutdownImplementations...)
	starts = append(starts, struct {
		name  string
		start func(t *testing.T, extraEnv ...string) (*rpcProcess, string)
	}{"pig-with-sibling", startRPCShutdownFixtureWithSibling})
	for _, impl := range starts {
		for _, handler := range []bool{true, false} {
			for _, tc := range cases {
				name := impl.name + "/" + tc.command
				var env []string
				if !handler {
					// RPC_SHUTDOWN_NO_HANDLER leaves shutdown() without the default session_shutdown handler.
					env = append(env, "RPC_SHUTDOWN_NO_HANDLER=1")
					name += "/no-handler"
				}
				t.Run(name, func(t *testing.T) {
					p, report := impl.start(t, env...)
					p.sendJSON(map[string]any{"id": "c", "type": "prompt", "message": "/" + tc.command})
					p.closeInput()
					afterEOF := drainRPCOutput(p)
					p.waitForExit("after the last command line")
					var want []rpcRecord
					if tc.dialog {
						want = append(want, rpcRecord{"type": "extension_ui_request", "method": "select", "title": "Ask", "options": []any{"yes"}})
					}
					if tc.response {
						want = append(want, rpcRecord{"id": "c", "type": "response", "command": "prompt", "success": true})
					}
					if handler {
						want = append(want, rpcShutdownStartedNotify)
					}
					if !reflect.DeepEqual(afterEOF, want) && (len(afterEOF) != 0 || len(want) != 0) {
						t.Fatalf("stdout after stdin ended = %v, want %v", afterEOF, want)
					}
					events := readRPCShutdownReport(t, report)
					slices.Sort(events)
					var wantEvents []string
					if tc.response {
						wantEvents = append(wantEvents, tc.command)
					}
					if handler {
						wantEvents = append(wantEvents, "session_shutdown")
					}
					slices.Sort(wantEvents)
					if tc.afterExit && strings.HasPrefix(impl.name, "pig") {
						events = slices.DeleteFunc(events, func(event string) bool { return event == tc.command })
					}
					if !slices.Equal(events, wantEvents) {
						t.Fatalf("extension events = %v, want %v", events, wantEvents)
					}
				})
			}
		}
	}
}

// A session_shutdown handler that awaits a timer keeps Pi's process alive, so a
// command that settles through a timer or file I/O meanwhile answers (measured
// with Pi 0.87.1: 4 of 4). The notification of the handler comes first.
func TestRPCInputEndCommandSettlesDuringSlowShutdownHandler(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		for _, command := range []string{"timer50", "fspromises"} {
			t.Run(impl.name+"/"+command, func(t *testing.T) {
				p, report := impl.start(t, "RPC_SHUTDOWN_SLOW=200")
				p.sendJSON(map[string]any{"id": "c", "type": "prompt", "message": "/" + command})
				p.closeInput()
				afterEOF := drainRPCOutput(p)
				p.waitForExit("after the slow shutdown handler")
				want := []rpcRecord{rpcShutdownStartedNotify, {"id": "c", "type": "response", "command": "prompt", "success": true}}
				if !reflect.DeepEqual(afterEOF, want) {
					t.Fatalf("stdout after stdin ended = %v, want %v", afterEOF, want)
				}
				events := readRPCShutdownReport(t, report)
				slices.Sort(events)
				wantEvents := []string{"session_shutdown", command}
				slices.Sort(wantEvents)
				if !slices.Equal(events, wantEvents) {
					t.Fatalf("extension events = %v, want %v", events, wantEvents)
				}
			})
		}
	}
}

// Pi has one stdout write callback for the whole process. A command of one
// extension that waits on a timer does not release the flush while another
// extension's command still completes through microtasks, so the second
// response is written and the first never is. Both extensions share one Node
// runtime process and have one connection each.
func TestRPCInputEndJoinsEachExtensionCommand(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_NO_HANDLER=1", rpcShutdownSecondExtension)
			p.sendJSON(map[string]any{"id": "slow", "type": "prompt", "message": "/timer"})
			p.sendJSON(map[string]any{"id": "fast", "type": "prompt", "message": "/micro2"})
			p.closeInput()
			afterEOF := drainRPCOutput(p)
			p.waitForExit("after both commands")
			want := []rpcRecord{{"id": "fast", "type": "response", "command": "prompt", "success": true}}
			if !reflect.DeepEqual(afterEOF, want) {
				t.Fatalf("stdout after stdin ended = %v, want %v", afterEOF, want)
			}
			events := readRPCShutdownReport(t, report)
			if !slices.Contains(events, "micro2") {
				t.Fatalf("extension events = %v, want micro2", events)
			}
		})
	}
}

// startRPCShutdownFixtureWithSibling loads a second Node extension beside the
// fixture, so both share one runtime process.
func startRPCShutdownFixtureWithSibling(t *testing.T, extraEnv ...string) (*rpcProcess, string) {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(t.TempDir(), "sibling.mjs")
	if err := os.WriteFile(sibling, []byte("export default function () {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	report := filepath.Join(t.TempDir(), "report.txt")
	env := append([]string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "RPC_SHUTDOWN_REPORT=" + report}, extraEnv...)
	return startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", sibling, "-e", fixture), report
}

// Pi 0.87.1: when stdin ends and a session_shutdown handler waits on a
// promise that never settles, nothing keeps Node's event loop alive, so the
// process exits 0 without finishing the dispose (rpc-mode.ts:738 never
// resumes). Pig's Node runtime reports that drain, and pig exits 0 the same.
func TestRPCInputEndWithPendingShutdownPromiseExitsZero(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_BLOCK=1")
			awaitReady(p)
			p.closeInput()
			afterEOF := drainRPCOutput(p)
			p.waitForExit("with a session_shutdown handler that never settles")
			if want := []rpcRecord{rpcShutdownStartedNotify}; !reflect.DeepEqual(afterEOF, want) {
				t.Fatalf("stdout after stdin ended = %v, want %v", afterEOF, want)
			}
			if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"session_shutdown"}) {
				t.Fatalf("extension events = %v, want [session_shutdown]", events)
			}
		})
	}
}

// Pi 0.87.1: a dialog that the last command line opens stays pending when
// stdin ends. Its handler never resumes and the prompt never responds; the
// select request and the session_shutdown notification reach stdout and the
// process exits 0.
func TestRPCInputEndLeavesLastLineDialogPending(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t)
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "ask", "type": "prompt", "message": "/ask"})
			p.closeInput()
			afterEOF := drainRPCOutput(p)
			p.waitForExit("with the last line's dialog pending")
			want := []rpcRecord{{"type": "extension_ui_request", "method": "select", "title": "Ask", "options": []any{"yes"}}, rpcShutdownStartedNotify}
			if !reflect.DeepEqual(afterEOF, want) {
				t.Fatalf("stdout after the last line = %v, want %v", afterEOF, want)
			}
			if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"session_shutdown"}) {
				t.Fatalf("extension events = %v, want [session_shutdown]: the dialog's handler must not resume", events)
			}
		})
	}
}

// Pi 0.87.1: ctx.shutdown() sets rpc-mode.ts shutdownRequested (346), which
// the command loop checks after the command (748-749); shutdown() then
// disposes the runtime and exits 0 although stdin is still open. The
// The prompt response and shutdown notification follow Pi's Promise continuations in a fixed order.
func TestRPCExtensionShutdownRequestExits(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t)
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "quit", "type": "prompt", "message": "/quit"})
			records := drainRPCOutput(p)
			p.waitForExit("after ctx.shutdown()")
			response := rpcRecord{"id": "quit", "type": "response", "command": "prompt", "success": true}
			if want := []rpcRecord{rpcShutdownStartedNotify, response}; !reflect.DeepEqual(records, want) {
				t.Fatalf("stdout = %v, want the prompt response and the session_shutdown notification", records)
			}
			if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"quit", "session_shutdown"}) {
				t.Fatalf("extension events = %v, want [quit session_shutdown]", events)
			}
		})
	}
}

// Pi 0.87.1 writes the extension_ui_request of a dialog a session_shutdown
// handler opens after stdin ended, then exits 0: no response can arrive, and
// Node's event loop drains. The handler never resumes.
func TestRPCInputEndWritesSessionShutdownDialog(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_DIALOG=1")
			awaitReady(p)
			p.closeInput()
			afterEOF := drainRPCOutput(p)
			p.waitForExit("after the session_shutdown dialog")
			want := []rpcRecord{rpcShutdownStartedNotify, {"type": "extension_ui_request", "method": "select", "title": "Shutdown dialog", "options": []any{"keep"}}}
			if !reflect.DeepEqual(afterEOF, want) {
				t.Fatalf("stdout after stdin ended = %v, want %v", afterEOF, want)
			}
			if events := readRPCShutdownReport(t, report); !slices.Equal(events, []string{"session_shutdown"}) {
				t.Fatalf("extension events = %v, want [session_shutdown]: the dialog's handler must not resume", events)
			}
		})
	}
}

// rpc-mode.ts shutdown() (728-744): the first trigger removes the signal
// handlers, unsubscribes stdout, disposes, flushes stdout unless the signal is
// SIGTERM, and exits. After it started, a termination signal takes its
// default action, and stdin end or a shutdown request re-enters shutdown(),
// which exits 0 at once; stdin end after the dispose finished does nothing,
// because detachInput ran (739).
func TestRPCShutdownTriggers(t *testing.T) {
	type harness struct {
		shutdown *rpcShutdown
		steps    chan string
		release  chan struct{}
	}
	newHarness := func(blockDispose bool) *harness {
		h := &harness{steps: make(chan string, 16), release: make(chan struct{})}
		if !blockDispose {
			close(h.release)
		}
		h.shutdown = newRPCShutdown(
			func() { h.steps <- "remove signal handlers" },
			func() { h.steps <- "detach" },
			func() { h.steps <- "dispose"; <-h.release },
			func() { h.steps <- "flush" },
			func(code int) { h.steps <- fmt.Sprintf("exit %03d", code) },
			func(sig os.Signal) { h.steps <- "die " + sig.String() },
		)
		return h
	}
	expect := func(t *testing.T, h *harness, want ...string) {
		t.Helper()
		for _, step := range want {
			if got := <-h.steps; got != step {
				t.Fatalf("step = %q, want %q", got, step)
			}
		}
		select {
		case step := <-h.steps:
			t.Fatalf("extra step %q", step)
		default:
		}
	}
	disposing := func(t *testing.T, h *harness, trigger func()) chan struct{} {
		t.Helper()
		done := make(chan struct{})
		go func() { trigger(); close(done) }()
		for _, step := range []string{"remove signal handlers", "detach", "dispose"} {
			if got := <-h.steps; got != step {
				t.Fatalf("step = %q, want %q", got, step)
			}
		}
		return done
	}

	t.Run("stdin end flushes and exits 0", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.inputEnd()
		expect(t, h, "remove signal handlers", "detach", "dispose", "flush", "exit 000")
	})
	t.Run("a shutdown request flushes and exits 0", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.requested()
		expect(t, h, "remove signal handlers", "detach", "dispose", "flush", "exit 000")
	})
	t.Run("SIGTERM exits 143 without a flush", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.signal(syscall.SIGTERM)
		expect(t, h, "remove signal handlers", "detach", "dispose", "exit 143")
	})
	t.Run("another signal flushes and exits 128 plus its number", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.signal(syscall.SIGINT)
		expect(t, h, "remove signal handlers", "detach", "dispose", "flush", "exit 130")
	})
	t.Run("a signal during a dispose takes its default action", func(t *testing.T) {
		h := newHarness(true)
		done := disposing(t, h, h.shutdown.inputEnd)
		h.shutdown.signal(syscall.SIGTERM)
		expect(t, h, "die "+syscall.SIGTERM.String())
		close(h.release)
		<-done
		expect(t, h, "flush", "exit 000")
	})
	t.Run("stdin end during a signal dispose exits 0 at once", func(t *testing.T) {
		h := newHarness(true)
		done := disposing(t, h, func() { h.shutdown.signal(syscall.SIGTERM) })
		h.shutdown.inputEnd()
		expect(t, h, "exit 000")
		close(h.release)
		<-done
		expect(t, h, "exit 143")
	})
	t.Run("a shutdown request during a stdin-end dispose exits 0 at once", func(t *testing.T) {
		h := newHarness(true)
		done := disposing(t, h, h.shutdown.inputEnd)
		h.shutdown.requested()
		expect(t, h, "exit 000")
		close(h.release)
		<-done
		expect(t, h, "flush", "exit 000")
	})
	t.Run("stdin end after a finished dispose does nothing", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.signal(syscall.SIGTERM)
		expect(t, h, "remove signal handlers", "detach", "dispose", "exit 143")
		h.shutdown.inputEnd()
		expect(t, h)
	})
}

// A command that awaits one threadpool operation or a nested setImmediate
// does not answer after stdin ends, and one that awaits microtasks and a
// setImmediate does (measured with Pi 0.87.1 through the CLI: nested
// setImmediate never answered; fs.stat, fs.access and fs.readdir answered 0 of
// 20 runs each in one measurement and fs.readdir once in 5 under load, so only
// pig's side is asserted for those; microimmediate and awaits5immediate 10 of
// 10). The window closes in the
// first loop iteration's close-callbacks phase, after that iteration's check
// phase and before its next poll phase.
func TestRPCInputEndWindowClosesBeforeNextPollComparedWithPi(t *testing.T) {
	const runs = 5
	// piRacy marks a threadpool row: Pi answered 0 of 20 runs of each in one
	// measurement, but under load in this harness it answered fs.readdir once in
	// 5, so only pig's side is asserted for these rows.
	kinds := []struct {
		kind    string
		answers bool
		piRacy  bool
	}{
		{"microimmediate", true, false},
		{"awaits5immediate", true, false},
		{"immediate2", false, false},
		{"fsstat", false, true},
		{"fsaccess", false, true},
		{"fsreaddir", false, true},
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-window.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, impl := range []string{"pig", "pi"} {
		for _, k := range kinds {
			if impl == "pi" && k.piRacy {
				continue
			}
			t.Run(impl+"/"+k.kind, func(t *testing.T) {
				answered := 0
				for range runs {
					home := t.TempDir()
					env := []string{"HOME=" + home, "RPC_WINDOW_KIND=" + k.kind}
					var p *rpcProcess
					if impl == "pig" {
						env = append(env, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1")
						p = startRPCProcessAt(t, t.TempDir(), env, "--no-session", "--offline", "--model", "test-faux/echo", "-e", fixture)
					} else {
						root, err := filepath.Abs("../..")
						if err != nil {
							t.Fatal(err)
						}
						node, err := exec.LookPath("node")
						if err != nil {
							t.Fatal(err)
						}
						cli := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
						env = append(env, "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"))
						p = startJSONLProcessAt(t, t.TempDir(), env, node, cli, "--mode", "rpc", "--no-session", "--offline", "-e", fixture)
					}
					p.sendJSON(map[string]any{"id": "p1", "type": "prompt", "message": "/probe"})
					p.closeInput()
					for _, r := range drainRPCOutput(p) {
						if r["id"] == "p1" && r["type"] == "response" {
							answered++
						}
					}
					p.waitForExit("after the last command line")
				}
				want := 0
				if k.answers {
					want = runs
				}
				if answered != want {
					t.Fatalf("%s answered %d of %d runs, want %d", impl, answered, runs, want)
				}
			})
		}
	}
}
