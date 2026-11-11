package views_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/viewstest"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports apps/p2/test/p2/views-text.test.ts (Pi main 682c7b93c): a text room's model (plugins/views/text.ts) and its client half (plugins/
// views/kit/text-client.ts): edits by ID, the drop rules, stored documents checked before use, and two editors converging through
// one server order. The inputs are Pi's.

func ids(prefix string) func(*views.ElementID, int64) views.ElementID {
	n := int64(0)
	return func(_ *views.ElementID, count int64) views.ElementID {
		id := views.ElementID{BunchID: prefix + strconv.FormatInt(n, 10)}
		n += count
		return id
	}
}

func textID(id views.ElementID) *views.TextID {
	return &views.TextID{BunchID: id.BunchID, Counter: float64(id.Counter)}
}

func idAt(t *testing.T, doc *views.TextDoc, index int) views.TextID {
	t.Helper()
	id, err := doc.IDs.At(index)
	if err != nil {
		t.Fatal(err)
	}
	return *textID(id)
}

func insertEdit(before *views.TextID, bunch string, counter float64, content string, inWord bool) views.TextEdit {
	return views.TextEdit{Insert: true, Before: before, ID: views.TextID{BunchID: bunch, Counter: counter}, Content: content, InWord: inWord}
}

// server applies batches in arrival order, as the room does, and keeps the log.
type server struct {
	doc *views.TextDoc
	log []logged
}

type logged struct {
	editor string
	n      int
	edits  []views.TextEdit
}

func newServer(text string) *server { return &server{doc: views.FreshText(text, "base")} }

func (s *server) receive(editor string, n int, edits []views.TextEdit) {
	s.doc = views.ApplyEdits(s.doc, edits)
	s.log = append(s.log, logged{editor, n, edits})
}

func TestTextModelInsertsAfterAnIDAndDeletesARangeByIDs(t *testing.T) {
	doc := views.FreshText("hello", "b")
	inserted := views.ApplyEdit(doc, insertEdit(&views.TextID{BunchID: idAt(t, doc, 4).BunchID, Counter: idAt(t, doc, 4).Counter}, "x", 0, " world", false))
	if inserted.String() != "hello world" {
		t.Fatalf("inserted = %q", inserted.String())
	}
	deleted := views.ApplyEdit(inserted, views.TextEdit{Start: idAt(t, inserted, 0), End: idAt(t, inserted, 5), Length: 6})
	if deleted.String() != "world" || deleted.IDs.Length() != 5 {
		t.Fatalf("deleted = %q (%d ids)", deleted.String(), deleted.IDs.Length())
	}
}

func TestTextModelDropsInsertsAtUnknownPlacesWithTakenOrOverlappingIDsBadCountersOrPastTheLimit(t *testing.T) {
	doc := views.FreshText("abc", "b")
	insert := func(before *views.TextID, bunch string, counter float64, content string) *views.TextDoc {
		return views.ApplyEdit(doc, insertEdit(before, bunch, counter, content, false))
	}
	same := func(name string, got *views.TextDoc) {
		t.Helper()
		if got != doc {
			t.Errorf("%s: the insert was taken", name)
		}
	}
	same("unknown before", insert(&views.TextID{BunchID: "nope"}, "x", 0, "z"))
	same("a taken ID", insert(nil, "b", 1, "z"))
	// (b, 9) is free but its second character would be (b, 10): with (b, 10) known, the whole insert is dropped.
	known := views.ApplyEdit(doc, insertEdit(nil, "b", 10, "q", false))
	if known == doc {
		t.Fatal("(b, 10) was not taken")
	}
	if got := views.ApplyEdit(known, insertEdit(nil, "b", 9, "zz", false)); got != known {
		t.Error("an overlapping range was taken")
	}
	same("a negative counter", insert(nil, "x", -1, "z"))
	same("a fractional counter", insert(nil, "x", 1.5, "z"))
	same("a range past safe integers", insert(nil, "x", 9007199254740991, "zz"))
	same("past the limit", insert(nil, "x", 0, strings.Repeat("z", views.MaxText)))
}

func TestTextModelDropsADeleteWhoseRangeGrewByMoreThanTenCharactersAndAnInWordInsertAfterADeletedCharacter(t *testing.T) {
	doc := views.FreshText("abcdef", "b")
	grown := views.ApplyEdit(doc, insertEdit(textID(views.ElementID{BunchID: "b", Counter: 2}), "x", 0, "0123456789AB", false))
	if got := views.ApplyEdit(grown, views.TextEdit{Start: idAt(t, doc, 1), End: idAt(t, doc, 4), Length: 4}); got != grown {
		t.Error("a grown delete was taken")
	}
	cut := views.ApplyEdit(doc, views.TextEdit{Start: idAt(t, doc, 2), End: idAt(t, doc, 2), Length: 1})
	if got := views.ApplyEdit(cut, insertEdit(textID(views.ElementID{BunchID: "b", Counter: 2}), "y", 0, "Z", true)); got != cut {
		t.Error("an in-word insert after a deleted character was taken")
	}
}

func TestTextModelLoadsOnlyValidStoredDocuments(t *testing.T) {
	doc := views.FreshText("hello", "b")
	saved := views.SaveText(doc, views.TextMarks{"e1": 3})
	loaded, _, ok := views.LoadText(saved)
	if !ok || loaded.String() != "hello" {
		t.Fatal("a saved document does not load")
	}
	// Unchanged text with other IDs: the count no longer matches.
	other, _ := views.NewIdList().InsertAfter(nil, views.ElementID{BunchID: "z"}, 3)
	fewer := views.SaveText(&views.TextDoc{Text: doc.Text, IDs: other}, views.TextMarks{"e1": 3})
	if _, _, ok := views.LoadText(fewer); ok {
		t.Error("IDs that do not number the characters loaded")
	}
	over := saved.Clone()
	over.Set("text", strings.Repeat("x", views.MaxText+1))
	if _, _, ok := views.LoadText(over); ok {
		t.Error("a text over the limit loaded")
	}
	if _, _, ok := views.LoadText(delta.JsonObjectOf("text", "a", "textIds", delta.JsonObjectOf("bunchIds", []any{"b"}, "signedCounts", []any{"x"}), "textMarks", delta.NewJsonObject(0))); ok {
		t.Error("a non-numeric count loaded")
	}
	duplicate := delta.JsonObjectOf("text", "ab", "textIds", delta.JsonObjectOf(
		"bunchIds", []any{"b", "b"}, "signedCounts", []any{1.0, 1.0}, "startCounterDeltas", []any{1.0}, "startCounterValues", []any{0.0},
	), "textMarks", delta.NewJsonObject(0))
	if _, _, ok := views.LoadText(duplicate); ok {
		t.Error("duplicate IDs loaded")
	}
	bad := saved.Clone()
	bad.Set("textMarks", delta.JsonObjectOf("e1", -1.0))
	if _, _, ok := views.LoadText(bad); ok {
		t.Error("a bad watermark loaded")
	}
	if _, _, ok := views.LoadText(nil); ok {
		t.Error("null loaded")
	}
}

func TestTextModelComputesTheEditsBetweenTwoTexts(t *testing.T) {
	doc := views.FreshText("the cat sat", "b")
	edits := views.EditsTo(doc, "the dog sat", ids("n"))
	if got := views.ApplyEdits(doc, edits).String(); got != "the dog sat" {
		t.Fatalf("applied = %q", got)
	}
	if len(edits) != 2 || edits[0].Insert || !edits[1].Insert {
		t.Fatalf("edits = %+v, want a delete then an insert", edits)
	}
}

// converge runs a and b's inputs concurrently against one server; both clients end equal to the server's text.
func converge(t *testing.T, start, a, b string) string {
	t.Helper()
	s := newServer(start)
	left := viewstest.NewTextClient("A", s.doc, 0)
	right := viewstest.NewTextClient("B", s.doc, 0)
	fromA := left.Input(a)
	fromB := right.Input(b)
	if fromB != nil {
		s.receive("B", fromB.N, fromB.Edits)
	}
	if fromA != nil {
		s.receive("A", fromA.N, fromA.Edits)
	}
	for _, entry := range s.log {
		left.Confirm(entry.editor, entry.n, entry.edits)
		right.Confirm(entry.editor, entry.n, entry.edits)
	}
	if len(left.Pending()) != 0 || len(right.Pending()) != 0 {
		t.Fatal("batches stayed pending")
	}
	if left.Doc().String() != s.doc.String() || right.Doc().String() != s.doc.String() {
		t.Fatalf("clients %q and %q, server %q", left.Doc().String(), right.Doc().String(), s.doc.String())
	}
	return s.doc.String()
}

func TestTextModelKeepsBothOfTwoConcurrentInsertsAtOneSpot(t *testing.T) {
	if text := converge(t, "ab", "aXb", "aYb"); text != "aXYb" && text != "aYXb" {
		t.Fatal(text)
	}
}

func TestTextModelDeletesAnothersInsertInsideADeletedRange(t *testing.T) {
	if text := converge(t, "abcdef", "af", "abcZdef"); text != "af" {
		t.Fatal(text)
	}
}

func TestTextModelReplaysPendingEditsOverOthersConfirmedOnes(t *testing.T) {
	s := newServer("one two")
	left := viewstest.NewTextClient("A", s.doc, 0)
	right := viewstest.NewTextClient("B", s.doc, 0)
	first := right.Input("one two three")
	if first == nil {
		t.Fatal("no batch")
	}
	s.receive("B", first.N, first.Edits)
	left.Input("zero one two")
	// A sees B's edit while its own is pending: its text shows both.
	left.Confirm("B", first.N, first.Edits)
	if got := left.Doc().String(); got != "zero one two three" {
		t.Fatal(got)
	}
}

func TestTextModelLeavesPendingAResetsBaseAlreadyHolds(t *testing.T) {
	s := newServer("x")
	client := viewstest.NewTextClient("A", s.doc, 0)
	batch := client.Input("xy")
	if batch == nil {
		t.Fatal("no batch")
	}
	s.receive("A", batch.N, batch.Edits)
	client.Reset(s.doc, batch.N)
	if len(client.Pending()) != 0 || client.Doc().String() != "xy" {
		t.Fatalf("pending %d, text %q", len(client.Pending()), client.Doc().String())
	}
}

// The saved form of a list is articulated's compact one: bunches, signed counts (negative: deleted), and the start counters that
// do not follow from the previous run of a bunch.
func TestIdListSavesAndLoadsAsArticulatedDoes(t *testing.T) {
	list, _ := views.NewIdList().InsertAfter(nil, views.ElementID{BunchID: "b"}, 5)
	list, _ = list.InsertAfter(&views.ElementID{BunchID: "b", Counter: 1}, views.ElementID{BunchID: "x"}, 2)
	list, err := list.DeleteRange(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	// Order b0 b1 x0 x1 b2 b3 b4; b1 and x0 deleted.
	saved := list.SaveCompact()
	got := fmt.Sprint(saved.BunchIDs, saved.SignedCounts, saved.StartCounterDeltas, saved.StartCounterValues)
	if want := "[b b x x b] [1 -1 -1 1 3] [] []"; got != want {
		t.Fatalf("saved %s, want %s", got, want)
	}
	loaded, err := views.LoadCompactIdList(saved)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Length() != list.Length() || loaded.Length() != 5 {
		t.Fatalf("loaded %d ids, saved %d", loaded.Length(), list.Length())
	}
	for i := range list.Length() {
		a, _ := list.At(i)
		b, _ := loaded.At(i)
		if a != b {
			t.Fatalf("id %d: %v, loaded %v", i, a, b)
		}
	}
	// A deleted ID answers by its bias: left of it, right of it.
	left, _ := list.IndexOf(views.ElementID{BunchID: "b", Counter: 1}, views.BiasLeft)
	right, _ := list.IndexOf(views.ElementID{BunchID: "b", Counter: 1}, views.BiasRight)
	if left != 0 || right != 1 {
		t.Fatalf("bias left %d right %d, want 0 and 1", left, right)
	}
}
