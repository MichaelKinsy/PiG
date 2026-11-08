//go:build !race

package codingagent

import "testing"

// A session persists every message, so noting the entry of one must not copy the pairs it holds: a full log takes one more without allocating.
func TestNoteMessageEntryDoesNotAllocateOnceFull(t *testing.T) {
	var s Session
	for i := range messageEntryLimit + 3 {
		s.NoteMessageEntry("e", uint64(1+i))
	}
	seq := uint64(messageEntryLimit + 10)
	if allocs := testing.AllocsPerRun(1000, func() {
		seq++
		s.NoteMessageEntry("e", seq)
	}); allocs != 0 {
		t.Errorf("NoteMessageEntry on a full log allocated %v times per call, want 0", allocs)
	}
	// And a message that was never numbered (no Node extension) names nothing and costs nothing.
	if allocs := testing.AllocsPerRun(1000, func() { s.NoteMessageEntry("e", 0) }); allocs != 0 {
		t.Errorf("an unnumbered message allocated %v times, want 0", allocs)
	}
}
