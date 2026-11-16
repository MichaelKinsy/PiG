package subprocess

import (
	"slices"
	"testing"
)

// The reports of two failed sends of one run are made at the run's end, in the order the sends were made (agent-session.ts:3552-3555: the catch callbacks
// run in the microtasks after the sending code, in the order the Promises rejected).
func TestSyncRunReportsMakeReportsAtTheEndInCallOrder(t *testing.T) {
	var s syncRunReports
	conn := &Conn{}
	var made []string
	first := s.after(conn, "", "y1")
	second := s.after(conn, "", "y2")
	first(func() { made = append(made, "first") })
	second(func() { made = append(made, "second") })
	if len(made) != 0 {
		t.Fatalf("reports made before the run ended: %v", made)
	}
	// The second send's run ends first in the connection's report order: the earlier send's report still comes first.
	s.endToken(conn, "y2")
	if want := []string{"first", "second"}; !slices.Equal(made, want) {
		t.Fatalf("made %v, want %v", made, want)
	}
	s.endToken(conn, "y1")
	if len(made) != 2 {
		t.Fatalf("a report was made twice: %v", made)
	}
}

// A report of a send whose run ended is made at once, and a send with no boundary has no hook at all.
func TestSyncRunReportsAfterTheEndAreMadeAtOnce(t *testing.T) {
	var s syncRunReports
	conn := &Conn{}
	if s.after(conn, "", "") != nil {
		t.Fatal("a send outside any request and with no token got a hook")
	}
	hook := s.after(conn, "", "y1")
	s.endToken(conn, "y1")
	made := 0
	hook(func() { made++ })
	if made != 1 {
		t.Fatalf("made %d times", made)
	}
}

// A connection that ends makes the reports it holds, in order, and no other connection's.
func TestSyncRunReportsClosedMakesTheConnectionsReports(t *testing.T) {
	var s syncRunReports
	conn, other := &Conn{}, &Conn{}
	var made []string
	s.after(conn, "", "y1")(func() { made = append(made, "a") })
	s.after(other, "", "y2")(func() { made = append(made, "other") })
	s.after(conn, "", "y3")(func() { made = append(made, "b") })
	s.closed(conn)
	if want := []string{"a", "b"}; !slices.Equal(made, want) {
		t.Fatalf("made %v, want %v", made, want)
	}
	s.endToken(other, "y2")
	if want := []string{"a", "b", "other"}; !slices.Equal(made, want) {
		t.Fatalf("made %v, want %v", made, want)
	}
}

// The host wires the connection's end to the reports it holds: a send whose run never ends (its runtime exited before the microtask checkpoint it
// would have reported) still gets its report, as Pi's catch callback would run before the process ends.
func TestHostMakesHeldReportsWhenTheConnectionEnds(t *testing.T) {
	h := &Host{}
	conn := &Conn{}
	h.observeXref(conn)
	made := 0
	h.syncReports.after(conn, "", "y1")(func() { made++ })
	if made != 0 {
		t.Fatal("the report was made before the connection ended")
	}
	conn.onClosed()
	if made != 1 {
		t.Fatalf("made %d reports after the connection ended, want 1", made)
	}
}
