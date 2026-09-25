package tui

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
