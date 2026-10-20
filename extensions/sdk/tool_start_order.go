package sdk

import "sync"

// toolStartOrder orders the calls of one tool batch the way Pi's single thread does.
//
// Pi starts every call of a parallel batch through `Promise.all(calls.map(call => run(call)))`, and each reaches tool.execute synchronously, so the handlers start in source
// order (agent-loop.ts:619-647, 820-837). The host writes a batch's tool_call requests in source order, but the loop runs each request on its own goroutine, and the
// scheduler is free to start the goroutine of a later request first. Three orders follow from Pi's one thread, and each is kept here:
//
//   - Start: a request's handler is invoked only after the handler of every earlier request was invoked or its request ended without one. A handler is Go code on a
//     preemptible thread, so a thread that the operating system preempts between its hand-off and its handler's first statement can still be overtaken, and no Go
//     construct closes that window.
//   - Dialog: the first dialog (select, confirm, input, editor, custom) a handler opens is written only after the first dialog of every earlier request was written or its
//     request ended, so the dialogs of a batch open in the order the calls started. A handler that waits on something else before its dialog holds the later calls' first
//     dialogs, which Pi's single thread lets overtake it; that is the price of an order the handlers' own code cannot be asked for.
//   - Continuation: when two dialogs are answered back to back, Pi runs the two handlers' continuations in the order of the answers, and each continuation runs to its
//     next await or its return before the next one starts. The reader settles a handler's slot when it reads the answer to the handler's dialog, and the handler's
//     response is written only after the response of every handler that settled before it, or after that handler's next host call (the await its continuation reached).
type toolStartOrder struct {
	mu         sync.Mutex
	tail       chan struct{}
	dialogTail chan struct{}
	// settle orders the responses of the handlers whose dialogs were answered.
	settle settleGate
	// parked, when set, runs each time a call starts to wait for an earlier one, so a test knows it is held without timing.
	parked func()
}

// toolStart is one request's place in the orders. begin returns when every earlier request began its handler or ended without one.
type toolStart struct {
	previous <-chan struct{}
	done     chan struct{}
	once     sync.Once

	dialogPrevious <-chan struct{}
	dialogDone     chan struct{}
	dialogOnce     sync.Once
	dialogOpened   sync.Once
	slot           *settleSlot
	parked         func()
}

// reserve takes the next place. The loop calls it in arrival order, before it starts the request's goroutine.
func (o *toolStartOrder) reserve() *toolStart {
	start := &toolStart{done: make(chan struct{}), dialogDone: make(chan struct{})}
	o.mu.Lock()
	start.previous = o.tail
	o.tail = start.done
	start.dialogPrevious = o.dialogTail
	o.dialogTail = start.dialogDone
	start.parked = o.parked
	o.mu.Unlock()
	start.slot = o.settle.newSlot()
	start.slot.parked = o.parked
	return start
}

// begin waits for the earlier requests and then hands the place on, so the next handler starts after this one. It is idempotent.
func (s *toolStart) begin() {
	if s == nil {
		return
	}
	if s.previous != nil {
		<-s.previous
	}
	s.once.Do(func() { close(s.done) })
}

// release hands the places on for a request that ended, also one that failed before it reached its handler. Like begin it waits for the
// earlier requests first, so a request without a handler cannot let a later handler start before an earlier one. It is idempotent.
func (s *toolStart) release() {
	if s == nil {
		return
	}
	s.begin()
	s.passDialog()
	s.slot.close()
}

// beforeCall runs before the handler writes a host call. The call is an await of the handler's continuation: a handler the reader settled yields its place in
// the response order. A handler's first dialog waits for the first dialogs of the earlier requests; onDialog reports whether the call is one.
func (s *toolStart) beforeCall(dialog bool) {
	if s == nil {
		return
	}
	s.slot.yield()
	if !dialog {
		return
	}
	first := false
	s.dialogOpened.Do(func() { first = true })
	if first && s.dialogPrevious != nil {
		select {
		case <-s.dialogPrevious:
		default:
			if s.parked != nil {
				s.parked()
			}
			<-s.dialogPrevious
		}
	}
}

// afterCall runs once the host call's frame is written, or failed to be.
func (s *toolStart) afterCall(dialog bool) {
	if s == nil || !dialog {
		return
	}
	s.passDialog()
}

// passDialog hands the dialog place on after the earlier requests passed theirs, so the chain stays in request order. It is idempotent.
func (s *toolStart) passDialog() {
	if s.dialogPrevious != nil {
		<-s.dialogPrevious
	}
	s.dialogOnce.Do(func() { close(s.dialogDone) })
}

// respond writes the request's response after the response of every handler whose dialog answer was read before this handler's.
func (s *toolStart) respond(send func()) {
	if s == nil {
		send()
		return
	}
	s.slot.run(send)
}

// isDialogMethod reports whether a host call opens a dialog the user answers.
func isDialogMethod(method string) bool {
	switch method {
	case "ui.select", "ui.confirm", "ui.input", "ui.editor", "ui.custom":
		return true
	}
	return false
}

// settleGate orders actions by the order their slots settled: a slot settles when the reader reads the answer its handler waits for, and its action runs after every
// slot that settled before it finished. A slot that was never settled runs at once.
type settleGate struct {
	mu   sync.Mutex
	turn sync.Cond
	// settled holds the slots that settled and have not yet finished or yielded, in settle order.
	settled []*settleSlot
}

type settleSlot struct {
	gate     *settleGate
	settled  bool
	finished bool
	parked   func()
}

func (g *settleGate) newSlot() *settleSlot { return &settleSlot{gate: g} }

func (g *settleGate) waitTurnLocked(s *settleSlot) {
	if g.turn.L == nil {
		g.turn.L = &g.mu
	}
	for s.settled && !s.finished && g.settled[0] != s {
		if s.parked != nil {
			s.parked()
		}
		g.turn.Wait()
	}
}

// settle gives the slot its place after every slot that settled before. The reader calls it before it hands the answer to the waiting handler. A slot that already
// holds a place keeps it.
func (s *settleSlot) settle() {
	if s == nil {
		return
	}
	g := s.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if s.settled || s.finished {
		return
	}
	s.settled = true
	g.settled = append(g.settled, s)
}

// yield gives the place up: the handler's continuation reached its next await, so the handlers that settled after it go on. The next answer settles the slot again.
func (s *settleSlot) yield() {
	if s == nil {
		return
	}
	g := s.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	s.leaveLocked()
}

func (s *settleSlot) leaveLocked() {
	if !s.settled {
		return
	}
	g := s.gate
	for i, held := range g.settled {
		if held == s {
			g.settled = append(g.settled[:i], g.settled[i+1:]...)
			break
		}
	}
	s.settled = false
	if g.turn.L == nil {
		g.turn.L = &g.mu
	}
	g.turn.Broadcast()
}

// run runs action after every slot that settled before this one left its place. A slot that holds no place runs it at once.
func (s *settleSlot) run(action func()) {
	g := s.gate
	g.mu.Lock()
	g.waitTurnLocked(s)
	g.mu.Unlock()
	defer s.close()
	action()
}

// close ends the slot: it stops holding up the slots behind it. It is idempotent.
func (s *settleSlot) close() {
	if s == nil {
		return
	}
	g := s.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	s.leaveLocked()
	s.finished = true
}
