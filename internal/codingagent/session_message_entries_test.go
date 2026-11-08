package codingagent

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// An extension process asks for the message entries after a cursor; the cursor counts every pair ever noted, so it survives the oldest pairs dropping off, and a cursor from another session starts over.
func TestMessageEntriesAfterACursor(t *testing.T) {
	var s Session
	if pairs, next := s.MessageEntriesAfter(0); pairs != nil || next != 0 {
		t.Fatalf("an empty log gave %v, %d", pairs, next)
	}
	s.NoteMessageEntry("e1", 11)
	s.NoteMessageEntry("e2", 12)
	s.NoteMessageEntry("skipped", 0)
	s.NoteMessageEntry("", 13)
	pairs, next := s.MessageEntriesAfter(0)
	if want := []sessionentry.MessageSeq{{ID: "e1", Seq: 11}, {ID: "e2", Seq: 12}}; !reflect.DeepEqual(pairs, want) || next != 2 {
		t.Fatalf("pairs = %v (cursor %d), want %v (cursor 2): an entry with no id or number is not recorded", pairs, next, want)
	}
	if pairs, next := s.MessageEntriesAfter(2); pairs != nil || next != 2 {
		t.Errorf("a reader at the end got %v, %d", pairs, next)
	}
	if pairs, next := s.MessageEntriesAfter(1); !reflect.DeepEqual(pairs, []sessionentry.MessageSeq{{ID: "e2", Seq: 12}}) || next != 2 {
		t.Errorf("a reader at 1 got %v, %d", pairs, next)
	}
	if pairs, next := s.MessageEntriesAfter(99); len(pairs) != 2 || next != 2 {
		t.Errorf("a cursor beyond the log (another session's) got %v, %d, want the pairs held", pairs, next)
	}

	for i := 3; i <= messageEntryLimit+10; i++ {
		s.NoteMessageEntry(fmt.Sprintf("e%d", i), uint64(10+i))
	}
	pairs, next = s.MessageEntriesAfter(0)
	if len(pairs) != messageEntryLimit || next != messageEntryLimit+10 {
		t.Fatalf("%d pairs held (cursor %d), want %d (cursor %d)", len(pairs), next, messageEntryLimit, messageEntryLimit+10)
	}
	if pairs[0].ID != "e11" || pairs[len(pairs)-1].ID != fmt.Sprintf("e%d", messageEntryLimit+10) {
		t.Errorf("held pairs run %s to %s, want the newest %d", pairs[0].ID, pairs[len(pairs)-1].ID, messageEntryLimit)
	}
	// A reader behind the oldest pair held gets what is held.
	if pairs, next := s.MessageEntriesAfter(1); len(pairs) != messageEntryLimit || next != messageEntryLimit+10 {
		t.Errorf("a reader behind the bound got %d pairs (cursor %d)", len(pairs), next)
	}
}
