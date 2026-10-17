// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package guard

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

type capture struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	code chan int
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}
func (c *capture) text() string { c.mu.Lock(); defer c.mu.Unlock(); return c.buf.String() }

func start(t *testing.T, o Options) (*Guard, *capture) {
	t.Helper()
	c := &capture{code: make(chan int, 1)}
	o.Name, o.Stderr, o.Poll = "test", c, 5*time.Millisecond
	o.Exit = func(code int) { c.code <- code }
	g := Start(o)
	t.Cleanup(g.Stop)
	return g, c
}

func exitCode(t *testing.T, c *capture) int {
	t.Helper()
	select {
	case code := <-c.code:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("the guard did not trip")
		return 0
	}
}

// A request that does not return within the step bound ends the process, names the request, and dumps the goroutines.
func TestARequestThatRunsTooLongTripsTheGuardWithItsStacks(t *testing.T) {
	g, c := start(t, Options{Step: 30 * time.Millisecond})
	g.Begin("op 4 kind 9")
	if code := exitCode(t, c); code != ExitBound {
		t.Fatalf("exit %d, want %d", code, ExitBound)
	}
	out := c.text()
	for _, want := range []string{"request op 4 kind 9 ran", "over the 30ms bound", "goroutine profile"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report lacks %q:\n%s", want, out)
		}
	}
}

// A finished request is not timed.
func TestAFinishedRequestDoesNotTripTheGuard(t *testing.T) {
	g, c := start(t, Options{Step: 20 * time.Millisecond})
	g.Begin("op 4")
	g.End()
	select {
	case code := <-c.code:
		t.Fatalf("tripped with %d: %s", code, c.text())
	case <-time.After(150 * time.Millisecond):
	}
}

// A heap over the bound ends the process even between requests.
func TestAHeapOverTheBoundTripsTheGuard(t *testing.T) {
	_, c := start(t, Options{MemoryBytes: 1})
	if code := exitCode(t, c); code != ExitBound || !strings.Contains(c.text(), "over the 0 MiB bound") {
		t.Fatalf("exit %d: %s", code, c.text())
	}
}

// A process whose parent is gone ends (a different parent ID than the one it started with), without a report: that is an ordinary
// end of the host, not a runaway, and the gate fails a row on a guard report.
func TestAnOrphanedProcessEnds(t *testing.T) {
	_, c := start(t, Options{Parent: 1 << 30})
	if code := exitCode(t, c); code != ExitParent || c.text() != "" {
		t.Fatalf("exit %d: %s", code, c.text())
	}
}

// Stop may be called again, for example by a deferred Stop after an explicit one: the second call does nothing.
func TestStopTwiceDoesNotPanic(t *testing.T) {
	g, _ := start(t, Options{Step: time.Second})
	g.Stop()
	g.Stop()
}
