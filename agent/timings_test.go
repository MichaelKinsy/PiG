package agent

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecorderTurnRoundTrip(t *testing.T) {
	r := NewRecorder()
	r.StartTurn()
	time.Sleep(2 * time.Millisecond)
	d := r.EndTurn()
	if d <= 0 {
		t.Fatalf("EndTurn returned non-positive %v", d)
	}
	snap := r.Snapshot()
	if len(snap.Turns) != 1 {
		t.Fatalf("Snapshot.Turns len = %d, want 1", len(snap.Turns))
	}
	if snap.Turns[0] != d {
		t.Fatalf("Snapshot.Turns[0]=%v want %v", snap.Turns[0], d)
	}
	if snap.CurrentTurn != 0 {
		t.Fatalf("CurrentTurn = %v after EndTurn, want 0", snap.CurrentTurn)
	}
}

func TestRecorderEndTurnWithoutStart(t *testing.T) {
	r := NewRecorder()
	if d := r.EndTurn(); d != 0 {
		t.Fatalf("EndTurn without StartTurn = %v, want 0", d)
	}
}

func TestRecorderRecordTool(t *testing.T) {
	r := NewRecorder()
	r.RecordTool("bash", 10*time.Millisecond)
	r.RecordTool("bash", 5*time.Millisecond)
	r.RecordTool("read", 3*time.Millisecond)
	snap := r.Snapshot()
	if got, want := snap.ToolTotals["bash"], 15*time.Millisecond; got != want {
		t.Fatalf("bash total = %v want %v", got, want)
	}
	if got, want := snap.ToolCounts["bash"], 2; got != want {
		t.Fatalf("bash count = %d want %d", got, want)
	}
	if got, want := snap.ToolCounts["read"], 1; got != want {
		t.Fatalf("read count = %d want %d", got, want)
	}
}

func TestRecorderConcurrent(t *testing.T) {
	r := NewRecorder()
	const N = 200
	var wg sync.WaitGroup
	var hits int64
	for range N {
		wg.Go(func() {
			r.RecordTool("bash", time.Millisecond)
			atomic.AddInt64(&hits, 1)
		})
	}
	wg.Wait()
	snap := r.Snapshot()
	if int64(snap.ToolCounts["bash"]) != hits {
		t.Fatalf("count=%d hits=%d", snap.ToolCounts["bash"], hits)
	}
	if snap.ToolTotals["bash"] != time.Duration(N)*time.Millisecond {
		t.Fatalf("totals=%v want %v", snap.ToolTotals["bash"], time.Duration(N)*time.Millisecond)
	}
}

func TestRecorderElapsedMonotonic(t *testing.T) {
	r := NewRecorder()
	a := r.Elapsed()
	time.Sleep(2 * time.Millisecond)
	b := r.Elapsed()
	if b <= a {
		t.Fatalf("Elapsed not monotonic: a=%v b=%v", a, b)
	}
}

func TestRecorderCurrentTurnElapsed(t *testing.T) {
	r := NewRecorder()
	if r.CurrentTurnElapsed() != 0 {
		t.Fatal("CurrentTurnElapsed before StartTurn must be 0")
	}
	r.StartTurn()
	time.Sleep(1 * time.Millisecond)
	if r.CurrentTurnElapsed() <= 0 {
		t.Fatal("CurrentTurnElapsed during turn must be > 0")
	}
	r.EndTurn()
	if r.CurrentTurnElapsed() != 0 {
		t.Fatal("CurrentTurnElapsed after EndTurn must be 0")
	}
}

func TestRecorderLastTurnDuration(t *testing.T) {
	r := NewRecorder()
	// Before any turn: zero.
	if d := r.LastTurnDuration(); d != 0 {
		t.Fatalf("LastTurnDuration before turns = %v, want 0", d)
	}
	// After one completed turn.
	r.StartTurn()
	time.Sleep(2 * time.Millisecond)
	r.EndTurn()
	d1 := r.LastTurnDuration()
	if d1 <= 0 {
		t.Fatalf("LastTurnDuration after one turn = %v, want > 0", d1)
	}
	// Second turn: LastTurnDuration returns the second turn's duration.
	r.StartTurn()
	time.Sleep(2 * time.Millisecond)
	r.EndTurn()
	d2 := r.LastTurnDuration()
	if d2 <= 0 {
		t.Fatalf("LastTurnDuration after two turns = %v, want > 0", d2)
	}
	// Value should be stable (not incrementing like Elapsed).
	snap := r.LastTurnDuration()
	time.Sleep(5 * time.Millisecond)
	if r.LastTurnDuration() != snap {
		t.Error("LastTurnDuration is not a frozen snapshot: it incremented after call")
	}
}
