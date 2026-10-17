package session_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// coldStore answers a Conversation read with NeedRead until it is warmed, as a
// host-backed store does for state it has not loaded.
type coldStore struct {
	*sessiontest.MemStore
	cold  map[session.ID]bool
	reads int
}

func (c *coldStore) Conversation(id session.ID) (*jv.Object, error) {
	if c.cold[id] {
		c.reads++
		return nil, &session.NeedRead{Reads: []session.ReadRequest{{Kind: "conversation", Args: []any{float64(id)}}}}
	}
	return c.MemStore.Conversation(id)
}

func newSession(store session.Store, next session.ID) *session.Session {
	return session.New(store, next, func() float64 { return 1000 })
}

func draft(kind string) *jv.Object {
	o := jv.NewObject()
	o.Set("kind", kind)
	return o
}

func TestNeedReadRestartsWithTheSameIDs(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	if _, err := s.Commit(func(tx *session.Tx) error { _, err := tx.CreateRootConversation(); return err }, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	cold := &coldStore{MemStore: mem, cold: map[session.ID]bool{1: true}}
	s2 := newSession(cold, mem.NextID())
	run := func() (*session.Publication, error) {
		return s2.Commit(func(tx *session.Tx) error {
			// The first entry mints an ID before the read that needs the host.
			if _, err := tx.AppendEntry(1, draft("a")); err != nil {
				return err
			}
			_, err := tx.AppendEntry(1, draft("b"))
			return err
		}, session.TxScope{})
	}
	before := s2.NextID()
	_, err := run()
	need, ok := session.IsNeedRead(err)
	if !ok || len(need.Reads) != 1 {
		t.Fatalf("want NeedRead, got %v", err)
	}
	if s2.NextID() != before {
		t.Fatalf("a restartable callback must not consume IDs: next %d, was %d", s2.NextID(), before)
	}
	cold.cold[1] = false
	pub, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(pub.Changes[0].Record); got != `{"kind":"a","id":2,"conversationId":1}` {
		t.Fatalf("restarted callback must mint the same IDs: %s", got)
	}
}

func TestFailedCallbackLeavesAnIDGap(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	boom := errors.New("boom")
	_, err := s.Commit(func(tx *session.Tx) error {
		if _, err := tx.CreateRootConversation(); err != nil {
			return err
		}
		if _, err := tx.AppendEntry(1, draft("x")); err != nil { // mints 2
			return err
		}
		return boom
	}, session.TxScope{})
	if !errors.Is(err, boom) || len(mem.Commits) != 0 {
		t.Fatalf("a failed callback commits nothing: %v, %d commits", err, len(mem.Commits))
	}
	if s.NextID() != 3 {
		t.Fatalf("IDs minted by a rolled-back callback stay consumed, as in Pi: next %d", s.NextID())
	}
}

func TestEmptyCommitWritesNothing(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	pub, err := s.Commit(func(tx *session.Tx) error { _, err := tx.Conversation(1); return err }, session.TxScope{})
	if err != nil || pub != nil || mem.NextSeq() != 1 {
		t.Fatalf("empty commit: pub=%v err=%v seq=%d", pub, err, mem.NextSeq())
	}
}

func TestReadAfterWriteIsTyped(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	_, err := s.Commit(func(tx *session.Tx) error {
		if _, err := tx.CreateRootConversation(); err != nil {
			return err
		}
		_, err := tx.Conversation(1)
		return err
	}, session.TxScope{})
	if !errors.Is(err, session.ErrReadAfterWrite) || err.Error() != "Tx.conversation() cannot read tables after the first table write" {
		t.Fatalf("got %v", err)
	}
}

type failingStore struct {
	*sessiontest.MemStore
	err error
}

func (f failingStore) Commit([]session.Write, session.ID) (session.Seq, error) { return 0, f.err }

// Pi eba84973 session.ts GuardedStorage: a commit is never exempt, so every failed commit fails the Session, a rejected
// one too, with the first error as the cause of the SessionFailed every later call gets.
func TestEveryFailedCommitFailsTheSession(t *testing.T) {
	create := func(tx *session.Tx) error { _, err := tx.CreateRootConversation(); return err }
	mem := sessiontest.New()
	for _, cause := range []error{errors.New("disk"), errors.New("Document copy 5 was rejected")} {
		broken := newSession(failingStore{mem, cause}, 2)
		if _, err := broken.Commit(create, session.TxScope{}); !errors.Is(err, cause) || !errors.Is(broken.Failure(), cause) {
			t.Fatalf("the failed commit returns %v, failure %v, want %v", err, broken.Failure(), cause)
		}
		_, err := broken.Commit(create, session.TxScope{})
		var failed *session.FailedError
		if !errors.Is(err, session.ErrSessionFailed) || !errors.As(err, &failed) || !errors.Is(failed.Cause, cause) || err.Error() != "Session failed after a storage error; close and reopen it" {
			t.Fatalf("a failed Session refuses commits with SessionFailed caused by %v: %v", cause, err)
		}
	}
}

func TestSealedTransactionRejectsUse(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	var leaked *session.Tx
	if _, err := s.Commit(func(tx *session.Tx) error { leaked = tx; return nil }, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if _, err := leaked.CreateConversation(session.Ownership{}); !errors.Is(err, session.ErrSealed) {
		t.Fatalf("got %v", err)
	}
}
