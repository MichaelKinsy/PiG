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
// every time; a host that was merely slow to run the abort inside the hold window passes, and a failure prints the timeline that says which it was.
type heldProbe struct {
	member  string
	release chan struct{}

	mu     sync.Mutex
	t0     time.Time
	events []heldEvent
}

type heldEvent struct {
	at   time.Duration
	what string
}

func newHeldProbe(member string) *heldProbe {
	return &heldProbe{member: member, release: make(chan struct{}), t0: time.Now()}
}

// wrap is the ChildSession wrapper to give newChildSessionHostWith.
func (p *heldProbe) wrap(s subprocess.ChildSession) subprocess.ChildSession {
	return heldChildSession{ChildSession: s, probe: p}
}

// start begins the timeline and releases the held member after hold. Call it right before the probe tool runs.
func (p *heldProbe) start(hold time.Duration) {
	p.mu.Lock()
	p.t0, p.events = time.Now(), nil
	p.mu.Unlock()
	time.AfterFunc(hold, func() {
		p.note("RELEASE")
		close(p.release)
	})
}

func (p *heldProbe) note(what string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, heldEvent{time.Since(p.t0), what})
}

// check fails the test unless the host served the abort (abortOp) after the held member entered the host and before it was released.
func (p *heldProbe) check(t *testing.T, abortOp string) {
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
	heldEnter, abortEnter, abortExit, released := index("enter "+p.member), index("enter "+abortOp), index("exit "+abortOp), index("RELEASE")
	var timeline strings.Builder
	for _, e := range p.events {
		fmt.Fprintf(&timeline, "\n  t=%.3f %s", e.at.Seconds(), e.what)
	}
	switch {
	case heldEnter < 0:
		t.Fatalf("the held member %s never reached the host; timeline:%s", p.member, timeline.String())
	case abortEnter < 0 || abortExit < 0:
		t.Fatalf("%s never completed in the host; timeline:%s", abortOp, timeline.String())
	case abortEnter < heldEnter:
		t.Fatalf("%s reached the host before the held %s: the probe did not hold; timeline:%s", abortOp, p.member, timeline.String())
	case released >= 0 && abortExit > released:
		t.Fatalf("%s was served only after the held %s was released (the abort waited behind the member); timeline:%s", abortOp, p.member, timeline.String())
	}
}

type heldChildSession struct {
	subprocess.ChildSession
	probe *heldProbe
}

func (h heldChildSession) Op(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	h.probe.note("enter " + op)
	defer h.probe.note("exit " + op)
	if op == h.probe.member {
		select {
		case <-h.probe.release:
		case <-time.After(10 * time.Second):
		}
		return nil, nil
	}
	return h.ChildSession.Op(ctx, op, args)
}
