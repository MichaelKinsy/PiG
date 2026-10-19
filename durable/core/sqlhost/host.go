// SPDX-License-Identifier: MIT

package sqlhost

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
)

// ErrClosed is returned for an event sent after Close.
var ErrClosed = errors.New("sqlhost: the host is closed")

// FatalError is the reason a host discarded its handle (ABI section 4.6, ADR D16). The next event needs a new Host: a
// cold open from SQLite loses nothing Pi would keep.
type FatalError struct {
	Message string
	Cause   error
}

// FatalCommitFailed is the Message of the FatalError a failed commit returns. The store may have taken the commit, so a
// caller treats the session as poisoned (Pi session.ts #runCommit).
const FatalCommitFailed = "a commit failed"

func (e *FatalError) Error() string {
	if e.Cause != nil {
		return "sqlhost: fatal: " + e.Message + ": " + e.Cause.Error()
	}
	return "sqlhost: fatal: " + e.Message
}

// Unwrap returns the underlying error.
func (e *FatalError) Unwrap() error { return e.Cause }

// RejectedError is a step with status rejected: Pi's error name and message, and no state changed.
type RejectedError struct {
	Name    string
	Message string
	Raw     []byte
}

func (e *RejectedError) Error() string {
	if e.Message == "" {
		return e.Name
	}
	return e.Name + ": " + e.Message
}

func rejectedOf(raw []byte) *RejectedError {
	var parsed struct{ Name, Message string }
	_ = json.Unmarshal(raw, &parsed)
	e := &RejectedError{Name: parsed.Name, Message: parsed.Message, Raw: append([]byte(nil), raw...)}
	if e.Name == "" {
		e.Name = "Rejected"
	}
	if e.Message == "" {
		e.Message = string(raw)
	}
	return e
}

// Handler runs one effect. It returns when the effect is complete or its ctx is canceled. Completions travel back through
// call.Post. A handler that returns an error other than a context cancellation discards the handle: the host does not guess
// the outcome of work the core is waiting on.
type Handler func(ctx context.Context, call *Call) error

// Call is one started effect.
type Call struct {
	ID      uint32
	Kind    abi.EffectKind
	Payload []byte // the host's copy
	// Range is the committed context a model_context effect names. Its messages are not in Payload (the list there is empty):
	// the handler decodes them from Range, and only those whose identity changed since its last call.
	Range *MessageRange
	host  *Host
}

// Post delivers an event the effect produced (model_event, tool_progress, tool_done, hook_done, ...). Now is stamped when
// the owner delivers it. A completion that arrives after the handle was discarded is dropped.
func (c *Call) Post(ev abi.Event) { c.host.Post(ev) }

// Expect registers a wait for a notice before the effect posts the event that provokes it. See Host.Expect.
func (c *Call) Expect(test func(abi.Notice) bool) (*Expectation, error) { return c.host.Expect(test) }

// Counters are the host's measurements (BAKEOFF M6, M7).
type Counters struct {
	Events     int
	Steps      int
	Commits    int
	Statements int
	Reads      int
	RowsRead   int
	// ContextReads and ContextRows are the reads the host ran to derive the contexts it holds (range requests and the
	// core's context reads) and the rows they returned: they do not cross the core's pig.read, so Reads and RowsRead exclude them.
	ContextReads int
	ContextRows  int
	Notices      int
	Rejected     int
	// WaiterTests counts the pending waiters notify tested against a notice.
	WaiterTests int
	Effects     map[abi.EffectKind]int
}

// Stepper is the native binding of a core (ABI section 11): core.Session and the probe implement it.
type Stepper interface {
	Step(ev abi.Event) *abi.Step
}

// Options configure a Host.
type Options struct {
	// Core is the session the owner goroutine drives.
	Core Stepper
	// SQL is the core's sql_table: statement ID to text.
	SQL []string
	// DB is the connection commits and reads run on. The Host uses it from the owner goroutine only.
	DB *DB
	// OwnDB closes DB when the Host closes.
	OwnDB bool
	// Clock returns milliseconds since the epoch. Every event carries it as now (ADR D8). Defaults to the wall clock.
	Clock func() float64
	// TimerFired, when set, runs as a timer fires, before its event is queued, with the time the timer was armed for. A
	// virtual clock (the conformance gate's detenv) moves there: virtual time advances only when a timer is due, so a
	// frozen clock would otherwise deliver every timer before its time and the core would re-arm it forever.
	TimerFired func(at float64)
	// Handlers start effects by kind. Timer, timer_clear, cancel and liveness are the host's own and need none.
	Handlers map[abi.EffectKind]Handler
	// OnNotice receives each notice on the owner goroutine, after the step's commits are applied. It must not block.
	OnNotice func(abi.Notice)
	// TimerLead makes a timer fire this much before its deadline. A deterministic clock sets 1ns so that, among timers and
	// effect completions due at the same instant, the host's timers run first (the order the reference's seq-ordered virtual
	// timers give a timer armed before the completion's own). Zero fires at the deadline.
	TimerLead time.Duration
	// Settle, when set, is called by the owner after it starts an effect's goroutine, before it takes the next event, and at
	// each hop of a hops chain. A deterministic run (a synctest bubble) sets synctest.Wait so that an effect that completes
	// without waiting posts its completion before the next effect starts (completions then reach the core in the order the
	// effects were issued, as they do from the reference's FIFO of resolved promises), and so that host code a step woke
	// posts before the chain's next hop.
	Settle func()
	// OnCancel receives each cancel effect (ABI effect 5) on the owner goroutine, also for an effect that has already
	// settled: the core cancels a settled phase effect to say its invocation has ended. It must not block.
	OnCancel func(effect uint32)
	// SetTimer, when set, arms every timer effect (HarnessOptions.SetTimer): callback fires once about delayMs from now, and
	// the returned function disarms it. background marks a timer that only frees memory, which it may decline by returning
	// nil; any other it arms or the host arms with a Go timer. Nil arms Go timers.
	SetTimer func(callback func(), delayMs float64, background bool) (disarm func())
	// OnLiveness receives the watchdog target (ABI effect 10): a time, or -1 when nothing is live. A native process
	// has nothing to wake, so the default ignores it.
	OnLiveness func(at float64)
	// OnFatal is called once, from the owner goroutine, when the handle is discarded.
	OnFatal func(error)
	// OnRejected receives a rejected step that no caller waits on: an effect completion the core refused.
	OnRejected func(kind abi.EventKind, err *RejectedError)
	// Settings, when set, returns the host's current resolved settings as JSON, or nil when they cannot be read now. Pi reads HarnessSettings at each use; the
	// core holds the copy open sent, so before each Send but open and close (every API call, not hops or effect
	// completions, which keeps the hot path free of it) the owner compares and, when they changed, delivers a registry
	// event {"settings": ...} first (host/cf/src/host.ts enqueue).
	Settings func() []byte
	// OnCommit observes every commit before it applies (trace capture).
	OnCommit func(c abi.Commit, sql []string)
	// OnCommitted observes every commit after the store applied it, in order. A trace that must hold exactly what the store
	// holds writes here: a process killed in this callback has the commit in the store and in the trace.
	OnCommitted func(c abi.Commit, sql []string)
}

// Host is one core session and its SQLite connection.
type Host struct {
	opts   Options
	box    *mailbox
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // effect goroutines
	active int            // effect goroutines running; guarded by mu
	quiet  chan struct{}  // closed when active reaches zero while WaitEffects waits; guarded by mu

	mu       sync.Mutex
	dead     error
	closed   bool
	counters Counters
	waiters  map[*waiter]struct{}               // the waiters Expect registered: every notice is tested against each
	byKey    map[noticeKey]map[*waiter]struct{} // the waiters ExpectNotice registered: a notice is tested against its own key's

	// owner-only state
	// lastEffect is the ID of the last effect started. The core issues effect IDs in increasing order for the life of a
	// handle (ABI section 3, invariant 6), so an ID at or below it was issued twice (-1 before the first); one number replaces a set of every
	// ID ever started, which grew with every effect of the handle's life.
	lastEffect int64
	running    map[uint32]context.CancelFunc
	// sentSettings is the settings JSON the core last received (open or a registry event); owner goroutine only.
	sentSettings []byte
	timers       map[uint32]func()
	ranges       *history.Store // the transcript a model_context range segment derives from
	// sent is each conversation's last model_context range (rangeContext), which the next one extends; rangeLineages numbers
	// the arrays it hands out (MessageRange.Lineage).
	sent          map[int64]*sentRange
	rangeLineages uint64
}

// noticeKey names the notice an ExpectNotice waiter is for: the kind and the u32 that starts the payload.
type noticeKey struct {
	kind abi.NoticeKind
	id   uint32
}

type waiter struct {
	test    func(abi.Notice) bool
	key     noticeKey
	keyed   bool
	resolve chan abi.Notice
	fail    chan error
}

// wallClock is the default Clock: whole milliseconds since the epoch, as the JS host's Date.now() (host/cf host.ts).
// Every event carries it as now, and the core stamps entries and records with it, so a fractional value would write
// timestamps Pi never writes (and that the driver's int64 message timestamps cannot decode).
func wallClock() float64 { return float64(time.Now().UnixMilli()) }

// New starts the owner goroutine. Send the open event next.
func New(opts Options) *Host {
	if opts.Clock == nil {
		opts.Clock = wallClock
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Host{
		opts: opts, box: newMailbox(), done: make(chan struct{}), ctx: ctx, cancel: cancel,
		waiters: map[*waiter]struct{}{}, byKey: map[noticeKey]map[*waiter]struct{}{}, lastEffect: -1, running: map[uint32]context.CancelFunc{}, timers: map[uint32]func(){},
	}
	h.counters.Effects = map[abi.EffectKind]int{}
	// A core that reads settled records on demand (ABI section 4.2, pig.read) reads them here, on the owner goroutine that
	// is running its step.
	if r, ok := opts.Core.(interface {
		SetReader(func(sql uint16, params []abi.Value) ([][]abi.Value, error))
	}); ok {
		r.SetReader(h.readSync)
	}
	//portlint:allow golifetime run ends when the mailbox closes and then closes h.done, which Close waits on
	go h.run()
	return h
}

// readSync answers one pig.read call of the core's running step.
func (h *Host) readSync(sql uint16, params []abi.Value) ([][]abi.Value, error) {
	answers, err := h.read([]abi.Read{{SQL: sql, Params: params}})
	if err != nil {
		return nil, err
	}
	return answers[0].Rows, nil
}

func (h *Host) run() {
	//portlint:allow doubleclose New starts run exactly once, so done is closed exactly once
	defer close(h.done)
	for {
		job, ok := h.box.take()
		if !ok {
			return
		}
		job(nil)
	}
}

// Err returns the reason the handle was discarded, or nil.
func (h *Host) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dead
}

// Counters returns a snapshot.
func (h *Host) Counters() Counters {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.counters
	c.Effects = map[abi.EffectKind]int{}
	maps.Copy(c.Effects, h.counters.Effects)
	return c
}

func (h *Host) count(f func(c *Counters)) {
	h.mu.Lock()
	f(&h.counters)
	h.mu.Unlock()
}

// Send delivers one event and returns when the owner has processed it, its read rounds and its commits included. The error
// is a *RejectedError for a rejected step, a *FatalError when the handle was or is discarded, or ErrClosed. When ctx ends
// first Send returns the context cause; the queued event still runs, because an event cannot be retracted from a core.
func (h *Host) Send(ctx context.Context, ev abi.Event) error {
	reply := make(chan error, 1)
	if !h.enqueue(func(dead error) {
		if dead != nil {
			reply <- dead
			return
		}
		if err := h.syncSettings(ev.Kind); err != nil {
			reply <- err
			return
		}
		reply <- h.deliver(ev, true)
	}) {
		return h.closedErr()
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Post queues an event without waiting. It never blocks. A rejected step goes to OnRejected.
func (h *Host) Post(ev abi.Event) {
	h.enqueue(func(dead error) {
		if dead == nil {
			_ = h.deliver(ev, false)
		}
	})
}

// WaitEffects returns once every effect handler that is running has returned, while the owner keeps delivering events (the
// completions the handlers post still reach the core). It is the join of Harness.close() before the core closes: a run's end
// reaches the core, which ends its invocation and answers what joined it.
func (h *Host) WaitEffects(ctx context.Context) error {
	// A WaitGroup cannot serve here: the owner keeps starting effects, and Add from zero must not race a Wait.
	for {
		h.mu.Lock()
		if h.active == 0 {
			h.mu.Unlock()
			return nil
		}
		if h.quiet == nil {
			h.quiet = make(chan struct{})
		}
		quiet := h.quiet
		h.mu.Unlock()
		select {
		case <-quiet:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// effectDone ends one effect goroutine's count and wakes WaitEffects when none runs.
func (h *Host) effectDone() {
	h.mu.Lock()
	h.active--
	if h.active == 0 && h.quiet != nil {
		close(h.quiet)
		h.quiet = nil
	}
	h.mu.Unlock()
}

// Query runs a read on the owner's connection, after every event queued before it. It serves Harness reads that need no
// core state (CONTRACT section 3.10): old history pages, task and submission lookups.
func (h *Host) Query(ctx context.Context, text string, params ...abi.Value) ([][]abi.Value, error) {
	type result struct {
		rows [][]abi.Value
		err  error
	}
	reply := make(chan result, 1)
	if !h.enqueue(func(dead error) {
		if dead != nil {
			reply <- result{err: dead}
			return
		}
		rows, _, err := h.opts.DB.All(h.ctx, text, params)
		reply <- result{rows, err}
	}) {
		return nil, h.closedErr()
	}
	select {
	case r := <-reply:
		return r.rows, r.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (h *Host) enqueue(job func(dead error)) bool {
	h.mu.Lock()
	if h.closed || h.dead != nil {
		h.mu.Unlock()
		return false
	}
	ok := h.box.put(job)
	h.mu.Unlock()
	return ok
}

func (h *Host) closedErr() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dead != nil {
		return h.dead
	}
	return ErrClosed
}

// WaitNotice resolves with the first notice delivered after the call that satisfies test. It fails when the handle is
// discarded first. test runs on the owner goroutine and must not block.
func (h *Host) WaitNotice(ctx context.Context, test func(abi.Notice) bool) (abi.Notice, error) {
	e, err := h.Expect(test)
	if err != nil {
		return abi.Notice{}, err
	}
	return e.Wait(ctx)
}

// Expectation is a registered wait for one notice.
type Expectation struct {
	h *Host
	w *waiter
}

// Expect registers a wait before the caller sends the event that provokes the notice, so the notice cannot be missed.
// Wait or Cancel must follow. test runs on the owner goroutine and must not block.
func (h *Host) Expect(test func(abi.Notice) bool) (*Expectation, error) {
	w := &waiter{test: test, resolve: make(chan abi.Notice, 1), fail: make(chan error, 1)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dead != nil || h.closed {
		return nil, h.closedErrLocked()
	}
	h.waiters[w] = struct{}{}
	return &Expectation{h: h, w: w}, nil
}

// ExpectNotice is Expect for a notice of the given kind whose payload starts with the u32 id (api_result, tx_result);
// accept sees the payload after the id. A notice is tested only against the waiters of its own kind and id, so the cost
// of a notice does not grow with the number of requests in flight.
func (h *Host) ExpectNotice(kind abi.NoticeKind, id uint32, accept func(body []byte) bool) (*Expectation, error) {
	w := &waiter{key: noticeKey{kind, id}, keyed: true, resolve: make(chan abi.Notice, 1), fail: make(chan error, 1)}
	w.test = func(n abi.Notice) bool { return accept(n.Payload[4:]) }
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dead != nil || h.closed {
		return nil, h.closedErrLocked()
	}
	set := h.byKey[w.key]
	if set == nil {
		set = map[*waiter]struct{}{}
		h.byKey[w.key] = set
	}
	set[w] = struct{}{}
	return &Expectation{h: h, w: w}, nil
}

// removeWaiterLocked drops w from whichever structure holds it. h.mu must be held.
func (h *Host) removeWaiterLocked(w *waiter) {
	if !w.keyed {
		delete(h.waiters, w)
		return
	}
	if set := h.byKey[w.key]; set != nil {
		delete(set, w)
		if len(set) == 0 {
			delete(h.byKey, w.key)
		}
	}
}

// takeWaitersLocked empties both structures and returns every waiter they held. h.mu must be held.
func (h *Host) takeWaitersLocked() []*waiter {
	all := make([]*waiter, 0, len(h.waiters))
	for w := range h.waiters {
		all = append(all, w)
	}
	for _, set := range h.byKey {
		for w := range set {
			all = append(all, w)
		}
	}
	h.waiters = map[*waiter]struct{}{}
	h.byKey = map[noticeKey]map[*waiter]struct{}{}
	return all
}

// Wait resolves with the notice, or fails when ctx ends or the handle is discarded first. It releases the registration.
func (e *Expectation) Wait(ctx context.Context) (abi.Notice, error) {
	defer e.Cancel()
	select {
	case n := <-e.w.resolve:
		return n, nil
	case err := <-e.w.fail:
		return abi.Notice{}, err
	case <-ctx.Done():
		return abi.Notice{}, context.Cause(ctx)
	}
}

// Cancel releases the registration. It is safe after Wait.
func (e *Expectation) Cancel() {
	e.h.mu.Lock()
	e.h.removeWaiterLocked(e.w)
	e.h.mu.Unlock()
}

func (h *Host) closedErrLocked() error {
	if h.dead != nil {
		return h.dead
	}
	return ErrClosed
}

func (h *Host) fatal(message string, cause error) error {
	err := &FatalError{Message: message, Cause: cause}
	h.discard(err)
	return err
}

// discard is the failure path of ABI section 4.6: cancel the effects, drop late completions, fail the waiters.
func (h *Host) discard(err error) {
	h.mu.Lock()
	if h.dead != nil {
		h.mu.Unlock()
		return
	}
	h.dead = err
	waiters := h.takeWaitersLocked()
	h.mu.Unlock()
	h.cancel()
	for _, stop := range h.timers {
		stop()
	}
	h.timers = map[uint32]func(){}
	h.running = map[uint32]context.CancelFunc{}
	for _, w := range waiters {
		select {
		case w.fail <- err:
		default:
		}
	}
	if h.opts.OnFatal != nil {
		if _, ok := errors.AsType[*FatalError](err); ok {
			h.opts.OnFatal(err)
		}
	}
}

// step calls the core and turns a panic into a fatal error: a Wasm trap discards the instance, and a native panic
// discards the session for the same reason.
func (h *Host) step(ev abi.Event) (s *abi.Step, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the core panicked: %v", r)
		}
	}()
	h.count(func(c *Counters) { c.Events++ })
	return h.opts.Core.Step(ev), nil
}

func (h *Host) stamp(ev abi.Event) abi.Event {
	if ev.Now == 0 {
		ev.Now = h.opts.Clock()
	}
	return ev
}

// deliver processes one event to completion. direct reports whether a caller waits on the result.
func (h *Host) deliver(ev abi.Event, direct bool) error {
	if err := h.Err(); err != nil {
		return err
	}
	ev = h.stamp(ev)
	var first error
	for {
		step, err := h.step(ev)
		if err != nil {
			return h.fatal(fmt.Sprintf("the core failed on a %d event", ev.Kind), err)
		}
		h.count(func(c *Counters) { c.Steps++ })
		switch step.Status {
		case abi.StatusRejected:
			rej := rejectedOf(step.Err)
			h.count(func(c *Counters) { c.Rejected++ })
			if direct {
				if first == nil {
					first = rej
				}
			} else if h.opts.OnRejected != nil {
				h.opts.OnRejected(ev.Kind, rej)
			}
			return first
		case abi.StatusFatal:
			return h.fatal("the core reported a fatal step", errors.New(string(step.Err)))
		}
		if len(step.Reads) > 0 {
			if len(step.Effects) > 0 {
				return h.fatal("a step with reads must have no effects", nil)
			}
			// ABI section 3, invariant 3: the step's commits are applied, in order, before its reads run (cold open's schema
			// prelude).
			if err := h.applyCommits(step.Commits); err != nil {
				return h.fatal(FatalCommitFailed, err)
			}
			answers, err := h.read(step.Reads)
			if err != nil {
				return h.fatal("a read failed", err)
			}
			ev = abi.Event{Kind: abi.EventRows, Rows: answers, Now: h.opts.Clock()}
			continue
		}
		if err := h.applyCommits(step.Commits); err != nil {
			return h.fatal(FatalCommitFailed, err)
		}
		if err := h.apply(step); err != nil {
			return err
		}
		return first
	}
}

// apply runs a step's notices and effects after its commits: in the step's Order when the core listed one (ABI section 5.3: the
// core's synchronous order, which decides where a chain it starts queues against the host's own work), otherwise all notices,
// then all effects.
func (h *Host) apply(step *abi.Step) error {
	notice := func(n abi.Notice) { h.notify(abi.Notice{Kind: n.Kind, Payload: owned(n.Data(), n.Chunks != nil)}) }
	effect := func(e abi.Effect) error {
		if err := h.Err(); err != nil {
			return err
		}
		h.start(abi.Effect{ID: e.ID, Kind: e.Kind, Payload: owned(e.Data(), e.Chunks != nil)})
		return nil
	}
	if len(step.Order) > 0 {
		for _, ref := range step.Order {
			if ref.Region == abi.OrderNotice {
				if int(ref.Index) >= len(step.Notices) {
					return h.fatal("a step's order names a notice it does not have", nil)
				}
				notice(step.Notices[ref.Index])
				continue
			}
			if int(ref.Index) >= len(step.Effects) {
				return h.fatal("a step's order names an effect it does not have", nil)
			}
			if err := effect(step.Effects[ref.Index]); err != nil {
				return err
			}
		}
		return h.Err()
	}
	for _, n := range step.Notices {
		notice(n)
	}
	for _, e := range step.Effects {
		if err := effect(e); err != nil {
			return err
		}
	}
	return h.Err()
}

// owned is the host's copy of a notice's or effect's payload data: Data's join of a chunked payload is one already; a
// payload in one slice is the core's, so it is copied.
func owned(data []byte, chunked bool) []byte {
	if chunked {
		return data
	}
	return append([]byte(nil), data...)
}

// startHops runs a hops effect (ABI section 5.3): n hops of the owner's FIFO, then a hopped event. The mailbox is that FIFO:
// hop 1 queues at its tail, each hop queues the next at the tail, and events that handlers and timers post meanwhile queue
// at their arrival positions, so the nth hop runs after exactly the work that arrived before it was queued, as a chain of
// microtasks does. cancel drops the chain; so does a closed or discarded host.
func (h *Host) startHops(id uint32, c abi.Hops) {
	var dropped bool
	h.running[id] = func() { dropped = true }
	left := c.N
	var hop func(error)
	hop = func(dead error) {
		if dead == nil && h.opts.Settle != nil {
			// A deterministic run lets host code woken since the last hop (a caller resuming on a delivered result, a handler
			// returning) run until it blocks, so what it posts queues ahead of the next hop, as its microtasks do in Pi.
			h.opts.Settle()
		}
		if dropped || dead != nil || h.Err() != nil {
			delete(h.running, id)
			return
		}
		if left--; left > 0 {
			if c.At != 0 && c.N-left == c.At {
				// The chain's statements land after hop At, where the core emitted them in the step of a hopped event.
				if err := h.applyCommits(c.Commits); err != nil {
					delete(h.running, id)
					_ = h.fatal(FatalCommitFailed, err)
					return
				}
			}
			h.box.put(hop)
			return
		}
		delete(h.running, id)
		_ = h.deliver(abi.Event{Kind: abi.EventHopped, ID: id}, false)
	}
	h.box.put(hop)
}

func (h *Host) read(reads []abi.Read) ([]abi.ReadRows, error) {
	answers := make([]abi.ReadRows, 0, len(reads))
	for _, r := range reads {
		if int(r.SQL) >= len(h.opts.SQL) {
			return nil, fmt.Errorf("read %d: %w", r.ID, errNoStatement)
		}
		if text := h.opts.SQL[r.SQL]; isContextRead(text) {
			rows, err := h.answerContext(text, r.Params)
			if err != nil {
				return nil, err
			}
			h.count(func(c *Counters) { c.Reads++ })
			cols := 0
			if len(rows) != 0 {
				cols = len(rows[0])
			}
			answers = append(answers, abi.ReadRows{ID: r.ID, Cols: uint8(cols), Rows: rows})
			continue
		}
		rows, cols, err := h.opts.DB.All(h.ctx, h.opts.SQL[r.SQL], r.Params)
		if err != nil {
			return nil, err
		}
		h.count(func(c *Counters) { c.Reads++; c.RowsRead += len(rows) })
		answers = append(answers, abi.ReadRows{ID: r.ID, Cols: uint8(cols), Rows: rows})
	}
	return answers, nil
}

func (h *Host) applyCommits(commits []abi.Commit) error {
	for _, c := range commits {
		if h.opts.OnCommit != nil {
			h.opts.OnCommit(c, h.opts.SQL)
		}
		if err := h.applyCommit(c); err != nil {
			return err
		}
		if err := h.observeRanges(c); err != nil {
			return err
		}
		if h.opts.OnCommitted != nil {
			h.opts.OnCommitted(c, h.opts.SQL)
		}
	}
	return nil
}

func (h *Host) applyCommit(c abi.Commit) (err error) {
	db := h.opts.DB
	if err := db.Begin(h.ctx); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if rerr := db.Rollback(context.WithoutCancel(h.ctx)); rerr != nil {
				err = errors.Join(err, fmt.Errorf("rollback also failed: %w", rerr))
			}
		}
	}()
	for _, s := range c.Stmts {
		if int(s.SQL) >= len(h.opts.SQL) {
			return fmt.Errorf("statement %d: %w", s.SQL, errNoStatement)
		}
		text := h.opts.SQL[s.SQL]
		n, rerr := db.Run(h.ctx, text, s.Params)
		if rerr != nil {
			return rerr
		}
		if isGuard(text) && n != 1 {
			return fmt.Errorf("single-writer guard: the durable_metadata update changed %d rows, not 1", n)
		}
	}
	if err := db.Commit(h.ctx); err != nil {
		return err
	}
	h.count(func(k *Counters) { k.Commits++; k.Statements += len(c.Stmts) })
	return nil
}

func (h *Host) notify(n abi.Notice) {
	h.count(func(c *Counters) { c.Notices++ })
	if h.opts.OnNotice != nil {
		h.opts.OnNotice(n)
	}
	h.mu.Lock()
	var hit []*waiter
	for w := range h.waiters {
		h.counters.WaiterTests++
		if w.test(n) {
			hit = append(hit, w)
			delete(h.waiters, w)
		}
	}
	if len(n.Payload) >= 4 {
		key := noticeKey{n.Kind, binary.LittleEndian.Uint32(n.Payload)}
		for w := range h.byKey[key] {
			h.counters.WaiterTests++
			if w.test(n) {
				hit = append(hit, w)
				h.removeWaiterLocked(w)
			}
		}
	}
	h.mu.Unlock()
	for _, w := range hit {
		w.resolve <- n
	}
}

// start runs one effect after its step's commits are applied.
func (h *Host) start(e abi.Effect) {
	if int64(e.ID) <= h.lastEffect {
		_ = h.fatal(fmt.Sprintf("effect ID %d was issued twice or out of order (the last one started is %d)", e.ID, h.lastEffect), nil)
		return
	}
	h.lastEffect = int64(e.ID)
	h.count(func(c *Counters) { c.Effects[e.Kind]++ })
	p := abi.NewPayload(e.Payload)
	switch e.Kind {
	case abi.EffectTimer:
		id, at, class := p.U32(), p.F64(), abi.TimerClass(p.U8())
		if p.Err() != nil {
			_ = h.fatal("a timer effect is truncated", p.Err())
			return
		}
		// A native process has no object to wake: durable and volatile timers are both in-process.
		h.setTimer(id, at, class == abi.TimerBackground)
		return
	case abi.EffectTimerClear:
		id := p.U32()
		if stop, ok := h.timers[id]; ok {
			stop()
			delete(h.timers, id)
		}
		return
	case abi.EffectLiveness:
		if h.opts.OnLiveness != nil {
			h.opts.OnLiveness(p.F64())
		}
		return
	case abi.EffectCancel:
		id := p.U32()
		if cancel, ok := h.running[id]; ok {
			cancel()
		}
		if h.opts.OnCancel != nil {
			h.opts.OnCancel(id)
		}
		return
	case abi.EffectHops:
		hops, err := abi.DecodeHops(e.Payload)
		if err != nil {
			_ = h.fatal(fmt.Sprintf("hops effect %d is malformed", e.ID), err)
			return
		}
		h.startHops(e.ID, hops)
		return
	}
	handler := h.opts.Handlers[e.Kind]
	if handler == nil {
		_ = h.fatal(fmt.Sprintf("no handler for effect kind %d", e.Kind), nil)
		return
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.running[e.ID] = cancel
	var rng *MessageRange
	if e.Kind == abi.EffectModelContext {
		// The payload is a recipe (ABI section 10.2): the host derives the context it names from storage.
		text, err := abi.Expand(e.Payload, func(conv, head, tail uint32) ([]byte, error) {
			var err error
			rng, err = h.rangeContext(conv, head, tail)
			return nil, err
		})
		if err != nil {
			_ = h.fatal(fmt.Sprintf("the model_context payload of effect %d cannot be expanded", e.ID), err)
			cancel()
			delete(h.running, e.ID)
			return
		}
		e.Payload = text
	}
	call := &Call{ID: e.ID, Kind: e.Kind, Payload: e.Payload, Range: rng, host: h}
	h.mu.Lock()
	h.active++
	h.mu.Unlock()
	h.wg.Go(func() {
		defer h.effectDone()
		defer cancel()
		defer h.finished(e.ID)
		err := h.runHandler(ctx, handler, call)
		if err != nil && !errors.Is(err, context.Canceled) && h.Err() == nil {
			h.enqueue(func(dead error) {
				if dead == nil {
					_ = h.fatal(fmt.Sprintf("the handler for effect %d (kind %d) failed", e.ID, e.Kind), err)
				}
			})
		}
	})
	if h.opts.Settle != nil {
		h.opts.Settle()
	}
}

func (h *Host) runHandler(ctx context.Context, handler Handler, call *Call) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the handler panicked: %v", r)
		}
	}()
	return handler(ctx, call)
}

// finished forgets an effect's cancel function. It runs on the effect goroutine and queues the bookkeeping to the owner,
// which owns the running map.
func (h *Host) finished(id uint32) {
	h.box.put(func(error) { delete(h.running, id) })
}

func (h *Host) setTimer(id uint32, at float64, background bool) {
	if stop, ok := h.timers[id]; ok {
		stop()
	}
	wait := max(time.Duration((at-h.opts.Clock())*float64(time.Millisecond)), 0)
	if wait > h.opts.TimerLead {
		wait -= h.opts.TimerLead
	}
	// Every timer gets a new ID, so the map drops a timer when it fires (on the owner, before its event) or is cleared:
	// it holds the armed timers only, not every timer of the handle's life.
	fire := func() {
		if h.opts.TimerFired != nil {
			h.opts.TimerFired(at)
		}
		h.enqueue(func(dead error) {
			delete(h.timers, id)
			if dead == nil {
				_ = h.deliver(abi.Event{Kind: abi.EventTimer, ID: id}, false)
			}
		})
	}
	if h.opts.SetTimer != nil {
		if stop := h.opts.SetTimer(fire, float64(wait)/float64(time.Millisecond), background); stop != nil {
			h.timers[id] = stop
		}
		return
	}
	t := time.AfterFunc(wait, fire)
	h.timers[id] = func() { t.Stop() }
}

// Close seals the session: the core receives close and cancels its effects, then the host cancels what remains, waits for
// the effect goroutines (bounded by ctx), and closes the connection when it owns it.
func (h *Host) Close(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		<-h.done
		return nil
	}
	already := h.dead != nil
	h.mu.Unlock()
	var sendErr error
	if !already {
		sendErr = h.Send(ctx, abi.Event{Kind: abi.EventClose})
	}
	h.mu.Lock()
	h.closed = true
	if h.dead == nil {
		h.dead = ErrClosed
	}
	waiters := h.takeWaitersLocked()
	h.mu.Unlock()
	for _, w := range waiters {
		select {
		case w.fail <- ErrClosed:
		default:
		}
	}
	h.cancel()
	h.box.close()
	select {
	case <-h.done:
	case <-ctx.Done():
		return errors.Join(sendErr, ctx.Err())
	}
	for _, job := range h.box.drain() {
		job(ErrClosed)
	}
	for _, stop := range h.timers {
		stop()
	}
	waited := make(chan struct{})
	go func() { h.wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-ctx.Done():
		return errors.Join(sendErr, ctx.Err())
	}
	var closeErr error
	if h.opts.OwnDB && h.opts.DB != nil {
		closeErr = h.opts.DB.Close()
	}
	var rej *RejectedError
	if errors.As(sendErr, &rej) || errors.Is(sendErr, ErrClosed) {
		sendErr = nil
	}
	if _, ok := errors.AsType[*FatalError](sendErr); ok {
		sendErr = nil
	}
	return errors.Join(sendErr, closeErr)
}

// syncSettings sends the settings again before an event when Options.Settings reports a change since the last send. It runs
// on the owner goroutine, as the cf host's enqueue does on its thread.
func (h *Host) syncSettings(kind abi.EventKind) error {
	if h.opts.Settings == nil || kind == abi.EventClose || kind == abi.EventRegistry {
		return nil
	}
	text := h.opts.Settings()
	if text == nil {
		return nil
	}
	if kind == abi.EventOpen || h.sentSettings == nil {
		h.sentSettings = text
		return nil
	}
	if bytes.Equal(text, h.sentSettings) {
		return nil
	}
	h.sentSettings = text
	payload := append(append([]byte(`{"settings":`), text...), '}')
	return h.deliver(abi.Event{Kind: abi.EventRegistry, Payload: payload, Now: h.opts.Clock()}, false)
}
