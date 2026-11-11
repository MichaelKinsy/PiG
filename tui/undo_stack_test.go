package tui

// pi: packages/tui/src/undo-stack.ts

import "testing"

func TestUndoStackPushPopClear(t *testing.T) {
	var s UndoStack[int]
	if s.Len() != 0 {
		t.Fatalf("initial Len = %d want 0", s.Len())
	}
	s.Push(1)
	s.Push(2)
	if s.Len() != 2 {
		t.Fatalf("Len after pushes = %d want 2", s.Len())
	}
	if got, ok := s.Pop(); !ok || got != 2 {
		t.Fatalf("first Pop = (%d,%v) want (2,true)", got, ok)
	}
	if got, ok := s.Pop(); !ok || got != 1 {
		t.Fatalf("second Pop = (%d,%v) want (1,true)", got, ok)
	}
	if _, ok := s.Pop(); ok {
		t.Fatal("expected empty Pop to return ok=false")
	}
	s.Push(3)
	s.Clear()
	if s.Len() != 0 {
		t.Fatalf("Len after Clear = %d want 0", s.Len())
	}
}

// Upstream stores a clone of every pushed state ("clone-on-push") and returns snapshots most recent first. A struct value is the Go
// clone: mutating the caller's copy after Push must not change the stored snapshot, and Pop returns snapshots in LIFO order.
func TestUndoStackSnapshotsAreDetachedAndLIFO(t *testing.T) {
	type state struct {
		Lines  [2]string
		Cursor int
	}
	var s UndoStack[state]
	live := state{Lines: [2]string{"hello", ""}, Cursor: 5}
	s.Push(live)
	live.Lines[0] = "hello world"
	live.Cursor = 11
	s.Push(live)
	live.Lines[0] = "mutated after push"

	got, ok := s.Pop()
	if !ok || got.Lines[0] != "hello world" || got.Cursor != 11 {
		t.Fatalf("first Pop = (%+v,%v), want the second pushed snapshot", got, ok)
	}
	got, ok = s.Pop()
	if !ok || got.Lines[0] != "hello" || got.Cursor != 5 {
		t.Fatalf("second Pop = (%+v,%v), want the first pushed snapshot", got, ok)
	}
	if got, ok = s.Pop(); ok {
		t.Fatalf("Pop on an empty stack = (%+v,true), want ok=false", got)
	}
}

// Clear empties the stack, and a cleared stack accepts new snapshots.
func TestUndoStackClearThenReuse(t *testing.T) {
	var s UndoStack[string]
	s.Push("a")
	s.Push("b")
	s.Clear()
	if _, ok := s.Pop(); ok || s.Len() != 0 {
		t.Fatalf("after Clear: Len=%d, Pop ok=%v; want empty", s.Len(), ok)
	}
	s.Push("c")
	if got, ok := s.Pop(); !ok || got != "c" {
		t.Fatalf("Pop after reuse = (%q,%v), want (c,true)", got, ok)
	}
}
