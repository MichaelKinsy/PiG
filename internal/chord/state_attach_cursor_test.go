package chord

import (
	"strings"
	"testing"
)

// cursorAttachment hands out a snapshot with a chosen cursor and records disposal.
type cursorAttachment struct {
	cursor   int
	disposed *bool
}

func (a cursorAttachment) Snapshot() ReplicatedStateSourceSnapshot {
	return ReplicatedStateSourceSnapshot{Value: map[string]any{"n": 0.0}, Cursor: a.cursor}
}
func (cursorAttachment) Activate(func(ReplicatedStateSourceFrame)) {}
func (a cursorAttachment) Dispose()                                { *a.disposed = true }

type cursorSource struct {
	cursor   int
	disposed *bool
}

func (s cursorSource) Attach() ReplicatedStateSourceAttachment {
	return cursorAttachment(s)
}

// TestAttachReplicatedStateRejectsAnUnsafeSnapshotCursor ports state.ts:255-256 and :432-435: the attachment's snapshot cursor must be a safe
// integer, otherwise attaching throws TypeError "Replicated state source snapshot cursor must be a safe integer" and, as for any failure
// inside the try (state.ts:324-341), disposes the attachment. The safe-integer bounds themselves are accepted.
func TestAttachReplicatedStateRejectsAnUnsafeSnapshotCursor(t *testing.T) {
	for _, cursor := range []int{maxSafeInteger + 1, -maxSafeInteger - 1} {
		var disposed bool
		state, err := AttachReplicatedState[docState](cursorSource{cursor: cursor, disposed: &disposed}, ReplicatedStateSourceOptions{})
		if state != nil || err == nil || !strings.Contains(err.Error(), "Replicated state source snapshot cursor must be a safe integer") {
			t.Errorf("cursor %d: state=%v err=%v, want the safe-integer error", cursor, state, err)
		}
		if !disposed {
			t.Errorf("cursor %d: the rejected attachment was not disposed", cursor)
		}
	}
	for _, cursor := range []int{maxSafeInteger, -maxSafeInteger} {
		var disposed bool
		state, err := AttachReplicatedState[docState](cursorSource{cursor: cursor, disposed: &disposed}, ReplicatedStateSourceOptions{})
		if err != nil || state == nil || disposed {
			t.Errorf("cursor %d: state=%v err=%v disposed=%v, want an attached state", cursor, state, err, disposed)
		}
	}
}
