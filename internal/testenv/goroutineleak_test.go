package testenv

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// leakBlockedSender starts a goroutine that blocks forever sending on a channel nothing else references and waits until it has blocked.
func leakBlockedSender() {
	started := make(chan struct{})
	go func() {
		ch := make(chan int)
		close(started)
		ch <- 1
	}()
	<-started
	time.Sleep(10 * time.Millisecond)
}

const leakHelperEnv = "PIG_TEST_GOROUTINELEAK_HELPER"

// TestGoroutineLeakHelperProcess is the child of TestRunWithoutGoroutineLeaksFailsALeakingRun: the leak it creates must not stay in this test binary.
func TestGoroutineLeakHelperProcess(t *testing.T) {
	if os.Getenv(leakHelperEnv) != "1" {
		return
	}
	os.Exit(runWithoutGoroutineLeaks(func() int {
		leakBlockedSender()
		return 0
	}, nil, os.Stderr))
}

func TestRunWithoutGoroutineLeaksFailsALeakingRun(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGoroutineLeakHelperProcess$")
	cmd.Env = append(os.Environ(), leakHelperEnv+"=1")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("helper error = %v, want exit code 1 for a run that leaked a blocked sender\noutput: %s", err, output)
	}
	if !strings.Contains(string(output), "leakBlockedSender") {
		t.Fatalf("output does not name the leaked goroutine's stack:\n%s", output)
	}
}

func TestRunWithoutGoroutineLeaksPassesACleanRun(t *testing.T) {
	var stderr strings.Builder
	code := runWithoutGoroutineLeaks(func() int {
		var wg sync.WaitGroup
		ch := make(chan int)
		wg.Go(func() { ch <- 1 })
		<-ch
		wg.Wait()
		return 0
	}, nil, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for a run whose goroutines all returned\nstderr: %s", code, &stderr)
	}
}

func TestRunWithoutGoroutineLeaksKeepsAFailingRunsCode(t *testing.T) {
	var stderr strings.Builder
	if code := runWithoutGoroutineLeaks(func() int { return 3 }, nil, &stderr); code != 3 {
		t.Fatalf("exit code = %d, want the run's own 3", code)
	}
}

// afterTests runs after the tests, even failing ones, and before the leak check.
func TestRunWithoutGoroutineLeaksRunsAfterTestsBeforeTheCheck(t *testing.T) {
	var order []string
	code := runWithoutGoroutineLeaks(func() int {
		order = append(order, "tests")
		return 3
	}, []func(){func() { order = append(order, "afterTests") }}, io.Discard)
	if code != 3 || !slices.Equal(order, []string{"tests", "afterTests"}) {
		t.Fatalf("code=%d order=%v, want 3 and [tests afterTests]", code, order)
	}
	if os.Getenv(leakHelperEnv) == "after" {
		os.Exit(runWithoutGoroutineLeaks(func() int { return 0 }, []func(){leakBlockedSender}, os.Stderr))
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRunWithoutGoroutineLeaksRunsAfterTestsBeforeTheCheck$")
	cmd.Env = append(os.Environ(), leakHelperEnv+"=after")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "leakBlockedSender") {
		t.Fatalf("a leak made by afterTests was not caught: err=%v\n%s", err, output)
	}
}
