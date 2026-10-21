package jsloop

import "sync"

// SettleGate orders actions that several goroutines run after waiting on an
// event loop's answers, in the order the answers arrived. Pi runs everything
// on one thread: when two awaits settle back to back, their continuations run
// in settle order, and nothing a continuation does overtakes an earlier one.
// Go wakes the goroutines that wait on those answers in no particular order:
// a sender that wakes two goroutines in a row runs the second first, so the
// answers' order is lost between the arrival and the continuation unless the
// order is recorded where the answers arrive and kept where the continuations
// act (SettleSlot).
//
// The zero value is ready to use.
type SettleGate struct {
	mu   sync.Mutex
	turn sync.Cond
	// settled holds the slots that settled and whose action has not finished, in settle order.
	settled []*SettleSlot
	// parked, when set, runs each time a Run starts to wait for an earlier slot.
	parked func()
}

// SetParkedHook has fn run each time a Run starts to wait for an earlier slot, so a test knows a caller is held without
// timing. Set it before the gate is used.
func (g *SettleGate) SetParkedHook(fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.parked = fn
}

// SettleSlot is one waiter's place in a SettleGate.
type SettleSlot struct {
	gate *SettleGate
	// state is guarded by gate.mu.
	state slotState
}

type slotState uint8

const (
	slotWaiting slotState = iota
	slotSettled
	slotFinished
)

// NewSlot returns a slot that holds no place until it settles.
func (g *SettleGate) NewSlot() *SettleSlot { return &SettleSlot{gate: g} }

// Gate returns the gate the slot belongs to.
func (s *SettleSlot) Gate() *SettleGate { return s.gate }

// Settle gives the slot its place after every slot that already settled. The
// goroutine that reads the answer calls it before it hands the answer to the
// waiter, so the order is the order the answers were read in. It does nothing
// for a slot that settled.
func (s *SettleSlot) Settle() {
	g := s.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if s.state != slotWaiting {
		return
	}
	s.state = slotSettled
	g.settled = append(g.settled, s)
}

// Run runs action once every slot that settled before this one finished. A
// slot that no reader settled settles when Run starts, so waiters whose
// answers carry no order keep the order they reach Run in. Each slot runs one
// action.
func (s *SettleSlot) Run(action func()) {
	s.Settle()
	g := s.gate
	g.mu.Lock()
	if g.turn.L == nil {
		g.turn.L = &g.mu
	}
	for s.state == slotSettled && g.settled[0] != s {
		if g.parked != nil {
			g.parked()
		}
		g.turn.Wait()
	}
	g.mu.Unlock()
	defer s.Close()
	action()
}

// Close ends the slot: a slot that settled and never ran stops holding up the
// slots behind it. It does nothing for a slot that ran.
func (s *SettleSlot) Close() {
	g := s.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if s.state == slotFinished {
		return
	}
	if s.state == slotSettled {
		for i, held := range g.settled {
			if held == s {
				g.settled = append(g.settled[:i], g.settled[i+1:]...)
				break
			}
		}
		if g.turn.L == nil {
			g.turn.L = &g.mu
		}
		g.turn.Broadcast()
	}
	s.state = slotFinished
}
