package testenv

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// fixtureLifetime outlasts any wait of these tests (testbudget.Default), so only a stop ends a fixture early, and bounds
// what a broken containment leaks while it is being debugged.
const fixtureLifetime = 2*testbudget.Default + time.Minute

// The fixtures are this test binary run in a mode named by TESTENV_FIXTURE, so every OS runs the same children.
func init() {
	mode := os.Getenv("TESTENV_FIXTURE")
	if mode == "" {
		return
	}
	switch mode {
	case "sleep":
		time.Sleep(fixtureLifetime)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		time.Sleep(fixtureLifetime)
	case "orphan", "hold-stdout":
		grandchild := fixtureCommand("sleep")
		if mode == "hold-stdout" {
			grandchild.Stdout, grandchild.Stderr = os.Stdout, os.Stderr
		}
		if err := grandchild.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println(grandchild.Process.Pid)
	}
	os.Exit(0)
}

func fixtureCommand(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "TESTENV_FIXTURE="+mode)
	return cmd
}

// recorder captures the failures and cleanups the spawn path registers, so a test can see them without failing.
type recorder struct {
	testing.TB
	errs     []string
	cleanups []func()
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
func (r *recorder) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }
func (r *recorder) runCleanups() {
	for _, v := range slices.Backward(r.cleanups) {
		v()
	}
	r.cleanups = nil
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still running", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitStopped waits for a child the spawn path was told to stop. A containment that failed to stop it fails the test
// within the test budget, instead of the child's sleep hanging the package to its timeout, and the child is
// then killed directly.
func waitStopped(t *testing.T, child *Child) {
	t.Helper()
	done := make(chan struct{})
	go func() { _ = child.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		_ = child.Cmd().Process.Kill()
		<-done
		t.Fatalf("process %d was not stopped", child.Cmd().Process.Pid)
	}
}

// A child that ignores SIGTERM still ends: the group receives SIGKILL after the grace period.
func TestStopEndsAChildThatIgnoresSIGTERM(t *testing.T) {
	rec := &recorder{TB: t}
	cmd := fixtureCommand("ignore-term")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child, err := Start(rec, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("fixture did not signal readiness: %q %v", line, err)
	}
	pid := child.Cmd().Process.Pid
	rec.runCleanups()
	waitStopped(t, child)
	waitGone(t, pid)
	if len(rec.errs) != 0 {
		t.Fatalf("stopping a running child is not a failure: %v", rec.errs)
	}
}

// A descendant that outlives its exited parent is stopped, and the test is told: this is the leak that left looping
// children running after a test.
func TestRunStopsADescendantOfAnExitedChild(t *testing.T) {
	rec := &recorder{TB: t}
	out, err := Output(rec, fixtureCommand("orphan"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("fixture output %q: %v", out, err)
	}
	waitGone(t, pid)
	if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], errSurvivors.Error()) {
		t.Fatalf("failures = %v, want one report of the surviving descendant", rec.errs)
	}
}

// A descendant that keeps the stdout pipe open cannot hold the test: Wait is bounded by WaitDelay and the group is then stopped.
func TestWaitDoesNotBlockOnStdoutHeldByADescendant(t *testing.T) {
	testWaitDelay = 300 * time.Millisecond
	defer func() { testWaitDelay = 0 }()
	rec := &recorder{TB: t}
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	started := time.Now()
	go func() {
		out, err := CombinedOutput(rec, fixtureCommand("hold-stdout"))
		done <- result{out, err}
	}()
	var out []byte
	var err error
	select {
	case r := <-done:
		out, err = r.out, r.err
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Run did not return while a descendant held stdout")
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("Run took %v with a descendant holding stdout and a WaitDelay of %v", elapsed, testWaitDelay)
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("err = %v, want nil or ErrWaitDelay", err)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil {
		t.Fatalf("fixture output %q: %v", out, convErr)
	}
	waitGone(t, pid)
}

// The cleanup of a child that is still running ends it and its group.
func TestCleanupStopsARunningChild(t *testing.T) {
	rec := &recorder{TB: t}
	child, err := Start(rec, fixtureCommand("sleep"))
	if err != nil {
		t.Fatal(err)
	}
	pid := child.Cmd().Process.Pid
	rec.runCleanups()
	waitStopped(t, child)
	waitGone(t, pid)
	if len(rec.errs) != 0 {
		t.Fatalf("cleanup of a running child reported %v", rec.errs)
	}
}

// Output and CombinedOutput keep exec.Cmd's contract: a stream the caller already set is an error, not silently replaced.
func TestOutputRefusesAStreamTheCallerSet(t *testing.T) {
	var held strings.Builder
	cmd := fixtureCommand("exit")
	cmd.Stdout = &held
	if _, err := Output(t, cmd); err == nil || cmd.Process != nil {
		t.Fatalf("Output with Stdout set: err %v, started %t; want exec's error and no child", err, cmd.Process != nil)
	}
	cmd = fixtureCommand("exit")
	cmd.Stderr = &held
	if _, err := CombinedOutput(t, cmd); err == nil || cmd.Process != nil {
		t.Fatalf("CombinedOutput with Stderr set: err %v, started %t; want exec's error and no child", err, cmd.Process != nil)
	}
}
