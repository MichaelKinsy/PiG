package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/utils"
)

// collector folds published updates into the latest view.
type collector struct {
	mu     sync.Mutex
	output *harness.ShellOutputView
	kinds  []string
}

func (collected *collector) onUpdate(_ context.Context, update harness.ShellOutputUpdate) error {
	collected.mu.Lock()
	defer collected.mu.Unlock()
	next := utils.ApplyShellOutputUpdate(collected.output, update)
	collected.output = &next
	collected.kinds = append(collected.kinds, update.Kind)
	return nil
}

func (collected *collector) text() string {
	collected.mu.Lock()
	defer collected.mu.Unlock()
	if collected.output == nil {
		return ""
	}
	return collected.output.Text
}

func execCollect(ctx context.Context, env *NodeExecutionEnv, command string, options *harness.ShellExecOptions) (harness.ShellExecResult, *collector, error) {
	collected := &collector{}
	if options == nil {
		options = &harness.ShellExecOptions{}
	}
	options.OnUpdate = collected.onUpdate
	result, err := env.Exec(ctx, command, options)
	return result, collected, err
}

// execAsync runs Exec on its own goroutine and returns the joined result.
func execAsync(ctx context.Context, env *NodeExecutionEnv, command string) func(t *testing.T) (harness.ShellExecResult, error) {
	type outcome struct {
		result harness.ShellExecResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := env.Exec(ctx, command, nil)
		done <- outcome{result, err}
	}()
	return func(t *testing.T) (harness.ShellExecResult, error) {
		t.Helper()
		select {
		case got := <-done:
			return got.result, got.err
		case <-time.After(3 * time.Second):
			t.Fatal("Exec did not settle within 3s")
			return harness.ShellExecResult{}, nil
		}
	}
}

func killPid(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

// waitForPid waits until the shell has written a pid to path: the redirect
// creates the file before the pid is in it.
func waitForPid(t *testing.T, path string) int {
	t.Helper()
	for range 300 {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never held a pid", path)
	return 0
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	for range 300 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(string(must(os.ReadFile(path)))))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestExecRunsInCwdWithEnvOverrides(t *testing.T) {
	env, root := newTestEnv(t)
	result, collected, err := execCollect(context.Background(), env, `printf '%s:%s' "$PWD" "$NODE_ENV_TEST"`, &harness.ShellExecOptions{Env: map[string]string{"NODE_ENV_TEST": "ok"}})
	mustDo(t, err)
	want := must(filepath.EvalSymlinks(root))
	if runtime.GOOS == "windows" {
		// Git Bash reports $PWD as an MSYS path (/tmp/... or /c/...).
		_, converted, err := execCollect(context.Background(), env, "cygpath -u '"+want+"'", nil)
		mustDo(t, err)
		want = strings.TrimSpace(converted.text())
	}
	if want += ":ok"; collected.text() != want || result.ExitCode != 0 {
		t.Fatalf("output = %q (want %q), exit = %d", collected.text(), want, result.ExitCode)
	}
}

func TestExecAppliesStringShellEnvironmentOverrides(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		want      string
	}{
		{"a missing override preserves the base value", nil, "x:/stale/parent.jsonl"},
		{"an empty override shadows the base value", map[string]string{"PI_SESSION_FILE": ""}, "x:"},
		{"a string override replaces the base value", map[string]string{"PI_SESSION_FILE": "/sessions/current.jsonl"}, "x:/sessions/current.jsonl"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), ShellEnv: map[string]string{
				"PI_SESSION_FILE":            "/stale/parent.jsonl",
				"PI_CODING_AGENT":            "true",
				"PI_NODE_ENV_PRESERVED_TEST": "preserved",
			}})
			_, collected, err := execCollect(context.Background(), env, `printf '%s:%s|%s|%s' "${PI_SESSION_FILE+x}" "${PI_SESSION_FILE-}" "$PI_CODING_AGENT" "$PI_NODE_ENV_PRESERVED_TEST"`, &harness.ShellExecOptions{Env: testCase.overrides})
			mustDo(t, err)
			if want := testCase.want + "|true|preserved"; collected.text() != want {
				t.Fatalf("output = %q, want %q", collected.text(), want)
			}
		})
	}
}

func TestExecCanReplaceRatherThanInheritTheEnvironment(t *testing.T) {
	t.Setenv("PI_NODE_ENV_INHERITED_TEST", "host")
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), ShellEnv: map[string]string{"PI_NODE_ENV_CONFIGURED_TEST": "configured"}})
	_, collected, err := execCollect(context.Background(), env, `printf '%s:%s:%s' "${PI_NODE_ENV_INHERITED_TEST-}" "${PI_NODE_ENV_CONFIGURED_TEST-}" "${PI_NODE_ENV_EXPLICIT_TEST-}"`, &harness.ShellExecOptions{
		InheritEnv: new(false),
		Env:        map[string]string{"PI_NODE_ENV_EXPLICIT_TEST": "explicit"},
	})
	mustDo(t, err)
	if collected.text() != "::explicit" {
		t.Fatalf("output = %q", collected.text())
	}
}

// The legacy WSL bash lives at a fixed drive-root path, so on Windows this
// spawn would start WSL itself; there the transport choice is covered by
// TestBashShellConfigReadsLegacyWslCommandsFromStdin. Upstream likewise runs
// this simulation only off Windows.
func TestExecUsesStdinCommandTransportForLegacyWslBashPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("simulates win32 with a fake C:\\Windows\\System32\\bash.exe under the test root; on Windows that path is the real WSL launcher")
	}
	root := t.TempDir()
	shellPath := `C:\Windows\System32\bash.exe`
	// Upstream's wrapper uses #!/bin/sh; macOS's /bin/sh shim can take tens of
	// seconds to start under load, and the wrapper only needs a POSIX shell.
	script := "#!/bin/bash\nprintf 'args:%s\\n' \"$*\" >&2\nexec /bin/bash \"$@\"\n"
	mustDo(t, os.WriteFile(filepath.Join(root, shellPath), []byte(script), 0o755))
	t.Chdir(root)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: shellPath})
	result, collected, err := execCollect(context.Background(), env, `name='World'; echo "Hello, ${name}!"`, nil)
	mustDo(t, err)
	if !strings.Contains(collected.text(), "Hello, World!") || !strings.Contains(collected.text(), "args:-s") || result.ExitCode != 0 {
		t.Fatalf("output = %q, exit = %d", collected.text(), result.ExitCode)
	}
}

// Legacy WSL bash reads the command from stdin (-s); every other bash takes it
// as an argument (-c).
func TestBashShellConfigReadsLegacyWslCommandsFromStdin(t *testing.T) {
	for shell, legacy := range map[string]bool{
		`C:\Windows\System32\bash.exe`:       true,
		`c:/windows/sysnative/bash.exe`:      true,
		`D:\Windows\System32\BASH.EXE`:       true,
		`C:\Program Files\Git\bin\bash.exe`:  false,
		`C:\tools\Windows\System32\bash.exe`: false,
		"/bin/bash":                          false,
	} {
		config := getBashShellConfig(shell)
		want := shellConfig{shell: shell, args: []string{"-c"}}
		if legacy {
			want = shellConfig{shell: shell, args: []string{"-s"}, commandFromStdin: true}
		}
		if !reflect.DeepEqual(config, want) {
			t.Errorf("getBashShellConfig(%q) = %+v, want %+v", shell, config, want)
		}
	}
}

func TestExecSettlesAfterTheShellExitsWhenADetachedDescendantRetainsStdio(t *testing.T) {
	env, root := newTestEnv(t)
	pidFile := filepath.Join(root, "grandchild.pid")
	t.Cleanup(func() {
		if pid := readPid(t, pidFile); pid > 0 {
			_ = killPid(pid)
		}
	})
	done := make(chan error, 1)
	var collected *collector
	go func() {
		var err error
		_, collected, err = execCollect(context.Background(), env, `sleep 30 & echo $! > grandchild.pid; echo child-exiting`, nil)
		done <- err
	}()
	select {
	case err := <-done:
		mustDo(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec waited for a descendant holding stdio")
	}
	if !strings.Contains(collected.text(), "child-exiting") {
		t.Fatalf("output = %q", collected.text())
	}
}

func TestExecCleanupTerminatesActiveShellProcesses(t *testing.T) {
	env, root := newTestEnv(t)
	wait := execAsync(context.Background(), env, "touch started; sleep 60")
	waitForFile(t, filepath.Join(root, "started"))
	env.Cleanup(context.Background())
	result, err := wait(t)
	mustDo(t, err)
	want := 128 + 9
	if runtime.GOOS == "windows" {
		// Cleanup ends the tree with taskkill /F /T; Node reports the shell
		// then as code 1 with no signal.
		want = 1
	}
	if result.ExitCode != want {
		t.Fatalf("exit = %d, want %d", result.ExitCode, want)
	}
}

func TestExecCombinesStdoutAndStderrIntoOneBoundedView(t *testing.T) {
	env, _ := newTestEnv(t)
	result, collected, err := execCollect(context.Background(), env, "printf out; printf err >&2", nil)
	mustDo(t, err)
	if result.ExitCode != 0 || !strings.Contains(collected.text(), "out") || !strings.Contains(collected.text(), "err") || collected.kinds[0] != harness.ShellOutputUpdateReplace {
		t.Fatalf("output = %q kinds = %v", collected.text(), collected.kinds)
	}
}

func TestExecReportsAMissingWorkingDirectoryBeforeSpawning(t *testing.T) {
	root := t.TempDir()
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: filepath.Join(root, "missing")})
	_, err := env.Exec(context.Background(), "printf ok", nil)
	executionErr := executionError(t, err)
	want := "Working directory does not exist: " + filepath.Join(root, "missing") + "\nCannot execute bash commands."
	if executionErr.Code != harness.ExecutionErrorSpawnError || executionErr.Message != want {
		t.Fatalf("err = %#v", executionErr)
	}
}

func TestExecReturnsNonZeroExitCodesAsSuccessfulResults(t *testing.T) {
	env, _ := newTestEnv(t)
	result := must(env.Exec(context.Background(), "exit 7", nil))
	if result.ExitCode != 7 || result.Truncation.TotalBytes != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecMapsSignalKilledProcessesToANonZeroExitCode(t *testing.T) {
	env, _ := newTestEnv(t)
	killed, terminated := 128+9, 128+15
	if runtime.GOOS == "windows" {
		// Git Bash exits a signalled shell with status signal<<8 and Node
		// reports that code with no signal: 2304 for SIGKILL, 3840 for SIGTERM.
		killed, terminated = 9<<8, 15<<8
	}
	if result := must(env.Exec(context.Background(), "kill -9 $$", nil)); result.ExitCode != killed {
		t.Fatalf("exit = %d, want %d", result.ExitCode, killed)
	}
	if result := must(env.Exec(context.Background(), "kill -TERM $$", nil)); result.ExitCode != terminated {
		t.Fatalf("SIGTERM exit = %d, want %d", result.ExitCode, terminated)
	}
}

func TestExecReturnsTimeoutErrorsForCommandsExceedingTheTimeout(t *testing.T) {
	env, _ := newTestEnv(t)
	started := time.Now()
	_, err := env.Exec(context.Background(), "sleep 5", &harness.ShellExecOptions{Timeout: new(0.01)})
	executionErr := executionError(t, err)
	if executionErr.Code != harness.ExecutionErrorTimeout || executionErr.Message != "timeout:0.01" || time.Since(started) > 3*time.Second {
		t.Fatalf("err = %#v after %s", executionErr, time.Since(started))
	}
}

func TestExecRejectsInvalidTimeouts(t *testing.T) {
	env, _ := newTestEnv(t)
	for timeout, want := range map[float64]string{0: "Invalid timeout: must be a finite number of seconds", -1: "Invalid timeout: must be a finite number of seconds", 1e10: "Invalid timeout: maximum is 2147483.647 seconds"} {
		_, err := env.Exec(context.Background(), "true", &harness.ShellExecOptions{Timeout: new(timeout)})
		if executionErr := executionError(t, err); executionErr.Code != harness.ExecutionErrorTimeout || executionErr.Message != want {
			t.Fatalf("timeout %v: %#v", timeout, executionErr)
		}
	}
}

func TestExecReturnsCallbackErrorsFromStreamHandlers(t *testing.T) {
	env, _ := newTestEnv(t)
	_, err := env.Exec(context.Background(), "printf out", &harness.ShellExecOptions{
		OnUpdate: func(context.Context, harness.ShellOutputUpdate) error { return errors.New("callback failed") },
	})
	if executionErr := executionError(t, err); executionErr.Code != harness.ExecutionErrorCallbackError || executionErr.Message != "callback failed" {
		t.Fatalf("err = %#v", executionErr)
	}
}

func TestExecReturnsShellUnavailableAndSpawnErrors(t *testing.T) {
	root := t.TempDir()
	missingShell := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: filepath.Join(root, "missing-shell")})
	_, err := missingShell.Exec(context.Background(), "printf ok", nil)
	if executionErr := executionError(t, err); executionErr.Code != harness.ExecutionErrorShellUnavailable || executionErr.Message != "Custom shell path not found: "+filepath.Join(root, "missing-shell") {
		t.Fatalf("missing shell = %#v", executionErr)
	}
	shellPath := filepath.Join(root, "not-executable-shell")
	mustDo(t, os.WriteFile(shellPath, []byte("not executable"), 0o600))
	_, err = NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: shellPath}).Exec(context.Background(), "printf ok", nil)
	code := "EACCES"
	if runtime.GOOS == "windows" {
		// Node on Windows cannot start a file without an executable extension.
		code = "ENOENT"
	}
	if executionErr := executionError(t, err); executionErr.Code != harness.ExecutionErrorSpawnError || executionErr.Message != "spawn "+shellPath+" "+code {
		t.Fatalf("spawn error = %#v", executionErr)
	}
}

func TestExecReturnsAbortedForAbortedCommands(t *testing.T) {
	env, root := newTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := execAsync(ctx, env, "touch started; sleep 5")
	waitForFile(t, filepath.Join(root, "started"))
	cancel()
	_, err := wait(t)
	if executionErr := executionError(t, err); executionErr.Code != harness.ExecutionErrorAborted || executionErr.Message != "aborted" {
		t.Fatalf("err = %#v", executionErr)
	}
	if _, err := env.Exec(abortedContext(), "true", nil); executionError(t, err).Code != harness.ExecutionErrorAborted {
		t.Fatalf("pre-aborted err = %v", err)
	}
}

func TestExecIgnoresProcessTreeKillFailuresDuringAbort(t *testing.T) {
	env, root := newTestEnv(t)
	original := killProcessTree
	killProcessTree = func(int) error { return errors.New("taskkill unavailable") }
	t.Cleanup(func() { killProcessTree = original })
	pidFile := filepath.Join(root, "shell.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command, kill := "echo $$ > shell.pid; exec sleep 60", killPid
	if runtime.GOOS == "windows" {
		// Git Bash's $$ is an MSYS pid; /proc/$$/winpid is the Windows one.
		// MSYS exec starts sleep as a child of that process, so the external
		// kill ends the whole tree.
		command, kill = "cat /proc/$$/winpid > shell.pid; exec sleep 60", original
	}
	wait := execAsync(ctx, env, command)
	pid := waitForPid(t, pidFile)
	time.Sleep(20 * time.Millisecond)
	cancel()
	mustDo(t, kill(pid))
	_, err := wait(t)
	if executionErr := executionError(t, err); executionErr.Code != harness.ExecutionErrorAborted {
		t.Fatalf("err = %#v", executionErr)
	}
}

func spillCapture(maxBytes int) *harness.ShellOutputCaptureOptions {
	return &harness.ShellOutputCaptureOptions{Limits: harness.ShellOutputLimits{MaxBytes: maxBytes, MaxLines: 10, Retain: harness.ShellOutputRetainTail}, Spill: true}
}

func ignoreUpdates(context.Context, harness.ShellOutputUpdate) error { return nil }

func TestExecDoesNotSpillBeforeBoundedOutputCrossesItsLimits(t *testing.T) {
	env, _ := newTestEnv(t)
	result := must(env.Exec(context.Background(), "printf short", &harness.ShellExecOptions{Capture: spillCapture(100), OnUpdate: ignoreUpdates}))
	if result.SpillPath != "" {
		t.Fatalf("spill path = %q", result.SpillPath)
	}
}

func TestExecPreservesExactRawBytesInTheSpill(t *testing.T) {
	env, _ := newTestEnv(t)
	result := must(env.Exec(context.Background(), `printf 'f\200\000o'`, &harness.ShellExecOptions{Capture: spillCapture(1), OnUpdate: ignoreUpdates}))
	if result.SpillPath == "" {
		t.Fatal("no spill path")
	}
	if got := must(env.ReadBinaryFile(context.Background(), result.SpillPath)); string(got) != "f\x80\x00o" {
		t.Fatalf("spill = %q", got)
	}
}

func TestExecFailsRatherThanSilentlyLosingARequestedSpill(t *testing.T) {
	env, root := newTestEnv(t)
	env.spillTempFile = func(context.Context, *harness.CreateTempFileOptions) (string, error) {
		return filepath.Join(root, "missing", "spill.log"), nil
	}
	_, err := env.Exec(context.Background(), "printf 12345678901234567890", &harness.ShellExecOptions{Capture: spillCapture(10), OnUpdate: ignoreUpdates})
	if executionErr := executionError(t, err); executionErr.Code != harness.ExecutionErrorUnknown || !strings.Contains(executionErr.Message, "Failed to preserve complete shell output") {
		t.Fatalf("err = %#v", executionErr)
	}
}

func TestExecPreservesCompleteOutputWhenAQuickProcessFloodsTheSpill(t *testing.T) {
	env, _ := newTestEnv(t)
	const size = 500_000
	result := must(env.Exec(context.Background(), "head -c 500000 /dev/zero | tr '\\0' x", &harness.ShellExecOptions{Capture: spillCapture(10), OnUpdate: ignoreUpdates}))
	if result.SpillPath == "" {
		t.Fatal("no spill path")
	}
	if got := must(env.ReadTextFile(context.Background(), result.SpillPath)); len(got) != size {
		t.Fatalf("spill length = %d", len(got))
	}
}

func TestExecCapturesLargeShellOutputToAFullOutputFile(t *testing.T) {
	env, _ := newTestEnv(t)
	result, collected, err := execCollect(context.Background(), env, "yes line | head -n 15000", &harness.ShellExecOptions{
		Capture: &harness.ShellOutputCaptureOptions{Limits: harness.ShellOutputLimits{MaxBytes: 50 * 1024, MaxLines: 2000, Retain: harness.ShellOutputRetainTail}, Spill: true},
	})
	mustDo(t, err)
	if !result.Truncation.Truncated || result.SpillPath == "" {
		t.Fatalf("result = %+v", result)
	}
	fullOutput := must(env.ReadTextFile(context.Background(), result.SpillPath))
	if lines := len(strings.Split(fullOutput, "\n")); lines <= 10000 {
		t.Fatalf("full output lines = %d", lines)
	}
	if len(collected.text()) >= len(fullOutput) {
		t.Fatalf("bounded view (%d) not shorter than full output (%d)", len(collected.text()), len(fullOutput))
	}
}
