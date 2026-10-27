// Package testbudget sizes the deadlines tests use to bound a hang.
//
// A test that waits for a subprocess, an extension, or a shell command should
// return as soon as the awaited event happens; its deadline only decides how
// long a genuine hang takes to fail. Fixed 5-10 s deadlines turned into
// failures when many test binaries shared a machine (load average 80 on 14
// cores), so waits use [Wait] instead. Only test code imports this package.
//
// The go test -timeout deadline caps every bound, so a test that starts when
// the package run is about to expire reports a timeout of its own, naming a
// wait, a few seconds before the test binary's timeout panic. That test is not
// at fault: the run is out of time. A package whose tests need longer than
// -timeout in one process (coding/cli needs about 25 minutes on 4 CPUs) runs
// as several go test processes in CI (automation/ci/test-grouped.sh).
package testbudget

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
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
// end before t's deadline. When the deadline cuts the bound short, Wait logs
// [Capped] on t, once: a wait the package's deadline ended fails the test, or
// kills the child process whose context carries the bound ("signal: killed"),
// just as a hang does, and that line is what tells the two apart.
func Wait(t *testing.T) time.Duration {
	t.Helper()
	deadline, hasDeadline := t.Deadline()
	budget, note, err := resolve(os.Getenv(Env), time.Until(deadline), hasDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		if _, seen := noted.LoadOrStore(t.Name(), true); !seen {
			t.Log(note)
		}
	}
	return budget
}

// noted holds the names of the tests that were told their waits are capped, so
// a test that waits in a loop hears it once.
var noted sync.Map

// resolve is [Resolve] plus the [Capped] note when the deadline lowered the
// budget.
func resolve(override string, untilDeadline time.Duration, hasDeadline bool) (budget time.Duration, note string, err error) {
	budget, err = Resolve(override, untilDeadline, hasDeadline)
	if err != nil {
		return 0, "", err
	}
	if uncapped, _ := Resolve(override, 0, false); budget < uncapped {
		note = Capped(budget, untilDeadline)
	}
	return budget, note, nil
}

// Capped is the note Wait logs when the go test -timeout deadline, until away,
// cuts a wait to budget. In that window nothing is known about the code under
// test: a test that fails after the note may be a hang or may be healthy work
// that the package's own slowness left no time for, and only the package's
// total time tells them apart.
func Capped(budget, until time.Duration) string {
	return fmt.Sprintf("testbudget: the go test -timeout deadline is %s away, so this wait is capped to %s. "+
		"A failure of this test after this line may be the package's own slowness, not a hang: check how long the package had run. "+
		"A package that uses its whole -timeout starves every test that starts in its last %s, healthy or not, "+
		"and needs more shards (automation/ci/test-grouped.sh, make test-cli), not a different test",
		until.Round(time.Millisecond), budget, grace+time.Second)
}

// Context returns t.Context() bounded by Wait(t).
func Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), Wait(t))
	t.Cleanup(cancel)
	return ctx
}

// DeadlineContext returns t.Context() bounded to end [grace] before t's go test
// deadline; without a deadline it is t.Context(). It bounds work whose length no
// event of the test decides, such as the go build of a shared test binary: the
// time that takes is what the host needs, so no fixed bound fits, and only the
// package timeout says it hangs. Work that fails at this bound should say so
// (see [Expired]).
func DeadlineContext(t *testing.T) context.Context {
	t.Helper()
	deadline, hasDeadline := t.Deadline()
	if !hasDeadline {
		return t.Context()
	}
	ctx, cancel := context.WithTimeout(t.Context(), deadlineBound(time.Until(deadline)))
	t.Cleanup(cancel)
	return ctx
}

// Expired describes ctx from [DeadlineContext] when its deadline ended it, or
// returns "".
func Expired(ctx context.Context) string {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ""
	}
	return "the go test -timeout deadline arrived (minus " + grace.String() + " left to report): the package run is out of time, and this step is not hung"
}

// Once runs one build per test process and keeps its result for every later
// test: the shape of a shared fixture binary. The build runs under
// [DeadlineContext] of the first test that asks, so it ends at the go test
// deadline and not at a fixed bound, and a build that the deadline cut reports
// that ([Expired]). A failed build is kept, not retried.
type Once[T any] struct {
	once  sync.Once
	value T
	err   error
}

// Do returns the build's result, running build on the first call.
func (o *Once[T]) Do(t *testing.T, build func(ctx context.Context) (T, error)) (T, error) {
	t.Helper()
	o.once.Do(func() {
		ctx := DeadlineContext(t)
		o.value, o.err = build(ctx)
		if o.err != nil {
			if expired := Expired(ctx); expired != "" {
				o.err = fmt.Errorf("%s: %w", expired, o.err)
			}
		}
	})
	return o.value, o.err
}

// deadlineBound is the time left before a deadline, less grace, with a floor of
// one second.
func deadlineBound(untilDeadline time.Duration) time.Duration {
	return max(untilDeadline-grace, time.Second)
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
		budget = min(budget, deadlineBound(untilDeadline))
	}
	return budget, nil
}
