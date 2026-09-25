package tui

// undo_stack.go: generic undo stack.
//
// Ports upstream packages/tui/src/undo-stack.ts.

// UndoStack stores detached state snapshots for undo/redo style flows.
type UndoStack[S any] struct {
	stack []S
}

// Push stores a snapshot.
func (u *UndoStack[S]) Push(state S) {
	u.stack = append(u.stack, state)
}

// Pop returns the most recent snapshot.
func (u *UndoStack[S]) Pop() (S, bool) {
	var zero S
	if len(u.stack) == 0 {
		return zero, false
	}
	last := u.stack[len(u.stack)-1]
	u.stack = u.stack[:len(u.stack)-1]
	return last, true
}

// Clear removes all snapshots.
func (u *UndoStack[S]) Clear() {
	u.stack = nil
}

// Len reports the number of snapshots.
func (u *UndoStack[S]) Len() int { return len(u.stack) }
