// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Watches over the core's `watch.open` and `watch.close` api requests (PROTOCOL.md, "api", `watch.open`; ABI section 8.1). The
// core answers watch.open with the committed value at the acquisition boundary and then publishes every later frame of that
// watch in a `published` notice {watchId, frame: {value?, ops}}, in commit order, with no overlap with the first value and no
// gap. A document frame carries its value; a view or graph frame carries only ops, which the driver applies to the value it
// holds. Frames are delivered through session.CommittedWatch and session.CommittedStateSource, the serialized exact-frame
// observers of the Go port of session/observation.ts, so Start, Stop, cancellation and the bounded pending queue are Pi's.

// rawFrame is one frame as the core published it; value is nil when the frame carries only ops.
type rawFrame struct {
	value json.RawMessage
	ops   []durable.Op
	// changes are the records the commit wrote for the conversation of a view opened with changes: true.
	changes json.RawMessage
}

// watchHub routes the core's watch frames to the watches the session opened.
type watchHub struct {
	sched *session.DeliveryScheduler
	// report is the Session's report: it receives a failing state or watch listener (view.ts, task-graph.ts and session.ts
	// attach with `onError: (error) => session.report(error)`).
	report func(error)

	mu      sync.Mutex
	next    uint32
	sinks   map[uint32]func(rawFrame)
	early   map[uint32][]rawFrame
	ends    []func() // CloseSession of each open watch
	bg      sync.WaitGroup
	spawnMu sync.Mutex // guards shut; apart from mu because sinks run under mu and spawn
	shut    bool       // closeSession ran: no further background work starts

	// holding lists the Contexts of the commits that hold the Session line, newest last. The core's published notice names no
	// commit, but a commit holds the line from `ready` to its answer and a commit's publication arrives before that answer, so
	// the commit that holds the line is the producer (the TS host's facade/commit-context.ts). A publication of a commit the host
	// did not start (the scheduler's) finds none and delivers on a background Context.
	holdMu  sync.Mutex
	holding []context.Context
}

func (h *watchHub) enterCommit(ctx context.Context) {
	h.holdMu.Lock()
	h.holding = append(h.holding, ctx)
	h.holdMu.Unlock()
}

func (h *watchHub) leaveCommit(ctx context.Context) {
	h.holdMu.Lock()
	if at := slices.Index(h.holding, ctx); at >= 0 {
		h.holding = slices.Delete(h.holding, at, at+1)
	}
	h.holdMu.Unlock()
}

// deliveryContext is the Context a delivery takes: the values of the commit that produced the publication, never its
// cancellation (session.ts: a publication carries its commit's Context; the delivery Context has no abort signal).
func (h *watchHub) deliveryContext() context.Context {
	h.holdMu.Lock()
	defer h.holdMu.Unlock()
	if len(h.holding) == 0 {
		return context.Background()
	}
	return context.WithoutCancel(h.holding[len(h.holding)-1])
}

// spawn runs fn on its own goroutine, tracked so closeSession can wait for it; it does nothing once the hub is shut.
func (h *watchHub) spawn(fn func()) {
	h.spawnMu.Lock()
	if h.shut {
		h.spawnMu.Unlock()
		return
	}
	h.bg.Add(1)
	h.spawnMu.Unlock()
	go func() {
		defer h.bg.Done()
		fn()
	}()
}

func newWatchHub(report func(error)) *watchHub {
	return &watchHub{sched: session.NewDeliveryScheduler(), report: report, sinks: map[uint32]func(rawFrame){}, early: map[uint32][]rawFrame{}}
}

// reserve allocates a watch ID and holds the frames that arrive for it until bind installs the watch (a notice may carry
// frames in the step that answers the open).
func (h *watchHub) reserve() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	h.early[h.next] = nil
	return h.next
}

// bind installs the sink of an opened watch and hands it the frames held for it, in order, before any later frame.
func (h *watchHub) bind(id uint32, advance func(rawFrame), end func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	held := h.early[id]
	delete(h.early, id)
	h.sinks[id] = advance
	h.ends = append(h.ends, end)
	for _, f := range held {
		advance(f)
	}
}

// release forgets a watch ID.
func (h *watchHub) release(id uint32) {
	h.mu.Lock()
	delete(h.early, id)
	delete(h.sinks, id)
	h.mu.Unlock()
}

// notice receives each notice on the owner goroutine; it only queues, as sqlhost.Options.OnNotice requires.
func (h *watchHub) notice(n abi.Notice) {
	if n.Kind != abi.NoticePublished {
		return
	}
	var p struct {
		WatchID *uint32 `json:"watchId"`
		Frame   *struct {
			Value json.RawMessage `json:"value"`
			// Ops decode through chord's JSON, so an object an operation carries keeps its key order and the applier can
			// traverse it: encoding/json would hold map[string]any objects the applier cannot enter.
			Ops     delta.Ops       `json:"ops"`
			Changes json.RawMessage `json:"changes"`
		} `json:"frame"`
	}
	if json.Unmarshal(n.Payload, &p) != nil || p.WatchID == nil || p.Frame == nil {
		return
	}
	frame := rawFrame{value: p.Frame.Value, ops: p.Frame.Ops, changes: p.Frame.Changes}
	h.mu.Lock()
	defer h.mu.Unlock()
	if advance, bound := h.sinks[*p.WatchID]; bound {
		advance(frame)
	} else if _, reserved := h.early[*p.WatchID]; reserved {
		h.early[*p.WatchID] = append(h.early[*p.WatchID], frame)
	}
}

// closeSession ends every open watch as Session.close does (reason session_closed) and waits for the watch.close requests.
func (h *watchHub) closeSession() {
	h.mu.Lock()
	ends := h.ends
	h.ends = nil
	h.mu.Unlock()
	for _, end := range ends {
		end()
	}
	h.spawnMu.Lock()
	h.shut = true
	h.spawnMu.Unlock()
	h.bg.Wait()
}

// mountSource is an opened core watch: its first value, and the reducer that turns each frame into the next typed value.
type mountSource[T any] struct {
	id        uint32
	first     T
	advance   func(rawFrame) (T, bool)
	closeCore func()
	hub       *watchHub
}

// openWatch sends watch.open for target and returns the mount, or nil when the target is absent. Cancelling ctx before the
// core answers rejects with the cancellation and leaves no watch behind (session.ts watchDoc, view.ts attach).
//
// A view or graph mount passes reduce, which makes the typed value each frame's ops produce without converting the whole value; a
// document mount passes nil and applies each frame's ops to the generic value it holds.
func openWatch[T any](ctx context.Context, s *coreSession, target map[string]any, convert func(any) (T, error), reduce reducer[T]) (*mountSource[T], error) {
	release, err := s.admit()
	if err != nil {
		return nil, err
	}
	defer release()
	hub := s.watches
	id := hub.reserve()
	closeCore := func() {
		hub.spawn(func() {
			_, _ = s.client.API(context.WithoutCancel(ctx), "watch.close", map[string]any{"watchId": id})
		})
	}
	raw, err := s.client.API(ctx, "watch.open", map[string]any{"watchId": id, "target": target})
	if err != nil {
		hub.release(id)
		if ctx.Err() != nil {
			closeCore()
		}
		return nil, err
	}
	var answer struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || len(answer.Value) == 0 || string(answer.Value) == "null" {
		hub.release(id)
		return nil, err
	}
	// Objects keep the key order the core wrote (chord's ordered JSON objects).
	value, err := delta.DecodeJson(answer.Value)
	if err != nil {
		hub.release(id)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		hub.release(id)
		closeCore()
		return nil, context.Cause(ctx)
	}
	first, err := convert(value)
	if err != nil {
		hub.release(id)
		closeCore()
		return nil, err
	}
	m := &mountSource[T]{id: id, first: first, closeCore: closeCore, hub: hub}
	if reduce != nil {
		current := first
		m.advance = func(f rawFrame) (T, bool) {
			if f.value != nil {
				value, err := delta.DecodeJson(f.value)
				if err != nil {
					var zero T
					return zero, false
				}
				next, err := convert(value)
				if err != nil {
					var zero T
					return zero, false
				}
				current = next
				return next, true
			}
			next, ok := reduce(current, f.ops)
			if !ok {
				if next, ok = fullConvert(current, f.ops, convert); !ok {
					var zero T
					return zero, false
				}
			}
			current = next
			return next, true
		}
		return m, nil
	}
	held := value
	m.advance = func(f rawFrame) (T, bool) {
		if f.value != nil {
			value, err := delta.DecodeJson(f.value)
			if err != nil {
				var zero T
				return zero, false
			}
			held = value
		} else {
			next, err := delta.ApplyImmutable(held, f.ops)
			if err != nil {
				var zero T
				return zero, false
			}
			held = next
		}
		typed, err := convert(held)
		return typed, err == nil
	}
	return m, nil
}

func jsonObject(v any) (durable.JsonObject, error) {
	if v == nil {
		return nil, nil
	}
	o, ok := v.(*delta.JsonObject)
	if !ok {
		return nil, fmt.Errorf("a document value is a JSON object, not %T", v)
	}
	return o, nil
}

// typed converts a generic JSON value to T through its JSON form.
func typed[T any](v any) (T, error) {
	var out T
	raw, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

// watchOf wraps a mount in a CommittedWatch delivering on the session's scheduler.
func watchOf[T any](ctx context.Context, m *mountSource[T]) *session.CommittedWatch[T] {
	var once sync.Once
	watch := session.NewCommittedWatchOn(m.hub.sched, m.first, func() { once.Do(func() { m.hub.release(m.id); m.closeCore() }) }, nil, m.hub.report)
	m.hub.bind(m.id, func(f rawFrame) {
		if value, ok := m.advance(f); ok {
			watch.Advance(m.hub.deliveryContext(), value, f.ops)
		}
	}, func() { watch.CloseSession(nil) })
	if ctx.Done() != nil {
		watch.ObserveCancellation(ctx)
	}
	return watch
}

// stateOf wraps a mount in a disposable read-only Chord state (view.ts State, task-graph.ts State). Its source-contract and
// listener failures go to report, as upstream's replicatedState(observer, { onError: (error) => session.report(error) })
// (view.ts:118, task-graph.ts:93, session.ts:360).
func stateOf[T any](m *mountSource[T], report func(error)) (*chord.AttachedReplicatedState[T], error) {
	var once sync.Once
	source := session.NewCommittedStateSourceOn(m.hub.sched, m.first, func() { once.Do(func() { m.hub.release(m.id); m.closeCore() }) })
	m.hub.bind(m.id, func(f rawFrame) {
		if value, ok := m.advance(f); ok {
			source.Advance(m.hub.deliveryContext(), value, f.ops)
			// Upstream's drain microtask delivers the frame to the state's subscribers after the commit settled; the owner
			// goroutine must not run subscriber code, so the drain runs on its own goroutine.
			m.hub.spawn(m.hub.sched.RunDrains)
		}
	}, source.CloseSession)
	state, err := chord.AttachReplicatedStateSource[T](source, chord.ReplicatedStateSourceOptions{OnError: report})
	if err != nil {
		m.hub.release(m.id)
		m.closeCore()
		return nil, err
	}
	return state, nil
}

// watchDoc is Session.watchDoc (session.ts:268-298): the watch of a document's committed value, or nil when the document is
// absent. Cancelling ctx before the core answers rejects with ctx's error; cancelling it after ends the watch with reason
// cancelled.
func (s *coreSession) watchDoc(ctx context.Context, token durable.AnyDocToken, args []any) (durable.WatchHandle[durable.JsonObject], error) {
	def := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(def, args)
	if err != nil {
		return nil, err
	}
	m, err := openDocWatch(ctx, s, def, resolved.Address)
	if err != nil || m == nil {
		return nil, err
	}
	return watchOf(ctx, m), nil
}

// openDocWatch opens a document watch, running the definition's migrate on the host when the core asks (watches.ts migrateRead).
func openDocWatch(ctx context.Context, s *coreSession, def *durable.AnyDocDefinition, address durable.DocumentAddress) (*mountSource[durable.JsonObject], error) {
	return migrateRead(def, func(migrated durable.JsonObject) (*mountSource[durable.JsonObject], error) {
		target := map[string]any{"kind": "doc", "address": wireAddress(address), "def": wireDef(def)}
		if migrated != nil {
			target["migrated"] = migrated
		}
		return openWatch(ctx, s, target, jsonObject, nil)
	})
}

func (c *conversation) Watch(ctx context.Context) (harness.ConversationWatch, error) {
	m, err := openWatch(ctx, c.s, map[string]any{"kind": "view", "conversationId": c.id}, typed[harness.ConversationView], viewReducer())
	if err != nil || m == nil {
		return nil, err
	}
	return watchOf(ctx, m), nil
}

func (c *conversation) ViewState(ctx context.Context) (durable.AttachedReplicatedState[harness.ConversationView], error) {
	m, err := openWatch(ctx, c.s, map[string]any{"kind": "view", "conversationId": c.id}, typed[harness.ConversationView], viewReducer())
	if err != nil || m == nil {
		return nil, err
	}
	return stateOf(m, c.s.reportListener)
}

// TaskGraph is the task graph as a disposable read-only Chord state (task-graph.ts State).
func (s *coreSession) TaskGraph(ctx context.Context) (durable.AttachedReplicatedState[harness.TaskGraph], error) {
	m, err := openWatch(ctx, s, map[string]any{"kind": "graph"}, typed[harness.TaskGraph], reduceTaskGraph)
	if err != nil || m == nil {
		return nil, err
	}
	return stateOf(m, s.reportListener)
}

// WatchTaskGraph is a serialized exact-frame watch of the task graph.
func (s *coreSession) WatchTaskGraph(ctx context.Context) (harness.TaskGraphWatch, error) {
	m, err := openWatch(ctx, s, map[string]any{"kind": "graph"}, typed[harness.TaskGraph], reduceTaskGraph)
	if err != nil || m == nil {
		return nil, err
	}
	return watchOf(ctx, m), nil
}

// DocumentStateErased is a disposable read-only Chord state of a document's committed value, or nil when the document is
// absent (session.ts documentState).
func (s *coreSession) DocumentStateErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.AttachedReplicatedState[durable.JsonObject], error) {
	def := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(def, args)
	if err != nil {
		return nil, err
	}
	m, err := openDocWatch(ctx, s, def, resolved.Address)
	if err != nil || m == nil {
		return nil, err
	}
	return stateOf(m, s.reportListener)
}
