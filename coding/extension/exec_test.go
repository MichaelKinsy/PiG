package extension_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Upstream execCommand spawns the program with shell: false, so shell builtins
// such as echo and pwd are not commands on Windows. The tests run this test
// binary as the child program instead, which behaves the same on every
// platform.
const execHelperEnv = "PIG_EXEC_TEST_HELPER"

func TestExecHelperProcess(t *testing.T) {
	if os.Getenv(execHelperEnv) != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	switch args[0] {
	case "echo":
		fmt.Println(args[1])
	case "stderr":
		fmt.Fprintln(os.Stderr, args[1])
	case "exit":
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "sleep":
		ms, _ := strconv.Atoi(args[1])
		time.Sleep(time.Duration(ms) * time.Millisecond)
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Println(wd)
	}
	os.Exit(0)
}

// helper returns the command and arguments that run TestExecHelperProcess in
// mode with args.
func helper(t *testing.T, mode string, args ...string) (string, []string) {
	t.Helper()
	t.Setenv(execHelperEnv, "1")
	return os.Args[0], append([]string{"-test.run=^TestExecHelperProcess$", "--", mode}, args...)
}

func TestExecCommand_Basic(t *testing.T) {
	command, args := helper(t, "echo", "hello")
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Errorf("exit code = %d, want 0", result.Code)
	}
	if got := result.Stdout; got != "hello\n" {
		t.Errorf("stdout = %q, want %q", got, "hello\n")
	}
	if result.Killed {
		t.Error("should not be killed")
	}
}

func TestExecCommand_NonZeroExit(t *testing.T) {
	command, args := helper(t, "exit", "42")
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 42 {
		t.Errorf("exit code = %d, want 42", result.Code)
	}
	if result.Killed {
		t.Error("should not be killed")
	}
}

// Upstream execCommand never rejects: when the program cannot start, the
// child's error event settles it as {stdout: "", stderr: "", code: 1,
// killed: false}.
func TestExecCommand_SpawnFailureResolvesWithCodeOne(t *testing.T) {
	command, args := helper(t, "echo", "unreachable")
	for name, run := range map[string]func() (extension.ExecResult, error){
		"missing program": func() (extension.ExecResult, error) {
			return extension.ExecCommand(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "missing-program"), nil, nil)
		},
		"missing cwd": func() (extension.ExecResult, error) {
			return extension.ExecCommand(context.Background(), filepath.Join(t.TempDir(), "missing-dir"), command, args, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := run()
			if err != nil {
				t.Fatalf("ExecCommand error = %v, want a result", err)
			}
			if want := (extension.ExecResult{Code: 1}); result != want {
				t.Fatalf("result = %+v, want %+v", result, want)
			}
		})
	}
}

func TestExecCommand_Stderr(t *testing.T) {
	command, args := helper(t, "stderr", "err")
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Stderr; got != "err\n" {
		t.Errorf("stderr = %q, want %q", got, "err\n")
	}
}

func TestExecCommand_Timeout(t *testing.T) {
	command, args := helper(t, "sleep", "10000")
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, &extension.ExecOptions{
		Timeout: 100, // 100ms
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Killed {
		t.Errorf("expected killed=true on timeout; result = %+v", result)
	}
}

func TestExecCommand_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	command, args := helper(t, "sleep", "10000")
	result, err := extension.ExecCommand(ctx, t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Killed {
		t.Error("expected killed=true on cancelled context")
	}
}

func TestExecCommandReportsInitiationBeforeCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	initiated := make(chan struct{})
	ctx = extension.WithCallInitiation(ctx, func() { close(initiated) })
	done := make(chan error, 1)
	go func() {
		_, err := extension.ExecCommand(ctx, t.TempDir(), "sleep", []string{"10"}, nil)
		done <- err
	}()
	select {
	case <-initiated:
	case <-done:
		t.Fatal("exec completed before reporting process initiation")
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("exec did not report process initiation")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("cancelled exec did not finish")
	}
}

func TestExecCommand_CWDOverride(t *testing.T) {
	cwd, dir := t.TempDir(), t.TempDir()
	command, args := helper(t, "pwd")
	result, err := extension.ExecCommand(context.Background(), cwd, command, args, &extension.ExecOptions{
		CWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The child runs in the override dir, not cwd.
	got, err := filepath.EvalSymlinks(filepath.Clean(result.Stdout[:max(len(result.Stdout)-1, 0)]))
	if err != nil {
		t.Fatalf("stdout = %q: %v", result.Stdout, err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("child cwd = %q, want override %q", got, want)
	}
}
