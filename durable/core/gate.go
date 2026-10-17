// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// Storage gate (durable/core/host/PROTOCOL.md, "Storage gate"). The core commits through its own store, but a Storage the
// host was given may override Pi's Storage methods: a subclass that holds, fails or loses a commit, or counts and holds
// reads. `open.storageGate` names the methods the host's Storage overrides. A commit of a gated Storage reaches the host
// first as the host-answered hook `storageCommit {writes}` (Pi's StorageWrite[]): the commit applies only when the
// hook completes, and a rejection fails it as Pi's `await storage.commit(writes)` rejection does. The line job that
// issued it holds the line until then, as Pi's job awaits the call; it runs again from the start when the answer
// arrives, with the same writes, as it does after a NeedRead.

// Commit gate states.
const (
	gateIdle uint8 = iota
	gateAsking
	gateAnswered
)

// commitGate is the state of the one commit that waits for the host's Storage.commit. The Session line holds at most one.
type commitGate struct {
	state uint8
	err   error
	// now is the Harness time of the job's first run: the run after the answer repeats it, so the writes the Storage saw are
	// the writes that commit.
	now float64
	// resume continues a host transaction that issued the gate; nil for a line job, which the Loop runs again.
	resume func()
}

// gated reports whether the host's Storage overrides the Pi Storage method.
func (h *handler) gated(method string) bool {
	if h.gate == nil {
		h.gate = map[string]bool{}
		if list, _ := valueOf(h.open, "storageGate").([]any); list != nil {
			for _, v := range list {
				if name, ok := v.(string); ok {
					h.gate[name] = true
				}
			}
		}
	}
	return h.gate[method]
}

// gateCommit asks the host's Storage to take the writes before the core applies them. It returns a *session.NeedGate while
// the host works, nil once the Storage accepted the commit, and the Storage's error when it refused.
//
//go:noinline
func (h *handler) gateCommit(writes []session.Write) error {
	cg := &h.cg
	switch cg.state {
	case gateAnswered:
		cg.state = gateIdle
		err := cg.err
		cg.err = nil
		return err
	case gateAsking:
		return &session.NeedGate{}
	}
	cg.state, cg.now = gateAsking, h.now
	list := make([]any, len(writes))
	for i := range writes {
		list[i] = writeValue(&writes[i])
	}
	body := jv.NewObject()
	body.Set("name", "storageCommit")
	body.Set("handler", float64(0))
	body.Set("conversationId", float64(0))
	body.Set("taskId", float64(0))
	body.Set("payload", jobj("writes", list))
	payload := jv.Append(nil, body)
	call := func() {
		h.startEffect(abi.EffectHook, payload, &pending{done: h.gateAnswered})
	}
	// A line job calls Storage.commit once its change callback and settle ran (hops.go commitCallHops), not when the core runs the
	// body: a job queued behind it on the line, and the caller of the job before it, run in those ticks.
	if h.sess.InJob() && !h.opening {
		if !h.holdFor(h.commitCallHops(writes), call) {
			call()
		}
	} else {
		call()
	}
	return &session.NeedGate{}
}

// gateAnswered is the Storage's answer to the commit the gate asked it to take.
func (h *handler) gateAnswered(outcome uint8, payload []byte) {
	cg := &h.cg
	cg.state = gateAnswered
	if outcome != abi.OutcomeResult {
		value, err := jv.Parse(payload)
		if err != nil {
			cg.err = err
		} else {
			cg.err = thrown(value)
		}
	}
	h.now = cg.now
	if resume := cg.resume; resume != nil {
		cg.resume = nil
		resume()
		return
	}
	h.loop.Ungate()
}

// jobj builds an ordered object from alternating keys and values.
func jobj(kv ...any) *jv.Object {
	o := jv.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// contentValue is the JSON of a document write's content.
func contentValue(w *session.Write) *jv.Object {
	if w.Base {
		return jobj("version", float64(w.Version), "kind", "base", "value", w.Value)
	}
	return jobj("version", float64(w.Version), "kind", "delta", "ops", delta.OpsValue(w.Ops))
}

// writeValue is the JSON of a pi-durable StorageWrite.
func writeValue(w *session.Write) *jv.Object {
	switch w.Kind {
	case session.WriteConversation:
		return jobj("type", "conversation", "value", w.Record)
	case session.WriteEntry:
		return jobj("type", "entry", "value", w.Record)
	case session.WriteTask:
		return jobj("type", "task", "value", w.Record)
	case session.WriteSubmission:
		return jobj("type", "submission", "value", w.Record)
	case session.WriteDocCreate:
		return jobj("type", "document.create", "record", w.Record, "content", contentValue(w))
	case session.WriteDocCopy:
		at := any("current")
		if !w.Source.At.Current {
			at = float64(w.Source.At.Seq)
		}
		return jobj("type", "document.copy", "record", w.Record, "source", jobj("id", float64(w.Source.ID), "at", at))
	case session.WriteDocChange:
		return jobj("type", "document.change", "id", float64(w.ID), "content", contentValue(w))
	}
	return jobj("type", "document.retire", "id", float64(w.ID))
}

// addressValue is a DocumentAddress {kind, scope, key?} as pi-durable's Storage takes it.
func addressValue(a session.Address) *jv.Object {
	scope := jobj("kind", string(a.Scope.Kind))
	switch a.Scope.Kind {
	case session.ScopeConversation:
		scope.Set("conversationId", float64(a.Scope.Owner))
	case session.ScopeTask:
		scope.Set("taskId", float64(a.Scope.Owner))
	}
	out := jobj("kind", a.Kind, "scope", scope)
	if a.Family {
		out.Set("key", a.Key)
	}
	return out
}

// pointJSON is a DocumentPoint: "current" or a commit sequence.
func pointJSON(p session.Point) any {
	if p.Current {
		return "current"
	}
	return float64(p.Seq)
}
