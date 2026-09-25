package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const (
	commandShimRealEnv = "PIG_TEST_COMMAND_SHIM_REAL"
	commandShimLogEnv  = "PIG_TEST_COMMAND_SHIM_LOG"
)

// writeCommandShim puts name on a PATH directory ahead of the real command and
// returns the environment that activates it. The shim is this test binary:
// TestMain sees commandShimRealEnv, appends the invocation's arguments to the
// log, one line per call, and runs the real command. It works on every
// platform the test binary runs on.
func writeCommandShim(t *testing.T, dir, name, log string) []string {
	t.Helper()
	real, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("look up %s: %v", name, err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		shim += ".exe"
	}
	if err := os.Link(self, shim); err != nil {
		data, readErr := os.ReadFile(self)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err := os.WriteFile(shim, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return []string{commandShimRealEnv + "=" + real, commandShimLogEnv + "=" + log}
}

// runCommandShim is the shim side of writeCommandShim. It returns the real
// command's exit code.
func runCommandShim(real, log string) int {
	file, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "command shim log:", err)
		return 2
	}
	_, err = fmt.Fprintln(file, strings.Join(os.Args[1:], " "))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "command shim log:", err)
		return 2
	}
	command := exec.Command(real, os.Args[1:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, commandShimRealEnv+"=") || strings.HasPrefix(entry, commandShimLogEnv+"=")
	})
	if err := command.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "command shim:", err)
		return 2
	}
	return 0
}

func shimInvocations(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// A warm startup counts its work instead of timing it. This is the owner's
// scenario: PiG Standard's Go extensions were built by an earlier run, the
// staged SDK is current, and another process holds the staged-SDK stage lock
// for the whole run. The startup must acquire no SDK lock (an acquisition
// would block until the test deadline), run no compiler, and still load every
// extension.
func TestWarmStandardStartupWorkBudgetWithHeldSDKLock(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	standard, err := filepath.Abs(filepath.Join("..", "..", "piglets", "standard", "pig-standard.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	project := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(extraEnv ...string) string {
		t.Helper()
		command := exec.CommandContext(testbudget.Context(t), binary, "--piglet", standard, "--model", "test-faux/faux-1", "--no-session", "--print", "What is 20+22?")
		command.Dir = project
		command.Env = append(os.Environ(), append([]string{"PIG_HOME=" + home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_STARTUP_TRACE=1"}, extraEnv...)...)
		var stderr strings.Builder
		command.Stderr = &stderr
		if err := command.Run(); err != nil {
			t.Fatalf("pig startup: %v\nstderr:\n%s", err, stderr.String())
		}
		return stderr.String()
	}
	// The first run stages the SDK and builds the packed cell.
	run()

	release, err := pigsdklock.AcquireStage(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	shims := t.TempDir()
	goLog := filepath.Join(t.TempDir(), "go.log")
	shimEnv := writeCommandShim(t, shims, "go", goLog)
	stderr := run(append(shimEnv, "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"))...)

	for _, name := range []string{"piglogin", "pigrunner", "angrypigs"} {
		if !strings.Contains(stderr, "extension."+name+".handshake-done") {
			t.Errorf("extension %s did not load on the warm run:\n%s", name, stderr)
		}
	}
	if strings.Contains(stderr, "transaction lock") {
		t.Errorf("warm startup reported an SDK lock wait:\n%s", stderr)
	}
	invocations := shimInvocations(t, goLog)
	if len(invocations) == 0 {
		t.Fatal("the go shim saw no invocation; the budget would not observe a build")
	}
	var builds []string
	for _, invocation := range invocations {
		if strings.HasPrefix(invocation, "build") {
			builds = append(builds, invocation)
		}
	}
	if len(builds) != 0 {
		t.Errorf("warm startup ran the Go compiler %d times: %q", len(builds), builds)
	}
	if count := strings.Count(stderr, "[startup] sdk-sync-start"); count != 1 {
		t.Errorf("SDK synchronization passes = %d, want 1", count)
	}
}

// When startup does have to wait for another process's SDK lock (here the SDK
// is not staged yet), the startup trace names the wait instead of leaving a
// silent gap.
func TestStartupTraceNamesSDKLockWait(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := pigsdklock.AcquireStage(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	command := exec.CommandContext(testbudget.Context(t), binary, "-ne", "--model", "test-faux/faux-1", "--no-session", "--print", "What is 20+22?")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_STARTUP_TRACE=1")
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("pig startup: %v\nstderr:\n%s", err, stderr.String())
	}
	trace := stderr.String()
	start := strings.Index(trace, "[startup] sdk-lock-wait-start")
	done := strings.Index(trace, "[startup] sdk-lock-wait-done")
	syncDone := strings.Index(trace, "[startup] sdk-sync-done")
	if start < 0 || done < start || syncDone < done {
		t.Fatalf("trace does not bracket the SDK lock wait inside SDK synchronization:\n%s", trace)
	}
}
