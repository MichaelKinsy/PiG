// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// Storage gate (durable/core/host/PROTOCOL.md, "Storage gate"; ABI.md section 5.5). The core commits through its own store, but a
// Storage the host was given may override Pi's Storage methods: a subclass that holds, fails or loses a commit, or counts and holds
// reads. `open.storageGate` names the methods the host's Storage overrides. The Session's writer (session/writer.go) sends every part
// admitted while no call is in flight as one Storage call: with `commit` gated that is the host-answered hook `storageCommit {batches}`
// (Pi's StorageWrite[][]), and the host calls `storage.commit(batches, receive, context)`. Its `receive(index, seq)` is the api op
// `receive`: the core stores batch `index` in that step. The hook's answer is the call's result, `{stored, refused?}` or a rejection, and
// the writer publishes the received parts then. The Storage holds the writer, never the line.

// commitCall is the Storage call the host's Storage has in flight. The writer has at most one.
type commitCall struct {
	batches [][]session.Write
	nextID  session.ID
	receive func(index int, seq session.Seq)
	done    func(session.CommitResult, error)
	// got is the receipts so far: the next batch to receive is got.
	got int
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

// syncGate makes the Session's writer call the host's Storage when the host's Storage overrides commit.
func (h *handler) syncGate() {
	if h.gated("commit") {
		h.sess.CommitBatches = h.commitBatches
	} else {
		h.sess.CommitBatches = nil
	}
	if h.gated("publishing") {
		h.sess.Publishing = h.publishing
	} else {
		h.sess.Publishing = nil
	}
	if on, _ := boolOf(h.open, "heldAdoption"); on {
		h.sess.HeldHost = h
	}
}

// Read-only: assigned once at initialization.
//
// adoptHeldHook is the hook effect of the adoption of a held commit.
var adoptHeldHook = []byte(`{"name":"adoptHeld","handler":0,"conversationId":0,"taskId":0,"payload":null}`)

// AdoptHeld is Session.HeldHost: the host adopts a held commit (hook adoptHeld) in the tick it is asked, as Pi's `tx.adoptHeld()` runs
// inside the publication; its throw is the part's own failure.
func (h *handler) AdoptHeld(done session.HeldAdopter) {
	h.heldDone = done
	if h.heldPending.done == nil {
		h.heldPending.done = h.heldAnswered
	}
	h.startEffect(abi.EffectHook, adoptHeldHook, &h.heldPending)
}

// heldAnswered is the host's answer to the adoption of a held commit.
func (h *handler) heldAnswered(outcome uint8, payload []byte) {
	done := h.heldDone
	h.heldDone = nil
	done.Adopted(answerErr(outcome, payload))
}

// publishing is Session.Publishing: the writer's publication of a part runs inside the host Storage's scope (Storage.publishing). The
// core asks the host (hook storagePublishing), whose Storage calls publish: the api op "publish" runs the writer's publication in that step.
func (h *handler) publishing(publish func()) bool {
	h.publishFn = publish
	body := jv.NewObject()
	body.Set("name", "storagePublishing")
	body.Set("handler", float64(0))
	body.Set("conversationId", float64(0))
	body.Set("taskId", float64(0))
	body.Set("payload", nil)
	h.startEffect(abi.EffectHook, jv.Append(nil, body), &pending{done: h.publishingAnswered})
	return true
}

// onPublish is the host Storage's `publish` (api op "publish"): the part's publication runs in this step. A Storage that calls it
// when none waits breaks Storage.publishing's contract (once), which fails the Session.
func (h *handler) onPublish() {
	publish := h.publishFn
	if publish == nil {
		h.sess.Fail(errors.New("Storage.publishing called publish more than once"))
		return
	}
	h.publishFn = nil
	publish()
}

// publishingAnswered is the end of the call into the Storage's publishing scope: the scope runs publish now or later, and must not throw
// (the host reports a throw and publishes directly).
func (h *handler) publishingAnswered(outcome uint8, payload []byte) {
	if err := answerErr(outcome, payload); err != nil {
		h.sess.Fail(err)
	}
}

// commitBatches is Session.CommitBatches: it asks the host's Storage to take the batches. The call settles through receive (the host's
// `receive` ops) and done (the hook's answer).
//
//go:noinline
func (h *handler) commitBatches(batches [][]session.Write, nextID session.ID, receive func(int, session.Seq), done func(session.CommitResult, error)) {
	h.call = commitCall{batches: batches, nextID: nextID, receive: receive, done: done}
	list := make([]any, len(batches))
	for i, writes := range batches {
		items := make([]any, len(writes))
		for j := range writes {
			items[j] = writeValue(&writes[j])
		}
		list[i] = items
	}
	body := jv.NewObject()
	body.Set("name", "storageCommit")
	body.Set("handler", float64(0))
	body.Set("conversationId", float64(0))
	body.Set("taskId", float64(0))
	body.Set("payload", jobj("batches", list))
	h.startEffect(abi.EffectHook, jv.Append(nil, body), &pending{done: h.commitAnswered})
}

// answerErr is the error a hook answer carries, nil for a result.
func answerErr(outcome uint8, payload []byte) error {
	if outcome == abi.OutcomeResult {
		return nil
	}
	value, err := jv.Parse(payload)
	if err != nil {
		return err
	}
	return thrown(value)
}

// commitAnswered is the Storage's answer to the call: {stored, refused?}, or the error it rejected with.
func (h *handler) commitAnswered(outcome uint8, payload []byte) {
	call := h.call
	h.call = commitCall{}
	if call.done == nil {
		return
	}
	if err := answerErr(outcome, payload); err != nil {
		call.done(session.CommitResult{Stored: call.got}, err)
		return
	}
	var result session.CommitResult
	if value, err := jv.Parse(payload); err == nil {
		if o, ok := value.(*jv.Object); ok {
			result.Stored = int(numOf(o, "stored"))
			if refused := objOf(o, "refused"); refused != nil {
				result.Refused = thrown(refused)
			}
			if adoption := objOf(o, "adoption"); adoption != nil {
				result.Adoption, result.AdoptionAt = thrown(objOf(adoption, "error")), int(numOf(adoption, "index"))
			}
		}
	}
	call.done(result, nil)
}

// onReceive is the host's receipt of batch index (api op "receive"): the Storage made it durable at seq. The core stores the batch, as
// the Storage did, in this step. A receipt out of order, or at a sequence other than the one the batch takes, is a disagreement between
// the Storage and the core, which fails the handle.
func (h *handler) onReceive(index int, seq session.Seq) error {
	call := &h.call
	if call.done == nil || index != call.got || index >= len(call.batches) {
		return errors.New("core: a Storage receipt names no batch of the call in flight")
	}
	// The call's nextID, the Session's high-water mark of the IDs its jobs minted, goes with its last batch: the batches before it are
	// applied in steps of their own, and the mirror's bound for the stored entries (mirror_lazy.go releaseApplied) must stay below the IDs
	// the batches after them write.
	var next session.ID
	if index == len(call.batches)-1 {
		next = call.nextID
	}
	got, err := (&adapter{h: h}).store(call.batches[index], next, index > 0)
	if err != nil {
		return err
	}
	if got != seq {
		return errors.New("core: the Storage received a batch at another sequence than the core's store")
	}
	call.got++
	call.receive(index, seq)
	return nil
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

// needPublication returns the *session.NeedPublication err is or wraps.
func needPublication(err error) (*session.NeedPublication, bool) {
	if err == nil {
		return nil, false
	}
	return errors.AsType[*session.NeedPublication](err)
}

// onTouch serves api op "touch" (ABI.md section 5.5): a host-served read that Pi gates first waits, in a read job on the line, for the
// publication of the keys it reads.
func (h *handler) onTouch(id uint32, req *jv.Object) error {
	list, _ := valueOf(req, "keys").([]any)
	keys := make([]string, 0, len(list))
	for _, item := range list {
		o, _ := item.(*jv.Object)
		key, err := touchKey(o)
		if err != nil {
			return err
		}
		keys = append(keys, key)
	}
	h.loop.ReadLine(func() error {
		for _, key := range keys {
			if err := h.sess.Touch(key); err != nil {
				return err
			}
		}
		return nil
	}, func(err error) {
		if err != nil {
			h.apiError(id, err)
			return
		}
		h.apiOK(id, nil)
	})
	return nil
}

// touchKey is the Session key a touch descriptor names.
func touchKey(o *jv.Object) (string, error) {
	switch kind := strOf(o, "kind"); kind {
	case "task":
		return session.TaskKey(session.ID(numOf(o, "id"))), nil
	case "conversation":
		return session.ConversationKey(session.ID(numOf(o, "id"))), nil
	case "entry":
		return session.EntryKey(session.ID(numOf(o, "id"))), nil
	case "submission":
		return session.SubmissionKey(session.ID(numOf(o, "id"))), nil
	case "request":
		return session.RequestKey(session.ID(numOf(o, "conversationId")), strOf(o, "requestId")), nil
	case "entries":
		return session.AppendKey(session.ID(numOf(o, "conversationId"))), nil
	case "all":
		return session.AllKeys, nil
	}
	return "", reject("TypeError", "touch: unknown key kind")
}

// touchClientRead is the wait of a client read on the line for the keys Pi's gated Storage touches before it (keys.ts): a read of what an
// unpublished commit wrote waits for its publication, and the job runs again.
func (h *handler) touchClientRead(op string, req *jv.Object) error {
	switch op {
	case "conversation":
		return h.sess.TouchRead(session.ConversationKey(session.ID(numOf(req, "conversationId"))))
	case "getTask":
		return h.sess.TouchRead(session.TaskKey(session.ID(numOf(req, "taskId"))))
	case "submission":
		return h.sess.TouchRead(session.SubmissionKey(session.ID(numOf(req, "submissionId"))))
	case "entry":
		if err := h.sess.TouchRead(session.ConversationKey(session.ID(numOf(req, "conversationId")))); err != nil {
			return err
		}
		return h.sess.TouchRead(session.EntryKey(session.ID(numOf(req, "entryId"))))
	case "scanEntries":
		return h.sess.TouchEntries(session.ID(numOf(objOf(req, "query"), "conversationId")))
	}
	return nil
}
