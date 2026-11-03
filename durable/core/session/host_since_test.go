package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// hostDocFixture is a Session with one conversation document whose value is {"n": 0}.
type hostDocFixture struct {
	t    *testing.T
	s    *session.Session
	def  *session.DocDef
	args session.DocArgs
}

func newHostDocFixture(t *testing.T) *hostDocFixture {
	t.Helper()
	mem := sessiontest.New()
	f := &hostDocFixture{t: t, s: newSession(mem, mem.NextID()), def: &session.DocDef{Kind: "t.note", Version: 1, Scope: session.ScopeConversation, History: session.HistoryLatest,
		Initial: func(any) *jv.Object { return jv.NewObject() }}}
	f.commit(func(tx *session.Tx) error {
		rec, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		id, _ := rec.Get("id")
		f.args = session.DocArgs{Owner: session.ID(id.(float64))}
		draft, err := tx.Doc(f.def, f.args)
		if err != nil {
			return err
		}
		return draft.Set("n", 0.0)
	})
	return f
}

func (f *hostDocFixture) commit(body func(tx *session.Tx) error) {
	f.t.Helper()
	if _, err := f.s.Commit(body, session.TxScope{}); err != nil {
		f.t.Fatal(err)
	}
}

// write is a commit in which the host sets n.
func (f *hostDocFixture) write(n float64) {
	f.t.Helper()
	f.commit(func(tx *session.Tx) error {
		if _, err := tx.HostDoc(f.def, f.args, session.DocRevision{}, false); err != nil {
			return err
		}
		return tx.SetDocOps(f.def, f.args, -1, []delta.Op{{Verb: delta.Set, Path: delta.Path{delta.Key("n")}, Value: n}})
	})
}

// read is HostDoc in a commit that changes nothing.
func (f *hostDocFixture) read(have session.DocRevision) (host session.HostDocValue) {
	f.t.Helper()
	f.commit(func(tx *session.Tx) error {
		var err error
		host, err = tx.HostDoc(f.def, f.args, have, true)
		return err
	})
	return host
}

// A host that holds a revision of a document gets the operations adopted since, which take its value to the present one;
// a host that holds none gets the value and the revision it is at.
func TestHostDocGivesTheOperationsSinceAHeldRevision(t *testing.T) {
	f := newHostDocFixture(t)
	first := f.read(session.DocRevision{})
	if first.Incremental || !first.Tracked || jv.Stringify(first.Value) != `{"n":0}` {
		t.Fatalf("a host holding nothing got %+v", first)
	}
	f.write(1)
	f.write(2)
	second := f.read(first.Revision)
	if !second.Incremental || !second.Tracked || second.Revision.Epoch != first.Revision.Epoch || second.Revision.Adopted != first.Revision.Adopted+2 {
		t.Fatalf("a host holding revision %+v got %+v", first.Revision, second)
	}
	if got := jv.Stringify(delta.OpsValue(second.Ops)); got != `[["s",["n"],1],["s",["n"],2]]` {
		t.Fatalf("operations %s", got)
	}
	value, err := delta.Apply(first.Value, second.Ops)
	if err != nil || jv.Stringify(value) != `{"n":2}` {
		t.Fatalf("held value with the operations: %s, %v", jv.Stringify(value), err)
	}
	if again := f.read(second.Revision); !again.Incremental || len(again.Ops) != 0 || again.Revision != second.Revision {
		t.Fatalf("a host holding the present revision got %+v", again)
	}
}

// What the core cannot continue the host from comes whole: a revision of another incarnation, one older than the history
// kept, and a document the transaction itself edited (its draft has changes no revision names).
func TestHostDocGivesTheValueWhenItCannotContinueFromTheHeldRevision(t *testing.T) {
	f := newHostDocFixture(t)
	held := f.read(session.DocRevision{}).Revision
	if other := f.read(session.DocRevision{Epoch: held.Epoch + 1, Adopted: held.Adopted}); other.Incremental || !other.Tracked {
		t.Fatalf("a revision of another incarnation got %+v", other)
	}
	f.commit(func(tx *session.Tx) error {
		draft, err := tx.Doc(f.def, f.args)
		if err != nil {
			return err
		}
		if err := draft.Set("core", "edit"); err != nil {
			return err
		}
		host, err := tx.HostDoc(f.def, f.args, held, true)
		if err != nil {
			return err
		}
		if host.Incremental || host.Tracked || jv.Stringify(host.Value) != `{"n":0,"core":"edit"}` {
			t.Errorf("a document the transaction edited came as %+v", host)
		}
		return nil
	})
	for n := range 600 {
		f.write(float64(n))
	}
	if far := f.read(held); far.Incremental || !far.Tracked || jv.Stringify(far.Value) != `{"n":599,"core":"edit"}` {
		t.Fatalf("a revision older than the history got %+v", far)
	}
	if near := f.read(session.DocRevision{Epoch: held.Epoch, Adopted: f.read(held).Revision.Adopted - 3}); !near.Incremental || len(near.Ops) != 3 {
		t.Fatalf("a revision three changes old got %+v", near)
	}
}
