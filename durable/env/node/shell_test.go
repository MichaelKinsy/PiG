package node

// Ported from packages/durable/test/env-node.test.ts at v1.0.0 (the shell
// describe). Each test name is the upstream case title. Upstream's cases that
// run a Node one-liner (process.execPath) use the equivalent bash printf or
// head; they exercise the same bytes.

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func execErr(t *testing.T, err error, code durableenv.ExecutionErrorCode) *durableenv.ExecutionError {
	t.Helper()
	executionErr := executionError(t, err)
	if executionErr.Code != code {
		t.Fatalf("code = %q (%v), want %q", executionErr.Code, executionErr, code)
	}
	return executionErr
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell behavior")
	}
}

func TestShellExecutesCommandsInCwdWithEnvOverrides(t *testing.T) {
	env, root := newTestEnv(t)
	result, output, err := collectShellOutput(env, `printf '%s' "$NODE_ENV_TEST" > cwd-marker.txt; printf '%s:%s' "$PWD" "$NODE_ENV_TEST"`, &durableenv.ShellExecOptions{Env: map[string]string{"NODE_ENV_TEST": "ok"}}, background)
	mustDo(t, err)
	// upstream: packages/durable/test/env-node.test.ts:587
	if marker, readErr := os.ReadFile(filepath.Join(root, "cwd-marker.txt")); readErr != nil || string(marker) != "ok" {
		t.Fatalf("cwd marker = %q, %v; the command did not run in the cwd with the environment", marker, readErr)
	}
	canonical, evalErr := filepath.EvalSymlinks(root)
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	// A POSIX shell reports its directory as bash does; Git Bash on Windows as a
	// Unix path.
	if runtime.GOOS != "windows" && output != canonical+":ok" {
		t.Fatalf("output = %q, want %q", output, canonical+":ok")
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(output, ":ok") {
		t.Fatalf("output = %q", output)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
}

func TestShellAppliesStringShellEnvironmentOverrides(t *testing.T) {
	cases := []struct {
		description string
		overrides   map[string]string
		want        string
	}{
		{"a missing override preserves the base value", nil, "x:/stale/parent.jsonl"},
		{"an empty override shadows the base value", map[string]string{"PI_SESSION_FILE": ""}, "x:"},
		{"a string override replaces the base value", map[string]string{"PI_SESSION_FILE": "/sessions/current.jsonl"}, "x:/sessions/current.jsonl"},
	}
	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			root := t.TempDir()
			env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellEnv: map[string]string{
				"PI_SESSION_FILE":            "/stale/parent.jsonl",
				"PI_CODING_AGENT":            "true",
				"PI_NODE_ENV_PRESERVED_TEST": "preserved",
			}})
			_, output, err := collectShellOutput(env, `printf '%s:%s|%s|%s' "${PI_SESSION_FILE+x}" "${PI_SESSION_FILE-}" "$PI_CODING_AGENT" "$PI_NODE_ENV_PRESERVED_TEST"`, &durableenv.ShellExecOptions{Env: tc.overrides}, background)
			mustDo(t, err)
			if want := tc.want + "|true|preserved"; output != want {
				t.Fatalf("output = %q, want %q", output, want)
			}
		})
	}
}

func TestShellCanReplaceRatherThanInheritTheDefaultShellEnvironment(t *testing.T) {
	const inheritedKey, configuredKey, explicitKey = "PI_NODE_ENV_INHERITED_TEST", "PI_NODE_ENV_CONFIGURED_TEST", "PI_NODE_ENV_EXPLICIT_TEST"
	t.Setenv(inheritedKey, "host")
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), ShellEnv: map[string]string{configuredKey: "configured"}})
	command := `printf '%s:%s:%s' "${` + inheritedKey + `-}" "${` + configuredKey + `-}" "${` + explicitKey + `-}"`
	_, output, err := collectShellOutput(env, command, &durableenv.ShellExecOptions{InheritEnv: new(false), Env: map[string]string{explicitKey: "explicit"}}, background)
	mustDo(t, err)
	if output != "::explicit" {
		t.Fatalf("output = %q", output)
	}
}

// Upstream also replaces process.platform with win32 for this case; the shell
// transport depends only on the path.
func TestShellUsesStdinCommandTransportForLegacyWSLBashPaths(t *testing.T) {
	skipOnWindows(t)
	root := t.TempDir()
	shellPath := `C:\Windows\System32\bash.exe`
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
	mustDo(t, env.WriteFile(background, shellPath, []byte("#!/bin/sh\nprintf 'args:%s\\n' \"$*\" >&2\nexec /bin/bash \"$@\"\n")))
	if err := os.Chmod(filepath.Join(root, shellPath), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	wslEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: shellPath})
	result, output, err := collectShellOutput(wslEnv, `name='World'; echo "Hello, ${name}!"`, nil, background)
	mustDo(t, err)
	if !strings.Contains(output, "Hello, World!") || !strings.Contains(output, "args:-s") || result.ExitCode != 0 {
		t.Fatalf("output = %q, exit %d", output, result.ExitCode)
	}
}

// Upstream gates this case to win32 and spawns a detached Node descendant. The
// same inherited-stdio shape (a background process holding stdout open after the
// shell exits) runs on every platform here.
func TestShellSettlesAfterTheShellExitsWhenADetachedDescendantRetainsInheritedStdio(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "grandchild.pid")
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
	ctx, cancel := cancellable()
	defer cancel()
	t.Cleanup(func() { killRecordedProcess(pidFile) })
	command := `sleep 60 & echo $! > ` + toBashSingleQuotedArg(pidFile) + `; echo child-exiting`
	done := make(chan struct{})
	var output string
	var execErr error
	go func() {
		defer close(done)
		_, output, execErr = collectShellOutput(env, command, nil, ctx)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("exec did not settle within 3s of the shell exiting")
	}
	mustDo(t, execErr)
	if !strings.Contains(output, "child-exiting") {
		t.Fatalf("output = %q", output)
	}
}

func toBashSingleQuotedArg(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, `\`, "/"), "'", `'"'"'`) + "'"
}

func killRecordedProcess(pidFile string) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	_ = killProcessTree(pid)
}

func TestShellCleanupTerminatesActiveShellProcesses(t *testing.T) {
	env, _ := newTestEnv(t)
	type outcome struct {
		result durableenv.ShellExecResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := env.Exec(background, "touch started; sleep 60", nil)
		done <- outcome{result, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !must(env.Exists(background, "started")) {
		if time.Now().After(deadline) {
			t.Fatal("command did not start within 10s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mustDo(t, env.Cleanup(background))
	select {
	case got := <-done:
		// Upstream: the killed shell settles as a result, not an error.
		if got.err != nil {
			t.Fatalf("exec after cleanup: %v", got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec did not settle within 3s of cleanup")
	}
}

func TestShellStreamsCombinedStdoutAndStderr(t *testing.T) {
	env, _ := newTestEnv(t)
	result, output, err := collectShellOutput(env, "printf out; printf err >&2", nil, background)
	mustDo(t, err)
	if result != (durableenv.ShellExecResult{ExitCode: 0}) {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(output, "out") || !strings.Contains(output, "err") {
		t.Fatalf("output = %q", output)
	}
}

func TestShellDecodesUTF8SplitAcrossRawProcessChunks(t *testing.T) {
	env, _ := newTestEnv(t)
	_, output, err := collectShellOutput(env, `printf '\xf0\x9f'; sleep 0.05; printf '\x98\x80'`, nil, background)
	mustDo(t, err)
	if output != "😀" {
		t.Fatalf("output = %q", output)
	}
}

func TestShellReportsAMissingWorkingDirectoryBeforeSpawning(t *testing.T) {
	root := t.TempDir()
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: filepath.Join(root, "missing")})
	_, err := env.Exec(background, "printf ok", nil)
	failure := execErr(t, err, durableenv.ExecutionErrorSpawnError)
	if !strings.Contains(failure.Message, "Working directory does not exist") {
		t.Fatalf("message = %q", failure.Message)
	}
}

func TestShellReturnsNonZeroCommandExitCodesAsSuccessfulExecutionResults(t *testing.T) {
	env, _ := newTestEnv(t)
	if result := must(env.Exec(background, "exit 7", nil)); result != (durableenv.ShellExecResult{ExitCode: 7}) {
		t.Fatalf("result = %+v", result)
	}
}

// Regression test for https://github.com/earendil-works/pi/issues/8992
func TestShellMapsSignalKilledProcessesToANonZeroExitCode(t *testing.T) {
	skipOnWindows(t)
	env, _ := newTestEnv(t)
	if got := must(env.Exec(background, "kill -9 $$", nil)).ExitCode; got != 128+9 {
		t.Fatalf("exit code = %d, want 137", got)
	}
}

func TestShellReturnsTimeoutErrorsForCommandsExceedingTheTimeout(t *testing.T) {
	env, _ := newTestEnv(t)
	_, err := env.Exec(background, "sleep 5", &durableenv.ShellExecOptions{Timeout: new(0.01)})
	_ = execErr(t, err, durableenv.ExecutionErrorTimeout)
}

func TestShellRejectsInvalidTimeoutsBeforeSpawning(t *testing.T) {
	env, _ := newTestEnv(t)
	for _, timeout := range []float64{0, -1, math.NaN(), math.Inf(1), 2_147_484} {
		_, err := env.Exec(background, "touch spawned", &durableenv.ShellExecOptions{Timeout: new(timeout)})
		failure := execErr(t, err, durableenv.ExecutionErrorTimeout)
		if !strings.Contains(failure.Message, "Invalid timeout") {
			t.Fatalf("timeout %v: message = %q", timeout, failure.Message)
		}
	}
	if must(env.Exists(background, "spawned")) {
		t.Fatal("a command with an invalid timeout spawned")
	}
}

func TestShellReturnsCallbackErrorsFromExecStreamHandlers(t *testing.T) {
	env, _ := newTestEnv(t)
	// A panic in the callback is Go's throw.
	_, err := env.Exec(background, "printf out", &durableenv.ShellExecOptions{OnOutput: func(context.Context, string, durableenv.ShellOutputInfo) {
		panic(errors.New("callback failed"))
	}})
	failure := execErr(t, err, durableenv.ExecutionErrorCallbackError)
	if failure.Message != "callback failed" {
		t.Fatalf("message = %q", failure.Message)
	}
}

func TestShellReturnsShellUnavailableAndSpawnErrors(t *testing.T) {
	root := t.TempDir()
	missingShellEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: filepath.Join(root, "missing-shell")})
	_, err := missingShellEnv.Exec(background, "printf ok", nil)
	_ = execErr(t, err, durableenv.ExecutionErrorShellUnavailable)

	shellPath := filepath.Join(root, "not-executable-shell")
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
	mustDo(t, env.WriteFile(background, shellPath, []byte("not executable")))
	if runtime.GOOS != "windows" {
		mustDo(t, os.Chmod(shellPath, 0o644))
	}
	spawnErrorEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: shellPath})
	_, err = spawnErrorEnv.Exec(background, "printf ok", nil)
	_ = execErr(t, err, durableenv.ExecutionErrorSpawnError)
}

func TestShellReturnsAnAbortedResultForPreAbortedAndAbortedCommands(t *testing.T) {
	env, _ := newTestEnv(t)
	_, err := env.Exec(abortedContext(), "touch spawned", nil)
	_ = execErr(t, err, durableenv.ExecutionErrorAborted)
	if must(env.Exists(background, "spawned")) {
		t.Fatal("a pre-aborted command spawned")
	}

	ctx, cancel := cancellable()
	done := make(chan error, 1)
	go func() {
		_, execFailure := env.Exec(ctx, "sleep 5", nil)
		done <- execFailure
	}()
	cancel()
	select {
	case err = <-done:
		_ = execErr(t, err, durableenv.ExecutionErrorAborted)
	case <-time.After(5 * time.Second):
		t.Fatal("aborted exec did not settle")
	}
}

// "ignores asynchronous taskkill spawn errors during abort" is not ported here:
// Node reports a failed taskkill spawn as an asynchronous "error" event that
// must be consumed. Go's Start returns that failure synchronously and every
// caller of killProcessTree discards it.

func TestShellDoesNotCreateASpillBeforeOutputCrossesItsThresholds(t *testing.T) {
	env, _ := newTestEnv(t)
	result := must(env.Exec(background, "printf short", &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 100, AfterLines: 10}}))
	if result.SpillPath != "" {
		t.Fatalf("spill path = %q", result.SpillPath)
	}
}

func TestShellPreservesExactRawBytesInTheSpillWhileStreamingDecodedText(t *testing.T) {
	env, _ := newTestEnv(t)
	expected := []byte{0x66, 0x80, 0x00, 0x6f}
	_, output, err := collectShellOutput(env, `printf 'f\x80\x00o'`, &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 1, AfterLines: 10}}, background)
	mustDo(t, err)
	if output != "f\uFFFD\x00o" {
		t.Fatalf("streamed text = %q", output)
	}
	result := must(env.Exec(background, `printf 'f\x80\x00o'`, &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 1, AfterLines: 10}}))
	if result.SpillPath == "" {
		t.Fatal("no spill path")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.SpillPath)) })
	if got := must(env.ReadBinaryFile(background, result.SpillPath)); string(got) != string(expected) {
		t.Fatalf("spill = %x, want %x", got, expected)
	}
}

func TestShellReportsTheSpillOfACommandThatTimesOut(t *testing.T) {
	env, _ := newTestEnv(t)
	_, err := env.Exec(background, "printf 12345678901234567890; sleep 5", &durableenv.ShellExecOptions{Timeout: new(0.3), Spill: &durableenv.ShellSpillOptions{AfterBytes: 10, AfterLines: 10}})
	failure := execErr(t, err, durableenv.ExecutionErrorTimeout)
	if failure.SpillPath == "" {
		t.Fatal("no spill path on the timeout error")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(failure.SpillPath)) })
	if got := must(env.ReadTextFile(background, failure.SpillPath)); got != "12345678901234567890" {
		t.Fatalf("spill = %q", got)
	}
}

// failingSpillEnv hands the spill a path inside a directory that does not exist.
type failingSpillEnv struct {
	*NodeExecutionEnv
}

func (env *failingSpillEnv) CreateTempFile(ctx context.Context, options *durableenv.CreateTempFileOptions) (string, error) {
	if options != nil && options.Prefix == "pi-output-" {
		return filepath.Join(env.Cwd(), "missing", "spill.log"), nil
	}
	return env.NodeExecutionEnv.CreateTempFile(ctx, options)
}

func TestShellFailsRatherThanSilentlyLosingARequestedSpill(t *testing.T) {
	env := &failingSpillEnv{NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})}
	env.Self = env
	_, err := env.Exec(background, "printf 12345678901234567890", &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 10, AfterLines: 10}})
	failure := execErr(t, err, durableenv.ExecutionErrorUnknown)
	if !strings.Contains(failure.Message, "Failed to preserve complete shell output") {
		t.Fatalf("message = %q", failure.Message)
	}
}

func TestShellPreservesCompleteLargeOutputInTheSpill(t *testing.T) {
	env, _ := newTestEnv(t)
	const size = 500_000
	result := must(env.Exec(background, "head -c "+strconv.Itoa(size)+" /dev/zero | tr '\\0' x", &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 10, AfterLines: 10}}))
	if result.SpillPath == "" {
		t.Fatal("no spill path")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.SpillPath)) })
	if got := must(env.ReadTextFile(background, result.SpillPath)); len(got) != size || strings.Trim(got, "x") != "" {
		t.Fatalf("spill has %d bytes", len(got))
	}
}

func TestShellStreamsEveryLineAndSpillsThemAllOnceOutputCrossesItsLineThreshold(t *testing.T) {
	env, _ := newTestEnv(t)
	result, output, err := collectShellOutput(env, "i=1; while [ $i -le 15000 ]; do echo line-$i; i=$((i+1)); done", &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 1024 * 1024, AfterLines: 100}}, background)
	mustDo(t, err)
	var want strings.Builder
	for i := 1; i <= 15000; i++ {
		want.WriteString("line-" + strconv.Itoa(i) + "\n")
	}
	if output != want.String() {
		t.Fatalf("streamed %d bytes, want %d", len(output), want.Len())
	}
	if result.SpillPath == "" {
		t.Fatal("no spill path")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.SpillPath)) })
	spilled := must(env.ReadTextLines(background, result.SpillPath, nil))
	if len(spilled) != 15000 || spilled[len(spilled)-1] != "line-15000" {
		t.Fatalf("spilled %d lines, last %q", len(spilled), spilled[len(spilled)-1])
	}
}

// Not upstream cases: they pin node.ts's line count of "complete or partial
// lines" (a final line without a newline counts; a trailing newline does not
// start another) and the final decoder flush.
func TestShellCountsAPartialFinalLineAgainstTheSpillLineThreshold(t *testing.T) {
	env, _ := newTestEnv(t)
	spill := &durableenv.ShellExecOptions{Spill: &durableenv.ShellSpillOptions{AfterBytes: 1024, AfterLines: 2}}
	if result := must(env.Exec(background, `printf 'a\nb\n'`, spill)); result.SpillPath != "" {
		t.Fatalf("two complete lines spilled to %q", result.SpillPath)
	}
	result := must(env.Exec(background, `printf 'a\nb\nc'`, spill))
	if result.SpillPath == "" {
		t.Fatal("two complete lines and a partial third did not spill")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.SpillPath)) })
}

func TestShellEmitsAReplacementCharacterForACharacterTheStreamEndsInTheMiddleOf(t *testing.T) {
	env, _ := newTestEnv(t)
	for _, command := range []string{`printf 'a\xf0\x9f'`, `printf 'a\xf0\x9f' >&2`} {
		_, output, err := collectShellOutput(env, command, nil, background)
		mustDo(t, err)
		if output != "a\uFFFD" {
			t.Fatalf("%s: output = %q", command, output)
		}
	}
}
