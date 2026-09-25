package tui

import "testing"

func TestKillRing_PeekEmpty(t *testing.T) {
	var k KillRing
	if got := k.Peek(); got != "" {
		t.Errorf("Peek on empty ring: want %q, got %q", "", got)
	}
}

func TestKillRing_PushEmpty(t *testing.T) {
	var k KillRing
	k.Push("", false, false)
	if k.Len() != 0 {
		t.Errorf("pushing empty text must be no-op; Len=%d", k.Len())
	}
}

func TestKillRing_Push_Basic(t *testing.T) {
	var k KillRing
	k.Push("hello", false, false)
	if got := k.Peek(); got != "hello" {
		t.Errorf("Peek after push: want %q, got %q", "hello", got)
	}
	if k.Len() != 1 {
		t.Errorf("Len after one push: want 1, got %d", k.Len())
	}
}

func TestKillRing_Push_Accumulate_Append(t *testing.T) {
	// Forward deletions (prepend=false) should append when accumulating.
	var k KillRing
	k.Push("foo", false, false) // first kill
	k.Push("bar", false, true)  // consecutive forward kill: accumulate
	if got := k.Peek(); got != "foobar" {
		t.Errorf("accumulate append: want %q, got %q", "foobar", got)
	}
	if k.Len() != 1 {
		t.Errorf("accumulate must not grow ring; Len=%d", k.Len())
	}
}

func TestKillRing_Push_Accumulate_Prepend(t *testing.T) {
	// Backward deletions (prepend=true) should prepend when accumulating.
	var k KillRing
	k.Push("world", false, false) // first kill
	k.Push("hello ", true, true)  // consecutive backward kill: prepend
	if got := k.Peek(); got != "hello world" {
		t.Errorf("accumulate prepend: want %q, got %q", "hello world", got)
	}
	if k.Len() != 1 {
		t.Errorf("accumulate must not grow ring; Len=%d", k.Len())
	}
}

func TestKillRing_Push_NoAccumulate_Grows(t *testing.T) {
	var k KillRing
	k.Push("a", false, false)
	k.Push("b", false, false)
	if k.Len() != 2 {
		t.Errorf("two non-accumulate pushes: want Len=2, got %d", k.Len())
	}
}

func TestKillRing_Rotate(t *testing.T) {
	var k KillRing
	k.Push("first", false, false)
	k.Push("second", false, false)
	k.Push("third", false, false)
	// Before rotate, Peek == "third"
	if got := k.Peek(); got != "third" {
		t.Fatalf("before rotate: want %q, got %q", "third", got)
	}
	k.Rotate()
	// After rotate: "third" moved to front; new Peek == "second"
	if got := k.Peek(); got != "second" {
		t.Errorf("after rotate: want %q, got %q", "second", got)
	}
	if k.Len() != 3 {
		t.Errorf("Rotate must not change ring size; Len=%d", k.Len())
	}
}

func TestKillRing_Rotate_Single(t *testing.T) {
	var k KillRing
	k.Push("only", false, false)
	k.Rotate() // no-op on single-entry ring
	if got := k.Peek(); got != "only" {
		t.Errorf("rotate single: want %q, got %q", "only", got)
	}
}

func TestKillRing_LengthAlias(t *testing.T) {
	var k KillRing
	k.Push("one", false, false)
	k.Push("two", false, false)
	if got := k.Length(); got != 2 {
		t.Fatalf("Length() = %d want 2", got)
	}
}
