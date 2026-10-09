package durable

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
)

// types.ts:727-730 TableCommitChange is Extract<StorageWrite, conversation | entry | task | submission>: the four table writes are commit changes that keep their StorageWrite discriminators, and no document write is one.
func TestTableCommitChangeIsTheFourTableWrites(t *testing.T) {
	for _, tc := range []struct {
		change TableCommitChange
		want   string
	}{
		{ConversationWrite{}, "conversation"},
		{EntryWrite{}, "entry"},
		{TaskWrite{}, "task"},
		{SubmissionWrite{}, "submission"},
	} {
		if got := CommitChangeType(tc.change); got != tc.want {
			t.Errorf("%T: commit change type %q, want %q", tc.change, got, tc.want)
		}
		if got := StorageWriteType(tc.change); got != tc.want {
			t.Errorf("%T: storage write type %q, want %q", tc.change, got, tc.want)
		}
	}
	tableType := reflect.TypeFor[TableCommitChange]()
	for _, document := range []any{DocumentCreateWrite{}, DocumentCopyWrite{}, DocumentChangeWrite{}, DocumentRetireWrite{}} {
		if reflect.TypeOf(document).Implements(tableType) {
			t.Errorf("%T must not be a TableCommitChange", document)
		}
	}
}

// types.ts:705-725 DocumentCommitChange is "document" (a record change) or "document.copy" (a definition-free copy of a DocumentCopySource, types.ts:671-674); both are CommitChanges, and neither is a table change.
func TestDocumentCommitChangeKindsAndCopySource(t *testing.T) {
	source := DocumentCopySource{Id: 7, At: AtSeq(3)}
	for _, tc := range []struct {
		change DocumentCommitChange
		want   string
	}{
		{DocumentChange{}, "document"},
		{DocumentCopyChange{Source: source}, "document.copy"},
	} {
		if got := CommitChangeType(tc.change); got != tc.want {
			t.Errorf("%T: type %q, want %q", tc.change, got, tc.want)
		}
	}
	copied := DocumentCopyChange{Source: source}
	if copied.Source.Id != 7 || copied.Source.At.Current || copied.Source.At.Seq != 3 {
		t.Fatalf("copy source = %+v", copied.Source)
	}
	if write := (DocumentCopyWrite{Source: source}); write.Source != source {
		t.Fatal("a copy write names the same DocumentCopySource")
	}
	if !CurrentPoint.Current {
		t.Fatal("CurrentPoint selects the current state")
	}
	tableType := reflect.TypeFor[TableCommitChange]()
	for _, change := range []any{DocumentChange{}, DocumentCopyChange{}} {
		if reflect.TypeOf(change).Implements(tableType) {
			t.Errorf("%T must not be a TableCommitChange", change)
		}
	}
}

// types.ts:842 DocumentState<T> is the read-only Chord state attached to one committed document incarnation: its Go form is the attached replicated state of the document's JSON object.
func TestDocumentStateIsTheAttachedReplicatedStateOfAJSONObject(t *testing.T) {
	if got, want := reflect.TypeFor[DocumentState[JsonObject]](), reflect.TypeFor[*chord.AttachedReplicatedState[JsonObject]](); got != want {
		t.Fatalf("DocumentState = %v, want %v", got, want)
	}
	var state DocumentState[JsonObject]
	if state != nil {
		t.Fatal("an unattached state is nil")
	}
}
