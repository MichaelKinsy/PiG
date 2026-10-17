// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sqlhost_test

import (
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// Hop chains and the step order list (ABI section 5.3), mirroring host/cf/test/hops.test.ts: the owner's mailbox is the one
// FIFO, a chain's hop queues at its tail, and events posted meanwhile queue at their arrival positions.

type scripted struct {
	mu   sync.Mutex
	host *sqlhost.Host
	db   *sqlhost.DB
	log  []string
	on   func(ev abi.Event) *abi.Step
}

func (s *scripted) Step(ev abi.Event) *abi.Step {
	s.mu.Lock()
	defer s.mu.Unlock()
	if step := s.on(ev); step != nil {
		return step
	}
	return &abi.Step{Status: abi.StatusOK}
}

func (s *scripted) note(format string, args ...any) {
	s.log = append(s.log, fmt.Sprintf(format, args...))
}

func (s *scripted) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

func hopsEffect(id, n uint32) abi.Effect {
	return abi.Effect{ID: id, Kind: abi.EffectHops, Payload: abi.AppendHops(nil, abi.Hops{N: n})}
}

func newScripted(t *testing.T, opts sqlhost.Options, on func(s *scripted, ev abi.Event) *abi.Step) *scripted {
	t.Helper()
	s := &scripted{}
	s.on = func(ev abi.Event) *abi.Step { return on(s, ev) }
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "h.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s.db = db
	opts.Core, opts.DB, opts.OwnDB = s, db, true
	s.host = sqlhost.New(opts)
	t.Cleanup(func() { _ = s.host.Close(t.Context()) })
	return s
}

func (s *scripted) waitLines(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if l := s.lines(); len(l) >= want {
			return l
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("log has %v, want %d lines", s.lines(), want)
	return nil
}

func tag(ev abi.Event) string {
	if ev.Kind == abi.EventHopped {
		return fmt.Sprintf("hopped:%d", ev.ID)
	}
	return fmt.Sprintf("x%d", ev.ID)
}

func TestHopsChainOfNRunsItsNthHopAfterTheWorkQueuedBeforeIt(t *testing.T) {
	// The core posts x1 before it returns the chain: queue [x1, hop1]. Each x handled posts the next x at the tail.
	for n, want := range map[uint32][]string{
		1: {"x1", "hopped:7", "x2"},
		2: {"x1", "x2", "hopped:7", "x3"},
		3: {"x1", "x2", "x3", "hopped:7", "x4"},
	} {
		s := newScripted(t, sqlhost.Options{}, func(s *scripted, ev abi.Event) *abi.Step {
			switch ev.Kind {
			case abi.EventOpen:
				s.host.Post(abi.Event{Kind: abi.EventTimer, ID: 1})
				return &abi.Step{Effects: []abi.Effect{hopsEffect(7, n)}}
			case abi.EventTimer:
				s.note("%s", tag(ev))
				if ev.ID <= uint32(len(want)) {
					s.host.Post(abi.Event{Kind: abi.EventTimer, ID: ev.ID + 1})
				}
			case abi.EventHopped:
				s.note("%s", tag(ev))
			}
			return nil
		})
		if err := s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen}); err != nil {
			t.Fatal(err)
		}
		got := s.waitLines(t, len(want))
		if !slices.Equal(got[:len(want)], want) {
			t.Errorf("n=%d: order %v, want %v", n, got, want)
		}
	}
}

func TestHopsConcurrentChainsFinishInHopCountThenStartOrder(t *testing.T) {
	s := newScripted(t, sqlhost.Options{}, func(s *scripted, ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventOpen:
			return &abi.Step{Effects: []abi.Effect{hopsEffect(1, 3), hopsEffect(2, 2), hopsEffect(3, 3)}}
		case abi.EventHopped:
			s.note("%s", tag(ev))
		}
		return nil
	})
	if err := s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen}); err != nil {
		t.Fatal(err)
	}
	if got := s.waitLines(t, 3); !slices.Equal(got, []string{"hopped:2", "hopped:1", "hopped:3"}) {
		t.Errorf("order %v: the 2-hop chain first, then the equal chains in start order", got)
	}
}

func TestHopsCancelDropsAnOpenChain(t *testing.T) {
	s := newScripted(t, sqlhost.Options{}, func(s *scripted, ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventOpen:
			s.host.Post(abi.Event{Kind: abi.EventTimer, ID: 1}) // queue [x1, hop(1), hop(2)]
			return &abi.Step{Effects: []abi.Effect{hopsEffect(1, 4), hopsEffect(2, 4)}}
		case abi.EventTimer:
			return &abi.Step{Effects: []abi.Effect{{ID: 9, Kind: abi.EffectCancel, Payload: binary.LittleEndian.AppendUint32(nil, 1)}}}
		case abi.EventHopped:
			s.note("%s", tag(ev))
		}
		return nil
	})
	if err := s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen}); err != nil {
		t.Fatal(err)
	}
	s.waitLines(t, 1)
	time.Sleep(20 * time.Millisecond)
	if got := s.lines(); !slices.Equal(got, []string{"hopped:2"}) {
		t.Errorf("reports %v: the cancelled chain never reports", got)
	}
}

func TestHopsCloseDropsAnOpenChain(t *testing.T) {
	s := newScripted(t, sqlhost.Options{}, func(s *scripted, ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventOpen:
			return &abi.Step{Effects: []abi.Effect{hopsEffect(1, 1<<30)}}
		case abi.EventHopped:
			s.note("%s", tag(ev))
		}
		return nil
	})
	if err := s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.host.Close(t.Context()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return: the chain keeps the owner busy")
	}
	if got := s.lines(); len(got) != 0 {
		t.Errorf("a dropped chain reported %v", got)
	}
}

func TestHopsZeroIsFatal(t *testing.T) {
	fatal := make(chan error, 1)
	s := newScripted(t, sqlhost.Options{OnFatal: func(err error) { fatal <- err }}, func(s *scripted, ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventOpen {
			return &abi.Step{Effects: []abi.Effect{hopsEffect(1, 0)}}
		}
		return nil
	})
	_ = s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen})
	select {
	case err := <-fatal:
		if !strings.Contains(err.Error(), "n = 0") {
			t.Errorf("fatal %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a hops effect with n = 0 was accepted")
	}
}

func liveness(id uint32, at float64) abi.Effect {
	return abi.Effect{ID: id, Kind: abi.EffectLiveness, Payload: binary.LittleEndian.AppendUint64(nil, math.Float64bits(at))}
}

func TestStepOrderListDecidesNoticeAndEffectOrder(t *testing.T) {
	notice := func(k byte) abi.Notice { return abi.Notice{Kind: abi.NoticeReport, Payload: []byte{k}} }
	for name, tc := range map[string]struct {
		order []abi.OrderRef
		want  []string
	}{
		"listed":   {[]abi.OrderRef{{Region: abi.OrderEffect, Index: 0}, {Region: abi.OrderNotice, Index: 0}, {Region: abi.OrderEffect, Index: 1}, {Region: abi.OrderNotice, Index: 1}}, []string{"effect:10", "notice:1", "effect:20", "notice:2"}},
		"no order": {nil, []string{"notice:1", "notice:2", "effect:10", "effect:20"}},
	} {
		var mu sync.Mutex
		var log []string
		add := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
		s := newScripted(t, sqlhost.Options{
			OnNotice:   func(n abi.Notice) { add(fmt.Sprintf("notice:%d", n.Payload[0])) },
			OnLiveness: func(at float64) { add(fmt.Sprintf("effect:%d", int(at))) },
		}, func(s *scripted, ev abi.Event) *abi.Step {
			if ev.Kind == abi.EventOpen {
				return &abi.Step{Notices: []abi.Notice{notice(1), notice(2)}, Effects: []abi.Effect{liveness(1, 10), liveness(2, 20)}, Order: tc.order}
			}
			return nil
		})
		if err := s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen}); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		if !slices.Equal(log, tc.want) {
			t.Errorf("%s: %v, want %v", name, log, tc.want)
		}
		mu.Unlock()
	}
}

func TestStepOrderNamingNothingIsFatal(t *testing.T) {
	fatal := make(chan error, 1)
	s := newScripted(t, sqlhost.Options{OnFatal: func(err error) { fatal <- err }}, func(s *scripted, ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventOpen {
			return &abi.Step{Order: []abi.OrderRef{{Region: abi.OrderEffect, Index: 3}}}
		}
		return nil
	})
	_ = s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen})
	select {
	case err := <-fatal:
		if !strings.Contains(err.Error(), "order") {
			t.Errorf("fatal %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an order entry that names nothing was accepted")
	}
}

// A host continuation is host code a delivered result wakes outside the owner: the caller awaiting an api_result, which then
// calls the session again (dcore-trace section 9: E20's submit(followUp) three microtasks after submit(first) resolves). In Pi
// it runs between the hops of a chain the same step started, so it reaches the core before the chain's hopped. With Settle
// (a deterministic run), each hop first lets the host's goroutines run until they block, so their posts queue ahead of the
// next hop, however many goroutine handoffs the continuation takes.
func TestHopsLetHostContinuationsPostBetweenHops(t *testing.T) {
	for range 20 {
		synctest.Test(t, func(t *testing.T) {
			woken := make(chan struct{}, 1)
			s := newScripted(t, sqlhost.Options{
				Settle:   synctest.Wait,
				OnNotice: func(abi.Notice) { woken <- struct{}{} },
			}, func(s *scripted, ev abi.Event) *abi.Step {
				switch ev.Kind {
				case abi.EventOpen:
					return &abi.Step{
						Notices: []abi.Notice{{Kind: abi.NoticeAPIResult, Payload: []byte(`{}`)}},
						Effects: []abi.Effect{hopsEffect(7, 2)},
					}
				case abi.EventTimer, abi.EventHopped:
					s.note("%s", tag(ev))
				}
				return nil
			})
			// The caller's continuation: two goroutine handoffs after the result before it posts its next call.
			go func() {
				<-woken
				relay := make(chan struct{})
				go func() {
					<-relay
					s.host.Post(abi.Event{Kind: abi.EventTimer, ID: 1})
				}()
				relay <- struct{}{}
			}()
			if err := s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen}); err != nil {
				t.Fatal(err)
			}
			// The owner is the bubble's one synctest.Wait caller; the test waits on the bubble's clock instead.
			if got := s.waitLines(t, 2); !slices.Equal(got, []string{"x1", "hopped:7"}) {
				t.Fatalf("order %v: the continuation posts between the hops, before hopped", got)
			}
			// The store's goroutines belong to the bubble: close it before the bubble ends.
			if err := s.host.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A chain that carries commits applies them after hop At, between the work that queued before that hop and the work that queued after it
// (ABI section 5.3), where the core used to emit them in the step of a hopped event at that hop.
func TestHopsCommitsOutliveTheStepThatCarriedThem(t *testing.T) {
	// core.Session.Step's slices are valid until the next Step (ABI section 11), and the core builds every hops payload of a
	// step in one arena it reuses from the next Step on. A chain's commits are applied steps later, after hop At, so the host
	// must not apply them from the core's buffer: here the next step overwrites that buffer before hop At.
	const insert = "INSERT INTO t VALUES (?)"
	var arena []byte
	s := newScripted(t, sqlhost.Options{SQL: []string{insert}}, func(s *scripted, ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventOpen:
			if _, err := s.db.Run(t.Context(), "CREATE TABLE t (x)", nil); err != nil {
				t.Error(err)
			}
			s.host.Post(abi.Event{Kind: abi.EventTimer, ID: 1}) // runs before hop 1
			commit := abi.Commit{Seq: 1, Stmts: []abi.Stmt{{SQL: 0, Params: []abi.Value{abi.Text("committed")}}}}
			arena = abi.AppendHops(arena[:0], abi.Hops{N: 3, At: 2, Commits: []abi.Commit{commit}})
			return &abi.Step{Effects: []abi.Effect{{ID: 7, Kind: abi.EffectHops, Payload: arena}}}
		case abi.EventTimer:
			// The next Step reuses the arena for its own chains.
			for i := range arena {
				arena[i] = 'X'
			}
		case abi.EventHopped:
			got, _, err := s.db.All(t.Context(), "SELECT x FROM t", nil)
			if err != nil {
				t.Error(err)
			}
			for _, row := range got {
				s.note("%s", row[0].Bytes)
			}
			s.note("%s", tag(ev))
		}
		return nil
	})
	if err := s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen}); err != nil {
		t.Fatal(err)
	}
	want := []string{"committed", "hopped:7"}
	if got := s.waitLines(t, len(want)); !slices.Equal(got[:len(want)], want) {
		t.Errorf("rows then event %q, want %q", got, want)
	}
}

func TestHopsCommitsLandAfterHopAt(t *testing.T) {
	const insert = "INSERT INTO t VALUES (?)"
	var s *scripted
	rows := func() string {
		got, _, err := s.db.All(t.Context(), "SELECT count(*) FROM t", nil)
		if err != nil {
			t.Error(err)
			return "?"
		}
		return fmt.Sprint(got[0][0].Int)
	}
	s = newScripted(t, sqlhost.Options{SQL: []string{insert}}, func(s *scripted, ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventOpen:
			if _, err := s.db.Run(t.Context(), "CREATE TABLE t (x)", nil); err != nil {
				t.Error(err)
			}
			s.host.Post(abi.Event{Kind: abi.EventTimer, ID: 1}) // queue [x1, hop 1]
			commit := abi.Commit{Seq: 1, Stmts: []abi.Stmt{{SQL: 0, Params: []abi.Value{abi.Int(1)}}}}
			return &abi.Step{Effects: []abi.Effect{{ID: 7, Kind: abi.EffectHops, Payload: abi.AppendHops(nil, abi.Hops{N: 3, At: 2, Commits: []abi.Commit{commit}})}}}
		case abi.EventTimer:
			s.note("x%d:%s", ev.ID, rows())
			if ev.ID < 3 {
				s.host.Post(abi.Event{Kind: abi.EventTimer, ID: ev.ID + 1})
			}
		case abi.EventHopped:
			s.note("%s:%s", tag(ev), rows())
		}
		return nil
	})
	if err := s.host.Send(t.Context(), abi.Event{Kind: abi.EventOpen}); err != nil {
		t.Fatal(err)
	}
	// x1 runs before hop 1, x2 before hop 2, x3 after hop 2 (which applied the commit) and before hop 3, which sends hopped.
	want := []string{"x1:0", "x2:0", "x3:1", "hopped:7:1"}
	if got := s.waitLines(t, len(want)); !slices.Equal(got[:len(want)], want) {
		t.Errorf("order %v, want %v", got, want)
	}
}
