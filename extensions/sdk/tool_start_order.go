package sdk

import (
	"sync"
	"sync/atomic"
)

// toolStartOrder orders the calls of one tool batch the way Pi's single thread does.
//
// Pi starts every call of a parallel batch through `Promise.all(calls.map(call => run(call)))`, and each reaches tool.execute synchronously, so the handlers start in source
// order (agent-loop.ts:619-647, 820-837). The host writes a batch's tool_call requests in source order, but the loop runs each request on its own goroutine, and the
// scheduler is free to start the goroutine of a later request first. Three orders follow from Pi's one thread, and each is kept here:
//
//   - Start: a request's handler is invoked only after the handler of every earlier request was invoked or its request ended without one. A handler is Go code on a
//     preemptible thread, so a thread that the operating system preempts between its hand-off and its handler's first statement can still be overtaken, and no Go
//     construct closes that window.
//   - Dialog: a handler's first host call, of any kind, is written only after the first host call of every earlier request was written or its request ended, and a
//     request passes its dialog place on once that first call is written, so the first dialogs of a batch open in the order the calls started, and a call that awaits an
//     exec before its dialog is overtaken by the later calls' dialogs, as in Pi. A handler that waits on something the SDK does not see (its own fetch, timer or child
//     process) before its first host call holds the later calls' first host calls, which Pi's single thread lets overtake it; that is the price of an order the handlers'
//     own code cannot be asked for.
//   - Continuation: when two dialogs are answered back to back, Pi runs the two handlers' continuations in the order of the answers, and each continuation runs to its
//     next await or its return before the next one starts. The reader settles a handler's slot when it reads the answer to the handler's dialog, and the handler's
//     response is written only after the response of every handler that settled before it, or after that handler's next host call (the await its continuation reached).
type toolStartOrder struct {
	mu   sync.Mutex
	tail chan struct{}
	// dialogQueue holds the requests whose dialog place has not passed, in arrival order. The first is the one whose first host call may be written; a request passes its
	// place once that call is written, whatever the call is.
	dialogQueue []*toolStart
	// settle orders the responses of the handlers whose dialogs were answered.
	settle settleGate
	// parked, when set, runs each time a call starts to wait for an earlier one, so a test knows it is held without timing.
	parked func()
}

// toolBatchOrders keeps one toolStartOrder for each parallel batch in flight. The orders are Pi's single thread's orders of one batch's calls: another batch, another
// session's calls (a subagent's tool calls on this connection) or a call outside a batch has no place in them, and a long call of one batch never holds a dialog of
// another. A request without a batch takes part in no order.
type toolBatchOrders struct {
	mu      sync.Mutex
	batches map[string]*toolBatch
}

type toolBatch struct {
	order toolStartOrder
	live  int
}

// reserve takes the next place in the batch's order, in the order the requests arrive. It returns nil for a request that names no batch.
func (b *toolBatchOrders) reserve(id string) *toolStart {
	if id == "" {
		return nil
	}
	b.mu.Lock()
	if b.batches == nil {
		b.batches = make(map[string]*toolBatch)
	}
	batch := b.batches[id]
	if batch == nil {
		batch = &toolBatch{}
		b.batches[id] = batch
	}
	batch.live++
	b.mu.Unlock()
	start := batch.order.reserve()
	start.owner, start.batchID = b, id
	return start
}

// ended forgets a batch once its last request ended; a request of the batch that arrives later finds a new order, with nothing ahead of it.
func (b *toolBatchOrders) ended(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if batch := b.batches[id]; batch != nil {
		if batch.live--; batch.live <= 0 {
			delete(b.batches, id)
		}
	}
}

// toolStart is one request's place in the orders. begin returns when every earlier request began its handler or ended without one.
type toolStart struct {
	previous <-chan struct{}
	done     chan struct{}
	once     sync.Once

	order   *toolStartOrder
	owner   *toolBatchOrders
	batchID string
	// dialogWaiting is closed when the request waits for its first host call and every earlier request passed its place; it is made only when the request has to wait,
	// and with dialogPassed guarded by order.mu.
	dialogWaiting chan struct{}
	dialogPassed  bool
	ended         bool
	dialogOpened  atomic.Bool
	// slot is the request's place in the response order, made when the request opens a dialog: a handler that opens none is never gated.
	slot   atomic.Pointer[settleSlot]
	parked func()
}

// reserve takes the next place. The loop calls it in arrival order, before it starts the request's goroutine.
func (o *toolStartOrder) reserve() *toolStart {
	start := &toolStart{done: make(chan struct{}), order: o}
	o.mu.Lock()
	start.previous = o.tail
	o.tail = start.done
	o.dialogQueue = append(o.dialogQueue, start)
	start.parked = o.parked
	o.mu.Unlock()
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
	o := s.order
	o.mu.Lock()
	first := !s.ended
	s.ended = true
	slot := s.slot.Load()
	o.mu.Unlock()
	slot.close()
	if first && s.owner != nil {
		s.owner.ended(s.batchID)
	}
}

// responseSlot returns the request's place in the response order, made on first use. A request that ended has none: a dialog a retained Context opens
// after the handler returned must not leave a place that nothing closes.
func (s *toolStart) responseSlot() *settleSlot {
	o := s.order
	o.mu.Lock()
	defer o.mu.Unlock()
	if s.ended {
		return nil
	}
	if slot := s.slot.Load(); slot != nil {
		return slot
	}
	slot := o.settle.newSlot()
	slot.parked = s.parked
	s.slot.Store(slot)
	return slot
}

// beforeCall runs before the handler writes a host call, of any kind. The call is an await of the handler's continuation: a handler the reader settled yields its place in
// the response order. A handler's first host call waits for the first host calls of the earlier requests, and afterCall then passes its dialog place on, as Pi's single thread
// lets a later call run once an earlier call reached its first await.
func (s *toolStart) beforeCall() {
	if s == nil {
		return
	}
	s.slot.Load().yield()
	if !s.dialogOpened.CompareAndSwap(false, true) {
		return
	}
	// pig divergence (D187): the first host call of a call waits for the first host calls of the earlier calls of its batch, also while an earlier call is blocked in work the SDK cannot see (its own fetch, timer or child process); Pi's single thread lets a later call's dialog overtake an earlier call that awaits real I/O.
	o := s.order
	o.mu.Lock()
	if len(o.dialogQueue) == 0 || o.dialogQueue[0] == s || s.dialogPassed {
		o.mu.Unlock()
		return
	}
	s.dialogWaiting = make(chan struct{})
	waiting := s.dialogWaiting
	o.mu.Unlock()
	if s.parked != nil {
		s.parked()
	}
	<-waiting
}

// afterCall runs once the host call's frame is written, or failed to be. The first host call of any kind gives the dialog place up: Pi's handler that awaits an exec has
// yielded the thread, so a later call's dialog may open before the handler's own.
func (s *toolStart) afterCall() {
	if s == nil {
		return
	}
	s.passDialog()
}

// passDialog gives up the request's dialog place: the first request that still holds one may write its first host call. It never waits, so a request that ends
// while an earlier one still runs is not held by it. It is idempotent.
func (s *toolStart) passDialog() {
	o := s.order
	o.mu.Lock()
	defer o.mu.Unlock()
	if s.dialogPassed {
		return
	}
	s.dialogPassed = true
	for i, held := range o.dialogQueue {
		if held == s {
			o.dialogQueue = append(o.dialogQueue[:i], o.dialogQueue[i+1:]...)
			break
		}
	}
	if len(o.dialogQueue) > 0 {
		if head := o.dialogQueue[0]; head.dialogWaiting != nil {
			close(head.dialogWaiting)
			head.dialogWaiting = nil
		}
	}
}

// respond writes the request's response after the response of every handler whose dialog answer was read before this handler's.
func (s *toolStart) respond(send func()) {
	if s == nil {
		send()
		return
	}
	// pig divergence (D187): the response waits for the handlers whose dialog answers were read earlier, until each returns or makes its next host call; Pi's continuations run to their next await, never to the end of blocking work.
	s.slot.Load().run(send)
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

// run runs action after every slot that settled before this one left its place. A slot that holds no place runs it at once, and so does no slot at all.
func (s *settleSlot) run(action func()) {
	if s == nil {
		action()
		return
	}
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
