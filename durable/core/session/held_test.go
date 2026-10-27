// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session_test

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// upstream: packages/durable/test/session-deferred-commits.test.ts on the core Session (held.go): what a deferred commit holds in
// memory, what stores it, and what the Storage and each kind of listener then sees. The cases keep the upstream names; the
// facade-only ones (read-only Session, withinMs TypeError, observeCommits) run in the contract's U-session-deferred-commits.

func heldIDOf(rec *jv.Object) float64 { id, _ := rec.Get("id"); return id.(float64) }

func heldDef(version int) *session.DocDef {
	def := &session.DocDef{
		Kind: "test.deferred.text", Version: version, Scope: session.ScopeConversation,
		History: session.HistoryLatest, Fork: session.ForkInitial,
		Initial: func(any) *jv.Object { o := jv.NewObject(); o.Set("text", ""); return o },
		CheckpointWhen: func(value *jv.Object, _ []delta.Op, _ int) bool {
			base, _ := value.Get("base")
			return base == true
		},
	}
	if version > 1 {
		def.Migrate = func(value *jv.Object, _ int) *jv.Object {
			out := jv.Clone(value).(*jv.Object)
			out.Set("migrated", true)
			return out
		}
	}
	return def
}

var (
	heldText = heldDef(1)
	// A latest, current-fork document and a session document: a deferred commit that writes them is stored at once.
	heldCurrentFork = &session.DocDef{Kind: "test.deferred.current-fork", Version: 1, Scope: session.ScopeConversation, History: session.HistoryLatest, Fork: session.ForkCurrent,
		Initial: func(any) *jv.Object { o := jv.NewObject(); o.Set("text", ""); return o }}
	heldSessionDoc = &session.DocDef{Kind: "test.deferred.session", Version: 1, Scope: session.ScopeSession,
		Initial: func(any) *jv.Object { o := jv.NewObject(); o.Set("text", ""); return o }}
)

type heldTimer struct {
	at       float64
	fire     func()
	disarmed bool
	fired    bool
}

type heldRig struct {
	t       *testing.T
	s       *session.Session
	store   *sessiontest.MemStore
	clock   float64
	timers  []*heldTimer
	jobs    []func() error
	conv    session.ID
	stored  []*session.Publication
	seen    []*session.Publication
	checked int
}

func newHeldRig(t *testing.T, defaultWithin *float64) *heldRig {
	t.Helper()
	store := sessiontest.New()
	rig := &heldRig{t: t, store: store, clock: 1000}
	rig.s = session.New(store, store.NextID(), func() float64 { return rig.clock })
	rig.s.PersistWithin = func() *float64 { return defaultWithin }
	rig.s.ArmForeground = func(at float64, fire func()) func() {
		timer := &heldTimer{at: at, fire: fire}
		rig.timers = append(rig.timers, timer)
		return func() { timer.disarmed = true }
	}
	rig.s.EnqueueSweep = func(job func() error) { rig.jobs = append(rig.jobs, job) }
	retention := 600_000.0
	rig.s.DocumentRetention = func() *float64 { return &retention }
	rig.s.SubscribeCommits(func(pub *session.Publication) {
		rig.seen = append(rig.seen, pub)
		if !pub.Memory {
			rig.stored = append(rig.stored, pub)
		}
	})
	if _, err := rig.s.Commit(func(tx *session.Tx) error {
		rec, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		rig.conv = session.ID(heldIDOf(rec))
		return nil
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	rig.commit(session.TxScope{}, func(tx *session.Tx) error { _, err := tx.Doc(heldText, session.DocArgs{Owner: rig.conv}); return err })
	return rig
}

func (r *heldRig) commit(scope session.TxScope, change func(tx *session.Tx) error) error {
	r.t.Helper()
	_, err := r.s.Commit(change, scope)
	return err
}

func (r *heldRig) mustCommit(scope session.TxScope, change func(tx *session.Tx) error) {
	r.t.Helper()
	if err := r.commit(scope, change); err != nil {
		r.t.Fatal(err)
	}
}

var (
	heldDeferred  = session.TxScope{Deferred: true, WithinMs: -1}
	heldImmediate = session.TxScope{}
)

func heldWithin(ms float64) session.TxScope { return session.TxScope{Deferred: true, WithinMs: ms} }

func (r *heldRig) append(text string, scope session.TxScope) {
	r.t.Helper()
	r.mustCommit(scope, func(tx *session.Tx) error {
		draft, err := tx.Doc(heldText, session.DocArgs{Owner: r.conv})
		if err != nil {
			return err
		}
		current, _ := draft.Get("text").(string)
		return draft.Set("text", current+text)
	})
}

func (r *heldRig) recordID() session.ID {
	r.t.Helper()
	rec, err := r.store.FindDocument(session.Address{Kind: heldText.Kind, Scope: session.Scope{Kind: session.ScopeConversation, Owner: r.conv}}, session.Current)
	if err != nil || rec == nil {
		r.t.Fatalf("document record: %v %v", rec, err)
	}
	id, _ := rec.Get("id")
	return session.ID(id.(float64))
}

func (r *heldRig) storedText() string {
	r.t.Helper()
	doc, err := r.store.Document(r.recordID(), session.Current)
	if err != nil || doc == nil {
		r.t.Fatalf("stored document: %v %v", doc, err)
	}
	text, _ := doc.Value.Get("text")
	return text.(string)
}

func (r *heldRig) memoryText() string {
	r.t.Helper()
	value, err := r.s.Snapshot(heldText, session.DocArgs{Owner: r.conv})
	if err != nil {
		r.t.Fatal(err)
	}
	text, _ := value.Get("text")
	return text.(string)
}

func (r *heldRig) armed() []*heldTimer {
	var out []*heldTimer
	for _, timer := range r.timers {
		if !timer.disarmed && !timer.fired {
			out = append(out, timer)
		}
	}
	return out
}

// fire fires every armed timer and runs the line jobs they queued.
func (r *heldRig) fire() {
	r.t.Helper()
	for _, timer := range r.timers {
		if timer.disarmed || timer.fired {
			continue
		}
		timer.fired = true
		timer.fire()
	}
	for len(r.jobs) > 0 {
		job := r.jobs[0]
		r.jobs = r.jobs[1:]
		if err := job(); err != nil {
			r.t.Fatal(err)
		}
	}
}

func (r *heldRig) batches() int { return len(r.store.Commits) }

func (r *heldRig) docWrites(from int) []session.Write {
	var out []session.Write
	for _, c := range r.store.Commits[from:] {
		for _, w := range c.Writes {
			if w.Kind == session.WriteDocChange {
				out = append(out, w)
			}
		}
	}
	return out
}

func TestHoldingADeferredCommit(t *testing.T) {
	t.Run("adopts a commit that only changes an existing latest, initial-fork conversation document in memory, unstored", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		admitted, publications := rig.batches(), len(rig.stored)
		rig.append("a", heldDeferred)
		rig.append("b", heldDeferred)
		if rig.batches() != admitted || len(rig.stored) != publications {
			t.Fatalf("a held commit reached Storage: %d batches, %d publications", rig.batches()-admitted, len(rig.stored)-publications)
		}
		if got := rig.memoryText(); got != "ab" {
			t.Fatalf("memory %q", got)
		}
		if got := rig.storedText(); got != "" {
			t.Fatalf("Storage %q", got)
		}
		if rig.s.HeldDocuments() != 1 {
			t.Fatalf("held %d", rig.s.HeldDocuments())
		}
		last := rig.seen[len(rig.seen)-2:]
		if !last[0].Memory || !last[1].Memory {
			t.Fatalf("observers saw %+v", last)
		}
	})

	t.Run("stores a deferred commit at once when it writes anything else", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		stores := func(change func(tx *session.Tx) error) bool {
			t.Helper()
			admitted := rig.batches()
			rig.mustCommit(heldDeferred, change)
			return rig.batches() > admitted
		}
		conv := session.DocArgs{Owner: rig.conv}
		for _, tc := range []struct {
			name   string
			change func(tx *session.Tx) error
		}{
			{"an entry next to the held document's change", func(tx *session.Tx) error {
				draft, err := tx.Doc(heldText, conv)
				if err != nil {
					return err
				}
				if err := draft.Set("text", "x"); err != nil {
					return err
				}
				_, err = tx.AppendEntry(rig.conv, entryDraft("test.note"))
				return err
			}},
			{"a creation", func(tx *session.Tx) error { _, err := tx.Doc(heldCurrentFork, conv); return err }},
			{"a session document", func(tx *session.Tx) error {
				draft, err := tx.Doc(heldSessionDoc, session.DocArgs{})
				if err != nil {
					return err
				}
				return draft.Set("text", "s")
			}},
			{"a held document next to an immediate one", func(tx *session.Tx) error {
				draft, err := tx.Doc(heldText, conv)
				if err != nil {
					return err
				}
				if err := draft.Set("text", "y"); err != nil {
					return err
				}
				other, err := tx.Doc(heldSessionDoc, session.DocArgs{})
				if err != nil {
					return err
				}
				return other.Set("text", "ss")
			}},
			{"a current-fork document's change", func(tx *session.Tx) error {
				draft, err := tx.Doc(heldCurrentFork, conv)
				if err != nil {
					return err
				}
				return draft.Set("text", "c")
			}},
		} {
			if !stores(tc.change) {
				t.Fatalf("%s was held", tc.name)
			}
		}
		if err := rig.commit(heldDeferred, func(tx *session.Tx) error { return tx.RetireDoc(heldCurrentFork, conv) }); err != nil {
			t.Fatal(err)
		}
		if rig.s.HeldDocuments() != 0 {
			t.Fatalf("held %d", rig.s.HeldDocuments())
		}
		// The control: the same change alone is held.
		admitted := rig.batches()
		rig.append("z", heldDeferred)
		if rig.batches() != admitted {
			t.Fatalf("the control was stored")
		}
	})

	t.Run("stores a deferred commit that migrates its document at once, as a base", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		from := rig.batches()
		rig.mustCommit(heldDeferred, func(tx *session.Tx) error {
			draft, err := tx.Doc(heldDef(2), session.DocArgs{Owner: rig.conv})
			if err != nil {
				return err
			}
			return draft.Set("text", "v2")
		})
		writes := rig.docWrites(from)
		if len(writes) != 1 || !writes[0].Base || rig.s.HeldDocuments() != 0 {
			t.Fatalf("writes %+v, held %d", writes, rig.s.HeldDocuments())
		}
	})
}

func entryDraft(kind string) *jv.Object {
	o := jv.NewObject()
	o.Set("kind", kind)
	return o
}

func TestStoringHeldChanges(t *testing.T) {
	t.Run("stores them first, folded with its own, in a commit that writes the document", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		rig.append("a", heldDeferred)
		rig.append("b", heldDeferred)
		from := rig.batches()
		rig.append("c", heldImmediate)
		if rig.batches() != from+1 {
			t.Fatalf("%d batches", rig.batches()-from)
		}
		writes := rig.docWrites(from)
		if len(writes) != 1 || writes[0].Base || len(writes[0].Ops) != 1 || writes[0].Ops[0].Verb != delta.Append || writes[0].Ops[0].Text != "abc" {
			t.Fatalf("writes %+v", writes)
		}
		if rig.storedText() != "abc" || rig.s.HeldDocuments() != 0 {
			t.Fatalf("stored %q held %d", rig.storedText(), rig.s.HeldDocuments())
		}
	})

	t.Run("stores them in a commit that only reads the document, with its other writes", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		rig.append("held", heldDeferred)
		from := rig.batches()
		rig.mustCommit(heldImmediate, func(tx *session.Tx) error {
			if _, err := tx.Doc(heldText, session.DocArgs{Owner: rig.conv}); err != nil {
				return err
			}
			_, err := tx.AppendEntry(rig.conv, entryDraft("test.derived"))
			return err
		})
		if rig.batches() != from+1 || rig.storedText() != "held" {
			t.Fatalf("batches %d stored %q", rig.batches()-from, rig.storedText())
		}
		kinds := map[session.WriteKind]int{}
		for _, w := range rig.store.Commits[from].Writes {
			kinds[w.Kind]++
		}
		if kinds[session.WriteDocChange] != 1 || kinds[session.WriteEntry] != 1 || len(rig.store.Commits[from].Writes) != 2 {
			t.Fatalf("writes %v", kinds)
		}
	})

	t.Run("leaves them held through commits that neither read nor write the document, or write nothing", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		rig.append("held", heldDeferred)
		from := rig.batches()
		rig.mustCommit(heldImmediate, func(tx *session.Tx) error {
			_, err := tx.AppendEntry(rig.conv, entryDraft("test.unrelated"))
			return err
		})
		rig.mustCommit(heldImmediate, func(tx *session.Tx) error { _, err := tx.Doc(heldText, session.DocArgs{Owner: rig.conv}); return err })
		if len(rig.docWrites(from)) != 0 || rig.s.HeldDocuments() != 1 {
			t.Fatalf("doc writes %d held %d", len(rig.docWrites(from)), rig.s.HeldDocuments())
		}
	})

	t.Run("keeps them when a commit that reads the document fails before Storage", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		rig.append("held", heldDeferred)
		err := rig.commit(heldImmediate, func(tx *session.Tx) error {
			draft, err := tx.Doc(heldText, session.DocArgs{Owner: rig.conv})
			if err != nil {
				return err
			}
			if err := draft.Set("text", "held!"); err != nil {
				return err
			}
			if _, err := tx.AppendEntry(rig.conv, entryDraft("test.note")); err != nil {
				return err
			}
			return errors.New("callback failed")
		})
		if err == nil || err.Error() != "callback failed" {
			t.Fatalf("%v", err)
		}
		if rig.s.HeldDocuments() != 1 {
			t.Fatalf("held %d", rig.s.HeldDocuments())
		}
		rig.fire()
		if rig.storedText() != "held" {
			t.Fatalf("stored %q", rig.storedText())
		}
	})

	t.Run("stores every held document in one transaction when the earliest deadline comes", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		var other session.ID
		rig.mustCommit(heldImmediate, func(tx *session.Tx) error {
			rec, err := tx.CreateConversation(session.Ownership{})
			if err != nil {
				return err
			}
			other = session.ID(heldIDOf(rec))
			_, err = tx.Doc(heldText, session.DocArgs{Owner: other})
			return err
		})
		rig.append("one", heldDeferred)
		rig.mustCommit(heldDeferred, func(tx *session.Tx) error {
			draft, err := tx.Doc(heldText, session.DocArgs{Owner: other})
			if err != nil {
				return err
			}
			return draft.Set("text", "two")
		})
		if armed := rig.armed(); len(armed) != 1 || armed[0].at != 2000 {
			t.Fatalf("armed %+v", armed)
		}
		publications, from := len(rig.stored), rig.batches()
		rig.fire()
		if rig.batches() != from+1 || len(rig.docWrites(from)) != 2 || len(rig.stored) != publications+1 {
			t.Fatalf("batches %d, doc writes %d, publications %d", rig.batches()-from, len(rig.docWrites(from)), len(rig.stored)-publications)
		}
		if rig.s.HeldDocuments() != 0 || len(rig.armed()) != 0 {
			t.Fatalf("held %d armed %d", rig.s.HeldDocuments(), len(rig.armed()))
		}
		if last := rig.stored[len(rig.stored)-1]; len(last.Changes) != 0 || len(last.Stored) != 2 {
			t.Fatalf("the timer's store showed observers %d changes", len(last.Changes))
		}
	})

	t.Run("arms the earliest deadline of the held commits, each with its withinMs or the default", func(t *testing.T) {
		within := 2000.0
		rig := newHeldRig(t, &within)
		rig.append("a", heldDeferred)
		if armed := rig.armed(); len(armed) != 1 || armed[0].at != 3000 {
			t.Fatalf("armed %+v", armed)
		}
		rig.clock += 500
		// Earlier: the timer moves up.
		rig.append("b", heldWithin(100))
		if armed := rig.armed(); len(armed) != 1 || armed[0].at != 1600 {
			t.Fatalf("armed %+v", armed)
		}
		// Later: the deadline stays.
		rig.append("c", heldWithin(60_000))
		if armed := rig.armed(); len(armed) != 1 || armed[0].at != 1600 {
			t.Fatalf("armed %+v", armed)
		}
	})

	t.Run("arms no timer for an infinite window, and moves the timer on when a commit stores the held document", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		rig.append("a", heldWithin(math.MaxFloat64))
		if len(rig.armed()) != 0 || rig.s.HeldDocuments() != 1 {
			t.Fatalf("armed %d held %d", len(rig.armed()), rig.s.HeldDocuments())
		}
		rig.append("b", heldDeferred)
		if armed := rig.armed(); len(armed) != 1 || armed[0].at != 2000 {
			t.Fatalf("armed %+v", armed)
		}
		rig.append("c", heldImmediate)
		if len(rig.armed()) != 0 || rig.s.HeldDocuments() != 0 {
			t.Fatalf("armed %d held %d", len(rig.armed()), rig.s.HeldDocuments())
		}
	})

	t.Run("stores them before UnloadDocuments drops the trackers", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		rig.append("held", heldDeferred)
		if err := rig.s.UnloadDocuments(); err != nil {
			t.Fatal(err)
		}
		if cached, _ := rig.s.DocumentCacheSizes(); rig.storedText() != "held" || cached != 0 || rig.s.HeldDocuments() != 0 {
			t.Fatalf("stored %q cached %d held %d", rig.storedText(), cached, rig.s.HeldDocuments())
		}
		if got := rig.memoryText(); got != "held" {
			t.Fatalf("reloaded %q", got)
		}
	})

	t.Run("stores them before a token of another definition version reloads the document", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		rig.append("held", heldDeferred)
		from := rig.batches()
		value, err := rig.s.Snapshot(heldDef(2), session.DocArgs{Owner: rig.conv})
		if err != nil {
			t.Fatal(err)
		}
		if text, _ := value.Get("text"); text != "held" {
			t.Fatalf("migrated %s", jv.Stringify(value))
		}
		if writes := rig.docWrites(from); len(writes) != 1 || writes[0].Base || rig.storedText() != "held" {
			t.Fatalf("writes %+v stored %q", writes, rig.storedText())
		}
	})

	t.Run("stores them before the document's retirement, in the retiring commit", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		rig.append("held", heldDeferred)
		from := rig.batches()
		rig.mustCommit(heldImmediate, func(tx *session.Tx) error { return tx.RetireDoc(heldText, session.DocArgs{Owner: rig.conv}) })
		writes := rig.store.Commits[from].Writes
		if len(writes) != 2 || writes[0].Kind != session.WriteDocChange || writes[1].Kind != session.WriteDocRetire {
			t.Fatalf("writes %+v", writes)
		}
		if len(writes[0].Ops) != 1 || writes[0].Ops[0].Text != "held" || rig.s.HeldDocuments() != 0 {
			t.Fatalf("held ops %+v, held %d", writes[0].Ops, rig.s.HeldDocuments())
		}
	})
}

func TestBasesAndOverflowOfHeldChanges(t *testing.T) {
	t.Run("stores a base when a held commit selected one, and counts the store as one delta otherwise", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		from := rig.batches()
		rig.mustCommit(heldDeferred, func(tx *session.Tx) error {
			draft, err := tx.Doc(heldText, session.DocArgs{Owner: rig.conv})
			if err != nil {
				return err
			}
			if err := draft.Set("text", "x"); err != nil {
				return err
			}
			return draft.Set("base", true)
		})
		rig.append("y", heldDeferred)
		rig.fire()
		writes := rig.docWrites(from)
		if len(writes) != 1 || !writes[0].Base || jv.Stringify(writes[0].Value) != `{"text":"xy","base":true}` {
			t.Fatalf("writes %+v", writes)
		}
	})

	overflow := func(rig *heldRig) {
		rig.mustCommit(heldImmediate, func(tx *session.Tx) error {
			draft, err := tx.Doc(heldText, session.DocArgs{Owner: rig.conv})
			if err != nil {
				return err
			}
			return draft.Set("keys", jv.NewObject())
		})
		for from := 0; from <= delta.MaxPendingOps; from += 4_000 {
			rig.mustCommit(heldDeferred, func(tx *session.Tx) error {
				draft, err := tx.Doc(heldText, session.DocArgs{Owner: rig.conv})
				if err != nil {
					return err
				}
				keys := draft.Obj("keys")
				for index := from; index < from+4_000; index++ {
					if err := keys.Set("k"+strconv.Itoa(index), float64(index)); err != nil {
						return err
					}
				}
				return nil
			})
		}
	}

	t.Run("stores the whole value once more than MAX_PENDING_OPS operations are held", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		overflow(rig)
		from := rig.batches()
		rig.fire()
		writes := rig.docWrites(from)
		if len(writes) != 1 || !writes[0].Base {
			t.Fatalf("writes %+v", writes)
		}
		last := rig.stored[len(rig.stored)-1].StoredChanges()
		if len(last) != 1 || len(last[0].Ops) != 1 || last[0].Ops[0].Verb != delta.Replace {
			t.Fatalf("a durable listener did not get a replacement: %+v", last)
		}
	})

	t.Run("stores a base of the commit's own value when it stores an overflowed hold", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		overflow(rig)
		rig.append("a", heldDeferred)
		from := rig.batches()
		rig.append("b", heldImmediate)
		writes := rig.docWrites(from)
		if len(writes) != 1 || !writes[0].Base {
			t.Fatalf("writes %+v", writes)
		}
		if text, _ := writes[0].Value.Get("text"); text != "ab" {
			t.Fatalf("base %s", jv.Stringify(writes[0].Value))
		}
	})
}

func TestPublicationsOfDeferredCommits(t *testing.T) {
	t.Run("give SubscribeCommits exactly what was stored, and nothing for a held commit", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		from, publications := rig.batches(), len(rig.stored)
		rig.append("a", heldDeferred)
		rig.append("b", heldDeferred)
		rig.append("c", heldImmediate)
		rig.append("d", heldDeferred)
		rig.fire()
		rig.append("e", heldDeferred)
		rig.mustCommit(heldImmediate, func(tx *session.Tx) error {
			if _, err := tx.Doc(heldText, session.DocArgs{Owner: rig.conv}); err != nil {
				return err
			}
			_, err := tx.AppendEntry(rig.conv, entryDraft("test.note"))
			return err
		})
		rig.append("f", heldDeferred)
		if err := rig.s.StoreHolds(); err != nil {
			t.Fatal(err)
		}
		batches, published := rig.store.Commits[from:], rig.stored[publications:]
		if len(published) != len(batches) {
			t.Fatalf("%d publications for %d batches", len(published), len(batches))
		}
		// A replica built from the publications alone always equals what Storage holds.
		var fromStorage, fromPublications any = jv.NewObject(), jv.NewObject()
		fromStorage.(*jv.Object).Set("text", "")
		fromPublications.(*jv.Object).Set("text", "")
		id := rig.recordID()
		for index, batch := range batches {
			var write *session.Write
			for i := range batch.Writes {
				if batch.Writes[i].Kind == session.WriteDocChange && batch.Writes[i].ID == id {
					write = &batch.Writes[i]
				}
			}
			var change *session.Change
			for _, c := range published[index].StoredChanges() {
				if c.Kind == session.ChangeDocument && c.Record != nil && heldIDOf(c.Record) == float64(id) {
					c := c
					change = &c
				}
			}
			if write == nil || change == nil {
				t.Fatalf("batch %d: write %v change %v", index, write, change)
			}
			var err error
			if write.Base {
				fromStorage = write.Value
			} else if fromStorage, err = delta.Apply(fromStorage, write.Ops); err != nil {
				t.Fatal(err)
			}
			if fromPublications, err = delta.Apply(fromPublications, change.Ops); err != nil {
				t.Fatal(err)
			}
			if jv.Stringify(fromPublications) != jv.Stringify(fromStorage) || jv.Stringify(change.Value) != jv.Stringify(fromStorage) {
				t.Fatalf("batch %d: replica %s, storage %s, value %s", index, jv.Stringify(fromPublications), jv.Stringify(fromStorage), jv.Stringify(change.Value))
			}
		}
		if jv.Stringify(fromStorage) != `{"text":"abcdef"}` {
			t.Fatalf("storage %s", jv.Stringify(fromStorage))
		}
	})

	t.Run("show every held commit to watches and the Session's own observers, once", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		from := len(rig.seen)
		for _, text := range []string{"a", "b", "c"} {
			rig.append(text, heldDeferred)
		}
		rig.append("d", heldImmediate)
		rig.fire()
		seen := rig.seen[from:]
		if len(seen) != 4 || !seen[0].Memory || !seen[1].Memory || !seen[2].Memory || seen[3].Memory {
			t.Fatalf("publications %d", len(seen))
		}
		// The storing commit shows its own operations; the held ones were shown already. The timer showed nothing.
		for index, text := range []string{"a", "b", "c", "d"} {
			if len(seen[index].Changes) != 1 || len(seen[index].Changes[0].Ops) != 1 || seen[index].Changes[0].Ops[0].Text != text {
				t.Fatalf("publication %d: %+v", index, seen[index].Changes)
			}
		}
	})
}

func TestAHeldDocumentIsPinned(t *testing.T) {
	t.Run("keeps a held document cached with retention 0 until it is stored, then lets it go", func(t *testing.T) {
		rig := newHeldRig(t, nil)
		zero := 0.0
		rig.s.DocumentRetention = func() *float64 { return &zero }
		rig.s.Microtask = func(fn func()) { fn() }
		rig.s.ArmTimer = func(float64, func()) func() { return func() {} }
		rig.mustCommit(heldImmediate, func(*session.Tx) error { return nil })
		rig.append("held", heldDeferred)
		cached, idle := rig.s.DocumentCacheSizes()
		if cached != 1 || idle != 0 || rig.s.HeldDocuments() != 1 || rig.s.DocumentsAttached() != 1 {
			t.Fatalf("cached %d idle %d held %d attached %d", cached, idle, rig.s.HeldDocuments(), rig.s.DocumentsAttached())
		}
		rig.fire()
		rig.mustCommit(heldImmediate, func(*session.Tx) error { return nil })
		if cached, _ = rig.s.DocumentCacheSizes(); cached != 0 || rig.s.HeldDocuments() != 0 || rig.s.DocumentsAttached() != 0 {
			t.Fatalf("cached %d held %d attached %d", cached, rig.s.HeldDocuments(), rig.s.DocumentsAttached())
		}
		if got := rig.memoryText(); got != "held" {
			t.Fatalf("reloaded %q", got)
		}
	})
}

func TestACrashLeavesStorageAtTheLastStoredState(t *testing.T) {
	rig := newHeldRig(t, nil)
	rig.append("a", heldDeferred)
	rig.fire()
	rig.append("b", heldDeferred)
	rig.append("c", heldDeferred)
	// The process ends here: Storage holds the last stored state, which memory had.
	if got := rig.storedText(); got != "a" {
		t.Fatalf("stored %q", got)
	}
	reopened := session.New(rig.store, rig.store.NextID(), func() float64 { return 1 })
	value, err := reopened.Snapshot(heldText, session.DocArgs{Owner: rig.conv})
	if err != nil || jv.Stringify(value) != `{"text":"a"}` || reopened.HeldDocuments() != 0 {
		t.Fatalf("reopened %v %v held %d", value, err, reopened.HeldDocuments())
	}
}
