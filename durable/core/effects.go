// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// pending is a started effect waiting for its completion events (ABI sections 5 and 6). progress receives the
// events that do not end the effect (model_event batches, tool_progress); done receives the one that ends it
// (hook_done, tool_done or phase_done: outcome 0 value, 1 thrown). A model_context effect ends with the model_event batch
// whose last event is done or error, which progress reports by returning true. A payload aliases the host's input
// buffer: a continuation that keeps it copies it. origin is the host async context the effect started in (origin.go): the
// continuations run in it, as an `await` resumes in the context of its function whoever settles the promise.
type pending struct {
	progress func(ev *abi.Event) (finished bool)
	done     func(outcome uint8, payload []byte)
	origin   uint32
}

// startEffect emits an effect and registers its continuation; the returned ID names it in a cancel.
func (h *handler) startEffect(kind abi.EffectKind, payload []byte, p *pending) uint32 {
	id := h.b.Effect(kind, payload)
	if h.effects == nil {
		h.effects = map[uint32]*pending{}
	}
	p.origin = h.origin()
	h.holdOrigin(p.origin)
	h.effects[id] = p
	return id
}

// cancelEffect aborts a started effect's signal (ABI effect 5) and nothing else: the host still reports how the handler
// ended, and the continuation settles on that completion, as Pi awaits the handler it signalled.
func (h *handler) cancelEffect(id uint32) {
	if _, ok := h.effects[id]; ok {
		h.b.Cancel(id)
	}
}

// armTimer arms a volatile timer at an absolute Harness time (ABI effect 1); the returned function clears it. A timer
// fires at most once, through onTimer.
func (h *handler) armTimer(at float64, fire func()) (cancel func()) {
	return h.arm(at, abi.TimerVolatile, fire)
}

// armBackgroundTimer arms a timer that only frees memory (ABI effect 1, class background): the host may decline it.
func (h *handler) armBackgroundTimer(at float64, fire func()) (cancel func()) {
	return h.arm(at, abi.TimerBackground, fire)
}

func (h *handler) arm(at float64, class abi.TimerClass, fire func()) (cancel func()) {
	id := h.b.Timer(at, class)
	h.timers = append(h.timers, armedTimer{id, fire})
	return func() {
		if h.takeTimer(id) != nil {
			h.b.ClearTimer(id)
		}
	}
}

// armedTimer is a timer the core armed and the continuation it fires. The armed timers are a slice, not a
// map[uint32]func(): TinyGo lost entries of such a map (see parkedHops).
type armedTimer struct {
	id   uint32
	fire func()
}

// takeTimer removes the armed timer and returns its continuation, or nil when it is not armed.
func (h *handler) takeTimer(id uint32) func() {
	for i, t := range h.timers {
		if t.id == id {
			h.timers = slices.Delete(h.timers, i, i+1)
			return t.fire
		}
	}
	return nil
}

// armWakeTimer arms the durable timer that resumes the earliest sleeping task (ABI effect 1, durable): a host that wakes its
// process itself, such as a Durable Object, keeps its alarm at the earliest durable time; the returned function clears it.
func (h *handler) armWakeTimer(at float64, fire func()) (cancel func()) {
	return h.arm(at, abi.TimerDurable, fire)
}

// after arms a volatile timer ms from now.
func (h *handler) after(ms float64, fire func()) (cancel func()) { return h.armTimer(h.now+ms, fire) }

// onCompletion routes a completion event to its effect; Handle then runs what it made runnable (pump). An event for an
// unknown or finished effect is a no-op.
//
//go:noinline
func (h *handler) onCompletion(ev *abi.Event) error {
	switch ev.Kind {
	case abi.EventModelEvent, abi.EventModelBytes, abi.EventToolProgress:
		p := h.effects[ev.ID]
		if p == nil || p.progress == nil {
			break
		}
		h.setOrigin(p.origin)
		if p.progress(ev) {
			delete(h.effects, ev.ID)
			h.dropOrigin(p.origin)
		}
	case abi.EventHookDone, abi.EventToolDone, abi.EventPhaseDone:
		p := h.effects[ev.ID]
		if p == nil {
			break
		}
		delete(h.effects, ev.ID)
		h.setOrigin(p.origin)
		if p.done != nil {
			p.done(ev.Phase, ev.Payload)
		}
		h.dropOrigin(p.origin)
	}
	return nil
}
