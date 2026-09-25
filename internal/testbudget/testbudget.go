// Package testbudget sizes the deadlines tests use to bound a hang.
//
// A test that waits for a subprocess, an extension, or a shell command should
// return as soon as the awaited event happens; its deadline only decides how
// long a genuine hang takes to fail. Fixed 5-10 s deadlines turned into
// failures when many test binaries shared a machine (load average 80 on 14
// cores), so waits use [Wait] instead. Only test code imports this package.
package testbudget

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// Env overrides [Default] with a positive whole number of seconds.
const Env = "PIG_TEST_WAIT_TIMEOUT_SEC"

// Default matches the extension-conformance connect budget, chosen for -p 12
// runs on shared CI runners where builds and Node startup stall for tens of
// seconds.
const Default = 120 * time.Second

// grace is left before the go test -timeout deadline so a hang fails with the
// test's own diagnostics instead of the test binary's timeout panic.
const grace = 10 * time.Second

// Wait returns the hang bound for t: Default or the Env override, capped to
// end before t's deadline.
func Wait(t *testing.T) time.Duration {
	t.Helper()
	deadline, hasDeadline := t.Deadline()
	budget, err := Resolve(os.Getenv(Env), time.Until(deadline), hasDeadline)
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

// Context returns t.Context() bounded by Wait(t).
func Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), Wait(t))
	t.Cleanup(cancel)
	return ctx
}

// Resolve computes a budget from an Env value and the time left before the
// test deadline.
func Resolve(override string, untilDeadline time.Duration, hasDeadline bool) (time.Duration, error) {
	budget := Default
	if override != "" {
		seconds, err := strconv.ParseInt(override, 10, 64)
		if err != nil || seconds <= 0 || seconds > int64((1<<63-1)/time.Second) {
			return 0, fmt.Errorf("%s=%q: want a positive whole number of seconds", Env, override)
		}
		budget = time.Duration(seconds) * time.Second
	}
	if hasDeadline {
		budget = min(budget, max(untilDeadline-grace, time.Second))
	}
	return budget, nil
}
