//go:build unix

package cli

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestPrintModeSignalExitCodes pins print mode's termination contract.
//
// Pi 0.87.1 print-mode.ts:49-64 handles SIGTERM/SIGHUP with numeric
// 128+signum exits but leaves SIGINT to the default signal action. Process
// wait status distinguishes that action from a normal exit with code 130.
func TestPrintModeSignalExitCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal exit-code test in short mode")
	}
	bin := buildPigBinaryForSignalTest(t)

	for _, tc := range []struct {
		name   string
		signal syscall.Signal
		want   int
	}{
		{"SIGTERM", syscall.SIGTERM, 143},
		{"SIGHUP", syscall.SIGHUP, 129},
		{"SIGINT", syscall.SIGINT, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-extensions", "--print", "Run: sleep for a while")
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(),
				"PIG_HOME="+t.TempDir(),
				"PIG_TEST_FAUX=1",
				"PIG_TEST_FAUX_SCENARIO=parity-basic",
			)
			// Own process group so the signal reaches only this run.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}

			// Wait for the faux tool to actually be running, so the signal
			// lands mid-run rather than during startup.
			marker := filepath.Join(workDir, ai.TestFauxToolStartedMarker)
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal("faux tool never started; cannot signal a run that is not in flight")
				}
				time.Sleep(20 * time.Millisecond)
			}

			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatalf("signal %s: %v", tc.signal, err)
			}
			// SIGINT takes the default action, as in Pi: pig dies at once, and the faux call's shell, which runs in its own
			// process group, keeps sleeping. The test kills what is left of it in its working directory when it ends.
			t.Cleanup(func() { killProcessesIn(workDir) })

			err := cmd.Wait()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("wait = %v, want an exit error carrying %d", err, tc.want)
			}
			if tc.signal == syscall.SIGINT {
				status, ok := exitErr.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
					t.Fatalf("wait status = %v, want SIGINT termination", exitErr.Sys())
				}
			}
			if got := exitErr.ExitCode(); got != tc.want {
				t.Errorf("exit code = %d, want %d (upstream print-mode contract)", got, tc.want)
			}
		})
	}
}

// TestPrintModeSignalDuringPipedStdinRead pins termination while print mode
// waits for piped stdin. Upstream main.ts reads piped stdin to its end before
// print mode registers any signal handler, so a writer that never closes the
// pipe (a harness that leaves stdin open) keeps it waiting, and SIGTERM ends
// the process through the default action: status 143, nothing printed. pig
// read stdin with a blocking io.ReadAll after its own SIGTERM handler had
// replaced the default action, so the signal only cancelled a context nobody
// was waiting on and the process hung.
func TestPrintModeSignalDuringPipedStdinRead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal exit-code test in short mode")
	}
	bin := buildPigBinaryForSignalTest(t)
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-extensions", "--print", "/probe")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_STARTUP_TRACE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	// Signal once pig is waiting on stdin, which the startup trace marks.
	reading := make(chan struct{})
	stderrDone := make(chan struct{})
	var trace strings.Builder
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderr)
		signalled := false
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "[startup] ") {
				trace.WriteString(line + "\n")
			}
			if !signalled && strings.Contains(line, "stdin-read-start") {
				signalled = true
				close(reading)
			}
		}
	}()
	select {
	case <-reading:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("pig never started reading stdin")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}

	// stderr reaches EOF when the process exits; Wait must follow the reads.
	select {
	case <-stderrDone:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("pig ignored SIGTERM while waiting for piped stdin")
	}
	err = cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 143 {
		t.Fatalf("wait = %v, want exit status 143", err)
	}
	if got := trace.String(); got != "" {
		t.Errorf("stderr = %q, want nothing besides the startup trace", got)
	}
}

// TestPrintModeJSONSignalStopsOutputAndPersistence pins what a JSON-mode run leaves after SIGTERM or SIGHUP lands while a
// tool runs. Pi's handler (print-mode.ts:50-63) kills tracked children and runs disposeRuntime: it unsubscribes the JSON
// writer first, then awaits runtimeHost.dispose (session_shutdown, then AgentSession.dispose, which disconnects the
// Session from the Agent before the aborted run settles, agent-session.ts:1393-1414), then exits. So stdout ends with the
// events written before the signal and the session file gets no tool result and no aborted assistant message. Probed
// with Pi 1.1.0 (bash `sleep` through an openai-completions mock): stdout ends at tool_execution_update, the session
// at the toolUse assistant message.
func TestPrintModeJSONSignalStopsOutputAndPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	// A session_shutdown handler that takes a while keeps the process alive after the tracked child is killed, so the aborted
	// tool settles and the run goes on while disposeRuntime awaits it. Pi has unsubscribed the JSON writer first, so stdout
	// still ends at the signal; the session file does get the run's next records then, in Pi as in PiG (probed with Pi
	// 1.1.0: toolResult and a second toolUse assistant message, two provider requests), so it is not asserted for this case.
	slowShutdown := filepath.Join(t.TempDir(), "slow-shutdown.mjs")
	if err := os.WriteFile(slowShutdown, []byte(`export default function (pi) { pi.on("session_shutdown", () => new Promise((resolve) => setTimeout(resolve, 1500))); }`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		signal    syscall.Signal
		want      int
		extension string
	}{
		{"SIGTERM", syscall.SIGTERM, 143, ""},
		{"SIGHUP", syscall.SIGHUP, 129, ""},
		{"SIGTERM with a slow session_shutdown handler", syscall.SIGTERM, 143, slowShutdown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir, sessionDir := t.TempDir(), t.TempDir()
			args := []string{"--model", "test-faux/faux-1", "--no-extensions", "--mode", "json", "--session-dir", sessionDir}
			if tc.extension != "" {
				args = append(args, "-e", tc.extension)
			}
			cmd := exec.Command(bin, append(args, "-p", "Run: sleep for a while")...)
			cmd.Dir = workDir
			agentDir := t.TempDir()
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var stdout strings.Builder
			cmd.Stdout = &stdout
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}
			t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			marker := filepath.Join(workDir, ai.TestFauxToolStartedMarker)
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("faux tool never started")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatalf("signal: %v", err)
			}
			err := cmd.Wait()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.want {
				t.Fatalf("wait = %v, want exit status %d", err, tc.want)
			}
			// pig additive (D102): every exit by a termination signal leaves its line, whichever signal handler exits first.
			if exitLog, _ := os.ReadFile(codingagent.ExitLogPath(agentDir)); !strings.Contains(string(exitLog), "received "+codingagent.SignalName(tc.signal)) {
				t.Fatalf("exit.log = %q, want a line with %q", exitLog, "received "+codingagent.SignalName(tc.signal))
			}
			for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
				for _, after := range []string{`"type":"tool_execution_end"`, `"type":"turn_end"`, `"type":"agent_end"`, `"role":"toolResult"`} {
					if strings.Contains(line, after) {
						t.Fatalf("stdout has an event written after the signal (%s):\n%s", after, stdout.String())
					}
				}
			}
			if tc.extension != "" {
				return
			}
			files, _ := filepath.Glob(filepath.Join(sessionDir, "*.jsonl"))
			if len(files) != 1 {
				t.Fatalf("session files = %v", files)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), `"role":"toolResult"`) || strings.Contains(string(data), `"stopReason":"aborted"`) {
				t.Fatalf("session persisted the aborted run after the signal:\n%s", data)
			}
		})
	}
}

// TestPrintModeSignalDisposalRunsLikePisEventLoop pins what a run in flight does while a signal's disposal runs the
// session_shutdown handlers. Pi's handler (print-mode.ts:50-63) starts disposeRuntime on its one event loop and exits
// when it settles; runPrintMode's finally finds it started (print-mode.ts:42-48,162-167).
//   - A handler that throws at once ends the disposal within microtasks, before the killed tool's exit is seen: the run
//     stops at the tool (the session ends at the toolUse message) and stderr has the handler's error. PiG let the run go
//     on during the handler's IPC round trip and, closing the runtime under it, printed "connection closed".
//   - A handler that waits on a timer lets the loop turn: the killed tool settles, the run makes its next request, and
//     the prompt resolves, so text mode reports the result (here the faux's unhandled-request error) before the exit.
//     PiG printed nothing after any signal.
//
// Probed with Pi 1.1.0 through an openai-completions mock (scenario print/09, the *-shutdown cases).
func TestPrintModeSignalDisposalRunsLikePisEventLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	extensions := t.TempDir()
	throwing := filepath.Join(extensions, "throwing-shutdown.mjs")
	slow := filepath.Join(extensions, "slow-shutdown.mjs")
	for path, source := range map[string]string{
		throwing: `export default function (pi) { pi.on("session_shutdown", () => { throw new Error("shutdown boom"); }); }`,
		slow:     `export default function (pi) { pi.on("session_shutdown", () => new Promise((resolve) => setTimeout(resolve, 1500))); }`,
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A Python handler runs on its own thread and reports no window. Its waitForIdle is a Promise-shaped host call, where
	// a Node handler would await and Pi's loop would turn, so the run goes on to its end and the call returns. Without the
	// call's yield the held run never ends and the signal never exits the process.
	waitsForIdle := filepath.Join(extensions, "waits_for_idle")
	if err := os.MkdirAll(waitsForIdle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(waitsForIdle, "waits_for_idle.py"), []byte(`import pig_sdk


def new_extension() -> pig_sdk.Extension:
    e = pig_sdk.Extension("waits_for_idle")
    e.on_event("session_shutdown", lambda ctx, data: ctx.wait_for_idle())
    return e
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode, extension string
		wantStderr            string
		wantToolResult        bool
	}{
		{"throwing handler/text", "text", throwing, "Extension error (" + throwing + "): shutdown boom\n", false},
		{"throwing handler/json", "json", throwing, "Extension error (" + throwing + "): shutdown boom\n", false},
		{"slow handler/text", "text", slow, "test-faux: unhandled request \"(no output)\n\nCommand exited with code 137\"\n", true},
		{"python handler waiting for idle/text", "text", waitsForIdle, "test-faux: unhandled request \"(no output)\n\nCommand exited with code 137\"\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir, agentDir, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
			cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-extensions", "-e", tc.extension, "--mode", tc.mode, "--session-dir", sessionDir, "-p", "Run: sleep for a while")
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}
			t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			marker := filepath.Join(workDir, ai.TestFauxToolStartedMarker)
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("faux tool never started")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatalf("signal: %v", err)
			}
			waited := make(chan error, 1)
			go func() { waited <- cmd.Wait() }()
			var err error
			select {
			case err = <-waited:
			case <-time.After(testbudget.Wait(t)):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-waited
				t.Fatalf("pig did not exit after SIGTERM; stderr:\n%s", stderr.String())
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 143 {
				t.Fatalf("wait = %v, want exit status 143; stderr:\n%s", err, stderr.String())
			}
			if stderr.String() != tc.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.wantStderr)
			}
			files, _ := filepath.Glob(filepath.Join(sessionDir, "*.jsonl"))
			if len(files) != 1 {
				t.Fatalf("session files = %v", files)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(data), `"role":"toolResult"`); got != tc.wantToolResult {
				t.Fatalf("session has a tool result = %v, want %v:\n%s", got, tc.wantToolResult, data)
			}
		})
	}
}
