package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// Issue #165: an interactive session "exited on its own while idle, a few minutes after a successful prompt round trip",
// with no crash log. Pi leaves a trace only for an uncaught exception. These tests pin that every exit pig makes
// without a user request leaves a line in exit.log or a record in crashes.json.

const exitRecordChildEnv = "PIG_EXIT_RECORD_CHILD"

// TestExitRecordChild runs one scenario in a child process, because each scenario ends the process.
func TestExitRecordChild(t *testing.T) {
	scenario := os.Getenv(exitRecordChildEnv)
	if scenario == "" {
		t.Skip("child process only")
	}
	agentDir := os.Getenv("PIG_EXIT_RECORD_AGENT_DIR")
	BeginSessionMarker(agentDir, "9.9.9", "/work")
	switch scenario {
	case "goroutine-panic":
		go func() { panic("boom from a goroutine") }()
		select {}
	case "background-panic":
		// The production path: a panic in a goroutine the session started reaches the session's handler.
		m := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9"})
		SetUncaughtGoroutineHandler(m.uncaughtOffLoop)
		m.backgroundTasks.Go(func() { panic("boom from a background task") })
		select {}
	case "unrecovered-background-panic":
		// Without a handler the panic is the runtime's, and the marker file captures it.
		var group backgroundGroup
		group.Go(func() { panic("boom without a handler") })
		select {}
	case "fatal-error":
		// A real runtime fatal error, which no recover can intercept: concurrent map writes.
		shared := map[int]int{}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for i := range 1 << 24 {
					shared[i%64] = i
				}
			})
		}
		wg.Wait()
	case "wait":
		fmt.Println("ready")
		select {}
	case "clean":
		EndSessionMarker()
	case "dead-terminal":
		m := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9", TerminateExtensionProcesses: terminatedSentinel(agentDir)})
		m.emergencyTerminalExit("terminal gone: read /dev/tty: input/output error")
	case "dead-terminal-off-loop":
		// A goroutine fails because the terminal is gone: Pi's uncaughtCrash takes emergencyTerminalExit, which kills the
		// detached children.
		m := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9", TerminateExtensionProcesses: terminatedSentinel(agentDir)})
		SetUncaughtGoroutineHandler(m.uncaughtOffLoop)
		m.backgroundTasks.Go(func() { panic(&os.PathError{Op: "write", Path: "/dev/tty", Err: syscall.EIO}) })
		select {}
	case "dead-terminal-running-shell", "crash-running-shell":
		m := NewInteractiveMode(InteractiveOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9"})
		startRunningShellCommand(agentDir)
		if scenario == "dead-terminal-running-shell" {
			m.emergencyTerminalExit("terminal gone: read /dev/tty: input/output error")
		}
		SetUncaughtGoroutineHandler(m.uncaughtOffLoop)
		m.backgroundTasks.Go(func() { panic("boom while a shell command runs") })
		select {}
	}
	os.Exit(0)
}

// terminatedSentinel stands in for killing the extension processes: it leaves a file the parent test checks.
func terminatedSentinel(agentDir string) func() {
	return func() { _ = os.WriteFile(filepath.Join(agentDir, "extensions-terminated"), nil, 0o644) }
}

func extensionsTerminated(agentDir string) bool {
	_, err := os.Stat(filepath.Join(agentDir, "extensions-terminated"))
	return err == nil
}

// startRunningShellCommand starts a command through the shell operations a `!` command and the bash tool share, with no
// cancellation, and returns once its background job has written its pid to agentDir/shell-job-pid.
func startRunningShellCommand(agentDir string) {
	go func() {
		_, _ = tools.NewLocalBashOperations(nil, "").Exec(context.Background(), "sleep 60 & echo $! > shell-job-pid.tmp && mv shell-job-pid.tmp shell-job-pid; wait", agentDir, tools.BashOperationsExecOptions{})
	}()
	for {
		if _, err := os.Stat(filepath.Join(agentDir, "shell-job-pid")); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runExitRecordChild(t *testing.T, scenario, agentDir string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExitRecordChild$")
	cmd.Env = append(os.Environ(), exitRecordChildEnv+"="+scenario, "PIG_EXIT_RECORD_AGENT_DIR="+agentDir, "PIG_HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return string(output), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(output), 0
}

func markerFiles(t *testing.T, agentDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(CrashOutputDir(agentDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func exitLogText(agentDir string) string {
	data, _ := os.ReadFile(ExitLogPath(agentDir))
	return string(data)
}

// A panic in a goroutine nobody recovers kills the process with Go's stderr dump. The marker file must carry the same
// report, and the next start must turn it into a crash record.
func TestUnrecoveredGoroutinePanicBecomesACrashRecordAtTheNextStart(t *testing.T) {
	for _, scenario := range []string{"goroutine-panic", "unrecovered-background-panic"} {
		t.Run(scenario, func(t *testing.T) {
			agentDir := t.TempDir()
			output, code := runExitRecordChild(t, scenario, agentDir)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2 (Go runtime panic)\n%s", code, output)
			}
			if len(markerFiles(t, agentDir)) != 1 {
				t.Fatalf("marker files = %v, want the dead session's file", markerFiles(t, agentDir))
			}
			if got := len(ReadCrashLog(CrashLogPath(agentDir))); got != 0 {
				t.Fatalf("crash records before the next start = %d", got)
			}
			ReconcileSessionMarkers(agentDir, time.Now())
			records := ReadCrashLog(CrashLogPath(agentDir))
			if len(records) != 1 {
				t.Fatalf("crash records = %+v, want 1", records)
			}
			record := records[0]
			wantMessage := "panic: boom from a goroutine"
			if scenario != "goroutine-panic" {
				wantMessage = "panic: boom without a handler"
			}
			if record.Kind != "uncaught_exception" || !strings.HasPrefix(record.Message, wantMessage) || record.CWD != "/work" || record.Version != "9.9.9" || record.Stack == nil || !strings.Contains(*record.Stack, "goroutine ") {
				t.Fatalf("record = %+v", record)
			}
			if !strings.Contains(exitLogText(agentDir), "session crashed in a goroutine: "+wantMessage) {
				t.Fatalf("exit.log = %q", exitLogText(agentDir))
			}
			if left := markerFiles(t, agentDir); len(left) != 0 {
				t.Fatalf("reconciled marker files remain: %v", left)
			}
			if _, ok := TakeUnnotifiedCrash(CrashLogPath(agentDir), time.Now()); !ok {
				t.Fatal("the next start does not announce the crash")
			}
		})
	}
}

func TestRuntimeFatalErrorBecomesAFatalCrashRecord(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "fatal-error", agentDir)
	if code != 2 || !strings.Contains(output, "fatal error: concurrent map") {
		t.Fatalf("the child did not die of a runtime fatal error (exit %d)\n%s", code, output)
	}
	ReconcileSessionMarkers(agentDir, time.Now())
	records := ReadCrashLog(CrashLogPath(agentDir))
	if len(records) != 1 || records[0].Kind != "fatal_error" || !strings.HasPrefix(records[0].Message, "fatal error: ") || records[0].Stack == nil || !strings.Contains(*records[0].Stack, "goroutine ") {
		t.Fatalf("records = %+v", records)
	}
}

// A panic in a session goroutine reaches the interactive handler: terminal restored, crash recorded, exit 1, as Pi's
// uncaughtException handler does for an unawaited rejection.
func TestBackgroundTaskPanicEndsTheSessionWithACrashRecord(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "background-panic", agentDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, output)
	}
	if !strings.Contains(output, "exiting due to uncaughtException:") || !strings.Contains(output, "boom from a background task") {
		t.Fatalf("stderr = %s", output)
	}
	records := ReadCrashLog(CrashLogPath(agentDir))
	if len(records) != 1 || records[0].Kind != "uncaught_exception" || records[0].Message != "boom from a background task" || records[0].Stack == nil || !strings.Contains(*records[0].Stack, "TestExitRecordChild") {
		t.Fatalf("records = %+v", records)
	}
	if left := markerFiles(t, agentDir); len(left) != 0 {
		t.Fatalf("a recorded crash leaves no marker: %v", left)
	}
}

func TestCleanEndLeavesNoMarker(t *testing.T) {
	agentDir := t.TempDir()
	if output, code := runExitRecordChild(t, "clean", agentDir); code != 0 {
		t.Fatalf("exit %d\n%s", code, output)
	}
	if left := markerFiles(t, agentDir); len(left) != 0 {
		t.Fatalf("marker files after a clean end: %v", left)
	}
	ReconcileSessionMarkers(agentDir, time.Now())
	if text := exitLogText(agentDir); text != "" {
		t.Fatalf("exit.log = %q", text)
	}
}

// The macOS case no code in the process can see: the OS kills a session. The next start must say so.
func TestKilledSessionIsReportedAtTheNextStart(t *testing.T) {
	agentDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExitRecordChild$")
	cmd.Env = append(os.Environ(), exitRecordChildEnv+"=wait", "PIG_EXIT_RECORD_AGENT_DIR="+agentDir, "PIG_HOME="+t.TempDir())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	buf := make([]byte, 64)
	for total := 0; !strings.Contains(string(buf[:total]), "ready"); {
		n, readErr := stdout.Read(buf[total:])
		total += n
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	ReconcileSessionMarkers(agentDir, time.Now())
	if len(markerFiles(t, agentDir)) != 1 || exitLogText(agentDir) != "" {
		t.Fatalf("a live session was reported: markers=%v log=%q", markerFiles(t, agentDir), exitLogText(agentDir))
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, stdout)
	_ = cmd.Wait()
	ReconcileSessionMarkers(agentDir, time.Now())
	text := exitLogText(agentDir)
	if !strings.Contains(text, fmt.Sprintf("pid=%d ", pid)) || !strings.Contains(text, "ended without recording an exit") || !strings.Contains(text, "in /work") {
		t.Fatalf("exit.log = %q", text)
	}
	if got := len(ReadCrashLog(CrashLogPath(agentDir))); got != 0 {
		t.Fatalf("a kill is not a crash record: %d", got)
	}
	if left := markerFiles(t, agentDir); len(left) != 0 {
		t.Fatalf("reconciled marker files remain: %v", left)
	}
}

func TestDeadTerminalExitLeavesALineWithTheReason(t *testing.T) {
	agentDir := t.TempDir()
	if _, code := runExitRecordChild(t, "dead-terminal", agentDir); code != 129 {
		t.Fatalf("exit code = %d, want 129", code)
	}
	if text := exitLogText(agentDir); !strings.Contains(text, "terminal gone: read /dev/tty: input/output error (exit 129)") {
		t.Fatalf("exit.log = %q", text)
	}
	if left := markerFiles(t, agentDir); len(left) != 0 {
		t.Fatalf("marker files: %v", left)
	}
	if !extensionsTerminated(agentDir) {
		t.Fatal("the emergency exit left the extension processes running")
	}
}

// A dead-terminal error raised in a background goroutine is not a crash: it takes the emergency exit, which still kills
// the extension processes (Pi's emergencyTerminalExit calls killTrackedDetachedChildren).
func TestDeadTerminalPanicOffTheOwnerLoopKillsTheExtensionProcesses(t *testing.T) {
	agentDir := t.TempDir()
	output, code := runExitRecordChild(t, "dead-terminal-off-loop", agentDir)
	if code != 129 {
		t.Fatalf("exit code = %d, want 129\n%s", code, output)
	}
	if !extensionsTerminated(agentDir) {
		t.Fatal("the extension processes outlived the emergency exit")
	}
	if text := exitLogText(agentDir); !strings.Contains(text, "terminal gone: write /dev/tty: input/output error (exit 129)") {
		t.Fatalf("exit.log = %q", text)
	}
	if got := len(ReadCrashLog(CrashLogPath(agentDir))); got != 0 {
		t.Fatalf("a dead terminal is not a crash record: %d", got)
	}
}

func TestRunRecordsAnInputEndedExit(t *testing.T) {
	agentDir := t.TempDir()
	RecordExit(agentDir, interactiveExitReason(io.EOF))
	RecordExit(agentDir, interactiveExitReason(errors.New("interactive: raw mode: boom")))
	text := exitLogText(agentDir)
	for _, want := range []string{"terminal input closed (EOF) while the session ran (exit 1)", "interactive mode ended with an error: interactive: raw mode: boom (exit 1)"} {
		if !strings.Contains(text, want) {
			t.Errorf("exit.log lacks %q: %q", want, text)
		}
	}
}

func TestExitLogKeepsTheNewestLines(t *testing.T) {
	agentDir := t.TempDir()
	filler := strings.Repeat("x", 300)
	for i := range 400 {
		RecordExit(agentDir, fmt.Sprintf("n=%d %s", i, filler))
	}
	info, err := os.Stat(ExitLogPath(agentDir))
	if err != nil || info.Size() > maxExitLogBytes+2000 {
		t.Fatalf("exit.log size = %v, err %v", info, err)
	}
	tail := ReadExitLogTail(agentDir, 1)
	if len(tail) != 1 || !strings.Contains(tail[0], "n=399 ") {
		t.Fatalf("tail = %q", tail)
	}
	if strings.Contains(exitLogText(agentDir), "n=0 ") {
		t.Fatal("oldest line survived trimming")
	}
}

func TestRecordExitWithoutAnAgentDirIsANoOp(t *testing.T) {
	RecordExit("", "nothing")
	BeginSessionMarker("", "v", "/")
	EndSessionMarker()
	ReconcileSessionMarkers("", time.Now())
}

func TestSignalNames(t *testing.T) {
	for sig, want := range map[os.Signal]string{syscall.SIGHUP: "SIGHUP", syscall.SIGTERM: "SIGTERM", syscall.SIGINT: "SIGINT"} {
		if got := SignalName(sig); got != want {
			t.Errorf("SignalName(%v) = %q, want %q", sig, got, want)
		}
	}
}

func TestBackgroundGroupPanicReachesTheHandlerWithItsStack(t *testing.T) {
	type report struct {
		value any
		stack string
	}
	reported := make(chan report, 1)
	t.Cleanup(SetUncaughtGoroutineHandler(func(value any, stack []byte) { reported <- report{value, string(stack)} }))
	var group backgroundGroup
	group.Go(func() { panic("task failed") })
	group.Wait()
	got := <-reported
	if got.value != "task failed" || !strings.Contains(got.stack, "TestBackgroundGroupPanicReachesTheHandlerWithItsStack") {
		t.Fatalf("report = %+v", got)
	}
}

// The suspected idle exit: the cache-warm timer fires minutes after a round trip, on a bare timer goroutine. Pi's refresh
// (packages/coding-agent/src/core/cache-warmer.ts:refresh) catches whatever the extension decision and the refresh
// request throw, and reschedules; only a throw outside those try blocks escapes the floating promise to the
// uncaughtException handler. A panic in the request is therefore swallowed and the run reschedules.
func TestCacheWarmerRefreshRequestPanicIsCaughtAndReschedules(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		reported := make(chan any, 2)
		t.Cleanup(SetUncaughtGoroutineHandler(func(value any, _ []byte) { reported <- value }))
		f := newFakeWarmRuntime(t, withResult(func(*ai.Model) *ai.AssistantMessageEventStream { panic("provider conversion failed") }))
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{Thinking: ai.ThinkingHigh, SessionID: "s"}), alwaysCurrent)
		advance(270 * time.Second)
		advance(270 * time.Second)
		if got := f.callCount(); got != 2 {
			t.Fatalf("calls = %d, want 2: a caught refresh failure reschedules", got)
		}
		select {
		case value := <-reported:
			t.Fatalf("a refresh-request panic reached the uncaught handler: %v", value)
		default:
		}
	})
}

// An extension decision that fails keeps Pi's own decision (cache-warmer.ts:refresh, first try block).
func TestCacheWarmerDecisionPanicKeepsPisDecision(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		reported := make(chan any, 1)
		t.Cleanup(SetUncaughtGoroutineHandler(func(value any, _ []byte) { reported <- value }))
		f := newFakeWarmRuntime(t, withDecide(func(CacheWarmingDecisionEvent) CacheWarmingAction { panic("decide failed") }))
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{Thinking: ai.ThinkingHigh, SessionID: "s"}), alwaysCurrent)
		advance(270 * time.Second)
		if got := f.callCount(); got != 1 {
			t.Fatalf("calls = %d, want 1: Pi's warm decision stands", got)
		}
		select {
		case value := <-reported:
			t.Fatalf("a decide panic reached the uncaught handler: %v", value)
		default:
		}
	})
}

// A panic outside Pi's try blocks rejects the floating refresh promise, which reaches uncaughtException. Here the
// run check at the start of the refresh fails.
func TestCacheWarmerRefreshPanicOutsideTheCaughtRequestReachesTheUncaughtHandler(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		reported := make(chan any, 1)
		t.Cleanup(SetUncaughtGoroutineHandler(func(value any, _ []byte) { reported <- value }))
		f := newFakeWarmRuntime(t)
		var armed atomic.Bool
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{Thinking: ai.ThinkingHigh, SessionID: "s"}), func() bool {
			if armed.Load() {
				panic("isCurrent failed")
			}
			return true
		})
		armed.Store(true)
		advance(270 * time.Second)
		select {
		case value := <-reported:
			if value != "isCurrent failed" {
				t.Fatalf("value = %v", value)
			}
		default:
			t.Fatal("the refresh panic did not reach the handler")
		}
		if got := f.callCount(); got != 0 {
			t.Fatalf("calls = %d, want 0", got)
		}
	})
}

// An in-process extension's ctx.shutdown() ends the session without a user request, like the subprocess host action.
func TestInprocExtensionShutdownLeavesALine(t *testing.T) {
	agentDir := t.TempDir()
	runner := inproc.NewRunner([]extension.Extension{{Name: "test-ext"}}, t.TempDir())
	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		opts:      InteractiveOptions{CWD: t.TempDir(), AgentDir: agentDir},
	}
	m.wireInprocContextActions()
	if err := runner.CreateCommandContext().Shutdown(); err != nil {
		t.Fatal(err)
	}
	if !m.requestExit.Load() {
		t.Fatal("ctx.shutdown() did not request exit")
	}
	if text := exitLogText(agentDir); !strings.Contains(text, "extension requested shutdown (ctx.shutdown())") {
		t.Fatalf("exit.log = %q", text)
	}
}

// A crash record carries the time the session died, not the time the next start found it, so the next start announces
// only a crash from the last seven days, as Pi's takeUnnotifiedCrash does.
func TestReconciledCrashKeepsTheCrashTime(t *testing.T) {
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	pid := dead.Process.Pid
	for _, tc := range []struct {
		name      string
		age       time.Duration
		announced bool
	}{
		{"recent", 72 * time.Hour, true},
		{"stale", 8 * 24 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			if err := os.MkdirAll(CrashOutputDir(agentDir), 0o755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(CrashOutputDir(agentDir), fmt.Sprintf("%d.log", pid))
			report := `{"pid":1,"ppid":1,"release":"9.9.9","cwd":"/work","started":"2026-01-01T00:00:00Z"}` + "\npanic: boom\n\ngoroutine 7 [running]:\nmain.f()\n"
			if err := os.WriteFile(marker, []byte(report), 0o644); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			crashedAt := now.Add(-tc.age).Truncate(time.Millisecond)
			if err := os.Chtimes(marker, crashedAt, crashedAt); err != nil {
				t.Fatal(err)
			}
			ReconcileSessionMarkers(agentDir, now)
			records := ReadCrashLog(CrashLogPath(agentDir))
			if len(records) != 1 || records[0].Timestamp != isoTimestamp(crashedAt) {
				t.Fatalf("records = %+v, want one stamped %s", records, isoTimestamp(crashedAt))
			}
			if _, ok := TakeUnnotifiedCrash(CrashLogPath(agentDir), now); ok != tc.announced {
				t.Fatalf("announced = %v, want %v", ok, tc.announced)
			}
		})
	}
}

// Pi attaches no end handler to standard input, so the end of the terminal's input stops input without ending the session.
// A non-terminal source keeps ending the loop with its error: tests and embedders drive it that way.
func TestTerminalInputEOFDoesNotEndTheSession(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.startTerminalInput(ctx, reader)
	defer func() { _ = m.stopTerminalInput() }()
	_ = writer.Close()
	select {
	case err := <-m.inputErrCh:
		t.Fatalf("end of terminal input was reported as an error: %v", err)
	case _, ok := <-m.inputReadCh:
		if ok {
			t.Fatal("unexpected input")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("input pump did not stop at the end of input")
	}
	if text := exitLogText(m.opts.AgentDir); text != "" {
		t.Fatalf("exit.log = %q", text)
	}
}
