package clihook

import (
	"bytes"
	"sync"
	"testing"
)

// resetState leaves the package as a program that registered nothing finds it.
func resetState(t *testing.T) {
	t.Helper()
	reset := func() {
		hook.Store(nil) // nothing registered at all, not a registered nil Hook
		SetStdout(nil)
		mu.Lock()
		exitCode, exitCodeS = 0, false
		mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// A program that registers no hook, or registers a nil one, has no hook: coding/cli's `Registered() != nil` is the whole seam, so Stock PiG never calls into this package.
// mutation-checked: Registered returning a no-op func instead of nil when nothing was ever registered fails "Registered() is not nil before any Register".
func TestRegisteredIsNilWithoutAHook(t *testing.T) {
	resetState(t)
	if Registered() != nil {
		t.Fatal("Registered() is not nil before any Register")
	}
	Register(nil)
	if Registered() != nil {
		t.Fatal("Register(nil) installed a hook")
	}
}

// A second Register replaces the first, as a Piglet Binary fuses one member that owns the command line; a nil Hook removes it.
// mutation-checked: Register keeping the first hook (`hook.CompareAndSwap(nil, &h)`) fails "after the second Register returned Exit 1".
func TestRegisterReplacesThePreviousHook(t *testing.T) {
	resetState(t)
	Register(func(Env) Result { return Result{Exit: 1} })
	Register(func(Env) Result { return Result{Exit: 2} })
	if got := Registered()(Env{}); got.Exit != 2 {
		t.Fatalf("hook after the second Register returned Exit %d, want the second hook's 2", got.Exit)
	}
	Register(nil)
	if Registered() != nil {
		t.Fatal("Register(nil) did not remove the hook")
	}
}

// The hook receives the Env as given and its Result comes back whole; a zero Result serves nothing, rewrites to an empty command line and sets no retry policy. Env and Result are plain data, so this pins the fields a member relies on (a removed or renamed field fails to compile) and the zero value's meaning; it has no mutation of its own.
func TestHookSeesTheEnvAndReturnsItsResult(t *testing.T) {
	resetState(t)
	var stdout bytes.Buffer
	var chdir string
	var seen Env
	Register(func(env Env) Result {
		seen = env
		return Result{Handled: true, Exit: 7, Args: []string{"x"}, Retry: &RetryPolicy{MaxRetries: 1, BaseDelayMs: 2, MaxAgentDelayMs: 3}, OwnPrintOutput: true}
	})
	res := Registered()(Env{Args: []string{"-p", "hi"}, Stdout: &stdout, Getenv: func(k string) string { return "env:" + k }, Chdir: func(d string) error { chdir = d; return nil }, StdinTTY: true})
	if len(seen.Args) != 2 || seen.Args[0] != "-p" || seen.Args[1] != "hi" || seen.Stdout != &stdout || seen.Getenv("K") != "env:K" || !seen.StdinTTY || seen.StdoutTTY {
		t.Fatalf("hook saw %+v", seen)
	}
	if err := seen.Chdir("/d"); err != nil || chdir != "/d" {
		t.Fatalf("Chdir = %v, %q", err, chdir)
	}
	if !res.Handled || res.Exit != 7 || len(res.Args) != 1 || res.Retry == nil || res.Retry.MaxRetries != 1 || res.Retry.BaseDelayMs != 2 || res.Retry.MaxAgentDelayMs != 3 || !res.OwnPrintOutput {
		t.Fatalf("result = %+v", res)
	}
	var zero Result
	if zero.Handled || zero.Exit != 0 || zero.Args != nil || zero.Retry != nil || zero.OwnPrintOutput {
		t.Fatalf("zero Result = %+v: it must serve nothing, rewrite to no arguments and ask for nothing", zero)
	}
}

// Stdout is nil until PiG's print mode hands the member the raw writer, and is whatever it handed last.
// mutation-checked: Stdout returning a fresh io.Discard when none was set fails the nil cell.
func TestStdoutIsNilUntilPrintModeSetsIt(t *testing.T) {
	resetState(t)
	if Stdout() != nil {
		t.Fatal("Stdout() is not nil before SetStdout")
	}
	var a, b bytes.Buffer
	SetStdout(&a)
	if Stdout() != &a {
		t.Fatal("Stdout() is not the writer SetStdout was given")
	}
	SetStdout(&b)
	if Stdout() != &b {
		t.Fatal("a second SetStdout did not replace the first")
	}
}

// ExitCode tells "no status recorded" from "0 recorded", and the last SetExitCode wins.
// mutation-checked: ExitCode returning `code, code != 0` fails the recorded-0 cell; SetExitCode keeping the first status fails the last cell.
func TestExitCodeRecordsTheLastStatus(t *testing.T) {
	resetState(t)
	if code, ok := ExitCode(); ok || code != 0 {
		t.Fatalf("ExitCode() = %d, %v before SetExitCode, want 0, false", code, ok)
	}
	SetExitCode(0)
	if code, ok := ExitCode(); !ok || code != 0 {
		t.Fatalf("ExitCode() = %d, %v after SetExitCode(0), want 0, true", code, ok)
	}
	SetExitCode(3)
	SetExitCode(4)
	if code, ok := ExitCode(); !ok || code != 4 {
		t.Fatalf("ExitCode() = %d, %v after SetExitCode(3) and (4), want 4, true", code, ok)
	}
}

// An extension's handler goroutine sets the status and the writer while PiG's goroutine reads them.
// mutation-checked: dropping the mutex of SetExitCode and ExitCode makes `go test -race` report a data race here.
func TestStateIsSafeAcrossGoroutines(t *testing.T) {
	resetState(t)
	var wg sync.WaitGroup
	var w bytes.Buffer
	for i := range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); SetExitCode(i); SetStdout(&w) }()
		go func() { defer wg.Done(); _, _ = ExitCode(); _ = Stdout() }()
	}
	wg.Wait()
	if _, ok := ExitCode(); !ok {
		t.Fatal("no status recorded")
	}
}
