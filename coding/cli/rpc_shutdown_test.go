package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// startRPCShutdownFixture starts RPC mode with testdata/rpc-shutdown.mjs and
// returns the process and the file the extension records its events in.
// rpcShutdownSecondExtension in extraEnv also loads rpc-shutdown-second.mjs.
const rpcShutdownSecondExtension = "RPC_SHUTDOWN_SECOND=1"

// rpcShutdownQuarantinedExtension in extraEnv also loads rpc-shutdown-quarantine.mjs. Under pig its factory crashes the Node process once, so the host quarantines it into a Node process apart from the quit handler's; under Pi the crash marker exists already and every extension shares one process.
const rpcShutdownQuarantinedExtension = "RPC_SHUTDOWN_QUARANTINE=1"

// quarantineCrashMarker returns the environment entry naming the crash marker of rpc-shutdown-quarantine.mjs. precreate makes the extension skip its crash.
func quarantineCrashMarker(t *testing.T, precreate bool) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "crash-marker")
	if precreate {
		if err := os.WriteFile(marker, []byte("crashed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return "RPC_SHUTDOWN_CRASH_MARKER=" + marker
}

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
	if slices.Contains(extraEnv, rpcShutdownQuarantinedExtension) {
		quarantined, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown-quarantine.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "-e", quarantined)
		env = append(env, quarantineCrashMarker(t, false))
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
	if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != pigversion.UpstreamPackageVersion {
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
		"-e", filepath.Join(root, "test", "parity", "testdata", "test-faux-provider.ts"), "-e", filepath.Join(root, "coding", "cli", "testdata", "rpc-shutdown.mjs")}
	if slices.Contains(extraEnv, rpcShutdownSecondExtension) {
		args = append(args, "-e", filepath.Join(root, "coding", "cli", "testdata", "rpc-shutdown-second.mjs"))
	}
	if slices.Contains(extraEnv, rpcShutdownQuarantinedExtension) {
		args = append(args, "-e", filepath.Join(root, "coding", "cli", "testdata", "rpc-shutdown-quarantine.mjs"))
		env = append(env, quarantineCrashMarker(t, true))
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
	// As in startRPCCommandAt: a descendant holding the stderr copy open must not hold Wait.
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p.scanOutput(stdout)
	t.Cleanup(p.cleanup(stdout))
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

// handledPromptResponse is the response to a prompt an extension command consumed: rpc-mode.ts:403-407 reports the "handled" disposition.
func handledPromptResponse(id string) rpcRecord {
	return rpcRecord{"id": id, "type": "response", "command": "prompt", "success": true, "data": map[string]any{"disposition": "handled"}}
}

var rpcShutdownStartedNotify = rpcRecord{"type": "extension_ui_request", "method": "notify", "message": "session_shutdown started", "notifyType": "info"}

// Pi (pin c23b9ec1, Pi main 7a53c310a #9126) when stdin ends while a tool call is in flight:
//
//  1. rpc-mode.ts:826-829 runs shutdown(): 744-751 unsubscribe the stdout
//     forwarder of Session events and await runtimeHost.dispose().
//  2. agent-session-runtime.ts:409-417 awaits session.abort() before it emits
//     session_shutdown. abort() (agent-session.ts:2601-2612) aborts the Agent
//     and awaits waitForIdle, so the event loop turns while the Session is
//     still connected: the aborted tool resolves, its tool_execution_end and
//     result message, the turn_end boundary, the next aborted turn, agent_end
//     and agent_settled reach the extensions and the session file, and none
//     reaches stdout, whose forwarder is gone.
//  3. Only then does dispose() emit session_shutdown (the fixture's handler
//     notifies first) and dispose the Session. shutdown() flushes stdout and
//     exits 0.
var (
	rpcMidPromptInputEndExtensionEvents = []string{
		"agent_start", "turn_start",
		"message_start", "message_end", // system
		"message_start", "message_end", // user
		"message_start", "message_end", // tool-call assistant
		"tool_execution_start", "tool_running",
		"tool_execution_end",
		"message_start", "message_end", // tool result
		"turn_end",
		"turn_start",
		"message_start", "message_end", // aborted assistant
		"turn_end",
		"agent_end", "agent_settled",
		"session_shutdown",
	}
	// The fixture's session_shutdown handler notifies; the aborted run's events are not forwarded.
	rpcMidPromptInputEndStdout = []rpcRecord{rpcShutdownStartedNotify}
)

// After stdin ends mid-prompt, pig settles the aborted run first, as Pi's
// dispose() does, and then delivers session_shutdown. Stdout gets only the
// session_shutdown notification; the extension sees the whole aborted run
// before session_shutdown.
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
// line's own iteration (measured with Pi 0.87.1 on Linux: sync, micro, nextTick and
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
		// piRacy marks a Pi row whose outcome is not fixed on this OS. On Windows, libuv reads a non-overlapped stdin pipe on a reader thread that posts its completions to the IOCP, so when the end of input lands is not tied to an event-loop phase, and whether two nested setImmediate ticks beat it varies by host: Pi answered every run of both immediate2 rows on one Windows host and 1 of 5 on a loaded Windows runner (TestRPCInputEndWindowClosesBeforeNextPollComparedWithPi). The pig rows stay strict.
		piRacy bool
	}{
		{command: "micro", response: true},
		{command: "sync", response: true},
		{command: "nexttick", response: true},
		{command: "immediate", response: true},
		{command: "microimmediate", response: true},
		{command: "awaits5immediate", response: true},
		{command: "immediate2", afterExit: true, piRacy: runtime.GOOS == "windows"},
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
				if impl.name == "pi" && tc.piRacy {
					continue
				}
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
						want = append(want, handledPromptResponse("c"))
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
				want := []rpcRecord{rpcShutdownStartedNotify, handledPromptResponse("c")}
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
			want := []rpcRecord{handledPromptResponse("fast")}
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

// Pi (pin c23b9ec1): ctx.shutdown() sets rpc-mode.ts shutdownRequested, which
// checkShutdownRequested (763-766) reads only when no command is being
// handled and the Session is idle: handleInputLine calls it after it wrote
// the command's response (rpc-mode.ts:809-813). shutdown() then disposes the
// runtime and exits 0 although stdin is still open, so the prompt response
// precedes the session_shutdown notification.
func TestRPCExtensionShutdownRequestExits(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t)
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "quit", "type": "prompt", "message": "/quit"})
			records := drainRPCOutput(p)
			p.waitForExit("after ctx.shutdown()")
			want := []rpcRecord{handledPromptResponse("quit"), rpcShutdownStartedNotify}
			if !reflect.DeepEqual(records, want) {
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

// rpc-mode.ts shutdown() (728-744): the first trigger unsubscribes stdout,
// disposes, flushes stdout unless the signal is SIGTERM, and exits. After it
// started (Pi main ace560c30), a stdin end, a
// shutdown request or a single signal does nothing and the dispose finishes; a
// repeated signal exits at once with 128 plus its number.
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
			func() { h.steps <- "detach" },
			func() { h.steps <- "dispose"; <-h.release },
			func() { h.steps <- "flush" },
			func(code int) { h.steps <- fmt.Sprintf("exit %03d", code) },
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
		for _, step := range []string{"detach", "dispose"} {
			if got := <-h.steps; got != step {
				t.Fatalf("step = %q, want %q", got, step)
			}
		}
		return done
	}

	t.Run("stdin end flushes and exits 0", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.inputEnd()
		expect(t, h, "detach", "dispose", "flush", "exit 000")
	})
	t.Run("a shutdown request flushes and exits 0", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.requested()
		expect(t, h, "detach", "dispose", "flush", "exit 000")
	})
	t.Run("SIGTERM exits 143 without a flush", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.signal(syscall.SIGTERM)
		expect(t, h, "detach", "dispose", "exit 143")
	})
	t.Run("another signal flushes and exits 128 plus its number", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.signal(syscall.SIGINT)
		expect(t, h, "detach", "dispose", "flush", "exit 130")
	})
	t.Run("a single signal during a dispose does not interrupt it", func(t *testing.T) {
		h := newHarness(true)
		done := disposing(t, h, func() { h.shutdown.inputEnd() })
		if h.shutdown.signal(syscall.SIGTERM) {
			t.Fatal("the signal started a second shutdown")
		}
		expect(t, h)
		close(h.release)
		<-done
		expect(t, h, "flush", "exit 000")
	})
	t.Run("a repeated signal during a dispose exits at once", func(t *testing.T) {
		h := newHarness(true)
		done := disposing(t, h, func() { h.shutdown.inputEnd() })
		h.shutdown.repeatedSignal(syscall.SIGTERM)
		expect(t, h, "exit 143")
		close(h.release)
		<-done
		// The process ended at the first exit; the dispose finishing afterwards neither flushes nor exits again.
		expect(t, h)
	})
	t.Run("stdin end during a signal dispose does not interrupt it", func(t *testing.T) {
		h := newHarness(true)
		done := disposing(t, h, func() { h.shutdown.signal(syscall.SIGTERM) })
		if h.shutdown.inputEnd() {
			t.Fatal("stdin end claimed the process while a signal's shutdown was running")
		}
		expect(t, h)
		close(h.release)
		<-done
		expect(t, h, "exit 143")
	})
	t.Run("a shutdown request during a stdin-end dispose does nothing", func(t *testing.T) {
		h := newHarness(true)
		done := disposing(t, h, func() { h.shutdown.inputEnd() })
		h.shutdown.requested()
		expect(t, h)
		close(h.release)
		<-done
		expect(t, h, "flush", "exit 000")
	})
	t.Run("stdin end after a finished dispose does nothing", func(t *testing.T) {
		h := newHarness(false)
		h.shutdown.signal(syscall.SIGTERM)
		expect(t, h, "detach", "dispose", "exit 143")
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
	// piRacy marks a row whose Pi answer is not fixed, so only pig's side is
	// asserted for it. The threadpool rows: Pi answered 0 of 20 runs of each in
	// one measurement, but under load in this harness it answered fs.readdir
	// once in 5.
	// immediate2 on Windows: libuv reads a non-overlapped stdin pipe on a reader thread that posts its completions to the IOCP, so when the end of input lands is not tied to an event-loop phase, and whether two nested setImmediate ticks beat it is not fixed; Pi answered 1 of 5 runs on a loaded Windows runner and every run of TestRPCInputEndAfterExtensionCommandComparedWithPi's immediate2 rows on another Windows host.
	kinds := []struct {
		kind    string
		answers bool
		piRacy  bool
	}{
		{"microimmediate", true, false},
		{"awaits5immediate", true, false},
		{"immediate2", false, runtime.GOOS == "windows"},
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
						p = startRPCProcessAt(t, t.TempDir(), env, "--no-session", "--offline", "--model", "test-faux/faux-1", "-e", fixture)
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
