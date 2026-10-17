package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// A host's tracker continues from the document's present content, so a document the core edited in the same
// transaction (the built-in creation hook's agent copy) keeps those edits when the host also writes it.
func TestHostDocContinuesFromTheCoresOwnEdits(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.DocDef{Kind: "t.note", Version: 1, Scope: session.ScopeConversation, History: session.HistoryLatest,
		Initial: func(any) *jv.Object { return jv.NewObject() }}
	var seen string
	pub, err := s.Commit(func(tx *session.Tx) error {
		rec, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		id, _ := rec.Get("id")
		args := session.DocArgs{Owner: session.ID(id.(float64))}
		draft, err := tx.Doc(def, args)
		if err != nil {
			return err
		}
		if err := draft.Set("core", "edit"); err != nil {
			return err
		}
		value, _, _, err := tx.HostDoc(def, args)
		if err != nil {
			return err
		}
		seen = jv.Stringify(value)
		return tx.SetDocOps(def, args, -1, []delta.Op{{Verb: delta.Set, Path: delta.Path{delta.Key("host")}, Value: "write"}})
	}, session.TxScope{})
	if err != nil {
		t.Fatal(err)
	}
	if seen != `{"core":"edit"}` {
		t.Fatalf("the host's draft starts from %s, not the core's edit", seen)
	}
	for _, c := range pub.Changes {
		if c.Kind != session.ChangeTable && c.Value != nil {
			if got := jv.Stringify(c.Value); got != `{"core":"edit","host":"write"}` {
				t.Fatalf("committed %s", got)
			}
			return
		}
	}
	t.Fatal("no document change published")
}

// Loaded is session.ts snapshot's cache check: a document becomes loaded once a snapshot read or a commit adopted it, and
// a reader of a loaded document needs no job on the Session line (runtime.agent() in a generation's prepare).
func TestLoadedReportsTheSnapshotCache(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.DocDef{Kind: "t.note", Version: 1, Scope: session.ScopeConversation, History: session.HistoryLatest,
		Initial: func(any) *jv.Object { return jv.NewObject() }}
	var args session.DocArgs
	if _, err := s.Commit(func(tx *session.Tx) error {
		rec, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		id, _ := rec.Get("id")
		args = session.DocArgs{Owner: session.ID(id.(float64))}
		draft, err := tx.Doc(def, args)
		if err != nil {
			return err
		}
		return draft.Set("k", "v")
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	s.UnloadDocuments()
	if s.Loaded(def, args) {
		t.Fatal("an unloaded document reports loaded")
	}
	if _, err := s.Snapshot(def, args); err != nil {
		t.Fatal(err)
	}
	if !s.Loaded(def, args) {
		t.Fatal("a snapshot read did not load the document")
	}
	if s.Loaded(&session.DocDef{Kind: "t.note", Version: 2, Scope: session.ScopeConversation, History: session.HistoryLatest}, args) {
		t.Fatal("another definition version reports loaded")
	}
}

// A retired incarnation's draft outlives the new incarnation that takes its address (transaction.ts retireDoc: the draft
// stays open until settlement), so a host names the incarnation a write belongs to by its DocEntry.
func TestHostDocWritesNameTheirIncarnation(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.DocDef{Kind: "t.note", Version: 1, Scope: session.ScopeConversation, History: session.HistoryLatest,
		Initial: func(any) *jv.Object { return jv.NewObject() }}
	var args session.DocArgs
	if _, err := s.Commit(func(tx *session.Tx) error {
		rec, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		id, _ := rec.Get("id")
		args = session.DocArgs{Owner: session.ID(id.(float64))}
		draft, err := tx.Doc(def, args)
		if err != nil {
			return err
		}
		return draft.Set("k", "v")
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	pub, err := s.Commit(func(tx *session.Tx) error {
		if _, _, _, err := tx.HostDoc(def, args); err != nil {
			return err
		}
		old := tx.DocEntry(def, args)
		if err := tx.RetireDoc(def, args); err != nil {
			return err
		}
		fresh := *def
		fresh.HostInitial = func(any) (*jv.Object, error) { return jv.NewObject(), nil }
		if _, _, _, err := tx.HostDoc(&fresh, args); err != nil {
			return err
		}
		replacement := tx.DocEntry(&fresh, args)
		if old != 0 || replacement != 1 {
			t.Errorf("entries %d and %d, want 0 and 1", old, replacement)
		}
		if err := tx.SetDocOps(def, args, old, []delta.Op{{Verb: delta.Set, Path: delta.Path{delta.Key("k")}, Value: "final"}}); err != nil {
			return err
		}
		return tx.SetDocOps(&fresh, args, replacement, []delta.Op{{Verb: delta.Set, Path: delta.Path{delta.Key("k")}, Value: "new"}})
	}, session.TxScope{})
	if err != nil {
		t.Fatal(err)
	}
	var docs []string
	for _, c := range pub.Changes {
		if c.Kind != session.ChangeDocument {
			continue
		}
		if c.Value == nil {
			docs = append(docs, "null")
		} else {
			docs = append(docs, jv.Stringify(c.Value))
		}
	}
	if len(docs) != 2 || docs[0] != "null" || docs[1] != `{"k":"new"}` {
		t.Fatalf("published %v", docs)
	}
}

// Pi sets the snapshot cache only when a line job's load resumes (session.ts snapshot, #loadDocument), so a caller that asks
// while the load's job is still running or finishing queues a job of its own instead of taking the cached path ahead of the
// callers queued before it (three parallel tool tasks asking runtime.agent() on a cold document: H-W4d).
func TestLoadedIsFalseForADocumentTheRunningJobLoaded(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.DocDef{Kind: "t.note", Version: 1, Scope: session.ScopeConversation, History: session.HistoryLatest,
		Initial: func(any) *jv.Object { return jv.NewObject() }}
	var args session.DocArgs
	if _, err := s.Commit(func(tx *session.Tx) error {
		rec, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		id, _ := rec.Get("id")
		args = session.DocArgs{Owner: session.ID(id.(float64))}
		draft, err := tx.Doc(def, args)
		if err != nil {
			return err
		}
		return draft.Set("k", "v")
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	s.UnloadDocuments()
	loop := &session.Loop{Session: s}
	var during, after bool
	loop.Line(func() error {
		_, err := s.Snapshot(def, args)
		during = s.Loaded(def, args)
		return err
	}, func(error) { after = s.Loaded(def, args) })
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if during {
		t.Fatal("a document the running job loaded reports loaded before the job publishes")
	}
	if !after || !s.Loaded(def, args) {
		t.Fatal("the document is not loaded once the job finished")
	}
}
