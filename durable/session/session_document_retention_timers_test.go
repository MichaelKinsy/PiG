package session_test

import (
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Ports packages/durable/test/session-document-retention.test.ts:468 it("keeps no timer where timers cannot be unreferenced, and
// evicts on commits"). Pi counts calls of a setTimeout that returns a plain number, a timer that cannot be unreferenced. A Go timer
// is not tied to the process, so PiG's form of that host is a SetTimer hook (HarnessOptions.SetTimer) that declines the
// background timers the eviction asks for. The property is Pi's: the cache asks for a timer once, is declined, never asks again,
// and evicts at the end of commits instead.
func TestSessionDocumentCacheKeepsNoTimerWhereTimersCannotBeBackground(t *testing.T) {
	var asked, armed atomic.Int32
	clock := &retentionClock{}
	clock.now.Store(1_000)
	retention := 1_000.0
	kernel := session.NewSessionImpl(sessiontest.NewControlledStorage(), session.Hooks{
		SetTimer: func(callback func(), delayMs float64, background bool) func() {
			if !background {
				armed.Add(1)
				return durable.NativeTimer(callback, delayMs, false)
			}
			asked.Add(1)
			return nil // as in Cloudflare Workers: no way to unreference the timer
		},
	}, clock.read, func() *float64 { return &retention })
	opened := &retentionSession{storage: nil, session: kernel, clock: clock}
	conversationId := opened.conversation(t)
	opened.bump(t, conversationId, 1)
	opened.bump(t, conversationId, 1)
	// Tried once, then never again.
	if got := asked.Load(); got > 1 {
		t.Fatalf("%d background timers asked for, want at most 1", got)
	}
	clock.advance(10_000)
	commit(t, kernel, func(durable.Tx) error { return nil })
	opened.settleLine(t)
	if cached := opened.sizes().Cached; cached != 0 {
		t.Fatalf("cached %d, want 0: the commit evicts what expired", cached)
	}
	if got := asked.Load(); got > 1 {
		t.Fatalf("%d background timers asked for after the commit, want at most 1", got)
	}
	opened.close(t)
	if got := armed.Load(); got != 0 {
		t.Fatalf("%d foreground timers armed by the document cache, want none", got)
	}
}
