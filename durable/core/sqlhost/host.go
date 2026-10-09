// SPDX-License-Identifier: MIT

package sqlhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// ErrClosed is returned for an event sent after Close.
var ErrClosed = errors.New("sqlhost: the host is closed")

// FatalError is the reason a host discarded its handle (ABI section 4.6, ADR D16). The next event needs a new Host: a
// cold open from SQLite loses nothing Pi would keep.
type FatalError struct {
	Message string
	Cause   error
}

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
	host    *Host
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
	Notices    int
	Rejected   int
	Effects    map[abi.EffectKind]int
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
	// Handlers start effects by kind. Timer, timer_clear, cancel and liveness are the host's own and need none.
	Handlers map[abi.EffectKind]Handler
	// OnNotice receives each notice on the owner goroutine, after the step's commits are applied. It must not block.
	OnNotice func(abi.Notice)
	// OnLiveness receives the watchdog target (ABI effect 10): a time, or -1 when nothing is live. A native process
	// has nothing to wake, so the default ignores it.
	OnLiveness func(at float64)
	// OnFatal is called once, from the owner goroutine, when the handle is discarded.
	OnFatal func(error)
	// OnRejected receives a rejected step that no caller waits on: an effect completion the core refused.
	OnRejected func(kind abi.EventKind, err *RejectedError)
	// OnCommit observes every commit before it applies (trace capture).
	OnCommit func(c abi.Commit, sql []string)
}

// Host is one core session and its SQLite connection.
type Host struct {
	opts   Options
	box    *mailbox
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // effect goroutines

	mu       sync.Mutex
	dead     error
	closed   bool
	counters Counters
	waiters  map[*waiter]struct{}

	// owner-only state
	seen    map[uint32]struct{}
	running map[uint32]context.CancelFunc
	timers  map[uint32]*time.Timer
}

type waiter struct {
	test    func(abi.Notice) bool
	resolve chan abi.Notice
	fail    chan error
}

// New starts the owner goroutine. Send the open event next.
func New(opts Options) *Host {
	if opts.Clock == nil {
		opts.Clock = func() float64 { return float64(time.Now().UnixNano()) / 1e6 }
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Host{
		opts: opts, box: newMailbox(), done: make(chan struct{}), ctx: ctx, cancel: cancel,
		waiters: map[*waiter]struct{}{}, seen: map[uint32]struct{}{}, running: map[uint32]context.CancelFunc{}, timers: map[uint32]*time.Timer{},
	}
	h.counters.Effects = map[abi.EffectKind]int{}
	//portlint:allow golifetime run ends when the mailbox closes and then closes h.done, which Close waits on
	go h.run()
	return h
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
// first Send returns ctx.Err(); the queued event still runs, because an event cannot be retracted from a core.
func (h *Host) Send(ctx context.Context, ev abi.Event) error {
	reply := make(chan error, 1)
	if !h.enqueue(func(dead error) {
		if dead != nil {
			reply <- dead
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
		return ctx.Err()
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
		return nil, ctx.Err()
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

// Wait resolves with the notice, or fails when ctx ends or the handle is discarded first. It releases the registration.
func (e *Expectation) Wait(ctx context.Context) (abi.Notice, error) {
	defer e.Cancel()
	select {
	case n := <-e.w.resolve:
		return n, nil
	case err := <-e.w.fail:
		return abi.Notice{}, err
	case <-ctx.Done():
		return abi.Notice{}, ctx.Err()
	}
}

// Cancel releases the registration. It is safe after Wait.
func (e *Expectation) Cancel() {
	e.h.mu.Lock()
	delete(e.h.waiters, e.w)
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
	waiters := h.waiters
	h.waiters = map[*waiter]struct{}{}
	h.mu.Unlock()
	h.cancel()
	for _, t := range h.timers {
		t.Stop()
	}
	h.timers = map[uint32]*time.Timer{}
	h.running = map[uint32]context.CancelFunc{}
	for w := range waiters {
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
			if len(step.Commits) > 0 || len(step.Effects) > 0 {
				return h.fatal("a step with reads must have no commits and no effects", nil)
			}
			answers, err := h.read(step.Reads)
			if err != nil {
				return h.fatal("a read failed", err)
			}
			ev = abi.Event{Kind: abi.EventRows, Rows: answers, Now: h.opts.Clock()}
			continue
		}
		if err := h.applyCommits(step.Commits); err != nil {
			return h.fatal("a commit failed", err)
		}
		for _, n := range step.Notices {
			h.notify(abi.Notice{Kind: n.Kind, Payload: append([]byte(nil), n.Payload...)})
		}
		for _, e := range step.Effects {
			if h.Err() != nil {
				return h.Err()
			}
			h.start(abi.Effect{ID: e.ID, Kind: e.Kind, Payload: append([]byte(nil), e.Payload...)})
		}
		if err := h.Err(); err != nil {
			return err
		}
		return first
	}
}

func (h *Host) read(reads []abi.Read) ([]abi.ReadRows, error) {
	answers := make([]abi.ReadRows, 0, len(reads))
	for _, r := range reads {
		if int(r.SQL) >= len(h.opts.SQL) {
			return nil, fmt.Errorf("read %d: %w", r.ID, errNoStatement)
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
		if w.test(n) {
			hit = append(hit, w)
			delete(h.waiters, w)
		}
	}
	h.mu.Unlock()
	for _, w := range hit {
		w.resolve <- n
	}
}

// start runs one effect after its step's commits are applied.
func (h *Host) start(e abi.Effect) {
	if _, dup := h.seen[e.ID]; dup {
		_ = h.fatal(fmt.Sprintf("effect ID %d was issued twice", e.ID), nil)
		return
	}
	h.seen[e.ID] = struct{}{}
	h.count(func(c *Counters) { c.Effects[e.Kind]++ })
	p := abi.NewPayload(e.Payload)
	switch e.Kind {
	case abi.EffectTimer:
		id, at, durable := p.U32(), p.F64(), p.U8()
		if p.Err() != nil {
			_ = h.fatal("a timer effect is truncated", p.Err())
			return
		}
		_ = durable // a native process has no object to wake: durable and volatile timers are both in-process
		h.setTimer(id, at)
		return
	case abi.EffectTimerClear:
		if t, ok := h.timers[p.U32()]; ok {
			t.Stop()
		}
		return
	case abi.EffectLiveness:
		if h.opts.OnLiveness != nil {
			h.opts.OnLiveness(p.F64())
		}
		return
	case abi.EffectCancel:
		if cancel, ok := h.running[p.U32()]; ok {
			cancel()
		}
		return
	}
	handler := h.opts.Handlers[e.Kind]
	if handler == nil {
		_ = h.fatal(fmt.Sprintf("no handler for effect kind %d", e.Kind), nil)
		return
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.running[e.ID] = cancel
	call := &Call{ID: e.ID, Kind: e.Kind, Payload: e.Payload, host: h}
	h.wg.Go(func() {
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

func (h *Host) setTimer(id uint32, at float64) {
	if old, ok := h.timers[id]; ok {
		old.Stop()
	}
	wait := max(time.Duration((at-h.opts.Clock())*float64(time.Millisecond)), 0)
	h.timers[id] = time.AfterFunc(wait, func() {
		h.Post(abi.Event{Kind: abi.EventTimer, ID: id})
	})
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
	waiters := h.waiters
	h.waiters = map[*waiter]struct{}{}
	h.mu.Unlock()
	for w := range waiters {
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
	for _, t := range h.timers {
		t.Stop()
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
