package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// heldProbe is the host side of the held-member order probes of the four SDKs (Go, Python, Rust and Lisp): a child session whose host holds one
// member until released, as a member awaiting a slow auth check or model_select handler, and records the order in which the host served the calls
// of that session. The probe passes on that order, not on a wall-clock bound: abortBash must have been served (entered and left the host) before the
// held member was released. A member sent over the synchronous lane makes the abort reach the host only after the release, which fails the same way
// every time. The SDK probes send the abort 0.4 s after they start the member; a host that is slow to run the member must not turn the probe red, so
// the host serves the abort only once the member has entered it (an event, not a timer) and counts the hold from that entry: the abort that arrives
// while the member is still on its way waits for it. A failure prints the timeline that says which call was where.
type heldProbe struct {
	member  string
	delay   time.Duration
	release chan struct{}
	entered chan struct{}
	enter   sync.Once

	mu     sync.Mutex
	t0     time.Time
	events []heldEvent
}

type heldEvent struct {
	at   time.Duration
	what string
}

// heldAbortOp is the host op the SDK probes send as the abort of the held member.
const heldAbortOp = "abortBash"

// heldCase is one row of the held-member order probes: delay is held in the host before the member enters it, as a host that is slow to run the member.
type heldCase struct {
	name, member string
	delay        time.Duration
}

func heldCases() []heldCase {
	return []heldCase{
		{"cycleModel", "cycleModel", 0},
		{"executeBash", "executeBash", 0},
		{"setModel", "setModel", 0},
		{"cycleModel-late", "cycleModel", 800 * time.Millisecond},
	}
}

func newHeldProbe(member string, delay time.Duration) *heldProbe {
	return &heldProbe{member: member, delay: delay, release: make(chan struct{}), entered: make(chan struct{}), t0: time.Now()}
}

// wrap is the ChildSession wrapper to give newChildSessionHostWith.
func (p *heldProbe) wrap(s subprocess.ChildSession) subprocess.ChildSession {
	return heldChildSession{ChildSession: s, probe: p}
}

// start begins the timeline and releases the held member after hold, counted from the member's entry into the host (a late member is held as long as an
// early one). Call it right before the probe tool runs.
func (p *heldProbe) start(hold time.Duration) {
	p.mu.Lock()
	p.t0, p.events = time.Now(), nil
	p.mu.Unlock()
	go func() {
		<-p.entered
		time.Sleep(hold)
		p.note("RELEASE")
		close(p.release)
	}()
}

func (p *heldProbe) note(what string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, heldEvent{time.Since(p.t0), what})
}

// check fails the test unless the host served the abort (heldAbortOp) after the held member entered the host and before it was released.
func (p *heldProbe) check(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	index := func(what string) int {
		for i, e := range p.events {
			if e.what == what {
				return i
			}
		}
		return -1
	}
	heldEnter, abortEnter, abortExit, released := index("enter "+p.member), index("enter "+heldAbortOp), index("exit "+heldAbortOp), index("RELEASE")
	var timeline strings.Builder
	for _, e := range p.events {
		fmt.Fprintf(&timeline, "\n  t=%.3f %s", e.at.Seconds(), e.what)
	}
	switch {
	case heldEnter < 0:
		t.Fatalf("the held member %s never reached the host; timeline:%s", p.member, timeline.String())
	case abortEnter < 0 || abortExit < 0:
		t.Fatalf("%s never completed in the host; timeline:%s", heldAbortOp, timeline.String())
	case abortEnter < heldEnter:
		t.Fatalf("%s was served before the held %s entered the host: the probe did not hold; timeline:%s", heldAbortOp, p.member, timeline.String())
	case released >= 0 && abortExit > released:
		t.Fatalf("%s was served only after the held %s was released (the abort waited behind the member); timeline:%s", heldAbortOp, p.member, timeline.String())
	}
}

type heldChildSession struct {
	subprocess.ChildSession
	probe *heldProbe
}

func (h heldChildSession) Op(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	p := h.probe
	switch op {
	case p.member:
		time.Sleep(p.delay)
		p.note("enter " + op)
		p.enter.Do(func() { close(p.entered) })
		defer p.note("exit " + op)
		select {
		case <-p.release:
		case <-time.After(10 * time.Second):
		}
		return nil, nil
	case heldAbortOp:
		p.note("arrive " + op)
		select {
		case <-p.entered:
		case <-time.After(5 * time.Second):
		}
		p.note("enter " + op)
		defer p.note("exit " + op)
	}
	return h.ChildSession.Op(ctx, op, args)
}
