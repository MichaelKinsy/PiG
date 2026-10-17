package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// A transaction the host drives (Begin, its operations, Finish) is timed by the awaits its operations charged, as RunCommit's callback is: the job's duration
// is Pi's change callback, not an estimate from the Storage reads and mints the core made.
func TestAHostDrivenTransactionTakesItsTicksFromItsOperations(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.DocDef{Kind: "t.note", Version: 1, Scope: session.ScopeSession, History: session.HistoryLatest,
		Initial: func(any) *jv.Object { return jv.NewObject() }}
	tx, err := s.Begin(session.TxScope{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Doc(def, session.DocArgs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finish(tx); err != nil {
		t.Fatal(err)
	}
	if got := s.TakeTicks(); got <= 0 {
		t.Fatalf("the finished transaction left %d ticks, want the awaits of its tx.doc", got)
	}
}
